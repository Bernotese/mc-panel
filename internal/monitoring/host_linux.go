//go:build linux

package monitoring

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// statfsBytes: free = blocks available to non-root users (spec §3.4).
func statfsBytes(dir string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}

// readSmall reads a /proc file (they are small; 64 KiB is plenty).
func (h *hostReader) readSmall(name string) []byte {
	f, err := os.Open(filepath.Join(h.proc, name)) //nolint:gosec // G304: fixed file names under the configured /proc root
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var b bytes.Buffer
	_, _ = b.ReadFrom(io.LimitReader(f, 64<<10))
	return b.Bytes()
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
	if total, avail, ok := parseMeminfo(h.readSmall("meminfo")); ok && total > 0 && avail <= total {
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

// parseMeminfo returns MemTotal and MemAvailable in bytes.
func parseMeminfo(b []byte) (total, avail uint64, ok bool) {
	var gotT, gotA bool
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, gotT = n*1024, true
		case "MemAvailable:":
			avail, gotA = n*1024, true
		}
	}
	return total, avail, gotT && gotA
}
