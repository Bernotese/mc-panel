package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// newExecRunner resolves the configured binary: an absolute path is taken as is (a missing
// file shows up in the version check), a bare name is looked up in PATH (dev).
func newExecRunner(bin string) (resticRunner, string) {
	switch {
	case bin == "":
		return nil, "restic is not configured (restic_binary)"
	case filepath.IsAbs(bin):
		return execRunner{bin: bin}, ""
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return nil, "restic was not found in PATH"
	}
	return execRunner{bin: p}, ""
}

// InitBackups checks the restic binary once (spec 4 §3.2: `restic version`, at most
// resticVersionWait, at least filesapi.MinResticVersion) and remembers the result for
// BackupsStatus. The reason never names a host path.
func (s *Service) InitBackups(ctx context.Context) filesapi.BackupsStatus {
	st := s.probeRestic(ctx)
	s.backupMu.Lock()
	s.backupStat = st
	s.backupMu.Unlock()
	if st.Available {
		s.log.Info("files: backups available", "restic", st.Version)
	} else {
		s.log.Warn("files: backups unavailable", "reason", st.Reason)
	}
	return st
}

func (s *Service) probeRestic(ctx context.Context) filesapi.BackupsStatus {
	if s.runner == nil {
		return filesapi.BackupsStatus{Reason: s.runnerWhy}
	}
	ctx, cancel := context.WithTimeout(ctx, resticVersionWait)
	defer cancel()
	out := &limitedBuffer{max: 64 << 10}
	p, err := s.runner.Start(ctx, resticCmd{Args: []string{"version"}, Env: s.resticEnv(0), Stdout: out})
	if err != nil {
		s.log.Warn("files: cannot start restic", "err", err)
		return filesapi.BackupsStatus{Reason: "restic cannot be started (restic_binary)"}
	}
	if err := p.Wait(); err != nil {
		s.log.Warn("files: restic version failed", "err", err)
		return filesapi.BackupsStatus{Reason: "restic version failed"}
	}
	v, ok := parseResticVersion(string(out.Bytes()))
	if !ok {
		return filesapi.BackupsStatus{Reason: "restic version printed an unknown format"}
	}
	ver := fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
	if !versionAtLeast(v, filesapi.MinResticVersion) {
		return filesapi.BackupsStatus{Version: ver, Reason: fmt.Sprintf("restic %s is too old (%s or newer is needed)", ver, filesapi.MinResticVersion)}
	}
	return filesapi.BackupsStatus{Available: true, Version: ver}
}

// BackupsStatus returns the result of the last InitBackups.
func (s *Service) BackupsStatus() filesapi.BackupsStatus {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	return s.backupStat
}

func (s *Service) backupsDir() string { return filepath.Join(s.cfg.InstancesDir, "backups") }

func (s *Service) repoDir(id int64) string {
	return filepath.Join(s.backupsDir(), "repos", strconv.FormatInt(id, 10))
}

func (s *Service) keyFile(id int64) string {
	return filepath.Join(s.backupsDir(), "keys", strconv.FormatInt(id, 10))
}

// requireBackups checks that backups can run for server id: restic is usable and backups/,
// repos/ and keys/ are real folders (repos/ and keys/ are created on demand; backups/ itself
// is created by the agent, in dev by the service). Containers never see backups/, so plain
// paths are fine here; symlinks are still refused.
func (s *Service) requireBackups(id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: id must be > 0", filesapi.ErrInvalid)
	}
	if st := s.BackupsStatus(); !st.Available {
		return fmt.Errorf("%w: %s", filesapi.ErrBackupsUnavailable, st.Reason)
	}
	if s.cfg.Dev {
		if err := os.MkdirAll(s.backupsDir(), 0o700); err != nil {
			return err
		}
	}
	if err := realDir(s.backupsDir()); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: the backup directory does not exist yet (the agent creates it at start)", filesapi.ErrBackupsUnavailable)
		}
		return err
	}
	for _, sub := range []string{"repos", "keys"} {
		dir := filepath.Join(s.backupsDir(), sub)
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := realDir(dir); err != nil {
			return err
		}
	}
	return nil
}

// realDir: dir exists and is a directory itself (Lstat: a symlink is refused).
func realDir(dir string) error {
	fi, err := os.Lstat(dir) //nolint:gosec // G703: below <instances_dir>/backups, built from fixed names and an int64 id, never request text
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a real directory", dir)
	}
	return nil
}

// repoExists reports whether the repository of server id has been initialised.
func (s *Service) repoExists(id int64) bool {
	fi, err := os.Lstat(filepath.Join(s.repoDir(id), "config"))
	return err == nil && fi.Mode().IsRegular()
}

