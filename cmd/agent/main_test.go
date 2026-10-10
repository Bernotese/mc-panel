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
	cfg := writeConfig(t, fmt.Sprintf("socket = %q\n", sock))
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
	if h.Status != "ok" || h.Firewall != "noop" || h.Version != version {
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
	cfg := writeConfig(t, fmt.Sprintf("socket = %q\nfirewall = \"nftables\"\n", sock))
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
	cfg := writeConfig(t, fmt.Sprintf("socket = %q\nfirewall = \"nftables\"\n", sock))
	err = run(context.Background(), []string{"-dev", "-config", cfg}, io.Discard, io.Discard)
	if !errors.Is(err, agent.ErrAlreadyRunning) {
		t.Fatalf("run = %v, want ErrAlreadyRunning", err)
	}
}

func TestMCUserIDs(t *testing.T) {
	noLookup := func(string) (*user.User, error) { return nil, errors.New("lookup must not run") }
	uid, gid, err := mcUserIDs(config.Agent{Dev: true}, noLookup)
	if err != nil || uid != os.Getuid() || gid != os.Getgid() {
		t.Fatalf("dev without mc_user = %d:%d, %v; want own ids", uid, gid, err)
	}
	if _, _, err := mcUserIDs(config.Agent{}, noLookup); err == nil {
		t.Fatal("prod without mc_user must fail")
	}

	var asked string
	uid, gid, err = mcUserIDs(config.Agent{MCUser: "mcpanel-mc"}, func(name string) (*user.User, error) {
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
		if _, _, err := mcUserIDs(config.Agent{MCUser: "mcpanel-mc"}, func(string) (*user.User, error) { return u, nil }); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, _, err := mcUserIDs(config.Agent{MCUser: "mcpanel-mc"}, func(name string) (*user.User, error) {
		return nil, user.UnknownUserError(name)
	}); err == nil || !strings.Contains(err.Error(), "useradd") {
		t.Fatalf("unknown mc_user: %v", err)
	}
	if _, _, err := mcUserIDs(config.Agent{Dev: true, MCUser: "me"}, func(string) (*user.User, error) {
		return &user.User{Uid: "0", Gid: "0"}, nil
	}); err != nil {
		t.Fatalf("dev may run as root (e.g. agent -dev under sudo): %v", err)
	}
}
