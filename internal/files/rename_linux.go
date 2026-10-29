//go:build linux

package files

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameat renames from in folder fromDir to to in folder toDir; it fails with EEXIST if to
// exists (renameat2 RENAME_NOREPLACE; EINVAL where the file system does not support it).
func renameat(fromDir *os.File, from string, toDir *os.File, to string) error {
	return unix.Renameat2(int(fromDir.Fd()), from, int(toDir.Fd()), to, unix.RENAME_NOREPLACE) //nolint:gosec // G115: file descriptors fit in an int
}
