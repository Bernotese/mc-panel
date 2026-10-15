package plugins

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// Errors of the mrpack routes, exported for the contract test in cmd/panel (ErrorStatus).
var (
	ErrMrpackIncompatible = errMrpackIncompatible
	ErrMrpackMismatch     = errMrpackMismatch
	ErrNothingToImport    = errNothingToImport
)

const (
	mrpackInstall   = "install"
	mrpackUpdate    = "update"
	mrpackUnchanged = "unchanged"
	mrpackSkip      = "skip"

	mrpackClientOnly    = "client_only"
	mrpackNotOnModrinth = "not_on_modrinth"
	mrpackIncompatible  = "incompatible"
	mrpackFileExists    = "file_exists"

	// mrpackBodyLimit bounds a preview or import body (the panel's default request limit).
	mrpackBodyLimit = 1 << 20

	viaMrpack = "mrpack"
)

// mrpackLoaders maps a server type to the dependency key of its loader in an index.
var mrpackLoaders = map[instancespec.Type]string{
	instancespec.TypeFabric:   mrpackLoaderFabric,
	instancespec.TypeNeoForge: mrpackLoaderNeoForge,
}

// mrpackEntry is the OpenAPI schema MrpackPlanEntry plus what a job needs.
type mrpackEntry struct {
	File     string      `json:"file"`
	SHA512   string      `json:"sha512"`
	Action   string      `json:"action"`
	Reason   *string     `json:"reason"`
	Optional bool        `json:"optional"`
	Project  *projectRef `json:"project"`
	Version  *versionRef `json:"version"`
	Replaces *string     `json:"replaces"`

	task jobTask // install and update
}

type mrpackPack struct {
	Name      string `json:"name"`
	VersionID string `json:"version_id"`
}

type mrpackGameVersion struct {
	Pack   string `json:"pack"`
	Server string `json:"server"`
}

type mrpackLoader struct {
	Name   string  `json:"name"`
	Pack   string  `json:"pack"`
	Server *string `json:"server"`
}

// mrpackPlan is the OpenAPI schema MrpackPlan.
type mrpackPlan struct {
	Pack            mrpackPack        `json:"pack"`
	GameVersion     mrpackGameVersion `json:"game_version"`
	Loader          mrpackLoader      `json:"loader"`
	VersionMismatch bool              `json:"version_mismatch"`
	Entries         []mrpackEntry     `json:"entries"`
	Kept            []string          `json:"kept"`
	IgnoredFiles    int               `json:"ignored_files"`
}

// mrpackServer loads the server of the request; only fabric and neoforge import modpacks
// (paper and purpur answer plugins_unsupported, spec §1.2).
func (h *Handler) mrpackServer(w http.ResponseWriter, r *http.Request) (server, bool) {
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return server{}, false
	}
	if _, ok := mrpackLoaders[srv.Type]; !ok {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodePluginsUnsupported, "only fabric and neoforge servers import modpacks")
		return server{}, false
	}
	return srv, true
}

// readBody reads the request body up to mrpackBodyLimit; it writes 413 or 400 itself.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mrpackBodyLimit))
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge, "request body too large")
		return nil, false
	case err != nil:
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "invalid body")
		return nil, false
	}
	return b, true
}

