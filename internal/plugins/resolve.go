package plugins

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

const (
	maxDepth    = 5  // dependency levels below the requested project
	maxPackages = 20 // packages in a plan, the requested one included
	// maxVersionDeps caps the required dependencies of one version that are looked at; the
	// rest is one too_many entry.
	maxVersionDeps = 50
	// maxUpstreamCalls bounds the upstream requests of one resolution (cached ones included):
	// a full plan needs at most three per package; fixed versions that do not exist cost a
	// call each without filling the plan.
	maxUpstreamCalls = 80

	reasonNoVersion       = "no_compatible_version"
	reasonClientOnly      = "client_only"
	reasonNotDownloadable = "not_downloadable"
	reasonTooDeep         = "too_deep"
	reasonTooMany         = "too_many"
)

// planEntry is the OpenAPI schema PluginPlanEntry plus what a job needs.
type planEntry struct {
	Source      upstream.Source  `json:"source"`
	ProjectID   string           `json:"project_id"`
	Slug        string           `json:"slug"`
	Title       string           `json:"title"`
	VersionID   string           `json:"version_id"`
	VersionName string           `json:"version_name"`
	Channel     upstream.Channel `json:"channel"`
	Filename    string           `json:"filename"`
	Size        int64            `json:"size"`
	RequiredBy  *string          `json:"required_by"`

	file            upstream.File
	replaces        string // installed jar this entry replaces (version change, update)
	replacesEnabled bool
	replacesSHA512  string // its sha512 when planned: a jar changed since then is not touched
	replacesVersion string // its version name (audit "from" of a version change)
}

type alreadyEntry struct {
	Source    upstream.Source `json:"source"`
	ProjectID string          `json:"project_id"`
	Title     string          `json:"title"`
	File      string          `json:"file"`
}

type externalEntry struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type incompatibleEntry struct {
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// resolution is the OpenAPI schema PluginResolution.
type resolution struct {
	Install      []planEntry         `json:"install"`
	Already      []alreadyEntry      `json:"already"`
	External     []externalEntry     `json:"external"`
	Incompatible []incompatibleEntry `json:"incompatible"`
}

func newResolution() resolution {
	return resolution{Install: []planEntry{}, Already: []alreadyEntry{}, External: []externalEntry{}, Incompatible: []incompatibleEntry{}}
}

type resolveRequest struct {
	Source           upstream.Source
	Project, Version string // Version "" = newest compatible
}

// planner resolves required dependencies against the installed projects.
type planner struct {
	up        *upstream.Client
	kind      kindInfo
	gv        string
	installed map[string]installedJar // projectKey -> jar
	seen      map[string]bool         // projectKey of every project already looked at
	calls     int                     // upstream requests so far (maxUpstreamCalls)
}

// spend books one upstream request; false once the budget of the resolution is used up.
func (p *planner) spend() bool {
	if p.calls >= maxUpstreamCalls {
		return false
	}
	p.calls++
	return true
}

func newPlanner(h *Handler, srv server, gv string, installed map[string]installedJar, seen map[string]bool) *planner {
	if seen == nil {
		seen = map[string]bool{}
	}
	return &planner{up: h.up, kind: srv.Kind, gv: gv, installed: installed, seen: seen}
}

// safeLink keeps only http(s) links of upstream texts (the browser renders them as links).
func safeLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return raw
}

