package docker_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
)

const (
	digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	image  = "itzg/minecraft-server:2026.9.2-java21@" + digest
	pinned = "itzg/minecraft-server@" + digest
)

func TestParseImageRef(t *testing.T) {
	r, err := docker.ParseImageRef(image)
	if err != nil || r != (docker.ImageRef{Repo: "itzg/minecraft-server", Tag: "2026.9.2-java21", Digest: digest}) || r.Pinned() != pinned {
		t.Fatalf("ParseImageRef = %+v, %v", r, err)
	}
	r, err = docker.ParseImageRef("registry.example.com:5000/mc/server@" + digest)
	if err != nil || r.Repo != "registry.example.com:5000/mc/server" || r.Tag != "" {
		t.Fatalf("registry with port = %+v, %v", r, err)
	}
	for _, bad := range []string{
		"itzg/minecraft-server:java21",
		"itzg/minecraft-server@sha256:abc",
		"Itzg/minecraft-server@" + digest,
		"itzg/minecraft-server:bad tag@" + digest,
		"@" + digest,
		"itzg//x@" + digest,
		"itzg/minecraft-server@" + digest + "/../x",
	} {
		if _, err := docker.ParseImageRef(bad); err == nil {
			t.Errorf("ParseImageRef(%q) = nil error", bad)
		}
	}
}

func TestPingAndUnavailable(t *testing.T) {
	d := dockertest.New(t)
	ctx := context.Background()
	if err := docker.New(d.Socket).Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	err := docker.New(filepath.Join(t.TempDir(), "missing.sock")).Ping(ctx)
	if !errors.Is(err, docker.ErrUnavailable) {
		t.Fatalf("Ping on a missing socket = %v, want ErrUnavailable", err)
	}
	d.Fail(dockertest.OpPing, http.StatusInternalServerError, "daemon broken")
	err = docker.New(d.Socket).Ping(ctx)
	var de *docker.Error
	if !errors.As(err, &de) || de.StatusCode != 500 || de.Message != "daemon broken" {
		t.Fatalf("Ping on a failing daemon = %v", err)
	}
}

func TestImagePull(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	ctx := context.Background()
	ok, err := c.ImageExists(ctx, image)
	if err != nil || ok {
		t.Fatalf("ImageExists before pull = %v, %v", ok, err)
	}
	if err := c.PullImage(ctx, image); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	if got := d.Pulls(); !slices.Equal(got, []string{pinned}) {
		t.Fatalf("pulls = %v, want fromImage=repo and tag=digest", got)
	}
	if ok, err := c.ImageExists(ctx, image); err != nil || !ok {
		t.Fatalf("ImageExists after pull = %v, %v", ok, err)
	}
	if _, err := c.ImageExists(ctx, "itzg/minecraft-server:java21"); err == nil {
		t.Fatal("unpinned reference must be refused before calling Docker")
	}
	if err := c.PullImage(ctx, "itzg/minecraft-server:java21"); err == nil {
		t.Fatal("unpinned pull must be refused")
	}
	if len(d.Pulls()) != 1 {
		t.Fatalf("unpinned pull reached Docker: %v", d.Pulls())
	}
}

func TestPullErrorInStream(t *testing.T) {
	d := dockertest.New(t)
	d.FailPull("manifest for itzg/minecraft-server not found: manifest unknown")
	err := docker.New(d.Socket).PullImage(context.Background(), image)
	if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("PullImage = %v, want stream error", err)
	}
	if d.HasImage(pinned) {
		t.Fatal("failed pull must not leave the image")
	}
}

func TestEnsureNetworkIsIdempotent(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	ctx := context.Background()
	for range 2 {
		if err := c.EnsureNetwork(ctx, "mcpanel-mc", map[string]string{"mcpanel.network": "1"}); err != nil {
			t.Fatalf("EnsureNetwork: %v", err)
		}
	}
	if !d.HasNetwork("mcpanel-mc") {
		t.Fatal("network not created")
	}
	creates := 0
	for _, call := range d.Calls() {
		if call == "network create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("network created %d times, calls %v", creates, d.Calls())
	}
}

