package serverfiles

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// listJSON is the OpenAPI schema FileList.
type listJSON struct {
	Path      string           `json:"path"`
	Entries   []filesapi.Entry `json:"entries"`
	Truncated bool             `json:"truncated"`
}

// contentJSON is the OpenAPI schema FileContent.
type contentJSON struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	MTime   int64  `json:"mtime"`
	Size    int64  `json:"size"`
}

// optional is a JSON value whose presence is known. The contract requires content and
// expected_mtime; a missing expected_mtime must not be read as null ("create a new file").
type optional[T any] struct {
	Set   bool
	Value *T // nil for JSON null
}

func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

// writeRequest is the OpenAPI schema WriteFileRequest.
type writeRequest struct {
	Path          string           `json:"path"`
	Content       optional[string] `json:"content"`
	ExpectedMTime optional[int64]  `json:"expected_mtime"`
}

type pathRequest struct {
	Path string `json:"path"`
}

type renameRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	p, ok := cleanPath(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	res, err := h.files.List(r.Context(), s.ID, p)
	if err != nil {
		h.writeFilesError(w, r, err)
		return
	}
	// The service hides these names itself; the panel filters again (defence in depth).
	entries := slices.DeleteFunc(slices.Clone(res.Entries), func(e filesapi.Entry) bool {
		return filesapi.IsHidden(filesapi.Join(p, e.Name))
	})
	if entries == nil {
		entries = []filesapi.Entry{}
	}
	httpx.WriteJSON(w, http.StatusOK, listJSON{Path: p, Entries: entries, Truncated: res.Truncated})
}

func (h *Handler) handleRead(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	p, ok := cleanItemPath(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	release, ok := h.editorSlot(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := h.files.Read(r.Context(), s.ID, p)
	if err != nil {
		h.writeFilesError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, contentJSON{Path: p, Content: res.Content, MTime: res.MTime, Size: res.Size})
}

func (h *Handler) handleWrite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	release, ok := h.editorSlot(w, r)
	if !ok {
		return
	}
	defer release()
	// The editor may save up to filesapi.MaxJSONBytes (decision 1); UnlimitedBody exempts this
	// route from the global 1 MiB limit. Over a slow connection that takes longer than the
	// server's ReadTimeout: every read gets its own deadline, like an upload (review B6), but
	// the whole body must arrive within saveTimeout (it holds an editor slot meanwhile).
	rc := http.NewResponseController(w)
	until := h.ioClock.Now().Add(h.saveTimeout)
	body := &deadlineReader{
		r: r.Body,
		dl: func() error {
			next := h.ioClock.Now().Add(h.ioTimeout)
			if until.Before(next) {
				next = until
			}
			return ignoreUnsupported(rc.SetReadDeadline(next))
		},
		eof: r.ContentLength == 0,
	}
	r.Body = http.MaxBytesReader(w, io.NopCloser(body), filesapi.MaxJSONBytes)
	var req writeRequest
	decoded := httpx.DecodeJSON(w, r, &req)
	body.finish()
	if !decoded {
		return
	}
	if req.Content.Value == nil {
		writeValidation(w, r, "content is required")
		return
	}
	if !req.ExpectedMTime.Set {
		writeValidation(w, r, "expected_mtime is required (null creates a new file)")
		return
	}
	p, ok := cleanItemPath(w, r, req.Path)
	if !ok {
		return
	}
	res, err := h.files.Write(context.WithoutCancel(ctx), filesapi.WriteRequest{
		ID: s.ID, Path: p, Content: *req.Content.Value, ExpectedMTime: req.ExpectedMTime.Value,
	})
	h.extendWrite(w, r)
	h.recordResult(ctx, audit.ActionFileWrite, s.ID, err, map[string]any{"path": p})
	if err != nil {
		h.writeFilesError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) handleMkdir(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	var req pathRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	p, ok := cleanItemPath(w, r, req.Path)
	if !ok {
		return
	}
	err := h.files.Mkdir(context.WithoutCancel(ctx), s.ID, p)
	h.recordResult(ctx, audit.ActionFileMkdir, s.ID, err, map[string]any{"path": p})
	if err != nil {
		h.writeFilesError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) handleRename(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	var req renameRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	from, ok := cleanItemPath(w, r, req.From)
	if !ok {
		return
	}
	to, ok := cleanItemPath(w, r, req.To)
	if !ok {
		return
	}
	err := h.files.Rename(context.WithoutCancel(ctx), filesapi.RenameRequest{ID: s.ID, From: from, To: to})
	h.recordResult(ctx, audit.ActionFileRename, s.ID, err, map[string]any{"from": from, "to": to})
	if err != nil {
		h.writeFilesError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	var req pathRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	p, ok := cleanItemPath(w, r, req.Path)
	if !ok {
		return
	}
	// Deleting a world can take minutes (filesapi.DeleteTimeout), longer than the WriteTimeout.
	err := h.files.Delete(context.WithoutCancel(ctx), s.ID, p)
	h.extendWrite(w, r)
	h.recordResult(ctx, audit.ActionFileDelete, s.ID, err, map[string]any{"path": p})
	if err != nil {
		h.writeFilesError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
