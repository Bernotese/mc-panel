package plugins

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

const (
	maxQueryLen = 100  // characters of q
	maxOffset   = 1000 // search offset
)

// refRe bounds project and version parameters (spec §6.6).
var refRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,128}$`)

func validRef(s string) bool { return refRe.MatchString(s) }

// sourceParam validates source for the server's kind (hangar only for plugins).
func sourceParam(w http.ResponseWriter, r *http.Request, srv server, raw string) (upstream.Source, bool) {
	src := upstream.Source(raw)
	if !src.Valid() || !srv.Kind.hasSource(src) {
		names := make([]string, 0, len(srv.Kind.Sources))
		for _, s := range srv.Kind.Sources {
			names = append(names, string(s))
		}
		writeValidation(w, r, "source must be one of: "+strings.Join(names, ", "))
		return "", false
	}
	return src, true
}

// refParam validates a required (or, with optional, possibly empty) project/version parameter.
func refParam(w http.ResponseWriter, r *http.Request, name, v string, optional bool) bool {
	if (optional && v == "") || validRef(v) {
		return true
	}
	writeValidation(w, r, name+" must be 1-128 characters of A-Z, a-z, 0-9, . _ + -")
	return false
}

// searchHit is the OpenAPI schema PluginSearchHit.
type searchHit struct {
	Source      upstream.Source `json:"source"`
	ProjectID   string          `json:"project_id"`
	Slug        string          `json:"slug"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Author      string          `json:"author"`
	Downloads   int64           `json:"downloads"`
	IconURL     *string         `json:"icon_url"`
	PageURL     string          `json:"page_url"`
	Installed   bool            `json:"installed"`
}

type searchResult struct {
	Hits   []searchHit `json:"hits"`
	Total  int         `json:"total"`
	Offset int         `json:"offset"`
}

func (h *Handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok || !h.allow(w, r) {
		return
	}
	q := r.URL.Query()
	src, ok := sourceParam(w, r, srv, q.Get("source"))
	if !ok {
		return
	}
	text := q.Get("q")
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxQueryLen {
		writeValidation(w, r, "q must be at most 100 characters")
		return
	}
	offset := 0
	if s := q.Get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 || n > maxOffset {
			writeValidation(w, r, "offset must be between 0 and 1000")
			return
		}
		offset = n
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	res, err := h.up.Search(ctx, src, upstream.SearchQuery{
		Query: text, Offset: offset, ProjectType: srv.Kind.ProjectType, Loaders: srv.Kind.Loaders,
		GameVersion: gv, ServerSideOnly: srv.Kind.isMod(),
	})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	installed := map[string]installedJar{}
	if inv, err := h.inventory(ctx, srv); err == nil {
		installed = installedByProject(inv)
	} else {
		h.log.DebugContext(ctx, "plugins: search without inventory", "server_id", srv.ID, "err", err)
	}
	out := searchResult{Hits: make([]searchHit, 0, len(res.Hits)), Total: res.Total, Offset: offset}
	for _, hit := range res.Hits {
		_, inst := installed[projectKey(src, hit.ProjectID)]
		out.Hits = append(out.Hits, searchHit{
			Source: src, ProjectID: hit.ProjectID, Slug: hit.Slug, Title: hit.Title, Description: hit.Description,
			Author: hit.Author, Downloads: hit.Downloads, IconURL: h.iconURL(srv.ID, hit.IconURL),
			PageURL: pageURL(src, srv.Kind.Kind, hit.Owner, hit.Slug), Installed: inst,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// versionJSON is the OpenAPI schema PluginVersion.
type versionJSON struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Channel      upstream.Channel `json:"channel"`
	PublishedAt  int64            `json:"published_at"`
	Downloadable bool             `json:"downloadable"`
	ExternalURL  *string          `json:"external_url"`
}

type versionList struct {
	Versions []versionJSON `json:"versions"`
}

func (h *Handler) handleVersions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok || !h.allow(w, r) {
		return
	}
	q := r.URL.Query()
	src, ok := sourceParam(w, r, srv, q.Get("source"))
	if !ok || !refParam(w, r, "project", q.Get("project"), false) {
		return
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	vs, err := h.up.Versions(ctx, src, q.Get("project"), upstream.VersionFilter{Loaders: srv.Kind.Loaders, GameVersion: gv})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	out := versionList{Versions: make([]versionJSON, 0, len(vs))}
	for _, v := range vs {
		if !compatible(v, srv.Kind, gv) {
			continue
		}
		vj := versionJSON{ID: v.ID, Name: v.Name, Channel: v.Channel, PublishedAt: v.PublishedAt.UnixMilli(), Downloadable: v.Downloadable()}
		if link := safeLink(v.ExternalURL); !v.Downloadable() && link != "" {
			vj.ExternalURL = &link
		}
		out.Versions = append(out.Versions, vj)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) handleResolve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r, true)
	if !ok || !h.allow(w, r) {
		return
	}
	q := r.URL.Query()
	src, ok := sourceParam(w, r, srv, q.Get("source"))
	if !ok || !refParam(w, r, "project", q.Get("project"), false) || !refParam(w, r, "version", q.Get("version"), true) {
		return
	}
	gv, err := h.gameVersion(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	inv, err := h.inventory(ctx, srv)
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	res, err := h.resolve(ctx, srv, gv, inv, resolveRequest{Source: src, Project: q.Get("project"), Version: q.Get("version")})
	if err != nil {
		h.writeError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
