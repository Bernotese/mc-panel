package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxMrpackFiles = 2000 // file entries of an index (spec §4.1)
	maxMrpackPath  = 255  // bytes of a path
	maxMrpackName  = 128  // characters of name and versionId

	mrpackLoaderFabric   = "fabric-loader"
	mrpackLoaderNeoForge = "neoforge"
)

// mrpackDependencyKeys are the dependency keys an index may name; every one but minecraft is a
// loader.
var mrpackDependencyKeys = map[string]bool{
	"minecraft": true, mrpackLoaderFabric: true, mrpackLoaderNeoForge: true, "forge": true, "quilt-loader": true,
}

// mrpackVersionRe is instancespec's versionRe (not exported there): a dependency version of the
// pack may become the instance version (spec §5, "Server-Version umstellen").
var mrpackVersionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.\-+_]{0,63}$`)

var errInvalidIndex = errors.New("invalid modpack index")

// mrpackMod is a mod entry of a validated index (path mods/<Name>).
type mrpackMod struct {
	Name   string // file name from the pack; not necessarily a valid jar name (see mrpackTarget)
	SHA512 string // lowercase hex
	Server string // env.server: required | optional | unsupported | "" (absent)
}

// mrpackIndex is a validated modrinth.index.json, reduced to what the import uses.
type mrpackIndex struct {
	Name, VersionID string
	Minecraft       string
	Loader          string // dependency key of the loader; "" = none
	LoaderVersion   string
	Mods            []mrpackMod // in pack order
	Ignored         int         // files outside mods/<name>.jar
}

// Wire types of the index: only the evaluated fields; unknown fields are ignored because the
// format may grow (spec §4.1). Pointers tell absent from empty.
type mrpackWireIndex struct {
	FormatVersion *int              `json:"formatVersion"`
	Game          string            `json:"game"`
	VersionID     string            `json:"versionId"`
	Name          string            `json:"name"`
	Files         *[]mrpackWireFile `json:"files"`
	Dependencies  map[string]string `json:"dependencies"`
}

type mrpackWireFile struct {
	Path   string `json:"path"`
	Hashes *struct {
		SHA512 string  `json:"sha512"`
		SHA1   *string `json:"sha1"`
	} `json:"hashes"`
	Env *struct {
		Client string `json:"client"`
		Server string `json:"server"`
	} `json:"env"`
}

func invalidIndex(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidIndex, fmt.Sprintf(format, args...))
}

func isHex(s string, n int) bool {
	return len(s) == n && strings.Trim(strings.ToLower(s), "0123456789abcdef") == ""
}

func validEnv(s string) bool {
	return s == "" || s == "required" || s == "optional" || s == "unsupported"
}

// checkMrpackPath refuses paths that could leave the instance directory or confuse a later
// consumer: absolute, backslashes, control characters, empty, "." and ".." segments.
func checkMrpackPath(p string) ([]string, error) {
	switch {
	case p == "" || len(p) > maxMrpackPath:
		return nil, invalidIndex("path must have 1 to %d bytes", maxMrpackPath)
	case !utf8.ValidString(p):
		return nil, invalidIndex("path is not UTF-8")
	case strings.HasPrefix(p, "/") || strings.Contains(p, `\`):
		return nil, invalidIndex("path %q must be relative and use /", p)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return nil, invalidIndex("path contains a control character")
		}
	}
	segs := strings.Split(p, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return nil, invalidIndex("path %q has an empty, . or .. segment", p)
		}
	}
	return segs, nil
}

// parseIndex decodes and validates a modrinth.index.json (spec §4.1).
func parseIndex(raw []byte) (mrpackIndex, error) {
	var w mrpackWireIndex
	if err := json.Unmarshal(raw, &w); err != nil {
		return mrpackIndex{}, invalidIndex("not a modrinth.index.json object")
	}
	switch {
	case w.FormatVersion == nil || *w.FormatVersion != 1:
		return mrpackIndex{}, invalidIndex("formatVersion must be 1")
	case w.Game != "minecraft":
		return mrpackIndex{}, invalidIndex("game must be minecraft")
	case w.Name == "" || utf8.RuneCountInString(w.Name) > maxMrpackName || !utf8.ValidString(w.Name):
		return mrpackIndex{}, invalidIndex("name must have 1 to %d characters", maxMrpackName)
	case utf8.RuneCountInString(w.VersionID) > maxMrpackName || !utf8.ValidString(w.VersionID):
		return mrpackIndex{}, invalidIndex("versionId must have at most %d characters", maxMrpackName)
	case w.Files == nil:
		return mrpackIndex{}, invalidIndex("files is missing")
	case len(*w.Files) > maxMrpackFiles:
		return mrpackIndex{}, invalidIndex("at most %d files", maxMrpackFiles)
	case w.Dependencies == nil:
		return mrpackIndex{}, invalidIndex("dependencies is missing")
	}
	ix := mrpackIndex{Name: w.Name, VersionID: w.VersionID, Mods: []mrpackMod{}}
	for k, v := range w.Dependencies {
		if !mrpackDependencyKeys[k] {
			return mrpackIndex{}, invalidIndex("unknown dependency %q", k)
		}
		if !mrpackVersionRe.MatchString(v) {
			return mrpackIndex{}, invalidIndex("dependency %s has an invalid version", k)
		}
		if k == "minecraft" {
			ix.Minecraft = v
			continue
		}
		if ix.Loader != "" {
			return mrpackIndex{}, invalidIndex("more than one loader")
		}
		ix.Loader, ix.LoaderVersion = k, v
	}
	if ix.Minecraft == "" {
		return mrpackIndex{}, invalidIndex("dependency minecraft is missing")
	}
	hashes, names := map[string]bool{}, map[string]bool{}
	for _, f := range *w.Files {
		segs, err := checkMrpackPath(f.Path)
		if err != nil {
			return mrpackIndex{}, err
		}
		if f.Hashes == nil || !isHex(f.Hashes.SHA512, 128) {
			return mrpackIndex{}, invalidIndex("%s: hashes.sha512 must be 128 hex characters", f.Path)
		}
		if f.Hashes.SHA1 != nil && !isHex(*f.Hashes.SHA1, 40) {
			return mrpackIndex{}, invalidIndex("%s: hashes.sha1 must be 40 hex characters", f.Path)
		}
		var env struct{ client, server string }
		if f.Env != nil {
			env.client, env.server = f.Env.Client, f.Env.Server
		}
		if !validEnv(env.client) || !validEnv(env.server) {
			return mrpackIndex{}, invalidIndex("%s: env must be required, optional or unsupported", f.Path)
		}
		if len(segs) != 2 || segs[0] != "mods" || !strings.HasSuffix(strings.ToLower(segs[1]), ".jar") {
			ix.Ignored++
			continue
		}
		sum, name := strings.ToLower(f.Hashes.SHA512), segs[1]
		if hashes[sum] {
			return mrpackIndex{}, invalidIndex("%s: the same sha512 is listed twice", f.Path)
		}
		if names[strings.ToLower(name)] {
			return mrpackIndex{}, invalidIndex("%s: the file name is listed twice", f.Path)
		}
		hashes[sum], names[strings.ToLower(name)] = true, true
		ix.Mods = append(ix.Mods, mrpackMod{Name: name, SHA512: sum, Server: env.server})
	}
	return ix, nil
}
