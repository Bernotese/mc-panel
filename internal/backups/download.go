package backups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

const maxDownloadsPerUser = 2

// userSlots counts running downloads per user.
type userSlots struct {
	mu  sync.Mutex
	max int
	n   map[int64]int
}

func newUserSlots(maxPerUser int) *userSlots { return &userSlots{max: maxPerUser, n: map[int64]int{}} }

// acquire takes a slot of userID; release must be called exactly once.
func (u *userSlots) acquire(userID int64) (release func(), ok bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.n[userID] >= u.max {
		return nil, false
	}
	u.n[userID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			u.mu.Lock()
			defer u.mu.Unlock()
			if u.n[userID]--; u.n[userID] <= 0 {
				delete(u.n, userID)
			}
		})
	}, true
}

// ignoreUnsupported comes from handler.go (task 12).

// deadlineWriter sets a fresh write deadline before every write and flushes after it (pattern
// of serverfiles: the server's WriteTimeout does not bound a long download).
type deadlineWriter struct {
	w     io.Writer
	dl    func() error
	flush func() error
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	if err := d.dl(); err != nil {
		return 0, err
	}
	n, err := d.w.Write(p)
	if err != nil {
		return n, err
	}
	return n, d.flush()
}

// idleReader cancels the service's download when a read waits longer than timeout.
type idleReader struct {
	r       io.Reader
	timer   *time.Timer
	timeout time.Duration
}

func (i *idleReader) Read(p []byte) (int, error) {
	i.timer.Reset(i.timeout)
	n, err := i.r.Read(p)
	i.timer.Stop()
	return n, err
}

// contentDisposition names the zip <server>-<YYYYMMDD-HHMMSS>.zip (time in the panel zone).
// Server names are DNS labels, but the header is built by mime.FormatMediaType anyway; should
// it refuse the name, the file is called server-<id>-….zip.
func contentDisposition(srv server, t time.Time) string {
	stamp := t.Format("20060102-150405")
	if v := mime.FormatMediaType("attachment", map[string]string{"filename": fmt.Sprintf("%s-%s.zip", srv.Name, stamp)}); v != "" {
		return v
	}
	return mime.FormatMediaType("attachment", map[string]string{"filename": fmt.Sprintf("server-%d-%s.zip", srv.ID, stamp)})
}

var errDownloadIdle = errors.New("the file service sent nothing for too long")

func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rc := http.NewResponseController(w)
	if err := ignoreUnsupported(rc.SetReadDeadline(time.Time{})); err != nil {
		h.log.DebugContext(ctx, "clear read deadline", "err", err)
	}
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	snap, ok := snapshotParam(w, r)
	if !ok {
		return
	}
	if err := h.ready(ctx, srv, false); err != nil {
		h.writeError(w, r, err)
		return
	}
	s, err := h.findSnapshot(ctx, srv.ID, snap)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	pr, _ := principal.From(ctx) // guaranteed by the rbac rule
	release, ok := h.downloads.acquire(pr.UserID)
	if !ok {
		h.writeError(w, r, errTooManyDownloads)
		return
	}
	defer release()
	dctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	d, err := h.files.DownloadBackup(dctx, srv.ID, snap)
	if errors.Is(err, filesapi.ErrBusy) { // zip slots full or a forget job holds the repository
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "the file service is busy, try again shortly")
		return
	}
	if err != nil {
		if !rejected(err) {
			h.record(ctx, audit.ActionBackupDownload, srv.ID, audit.ResultFailure, map[string]any{"snapshot": snap})
		}
		h.writeError(w, r, err)
		return
	}
	defer func() { _ = d.Body.Close() }()
	dw := &deadlineWriter{
		w:     w,
		dl:    func() error { return ignoreUnsupported(rc.SetWriteDeadline(h.ioClock.Now().Add(h.ioTimeout))) },
		flush: func() error { return ignoreUnsupported(rc.Flush()) },
	}
	if err := dw.dl(); err != nil {
		h.record(ctx, audit.ActionBackupDownload, srv.ID, audit.ResultFailure, map[string]any{"snapshot": snap})
		httpx.Internal(w, r, fmt.Errorf("download: set write deadline: %w", err))
		return
	}
	loc, _, err := h.location(ctx)
	if err != nil {
		loc = time.UTC
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(srv, time.UnixMilli(s.Time).In(loc)))
	w.WriteHeader(http.StatusOK)
	idle := &idleReader{r: d.Body, timer: time.AfterFunc(h.ioTimeout, func() { cancel(errDownloadIdle) }), timeout: h.ioTimeout}
	idle.timer.Stop()
	_, err = io.Copy(dw, idle)
	switch {
	case err == nil:
		h.record(ctx, audit.ActionBackupDownload, srv.ID, audit.ResultSuccess, map[string]any{"snapshot": snap})
		return
	case errors.Is(context.Cause(dctx), errDownloadIdle):
		h.log.WarnContext(ctx, "backup download aborted, the file service stalled", "server_id", srv.ID)
	default:
		h.log.DebugContext(ctx, "backup download aborted", "server_id", srv.ID, "err", err)
	}
	h.record(ctx, audit.ActionBackupDownload, srv.ID, audit.ResultFailure, map[string]any{"snapshot": snap})
	// The status line is out: end the response without its regular end so the browser reports
	// a failed download instead of keeping a truncated zip.
	panic(http.ErrAbortHandler)
}
