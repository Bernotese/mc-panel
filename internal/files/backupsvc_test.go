package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestInitBackupsStatus(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if st := s.BackupsStatus(); !st.Available || st.Version != "0.19.1" || st.Reason != "" {
		t.Fatalf("status = %+v", st)
	}
	if c := f.callsOf("version"); len(c) != 1 || strings.Join(c[0].Env, " ") != "HOME="+s.backupsDir()+" RESTIC_PROGRESS_FPS=1" {
		t.Fatalf("version calls = %+v", c)
	}

	f.version = "restic 0.16.4 compiled with go1.21 on linux/amd64"
	if st := s.InitBackups(ctx); st.Available || !strings.Contains(st.Reason, "0.16.4") || !strings.Contains(st.Reason, "0.17.0") {
		t.Fatalf("old restic = %+v", st)
	}
	f.version = "something else"
	if st := s.InitBackups(ctx); st.Available || st.Reason == "" {
		t.Fatalf("unparsable version = %+v", st)
	}
	f.fail["version"] = &resticExit{Code: 1, Stderr: "boom\n"}
	if st := s.InitBackups(ctx); st.Available || st.Reason == "" {
		t.Fatalf("failing version = %+v", st)
	}

	// No binary at all (production path that does not exist): unavailable, never a host path.
	s2, _ := newService(t, func(c *Config) { c.ResticBinary = "/nonexistent/restic" })
	st := s2.InitBackups(ctx)
	if st.Available || strings.Contains(st.Reason, "/nonexistent") || st.Reason == "" {
		t.Fatalf("missing binary = %+v", st)
	}
}

// hangingRunner never answers: the version check must give up after resticVersionWait.
type hangingRunner struct{}

func (hangingRunner) Start(ctx context.Context, _ resticCmd) (resticProc, error) {
	p := &fakeProc{done: make(chan struct{}), killed: make(chan struct{})}
	go func() { <-ctx.Done(); p.err = &resticExit{Code: 130}; close(p.done) }()
	return p, nil
}

func TestInitBackupsVersionTimeout(t *testing.T) {
	s, _ := newService(t, func(c *Config) { c.runner = hangingRunner{} })
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond) // stands in for the 10 s limit
	defer cancel()
	if st := s.InitBackups(ctx); st.Available {
		t.Fatalf("hanging restic = %+v", st)
	}
}

func TestRequireBackups(t *testing.T) {
	s, _, _ := newBackupService(t)
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"repos", "keys"} {
		if fi, err := os.Lstat(filepath.Join(s.backupsDir(), sub)); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", sub, fi, err)
		}
	}
	if err := s.requireBackups(0); !errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("id 0: %v", err)
	}

	// Production without backups/ (the agent did not run yet): unavailable, no host path.
	s2, _ := newService(t, func(c *Config) { c.runner = newFakeRestic() })
	s2.InitBackups(context.Background())
	err := s2.requireBackups(1)
	if !errors.Is(err, filesapi.ErrBackupsUnavailable) || strings.Contains(s2.publicMessage(err.Error()), s2.cfg.InstancesDir) {
		t.Fatalf("missing backups/: %v", err)
	}
	// A symlink in place of repos/ is refused, never followed.
	s3, _, _ := newBackupService(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(s3.backupsDir(), "repos")); err != nil {
		t.Fatal(err)
	}
	if err := s3.requireBackups(1); err == nil {
		t.Fatal("symlinked repos/ accepted")
	}
	// Dev creates backups/ itself.
	s4, _ := newService(t, func(c *Config) { c.runner = newFakeRestic(); c.Dev = true })
	s4.InitBackups(context.Background())
	if err := s4.requireBackups(1); err != nil {
		t.Fatalf("dev: %v", err)
	}
	// Without restic: unavailable with the reason.
	s5, _ := newService(t)
	if err := os.Mkdir(s5.backupsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	s5.InitBackups(context.Background())
	if err := s5.requireBackups(1); !errors.Is(err, filesapi.ErrBackupsUnavailable) {
		t.Fatalf("no restic: %v", err)
	}
}

func TestEnsureRepoCreatesKeyAndRepo(t *testing.T) {
	ctx := context.Background()
	s, _, f := newBackupService(t)
	if err := s.requireBackups(1); err != nil {
		t.Fatal(err)
	}
	if s.repoExists(1) {
		t.Fatal("repo before init")
	}
	if err := s.ensureRepo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(s.keyFile(1))
	if err != nil || len(key) != 44 || strings.ContainsRune(string(key), '\n') { // base64 of 32 bytes
		t.Fatalf("key = %q %v", key, err)
	}
	if fi, _ := os.Lstat(s.keyFile(1)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %v", fi.Mode())
	}
	if !s.repoExists(1) || len(f.callsOf("init")) != 1 {
		t.Fatalf("init calls = %d", len(f.callsOf("init")))
	}
	env := strings.Join(f.callsOf("init")[0].Env, " ")
	want := "HOME=" + s.backupsDir() + " RESTIC_PROGRESS_FPS=1 RESTIC_REPOSITORY=" + s.repoDir(1) + " RESTIC_PASSWORD_FILE=" + s.keyFile(1)
	if env != want {
		t.Fatalf("env = %s, want %s", env, want)
	}
	// Second call: nothing new, the key stays.
	if err := s.ensureRepo(ctx, 1); err != nil || len(f.callsOf("init")) != 1 {
		t.Fatalf("second ensureRepo: %v, %d inits", err, len(f.callsOf("init")))
	}
	if again, _ := os.ReadFile(s.keyFile(1)); string(again) != string(key) {
		t.Fatal("key was replaced")
	}
	// Repository without key: refused with a message that names no host path.
	if err := os.Remove(s.keyFile(1)); err != nil {
		t.Fatal(err)
	}
	err = s.ensureRepo(ctx, 1)
	if err == nil || !strings.Contains(err.Error(), "keys/1") || strings.Contains(err.Error(), s.cfg.InstancesDir) {
		t.Fatalf("missing key: %v", err)
	}
}

func TestResticErrMapping(t *testing.T) {
	s, _, _ := newBackupService(t)
	for _, c := range []struct {
		code int
		want error
	}{
		{exitNoRepo, filesapi.ErrNotFound},
		{exitLocked, filesapi.ErrBusy},
	} {
		if err := s.resticErr("snapshots", &resticExit{Code: c.code}); !errors.Is(err, c.want) {
			t.Errorf("exit %d: %v", c.code, err)
		}
	}
	err := s.resticErr("backup", &resticExit{Code: 1, Stderr: "Fatal: unable to open " + s.repoDir(1) + "/config\n"})
	if msg := s.publicMessage(err.Error()); strings.Contains(msg, s.cfg.InstancesDir) || !strings.Contains(msg, "restic backup failed") {
		t.Fatalf("message = %q", msg)
	}
	if err := s.resticErr("tag", &resticExit{Code: exitWrongPassword}); err == nil || errors.Is(err, filesapi.ErrInvalid) {
		t.Fatalf("wrong password = %v (internal error expected)", err)
	}
}
