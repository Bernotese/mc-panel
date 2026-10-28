package servers

import (
	"context"
	"time"
)

// RefreshStatus loads the agent's actual state into the status cache, which the reconcile
// loop does in production.
func RefreshStatus(ctx context.Context, h *Handler) { _, _, _ = h.refreshStatus(ctx) }

// Attempts returns the number of instances with a remembered reconcile attempt (backoff).
func Attempts(h *Handler) int {
	h.reconcile.mu.Lock()
	defer h.reconcile.mu.Unlock()
	return len(h.reconcile.attempts)
}

// SetStateTimeout shortens the files service's answer time of d (stateTimeout).
func SetStateTimeout(d *FilesDataStates, t time.Duration) { d.timeout = t }

// SetAdmissionWait shortens how long h's handlers wait for an admission lock (AdmissionWait).
func SetAdmissionWait(h *Handler, t time.Duration) { h.admission.wait = t }

// NewAdmissionWait is NewAdmission with a shorter wait.
func NewAdmissionWait(t time.Duration) *Admission {
	a := NewAdmission()
	a.wait = t
	return a
}

// AdmissionEntries is the number of servers with a lock entry (held or waited for).
func AdmissionEntries(a *Admission) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.locks)
}
