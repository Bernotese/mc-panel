package instancespec_test

import (
	"encoding/json"
	"path"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// Paper and Purpur sit behind mc-router, so every player has the router's address: the
// patch must switch Bukkit's connection throttle off, and the env must point at it.
func TestPatchesDisableBukkitConnectionThrottle(t *testing.T) {
	for _, typ := range []instancespec.Type{instancespec.TypePaper, instancespec.TypePurpur} {
		s := validSpec()
		s.Type = typ
		env, err := instancespec.Env("survival", s)
		if err != nil {
			t.Fatal(err)
		}
		var file string
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, "PATCH_DEFINITIONS="); ok {
				file = v
			}
		}
		dir, name := path.Split(file)
		if dir != instancespec.PatchesDir+"/" {
			t.Fatalf("%s: PATCH_DEFINITIONS = %q, want a file in %s", typ, file, instancespec.PatchesDir)
		}
		content, ok := instancespec.Patches()[name]
		if !ok {
			t.Fatalf("%s: %s is not among the patches %v", typ, name, instancespec.Patches())
		}
		var def struct {
			File string `json:"file"`
			Ops  []map[string]struct {
				Path      string          `json:"path"`
				Value     json.RawMessage `json:"value"`
				ValueType string          `json:"value-type"`
			} `json:"ops"`
		}
		dec := json.NewDecoder(strings.NewReader(content))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&def); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if def.File != "/data/bukkit.yml" || len(def.Ops) != 1 {
			t.Fatalf("%s: %+v", name, def)
		}
		set, ok := def.Ops[0]["$set"]
		if !ok || set.Path != "$.settings.connection-throttle" || string(set.Value) != "-1" || set.ValueType != "int" {
			t.Fatalf("%s: op = %+v", name, def.Ops[0])
		}
	}
}

func TestPatchesFileNames(t *testing.T) {
	p := instancespec.Patches()
	if len(p) == 0 {
		t.Fatal("no patches")
	}
	for name := range p {
		if strings.ContainsAny(name, `/\`) || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			t.Errorf("unsafe patch file name %q", name)
		}
	}
	// A fresh map: callers cannot change what the next caller gets.
	for name := range p {
		delete(p, name)
	}
	if len(instancespec.Patches()) == 0 {
		t.Fatal("Patches returned the shared map")
	}
}
