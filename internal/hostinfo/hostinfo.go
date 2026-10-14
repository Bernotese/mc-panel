// Package hostinfo reads facts about the host the panel runs on (for now the installed
// memory) for the packages that need them (monitoring, the server limits).
package hostinfo

import (
	"bufio"
	"bytes"
	"errors"
	"strconv"
	"strings"
)

// ErrUnsupported: the platform has no /proc/meminfo (macOS in development).
var ErrUnsupported = errors.New("hostinfo: not supported on this platform")

// ParseMeminfo returns MemTotal and MemAvailable of a /proc/meminfo file in bytes; ok is false
// unless both are present and numeric.
func ParseMeminfo(b []byte) (total, avail uint64, ok bool) {
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

// memTotal extracts MemTotal (bytes) from a meminfo file; MemAvailable is not needed.
func memTotal(b []byte) (uint64, error) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "MemTotal:" {
			n, err := strconv.ParseUint(f[1], 10, 64)
			if err != nil || n == 0 {
				return 0, errors.New("hostinfo: invalid MemTotal")
			}
			return n * 1024, nil
		}
	}
	return 0, errors.New("hostinfo: no MemTotal in meminfo")
}
