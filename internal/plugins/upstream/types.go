package upstream

import (
	"regexp"
	"slices"
	"time"
)

// SearchQuery is one search; ProjectType, Loaders and ServerSideOnly apply to Modrinth.
type SearchQuery struct {
	Query          string
	Offset         int
	ProjectType    string   // "plugin" | "mod"
	Loaders        []string // OR-ed categories facet
	GameVersion    string
	ServerSideOnly bool // server_side required OR optional (mods)
}

type SearchHit struct {
	ProjectID, Slug, Owner, Title, Description, Author, IconURL string
	Downloads                                                   int64
}

type SearchResult struct {
	Hits  []SearchHit // never nil
	Total int
}

// Project is a Modrinth or Hangar project; Hangar's ID is its numeric id as text.
type Project struct {
	Source                                       Source
	ID, Slug, Owner, Title, Description, IconURL string
	ServerSide                                   string // Modrinth server_side; "" for Hangar
}

// ClientOnly reports a Modrinth project that does not run on a server.
func (p Project) ClientOnly() bool { return p.ServerSide == "unsupported" }

// File is the downloadable jar of a version.
type File struct {
	URL, Name      string
	Size           int64
	SHA512, SHA256 string // lowercase hex; Modrinth sets SHA512, Hangar SHA256
}

// Dependency is a required dependency of a version.
type Dependency struct {
	ProjectID   string // "" = not a project (external link or Modrinth file name only)
	VersionID   string // Modrinth: exactly this version; "" = newest compatible
	Name        string // Hangar dependency name or Modrinth file_name
	ExternalURL string // Hangar externalUrl
}

type Version struct {
	Source       Source
	ID           string // Modrinth version id, Hangar version name
	ProjectID    string
	Name         string // Modrinth version_number, Hangar name
	Channel      Channel
	PublishedAt  time.Time
	GameVersions []string // Modrinth game_versions, Hangar platformDependencies.PAPER
	Loaders      []string // Modrinth only
	File         *File    // nil = no downloadable jar
	ExternalURL  string   // Hangar: only available externally
	Dependencies []Dependency
}

func (v Version) Downloadable() bool { return v.File != nil }

// VersionFilter selects compatible versions.
type VersionFilter struct {
	Loaders     []string // Modrinth only
	GameVersion string
	ReleaseOnly bool // Hangar: channel=Release; Modrinth: filtered locally
}

// Icon is a fetched project icon.
type Icon struct {
	Data        []byte
	ContentType string
}

// refRe bounds every project, version and hash reference before it becomes part of a URL.
var refRe = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,128}$`)

func validRef(s string) bool { return refRe.MatchString(s) }

func sortNewestFirst(vs []Version) {
	slices.SortStableFunc(vs, func(a, b Version) int { return b.PublishedAt.Compare(a.PublishedAt) })
}