// mrpackPlan computes the plan of a validated index for the server (spec §3); preview and
// import share it.
func (h *Handler) mrpackPlan(ctx context.Context, srv server, ix mrpackIndex) (mrpackPlan, error) {
	if want := mrpackLoaders[srv.Type]; ix.Loader != want {
		return mrpackPlan{}, fmt.Errorf("%w: the pack is for %s, this server runs %s", errMrpackIncompatible, cmp.Or(ix.Loader, "no loader"), srv.Type)
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		return mrpackPlan{}, err
	}
	plan := mrpackPlan{
		Pack:         mrpackPack{Name: ix.Name, VersionID: ix.VersionID},
		GameVersion:  mrpackGameVersion{Pack: ix.Minecraft, Server: gv},
		Loader:       mrpackLoader{Name: string(srv.Type), Pack: ix.LoaderVersion},
		Entries:      make([]mrpackEntry, 0, len(ix.Mods)),
		Kept:         []string{},
		IgnoredFiles: ix.Ignored,
	}
	if srv.Loader != "" {
		plan.Loader.Server = &srv.Loader
	}
	plan.VersionMismatch = ix.Minecraft != gv || (srv.Loader != "" && srv.Loader != ix.LoaderVersion)

	inv, err := h.inventory(ctx, srv)
	if err != nil {
		return mrpackPlan{}, err
	}
	if slices.Contains(inv.SourceErrors, upstream.Modrinth) {
		// Without the mapping of the installed jars an update would look like a name clash.
		return mrpackPlan{}, fmt.Errorf("%w: modrinth hash lookup failed", upstream.ErrUnavailable)
	}
	hashes := make([]string, 0, len(ix.Mods))
	for _, m := range ix.Mods {
		hashes = append(hashes, m.SHA512)
	}
	found, err := h.up.VersionsByHash(ctx, hashes)
	if err != nil {
		return mrpackPlan{}, err
	}
	ids := make([]string, 0, len(found))
	for _, v := range found {
		ids = append(ids, v.ProjectID)
	}
	projects, err := h.up.ModrinthProjects(ctx, ids)
	if err != nil {
		return mrpackPlan{}, err
	}

	c := mrpackClassifier{h: h, srv: srv, inv: inv, installed: installedByProject(inv), used: map[string]bool{}, names: map[string]bool{}}
	for _, m := range ix.Mods {
		v, known := found[m.SHA512]
		var proj *upstream.Project
		if known {
			p, ok := projects[v.ProjectID]
			if !ok { // removed meanwhile: what the version tells
				p = upstream.Project{Source: upstream.Modrinth, ID: v.ProjectID, Title: m.Name}
			}
			proj = &p
		}
		plan.Entries = append(plan.Entries, c.classify(m, v, proj))
	}
	for _, j := range inv.Jars {
		if !c.used[j.File] {
			plan.Kept = append(plan.Kept, j.File)
		}
	}
	return plan, nil
}

// mrpackClassifier classifies the mods of a pack in order (spec §3, step 4).
type mrpackClassifier struct {
	h         *Handler
	srv       server
	inv       inventory
	installed map[string]installedJar // projectKey -> jar
	used      map[string]bool         // installed jars an entry keeps (unchanged) or replaces
	names     map[string]bool         // lower-case target names planned so far
}

func (c *mrpackClassifier) classify(m mrpackMod, v upstream.Version, proj *upstream.Project) mrpackEntry {
	e := mrpackEntry{File: m.Name, SHA512: m.SHA512, Optional: m.Server == "optional"}
	skip := func(reason string) mrpackEntry {
		e.Action, e.Reason = mrpackSkip, &reason
		return e
	}
	if proj != nil {
		e.Project = &projectRef{
			ID: proj.ID, Slug: proj.Slug, Title: proj.Title,
			PageURL: pageURL(upstream.Modrinth, c.srv.Kind.Kind, "", cmp.Or(proj.Slug, proj.ID)),
			IconURL: c.h.iconURL(c.srv.ID, proj.IconURL),
		}
		e.Version = &versionRef{ID: v.ID, Name: v.Name, Channel: v.Channel}
	}
	switch {
	case m.Server == "unsupported" || (proj != nil && proj.ClientOnly()):
		return skip(mrpackClientOnly)
	case proj == nil:
		return skip(mrpackNotOnModrinth)
	case !slices.ContainsFunc(v.Loaders, func(l string) bool { return slices.Contains(c.srv.Kind.Loaders, l) }):
		return skip(mrpackIncompatible)
	}
	file, ok := v.FileBySHA512(m.SHA512)
	if !ok {
		return skip(mrpackNotOnModrinth) // Modrinth named a version whose jars lack the hash
	}
	for _, j := range c.inv.Jars {
		if j.sha512 == m.SHA512 && !c.used[j.File] {
			c.used[j.File] = true
			e.Action, e.File = mrpackUnchanged, j.File
			return e
		}
	}
	target := upstream.FileName(m.Name, proj.Slug, v.Name)
	e.File = target
	pe := planEntry{
		Source: upstream.Modrinth, ProjectID: proj.ID, Slug: proj.Slug, Title: proj.Title,
		VersionID: v.ID, VersionName: v.Name, Channel: v.Channel, Filename: target, Size: file.Size, file: file,
	}
	var from *versionRef
	if j, ok := c.installed[projectKey(upstream.Modrinth, proj.ID)]; ok && !c.used[j.File] {
		pe.replaces, pe.replacesEnabled, pe.replacesSHA512 = j.File, j.Enabled, j.sha512
		from = &versionRef{Name: j.File}
		if j.Version != nil {
			pe.replacesVersion = j.Version.Name
			from = j.Version
		}
	}
	if c.names[strings.ToLower(target)] {
		return skip(mrpackFileExists)
	}
	for _, j := range c.inv.Jars {
		if j.File != pe.replaces && strings.EqualFold(j.base, target) {
			return skip(mrpackFileExists)
		}
	}
	c.names[strings.ToLower(target)] = true
	e.Action = mrpackInstall
	if pe.replaces != "" {
		c.used[pe.replaces] = true
		e.Action, e.Replaces = mrpackUpdate, &pe.replaces
		e.File = targetName(pe) // a disabled jar stays disabled
	}
	e.task = jobTask{entry: pe, from: from, via: viaMrpack}
	return e
}

