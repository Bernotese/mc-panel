package files

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// allCalls returns a copy of every recorded call.
func (f *fakeRestic) allCalls() []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// gomaxprocsOf returns the GOMAXPROCS entries of env.
func gomaxprocsOf(env []string) []string {
	var out []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "GOMAXPROCS=") {
			out = append(out, kv)
		}
	}
	return out
}

func TestResticEnvWithoutBudget(t *testing.T) {
	s, _, f := newBackupService(t)
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	waitJob(t, s, 1)
	calls := f.allCalls()
	if len(calls) == 0 {
		t.Fatal("no restic calls")
	}
	for _, c := range calls {
		if g := gomaxprocsOf(c.Env); len(g) != 0 {
			t.Fatalf("%q: env has %q without restic_gomaxprocs", c.Args, g)
		}
	}
}

func TestResticEnvPassesTheBudget(t *testing.T) {
	s, _, f := newBackupService(t, func(c *Config) { c.Limits.ResticGOMAXPROCS = 2 })
	if _, err := s.CreateBackup(context.Background(), filesapi.CreateBackupRequest{ID: 1, Kind: filesapi.SnapshotManual}); err != nil {
		t.Fatal(err)
	}
	waitJob(t, s, 1)
	cmds := map[string]bool{}
	for _, c := range f.allCalls() {
		cmd, _ := command(c.Args)
		cmds[cmd] = true
		if g := gomaxprocsOf(c.Env); !slices.Equal(g, []string{"GOMAXPROCS=2"}) {
			t.Fatalf("%s: GOMAXPROCS entries = %q, want exactly GOMAXPROCS=2", cmd, g)
		}
	}
	for _, want := range []string{"version", "init", "backup"} {
		if !cmds[want] {
			t.Fatalf("no %s call among %v", want, cmds)
		}
	}
	// Only the allowlist plus the budget: nothing of the service's environment.
	t.Setenv("GOGC", "off")
	env := s.resticEnv(1, "TZ=UTC")
	want := []string{"HOME=" + s.backupsDir(), "RESTIC_PROGRESS_FPS=1", "GOMAXPROCS=2",
		"RESTIC_REPOSITORY=" + s.repoDir(1), "RESTIC_PASSWORD_FILE=" + s.keyFile(1), "TZ=UTC"}
	if !slices.Equal(env, want) {
		t.Fatalf("env = %q\nwant  %q", env, want)
	}
}
