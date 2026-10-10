package instances_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// TestMissedReloadIsRetried: a SIGHUP that failed must be sent by the next Tick,
// although the routes file on disk did not change any more.
func TestMissedReloadIsRetried(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.m.Tick(ctx) // router created with the empty routes file
	e.d.Fail(dockertest.OpKill, http.StatusInternalServerError, "kill failed")
	e.ensure(t, ensureReq(1, "survival", 1)) // routes change, the reload fails
	if k := e.container(t, "mcpanel-router").Kills; len(k) != 0 {
		t.Fatalf("kills = %v", k)
	}
	e.d.Clear()
	e.m.Tick(ctx)
	if k := e.container(t, "mcpanel-router").Kills; !slices.Equal(k, []string{"SIGHUP"}) {
		t.Fatalf("missed reload not retried: kills = %v", k)
	}
	e.m.Tick(ctx)
	if k := e.container(t, "mcpanel-router").Kills; len(k) != 1 {
		t.Fatalf("applied routes must not be reloaded again: kills = %v", k)
	}
}

// TestStatusIsBounded: a hanging Docker call must not hold GET /v1/instances past
// the server's write timeout; the answer then reports Docker as unavailable.
func TestStatusIsBounded(t *testing.T) {
	e := newEnv(t, func(c *instances.Config) { c.StatusTimeout = 300 * time.Millisecond })
	e.ensure(t, ensureReq(1, "survival", 1))
	_, release := e.d.Hold(dockertest.OpInspect)
	defer release()
	start := time.Now()
	st := e.m.Status(context.Background())
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("Status took %v", d)
	}
	if st.Docker != agentapi.DockerUnavailable || st.Instances == nil || len(st.Instances) != 0 || st.Router.State != agentapi.StateMissing {
		t.Fatalf("status after timeout = %+v, want docker unavailable and no partial data", st)
	}
}

// TestPrivateDirectoriesAreEnforced: instances/ and deleted/ are 0700 on every
// ensure/remove, also when they existed with looser modes.
func TestPrivateDirectoriesAreEnforced(t *testing.T) {
	e := newEnv(t)
	for _, d := range []string{"instances", "deleted"} {
		p := filepath.Join(e.dir, d)
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.ensure(t, ensureReq(1, "survival", 1))
	if got := perm(t, filepath.Join(e.dir, "instances")); got != 0o700 {
		t.Fatalf("instances/ = %v after ensure, want 0700", got)
	}
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	if got := perm(t, filepath.Join(e.dir, "deleted")); got != 0o700 {
		t.Fatalf("deleted/ = %v after remove, want 0700", got)
	}
	if err := os.Chmod(filepath.Join(e.dir, "instances"), 0o777); err != nil {
		t.Fatal(err)
	}
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	if got := perm(t, filepath.Join(e.dir, "instances")); got != 0o700 {
		t.Fatalf("instances/ = %v after remove, want 0700", got)
	}
}

// TestSymlinkedDataDirIsRefused: instances/<id> replaced by a symlink makes ensure
// fail without touching the link target.
func TestSymlinkedDataDirIsRefused(t *testing.T) {
	e := newEnv(t)
	target := t.TempDir()
	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.dir, "instances"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, instances.DataDir(e.dir, 1)); err != nil {
		t.Fatal(err)
	}
	e.ensure(t, ensureReq(1, "survival", 1))
	if s := e.status(t, 1); s.State != agentapi.StateMissing || s.LastError == "" {
		t.Fatalf("status = %+v, want a failed ensure", s)
	}
	if got := perm(t, target); got != 0o700 {
		t.Fatalf("symlink target changed to %v", got)
	}
}

// TestListFileSymlinkIsReplaced: ops.json pointing outside the data directory is
// replaced by a regular file; the link target stays as it was.
func TestListFileSymlinkIsReplaced(t *testing.T) {
	e := newEnv(t)
	data := instances.DataDir(e.dir, 1)
	if err := os.MkdirAll(data, 0o750); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ops.json", "whitelist.json"} {
		if err := os.Symlink(outside, filepath.Join(data, name)); err != nil {
			t.Fatal(err)
		}
	}
	e.ensure(t, ensureReq(1, "survival", 1))
	if s := e.status(t, 1); s.LastError != "" {
		t.Fatalf("last_error = %q", s.LastError)
	}
	if b, err := os.ReadFile(outside); err != nil || string(b) != "keep" {
		t.Fatalf("link target = %q, %v", b, err)
	}
	for _, name := range []string{"ops.json", "whitelist.json"} {
		p := filepath.Join(data, name)
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s = %v, %v; want a regular file", name, fi, err)
		}
		if b, _ := os.ReadFile(p); string(b) != "[]\n" {
			t.Fatalf("%s = %q", name, b)
		}
	}
}

