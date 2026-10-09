package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// ErrAlreadyRunning is returned by Listen when another process accepts
// connections on the socket path.
var ErrAlreadyRunning = errors.New("another agent is already listening on this socket")

// Listen removes a stale socket file, listens on path, sets the socket's group
// to gid (gid < 0 leaves it unchanged) and its permissions to mode. Closing the
// returned listener removes the socket file.
func Listen(path string, mode os.FileMode, gid int) (net.Listener, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return nil, fmt.Errorf("agent: socket path %q must be an absolute filesystem path", path)
	}
	if err := removeStale(path); err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("agent: listen on %s: %w", path, err)
	}
	if err := secure(path, mode, gid); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// secure applies group and mode to the socket and verifies afterwards that
// path is still the socket we created, with the expected mode and group.
func secure(path string, mode os.FileMode, gid int) error {
	if gid >= 0 {
		if err := os.Lchown(path, -1, gid); err != nil {
			return fmt.Errorf("agent: chown %s to gid %d: %w", path, gid, err)
		}
	}
	if err := os.Chmod(path, mode); err != nil { //nolint:gosec // G302: 0600 (dev) or 0660 (prod, panel group) by design
		return fmt.Errorf("agent: chmod %s: %w", path, err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("agent: verify %s: %w", path, err)
	}
	if fi.Mode().Type() != fs.ModeSocket || fi.Mode().Perm() != mode.Perm() {
		return fmt.Errorf("agent: %s is not the expected socket with mode %v (found %v)", path, mode.Perm(), fi.Mode())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		if gid >= 0 && int(st.Gid) != gid {
			return fmt.Errorf("agent: %s has gid %d, want %d", path, st.Gid, gid)
		}
		if int(st.Uid) != os.Geteuid() {
			return fmt.Errorf("agent: %s is owned by uid %d, want %d", path, st.Uid, os.Geteuid())
		}
	}
	return nil
}

// removeStale deletes path only if it is a socket nobody listens on.
// Regular files, directories and symlinks are never removed.
func removeStale(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("agent: stat %s: %w", path, err)
	}
	if fi.Mode().Type() != fs.ModeSocket {
		return fmt.Errorf("agent: %s exists and is not a socket (%v); refusing to remove it", path, fi.Mode().Type())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err == nil {
		_ = c.Close()
		return fmt.Errorf("%w: %s", ErrAlreadyRunning, path)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("agent: cannot tell whether %s is stale: %w", path, err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("agent: remove stale socket %s: %w", path, err)
	}
	return nil
}
