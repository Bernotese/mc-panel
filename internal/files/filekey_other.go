//go:build !linux && !darwin

package files

import "io/fs"

type devID = uint64

// fileKeyOf: no device and inode here, so jars are never cached (always computed).
func fileKeyOf(fs.FileInfo) (jarKey, bool) { return jarKey{}, false }

// allocatedBytes: not reported here, so no jar counts as sparse.
func allocatedBytes(fs.FileInfo) (int64, bool) { return 0, false }
