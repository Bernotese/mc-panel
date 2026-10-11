package serverfiles

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

// maxUploadsPerUser bounds parallel uploads of one account (spec §5.1).
const maxUploadsPerUser = 2

// uploadSlots counts running uploads per user.
type uploadSlots struct {
	mu sync.Mutex
	n  map[int64]int
}

func newUploadSlots() *uploadSlots { return &uploadSlots{n: map[int64]int{}} }

// acquire takes a slot of userID; release must be called exactly once.
func (u *uploadSlots) acquire(userID int64) (release func(), ok bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.n[userID] >= maxUploadsPerUser {
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

// deadlineReader sets a fresh read deadline before every read of the request body: the
// server's ReadTimeout only bounds silence, not the length of an upload.
type deadlineReader struct {
	r  io.Reader
	dl func() error
}

func (d *deadlineReader) Read(p []byte) (int, error) {
	if err := d.dl(); err != nil {
		return 0, err
	}
	return d.r.Read(p)
}

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
	}
	res, err := h.files.Upload(ctx, s.ID, p, overwrite, r.ContentLength, body)
	// The answer may come long after the WriteTimeout that started with the request.
	if dlErr := ignoreUnsupported(rc.SetWriteDeadline(h.ioClock.Now().Add(h.ioTimeout))); dlErr != nil {
		h.log.DebugContext(ctx, "set write deadline", "err", dlErr)
	}
	h.record(ctx, audit.ActionFileUpload, s.ID, result(err), map[string]any{"path": p, "size": r.ContentLength})
	if err != nil {
		h.writeFilesError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	p, ok := cleanPath(w, r, r.URL.Query().Get("path")) // "" = the whole data directory as zip
	if !ok {
		return
	}
	d, err := h.files.Download(ctx, s.ID, p)
	if err != nil {
		h.record(ctx, audit.ActionFileDownload, s.ID, audit.ResultFailure, map[string]any{"path": p})
		h.writeFilesError(w, r, err)
		return
	}
	defer func() { _ = d.Body.Close() }()
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
	rc := http.NewResponseController(w)
	dw := &deadlineWriter{
		w:     w,
		dl:    func() error { return ignoreUnsupported(rc.SetWriteDeadline(h.ioClock.Now().Add(h.ioTimeout))) },
		flush: func() error { return ignoreUnsupported(rc.Flush()) },
	}
	res := audit.ResultSuccess
	if err := dw.dl(); err != nil {
		res = audit.ResultFailure
	} else {
		w.WriteHeader(http.StatusOK)
		if _, err := io.Copy(dw, d.Body); err != nil {
			res = audit.ResultFailure
			h.log.DebugContext(ctx, "download aborted", "server_id", s.ID, "path", p, "err", err)
		}
	}
	h.record(ctx, audit.ActionFileDownload, s.ID, res, map[string]any{"path": p})
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
