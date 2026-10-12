package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
	"github.com/Bernotese/mc-panel/internal/store"
)

type installRequest struct {
	Source  upstream.Source `json:"source"`
	Project string          `json:"project"`
	Version *string         `json:"version"`
}

// jobTask is one jar of a job; its step has the same index.
type jobTask struct {
	entry planEntry
	group int         // a failed task skips the remaining tasks of its group
	from  *versionRef // update: the replaced version (audit plugin.update); nil = install
	err   *taskError  // the task failed before the job started (update resolution)
}

type taskError struct{ code, msg string }

// targetName is the file a task writes: the planned name, with ".disabled" when it replaces a
// disabled jar.
func targetName(e planEntry) string {
	if e.replaces != "" && !e.replacesEnabled {
		return e.Filename + disabledSuffix
	}
	return e.Filename
}

func stepsOf(tasks []jobTask) []jobStep {
	out := make([]jobStep, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, jobStep{Title: t.entry.Title, File: targetName(t.entry), State: stepPending})
	}
	return out
}

// cleanJarFile validates a file parameter (spec §6.6): a plain jar name; it returns the name
// without ".disabled" and whether the jar is enabled.
func cleanJarFile(name string) (base string, enabled, ok bool) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", false, false
	}
	c, err := filesapi.CleanPath(name)
	if err != nil || c != name || filesapi.IsHidden(c) {
		return "", false, false
	}
	enabled, ok = filesapi.IsJarName(name)
	if !ok {
		return "", false, false
	}
	return baseName(name, enabled), enabled, true
}

// baseOf is baseName for a name that may not be a jar name.
func baseOf(name string) string {
	enabled, ok := filesapi.IsJarName(name)
	if !ok {
		return name
	}
	return baseName(name, enabled)
}

// nameConflict reports a planned file name another jar already uses, also only differing in
// case or by ".disabled"; the jar an entry replaces does not count (Review Focus #2).
func nameConflict(inv inventory, plan []planEntry) error {
	for _, e := range plan {
		for _, j := range inv.Jars {
			if j.File != e.replaces && strings.EqualFold(j.base, e.Filename) {
				return fmt.Errorf("%w: %s", errFileExists, j.File)
			}
		}
	}
	return nil
}

