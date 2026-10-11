package serverfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

const (
	// maxUploadsPerUser bounds parallel uploads of one account (spec §5.1).
	maxUploadsPerUser = 2
	// maxDownloadsPerUser bounds parallel downloads of one account; the service limits folder
	// zips and file downloads in total as well.
	maxDownloadsPerUser = 4
	// maxZipsPerUser bounds the folder zips one account streams at a time: the service streams
	// only a few zips in total (each holds its central directory in memory), and one account
	// must not take them all. Whether a download is a folder is known from the service's
	// answer; a second zip is cancelled right there.
	maxZipsPerUser = 1
)

// userSlots counts running transfers per user.
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

func ignoreUnsupported(err error) error {
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

// errUploadDone is returned by a read of the upload body after the upload was answered.
var errUploadDone = errors.New("the upload was already answered")

// deadlineReader sets a fresh read deadline before every read of the request body: the
// server's ReadTimeout only bounds silence, not the length of an upload.
//
// Once the body has reported io.EOF it never sets a deadline again. When the body hits EOF,
// net/http clears the read deadline and starts a background read that watches the connection
// and cancels the request context if that read fails. The HTTP client of the files service
// (net/http's Transport) reads once more after the announced length to detect extra data; a
// deadline set for that read would apply to the background read and cancel the upload while
// the service is still storing the file. After finish the reader never touches the
// connection again (a late read by a client goroutine fails).
type deadlineReader struct {
	r  io.Reader
	dl func() error // sets a fresh read deadline

	mu      sync.Mutex
	eof     bool
	done    bool
	readErr error // the first error of the body other than io.EOF
}

// stalled reports whether the body ran into its read deadline (the browser stopped sending).
func (d *deadlineReader) stalled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	var ne net.Error
	return errors.Is(d.readErr, os.ErrDeadlineExceeded) || (errors.As(d.readErr, &ne) && ne.Timeout())
}

func (d *deadlineReader) Read(p []byte) (int, error) {
	d.mu.Lock()
	switch {
	case d.done:
		d.mu.Unlock()
		return 0, errUploadDone
	case d.eof:
		d.mu.Unlock()
		return 0, io.EOF
	}
	err := d.dl()
	d.mu.Unlock()
	if err != nil {
		return 0, err
	}
	n, err := d.r.Read(p)
	if err != nil {
		d.mu.Lock()
		if errors.Is(err, io.EOF) {
			d.eof = true
		} else if d.readErr == nil {
			d.readErr = err
		}
		d.mu.Unlock()
	}
	return n, err
}

// finish ends the reader's use of the connection once the upload is answered.
func (d *deadlineReader) finish() {
	d.mu.Lock()
	d.done = true
	d.mu.Unlock()
}

// idleReader cancels the service's download when a read waits longer than timeout for the
// next bytes: a stalled service must not hold the browser's connection forever. The timer
// only runs while the handler waits for the service, not while it writes to the browser
// (the write deadline bounds that).
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

// errDownloadIdle is the cancel cause of a download whose service stopped sending.
var errDownloadIdle = errors.New("the file service sent nothing for too long")

// deadlineWriter sets a fresh write deadline before every write and flushes after it, so the
// browser receives the download as it arrives (decision 2).
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