// resolve builds the plan for the requested project (spec §6.3, decision 4). Errors: the
// requested project or version does not fit (errIncompatible), is only external
// (errNotDownloadable), is unknown (upstream.ErrNotFound) or a source is down.
func (h *Handler) resolve(ctx context.Context, srv server, gv string, inv inventory, req resolveRequest) (resolution, error) {
	p := newPlanner(h, srv, gv, installedByProject(inv), nil)
	res := newResolution()
	p.spend() // the first request of the resolution always fits the budget
	proj, err := p.up.Project(ctx, req.Source, req.Project)
	if err != nil {
		return resolution{}, err
	}
	if p.kind.isMod() && proj.ClientOnly() {
		return resolution{}, fmt.Errorf("%w: %s only runs on the client", errIncompatible, proj.Title)
	}
	key := projectKey(proj.Source, proj.ID)
	j, installed := p.installed[key]
	already := func() (resolution, error) {
		res.Already = append(res.Already, alreadyEntry{Source: proj.Source, ProjectID: proj.ID, Title: proj.Title, File: j.File})
		return res, nil
	}
	if installed && req.Version == "" {
		return already() // whether or not a version still fits (spec §6.3)
	}
	v, err := p.rootVersion(ctx, proj, req.Version)
	if err != nil {
		return resolution{}, err
	}
	p.seen[key] = true
	root := p.entry(proj, v, nil)
	if installed {
		if j.Version != nil && j.Version.ID == v.ID {
			return already()
		}
		root.replaces, root.replacesEnabled, root.replacesSHA512 = j.File, j.Enabled, j.sha512
		if j.Version != nil {
			root.replacesVersion = j.Version.Name
		}
	}
	deps, err := p.dependencies(ctx, &res, v, proj.Source, proj.Title)
	if err != nil {
		return resolution{}, err
	}
	res.Install = append(deps, root)
	return res, nil
}

// rootVersion picks the version of the requested project: want, or the newest compatible
// release, else the newest compatible version.
func (p *planner) rootVersion(ctx context.Context, proj upstream.Project, want string) (upstream.Version, error) {
	if want == "" {
		v, reason, err := p.pick(ctx, proj.Source, proj.ID)
		switch {
		case err != nil:
			return upstream.Version{}, err
		case reason == reasonNotDownloadable:
			return upstream.Version{}, fmt.Errorf("%w: %s offers compatible versions only as external downloads", errNotDownloadable, proj.Title)
		case reason != "":
			return upstream.Version{}, fmt.Errorf("%w: %s has no version for Minecraft %s", errIncompatible, proj.Title, p.gv)
		}
		return v, nil
	}
	v, err := p.up.Version(ctx, proj.Source, proj.ID, want)
	if err != nil {
		return upstream.Version{}, err
	}
	if v.ProjectID != proj.ID || !compatible(v, p.kind, p.gv) {
		return upstream.Version{}, fmt.Errorf("%w: version %s of %s does not fit this server", errIncompatible, want, proj.Title)
	}
	if !v.Downloadable() {
		return upstream.Version{}, fmt.Errorf("%w: version %s of %s is only available externally", errNotDownloadable, want, proj.Title)
	}
	return v, nil
}

// pick returns the newest compatible downloadable release, else the newest compatible
// downloadable version; reason tells why nothing fits.
func (p *planner) pick(ctx context.Context, src upstream.Source, projectID string) (upstream.Version, string, error) {
	external := false
	for _, releaseOnly := range []bool{true, false} {
		if !p.spend() {
			return upstream.Version{}, reasonTooMany, nil
		}
		vs, err := p.up.Versions(ctx, src, projectID, upstream.VersionFilter{Loaders: p.kind.Loaders, GameVersion: p.gv, ReleaseOnly: releaseOnly})
		if err != nil {
			return upstream.Version{}, "", err
		}
		for _, v := range vs {
			if !compatible(v, p.kind, p.gv) {
				continue
			}
			if !v.Downloadable() {
				external = true
				continue
			}
			return v, "", nil
		}
	}
	if external {
		return upstream.Version{}, reasonNotDownloadable, nil
	}
	return upstream.Version{}, reasonNoVersion, nil
}

func (p *planner) entry(proj upstream.Project, v upstream.Version, requiredBy *string) planEntry {
	return planEntry{
		Source: proj.Source, ProjectID: proj.ID, Slug: proj.Slug, Title: proj.Title,
		VersionID: v.ID, VersionName: v.Name, Channel: v.Channel,
		Filename: upstream.FileName(v.File.Name, proj.Slug, v.Name), Size: v.File.Size,
		RequiredBy: requiredBy, file: *v.File,
	}
}

