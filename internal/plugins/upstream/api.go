package upstream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

func errSource(src Source) error { return fmt.Errorf("%w: unknown source %q", ErrNotFound, src) }

// cacheKey is a collision-free cache key for any JSON-encodable value.
func cacheKey(parts ...any) string {
	b, _ := json.Marshal(parts) // only strings, bools, ints and string slices
	return string(b)
}

// Search runs a search (cached 5 min).
func (c *Client) Search(ctx context.Context, src Source, q SearchQuery) (SearchResult, error) {
	if !src.Valid() {
		return SearchResult{}, errSource(src)
	}
	return c.search.Get(ctx, cacheKey(src, q), func(ctx context.Context) (SearchResult, error) {
		if src == Hangar {
			return c.hangarSearch(ctx, q)
		}
		return c.modrinthSearch(ctx, q)
	})
}

// Project returns a project by id or slug (cached 15 min).
func (c *Client) Project(ctx context.Context, src Source, id string) (Project, error) {
	if !src.Valid() {
		return Project{}, errSource(src)
	}
	if !validRef(id) {
		return Project{}, fmt.Errorf("%w: invalid project %q", ErrNotFound, id)
	}
	return c.projects.Get(ctx, cacheKey(src, id), func(ctx context.Context) (Project, error) {
		if src == Hangar {
			return c.hangarProject(ctx, id)
		}
		return c.modrinthProject(ctx, id)
	})
}

// ModrinthProjects returns the Modrinth projects with these ids, keyed by id; unknown ids are
// missing from the map. Cached projects (Project, by id) are not asked again; the others are
// fetched MaxProjectBatch at a time and cached like Project.
func (c *Client) ModrinthProjects(ctx context.Context, ids []string) (map[string]Project, error) {
	out := map[string]Project{}
	var missing []string
	seen := map[string]bool{}
	for _, id := range ids {
		if !validRef(id) || seen[id] {
			continue
		}
		seen[id] = true
		if p, ok := c.projects.Peek(cacheKey(Modrinth, id)); ok {
			out[id] = p
			continue
		}
		missing = append(missing, id)
	}
	for chunk := range slices.Chunk(missing, MaxProjectBatch) {
		ps, err := c.modrinthProjects(ctx, chunk)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if !seen[p.ID] {
				continue // not asked for (an answer by slug or a foreign id)
			}
			c.projects.Put(cacheKey(Modrinth, p.ID), p)
			out[p.ID] = p
		}
	}
	return out, nil
}

// Versions returns the versions of project that match f, newest first, at most MaxVersions
// (the full list is cached 15 min).
func (c *Client) Versions(ctx context.Context, src Source, project string, f VersionFilter) ([]Version, error) {
	if !src.Valid() {
		return nil, errSource(src)
	}
	if !validRef(project) {
		return nil, fmt.Errorf("%w: invalid project %q", ErrNotFound, project)
	}
	hangarRelease := src == Hangar && f.ReleaseOnly
	loaders := f.Loaders
	if src == Hangar {
		loaders = nil
	}
	vs, err := c.versions.Get(ctx, cacheKey(src, project, loaders, f.GameVersion, hangarRelease), func(ctx context.Context) ([]Version, error) {
		if src == Hangar {
			return c.hangarVersions(ctx, project, f.GameVersion, hangarRelease)
		}
		return c.modrinthVersions(ctx, project, loaders, f.GameVersion)
	})
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, min(len(vs), MaxVersions))
	for _, v := range vs {
		if f.ReleaseOnly && v.Channel != Release {
			continue
		}
		out = append(out, v)
		if len(out) == MaxVersions {
			break
		}
	}
	return out, nil
}

// Version returns one version (cached 15 min). Modrinth ignores project; Hangar needs it.
func (c *Client) Version(ctx context.Context, src Source, project, id string) (Version, error) {
	if !src.Valid() {
		return Version{}, errSource(src)
	}
	if !validRef(id) || (src == Hangar && !validRef(project)) {
		return Version{}, fmt.Errorf("%w: invalid version %q of %q", ErrNotFound, id, project)
	}
	if src == Modrinth {
		project = ""
	}
	return c.version.Get(ctx, cacheKey(src, project, id), func(ctx context.Context) (Version, error) {
		if src == Hangar {
			return c.hangarVersion(ctx, project, id)
		}
		return c.modrinthVersion(ctx, id)
	})
}

// normHashes returns the valid sha512 hex strings of hs, lower case, sorted and unique.
func normHashes(hs []string) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		h = strings.ToLower(h)
		if len(h) == 128 && strings.Trim(h, "0123456789abcdef") == "" {
			out = append(out, h)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// VersionsByHash maps sha512 hashes to the Modrinth versions they belong to; unknown hashes are
// missing from the map. Known and unknown hashes are cached for an hour; concurrent lookups of
// the same batch share one request.
func (c *Client) VersionsByHash(ctx context.Context, sha512s []string) (map[string]Version, error) {
	out := map[string]Version{}
	var missing []string
	for _, h := range normHashes(sha512s) {
		if e, ok := c.hashes.Peek(h); ok {
			if e.known {
				out[h] = e.v
			}
			continue
		}
		missing = append(missing, h)
	}
	for chunk := range slices.Chunk(missing, MaxHashBatch) {
		sum := sha256.Sum256([]byte(strings.Join(chunk, ",")))
		got, err := c.hashCalls.Get(ctx, hex.EncodeToString(sum[:]), func(ctx context.Context) (map[string]Version, error) {
			return c.modrinthVersionFiles(ctx, chunk)
		})
		if err != nil {
			return nil, err
		}
		for _, h := range chunk {
			v, ok := got[h]
			c.hashes.Put(h, hashEntry{v: v, known: ok})
			if ok {
				out[h] = v
			}
		}
	}
	return out, nil
}

// LatestByHash asks Modrinth for the newest version per hash that matches loaders, game
// versions and channels (not cached: the plugins package caches the result per server).
func (c *Client) LatestByHash(ctx context.Context, sha512s, loaders, gameVersions []string, types []Channel) (map[string]Version, error) {
	out := map[string]Version{}
	ts := make([]string, 0, len(types))
	for _, t := range types {
		ts = append(ts, string(t))
	}
	for chunk := range slices.Chunk(normHashes(sha512s), MaxHashBatch) {
		got, err := c.modrinthLatest(ctx, chunk, loaders, gameVersions, ts)
		if err != nil {
			return nil, err
		}
		for h, v := range got {
			out[h] = v
		}
	}
	return out, nil
}
