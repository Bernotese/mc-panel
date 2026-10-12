package plugins

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
	"github.com/Bernotese/mc-panel/internal/store"
)

const (
	sourceManual = "manual"
	// projectFetches bounds the parallel project lookups of one inventory.
	projectFetches = 8
	disabledSuffix = ".disabled"
)

// projectRef is the OpenAPI schema PluginProjectRef.
type projectRef struct {
	ID      string  `json:"id"`
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	PageURL string  `json:"page_url"`
	IconURL *string `json:"icon_url"`
}

// versionRef is the OpenAPI schema PluginVersionRef.
type versionRef struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Channel upstream.Channel `json:"channel"`
}

// installedJar is the OpenAPI schema InstalledJar plus internal fields.
type installedJar struct {
	File             string            `json:"file"`
	Enabled          bool              `json:"enabled"`
	Size             int64             `json:"size"`
	MTime            int64             `json:"mtime"`
	Source           string            `json:"source"`
	Project          *projectRef       `json:"project"`
	Version          *versionRef       `json:"version"`
	Meta             *filesapi.JarMeta `json:"meta"`
	InstalledByPanel bool              `json:"installed_by_panel"`

	base   string // file name without ".disabled"
	sha512 string
	owner  string // Hangar owner (page URL)
}

// inventory is the OpenAPI schema PluginInventory.
type inventory struct {
	Kind          string            `json:"kind"`
	Dir           string            `json:"dir"`
	GameVersion   *string           `json:"game_version"`
	Sources       []upstream.Source `json:"sources"`
	Jars          []installedJar    `json:"jars"`
	Truncated     bool              `json:"truncated"`
	RestartNeeded bool              `json:"restart_needed"`
	SourceErrors  []upstream.Source `json:"source_errors"`
}

func projectKey(src upstream.Source, id string) string { return string(src) + "/" + id }

// baseName strips ".disabled" (any case) from a jar name that filesapi.IsJarName accepted.
func baseName(name string, enabled bool) string {
	if enabled {
		return name
	}
	return name[:len(name)-len(disabledSuffix)]
}

func (inv *inventory) addSourceError(src upstream.Source) {
	if !slices.Contains(inv.SourceErrors, src) {
		inv.SourceErrors = append(inv.SourceErrors, src)
	}
}

// inventory lists the jars of the server and maps each (spec §6.2): DB row with the same name
// and sha512 → panel source; else Modrinth by hash; else manual. Rows without their jar are
// deleted, unless the listing is truncated or a job of the server runs (decision 9).
func (h *Handler) inventory(ctx context.Context, srv server) (inventory, error) {
	inv := inventory{
		Kind: srv.Kind.Kind, Dir: srv.Kind.Dir, Sources: srv.Kind.Sources,
		Jars: []installedJar{}, SourceErrors: []upstream.Source{},
	}
	// Rows first: a row a running job writes after this read is never taken for an orphan. A
	// job that started (and maybe ended) since then also forbids the cleanup: the listing may
	// show the jar it wrote under a planned row's name.
	gen := h.jobs.generation(srv.ID)
	rows, err := h.q.PluginsListInstalls(ctx, srv.ID)
	if err != nil {
		return inventory{}, err
	}
	list, err := h.listJars(ctx, srv)
	if err != nil {
		return inventory{}, err
	}
	inv.Truncated = list.Truncated
	byName := make(map[string]store.PluginInstall, len(rows))
	for _, r := range rows {
		byName[r.Filename] = r
	}
	used := map[string]bool{}
	var unknown []int
	for _, j := range list.Jars {
		enabled, ok := filesapi.IsJarName(j.Name)
		if !ok {
			continue
		}
		it := installedJar{
			File: j.Name, Enabled: enabled, Size: j.Size, MTime: j.MTime, Source: sourceManual, Meta: j.Meta,
			base: baseName(j.Name, enabled), sha512: j.SHA512,
		}
		if row, ok := byName[it.base]; ok && j.SHA512 != "" && row.Sha512 == j.SHA512 && !used[it.base] {
			used[it.base] = true
			it.Source, it.InstalledByPanel = row.Source, true
			it.Project = &projectRef{ID: row.ProjectID, Slug: row.ProjectSlug, Title: row.Title}
			it.Version = &versionRef{ID: row.VersionID, Name: row.VersionName, Channel: upstream.Channel(row.Channel)}
		} else if j.SHA512 != "" {
			unknown = append(unknown, len(inv.Jars))
		}
		inv.Jars = append(inv.Jars, it)
	}
	if !inv.Truncated && !h.jobRunning(srv.ID) && h.jobs.generation(srv.ID) == gen {
		for _, r := range rows {
			if used[r.Filename] {
				continue
			}
			if err := h.q.PluginsDeleteInstall(ctx, store.PluginsDeleteInstallParams{ServerID: srv.ID, Filename: r.Filename}); err != nil {
				return inventory{}, err
			}
		}
	}
	if len(unknown) > 0 {
		h.mapByHash(ctx, &inv, unknown)
	}
	h.enrichProjects(ctx, srv, &inv)
	return inv, nil
}