func (h *Handler) handleInstall(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	var req installRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if _, ok := sourceParam(w, r, srv, string(req.Source)); !ok {
		return
	}
	version := ""
	if req.Version != nil {
		version = *req.Version
	}
	if !refParam(w, r, "project", req.Project, false) || !refParam(w, r, "version", version, true) {
		return
	}
	if h.jobs.running(srv.ID) {
		h.writeError(w, r, errJobRunning)
		return
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	// The plan is computed again: the preview the browser showed is not trusted.
	inv, err := h.inventory(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	res, err := h.resolve(ctx, srv, gv, inv, resolveRequest{Source: req.Source, Project: req.Project, Version: version})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if len(res.Incompatible) > 0 {
		titles := make([]string, 0, len(res.Incompatible))
		for _, x := range res.Incompatible {
			titles = append(titles, x.Title)
		}
		h.writeError(w, r, fmt.Errorf("%w: required dependencies without a compatible version: %s", errIncompatible, strings.Join(titles, ", ")))
		return
	}
	if len(res.Install) == 0 {
		h.writeError(w, r, errAlreadyInstalled)
		return
	}
	if err := nameConflict(inv, res.Install); err != nil {
		h.writeError(w, r, err)
		return
	}
	tasks := make([]jobTask, 0, len(res.Install))
	for _, e := range res.Install {
		tasks = append(tasks, jobTask{entry: e})
	}
	view, err := h.jobs.start(ctx, srv.ID, jobInstall, stepsOf(tasks), func(ctx context.Context, j *job) {
		h.runJob(ctx, j, srv, tasks, true)
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, view)
}

// runJob runs the tasks in order. A failure ends the job (stopOnError, install) or skips the
// rest of the task's group (update). The updates check of the server is dropped at the end.
func (h *Handler) runJob(ctx context.Context, j *job, srv server, tasks []jobTask, stopOnError bool) {
	defer h.updates.Delete(updatesKey(srv.ID))
	if err := h.files.Mkdir(ctx, srv.ID, srv.Kind.Dir); err != nil && !errors.Is(err, filesapi.ErrExists) {
		h.log.WarnContext(ctx, "plugins: create folder", "server_id", srv.ID, "dir", srv.Kind.Dir, "err", err)
	}
	failed := map[int]bool{}
	stop := false
	for i, t := range tasks {
		switch {
		case stop || failed[t.group]:
			j.set(i, stepSkipped)
			continue
		case t.err != nil:
			h.auditTask(ctx, srv.ID, t, targetName(t.entry), errors.New(t.err.msg))
			j.fail(i, t.err.code, t.err.msg)
		default:
			j.set(i, stepDownloading)
			err := h.installTask(ctx, srv, t)
			if err == nil {
				j.set(i, stepDone)
				continue
			}
			code, msg := stepError(err)
			if ctx.Err() != nil {
				code, msg = codeFilesUnavailable, "the job was aborted because the panel is shutting down"
			}
			h.log.WarnContext(ctx, "plugins: job step failed", "server_id", srv.ID, "file", t.entry.Filename, "code", code, "err", err)
			j.fail(i, code, msg)
		}
		failed[t.group] = true
		stop = stopOnError
	}
}

// installTask downloads one jar, hands it to the service with its checksum and records it.
func (h *Handler) installTask(ctx context.Context, srv server, t jobTask) (err error) {
	e := t.entry
	target := targetName(e)
	defer func() { h.auditTask(ctx, srv.ID, t, target, err) }()
	sum, err := checksumOf(e.file)
	if err != nil {
		return err
	}
	// The name must still be free unless it is the jar this task replaces (Review Focus #2/#3).
	list, err := h.listJars(ctx, srv)
	if err != nil {
		return err
	}
	for _, jar := range list.Jars {
		if jar.Name != e.replaces && strings.EqualFold(baseOf(jar.Name), e.Filename) {
			return fmt.Errorf("%w: %s", errFileExists, jar.Name)
		}
	}
	body, err := h.up.Download(ctx, e.file)
	if err != nil {
		return err
	}
	tr := &trackReader{r: body}
	resp, err := h.files.UploadChecked(ctx, srv.ID, filesapi.Join(srv.Kind.Dir, target), e.replaces == target, e.Size, sum, tr)
	_ = body.Close()
	if err != nil {
		if rerr := tr.failure(); rerr != nil {
			return rerr // the download broke, not the service
		}
		return err
	}
	if resp.SHA512 == "" {
		return fmt.Errorf("%w: the file service returned no checksum", filesapi.ErrUnavailable)
	}
	// The jar is on disk: record it even if the job is being cancelled right now.
	ctx = context.WithoutCancel(ctx)
	if e.replaces != "" && e.replaces != target {
		if err := h.files.Delete(ctx, srv.ID, filesapi.Join(srv.Kind.Dir, e.replaces)); err != nil && !errors.Is(err, filesapi.ErrNotFound) {
			return err
		}
	}
	if old := baseOf(e.replaces); e.replaces != "" && old != e.Filename {
		if err := h.q.PluginsDeleteInstall(ctx, store.PluginsDeleteInstallParams{ServerID: srv.ID, Filename: old}); err != nil {
			return err
		}
	}
	if err := h.q.PluginsUpsertInstall(ctx, store.PluginsUpsertInstallParams{
		ServerID: srv.ID, Filename: e.Filename, Sha512: resp.SHA512, Source: string(e.Source), ProjectID: e.ProjectID,
		ProjectSlug: e.Slug, Title: e.Title, VersionID: e.VersionID, VersionName: e.VersionName, Channel: string(e.Channel),
		InstalledAt: h.clock.Now().UnixMilli(),
	}); err != nil {
		return err
	}
	h.markChanged(srv.ID)
	return nil
}

// auditTask writes plugin.install or plugin.update for one jar; the actor is whoever started
// the job (its context carries the request's principal).
func (h *Handler) auditTask(ctx context.Context, serverID int64, t jobTask, target string, err error) {
	e := t.entry
	if t.from != nil {
		h.record(ctx, audit.ActionPluginUpdate, serverID, result(err), map[string]any{"file": target, "from": t.from.Name, "to": e.VersionName})
		return
	}
	h.record(ctx, audit.ActionPluginInstall, serverID, result(err), map[string]any{
		"source": string(e.Source), "project": e.ProjectID, "version": e.VersionID, "file": target,
	})
}

// checksumOf is the digest the service checks before it stores the jar (Modrinth sha512,
// Hangar sha256).
func checksumOf(f upstream.File) (filesapi.Checksum, error) {
	sum := filesapi.Checksum{Algo: filesapi.AlgoSHA512, Hex: f.SHA512}
	if f.SHA512 == "" {
		sum = filesapi.Checksum{Algo: filesapi.AlgoSHA256, Hex: f.SHA256}
	}
	if err := sum.Validate(); err != nil {
		return filesapi.Checksum{}, fmt.Errorf("%w: %w", errNoChecksum, err)
	}
	return sum, nil
}

// trackReader remembers the first read error of the download, so that a broken download is
// not mistaken for a failing file service.
type trackReader struct {
	r   io.Reader
	mu  sync.Mutex
	err error
}

func (t *trackReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		t.mu.Lock()
		if t.err == nil {
			t.err = err
		}
		t.mu.Unlock()
	}
	return n, err
}

func (t *trackReader) failure() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// stepError maps a failed task to its step code (spec §6.4) and a text without host paths.
func stepError(err error) (code, msg string) {
	switch {
	case errors.Is(err, errFileExists), errors.Is(err, filesapi.ErrExists):
		return codeFileExists, "a file with this name already exists in the folder"
	case errors.Is(err, filesapi.ErrHashMismatch):
		return codeHashMismatch, "the downloaded file does not match the checksum of the source"
	case errors.Is(err, filesapi.ErrInsufficientStorage):
		return codeInsufficientStorage, "not enough free disk space"
	case errors.Is(err, upstream.ErrDownload), errors.Is(err, errNoChecksum), errors.Is(err, errNotDownloadable),
		errors.Is(err, errIncompatible), errors.Is(err, filesapi.ErrInvalid), errors.Is(err, filesapi.ErrTooLarge):
		return codeDownloadFailed, "the download failed or the file was refused"
	case errors.Is(err, upstream.ErrUnavailable), errors.Is(err, upstream.ErrNotFound):
		return codeSourceUnavailable, "Modrinth or Hangar is not reachable"
	}
	return codeFilesUnavailable, "the file service is not reachable"
}