// parseOverwrite reads the boolean overwrite query parameter: absent = false, otherwise exactly
// one value "true" or "false" (OpenAPI boolean); the files client sends it to the service as 0|1.
func parseOverwrite(q url.Values) (bool, bool) {
	vs, ok := q["overwrite"]
	if !ok {
		return false, true
	}
	if len(vs) != 1 {
		return false, false
	}
	switch vs[0] {
	case "false":
		return false, true
	case "true":
		return true, true
	}
	return false, false
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	p, ok := cleanItemPath(w, r, q.Get("path"))
	if !ok {
		return
	}
	overwrite, ok := parseOverwrite(q)
	if !ok {
		writeValidation(w, r, "overwrite must be true or false")
		return
	}
	if r.ContentLength < 0 {
		writeValidation(w, r, "Content-Length is required")
		return
	}
	pr, _ := principal.From(ctx) // guaranteed by the rbac rule
	release, ok := h.uploads.acquire(pr.UserID)
	if !ok {
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "at most 2 uploads at a time, try again when one is done")
		return
	}
	defer release()
	rc := http.NewResponseController(w)
	body := &deadlineReader{
		r:  io.LimitReader(r.Body, r.ContentLength),
		dl: func() error { return ignoreUnsupported(rc.SetReadDeadline(h.ioClock.Now().Add(h.ioTimeout))) },
		// An empty body is at its end from the start (net/http already runs the background
		// read): a read must not arm a deadline.
		eof: r.ContentLength == 0,
	}
	res, err := h.files.Upload(ctx, s.ID, p, overwrite, r.ContentLength, body)
	body.finish()
	// The answer may come long after the WriteTimeout that started with the request.
	h.extendWrite(w, r)
	h.recordResult(ctx, audit.ActionFileUpload, s.ID, err, map[string]any{"path": p, "size": r.ContentLength})
	if err != nil && body.stalled() {
		// The browser stopped sending: its problem, not an unreachable file service. net/http
		// cancels ctx on the failed read, but the browser may still be waiting for the answer.
		writeValidation(w, r, fmt.Sprintf("the upload stalled (no data for %s)", h.ioTimeout))
		return
	}
	if err != nil {
		h.writeFilesError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rc := http.NewResponseController(w)
	// Safeguard: net/http clears the read deadline itself before it runs a handler for a
	// request without body (its background read then watches the connection, and a closed
	// connection cancels ctx). Clearing it here keeps a long download independent of the
	// server's ReadTimeout even if that behaviour changes.
	if err := ignoreUnsupported(rc.SetReadDeadline(time.Time{})); err != nil {
		h.log.DebugContext(ctx, "clear read deadline", "err", err)
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	p, ok := cleanPath(w, r, r.URL.Query().Get("path")) // "" = the whole data directory as zip
	if !ok {
		return
	}
	pr, _ := principal.From(ctx) // guaranteed by the rbac rule
	release, ok := h.downloads.acquire(pr.UserID)
	if !ok {
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "at most 4 downloads at a time, try again when one is done")
		return
	}
	defer release()
	// dctx ends the service's download when the browser leaves (ctx) or the service stalls.
	dctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	d, err := h.files.Download(dctx, s.ID, p)
	if err != nil {
		// Refused downloads (a missing or invalid path, a symlink, a busy service) are
		// rejections, not audited (spec §5.3); a download that could not be served is.
		if !rejected(err) && !errors.Is(err, filesapi.ErrNotFound) && !errors.Is(err, filesapi.ErrNotRegular) && !errors.Is(err, filesapi.ErrInvalid) {
			h.record(ctx, audit.ActionFileDownload, s.ID, audit.ResultFailure, map[string]any{"path": p})
		}
		h.writeFilesError(w, r, err)
		return
	}
	defer func() { _ = d.Body.Close() }()
	if d.IsDir {
		releaseZip, ok := h.zips.acquire(pr.UserID)
		if !ok {
			// Closing the body (deferred) cancels the zip at the service at once; a rejection,
			// not audited.
			w.Header().Set("Retry-After", "5")
			httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "only one folder download at a time, try again when it is done")
			return
		}
		defer releaseZip()
	}
	dw := &deadlineWriter{
		w:     w,
		dl:    func() error { return ignoreUnsupported(rc.SetWriteDeadline(h.ioClock.Now().Add(h.ioTimeout))) },
		flush: func() error { return ignoreUnsupported(rc.Flush()) },
	}
	if err := dw.dl(); err != nil {
		// Nothing is written yet: answer with an error instead of a 200 with the file's headers
		// and no body. The connection is probably gone; the 500 makes it visible in the log.
		h.record(ctx, audit.ActionFileDownload, s.ID, audit.ResultFailure, map[string]any{"path": p})
		httpx.Internal(w, r, fmt.Errorf("download: set write deadline: %w", err))
		return
	}
	name := d.Name
	if name == "" {
		name = s.Name
	}
	hdr := w.Header()
	if d.IsDir {
		name += ".zip"
		hdr.Set("Content-Type", "application/zip")
	} else {
		hdr.Set("Content-Type", "application/octet-stream")
		if d.Size >= 0 {
			hdr.Set("Content-Length", strconv.FormatInt(d.Size, 10))
		}
	}
	hdr.Set("Content-Disposition", attachment(name))
	w.WriteHeader(http.StatusOK)
	idle := &idleReader{r: d.Body, timer: time.AfterFunc(h.ioTimeout, func() { cancel(errDownloadIdle) }), timeout: h.ioTimeout}
	idle.timer.Stop()
	_, err = io.Copy(dw, idle)
	if err == nil {
		h.record(ctx, audit.ActionFileDownload, s.ID, audit.ResultSuccess, map[string]any{"path": p})
		return
	}
	h.record(ctx, audit.ActionFileDownload, s.ID, audit.ResultFailure, map[string]any{"path": p})
	if errors.Is(context.Cause(dctx), errDownloadIdle) {
		h.log.WarnContext(ctx, "download aborted, the file service stalled", "server_id", s.ID, "path", p)
	} else {
		h.log.DebugContext(ctx, "download aborted", "server_id", s.ID, "path", p, "err", err)
	}
	// The status line is out: end the response without its regular end (no final chunk of a
	// zip stream, a short body for a file), so the browser reports a failed download instead
	// of keeping a truncated file.
	panic(http.ErrAbortHandler)
}

// attachment returns a Content-Disposition value with an ASCII fallback name and the exact
// UTF-8 name (RFC 6266, RFC 5987).
func attachment(name string) string {
	var ascii strings.Builder
	for _, c := range name {
		if c < 0x20 || c > 0x7e || c == '"' || c == '\\' || c == ';' {
			ascii.WriteByte('_')
			continue
		}
		ascii.WriteRune(c)
	}
	return `attachment; filename="` + ascii.String() + `"; filename*=UTF-8''` + rfc5987(name)
}

func rfc5987(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isAttrChar(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

func isAttrChar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	return strings.IndexByte("!#$&+-.^_`|~", c) >= 0
}
