package plugins

import (
	"context"
	"fmt"
	"net/url"
	"slices"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
)

// kindInfo describes what a server type installs (spec §3.1).
type kindInfo struct {
	Kind        string // "plugin" | "mod"
	Dir         string // filesapi.DirPlugins | filesapi.DirMods
	ProjectType string // Modrinth search facet
	Loaders     []string
	Sources     []upstream.Source
}

var bothSources = []upstream.Source{upstream.Modrinth, upstream.Hangar}

var kinds = map[instancespec.Type]kindInfo{
	instancespec.TypePaper: {Kind: "plugin", Dir: filesapi.DirPlugins, ProjectType: "plugin",
		Loaders: []string{"paper", "spigot", "bukkit"}, Sources: bothSources},
	instancespec.TypePurpur: {Kind: "plugin", Dir: filesapi.DirPlugins, ProjectType: "plugin",
		Loaders: []string{"purpur", "paper", "spigot", "bukkit"}, Sources: bothSources},
	instancespec.TypeFabric: {Kind: "mod", Dir: filesapi.DirMods, ProjectType: "mod",
		Loaders: []string{"fabric"}, Sources: []upstream.Source{upstream.Modrinth}},
	instancespec.TypeNeoForge: {Kind: "mod", Dir: filesapi.DirMods, ProjectType: "mod",
		Loaders: []string{"neoforge"}, Sources: []upstream.Source{upstream.Modrinth}},
}

func kindOf(t instancespec.Type) (kindInfo, bool) {
	k, ok := kinds[t]
	return k, ok
}

func (k kindInfo) hasSource(s upstream.Source) bool { return slices.Contains(k.Sources, s) }

func (k kindInfo) isMod() bool { return k.Kind == "mod" }

// pageURL is the project page (spec §6.6); a Hangar project without known owner links to the
// Hangar start page (its page URL needs the owner).
func pageURL(src upstream.Source, kind, owner, slug string) string {
	if src == upstream.Hangar {
		if owner == "" || slug == "" {
			return "https://hangar.papermc.io/"
		}
		return "https://hangar.papermc.io/" + url.PathEscape(owner) + "/" + url.PathEscape(slug)
	}
	return "https://modrinth.com/" + kind + "/" + url.PathEscape(slug)
}

// compatible reports whether v runs on a server of kind k with game version gv: the game
// version must be listed, and a Modrinth version must name one of the kind's loaders (never the
// project_type, which Modrinth reports as "mod" for plugins as well).
func compatible(v upstream.Version, k kindInfo, gv string) bool {
	if !slices.Contains(v.GameVersions, gv) {
		return false
	}
	if v.Source == upstream.Hangar {
		return true
	}
	return slices.ContainsFunc(v.Loaders, func(l string) bool { return slices.Contains(k.Loaders, l) })
}

// gameVersion is the instance's Minecraft version; LATEST is resolved over the version catalog
// (errCatalog if that fails).
func (h *Handler) gameVersion(ctx context.Context, srv server) (string, error) {
	if srv.Version != "LATEST" {
		return srv.Version, nil
	}
	cat, err := h.catalog.Get(ctx, srv.Type)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errCatalog, err)
	}
	if len(cat.Versions) == 0 {
		return "", fmt.Errorf("%w: empty catalog", errCatalog)
	}
	return cat.Versions[0].ID, nil
}

// iconURL is the icon proxy URL of an upstream icon, or nil if there is none or it is not
// allowed.
func (h *Handler) iconURL(serverID int64, raw string) *string {
	if !h.up.IconURLAllowed(raw) {
		return nil
	}
	s := fmt.Sprintf("/api/servers/%d/plugins/icon?url=%s", serverID, url.QueryEscape(raw))
	return &s
}
