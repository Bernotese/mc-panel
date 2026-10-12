package files

import (
	"archive/zip"
	"bytes"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// metaOf builds a jar from entries (makeZip, extract_test.go) and reads its metadata.
func metaOf(t *testing.T, entries ...zipEntry) *filesapi.JarMeta {
	t.Helper()
	b := makeZip(t, entries...)
	return readJarMeta(bytes.NewReader(b), int64(len(b)), maxJarEntries)
}

func wantMeta(t *testing.T, name string, got, want *filesapi.JarMeta) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s: %+v, want nil", name, *got)
	case want != nil && (got == nil || *got != *want):
		t.Errorf("%s: %+v, want %+v", name, got, *want)
	}
}

func TestJarMetaPlugins(t *testing.T) {
	plugin := func(name, version string) *filesapi.JarMeta {
		return &filesapi.JarMeta{Kind: filesapi.JarKindPlugin, Name: name, Version: version}
	}
	for name, c := range map[string]struct {
		entries []zipEntry
		want    *filesapi.JarMeta
	}{
		"plugin.yml": {[]zipEntry{{name: "plugin.yml", body: "\xef\xbb\xbf# LuckPerms\r\nname: LuckPerms\r\nversion: \"5.5.71\" # build 1\r\n" +
			"main: me.lucko.luckperms.bukkit.loader.BukkitLoaderPlugin\r\ncommands:\r\n  name: nested\r\n  version: 0\r\nname: Second\r\n"}},
			plugin("LuckPerms", "5.5.71")},
		"single quotes and comment": {[]zipEntry{{name: "plugin.yml", body: "version: 2.21.0-dev+123 # git\nname: 'Essentials X'\n"}},
			plugin("Essentials X", "2.21.0-dev+123")},
		"no version": {[]zipEntry{{name: "plugin.yml", body: "name: Tiny\n"}}, plugin("Tiny", "")},
		"paper-plugin.yml first": {[]zipEntry{
			{name: "plugin.yml", body: "name: Legacy\nversion: 1\n"},
			{name: "paper-plugin.yml", body: "name: Modern\nversion: 2\n"},
		}, plugin("Modern", "2")},
		"plugin before mod": {[]zipEntry{
			{name: "fabric.mod.json", body: `{"id":"hybrid","version":"1"}`},
			{name: "plugin.yml", body: "name: Hybrid\nversion: 1\n"},
		}, plugin("Hybrid", "1")},
		"no name":          {[]zipEntry{{name: "plugin.yml", body: "version: 1\n"}}, nil},
		"empty name":       {[]zipEntry{{name: "plugin.yml", body: "name:\nversion: 1\n"}}, nil},
		"only nested name": {[]zipEntry{{name: "plugin.yml", body: "commands:\n  name: x\n"}}, nil},
		"in a subfolder":   {[]zipEntry{{name: "x/plugin.yml", body: "name: Nested\n"}}, nil},
		"no metadata":      {[]zipEntry{{name: "a.class", body: "cafebabe"}}, nil},
	} {
		wantMeta(t, name, metaOf(t, c.entries...), c.want)
	}
}

func TestJarMetaFabric(t *testing.T) {
	mod := func(id, name, version string) *filesapi.JarMeta {
		return &filesapi.JarMeta{Kind: filesapi.JarKindMod, ID: id, Name: name, Version: version}
	}
	for name, c := range map[string]struct {
		body string
		want *filesapi.JarMeta
	}{
		"complete": {`{"schemaVersion":1,"id":"lithium","name":"Lithium","version":"mc1.21.4-0.14.3","depends":{"fabricloader":">=0.16"}}`,
			mod("lithium", "Lithium", "mc1.21.4-0.14.3")},
		"with BOM":       {"\xef\xbb\xbf" + `{"id":"fabric-api","version":"0.110.0"}`, mod("fabric-api", "", "0.110.0")},
		"no id":          {`{"name":"Nameless","version":"1"}`, nil},
		"number version": {`{"id":"x","version":1}`, nil},
		"broken JSON":    {`{"id":"x",`, nil},
	} {
		wantMeta(t, name, metaOf(t, zipEntry{name: "fabric.mod.json", body: c.body}), c.want)
	}
}