func TestContainerLifecycle(t *testing.T) {
	d := dockertest.New(t)
	d.AddImage(pinned)
	c := docker.New(d.Socket)
	ctx := context.Background()
	if err := c.EnsureNetwork(ctx, "mcpanel-mc", nil); err != nil {
		t.Fatal(err)
	}
	cfg := docker.ContainerConfig{
		Image:       pinned,
		User:        "996:995",
		Env:         []string{"EULA=TRUE"},
		Labels:      map[string]string{"mcpanel.instance": "1"},
		Hostname:    "survival",
		StopTimeout: 60,
		HostConfig: docker.HostConfig{
			Binds: []string{"/srv/mc-panel/instances/1:/data"}, NetworkMode: "mcpanel-mc",
			RestartPolicy: docker.RestartPolicy{Name: "unless-stopped"}, CapDrop: []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges:true"}, Memory: 5 << 30, MemorySwap: 5 << 30, PidsLimit: 1024,
			LogConfig: docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		NetworkingConfig: docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{"mcpanel-mc": {}}},
	}
	id, err := c.CreateContainer(ctx, "mcpanel-mc-1", cfg)
	if err != nil || len(id) != 64 {
		t.Fatalf("CreateContainer = %q, %v", id, err)
	}
	got, _ := d.Container("mcpanel-mc-1")
	if !reflect.DeepEqual(got.Config, cfg) {
		t.Fatalf("Docker received\n%+v\nwant\n%+v", got.Config, cfg)
	}
	if _, err := c.CreateContainer(ctx, "mcpanel-mc-1", cfg); !docker.IsConflict(err) {
		t.Fatalf("duplicate name = %v, want conflict", err)
	}

	for range 2 { // second start: 304 Not Modified is success
		if err := c.Start(ctx, "mcpanel-mc-1"); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	info, err := c.Inspect(ctx, "mcpanel-mc-1")
	if err != nil || info.ID != id || info.Name != "/mcpanel-mc-1" || !info.State.Running ||
		info.State.Status != "running" || info.Config.Labels["mcpanel.instance"] != "1" || info.State.StartedAt != "2026-10-10T12:00:00Z" {
		t.Fatalf("Inspect = %+v, %v", info, err)
	}
	if err := c.Kill(ctx, "mcpanel-mc-1", "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	if err := c.Restart(ctx, "mcpanel-mc-1", 60); err != nil {
		t.Fatal(err)
	}
	for range 2 { // second stop: 304 Not Modified is success
		if err := c.Stop(ctx, "mcpanel-mc-1", 60); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}
	got, _ = d.Container("mcpanel-mc-1")
	if got.Status != "exited" || !slices.Equal(got.Kills, []string{"SIGHUP"}) || !slices.Equal(got.StopTimes, []int{60, 60}) {
		t.Fatalf("container = %+v", got)
	}
	if err := c.Kill(ctx, "mcpanel-mc-1", "SIGHUP"); !docker.IsConflict(err) {
		t.Fatalf("kill on stopped container = %v, want conflict", err)
	}
	if err := c.Remove(ctx, "mcpanel-mc-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Inspect(ctx, "mcpanel-mc-1"); !docker.IsNotFound(err) {
		t.Fatalf("Inspect after remove = %v, want not found", err)
	}
	if err := c.Start(ctx, "mcpanel-mc-1"); !docker.IsNotFound(err) {
		t.Fatalf("Start of a missing container = %v", err)
	}
}

func TestRemoveRunningNeedsForce(t *testing.T) {
	d := dockertest.New(t)
	d.AddContainer("x", docker.ContainerConfig{Image: pinned}, true)
	c := docker.New(d.Socket)
	ctx := context.Background()
	if err := c.Remove(ctx, "x", false); !docker.IsConflict(err) {
		t.Fatalf("Remove running without force = %v", err)
	}
	if err := c.Remove(ctx, "x", true); err != nil {
		t.Fatal(err)
	}
}

func TestListFiltersByLabel(t *testing.T) {
	d := dockertest.New(t)
	d.AddContainer("mcpanel-mc-1", docker.ContainerConfig{Image: pinned, Labels: map[string]string{"mcpanel.instance": "1"}}, true)
	d.AddContainer("mcpanel-mc-2", docker.ContainerConfig{Image: pinned, Labels: map[string]string{"mcpanel.instance": "2"}}, false)
	d.AddContainer("mcpanel-mc-7", docker.ContainerConfig{Image: pinned, Labels: map[string]string{"com.example": "other"}}, true)
	list, err := docker.New(d.Socket).List(context.Background(), "mcpanel.instance")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range list {
		names = append(names, s.Names[0])
		if !s.HasName(strings.TrimPrefix(s.Names[0], "/")) {
			t.Errorf("HasName(%q) = false", s.Names[0])
		}
	}
	if !slices.Equal(names, []string{"/mcpanel-mc-1", "/mcpanel-mc-2"}) {
		t.Fatalf("List = %v; stopped containers must be included, unlabelled ones never", names)
	}
	if list[1].State != "created" || list[1].Labels["mcpanel.instance"] != "2" {
		t.Fatalf("summary = %+v", list[1])
	}
}

func TestStats(t *testing.T) {
	d := dockertest.New(t)
	d.AddContainer("mcpanel-mc-1", docker.ContainerConfig{Image: pinned}, true)
	want := docker.Stats{
		CPUStats:    docker.CPUStats{CPUUsage: docker.CPUUsage{TotalUsage: 400}, SystemCPUUsage: 10000, OnlineCPUs: 4},
		MemoryStats: docker.MemoryStats{Usage: 3 << 30, Limit: 5 << 30, Stats: map[string]uint64{"inactive_file": 1 << 30}},
	}
	d.SetStats("mcpanel-mc-1", want)
	got, err := docker.New(d.Socket).Stats(context.Background(), "mcpanel-mc-1")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Stats = %+v, %v", got, err)
	}
	calls := d.Calls()
	if calls[len(calls)-1] != "stats mcpanel-mc-1" {
		t.Fatalf("calls = %v", calls)
	}
}

