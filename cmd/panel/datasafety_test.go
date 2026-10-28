package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/files"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// startFilesService runs a real mcpanel-files service on instancesDir behind the Unix socket
// sock, as cmd/files does at start (recover interrupted restores, clean up temp entries).
func startFilesService(t *testing.T, instancesDir, sock string) (stop func()) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := files.New(files.Config{InstancesDir: instancesDir, Limits: config.DefaultFiles().Limits, Clock: clock.Real{}, Log: log})
	svc.RecoverRestores()
	svc.CleanupTemp(context.Background())
	_ = os.Remove(sock)
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := files.NewServer(svc, "test", log)
	done := make(chan struct{})
	go func() { _ = srv.Serve(l); close(done) }()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-done
		svc.Close()
	}
	t.Cleanup(stop)
	return stop
}

// waitDataState polls the server view until its data_state is want. Each poll advances the
// fake clock by one refill of the API rate limit (defense.APILimit), so polling never runs out
// of requests.
func waitDataState(t *testing.T, c *client, id int64, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var got any
	for time.Now().Before(deadline) {
		if code, out := c.do("GET", fmt.Sprintf("/servers/%d", id), nil); code == 200 {
			if got = out["data_state"]; got == want {
				return
			}
		}
		c.e.clk.Advance(defense.APILimit.Every)
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("data_state of server %d = %v, want %s", id, got, want)
}

// Data safety spec §5 (A02, end to end): a stuck restore journal of the real files service
// blocks start and restart through the panel, across a restart of the files service and of the
// panel; the files service being down makes the state unknown (no start either); resolve moves
// the half-swapped folders out of the data directory and the server starts again.
func TestEndToEndStuckRestoreBlocksStart(t *testing.T) {
	instancesDir := t.TempDir()
	sock := filepath.Join(t.TempDir(), "files.sock")
	stopFiles := startFilesService(t, instancesDir, sock)
	fc := filesapi.NewClient(sock)
	e := newEnvWith(t, func(d *appDeps) { d.Files, d.Backups = fc, fc })
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	srv := admin.must(201, "POST", "/servers", paperServer("survival"))
	id := int64(srv["id"].(float64))
	waitServer(t, admin, id, "running", stateIs("running"))
	waitDataState(t, admin, id, "safe")
	admin.must(202, "POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "stop"})
	waitServer(t, admin, id, "stopped", stateIs("stopped"))

	// what a restore whose swap could not be undone leaves behind (files spec 4 §4.3)
	data := filepath.Join(instancesDir, "instances", strconv.FormatInt(id, 10))
	tmp := ".mcpanel-tmp-restore-e2e"
	for p, body := range map[string]string{
		"server.properties":      "motd=current\n",
		tmp + "/old/level.dat":   "previous world",
		tmp + "/new/level.dat":   "snapshot world",
		"world/level.dat":        "half restored",
		"world/region/r.0.0.mca": "x",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(data, p)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, p), []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	journal := filepath.Join(instancesDir, "backups", "restore-journal")
	if err := os.MkdirAll(journal, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journal, strconv.FormatInt(id, 10)), []byte(`{"tmp":"`+tmp+`","phase":"stuck"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	refused := func(stage string) {
		t.Helper()
		waitDataState(t, admin, id, "recovery_required")
		for _, action := range []string{"start", "restart"} {
			if code, out := admin.do("POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": action}); code != 409 || out["error"] != "recovery_required" {
				t.Fatalf("%s: %s = %d %v", stage, action, code, out)
			}
		}
	}
	refused("stuck journal")

	// the files service goes away: unknown, never safe
	stopFiles()
	waitDataState(t, admin, id, "unknown")
	if code, out := admin.do("POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "start"}); code != 503 || out["error"] != "data_state_unknown" {
		t.Fatalf("start without the files service = %d %v", code, out)
	}
	// ... and comes back: the journal on disk still holds the server
	startFilesService(t, instancesDir, sock)
	refused("after a files restart")
	if _, err := os.Stat(filepath.Join(data, tmp, "old", "level.dat")); err != nil {
		t.Fatalf("the temp cleanup removed the journaled folder: %v", err)
	}

	e.restartApp()
	refused("after a panel restart")

	out := admin.must(200, "POST", fmt.Sprintf("/servers/%d/recovery/resolve", id), nil)
	if out["phase"] != "stuck" || out["folder"] == "" || out["folder"] == nil {
		t.Fatalf("resolve = %v", out)
	}
	if _, err := os.Stat(filepath.Join(data, tmp)); !os.IsNotExist(err) {
		t.Fatalf("the half-swapped folder is still in the data directory: %v", err)
	}
	moved, err := filepath.Glob(filepath.Join(instancesDir, "backups", "recovery", "*", "*", "old", "level.dat"))
	if err != nil || len(moved) != 1 {
		t.Fatalf("moved folders = %v, %v", moved, err)
	}
	if b, err := os.ReadFile(filepath.Join(data, "world", "level.dat")); err != nil || string(b) != "half restored" {
		t.Fatalf("the visible data directory changed: %q %v", b, err)
	}
	if code, again := admin.do("POST", fmt.Sprintf("/servers/%d/recovery/resolve", id), nil); code != 409 || again["error"] != "recovery_required" {
		t.Fatalf("second resolve = %d %v", code, again)
	}

	waitDataState(t, admin, id, "safe")
	admin.must(202, "POST", fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "start"})
	waitServer(t, admin, id, "running", stateIs("running"))
}