const neoToml = `modLoader="javafml"
loaderVersion="[4,)"
license="MIT"

[[mods]]
modId="examplemod"
displayName="Example Mod"
version="${file.jarVersion}"

[[mods]]
modId="second"
displayName="Second"
version="9"
`

func TestJarMetaNeoForge(t *testing.T) {
	mod := func(version string) *filesapi.JarMeta {
		return &filesapi.JarMeta{Kind: filesapi.JarKindMod, ID: "examplemod", Name: "Example Mod", Version: version}
	}
	manifest := "Manifest-Version: 1.0\r\nImplementation-Title: examplemod\r\nimplementation-version: 2.0.1\r\n\r\nName: x/\r\nImplementation-Version: 9.9\r\n"
	for name, c := range map[string]struct {
		entries []zipEntry
		want    *filesapi.JarMeta
	}{
		"jarVersion from the manifest": {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: neoToml}, {name: "META-INF/MANIFEST.MF", body: manifest}}, mod("2.0.1")},
		"continuation line": {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: neoToml},
			{name: "META-INF/MANIFEST.MF", body: "Manifest-Version: 1.0\nImplementation-Version: 2.0.1-very-lo\n ng\n"}}, mod("2.0.1-very-long")},
		"no manifest":         {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: neoToml}}, mod("")},
		"manifest without it": {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: neoToml}, {name: "META-INF/MANIFEST.MF", body: "Manifest-Version: 1.0\n\nName: a\nImplementation-Version: 3\n"}}, mod("")},
		"literal version":     {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: strings.Replace(neoToml, "${file.jarVersion}", "1.2.3", 1)}}, mod("1.2.3")},
		"fabric first": {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: neoToml}, {name: "fabric.mod.json", body: `{"id":"both","version":"1"}`}},
			&filesapi.JarMeta{Kind: filesapi.JarKindMod, ID: "both", Version: "1"}},
		"no mods":     {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: "modLoader=\"javafml\"\n"}}, nil},
		"broken toml": {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: "[[mods]\nmodId=\n"}}, nil},
		"no modId":    {[]zipEntry{{name: "META-INF/neoforge.mods.toml", body: "[[mods]]\ndisplayName=\"X\"\n"}}, nil},
	} {
		wantMeta(t, name, metaOf(t, c.entries...), c.want)
	}
}

func TestJarMetaCleansValues(t *testing.T) {
	m := metaOf(t, zipEntry{name: "plugin.yml", body: "name: Evil\x1b[31mRed\u202e\xff\x07\nversion: " + strings.Repeat("9", 300) + "\n"})
	if m == nil || m.Name != "Evil[31mRed" {
		t.Fatalf("control characters: %+v", m)
	}
	if len(m.Version) != maxMetaValue {
		t.Fatalf("version cut to %d characters, want %d", len(m.Version), maxMetaValue)
	}
	m = metaOf(t, zipEntry{name: "fabric.mod.json", body: `{"id":"x","name":"` + strings.Repeat("é", 200) + `"}`})
	if m == nil || utf8.RuneCountInString(m.Name) != maxMetaValue || !utf8.ValidString(m.Name) {
		t.Fatalf("multi-byte name: %+v", m)
	}
}

