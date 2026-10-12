package plugins

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// updatesTTL is how long an updates check of a server is served from memory (spec §6.5).
const updatesTTL = time.Hour

// updateEntry is the OpenAPI schema PluginUpdate plus the jar it replaces.
type updateEntry struct {
	File string     `json:"file"`
	From versionRef `json:"from"`
	To   versionRef `json:"to"`

	jar    installedJar
	source upstream.Source
}

// updatesResult is the OpenAPI schema PluginUpdates.
type updatesResult struct {
	Updates   []updateEntry `json:"updates"`
	CheckedAt int64         `json:"checked_at"`

	scope string // server type and game version the check was made for
}

func updatesScope(srv server, gv string) string { return string(srv.Type) + "/" + gv }

func updatesKey(serverID int64) string { return strconv.FormatInt(serverID, 10) }

// channelsUpTo are the Modrinth version types an installed channel accepts: the same or more
// stable.
func channelsUpTo(c upstream.Channel) []upstream.Channel {
	switch c {
	case upstream.Release:
		return []upstream.Channel{upstream.Release}
	case upstream.Beta:
		return []upstream.Channel{upstream.Release, upstream.Beta}
	}
	return []upstream.Channel{upstream.Release, upstream.Beta, upstream.Alpha}
}

// checkUpdates returns the updates of the server, cached for an hour (refresh drops the cache).
func (h *Handler) checkUpdates(ctx context.Context, srv server, gv string, refresh bool) (updatesResult, error) {
	key := updatesKey(srv.ID)
	// A check for another type or game version (the server was changed) is stale.
	if v, ok := h.updates.Peek(key); refresh || (ok && v.scope != updatesScope(srv, gv)) {
		h.updates.Delete(key)
	}
	return h.updates.Get(ctx, key, func(ctx context.Context) (updatesResult, error) { return h.findUpdates(ctx, srv, gv) })
}

func (h *Handler) findUpdates(ctx context.Context, srv server, gv string) (updatesResult, error) {
	inv, err := h.inventory(ctx, srv)
	if err != nil {
		return updatesResult{}, err
	}
	if slices.Contains(inv.SourceErrors, upstream.Modrinth) {
		return updatesResult{}, fmt.Errorf("%w: modrinth hash lookup failed", upstream.ErrUnavailable)
	}
	out := updatesResult{Updates: []updateEntry{}, CheckedAt: h.clock.Now().UnixMilli(), scope: updatesScope(srv, gv)}
	byChannel := map[upstream.Channel][]installedJar{}
	var hangar []installedJar
	for _, j := range inv.Jars {
		if j.Version == nil || j.Project == nil || j.sha512 == "" {
			continue // manual jars get no updates
		}
		switch upstream.Source(j.Source) {
		case upstream.Modrinth:
			byChannel[j.Version.Channel] = append(byChannel[j.Version.Channel], j)
		case upstream.Hangar:
			hangar = append(hangar, j)
		}
	}
	for _, ch := range []upstream.Channel{upstream.Release, upstream.Beta, upstream.Alpha} {
		jars := byChannel[ch]
		if len(jars) == 0 {
			continue
		}
		hashes := make([]string, 0, len(jars))
		for _, j := range jars {
			hashes = append(hashes, j.sha512)
		}
		latest, err := h.up.LatestByHash(ctx, hashes, srv.Kind.Loaders, []string{gv}, channelsUpTo(ch))
		if err != nil {
			return updatesResult{}, err
		}
		for _, j := range jars {
			if v, ok := latest[j.sha512]; ok && v.ID != j.Version.ID && v.Downloadable() {
				out.Updates = append(out.Updates, updateEntry{
					File: j.File, From: *j.Version, To: versionRef{ID: v.ID, Name: v.Name, Channel: v.Channel},
					jar: j, source: upstream.Modrinth,
				})
			}
		}
	}
	for _, j := range hangar {
		u, ok, err := h.hangarUpdate(ctx, srv, gv, j)
		if err != nil {
			return updatesResult{}, err
		}
		if ok {
			out.Updates = append(out.Updates, u)
		}
	}
	slices.SortFunc(out.Updates, func(a, b updateEntry) int { return strings.Compare(a.File, b.File) })
	return out, nil
}

// hangarUpdate finds the newest downloadable version in the same or a more stable channel that
// was published after the installed one.
func (h *Handler) hangarUpdate(ctx context.Context, srv server, gv string, j installedJar) (updateEntry, bool, error) {
	release := j.Version.Channel == upstream.Release
	vs, err := h.up.Versions(ctx, upstream.Hangar, j.Project.ID, upstream.VersionFilter{GameVersion: gv, ReleaseOnly: release})
	if errors.Is(err, upstream.ErrNotFound) {
		return updateEntry{}, false, nil // the project was removed upstream: no updates
	}
	if err != nil {
		return updateEntry{}, false, err
	}
	i := slices.IndexFunc(vs, func(v upstream.Version) bool {
		return v.Downloadable() && compatible(v, srv.Kind, gv) && (!release || v.Channel == upstream.Release)
	})
	if i < 0 || vs[i].ID == j.Version.ID {
		return updateEntry{}, false, nil
	}
	cand := vs[i]
	cur, err := h.up.Version(ctx, upstream.Hangar, j.Project.ID, j.Version.ID)
	switch {
	case errors.Is(err, upstream.ErrNotFound):
		// the installed version is gone upstream: any candidate is newer
	case err != nil:
		return updateEntry{}, false, err
	case !cand.PublishedAt.After(cur.PublishedAt):
		return updateEntry{}, false, nil
	}
	return updateEntry{
		File: j.File, From: *j.Version, To: versionRef{ID: cand.ID, Name: cand.Name, Channel: cand.Channel},
		jar: j, source: upstream.Hangar,
	}, true, nil
}

