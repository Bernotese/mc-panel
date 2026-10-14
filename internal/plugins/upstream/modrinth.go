package upstream

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Modrinth wire types: only the fields the panel uses (unknown fields are ignored, null leaves
// the zero value).
type mSearch struct {
	Hits []struct {
		ProjectID   string `json:"project_id"`
		Slug        string `json:"slug"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Author      string `json:"author"`
		Downloads   int64  `json:"downloads"`
		IconURL     string `json:"icon_url"`
	} `json:"hits"`
	TotalHits int `json:"total_hits"`
}

type mProject struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Title       string `json:"title"`
	Description string `json:"description"`
	IconURL     string `json:"icon_url"`
	ServerSide  string `json:"server_side"`
}

type mFile struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Primary  bool   `json:"primary"`
	Size     int64  `json:"size"`
	Hashes   struct {
		SHA512 string `json:"sha512"`
	} `json:"hashes"`
}

type mVersion struct {
	ID            string    `json:"id"`
	ProjectID     string    `json:"project_id"`
	VersionNumber string    `json:"version_number"`
	VersionType   string    `json:"version_type"`
	DatePublished time.Time `json:"date_published"`
	GameVersions  []string  `json:"game_versions"`
	Loaders       []string  `json:"loaders"`
	Files         []mFile   `json:"files"`
	Dependencies  []struct {
		ProjectID      string `json:"project_id"`
		VersionID      string `json:"version_id"`
		FileName       string `json:"file_name"`
		DependencyType string `json:"dependency_type"`
	} `json:"dependencies"`
}

func modrinthChannel(t string) Channel {
	switch t {
	case "release":
		return Release
	case "beta":
		return Beta
	}
	return Alpha // unknown types count as the least stable
}

func isModrinthJar(f mFile) bool {
	return f.URL != "" && strings.HasSuffix(strings.ToLower(f.Filename), ".jar")
}

func (f mFile) norm() File {
	return File{URL: f.URL, Name: f.Filename, Size: f.Size, SHA512: strings.ToLower(f.Hashes.SHA512)}
}

// pickModrinthFile returns the primary jar, else the first jar, else nil.
func pickModrinthFile(files []mFile) *File {
	isJar := isModrinthJar
	i := slices.IndexFunc(files, func(f mFile) bool { return f.Primary && isJar(f) })
	if i < 0 {
		i = slices.IndexFunc(files, isJar)
	}
	if i < 0 {
		return nil
	}
	f := files[i].norm()
	return &f
}

func (m mVersion) norm() Version {
	v := Version{
		Source: Modrinth, ID: m.ID, ProjectID: m.ProjectID, Name: cmp.Or(m.VersionNumber, m.ID),
		Channel: modrinthChannel(m.VersionType), PublishedAt: m.DatePublished,
		GameVersions: m.GameVersions, Loaders: m.Loaders, File: pickModrinthFile(m.Files),
	}
	for _, f := range m.Files {
		if isModrinthJar(f) {
			v.Files = append(v.Files, f.norm())
		}
	}
	for _, d := range m.Dependencies {
		if d.DependencyType == "required" {
			v.Dependencies = append(v.Dependencies, Dependency{ProjectID: d.ProjectID, VersionID: d.VersionID, Name: d.FileName})
		}
	}
	return v
}

func (c *Client) modrinthSearch(ctx context.Context, q SearchQuery) (SearchResult, error) {
	facets := [][]string{{"project_type:" + q.ProjectType}}
	if len(q.Loaders) > 0 {
		or := make([]string, 0, len(q.Loaders))
		for _, l := range q.Loaders {
			or = append(or, "categories:"+l)
		}
		facets = append(facets, or)
	}
	if q.GameVersion != "" {
		facets = append(facets, []string{"versions:" + q.GameVersion})
	}
	if q.ServerSideOnly {
		facets = append(facets, []string{"server_side:required", "server_side:optional"})
	}
	fb, err := json.Marshal(facets)
	if err != nil {
		return SearchResult{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	index := "downloads"
	if q.Query != "" {
		index = "relevance"
	}
	v := url.Values{
		"query": {q.Query}, "facets": {string(fb)}, "index": {index},
		"offset": {strconv.Itoa(q.Offset)}, "limit": {strconv.Itoa(PageSize)},
	}
	var resp mSearch
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Modrinth+"/v2/search?"+v.Encode(), nil, &resp); err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{Hits: make([]SearchHit, 0, len(resp.Hits)), Total: resp.TotalHits}
	for _, h := range resp.Hits {
		if !validRef(h.ProjectID) {
			continue
		}
		out.Hits = append(out.Hits, SearchHit{
			ProjectID: h.ProjectID, Slug: h.Slug, Title: h.Title, Description: h.Description,
			Author: h.Author, IconURL: h.IconURL, Downloads: h.Downloads,
		})
	}
	return out, nil
}

func (m mProject) norm() Project {
	return Project{
		Source: Modrinth, ID: m.ID, Slug: m.Slug, Title: cmp.Or(m.Title, m.Slug, m.ID), Description: m.Description,
		IconURL: m.IconURL, ServerSide: m.ServerSide,
	}
}

func (c *Client) modrinthProject(ctx context.Context, id string) (Project, error) {
	var m mProject
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Modrinth+"/v2/project/"+url.PathEscape(id), nil, &m); err != nil {
		return Project{}, err
	}
	if !validRef(m.ID) {
		return Project{}, fmt.Errorf("%w: modrinth project %q without a valid id", ErrUnavailable, id)
	}
	return m.norm(), nil
}

// modrinthProjects fetches several projects by id in one request (unknown ids are left out).
func (c *Client) modrinthProjects(ctx context.Context, ids []string) ([]Project, error) {
	b, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	var resp []mProject
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Modrinth+"/v2/projects?"+url.Values{"ids": {string(b)}}.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(resp))
	for _, m := range resp {
		if validRef(m.ID) {
			out = append(out, m.norm())
		}
	}
	return out, nil
}

func (c *Client) modrinthVersions(ctx context.Context, project string, loaders []string, gv string) ([]Version, error) {
	q := url.Values{}
	if len(loaders) > 0 {
		lb, _ := json.Marshal(loaders)
		q.Set("loaders", string(lb))
	}
	if gv != "" {
		gb, _ := json.Marshal([]string{gv})
		q.Set("game_versions", string(gb))
	}
	u := c.bases.Modrinth + "/v2/project/" + url.PathEscape(project) + "/version"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var resp []mVersion
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(resp))
	for _, m := range resp {
		if validRef(m.ID) {
			out = append(out, m.norm())
		}
	}
	sortNewestFirst(out)
	return out, nil
}

func (c *Client) modrinthVersion(ctx context.Context, id string) (Version, error) {
	var m mVersion
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Modrinth+"/v2/version/"+url.PathEscape(id), nil, &m); err != nil {
		return Version{}, err
	}
	if !validRef(m.ID) {
		return Version{}, fmt.Errorf("%w: modrinth version %q without a valid id", ErrUnavailable, id)
	}
	return m.norm(), nil
}

func (c *Client) modrinthVersionFiles(ctx context.Context, hashes []string) (map[string]Version, error) {
	var resp map[string]mVersion
	in := map[string]any{"hashes": hashes, "algorithm": "sha512"}
	if err := c.doJSON(ctx, http.MethodPost, c.bases.Modrinth+"/v2/version_files", in, &resp); err != nil {
		return nil, err
	}
	return normByHash(resp), nil
}

func (c *Client) modrinthLatest(ctx context.Context, hashes, loaders, gameVersions, types []string) (map[string]Version, error) {
	var resp map[string]mVersion
	in := map[string]any{
		"hashes": hashes, "algorithm": "sha512", "loaders": loaders,
		"game_versions": gameVersions, "version_types": types,
	}
	if err := c.doJSON(ctx, http.MethodPost, c.bases.Modrinth+"/v2/version_files/update", in, &resp); err != nil {
		return nil, err
	}
	return normByHash(resp), nil
}

func normByHash(resp map[string]mVersion) map[string]Version {
	out := make(map[string]Version, len(resp))
	for h, m := range resp {
		if validRef(m.ID) {
			out[strings.ToLower(h)] = m.norm()
		}
	}
	return out
}