// Review focus 5: broken jars, oversized metadata, a deflate bomb in plugin.yml and a huge
// central directory are nil - never an error, never a large allocation.
func TestJarMetaRefusesBrokenAndHostileJars(t *testing.T) {
	good := makeZip(t, zipEntry{name: "plugin.yml", body: "name: Good\n"})
	for name, b := range map[string][]byte{
		"empty":       {},
		"not a zip":   []byte("PK\x03\x04 and nothing else"),
		"truncated":   good[:len(good)-10],
		"honest 64k+": makeZip(t, zipEntry{name: "plugin.yml", body: "name: Big\n" + strings.Repeat("#", maxMetaBytes)}),
		"stored lie":  rawZip(t, "plugin.yml", []byte("name: Lie\n"+strings.Repeat("a", 1<<20)), 64, zip.Store, 0),
		"cd bomb":     cdBomb(1000),
	} {
		if m := readJarMeta(bytes.NewReader(b), int64(len(b)), 100); m != nil {
			t.Errorf("%s: %+v, want nil", name, *m)
		}
	}

	bomb := rawZip(t, "plugin.yml", []byte("name: Bomb\n"+strings.Repeat("a", 32<<20)), 64, zip.Deflate, 0)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	m := readJarMeta(bytes.NewReader(bomb), int64(len(bomb)), maxJarEntries)
	runtime.ReadMemStats(&after)
	if m != nil {
		t.Fatalf("deflate bomb: %+v, want nil", *m)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 4<<20 {
		t.Fatalf("reading a bomb allocated %d MiB", grew>>20)
	}
}

func TestJarMetaEntryLimit(t *testing.T) {
	if maxJarEntries != 100_000 || maxMetaBytes != 64<<10 || maxMetaValue != 128 {
		t.Fatal("limits differ from spec 3c §5.1")
	}
	b := makeZip(t, zipEntry{name: "plugin.yml", body: "name: Many\n"}, zipEntry{name: "a.class"}, zipEntry{name: "b.class"})
	if m := readJarMeta(bytes.NewReader(b), int64(len(b)), 3); m == nil || m.Name != "Many" {
		t.Fatalf("at the limit: %+v", m)
	}
	if m := readJarMeta(bytes.NewReader(b), int64(len(b)), 2); m != nil {
		t.Fatalf("above the limit: %+v, want nil", *m)
	}
}

// manyEntries is a jar with plugin.yml and n-1 further empty entries (stored: cheap to build).
func manyEntries(t *testing.T, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("plugin.yml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("name: Many\n")); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < n; i++ {
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: "c/" + strconv.Itoa(i), Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Through the service: metadata next to checksums, also for jars above the checksum limit,
// nil for hostile jars without failing the list, and cached with the checksums.
func TestJarsMetaIsBestEffort(t *testing.T) {
	s, data := newService(t)
	s.jarHashMax = 1 << 10
	var h countingHook
	s.jarHook = h.hook
	putBytes(t, data, "plugins/Good.jar", makeZip(t, zipEntry{name: "plugin.yml", body: "name: Good\nversion: 1.0\n"}))
	putBytes(t, data, "plugins/Big.jar.disabled", makeZip(t,
		zipEntry{name: "plugin.yml", body: "name: Big\nversion: 2\n"},
		zipEntry{name: "pad.bin", body: randomHex(4 << 10)})) // incompressible: above jarHashMax
	putBytes(t, data, "plugins/Bomb.jar", rawZip(t, "plugin.yml", []byte("name: Bomb\n"+strings.Repeat("a", 4<<20)), 64, zip.Deflate, 0))
	putBytes(t, data, "plugins/Broken.jar", []byte("not a zip"))
	putBytes(t, data, "plugins/Many.jar", manyEntries(t, maxJarEntries+1))

	res := listJars(t, s, filesapi.DirPlugins)
	if want := []string{"Big.jar.disabled", "Bomb.jar", "Broken.jar", "Good.jar", "Many.jar"}; strings.Join(jarNames(res), ",") != strings.Join(want, ",") {
		t.Fatalf("jars %v, want %v", jarNames(res), want)
	}
	good, big := jarByName(t, res, "Good.jar"), jarByName(t, res, "Big.jar.disabled")
	if good.Meta == nil || *good.Meta != (filesapi.JarMeta{Kind: filesapi.JarKindPlugin, Name: "Good", Version: "1.0"}) || good.SHA512 == "" {
		t.Fatalf("Good.jar: %+v", good)
	}
	if big.Meta == nil || big.Meta.Name != "Big" || big.SHA512 != "" || big.Enabled {
		t.Fatalf("Big.jar.disabled: %+v, want metadata without checksums", big)
	}
	for _, n := range []string{"Bomb.jar", "Broken.jar", "Many.jar"} {
		if j := jarByName(t, res, n); j.Meta != nil {
			t.Errorf("%s: meta %+v, want nil", n, *j.Meta)
		}
	}

	// The answer holds a copy: changing it does not change the cache, and the second listing
	// computes nothing.
	good.Meta.Name = "changed"
	again := listJars(t, s, filesapi.DirPlugins)
	if m := jarByName(t, again, "Good.jar").Meta; m == nil || m.Name != "Good" {
		t.Fatalf("cached meta changed: %+v", m)
	}
	for _, n := range []string{"Good.jar", "Big.jar.disabled", "Bomb.jar", "Broken.jar", "Many.jar"} {
		if c := h.count(n); c != 1 {
			t.Errorf("%s computed %d times, want 1", n, c)
		}
	}
}