func parseRefresh(s string) bool { return s == "1" || strings.EqualFold(s, "true") }

func (h *Handler) handleUpdates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	refresh := parseRefresh(r.URL.Query().Get("refresh"))
	if refresh && !h.allow(w, r) {
		return
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	res, err := h.checkUpdates(ctx, srv, gv, refresh)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

type updateRequest struct {
	Files []string `json:"files"`
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	var req updateRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if len(req.Files) > filesapi.MaxJars {
		writeValidation(w, r, "too many files")
		return
	}
	for _, f := range req.Files {
		if _, _, ok := cleanJarFile(f); !ok {
			writeValidation(w, r, "invalid file name: "+f)
			return
		}
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
	upd, err := h.checkUpdates(ctx, srv, gv, false)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	selected := upd.Updates
	if len(req.Files) > 0 {
		selected = make([]updateEntry, 0, len(req.Files))
		for _, f := range req.Files {
			i := slices.IndexFunc(upd.Updates, func(u updateEntry) bool { return u.File == f })
			if i < 0 {
				writeValidation(w, r, "no update available for "+f)
				return
			}
			selected = append(selected, upd.Updates[i])
		}
	}
	if len(selected) == 0 {
		writeValidation(w, r, "no updates available")
		return
	}
	inv, err := h.inventory(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	tasks := h.updateTasks(ctx, srv, gv, inv, selected)
	view, err := h.jobs.start(ctx, srv.ID, jobUpdate, stepsOf(tasks), func(ctx context.Context, j *job) {
		h.runJob(ctx, j, srv, tasks, false)
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, view)
}

// updateTasks plans every selected update with its new required dependencies (decision 6): one
// group per update, its dependencies first.
func (h *Handler) updateTasks(ctx context.Context, srv server, gv string, inv inventory, selected []updateEntry) []jobTask {
	current := map[string]installedJar{}
	for _, j := range inv.Jars {
		current[j.File] = j
	}
	p := newPlanner(h, srv, gv, installedByProject(inv), nil)
	var tasks []jobTask
	for g, u := range selected {
		from := u.From
		root, deps, terr := h.planUpdate(ctx, p, u)
		if j, ok := current[u.File]; !ok || j.sha512 != u.jar.sha512 {
			deps, terr = nil, &taskError{codeFilesUnavailable, "the jar changed since the update check, check for updates again"}
		}
		for _, d := range deps {
			tasks = append(tasks, jobTask{entry: d, group: g})
		}
		tasks = append(tasks, jobTask{entry: root, group: g, from: &from, err: terr})
	}
	return tasks
}

func (h *Handler) planUpdate(ctx context.Context, p *planner, u updateEntry) (planEntry, []planEntry, *taskError) {
	fallback := planEntry{
		Source: u.source, ProjectID: u.jar.Project.ID, Slug: u.jar.Project.Slug, Title: cmp.Or(u.jar.Project.Title, u.File),
		VersionID: u.To.ID, VersionName: u.To.Name, Channel: u.To.Channel, Filename: u.jar.base,
		replaces: u.File, replacesEnabled: u.jar.Enabled, replacesSHA512: u.jar.sha512,
	}
	fail := func(err error) (planEntry, []planEntry, *taskError) {
		code, msg := stepError(err)
		return fallback, nil, &taskError{code, msg}
	}
	proj, err := p.up.Project(ctx, u.source, u.jar.Project.ID)
	if err != nil {
		return fail(err)
	}
	v, err := p.up.Version(ctx, u.source, proj.ID, u.To.ID)
	if err != nil {
		return fail(err)
	}
	if !v.Downloadable() {
		return fail(errNotDownloadable)
	}
	root := p.entry(proj, v, nil)
	root.replaces, root.replacesEnabled, root.replacesSHA512 = u.File, u.jar.Enabled, u.jar.sha512
	p.seen[projectKey(u.source, proj.ID)] = true
	res := newResolution()
	deps, err := p.dependencies(ctx, &res, v, u.source, proj.Title)
	if err != nil {
		return fail(err)
	}
	if len(res.Incompatible) > 0 {
		titles := make([]string, 0, len(res.Incompatible))
		for _, x := range res.Incompatible {
			titles = append(titles, x.Title)
		}
		return root, nil, &taskError{codeDownloadFailed, "a required dependency has no compatible version: " + strings.Join(titles, ", ")}
	}
	return root, deps, nil
}
