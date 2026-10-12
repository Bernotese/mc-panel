//go:build darwin

package files

import (
	"io/fs"
	"syscall"
)

// devID is the type of syscall.Stat_t.Dev on this platform.
type devID = int32

// fileKeyOf returns the cache key of a file from its stat data; ok is false when the
// platform does not provide device and inode. The ctime is part of it because only the kernel
// sets it: a container can rewrite a jar with the same size and set its mtime back.
func fileKeyOf(fi fs.FileInfo) (jarKey, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return jarKey{}, false
	}
	return jarKey{dev: st.Dev, ino: st.Ino, size: fi.Size(), mtime: fi.ModTime().UnixNano(), ctime: st.Ctimespec.Nano()}, true
}

// allocatedBytes returns the disk space the file occupies (512-byte blocks); ok is false when
// the platform does not report it.
func allocatedBytes(fi fs.FileInfo) (int64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Blocks * 512, true
}
