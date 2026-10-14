//go:build unix

package unixsock

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
)

// activationFD is SD_LISTEN_FDS_START: systemd passes the first socket as fd 3.
const activationFD = 3

// Activated returns the listener systemd passed as fd 3 when LISTEN_PID is pid and
// LISTEN_FDS is 1, otherwise nil. It always unsets LISTEN_PID, LISTEN_FDS and
// LISTEN_FDNAMES, so they never leak into child processes. Used by the files service
// (mcpanel-files.socket) and the panel (mcpanel-panel.socket).
func Activated(getenv func(string) string, unsetenv func(string) error, pid int) (net.Listener, error) {
	lpid, lfds := getenv("LISTEN_PID"), getenv("LISTEN_FDS")
	for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		_ = unsetenv(k)
	}
	if lpid == "" && lfds == "" {
		return nil, nil
	}
	if lpid != strconv.Itoa(pid) {
		return nil, nil // meant for another process
	}
	if n, err := strconv.Atoi(lfds); err != nil || n != 1 {
		return nil, fmt.Errorf("socket activation: expected exactly one socket, got LISTEN_FDS=%q", lfds)
	}
	syscall.CloseOnExec(activationFD)
	f := os.NewFile(activationFD, "activated.sock")
	l, err := net.FileListener(f)
	_ = f.Close() // FileListener holds its own duplicate
	if err != nil {
		return nil, fmt.Errorf("socket activation: %w", err)
	}
	if _, ok := l.(*net.UnixListener); !ok {
		_ = l.Close()
		return nil, errors.New("socket activation: fd 3 is not a Unix socket")
	}
	return l, nil
}
