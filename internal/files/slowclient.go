package files

import (
	"errors"
	"fmt"
	"time"
)

// Slow-client limits of uploads and downloads (SEC-24): the service has only a few upload and
// zip slots, and the stream deadline (ioTimeout) only catches a client that stops completely.
// After minRateGrace, every minRateWindow spent waiting for the client must move at least
// minRateBytes; time the service itself spends (disk, walking folders, restic) does not count.
// An upload ends after maxUploadTime, a download after maxDownloadTime.
const (
	minRateGrace    = 2 * time.Minute
	minRateWindow   = time.Minute
	minRateBytes    = 64 << 10
	maxUploadTime   = 6 * time.Hour
	maxDownloadTime = 12 * time.Hour
)

var (
	errClientTooSlow = fmt.Errorf("the client moved less than %d KiB per minute", minRateBytes>>10)
	errStreamTooLong = errors.New("the transfer took longer than allowed")
)

// rateGuard applies the slow-client limits to one stream.
type rateGuard struct {
	now    func() time.Time
	start  time.Time
	max    time.Duration
	waited time.Duration // waiting for the client in the current window
	bytes  int64         // bytes moved in the current window
}

func newRateGuard(now func() time.Time, maxTime time.Duration) *rateGuard {
	return &rateGuard{now: now, start: now(), max: maxTime}
}

// before is called before every read or write of the stream.
func (g *rateGuard) before() error {
	if g.now().Sub(g.start) > g.max {
		return fmt.Errorf("%w (%s)", errStreamTooLong, g.max)
	}
	return nil
}

// after records one read or write: n bytes after waiting waited for the client.
func (g *rateGuard) after(n int, waited time.Duration) error {
	g.bytes += int64(max(n, 0))
	g.waited += max(waited, 0)
	if g.waited < minRateWindow {
		return nil
	}
	slow := g.bytes < minRateBytes && g.now().Sub(g.start) >= minRateGrace
	g.waited, g.bytes = 0, 0
	if slow {
		return errClientTooSlow
	}
	return nil
}
