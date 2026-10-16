package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/defense"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
)

// Security notifications of the login flow (SEC-20). The dispatcher deduplicates per type and
// IP for a few minutes; auth.2fa_failed is additionally limited to one per user and IP key per
// failedSecondFactorNoteWindow, because every wrong code would otherwise notify again.
const (
	failedSecondFactorNoteWindow = time.Hour
	maxNoteDedupEntries          = 4096
)

// noteDedup remembers when a key last produced a notification. It is bounded: when it is full
// of fresh entries, further keys are suppressed (the attempts are still recorded and audited).
type noteDedup struct {
	mu     sync.Mutex
	window time.Duration
	last   map[string]time.Time
}

func newNoteDedup(window time.Duration) *noteDedup {
	return &noteDedup{window: window, last: make(map[string]time.Time)}
}

// allow reports whether key may notify at now and remembers it if so.
func (d *noteDedup) allow(key string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.last[key]; ok && now.Sub(t) < d.window {
		return false
	}
	if len(d.last) >= maxNoteDedupEntries {
		for k, t := range d.last {
			if now.Sub(t) >= d.window {
				delete(d.last, k)
			}
		}
		if len(d.last) >= maxNoteDedupEntries {
			return false
		}
	}
	d.last[key] = now
	return true
}

// isLoginSecondFactorStage: the second step of a login, after a correct password (not sudo).
func isLoginSecondFactorStage(s defense.AttemptStage) bool {
	return s == defense.StageTOTP || s == defense.StageWebAuthn || s == defense.StageRecovery
}

// fromWhere describes the client address of the request for a notification message.
func (h *Handler) fromWhere(ctx context.Context) string {
	ip := httpx.ClientIP(ctx)
	if !ip.IsValid() {
		return ""
	}
	if c := h.geo.Country(ip); c != "" {
		return fmt.Sprintf(" from %s (%s)", ip, c)
	}
	return fmt.Sprintf(" from %s", ip)
}

func (h *Handler) notifySecurity(ctx context.Context, t notify.EventType, title, msg, username string) {
	ip := ""
	if a := httpx.ClientIP(ctx); a.IsValid() {
		ip = a.String()
	}
	h.notifier.Notify(ctx, notify.Event{Type: t, Title: title, Message: msg, IP: ip, Username: username, Time: h.clock.Now()})
}

// notifySecondFactorFailed reports a wrong second factor after a correct password: someone
// knows the password. Once per user and IP key per hour.
func (h *Handler) notifySecondFactorFailed(ctx context.Context, username string, userID int64) {
	key := fmt.Sprintf("%d|%s", userID, httpx.IPKey(httpx.ClientIP(ctx)))
	if !h.failedNotes.allow(key, h.clock.Now()) {
		return
	}
	h.notifySecurity(ctx, notify.Event2FAFailed, "Wrong second factor",
		username+" entered the correct password but a wrong second factor"+h.fromWhere(ctx)+".", username)
}
