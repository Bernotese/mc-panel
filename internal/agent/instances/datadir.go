package instances

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// deletedTimeFormat is the UTC timestamp suffix of archived data directories.
const deletedTimeFormat = "20060102T150405Z"

// ensureDataDir creates <instances_dir>/instances/<id> (0750, owned by the
// container user). itzg skips its own chown when started with a non-root User,
// so the directory must belong to that user beforehand. instances/ itself is
// 0700 root: the panel user must not read world data.
func (m *Manager) ensureDataDir(id int64) error {
	if err := os.MkdirAll(m.host.InstancesDir, 0o755); err != nil { //nolint:gosec // G301: spec §3.3, root:root 0755; contains no secrets itself
		return err
	}
	parent := filepath.Join(m.host.InstancesDir, "instances")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	dir := DataDir(m.host.InstancesDir, id)
	if err := os.Mkdir(dir, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() { // e.g. a symlink planted in place of the directory
		return fmt.Errorf("data directory %s is not a directory", dir)
	}
	if err := os.Chown(dir, m.host.UID, m.host.GID); err != nil {
		return fmt.Errorf("chown %s to %d:%d: %w", dir, m.host.UID, m.host.GID, err)
	}
	return os.Chmod(dir, 0o750) //nolint:gosec // G302: directory, group mcpanel-mc needs x to traverse
}

// archiveData moves <instances_dir>/instances/<id> to
// <instances_dir>/deleted/<name>-<UTC timestamp> (with a numeric suffix if that
// exists already). A missing data directory is not an error.
func (m *Manager) archiveData(id int64, name string) error {
	src := DataDir(m.host.InstancesDir, id)
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	deleted := DeletedDir(m.host.InstancesDir)
	if err := os.MkdirAll(deleted, 0o700); err != nil {
		return err
	}
	if !instancespec.ValidName(name) {
		name = "instance-" + strconv.FormatInt(id, 10)
	}
	base := name + "-" + m.clock.Now().UTC().Format(deletedTimeFormat)
	dst := filepath.Join(deleted, base)
	for i := 2; ; i++ {
		if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
			break
		} else if err != nil {
			return err
		}
		if i > 100 {
			return fmt.Errorf("archive %s: too many directories named %s-*", src, base)
		}
		dst = filepath.Join(deleted, base+"-"+strconv.Itoa(i))
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	m.log.Info("instance data archived", "id", id, "to", dst)
	return nil
}

// clearStaleLists empties ops.json and whitelist.json of an instance whose spec has
// no ops, or no enabled non-empty whitelist. instancespec.Env omits OPS/WHITELIST
// for empty lists, and itzg then leaves the files alone, so the last removed op
// would stay op. Only existing files are rewritten: atomically, owned by the
// container user, mode 0644 (as the server writes them).
func (m *Manager) clearStaleLists(id int64, name string, p instancespec.Properties) error {
	r := instancespec.Resolve(name, p)
	dir := DataDir(m.host.InstancesDir, id)
	if len(r.Ops) == 0 {
		if err := m.emptyListFile(id, dir, "ops.json"); err != nil {
			return err
		}
	}
	if !*r.WhitelistEnabled || len(r.Whitelist) == 0 {
		if err := m.emptyListFile(id, dir, "whitelist.json"); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) emptyListFile(id int64, dir, file string) (err error) {
	path := filepath.Join(dir, file)
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	// The data directory belongs to the container user: CreateTemp (O_EXCL) and
	// rename never follow a planted symlink; the rename replaces the link itself.
	tmp, err := os.CreateTemp(dir, "."+file+"-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.WriteString("[]\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Chown(m.host.UID, m.host.GID); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	m.log.Info("instance list file cleared", "id", id, "file", file)
	return nil
}
