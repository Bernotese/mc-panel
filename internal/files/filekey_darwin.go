//go:build darwin

package files

import (
	"io/fs"
	"syscall"
)

// devID is the type of syscall.Stat_t.Dev on this platform.
type devID = int32

// fileKeyOf returns the cache key of a file from its stat data; ok is false when the
// platform does not provide device and inode.
func fileKeyOf(fi fs.FileInfo) (jarKey, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return jarKey{}, false
	}
	return jarKey{dev: st.Dev, ino: st.Ino, size: fi.Size(), mtime: fi.ModTime().UnixNano()}, true
}
