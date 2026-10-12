package files

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	maxJarEntries = 100_000 // central directory of a jar (spec 3c §5.1)
	// maxJarCentralDirBytes bounds the central directory of a jar (real jars with 100 000
	// entries stay well below it); with two hash passes at a time it keeps a hostile jar from
	// costing the service hundreds of MB. Extraction keeps maxCentralDirBytes.
	maxJarCentralDirBytes = 16 << 20
	maxMetaBytes          = 64 << 10 // one metadata file, uncompressed
	maxMetaValue          = 128      // characters per value
	jarVersionVar         = "${file.jarVersion}"

	paperPluginYML = "paper-plugin.yml"
	pluginYML      = "plugin.yml"
	fabricModJSON  = "fabric.mod.json"
	neoForgeTOML   = "META-INF/neoforge.mods.toml"
	manifestMF     = "META-INF/MANIFEST.MF"
)

var errMetaTooLarge = errors.New("metadata file too large")

// readJarMeta reads kind, id, name and version of a plugin or mod from the jar r (size bytes),
// best effort (spec 3c §5.1): the first of paper-plugin.yml, plugin.yml, fabric.mod.json and
// META-INF/neoforge.mods.toml that exists decides (plus MANIFEST.MF for ${file.jarVersion}).
// The central directory is checked before archive/zip reads it (at most maxEntries entries),
// every file is read with a hard limit of maxMetaBytes whatever its header claims. Anything it
// cannot read is nil, never an error.
func readJarMeta(r io.ReaderAt, size, maxEntries int64) *filesapi.JarMeta {
	if err := checkCentralDirectory(r, size, maxEntries, maxJarCentralDirBytes); err != nil {
		return nil
	}
	zr, err := zip.NewReader(r, size)
	if err != nil && (zr == nil || !errors.Is(err, zip.ErrInsecurePath)) {
		return nil // an insecure name elsewhere does not matter: only fixed names are read
	}
	if int64(len(zr.File)) > maxEntries {
		return nil
	}
	files := map[string]*zip.File{}
	for _, zf := range zr.File {
		switch zf.Name {
		case paperPluginYML, pluginYML, fabricModJSON, neoForgeTOML, manifestMF:
			if _, dup := files[zf.Name]; !dup {
				files[zf.Name] = zf
			}
		}
	}
	switch {
	case files[paperPluginYML] != nil:
		return pluginMeta(files[paperPluginYML])
	case files[pluginYML] != nil:
		return pluginMeta(files[pluginYML])
	case files[fabricModJSON] != nil:
		return fabricMeta(files[fabricModJSON])
	case files[neoForgeTOML] != nil:
		return neoForgeMeta(files[neoForgeTOML], files[manifestMF])
	}
	return nil
}

// readEntry reads one metadata file: its declared size and the bytes actually read are both
// bounded by maxMetaBytes (a deflate bomb with a small declared size stops at the limit).
func readEntry(zf *zip.File) ([]byte, error) {
	if zf.UncompressedSize64 > maxMetaBytes {
		return nil, errMetaTooLarge
	}
	rc, err := zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxMetaBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxMetaBytes {
		return nil, errMetaTooLarge
	}
	return bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), nil
}

// pluginMeta reads name and version of a Bukkit/Paper plugin; a plugin without a name is nil.
func pluginMeta(zf *zip.File) *filesapi.JarMeta {
	b, err := readEntry(zf)
	if err != nil {
		return nil
	}
	v := topLevelValues(string(b), "name", "version")
	if v["name"] == "" {
		return nil
	}
	return &filesapi.JarMeta{Kind: filesapi.JarKindPlugin, Name: v["name"], Version: v["version"]}
}

// topLevelValues reads simple top-level "key: value" lines of a YAML file - no YAML parser,
// plugin.yml only needs two scalars. Indented lines (nested keys), comments and lines without
// a colon are skipped; the first line of a key wins. Values are unquoted and cleaned.
func topLevelValues(text string, keys ...string) map[string]string {
	out := make(map[string]string, len(keys))
	for line := range strings.Lines(text) {
		line = strings.TrimRight(line, "\r\n")
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if _, seen := out[key]; seen || !slices.Contains(keys, key) {
			continue
		}
		out[key] = metaValue(unquote(strings.TrimSpace(val)))
	}
	return out
}

// unquote removes YAML quotes ('x' or "x", up to the closing quote) or, for a plain value, a
// trailing " # comment".
func unquote(v string) string {
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		if i := strings.IndexByte(v[1:], q); i >= 0 {
			return v[1 : 1+i]
		}
		return v[1:]
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// fabricMeta reads id, name and version of a Fabric mod; a mod without an id is nil.
func fabricMeta(zf *zip.File) *filesapi.JarMeta {
	b, err := readEntry(zf)
	if err != nil {
		return nil
	}
	var m struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	meta := &filesapi.JarMeta{Kind: filesapi.JarKindMod, ID: metaValue(m.ID), Name: metaValue(m.Name), Version: metaValue(m.Version)}
	if meta.ID == "" {
		return nil
	}
	return meta
}

// neoForgeMeta reads the first [[mods]] entry of a NeoForge mod; "${file.jarVersion}" in its
// version is the Implementation-Version of MANIFEST.MF (empty without one).
func neoForgeMeta(zf, manifest *zip.File) *filesapi.JarMeta {
	b, err := readEntry(zf)
	if err != nil {
		return nil
	}
	var doc struct {
		Mods []struct {
			ModID       string `toml:"modId"`
			DisplayName string `toml:"displayName"`
			Version     string `toml:"version"`
		} `toml:"mods"`
	}
	if _, err := toml.Decode(string(b), &doc); err != nil || len(doc.Mods) == 0 {
		return nil
	}
	m := doc.Mods[0]
	version := m.Version
	if strings.Contains(version, jarVersionVar) {
		impl := ""
		if manifest != nil {
			impl = manifestValue(manifest, "Implementation-Version")
		}
		if impl == "" {
			version = ""
		} else {
			version = strings.ReplaceAll(version, jarVersionVar, impl)
		}
	}
	meta := &filesapi.JarMeta{Kind: filesapi.JarKindMod, ID: metaValue(m.ModID), Name: metaValue(m.DisplayName), Version: metaValue(version)}
	if meta.ID == "" {
		return nil
	}
	return meta
}

// manifestValue returns an attribute of the main section of MANIFEST.MF (names are
// case-insensitive, continuation lines start with one space); "" if missing or unreadable.
func manifestValue(zf *zip.File, name string) string {
	b, err := readEntry(zf)
	if err != nil {
		return ""
	}
	text := strings.ReplaceAll(strings.ReplaceAll(string(b), "\r\n", "\n"), "\r", "\n")
	text = strings.ReplaceAll(text, "\n ", "")
	for line := range strings.Lines(text) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			break // end of the main section
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// metaValue makes a value from a jar safe to show: invalid UTF-8, control characters (Cc)
// and format characters (Cf, e.g. bidi overrides) removed, trimmed, at most maxMetaValue
// characters.
func metaValue(v string) string {
	v = strings.ToValidUTF8(v, "")
	v = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, v)
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > maxMetaValue {
		v = strings.TrimSpace(string([]rune(v)[:maxMetaValue]))
	}
	return v
}
