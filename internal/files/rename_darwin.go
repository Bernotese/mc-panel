//go:build darwin

package files

import (
	"os"

	"golang.org/x/sys/unix"
)

// renameat renames from in folder fromDir to to in folder toDir; it fails with EEXIST if to
// exists (renameatx_np RENAME_EXCL; ENOTSUP where the file system does not support it).
func renameat(fromDir *os.File, from string, toDir *os.File, to string) error {
	return unix.RenameatxNp(int(fromDir.Fd()), from, int(toDir.Fd()), to, unix.RENAME_EXCL) //nolint:gosec // G115: file descriptors fit in an int
}
