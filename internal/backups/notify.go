package backups

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
)

const (
	alertEvery   = 6 * time.Hour // spec §5.9: per server and kind at most once in 6 h
	alertBackup  = "backup"
	alertRestore = "restore"
)

// alerts remembers when a backup.failed notification went out per server and kind.
type alerts struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newAlerts() *alerts { return &alerts{last: map[string]time.Time{}} }

func alertKey(serverID int64, what string) string { return fmt.Sprintf("%d/%s", serverID, what) }

// alert sends backup.failed unless one went out for the server and kind within alertEvery.
func (h *Handler) alert(ctx context.Context, serverID int64, name, what, msg string) {
	now := h.clock.Now()
	key := alertKey(serverID, what)
	h.alerts.mu.Lock()
	if last, ok := h.alerts.last[key]; ok && now.Sub(last) < alertEvery {
		h.alerts.mu.Unlock()
		return
	}
	h.alerts.last[key] = now
	h.alerts.mu.Unlock()
	title := "Backup failed: " + name
	if what == alertRestore {
		title = "Restore failed: " + name
	}
	p, _ := principal.From(ctx) // a restore keeps the user who started it; scheduled runs have none
	h.notifier.Notify(context.WithoutCancel(ctx), notify.Event{
		Type: notify.EventBackupFailed, Title: title, Message: msg, Username: p.Username, Time: now,
	})
}

// alertOK resets the throttle after a success.
func (h *Handler) alertOK(serverID int64, what string) {
	h.alerts.mu.Lock()
	defer h.alerts.mu.Unlock()
	delete(h.alerts.last, alertKey(serverID, what))
}
