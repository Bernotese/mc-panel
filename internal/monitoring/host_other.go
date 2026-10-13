//go:build !linux

package monitoring

import "golang.org/x/sys/unix"

// macOS (the only other build target, see make check): only the disk values (spec §3.4).
func statfsBytes(dir string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}

func (h *hostReader) readProc() HostSample { return HostSample{} }