// dependencies plans the required dependencies of v breadth-first (depth ≤ maxDepth, plan ≤
// maxPackages including the requesting project) and returns them in install order, the deepest
// first. Installed, external and incompatible dependencies go into res.
func (p *planner) dependencies(ctx context.Context, res *resolution, v upstream.Version, src upstream.Source, parent string) ([]planEntry, error) {
	type item struct {
		dep    upstream.Dependency
		parent string
		depth  int
	}
	var queue []item
	enqueue := func(deps []upstream.Dependency, parent string, depth int) {
		if len(deps) > maxVersionDeps {
			res.Incompatible = append(res.Incompatible, incompatibleEntry{
				Title: fmt.Sprintf("%d more dependencies of %s", len(deps)-maxVersionDeps, parent), Reason: reasonTooMany,
			})
			deps = deps[:maxVersionDeps]
		}
		for _, d := range deps {
			queue = append(queue, item{d, parent, depth})
		}
	}
	enqueue(v.Dependencies, parent, 1)
	var planned []planEntry
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		e, next, err := p.dependency(ctx, res, it.dep, src, it.parent, it.depth, 1+len(planned))
		if err != nil {
			return nil, err
		}
		if e == nil {
			continue
		}
		planned = append(planned, *e)
		enqueue(next, e.Title, it.depth+1)
	}
	slices.Reverse(planned)
	return planned, nil
}

// dependency plans one dependency; a nil entry means nothing to install (seen, installed,
// external or incompatible). planned is the size of the plan so far.
func (p *planner) dependency(ctx context.Context, res *resolution, d upstream.Dependency, src upstream.Source, parent string, depth, planned int) (*planEntry, []upstream.Dependency, error) {
	name := cmp.Or(d.Name, d.ProjectID, d.VersionID)
	incompatible := func(title, reason string) (*planEntry, []upstream.Dependency, error) {
		res.Incompatible = append(res.Incompatible, incompatibleEntry{Title: title, Reason: reason})
		return nil, nil, nil
	}
	if d.ProjectID == "" && d.VersionID == "" {
		res.External = append(res.External, externalEntry{Name: cmp.Or(d.Name, "unknown"), URL: safeLink(d.ExternalURL)})
		return nil, nil, nil
	}
	// Limits and what is known locally first: no upstream call for a dependency that cannot be
	// planned anyway.
	switch {
	case depth > maxDepth:
		return incompatible(name, reasonTooDeep)
	case planned >= maxPackages:
		return incompatible(name, reasonTooMany)
	}
	if d.ProjectID != "" && p.seen[projectKey(src, d.ProjectID)] {
		return nil, nil, nil
	}
	var fixed *upstream.Version
	if d.VersionID != "" {
		if !p.spend() {
			return incompatible(name, reasonTooMany)
		}
		v, err := p.up.Version(ctx, src, d.ProjectID, d.VersionID)
		switch {
		case errors.Is(err, upstream.ErrNotFound):
			return incompatible(name, reasonNoVersion)
		case err != nil:
			return nil, nil, err
		}
		fixed = &v
		d.ProjectID = cmp.Or(d.ProjectID, v.ProjectID)
	}
	key := projectKey(src, d.ProjectID)
	if p.seen[key] {
		return nil, nil, nil // a cycle or a dependency shared by two projects
	}
	p.seen[key] = true
	if j, ok := p.installed[key]; ok {
		title := j.File
		if j.Project != nil {
			title = cmp.Or(j.Project.Title, j.File)
		}
		res.Already = append(res.Already, alreadyEntry{Source: src, ProjectID: d.ProjectID, Title: title, File: j.File})
		return nil, nil, nil
	}
	if !p.spend() {
		return incompatible(name, reasonTooMany)
	}
	proj, err := p.up.Project(ctx, src, d.ProjectID)
	switch {
	case errors.Is(err, upstream.ErrNotFound):
		return incompatible(name, reasonNoVersion)
	case err != nil:
		return nil, nil, err
	}
	if p.kind.isMod() && proj.ClientOnly() {
		return incompatible(proj.Title, reasonClientOnly)
	}
	var v upstream.Version
	if fixed != nil {
		v = *fixed
		switch {
		case v.ProjectID != proj.ID || !compatible(v, p.kind, p.gv):
			return incompatible(proj.Title, reasonNoVersion)
		case !v.Downloadable():
			return incompatible(proj.Title, reasonNotDownloadable)
		}
	} else {
		var reason string
		v, reason, err = p.pick(ctx, src, proj.ID)
		if err != nil {
			return nil, nil, err
		}
		if reason != "" {
			return incompatible(proj.Title, reason)
		}
	}
	requiredBy := parent
	e := p.entry(proj, v, &requiredBy)
	return &e, v.Dependencies, nil
}
