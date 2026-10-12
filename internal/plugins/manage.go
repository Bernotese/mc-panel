package plugins

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

type toggleRequest struct {
	File    string `json:"file"`
	Enabled *bool  `json:"enabled"`
}

type fileRequest struct {
	File string `json:"file"`
}

// changed records a plugin change: restart_needed and a new updates check.
func (h *Handler) changed(serverID int64) {
	h.markChanged(serverID)
	h.updates.Delete(updatesKey(serverID))
}

// findJar lists the jars and returns the one named file. A name the listing does not show is
// filesapi.ErrNotFound, also when the listing is truncated: it may be a directory (the service
// deletes those recursively) or anything else that is no jar. A different jar named like
// target in any case is errFileExists (Review Focus #2).
func (h *Handler) findJar(ctx context.Context, srv server, file, target string) (filesapi.Jar, error) {
	list, err := h.listJars(ctx, srv)
	if err != nil {
		return filesapi.Jar{}, err
	}
	var (
		jar   filesapi.Jar
		found bool
	)
	for _, j := range list.Jars {
		switch {
		case j.Name == file:
			jar, found = j, true
		case target != "" && strings.EqualFold(j.Name, target):
			return filesapi.Jar{}, fmt.Errorf("%w: %s", errFileExists, j.Name)
		}
	}
	if !found {
		return filesapi.Jar{}, fmt.Errorf("%w: %s", filesapi.ErrNotFound, file)
	}
	return jar, nil
}

func (h *Handler) handleToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	var req toggleRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	base, enabled, ok := cleanJarFile(req.File)
	if !ok || req.Enabled == nil {
		writeValidation(w, r, "file must be a jar name ending in .jar or .jar.disabled, enabled a boolean")
		return
	}
	if h.jobs.running(srv.ID) {
		h.writeError(w, r, errJobRunning)
		return
	}
	if enabled == *req.Enabled {
		w.WriteHeader(http.StatusNoContent) // already in the wanted state
		return
	}
	target := base
	if !*req.Enabled {
		target = base + disabledSuffix
	}
	if _, err := h.findJar(ctx, srv, req.File, target); err != nil {
		h.writeError(w, r, err)
		return
	}
	err := h.files.Rename(context.WithoutCancel(ctx), filesapi.RenameRequest{
		ID: srv.ID, From: filesapi.Join(srv.Kind.Dir, req.File), To: filesapi.Join(srv.Kind.Dir, target),
	})
	h.record(ctx, audit.ActionPluginToggle, srv.ID, result(err), map[string]any{"file": req.File, "enabled": *req.Enabled})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.changed(srv.ID)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	var req fileRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	base, _, ok := cleanJarFile(req.File)
	if !ok {
		writeValidation(w, r, "file must be a jar name ending in .jar or .jar.disabled")
		return
	}
	if h.jobs.running(srv.ID) {
		h.writeError(w, r, errJobRunning)
		return
	}
	jar, err := h.findJar(ctx, srv, req.File, "")
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	dctx := context.WithoutCancel(ctx)
	err = h.files.Delete(dctx, srv.ID, filesapi.Join(srv.Kind.Dir, req.File))
	if err == nil {
		h.dropRow(dctx, srv.ID, base, jar.SHA512)
	}
	h.record(ctx, audit.ActionPluginRemove, srv.ID, result(err), map[string]any{"file": req.File})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	h.changed(srv.ID)
	w.WriteHeader(http.StatusNoContent)
}

// dropRow deletes the row of a deleted jar, but only if it describes exactly that jar: the
// row of "x.jar" may belong to "x.jar.disabled" (Review Focus #2). A failure is logged; the
// next inventory removes an orphan anyway.
func (h *Handler) dropRow(ctx context.Context, serverID int64, base, sha512 string) {
	rows, err := h.q.PluginsListInstalls(ctx, serverID)
	if err != nil {
		h.log.WarnContext(ctx, "plugins: list rows after delete", "server_id", serverID, "err", err)
		return
	}
	for _, row := range rows {
		if row.Filename != base || row.Sha512 != sha512 {
			continue
		}
		if err := h.q.PluginsDeleteInstall(ctx, store.PluginsDeleteInstallParams{ServerID: serverID, Filename: base}); err != nil {
			h.log.WarnContext(ctx, "plugins: delete row", "server_id", serverID, "file", base, "err", err)
		}
	}
}
