package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/config"
)

// socketPath returns a short socket path (macOS limits them to 104 bytes).
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mca")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "a.sock")
}

// devConfig is a -dev config that never touches a real Docker daemon or ./.dev/mc.
func devConfig(t *testing.T, sock, dockerSock string) string {
	t.Helper()
	return fmt.Sprintf("socket = %q\ndocker_socket = %q\ninstances_dir = %q\n", sock, dockerSock, t.TempDir())
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForSocket(t *testing.T, path string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("run returned early: %v", err)
		default:
		}
		if fi, err := os.Lstat(path); err == nil && fi.Mode().Type() == fs.ModeSocket {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s did not appear", path)
}

func TestRunDevServesHealthAndShutsDown(t *testing.T) {
	sock := socketPath(t)
	cfg := writeConfig(t, devConfig(t, sock, filepath.Join(t.TempDir(), "no-docker.sock")))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr syncBuffer
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"-dev", "-config", cfg}, &stdout, &stderr) }()
	waitForSocket(t, sock, done)

	h, err := agent.NewClient(sock).Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.Status != "ok" || h.Firewall != "noop" || h.Version != version || h.Docker != "unavailable" {
		t.Fatalf("health = %+v", h)
	}
	if fi, err := os.Lstat(sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("dev socket mode = %v, %v; want 0600", fi.Mode(), err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	if _, err := os.Lstat(sock); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket still present after shutdown: %v", err)
	}
	for _, want := range []string{"agent started", "agent shutting down"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("log missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestRunPrintsVersion(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"-version"}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != version+"\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{{"-nope"}, {"-dev", "serve"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Errorf("run(%v) = nil, want error", args)
		}
	}
}

func TestRunFailsWhenFirewallUnavailable(t *testing.T) {
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		t.Skip("as root this would modify the host firewall")
	}
	sock := socketPath(t)
	cfg := writeConfig(t, devConfig(t, sock, filepath.Join(t.TempDir(), "no-docker.sock"))+"firewall = \"nftables\"\n")
	err := run(context.Background(), []string{"-dev", "-config", cfg}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "firewall") {
		t.Fatalf("run = %v, want firewall error", err)
	}
	if _, err := os.Lstat(sock); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket must not be created when the firewall fails: %v", err)
	}
}

func TestSocketPerms(t *testing.T) {
	mode, gid, err := socketPerms(config.Agent{Dev: true, PanelUID: 501}, func(string) (*user.User, error) {
		return nil, errors.New("lookup must not run in dev mode")
	})
	if err != nil || mode != 0o600 || gid != -1 {
		t.Fatalf("dev = %v %d %v", mode, gid, err)
	}

	var asked string
	mode, gid, err = socketPerms(config.Agent{PanelUID: 998}, func(uid string) (*user.User, error) {
		asked = uid
		return &user.User{Uid: uid, Gid: "997"}, nil
	})
	if err != nil || mode != 0o660 || gid != 997 || asked != "998" {
		t.Fatalf("prod = %v %d %v (asked %q)", mode, gid, err, asked)
	}

	if _, _, err := socketPerms(config.Agent{PanelUID: 998}, func(string) (*user.User, error) {
		return nil, user.UnknownUserIdError(998)
	}); err == nil {
		t.Fatal("unknown panel uid must fail")
	}
	if _, _, err := socketPerms(config.Agent{PanelUID: 998}, func(string) (*user.User, error) {
		return &user.User{Gid: "staff"}, nil
	}); err == nil {
		t.Fatal("non-numeric gid must fail")
	}
}

func TestUIDFromConfig(t *testing.T) {
	for in, want := range map[int]uint32{0: 0, 998: 998, math.MaxUint32: math.MaxUint32} {
		if got, err := uidFromConfig(in); err != nil || got != want {
			t.Errorf("uidFromConfig(%d) = %d, %v", in, got, err)
		}
	}
	for _, in := range []int{-1, math.MaxUint32 + 1} {
		if _, err := uidFromConfig(in); err == nil {
			t.Errorf("uidFromConfig(%d) must fail", in)
		}
	}
}

