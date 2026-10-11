package instances

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// deletedTimeFormat is the UTC timestamp suffix of archived data directories.
const deletedTimeFormat = "20060102T150405Z"

// ensureLayout makes sure <instances_dir> exists (0755) and that instances/ and
// deleted/ are private directories of the agent (root:root 0700 in production):
// the panel user must not read world data, and no container user may swap them.
func (m *Manager) ensureLayout() error {
	if err := os.MkdirAll(m.host.InstancesDir, 0o755); err != nil { //nolint:gosec // G301: spec §3.3, root:root 0755; contains no secrets itself
		return err
	}
	for _, d := range []string{filepath.Join(m.host.InstancesDir, "instances"), DeletedDir(m.host.InstancesDir)} {
		if err := ensurePrivateDir(d); err != nil {
			return err
		}
	}
	return nil
}

// ensurePrivateDir creates dir if needed and enforces owner = agent process
// (root in production) and mode 0700 through a descriptor opened with
// O_NOFOLLOW: a symlink in its place is refused, never followed.
func ensurePrivateDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	f, err := openDirNoFollow(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Chown(os.Geteuid(), os.Getegid()); err != nil {
		return fmt.Errorf("chown %s: %w", dir, err)
	}
	return f.Chmod(0o700)
}

// openDirNoFollow opens dir itself, refusing a symlink or anything but a directory.
func openDirNoFollow(dir string) (*os.File, error) {
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0) //nolint:gosec // G304: below the configured instances_dir, never request input
	if err != nil {
		return nil, fmt.Errorf("open %s (symlinks are refused): %w", dir, err)
	}
	fi, err := f.Stat()
	if err == nil && !fi.IsDir() {
		err = errors.New("not a directory")
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}
	return f, nil
}

// ensureDataDir creates <instances_dir>/instances/<id> (0750, owned by the
// container user). itzg skips its own chown when started with a non-root User,
// so the directory must belong to that user beforehand. Ownership and mode are
// set through an O_NOFOLLOW descriptor, so a planted symlink is never followed.
func (m *Manager) ensureDataDir(id int64) error {
	if err := m.ensureLayout(); err != nil {
		return err
	}
	dir := DataDir(m.host.InstancesDir, id)
	if err := os.Mkdir(dir, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	f, err := openDirNoFollow(dir)
	if err != nil {
		return fmt.Errorf("data directory: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Chown(m.host.UID, m.host.GID); err != nil {
		return fmt.Errorf("chown %s to %d:%d: %w", dir, m.host.UID, m.host.GID, err)
	}
	return f.Chmod(0o750)
}

// freePath returns base, or base-2, base-3, … if base exists already.
func freePath(base string) (string, error) {
	p := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
			return p, nil
		} else if err != nil {
			return "", err
		}
		if i > 100 {
			return "", fmt.Errorf("too many entries named %s-*", base)
		}
		p = base + "-" + strconv.Itoa(i)
	}
}

// archiveData moves <instances_dir>/instances/<id> to
// <instances_dir>/deleted/<name>-<UTC timestamp> (with a numeric suffix if that
// exists already). A missing data directory is not an error. The caller has
// run ensureLayout.
func (m *Manager) archiveData(id int64, name string) error {
	src := DataDir(m.host.InstancesDir, id)
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if !instancespec.ValidName(name) {
		name = "instance-" + strconv.FormatInt(id, 10)
	}
	dst, err := freePath(filepath.Join(DeletedDir(m.host.InstancesDir), name+"-"+m.clock.Now().UTC().Format(deletedTimeFormat)))
	if err != nil {
		return fmt.Errorf("archive %s: %w", src, err)
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	m.log.Info("instance data archived", "id", id, "to", dst)
	return nil
}

// CheckInstancesDir runs at agent start. It refuses an instances_dir that is a
// symlink or not a directory. With strict (production) every existing component
// of the path must also be a real directory owned by root or trustedUID and not
// writable by group or others (a root-owned sticky directory such as /tmp is
// allowed above it): no unprivileged user may swap the tree the root agent
// writes into. A missing instances_dir is fine; the agent creates it.
func CheckInstancesDir(dir string, strict bool, trustedUID uint32) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("instances_dir %q must be a clean absolute path", dir)
	}
	fi, err := os.Lstat(dir)
	switch {
	case err == nil && fi.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("instances_dir %s is a symlink; configure the real path", dir)
	case err == nil && !fi.IsDir():
		return fmt.Errorf("instances_dir %s is not a directory", dir)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if !strict {
		return nil
	}
	paths := []string{"/"}
	for _, c := range strings.Split(strings.TrimPrefix(dir, "/"), "/") {
		paths = append(paths, filepath.Join(paths[len(paths)-1], c))
	}
	for i, p := range paths {
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("instances_dir %s: %s is not a real directory (symlink?)", dir, p)
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("instances_dir %s: cannot read the owner of %s", dir, p)
		}
		if st.Uid != 0 && st.Uid != trustedUID {
			return fmt.Errorf("instances_dir %s: %s is owned by uid %d, want root", dir, p, st.Uid)
		}
		last := i == len(paths)-1
		if fi.Mode().Perm()&0o022 != 0 && (last || st.Uid != 0 || fi.Mode()&fs.ModeSticky == 0) {
			return fmt.Errorf("instances_dir %s: %s is writable by group or others (%v)", dir, p, fi.Mode().Perm())
		}
	}
	return nil
}
