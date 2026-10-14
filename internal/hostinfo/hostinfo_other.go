//go:build !linux

package hostinfo

// MemTotal is unknown outside Linux (macOS in development); callers fall back to their
// static limits.
func MemTotal() (uint64, error) { return 0, ErrUnsupported }
