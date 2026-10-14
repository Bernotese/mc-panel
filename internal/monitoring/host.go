package monitoring

// hostReader reads the host values of spec §3.4. proc is "/proc" in production; tests point
// it at testdata. disk is instances_dir ("" = no disk values). Only the collector's pass uses
// it (one goroutine), so prev needs no lock.
type hostReader struct {
	proc   string
	disk   string
	prev   *cpuTimes
	statfs func(dir string) (free, total uint64, err error)
}

type cpuTimes struct{ busy, total uint64 }

func newHostReader(instancesDir string) *hostReader {
	return &hostReader{proc: "/proc", disk: instancesDir, statfs: statfsBytes}
}

func (h *hostReader) read() HostSample {
	s := h.readProc() // platform part: CPU, RAM, load
	if h.disk != "" && h.statfs != nil {
		if free, total, err := h.statfs(h.disk); err == nil && total > 0 && free <= total {
			f, t := int64(free), int64(total)
			s.DiskFree, s.DiskTotal = &f, &t
		}
	}
	return s
}