func (s *Service) keyExists(id int64) bool {
	fi, err := os.Lstat(s.keyFile(id))
	return err == nil && fi.Mode().IsRegular()
}

// ensureKey creates the repository key of server id if it does not exist (never replaced). The
// key and the keys/ folder are synced before restic init: a repository whose key was lost in a
// crash could never be read again.
func (s *Service) ensureKey(id int64) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	f, err := os.OpenFile(s.keyFile(id), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // G304: <instances_dir>/backups/keys/<id>, id is an int64 > 0
	if errors.Is(err, fs.ErrExist) {
		if s.keyExists(id) {
			return nil
		}
		return fmt.Errorf("keys/%d is not a regular file", id)
	}
	if err != nil {
		return err
	}
	_, err = f.WriteString(base64.StdEncoding.EncodeToString(b))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = syncDir(filepath.Dir(s.keyFile(id)))
	}
	if err != nil {
		_ = os.Remove(s.keyFile(id))
		return err
	}
	return nil
}

// syncDir fsyncs a folder, so a new entry in it survives a crash. File systems that cannot
// sync a folder (EINVAL, ENOTSUP) are accepted.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: <instances_dir>/backups/keys, a fixed folder of the service
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}

// ensureRepo initialises the repository of server id on its first backup (spec 4 §3.1). A
// repository without its key is unreadable: that is an error, never a new key.
func (s *Service) ensureRepo(ctx context.Context, id int64) error {
	if s.repoExists(id) {
		if !s.keyExists(id) {
			return fmt.Errorf("the key of this server's backup repository is missing (keys/%d)", id)
		}
		return nil
	}
	if err := s.ensureKey(id); err != nil {
		return fmt.Errorf("create the repository key: %w", err)
	}
	_, err := s.restic(ctx, id, nil, "init")
	return err
}

// resticEnv is the complete environment of a restic call (spec 4 §3.3); id 0 = no repository.
func (s *Service) resticEnv(id int64, extra ...string) []string {
	env := []string{"HOME=" + s.backupsDir(), "RESTIC_PROGRESS_FPS=1"}
	if id > 0 {
		env = append(env, "RESTIC_REPOSITORY="+s.repoDir(id), "RESTIC_PASSWORD_FILE="+s.keyFile(id))
	}
	return append(env, extra...)
}

// restic runs one small restic command for server id (args after "--no-cache") and returns
// its stdout (at most resticStdoutMax bytes).
func (s *Service) restic(ctx context.Context, id int64, extraEnv []string, args ...string) ([]byte, error) {
	out := &limitedBuffer{max: resticStdoutMax}
	p, err := s.runner.Start(ctx, resticCmd{Args: append([]string{"--no-cache"}, args...), Env: s.resticEnv(id, extraEnv...), Stdout: out})
	if err != nil {
		return nil, s.startErr(err)
	}
	op := subcommand(args)
	if err := p.Wait(); err != nil {
		return nil, s.resticErr(op, err)
	}
	if out.overflow {
		return nil, fmt.Errorf("restic %s printed more than %d bytes", op, resticStdoutMax)
	}
	return bytes.Clone(out.Bytes()), nil
}

// startErr: restic could not be started although the start check passed (the binary was
// removed or replaced meanwhile). The full error (with the binary's path) goes to the log; the
// answer is the fixed ErrBackupsUnavailable message (job code unavailable, HTTP 503).
func (s *Service) startErr(err error) error {
	s.log.Error("files: cannot start restic", "err", err)
	return fmt.Errorf("%w: restic cannot be started", filesapi.ErrBackupsUnavailable)
}

// subcommand is the first argument that is not a flag.
func subcommand(args []string) string {
	for _, a := range args {
		if len(a) > 0 && a[0] != '-' {
			return a
		}
	}
	return "?"
}

// resticErr maps a failed restic run (spec 4 §3.3): the full stderr goes to the log, the
// answer gets a short message (publicMessage removes host paths when it leaves the service).
func (s *Service) resticErr(op string, err error) error {
	var ex *resticExit
	if !errors.As(err, &ex) {
		return fmt.Errorf("restic %s: %w", op, err)
	}
	s.log.Warn("files: restic failed", "op", op, "code", ex.Code, "stderr", ex.Stderr)
	switch ex.Code {
	case exitNoRepo:
		return fmt.Errorf("%w: backup repository does not exist", filesapi.ErrNotFound)
	case exitLocked:
		return fmt.Errorf("%w: the backup repository is locked", filesapi.ErrBusy)
	case exitWrongPassword:
		return errors.New("the key does not match the backup repository")
	}
	return fmt.Errorf("restic %s failed: %s", op, lastLine(ex.Stderr))
}