func TestRunRefusesSecondStartBeforeTouchingFirewall(t *testing.T) {
	sock := socketPath(t)
	first, err := agent.Listen(sock, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	// firewall = "nftables" would fail (or modify nftables on Linux) if it were set up first.
	cfg := writeConfig(t, devConfig(t, sock, filepath.Join(t.TempDir(), "no-docker.sock"))+"firewall = \"nftables\"\n")
	err = run(context.Background(), []string{"-dev", "-config", cfg}, io.Discard, io.Discard)
	if !errors.Is(err, agent.ErrAlreadyRunning) {
		t.Fatalf("run = %v, want ErrAlreadyRunning", err)
	}
}

func TestMCUserIDs(t *testing.T) {
	noLookup := func(string) (*user.User, error) { return nil, errors.New("lookup must not run") }
	uid, gid, err := mcUserIDs(config.Agent{Dev: true}, -1, noLookup)
	if err != nil || uid != os.Getuid() || gid != os.Getgid() {
		t.Fatalf("dev without mc_user = %d:%d, %v; want own ids", uid, gid, err)
	}
	if _, _, err := mcUserIDs(config.Agent{}, 990, noLookup); err == nil {
		t.Fatal("prod without mc_user must fail")
	}

	var asked string
	uid, gid, err = mcUserIDs(config.Agent{MCUser: "mcpanel-mc"}, 990, func(name string) (*user.User, error) {
		asked = name
		return &user.User{Username: name, Uid: "996", Gid: "995"}, nil
	})
	if err != nil || uid != 996 || gid != 995 || asked != "mcpanel-mc" {
		t.Fatalf("prod = %d:%d, %v (asked %q)", uid, gid, err, asked)
	}

	for name, u := range map[string]*user.User{
		"root uid":        {Uid: "0", Gid: "995"},
		"root gid":        {Uid: "996", Gid: "0"},
		"non-numeric uid": {Uid: "mc", Gid: "995"},
		"non-numeric gid": {Uid: "996", Gid: "staff"},
		"negative uid":    {Uid: "-1", Gid: "995"},
	} {
		if _, _, err := mcUserIDs(config.Agent{MCUser: "mcpanel-mc"}, 990, func(string) (*user.User, error) { return u, nil }); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, _, err := mcUserIDs(config.Agent{MCUser: "mcpanel-mc"}, 990, func(name string) (*user.User, error) {
		return nil, user.UnknownUserError(name)
	}); err == nil || !strings.Contains(err.Error(), "useradd") {
		t.Fatalf("unknown mc_user: %v", err)
	}
	if _, _, err := mcUserIDs(config.Agent{Dev: true, MCUser: "me"}, -1, func(string) (*user.User, error) {
		return &user.User{Uid: "0", Gid: "0"}, nil
	}); err != nil {
		t.Fatalf("dev may run as root (e.g. agent -dev under sudo): %v", err)
	}
}

// TestRunDevManagesInstances runs the whole agent against the fake Docker daemon:
// the background loop creates the router, and an ensure through the real client
// creates the instance container.
func TestRunDevManagesInstances(t *testing.T) {
	d := dockertest.New(t)
	sock := socketPath(t)
	cfg := writeConfig(t, devConfig(t, sock, d.Socket))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"-dev", "-config", cfg}, io.Discard, io.Discard) }()
	waitForSocket(t, sock, done)

	c := agent.NewClient(sock)
	waitFor(t, "router running", func() bool {
		st, err := c.Instances(context.Background())
		return err == nil && st.Docker == agentapi.DockerOK && st.Router.State == agentapi.StateRunning
	})
	if h, err := c.Health(context.Background()); err != nil || h.Docker != agentapi.DockerOK {
		t.Fatalf("health = %+v, %v", h, err)
	}
	err := c.Ensure(context.Background(), agentapi.EnsureRequest{
		ID: 1, Name: "survival", FQDN: "survival.mc.example.com", Revision: 1,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096},
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	waitFor(t, "instance created", func() bool {
		st, err := c.Instances(context.Background())
		return err == nil && len(st.Instances) == 1 && st.Instances[0].State == agentapi.StateCreated && st.Instances[0].Operation == ""
	})
	if ctr, ok := d.Container("mcpanel-mc-1"); !ok || ctr.Config.User != fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()) {
		t.Fatalf("container = %+v, %v; dev mode runs it as the agent's own uid:gid", ctr, ok)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// TestMCUserIDsSeparatesPanelAndContainers: the Minecraft containers must not share
// the panel's uid or primary group (they could read panel.db or the agent socket),
// and with Docker enabled even dev mode refuses root.
func TestMCUserIDsSeparatesPanelAndContainers(t *testing.T) {
	prod := config.Agent{PanelUID: 998, MCUser: "mcpanel-mc"}
	as := func(uid, gid string) func(string) (*user.User, error) {
		return func(name string) (*user.User, error) { return &user.User{Username: name, Uid: uid, Gid: gid}, nil }
	}
	if _, _, err := mcUserIDs(prod, 990, as("998", "995")); err == nil {
		t.Error("mc_user with the panel's uid must be refused")
	}
	if _, _, err := mcUserIDs(prod, 990, as("996", "990")); err == nil {
		t.Error("mc_user with the panel's primary gid must be refused")
	}
	if uid, gid, err := mcUserIDs(prod, 990, as("996", "995")); err != nil || uid != 996 || gid != 995 {
		t.Errorf("separate user = %d:%d, %v", uid, gid, err)
	}
	devDocker := config.Agent{Dev: true, MCUser: "me", DockerSocket: "/var/run/docker.sock"}
	for name, ids := range map[string][2]string{"uid 0": {"0", "20"}, "gid 0": {"501", "0"}} {
		if _, _, err := mcUserIDs(devDocker, -1, as(ids[0], ids[1])); err == nil {
			t.Errorf("dev with Docker, %s: want error", name)
		}
	}
	if _, _, err := mcUserIDs(devDocker, -1, as("501", "20")); err != nil {
		t.Errorf("dev with Docker as a normal user: %v", err)
	}
}

// TestCheckImages: config validation only applies a regex; every configured image
// must also be accepted by the Docker client, or the agent refuses to start.
func TestCheckImages(t *testing.T) {
	if err := checkImages(config.DefaultImages()); err != nil {
		t.Fatalf("default images: %v", err)
	}
	digest := "@sha256:" + strings.Repeat("ab", 32)
	for key, mod := range map[string]func(*config.Images){
		"java8":  func(i *config.Images) { i.Java8 = "Itzg/minecraft-server" + digest },
		"java17": func(i *config.Images) { i.Java17 = "itzg//minecraft-server" + digest },
		"router": func(i *config.Images) { i.Router = "itzg/mc-router/" + digest },
	} {
		imgs := config.DefaultImages()
		mod(&imgs)
		if err := checkImages(imgs); err == nil || !strings.Contains(err.Error(), "images."+key) {
			t.Errorf("%s: checkImages = %v, want an error naming images.%s", key, err, key)
		}
	}
}

// startDevAgent runs the agent in dev mode against the fake Docker daemon d and
// returns its socket and a stop function that waits for run to return cleanly.
func startDevAgent(t *testing.T, d *dockertest.Daemon) (string, func()) {
	t.Helper()
	sock := socketPath(t)
	cfg := writeConfig(t, devConfig(t, sock, d.Socket))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"-dev", "-config", cfg}, io.Discard, io.Discard) }()
	waitForSocket(t, sock, done)
	stopped := false
	stop := func() {
		t.Helper()
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("run did not return after cancel")
		}
	}
	t.Cleanup(stop)
	return sock, stop
}

