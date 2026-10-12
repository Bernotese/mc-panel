package upstream

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// hangarPlatform is the only Hangar platform the panel installs for (Paper and Purpur).
const hangarPlatform = "PAPER"

type hProject struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Namespace struct {
		Owner string `json:"owner"`
		Slug  string `json:"slug"`
	} `json:"namespace"`
	Stats struct {
		Downloads int64 `json:"downloads"`
	} `json:"stats"`
	Description string `json:"description"`
	AvatarURL   string `json:"avatarUrl"`
}

type hSearch struct {
	Pagination struct {
		Count int `json:"count"`
	} `json:"pagination"`
	Result []hProject `json:"result"`
}

type hDownload struct {
	FileInfo *struct {
		Name       string `json:"name"`
		SizeBytes  int64  `json:"sizeBytes"`
		SHA256Hash string `json:"sha256Hash"`
	} `json:"fileInfo"`
	ExternalURL string `json:"externalUrl"`
	DownloadURL string `json:"downloadUrl"`
}

type hDependency struct {
	Name        string `json:"name"`
	ProjectID   *int64 `json:"projectId"`
	Required    bool   `json:"required"`
	ExternalURL string `json:"externalUrl"`
}

type hVersion struct {
	ProjectID int64     `json:"projectId"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	Channel   struct {
		Name string `json:"name"`
	} `json:"channel"`
	Downloads            map[string]hDownload     `json:"downloads"`
	PluginDependencies   map[string][]hDependency `json:"pluginDependencies"`
	PlatformDependencies map[string][]string      `json:"platformDependencies"`
}

type hVersions struct {
	Result []hVersion `json:"result"`
}

// hangarChannel maps the channel "Release" (any case) to release and every other to beta
// (spec §3.1).
func hangarChannel(name string) Channel {
	if strings.EqualFold(name, "release") {
		return Release
	}
	return Beta
}

func (p hProject) project() Project {
	return Project{
		Source: Hangar, ID: strconv.FormatInt(p.ID, 10), Slug: p.Namespace.Slug, Owner: p.Namespace.Owner,
		Title: cmp.Or(p.Name, p.Namespace.Slug), Description: p.Description, IconURL: p.AvatarURL,
	}
}

func (h hVersion) norm() Version {
	v := Version{
		Source: Hangar, ID: h.Name, ProjectID: strconv.FormatInt(h.ProjectID, 10), Name: h.Name,
		Channel: hangarChannel(h.Channel.Name), PublishedAt: h.CreatedAt,
		GameVersions: h.PlatformDependencies[hangarPlatform],
	}
	if d, ok := h.Downloads[hangarPlatform]; ok {
		if d.FileInfo != nil && d.DownloadURL != "" {
			v.File = &File{URL: d.DownloadURL, Name: d.FileInfo.Name, Size: d.FileInfo.SizeBytes, SHA256: strings.ToLower(d.FileInfo.SHA256Hash)}
		} else {
			v.ExternalURL = d.ExternalURL
		}
	}
	for _, d := range h.PluginDependencies[hangarPlatform] {
		if !d.Required {
			continue
		}
		dep := Dependency{Name: d.Name, ExternalURL: d.ExternalURL}
		if d.ProjectID != nil && *d.ProjectID > 0 {
			dep.ProjectID, dep.ExternalURL = strconv.FormatInt(*d.ProjectID, 10), ""
		}
		v.Dependencies = append(v.Dependencies, dep)
	}
	return v
}

func (c *Client) hangarSearch(ctx context.Context, q SearchQuery) (SearchResult, error) {
	v := url.Values{
		"q": {q.Query}, "platform": {hangarPlatform}, "sort": {"-downloads"},
		"offset": {strconv.Itoa(q.Offset)}, "limit": {strconv.Itoa(PageSize)},
	}
	if q.GameVersion != "" {
		v.Set("version", q.GameVersion)
	}
	var resp hSearch
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Hangar+"/api/v1/projects?"+v.Encode(), nil, &resp); err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{Hits: make([]SearchHit, 0, len(resp.Result)), Total: resp.Pagination.Count}
	for _, p := range resp.Result {
		if p.ID <= 0 {
			continue
		}
		pr := p.project()
		out.Hits = append(out.Hits, SearchHit{
			ProjectID: pr.ID, Slug: pr.Slug, Owner: pr.Owner, Title: pr.Title, Description: pr.Description,
			Author: pr.Owner, IconURL: pr.IconURL, Downloads: p.Stats.Downloads,
		})
	}
	return out, nil
}

func (c *Client) hangarProject(ctx context.Context, id string) (Project, error) {
	var p hProject
	if err := c.doJSON(ctx, http.MethodGet, c.bases.Hangar+"/api/v1/projects/"+url.PathEscape(id), nil, &p); err != nil {
		return Project{}, err
	}
	if p.ID <= 0 {
		return Project{}, fmt.Errorf("%w: hangar project %q without an id", ErrUnavailable, id)
	}
	return p.project(), nil
}

func (c *Client) hangarVersions(ctx context.Context, project, gv string, releaseOnly bool) ([]Version, error) {
	v := url.Values{"platform": {hangarPlatform}, "limit": {strconv.Itoa(MaxVersions)}}
	if gv != "" {
		v.Set("platformVersion", gv)
	}
	if releaseOnly {
		v.Set("channel", "Release")
	}
	var resp hVersions
	u := c.bases.Hangar + "/api/v1/projects/" + url.PathEscape(project) + "/versions?" + v.Encode()
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(resp.Result))
	for _, h := range resp.Result {
		if validRef(h.Name) {
			out = append(out, h.norm())
		}
	}
	sortNewestFirst(out)
	return out, nil
}

func (c *Client) hangarVersion(ctx context.Context, project, name string) (Version, error) {
	var h hVersion
	u := c.bases.Hangar + "/api/v1/projects/" + url.PathEscape(project) + "/versions/" + url.PathEscape(name)
	if err := c.doJSON(ctx, http.MethodGet, u, nil, &h); err != nil {
		return Version{}, err
	}
	if !validRef(h.Name) {
		return Version{}, fmt.Errorf("%w: hangar version %q without a valid name", ErrUnavailable, name)
	}
	return h.norm(), nil
}
