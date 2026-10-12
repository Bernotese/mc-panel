package backups

import "time"

// Timings shortens the waits of the handler (all wall time).
type Timings struct{ Poll, RCON, Stop, Shutdown, IO time.Duration }

func SetTimings(h *Handler, t Timings) {
	h.pollEvery, h.rconTimeout, h.stopTimeout, h.shutdownWait, h.ioTimeout = t.Poll, t.RCON, t.Stop, t.Shutdown, t.IO
}

// SetTickEvery sets the ticker interval of Run (must be called before Run).
func SetTickEvery(h *Handler, d time.Duration) { h.tickEvery = d }
