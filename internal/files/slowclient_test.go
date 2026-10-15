package files

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type stepClock struct{ t time.Time }

func (c *stepClock) now() time.Time { return c.t }

// SEC-24: a client that moves less than 64 KiB per minute of waiting is cut off after the
// grace period; time the service spends itself does not count, and the ceiling ends any stream.
func TestRateGuard(t *testing.T) {
	c := &stepClock{t: testStart}
	op := func(g *rateGuard, n int, waited time.Duration) error {
		if err := g.before(); err != nil {
			return err
		}
		c.t = c.t.Add(waited)
		return g.after(n, waited)
	}

	// A trickle (1 KiB every 30 s) is fine during the grace period, then cut off.
	g := newRateGuard(c.now, time.Hour)
	var err error
	steps := 0
	for err == nil && steps < 100 {
		err = op(g, 1<<10, 30*time.Second)
		steps++
	}
	if !errors.Is(err, errClientTooSlow) {
		t.Fatalf("trickle: %v", err)
	}
	if elapsed := c.t.Sub(testStart); elapsed < minRateGrace || elapsed > minRateGrace+minRateWindow {
		t.Fatalf("trickle cut off after %s", elapsed)
	}

	// A fast client with long pauses of the service (not waiting for the client) is fine.
	c.t = testStart
	g = newRateGuard(c.now, 24*time.Hour)
	for i := range 2000 {
		if err := op(g, 32<<10, 50*time.Millisecond); err != nil {
			t.Fatalf("fast client, step %d: %v", i, err)
		}
		c.t = c.t.Add(10 * time.Second) // the service walks folders or waits for restic
	}

	// Exactly the minimum rate passes.
	c.t = testStart
	g = newRateGuard(c.now, time.Hour)
	for i := range 20 {
		if err := op(g, minRateBytes, minRateWindow); err != nil {
			t.Fatalf("minimum rate, minute %d: %v", i, err)
		}
	}

	// The ceiling ends even a fast stream.
	c.t = testStart
	g = newRateGuard(c.now, time.Hour)
	c.t = c.t.Add(time.Hour + time.Second)
	if err := op(g, 1<<20, time.Millisecond); !errors.Is(err, errStreamTooLong) {
		t.Fatalf("over the ceiling: %v", err)
	}
}

// trickleReader yields one byte per read and lets each read take 30 s of the step clock.
type trickleReader struct{ c *stepClock }

func (r trickleReader) Read(p []byte) (int, error) {
	r.c.t = r.c.t.Add(30 * time.Second)
	p[0] = 'x'
	return 1, nil
}

// The upload body reader stops a trickling client, and the upload reports it as the caller's
// problem (invalid), not as an internal error.
func TestUploadReaderCutsOffTrickle(t *testing.T) {
	c := &stepClock{t: testStart}
	rc := http.NewResponseController(httptest.NewRecorder())
	body := &bodyReader{r: newDeadlineReader(trickleReader{c}, rc, c.now)}
	_, err := io.Copy(io.Discard, body)
	if !errors.Is(err, errClientTooSlow) || !errors.Is(body.problem(), filesapi.ErrInvalid) {
		t.Fatalf("trickle upload: %v (%v)", err, body.problem())
	}
	if elapsed := c.t.Sub(testStart); elapsed > minRateGrace+minRateWindow {
		t.Fatalf("cut off only after %s", elapsed)
	}
}