// TestRunHealthIsCorrectFromFirstRequest: with Docker enabled the agent checks
// Docker once before it serves, so the very first /v1/health already reports
// docker "ok" instead of the "unavailable" of a not yet run background check.
func TestRunHealthIsCorrectFromFirstRequest(t *testing.T) {
	d := dockertest.New(t)
	arrived, release := d.Hold(dockertest.OpPing)
	defer release()
	go func() {
		<-arrived
		time.Sleep(time.Second) // a slow but healthy daemon (below the 2 s ping bound)
		release()
	}()
	sock, stop := startDevAgent(t, d)
	h, err := agent.NewClient(sock).Health(context.Background())
	if err != nil || h.Docker != agentapi.DockerOK {
		t.Fatalf("first health = %+v, %v; want docker ok", h, err)
	}
	stop()
}

// TestRunAnswersBeforePanelTimeoutWhenDockerHangs: with a hanging Docker daemon
// the agent still answers within the panel client's 3 s timeout: GET /v1/instances
// reports docker "unavailable" and a write gets 503 docker_unavailable.
func TestRunAnswersBeforePanelTimeoutWhenDockerHangs(t *testing.T) {
	d := dockertest.New(t)
	sock, stop := startDevAgent(t, d)
	c := agent.NewClient(sock) // the panel's client, agent.DefaultTimeout = 3 s
	waitFor(t, "router running", func() bool {
		st, err := c.Instances(context.Background())
		return err == nil && st.Docker == agentapi.DockerOK && st.Router.State == agentapi.StateRunning
	})

	var releases []func()
	for _, op := range []string{dockertest.OpList, dockertest.OpInspect, dockertest.OpPing} {
		_, release := d.Hold(op)
		releases = append(releases, release)
	}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()
	st, err := c.Instances(context.Background())
	if err != nil {
		t.Fatalf("GET /v1/instances = %v; must answer before the panel's timeout", err)
	}
	if st.Docker != agentapi.DockerUnavailable {
		t.Fatalf("docker = %q, want unavailable", st.Docker)
	}
	err = c.Ensure(context.Background(), agentapi.EnsureRequest{
		ID: 1, Name: "survival", FQDN: "survival.mc.example.com", Revision: 1,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096},
	})
	if !errors.Is(err, agentapi.ErrDockerUnavailable) {
		t.Fatalf("ensure = %v; want 503 docker_unavailable before the panel's timeout", err)
	}
	for _, r := range releases {
		r()
	}
	stop()
}
