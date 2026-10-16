package instances_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

func assertPatches(t *testing.T, dir string) {
	t.Helper()
	if got := perm(t, dir); got != 0o755 {
		t.Fatalf("%s = %v, want 0755 (the container user reads it)", dir, got)
	}
	want := instancespec.Patches()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != len(want) {
		t.Fatalf("patches dir = %v, %v; want %d files", entries, err, len(want))
	}
	for name, content := range want {
		assertFile(t, filepath.Join(dir, name), []byte(content))
	}
}

// TestPatchesWrittenAtInitAndBeforeEveryStart: the patch definitions sit in a root-owned
// directory outside the data directory, and a create or start restores a removed or
// altered file (an empty bind source would make itzg fail on PATCH_DEFINITIONS).
func TestPatchesWrittenAtInitAndBeforeEveryStart(t *testing.T) {
	e := newEnv(t)
	if err := e.m.Init(); err != nil {
		t.Fatal(err)
	}
	dir := instances.PatchesDir(e.dir)
	assertPatches(t, dir)

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	e.d.AddImage(java21Pinned)
	e.ensure(t, ensureReq(1, "survival", 1))
	assertPatches(t, dir)
	binds := e.container(t, instances.ContainerName(1)).Config.HostConfig.Binds
	if len(binds) != 3 || binds[2] != dir+":"+instancespec.PatchesDir+":ro" {
		t.Fatalf("binds = %v", binds)
	}

	for name := range instancespec.Patches() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.power(t, 1, agentapi.PowerStart)
	assertPatches(t, dir)
	if s := e.status(t, 1); s.LastError != "" {
		t.Fatalf("last_error = %q", s.LastError)
	}
}

func TestWritePatchesIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "patches")
	for range 2 {
		if err := instances.WritePatches(dir); err != nil {
			t.Fatal(err)
		}
		assertPatches(t, dir)
	}
}