// listJars lists the jar directory of srv. An old files service without /v1/jars answers
// not_found (a server without data directory gets an empty list), so not_found counts as an
// unavailable service, never as a missing file.
func (h *Handler) listJars(ctx context.Context, srv server) (filesapi.JarsResponse, error) {
	list, err := h.files.Jars(ctx, srv.ID, srv.Kind.Dir)
	if errors.Is(err, filesapi.ErrNotFound) {
		return filesapi.JarsResponse{}, fmt.Errorf("%w: the file service does not list jars (update mcpanel-files)", filesapi.ErrUnavailable)
	}
	return list, err
}

// mapByHash asks Modrinth for the jars without a DB row; failures leave them manual and add a
// source error.
func (h *Handler) mapByHash(ctx context.Context, inv *inventory, idx []int) {
	hashes := make([]string, 0, len(idx))
	for _, i := range idx {
		hashes = append(hashes, inv.Jars[i].sha512)
	}
	found, err := h.up.VersionsByHash(ctx, hashes)
	if err != nil {
		h.log.WarnContext(ctx, "plugins: modrinth hash lookup failed", "err", err)
		inv.addSourceError(upstream.Modrinth)
		return
	}
	for _, i := range idx {
		j := &inv.Jars[i]
		v, ok := found[j.sha512]
		if !ok {
			continue
		}
		j.Source = string(upstream.Modrinth)
		j.Project = &projectRef{ID: v.ProjectID}
		j.Version = &versionRef{ID: v.ID, Name: v.Name, Channel: v.Channel}
	}
}

// enrichProjects fills slug, title, owner and icon of every mapped jar from the (cached)
// project lookups, at most projectFetches at a time; a failed lookup adds a source error.
func (h *Handler) enrichProjects(ctx context.Context, srv server, inv *inventory) {
	type key struct {
		src upstream.Source
		id  string
	}
	want := map[key]bool{}
	for _, j := range inv.Jars {
		if j.Project != nil {
			want[key{upstream.Source(j.Source), j.Project.ID}] = true
		}
	}
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		got    = map[key]upstream.Project{}
		failed = map[upstream.Source]bool{}
		sem    = make(chan struct{}, projectFetches)
	)
	for k := range want {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			mu.Lock()
			down := failed[k.src]
			mu.Unlock()
			if down {
				return // the source failed already: do not wait for every lookup (source_errors)
			}
			p, err := h.up.Project(ctx, k.src, k.id)
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, upstream.ErrNotFound) {
				return // removed upstream: the jar keeps what the DB or hash lookup knows
			}
			if err != nil {
				failed[k.src] = true
				return
			}
			got[k] = p
		})
	}
	wg.Wait()
	for i := range inv.Jars {
		j := &inv.Jars[i]
		if j.Project == nil {
			continue
		}
		src := upstream.Source(j.Source)
		if p, ok := got[key{src, j.Project.ID}]; ok {
			j.Project.Slug = cmp.Or(j.Project.Slug, p.Slug)
			j.Project.Title = cmp.Or(j.Project.Title, p.Title)
			j.owner = p.Owner
			j.Project.IconURL = h.iconURL(srv.ID, p.IconURL)
		}
		j.Project.PageURL = pageURL(src, srv.Kind.Kind, j.owner, cmp.Or(j.Project.Slug, j.Project.ID))
	}
	for _, src := range []upstream.Source{upstream.Modrinth, upstream.Hangar} {
		if failed[src] {
			inv.addSourceError(src)
		}
	}
}

// installedByProject indexes the mapped jars (enabled or not) by source and project id.
func installedByProject(inv inventory) map[string]installedJar {
	m := map[string]installedJar{}
	for _, j := range inv.Jars {
		if j.Project == nil || j.Source == sourceManual {
			continue
		}
		k := projectKey(upstream.Source(j.Source), j.Project.ID)
		if _, ok := m[k]; !ok {
			m[k] = j
		}
	}
	return m
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok {
		return
	}
	inv, err := h.inventory(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	if gv, err := h.gameVersion(ctx, srv); err == nil {
		inv.GameVersion = &gv
	} else {
		h.log.DebugContext(ctx, "plugins: game version unknown", "server_id", srv.ID, "err", err)
	}
	inv.RestartNeeded = h.restartNeeded(ctx, srv.ID)
	httpx.WriteJSON(w, http.StatusOK, inv)
}
