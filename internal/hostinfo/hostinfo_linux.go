//go:build linux

package hostinfo

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
)

// MemTotal returns the installed memory of the host in bytes (MemTotal of /proc/meminfo).
func MemTotal() (uint64, error) { return memTotalFrom("/proc") }

func memTotalFrom(proc string) (uint64, error) {
	b, err := ReadProc(proc, "meminfo")
	if err != nil {
		return 0, err
	}
	return memTotal(b)
}

// ReadProc reads a small file below the proc root (they are small; 64 KiB is plenty).
func ReadProc(proc, name string) ([]byte, error) {
	f, err := os.Open(filepath.Join(proc, name)) //nolint:gosec // G304: fixed file names under the configured /proc root
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var b bytes.Buffer
	if _, err := b.ReadFrom(io.LimitReader(f, 64<<10)); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
