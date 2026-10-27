package backups

import (
	"context"
	"time"
)

// Timings shortens the waits of the handler (all wall time).
type Timings struct{ Poll, RCON, Stop, Shutdown, IO time.Duration }

func SetTimings(h *Handler, t Timings) {
	h.pollEvery, h.rconTimeout, h.stopTimeout, h.shutdownWait, h.ioTimeout = t.Poll, t.RCON, t.Stop, t.Shutdown, t.IO
}

// SetTickEvery sets the ticker interval of Run (must be called before Run).
func SetTickEvery(h *Handler, d time.Duration) { h.tickEvery = d }

// Tick runs one ticker pass.
func Tick(ctx context.Context, h *Handler) { h.tick(ctx) }

// WaitIdle waits up to 5 s until no job waits or runs.
func WaitIdle(h *Handler) bool {
	deadline := time.Now().Add(5 * time.Second)
	for !h.queue.idle() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}

// JobOf returns state and error code of the server's last job without the route (whose
// loadServer answers 404 once the server is deleted); ok is false without a job.
func JobOf(h *Handler, serverID int64) (state, code string, ok bool) {
	v := h.queue.get(serverID)
	if v == nil {
		return "", "", false
	}
	if v.ErrorCode != nil {
		code = *v.ErrorCode
	}
	return v.State, code, true
}

// WaitFirstTick waits up to 5 s for the first tick of Run (it runs concurrently with requests).
func WaitFirstTick(h *Handler) bool {
	select {
	case <-h.ticked:
		return true
	case <-time.After(5 * time.Second):
		return false
	}
}

// Drop ends the server's job like a server delete does (queue.drop).
func Drop(h *Handler, serverID int64) { h.queue.drop(serverID, 5*time.Second) }