// TestListFileDirectoryIsMovedAside: a container must not be able to block its own
// recreate by turning ops.json into a directory.
func TestListFileDirectoryIsMovedAside(t *testing.T) {
	e := newEnv(t)
	data := instances.DataDir(e.dir, 1)
	if err := os.MkdirAll(filepath.Join(data, "ops.json"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "ops.json", "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.ensure(t, ensureReq(1, "survival", 1))
	if s := e.status(t, 1); s.LastError != "" || s.State != agentapi.StateCreated {
		t.Fatalf("status = %+v, want the container created", s)
	}
	if _, err := os.Stat(filepath.Join(data, "ops.json.blocked-20261010T120000Z", "x")); err != nil {
		t.Fatalf("directory not moved aside: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(data, "ops.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ops.json still there: %v", err)
	}
	if !strings.Contains(e.logs.String(), "ops.json") {
		t.Fatal("moving a blocked list file aside must be logged")
	}
}

// TestErrorOnlyEntriesAreEvicted: instances that only keep a last_error must not
// make the agent busy forever once 256 of them exist.
func TestErrorOnlyEntriesAreEvicted(t *testing.T) {
	e := newEnv(t)
	for id := int64(1); id <= instances.MaxTrackedInstances; id++ {
		e.power(t, id, agentapi.PowerStart) // fails: no container, keeps a last_error
	}
	if err := e.m.Power(context.Background(), instances.MaxTrackedInstances+1, agentapi.PowerStart); err != nil {
		t.Fatalf("257th instance = %v, want the oldest error-only entry evicted", err)
	}
	e.m.Wait()
	var ids []int64
	for _, s := range e.m.Status(context.Background()).Instances {
		ids = append(ids, s.ID)
	}
	if slices.Contains(ids, 1) || !slices.Contains(ids, 2) || !slices.Contains(ids, instances.MaxTrackedInstances+1) {
		t.Fatalf("tracked ids = %v; want 1 evicted, 2 and 257 kept", ids)
	}
}

// TestEnsureNeverDowngrades: an ensure with an older revision than the container's
// label is a no-op, even with recreate.
func TestEnsureNeverDowngrades(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 3))
	before := e.container(t, "mcpanel-mc-1")
	old := ensureReq(1, "survival", 2)
	old.Recreate = true
	old.Spec.MemoryMiB = 2048
	e.ensure(t, old)
	after := e.container(t, "mcpanel-mc-1")
	if after.ID != before.ID || after.Config.Labels["mcpanel.revision"] != "3" {
		t.Fatalf("downgraded to an older revision: %+v", after)
	}
}

// TestRouterTemplateChangeReplacesRouter: a changed router template (same image)
// replaces the container once; afterwards it stays.
func TestRouterTemplateChangeReplacesRouter(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.m.Tick(ctx)
	r := e.container(t, "mcpanel-router")
	m2 := instances.New(e.config(func(c *instances.Config) {
		c.Customize = func(name string, cfg *docker.ContainerConfig) {
			if name == instances.RouterName {
				cfg.HostConfig.Memory, cfg.HostConfig.MemorySwap = 128<<20, 128<<20
			}
		}
	}))
	defer m2.Close()
	m2.Tick(ctx)
	r2 := e.container(t, "mcpanel-router")
	if r2.ID == r.ID || r2.Config.HostConfig.Memory != 128<<20 || r2.Config.Labels["mcpanel.router-template"] == "" || r2.Status != "running" {
		t.Fatalf("router not replaced after a template change: %+v", r2)
	}
	m2.Tick(ctx)
	if r3 := e.container(t, "mcpanel-router"); r3.ID != r2.ID {
		t.Fatal("router replaced again without a change")
	}
}

// TestRouterImageNormalisationKeepsRouter: Docker may report the image as
// "docker.io/…"; that must not replace the router on every Tick.
func TestRouterImageNormalisationKeepsRouter(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.m.Tick(ctx)
	r := e.container(t, "mcpanel-router")
	e.d.SetImage("mcpanel-router", "docker.io/"+routerPinned)
	e.m.Tick(ctx)
	e.m.Tick(ctx)
	if r2 := e.container(t, "mcpanel-router"); r2.ID != r.ID {
		t.Fatalf("router replaced because of the image spelling: %+v", r2)
	}
}

// TestForeignNetworkIsNeverAdopted: a network called mcpanel-mc without mc-panel's
// label is not used; ensure fails with last_error.
func TestForeignNetworkIsNeverAdopted(t *testing.T) {
	e := newEnv(t)
	e.d.AddNetwork("mcpanel-mc", "bridge", map[string]string{"com.example": "other"})
	e.ensure(t, ensureReq(1, "survival", 1))
	if s := e.status(t, 1); s.State != agentapi.StateMissing || !strings.Contains(s.LastError, "not managed") {
		t.Fatalf("status = %+v", s)
	}
	if _, ok := e.d.Container("mcpanel-mc-1"); ok {
		t.Fatal("container created in a foreign network")
	}
}

// TestCheckInstancesDir: at startup the agent refuses an instances_dir that is a
// symlink or (outside dev mode) below a path an unprivileged user could change.
func TestCheckInstancesDir(t *testing.T) {
	base := t.TempDir()
	uid := uint32(os.Getuid())
	mk := func(name string, mode os.FileMode) string {
		t.Helper()
		p := filepath.Join(base, name)
		if err := os.MkdirAll(p, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ok := mk("mc", 0o755)
	if err := instances.CheckInstancesDir(ok, true, uid); err != nil {
		t.Fatalf("clean dir: %v", err)
	}
	if err := instances.CheckInstancesDir(filepath.Join(ok, "not-yet"), true, uid); err != nil {
		t.Fatalf("missing dir below a clean path: %v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(ok, link); err != nil {
		t.Fatal(err)
	}
	for _, strict := range []bool{true, false} {
		if err := instances.CheckInstancesDir(link, strict, uid); err == nil {
			t.Errorf("symlink (strict=%v) must be refused", strict)
		}
	}
	writable := mk("gw", 0o775)
	if err := instances.CheckInstancesDir(writable, true, uid); err == nil {
		t.Error("group-writable instances_dir must be refused")
	}
	if err := instances.CheckInstancesDir(filepath.Join(writable, "mc"), true, uid); err == nil {
		t.Error("instances_dir below a group-writable directory must be refused")
	}
	if err := instances.CheckInstancesDir(writable, false, uid); err != nil {
		t.Errorf("dev mode skips the ownership checks: %v", err)
	}
	if uid != 0 {
		if err := instances.CheckInstancesDir(ok, true, uid+1); err == nil {
			t.Error("directory owned by an untrusted user must be refused")
		}
	}
}
