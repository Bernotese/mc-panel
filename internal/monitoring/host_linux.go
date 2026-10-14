//go:build linux

package monitoring

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/Bernotese/mc-panel/internal/hostinfo"
)

// statfsBytes: free = blocks available to non-root users (spec §3.4). Block counts are in
// fragment size units (f_frsize); Bsize is only the preferred I/O size.
func statfsBytes(dir string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	unit := uint64(st.Frsize)
	if unit == 0 {
		unit = uint64(st.Bsize)
	}
	return st.Bavail * unit, st.Blocks * unit, nil
}

// readSmall reads a /proc file; nil if it cannot be read.
func (h *hostReader) readSmall(name string) []byte {
	b, _ := hostinfo.ReadProc(h.proc, name)
	return b
}

func (h *hostReader) readProc() HostSample {
	var s HostSample
	if cur, ok := parseCPU(h.readSmall("stat")); ok {
		if h.prev != nil && cur.total > h.prev.total && cur.busy >= h.prev.busy {
			p := 100 * float64(cur.busy-h.prev.busy) / float64(cur.total-h.prev.total)
			s.CPUPercent = &p
		}
		h.prev = &cur
	} else {
		h.prev = nil
	}
	if total, avail, ok := hostinfo.ParseMeminfo(h.readSmall("meminfo")); ok && total > 0 && avail <= total {
		t, u := int64(total), int64(total-avail)
		s.MemoryTotal, s.MemoryUsed = &t, &u
	}
	if f := strings.Fields(string(h.readSmall("loadavg"))); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil && v >= 0 {
			s.Load1 = &v
		}
	}
	return s
}

// parseCPU sums the first "cpu" line: idle = idle + iowait, total = the first eight fields
// (user … steal; guest time is already part of user).
func parseCPU(b []byte) (cpuTimes, bool) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 || f[0] != "cpu" {
			continue
		}
		if len(f) < 9 {
			return cpuTimes{}, false
		}
		var v [8]uint64
		var total uint64
		for i := range v {
			n, err := strconv.ParseUint(f[i+1], 10, 64)
			if err != nil {
				return cpuTimes{}, false
			}
			v[i] = n
			total += n
		}
		idle := v[3] + v[4]
		return cpuTimes{busy: total - idle, total: total}, true
	}
	return cpuTimes{}, false
}
