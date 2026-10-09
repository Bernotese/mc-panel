package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
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
	if err := removeStale(path); err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("agent: listen on %s: %w", path, err)
	}
	if gid >= 0 {
		if err := os.Chown(path, -1, gid); err != nil {
			_ = l.Close()
			return nil, fmt.Errorf("agent: chown %s to gid %d: %w", path, gid, err)
		}
	}
	if err := os.Chmod(path, mode); err != nil { //nolint:gosec // G302: 0600 (dev) or 0660 (prod, panel group) by design
		_ = l.Close()
		return nil, fmt.Errorf("agent: chmod %s: %w", path, err)
	}
	return l, nil
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
	c, err := net.DialTimeout("unix", path, 500*time.Millisecond)
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