// rawServer serves h on a fresh Unix socket (for responses the fake never sends).
func rawServer(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dkr")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "r.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = os.RemoveAll(dir)
	})
	return path
}

func TestRequestShapeAndLimits(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	path := rawServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_, _ = io.WriteString(w, "["+strings.Repeat(" ", 9<<20)+"]")
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	ctx := context.Background()
	// The size limit gets its own, generous timeout: under -race, pushing 9 MiB through
	// the socket can take longer than the 200 ms that prove the hanging-daemon timeout.
	big := docker.NewWithTimeouts(path, 30*time.Second, time.Second)
	if _, err := big.List(ctx, "mcpanel.instance"); !errors.Is(err, docker.ErrUnavailable) || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response = %v", err)
	}
	c := docker.NewWithTimeouts(path, 200*time.Millisecond, time.Second)
	start := time.Now()
	if err := c.Ping(ctx); !errors.Is(err, docker.ErrUnavailable) {
		t.Fatalf("hanging daemon = %v, want ErrUnavailable", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout not applied")
	}
	if err := c.Kill(ctx, "mcpanel-router", "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`GET /v1.44/containers/json?all=1&filters=%7B%22label%22%3A%5B%22mcpanel.instance%22%5D%7D`,
		"GET /v1.44/_ping",
		"POST /v1.44/containers/mcpanel-router/kill?signal=SIGHUP",
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(seen, want) {
		t.Fatalf("requests =\n%q\nwant\n%q", seen, want)
	}
}

// TestEnsureNetworkRefusesForeignNetwork: a network that only shares the name (without
// the requested labels, or with another driver) is never adopted or modified.
func TestEnsureNetworkRefusesForeignNetwork(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	ctx := context.Background()
	labels := map[string]string{"mcpanel.network": "1"}
	d.AddNetwork("mcpanel-mc", "bridge", map[string]string{"com.example": "other"})
	if err := c.EnsureNetwork(ctx, "mcpanel-mc", labels); err == nil || !strings.Contains(err.Error(), "not managed") {
		t.Fatalf("network without label = %v, want refusal", err)
	}
	d.AddNetwork("mcpanel-host", "host", labels)
	if err := c.EnsureNetwork(ctx, "mcpanel-host", labels); err == nil {
		t.Fatal("network with another driver must be refused")
	}
	d.AddNetwork("mcpanel-ok", "bridge", map[string]string{"mcpanel.network": "1", "extra": "x"})
	if err := c.EnsureNetwork(ctx, "mcpanel-ok", labels); err != nil {
		t.Fatalf("own network: %v", err)
	}
	for _, call := range d.Calls() {
		if strings.HasPrefix(call, "network create") {
			t.Fatalf("existing networks must never be re-created: %v", d.Calls())
		}
	}
}
