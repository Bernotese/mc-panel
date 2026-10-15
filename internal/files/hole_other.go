//go:build !linux

package files

import "os"

// hasHole: without SEEK_HOLE here (development hosts), the allocated blocks alone decide.
func hasHole(*os.File, int64) bool { return true }
