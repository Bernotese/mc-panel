//go:build linux

package files

import (
	"os"

	"golang.org/x/sys/unix"
)

// hasHole reports whether the open file f of size bytes has a hole (lseek SEEK_HOLE). A file
// system without hole support reports none. Compressing file systems (ZFS) also report few
// allocated blocks for a file without holes; such a file is backed up normally.
func hasHole(f *os.File, size int64) bool {
	rc, err := f.SyscallConn()
	if err != nil {
		return false
	}
	var off int64
	var serr error
	if err := rc.Control(func(fd uintptr) { off, serr = unix.Seek(int(fd), 0, unix.SEEK_HOLE) }); err != nil || serr != nil {
		return false
	}
	return off < size
}
