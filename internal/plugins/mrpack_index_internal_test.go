package plugins

import (
	"encoding/json"
	"strings"
	"testing"
)

var hashA = strings.Repeat("a", 128)

func validIndex() map[string]any {
	return map[string]any{
		"formatVersion": 1, "game": "minecraft", "versionId": "1.0.0", "name": "Pack",
		"dependencies": map[string]any{"minecraft": "1.21.11", "fabric-loader": "0.17.2"},
		"files": []any{
			map[string]any{"path": "mods/fabric-api.jar", "hashes": map[string]any{"sha512": strings.ToUpper(hashA), "sha1": strings.Repeat("b", 40)},
				"env": map[string]any{"client": "required", "server": "required"}, "downloads": []any{"https://cdn.modrinth.com/x"}, "fileSize": 10},
			map[string]any{"path": "config/x.json", "hashes": map[string]any{"sha512": strings.Repeat("c", 128)}},
		},
		"futureField": true,
	}
}

func parse(t *testing.T, v map[string]any) (mrpackIndex, error) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return parseIndex(b)
}

func TestParseIndexValid(t *testing.T) {
	ix, err := parse(t, validIndex())
	if err != nil {
		t.Fatal(err)
	}
	if ix.Name != "Pack" || ix.VersionID != "1.0.0" || ix.Minecraft != "1.21.11" || ix.Loader != "fabric-loader" || ix.LoaderVersion != "0.17.2" {
		t.Fatalf("index = %+v", ix)
	}
	if len(ix.Mods) != 1 || ix.Ignored != 1 {
		t.Fatalf("mods = %+v, ignored = %d", ix.Mods, ix.Ignored)
	}
	m := ix.Mods[0]
	if m.Name != "fabric-api.jar" || m.SHA512 != hashA || m.Server != "required" {
		t.Fatalf("mod = %+v", m)
	}
}

func TestParseIndexIgnoredPaths(t *testing.T) {
	v := validIndex()
	v["files"] = append(v["files"].([]any),
		map[string]any{"path": "mods/sub/x.jar", "hashes": map[string]any{"sha512": strings.Repeat("d", 128)}},
		map[string]any{"path": "mods/readme.txt", "hashes": map[string]any{"sha512": strings.Repeat("e", 128)}},
		map[string]any{"path": "resourcepacks/r.zip", "hashes": map[string]any{"sha512": strings.Repeat("f", 128)}},
		map[string]any{"path": "mods/Weird Name [1.0].jar", "hashes": map[string]any{"sha512": strings.Repeat("1", 128)}},
	)
	ix, err := parse(t, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Mods) != 2 || ix.Ignored != 4 {
		t.Fatalf("mods = %d, ignored = %d", len(ix.Mods), ix.Ignored)
	}
	if ix.Mods[1].Name != "Weird Name [1.0].jar" {
		t.Fatalf("name = %q", ix.Mods[1].Name)
	}
}

func TestParseIndexInvalid(t *testing.T) {
	file := func(path, hash string) map[string]any {
		return map[string]any{"path": path, "hashes": map[string]any{"sha512": hash}}
	}
	withFiles := func(fs ...any) func(map[string]any) {
		return func(v map[string]any) { v["files"] = fs }
	}
	many := make([]any, maxMrpackFiles+1)
	for i := range many {
		many[i] = file("config/"+strings.Repeat("x", i%50)+".txt", strings.Repeat("0", 120)+strings.Repeat("1", 8))
	}
	cases := map[string]func(map[string]any){
		"format 2":           func(v map[string]any) { v["formatVersion"] = 2 },
		"no format":          func(v map[string]any) { delete(v, "formatVersion") },
		"game":               func(v map[string]any) { v["game"] = "terraria" },
		"empty name":         func(v map[string]any) { v["name"] = "" },
		"long name":          func(v map[string]any) { v["name"] = strings.Repeat("n", 129) },
		"long version":       func(v map[string]any) { v["versionId"] = strings.Repeat("v", 129) },
		"no files":           func(v map[string]any) { delete(v, "files") },
		"too many files":     withFiles(many...),
		"dotdot":             withFiles(file("mods/../x.jar", hashA)),
		"dotdot start":       withFiles(file("../mods/x.jar", hashA)),
		"absolute":           withFiles(file("/mods/x.jar", hashA)),
		"backslash":          withFiles(file(`mods\x.jar`, hashA)),
		"nul":                withFiles(file("mods/x\x00.jar", hashA)),
		"control":            withFiles(file("mods/x\n.jar", hashA)),
		"empty path":         withFiles(file("", hashA)),
		"long path":          withFiles(file("mods/"+strings.Repeat("x", 251)+".jar", hashA)),
		"dot segment":        withFiles(file("mods/./x.jar", hashA)),
		"empty segment":      withFiles(file("mods//x.jar", hashA)),
		"no sha512":          withFiles(map[string]any{"path": "mods/x.jar", "hashes": map[string]any{"sha1": strings.Repeat("b", 40)}}),
		"short sha512":       withFiles(file("mods/x.jar", "abc")),
		"non-hex sha512":     withFiles(file("mods/x.jar", strings.Repeat("g", 128))),
		"bad sha1":           withFiles(map[string]any{"path": "mods/x.jar", "hashes": map[string]any{"sha512": hashA, "sha1": "xyz"}}),
		"duplicate hash":     withFiles(file("mods/x.jar", hashA), file("mods/y.jar", strings.ToUpper(hashA))),
		"duplicate name":     withFiles(file("mods/x.jar", hashA), file("mods/X.JAR", strings.Repeat("b", 128))),
		"bad env":            withFiles(map[string]any{"path": "mods/x.jar", "hashes": map[string]any{"sha512": hashA}, "env": map[string]any{"server": "maybe"}}),
		"bad env client":     withFiles(map[string]any{"path": "mods/x.jar", "hashes": map[string]any{"sha512": hashA}, "env": map[string]any{"client": "yes"}}),
		"no dependencies":    func(v map[string]any) { delete(v, "dependencies") },
		"no minecraft":       func(v map[string]any) { v["dependencies"] = map[string]any{"fabric-loader": "0.17.2"} },
		"unknown dependency": func(v map[string]any) { v["dependencies"] = map[string]any{"minecraft": "1.21", "rift": "1"} },
		"bad version":        func(v map[string]any) { v["dependencies"] = map[string]any{"minecraft": "1.21 ; rm"} },
		"two loaders": func(v map[string]any) {
			v["dependencies"] = map[string]any{"minecraft": "1.21", "fabric-loader": "1", "neoforge": "2"}
		},
		"files not array":   func(v map[string]any) { v["files"] = "x" },
		"hashes not object": withFiles(map[string]any{"path": "mods/x.jar", "hashes": "x"}),
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			v := validIndex()
			mutate(v)
			if _, err := parse(t, v); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, err := parseIndex([]byte("[]")); err == nil {
		t.Fatal("array accepted")
	}
	if _, err := parseIndex([]byte("{")); err == nil {
		t.Fatal("broken JSON accepted")
	}
}

func TestParseIndexWithoutLoader(t *testing.T) {
	v := validIndex()
	v["dependencies"] = map[string]any{"minecraft": "1.21.11"}
	ix, err := parse(t, v)
	if err != nil {
		t.Fatal(err)
	}
	if ix.Loader != "" {
		t.Fatalf("loader = %q", ix.Loader)
	}
}
