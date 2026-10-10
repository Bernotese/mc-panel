package instances_test

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

func summary(name string, labels map[string]string) docker.ContainerSummary {
	return docker.ContainerSummary{ID: "id-" + name, Names: []string{"/" + name}, Labels: labels, State: "running"}
}

func mcLabels(id, name, fqdn string) map[string]string {
	return map[string]string{"mcpanel.instance": id, "mcpanel.revision": "1", "mcpanel.name": name, "mcpanel.fqdn": fqdn}
}

func TestRoutesFromContainers(t *testing.T) {
	got := instances.Routes([]docker.ContainerSummary{
		summary("mcpanel-mc-2", mcLabels("2", "creative", "creative.mc.example.com")),
		summary("mcpanel-mc-1", mcLabels("1", "survival", "survival.mc.example.com")),
		// stopped containers keep their route
		{ID: "id-3", Names: []string{"/mcpanel-mc-3"}, Labels: mcLabels("3", "lobby", "lobby.mc.example.com"), State: "exited"},
		// foreign: label points to another id than the name
		summary("mcpanel-mc-4", mcLabels("5", "evil", "evil.mc.example.com")),
		// foreign: no label at all
		summary("mcpanel-mc-6", map[string]string{"mcpanel.fqdn": "x.mc.example.com"}),
		// fqdn does not belong to the name
		summary("mcpanel-mc-7", mcLabels("7", "seven", "other.mc.example.com")),
		// invalid fqdn
		summary("mcpanel-mc-8", mcLabels("8", "eight", "eight.mc.example.com\n")),
		// duplicate fqdn: lowest id (1) wins
		summary("mcpanel-mc-9", mcLabels("9", "survival", "survival.mc.example.com")),
	})
	want := map[string]string{
		"survival.mc.example.com": "mcpanel-mc-1:25565",
		"creative.mc.example.com": "mcpanel-mc-2:25565",
		"lobby.mc.example.com":    "mcpanel-mc-3:25565",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("Routes = %v, want %v", got, want)
	}
}

func TestRenderRoutes(t *testing.T) {
	b, err := instances.RenderRoutes(map[string]string{
		"survival.mc.example.com": "mcpanel-mc-1:25565",
		"creative.mc.example.com": "mcpanel-mc-2:25565",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"mappings\": {\n    \"creative.mc.example.com\": \"mcpanel-mc-2:25565\",\n    \"survival.mc.example.com\": \"mcpanel-mc-1:25565\"\n  }\n}\n"
	if string(b) != want {
		t.Fatalf("RenderRoutes =\n%s\nwant\n%s", b, want)
	}
	empty, err := instances.RenderRoutes(nil)
	if err != nil || string(empty) != "{\n  \"mappings\": {}\n}\n" {
		t.Fatalf("empty = %q, %v", empty, err)
	}
}

func TestWriteRoutesAtomicAndIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "router")
	first := []byte("{\n  \"mappings\": {}\n}\n")
	changed, err := instances.WriteRoutes(dir, first)
	if err != nil || !changed {
		t.Fatalf("first write = %v, %v", changed, err)
	}
	path := filepath.Join(dir, "routes.json")
	assertFile(t, path, first)
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("dir mode = %v, %v; want 0755 (router runs as uid 65532)", fi.Mode(), err)
	}

	changed, err = instances.WriteRoutes(dir, first)
	if err != nil || changed {
		t.Fatalf("identical write = %v, %v; want unchanged", changed, err)
	}

	second := []byte("{\n  \"mappings\": {\n    \"a.mc.example.com\": \"mcpanel-mc-1:25565\"\n  }\n}\n")
	changed, err = instances.WriteRoutes(dir, second)
	if err != nil || !changed {
		t.Fatalf("second write = %v, %v", changed, err)
	}
	assertFile(t, path, second)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temp files left behind: %v (%v)", entries, err)
	}
}

func TestWriteRoutesRefusesNonFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "router")
	if err := os.MkdirAll(filepath.Join(dir, "routes.json"), 0o755); err != nil { // a directory where the file belongs
		t.Fatal(err)
	}
	if _, err := instances.WriteRoutes(dir, []byte("{}\n")); err == nil {
		t.Fatal("a directory at routes.json must fail")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("unexpected files: %v", entries)
	}
}

func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("%s mode = %v, %v; want 0644", path, fi.Mode(), err)
	}
}
