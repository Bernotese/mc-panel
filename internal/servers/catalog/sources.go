package catalog

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// validVersion reports whether instancespec accepts v as a version, so the form never offers
// a value the create request would reject (spaces, control characters, overlong values).
func validVersion(v string) bool {
	if v == "LATEST" {
		return false
	}
	s := instancespec.Spec{Type: instancespec.TypeVanilla, Version: v, Java: instancespec.Java21, MemoryMiB: instancespec.MinMemoryMiB}
	return s.Validate() == nil
}

// versions keeps the valid ids in the given order (first occurrence wins) and derives Java.
func versions(t instancespec.Type, ids []string) []Version {
	out := make([]Version, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if validVersion(id) && !seen[id] {
			seen[id] = true
			out = append(out, Version{ID: id, Java: instancespec.JavaFor(t, id)})
		}
	}
	return out
}

// leadingInt parses the leading digits of s ("4-rc1" -> 4); no digits -> -1.
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return -1
	}
	return n
}

// compareVersions orders dotted versions numerically ("1.21.11" > "1.21.4" > "1.21").
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(pa), len(pb)) {
		if c := cmp.Compare(leadingInt(pa[i]), leadingInt(pb[i])); c != 0 {
			return c
		}
	}
	if c := cmp.Compare(len(pa), len(pb)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func newestFirst(ids []string) []string {
	ids = slices.Clone(ids)
	slices.SortFunc(ids, func(a, b string) int { return compareVersions(b, a) })
	return slices.Compact(ids)
}

// vanilla: releases from the Mojang manifest, in manifest order (newest first).
func (c *Client) vanilla(ctx context.Context) (Catalog, error) {
	var m struct {
		Versions []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"versions"`
	}
	if err := c.getJSON(ctx, c.up.Mojang+"/mc/game/version_manifest_v2.json", &m); err != nil {
		return Catalog{}, err
	}
	var ids []string
	for _, v := range m.Versions {
		if v.Type == "release" {
			ids = append(ids, v.ID)
		}
	}
	return Catalog{Versions: versions(instancespec.TypeVanilla, ids)}, nil
}

// paper: versions with at least one STABLE build (itzg only installs STABLE builds by
// default). Pre-releases ("-rc", "-pre") are skipped; only the newest paperStableChecks
// versions are checked.
func (c *Client) paper(ctx context.Context) (Catalog, error) {
	var resp struct {
		Versions []struct {
			Version struct {
				ID string `json:"id"`
			} `json:"version"`
		} `json:"versions"`
	}
	if err := c.getJSON(ctx, c.up.Paper+"/v3/projects/paper/versions", &resp); err != nil {
		return Catalog{}, err
	}
	var ids []string
	for _, v := range resp.Versions {
		if validVersion(v.Version.ID) && !strings.Contains(v.Version.ID, "-") {
			ids = append(ids, v.Version.ID)
		}
	}
	var keep []string
	for i, id := range newestFirst(ids) {
		if i < paperStableChecks {
			var builds []json.RawMessage
			u := c.up.Paper + "/v3/projects/paper/versions/" + url.PathEscape(id) + "/builds?channel=STABLE"
			if err := c.getJSON(ctx, u, &builds); err != nil {
				return Catalog{}, err
			}
			if len(builds) == 0 {
				continue
			}
		}
		keep = append(keep, id)
	}
	return Catalog{Versions: versions(instancespec.TypePaper, keep)}, nil
}

// purpur: versions up to and including metadata.current (newer ones are experimental).
func (c *Client) purpur(ctx context.Context) (Catalog, error) {
	var resp struct {
		Metadata struct {
			Current string `json:"current"`
		} `json:"metadata"`
		Versions []string `json:"versions"`
	}
	if err := c.getJSON(ctx, c.up.Purpur+"/v2/purpur", &resp); err != nil {
		return Catalog{}, err
	}
	i := slices.Index(resp.Versions, resp.Metadata.Current)
	if i < 0 {
		return Catalog{}, fmt.Errorf("purpur: current version %q missing from the version list", resp.Metadata.Current)
	}
	ids := slices.Clone(resp.Versions[:i+1])
	slices.Reverse(ids) // the API lists oldest first
	return Catalog{Versions: versions(instancespec.TypePurpur, ids)}, nil
}

// fabric: stable game versions and stable loader versions, both in upstream order (newest first).
func (c *Client) fabric(ctx context.Context) (Catalog, error) {
	type item struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	var game, loader []item
	if err := c.getJSON(ctx, c.up.Fabric+"/v2/versions/game", &game); err != nil {
		return Catalog{}, err
	}
	if err := c.getJSON(ctx, c.up.Fabric+"/v2/versions/loader", &loader); err != nil {
		return Catalog{}, err
	}
	var ids []string
	for _, g := range game {
		if g.Stable {
			ids = append(ids, g.Version)
		}
	}
	loaders := []string{}
	for _, l := range loader {
		if l.Stable && validVersion(l.Version) && !slices.Contains(loaders, l.Version) {
			loaders = append(loaders, l.Version)
		}
	}
	return Catalog{Versions: versions(instancespec.TypeFabric, ids), Loaders: loaders}, nil
}

// neoforge: the Minecraft versions that have at least one NeoForge release (no -beta,
// -alpha, +snapshot), newest first. The id is the Minecraft version; itzg picks the newest
// NeoForge build for it when loader_version is null.
func (c *Client) neoforge(ctx context.Context) (Catalog, error) {
	var resp struct {
		Versions []string `json:"versions"`
	}
	if err := c.getJSON(ctx, c.up.NeoForge+"/api/maven/versions/releases/net/neoforged/neoforge", &resp); err != nil {
		return Catalog{}, err
	}
	var ids []string
	for _, v := range resp.Versions {
		if mc, ok := NeoForgeMC(v); ok {
			ids = append(ids, mc)
		}
	}
	return Catalog{Versions: versions(instancespec.TypeNeoForge, newestFirst(ids))}, nil
}

// NeoForgeMC maps a NeoForge release to its Minecraft version: the old scheme A.B.C
// (20 ≤ A ≤ 25) belongs to 1.A.B (1.A if B is 0), the yearly scheme Y.D.H.B (Y ≥ 26) to
// Y.D.H (Y.D if H is 0). Pre-releases (-beta, -alpha, +snapshot) and anything else are
// rejected.
func NeoForgeMC(v string) (string, bool) {
	parts := strings.Split(v, ".")
	n := make([]int, len(parts))
	for i, p := range parts {
		if p == "" || len(p) > 9 || strings.Trim(p, "0123456789") != "" {
			return "", false
		}
		n[i], _ = strconv.Atoi(p)
	}
	switch {
	case len(n) == 3 && n[0] >= 20 && n[0] <= 25:
		if n[1] == 0 {
			return fmt.Sprintf("1.%d", n[0]), true
		}
		return fmt.Sprintf("1.%d.%d", n[0], n[1]), true
	case len(n) == 4 && n[0] >= 26:
		if n[2] == 0 {
			return fmt.Sprintf("%d.%d", n[0], n[1]), true
		}
		return fmt.Sprintf("%d.%d.%d", n[0], n[1], n[2]), true
	}
	return "", false
}