// parseBodyIndex validates the index; it answers 400 itself.
func parseBodyIndex(w http.ResponseWriter, r *http.Request, raw []byte) (mrpackIndex, bool) {
	ix, err := parseIndex(raw)
	if err != nil {
		writeValidation(w, r, err.Error())
		return mrpackIndex{}, false
	}
	return ix, true
}

func (h *Handler) handleMrpackPreview(w http.ResponseWriter, r *http.Request) {
	srv, ok := h.mrpackServer(w, r)
	if !ok {
		return
	}
	raw, ok := readBody(w, r)
	if !ok {
		return
	}
	ix, ok := parseBodyIndex(w, r, raw)
	if !ok || !h.allow(w, r) {
		return
	}
	plan, err := h.mrpackPlan(r.Context(), srv, ix)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, plan)
}

type mrpackImportRequest struct {
	Index          json.RawMessage `json:"index"`
	Exclude        []string        `json:"exclude"`
	AcceptMismatch bool            `json:"accept_mismatch"`
}

func (h *Handler) handleMrpackImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.mrpackServer(w, r)
	if !ok {
		return
	}
	var req mrpackImportRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if len(req.Index) == 0 || string(req.Index) == "null" {
		writeValidation(w, r, "index is missing")
		return
	}
	if len(req.Exclude) > maxMrpackFiles {
		writeValidation(w, r, "exclude has too many entries")
		return
	}
	exclude := map[string]bool{}
	for _, x := range req.Exclude {
		if !isHex(x, 128) {
			writeValidation(w, r, "exclude must list sha512 hashes")
			return
		}
		exclude[strings.ToLower(x)] = true
	}
	ix, ok := parseBodyIndex(w, r, req.Index)
	if !ok {
		return
	}
	if h.jobs.running(srv.ID) {
		h.writeError(w, r, errJobRunning)
		return
	}
	if !h.allow(w, r) {
		return
	}
	// The plan is computed again: the preview the browser showed is not trusted.
	plan, err := h.mrpackPlan(ctx, srv, ix)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if plan.VersionMismatch && !req.AcceptMismatch {
		h.writeError(w, r, fmt.Errorf("%w: the pack is for Minecraft %s with %s %s", errMrpackMismatch, ix.Minecraft, srv.Type, ix.LoaderVersion))
		return
	}
	for x := range exclude {
		if !slices.ContainsFunc(plan.Entries, func(e mrpackEntry) bool { return e.SHA512 == x && e.Optional }) {
			writeValidation(w, r, "exclude names a hash that is not an optional entry of the pack")
			return
		}
	}
	var tasks []jobTask
	counts := map[string]int{}
	for _, e := range plan.Entries {
		switch {
		case e.Action != mrpackInstall && e.Action != mrpackUpdate:
			counts[e.Action]++
		case exclude[e.SHA512]:
			counts[mrpackSkip]++
		default:
			e.task.group = len(tasks) // a failed step does not skip the others
			tasks = append(tasks, e.task)
		}
	}
	if len(tasks) == 0 {
		h.writeError(w, r, errNothingToImport)
		return
	}
	view, err := h.jobs.start(ctx, srv.ID, jobMrpack, stepsOf(tasks), func(ctx context.Context, j *job) {
		h.runJob(ctx, j, srv, tasks, false)
		h.auditMrpack(ctx, srv.ID, ix, tasks, j.snapshot(), counts)
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, view)
}

// auditMrpack writes the one plugin.mrpack_import entry at the end of an import job (each jar
// has its own plugin.install or plugin.update entry).
func (h *Handler) auditMrpack(ctx context.Context, serverID int64, ix mrpackIndex, tasks []jobTask, v jobView, counts map[string]int) {
	var installed, updated, failed int
	for i, st := range v.Steps {
		switch {
		case st.State == stepFailed:
			failed++
		case st.State == stepDone && tasks[i].from != nil:
			updated++
		case st.State == stepDone:
			installed++
		}
	}
	res := audit.ResultSuccess
	if failed > 0 {
		res = audit.ResultFailure
	}
	h.record(ctx, audit.ActionPluginMrpackImport, serverID, res, map[string]any{
		"pack": ix.Name, "version": ix.VersionID, "installed": installed, "updated": updated,
		"unchanged": counts[mrpackUnchanged], "skipped": counts[mrpackSkip], "failed": failed,
	})
}
