// Package notify sends security notifications. notify.go is a Phase-0 contract;
// the dispatcher and Discord/ntfy senders live in stream B.
package notify

import (
	"context"
	"sync"
	"time"
)

type EventType string

const (
	EventNewDeviceLogin     EventType = "login.new_device"
	EventJailed             EventType = "security.jailed"
	EventPermissionsChanged EventType = "rbac.changed"
	EventUserCreated        EventType = "user.created"
	EventUserDeleted        EventType = "user.deleted"
	Event2FAChanged         EventType = "auth.2fa_changed"
	Event2FALockout         EventType = "auth.2fa_lockout"
	Event2FAFailed          EventType = "auth.2fa_failed" // correct password, wrong second factor
	EventRecoveryCodeUsed   EventType = "auth.recovery_code_used"
	EventPasswordChanged    EventType = "auth.password_changed"
	EventAuditChainBroken   EventType = "audit.chain_broken"
	EventBackupFailed       EventType = "backup.failed"
	EventDevSyncFailed      EventType = "dev.sync_failed"
	EventServerDown         EventType = "server.down"
	EventServerRecovered    EventType = "server.recovered"
	EventServerCrashLoop    EventType = "server.crash_loop"
	EventHostDiskLow        EventType = "host.disk_low"
	EventHostDiskOK         EventType = "host.disk_ok"
	EventTest               EventType = "test"
)

type Event struct {
	Type     EventType
	Title    string
	Message  string
	IP       string // may be empty; used for deduplication
	Username string
	Time     time.Time
}

// Notifier must never block the caller and never return errors to it.
type Notifier interface {
	Notify(ctx context.Context, e Event)
}

type Nop struct{}

func (Nop) Notify(context.Context, Event) {}

var (
	_ Notifier = Nop{}
	_ Notifier = (*Recording)(nil)
)

// Recording is a test double.
type Recording struct {
	mu     sync.Mutex
	Events []Event
}

func (r *Recording) Notify(_ context.Context, e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, e)
}

func (r *Recording) Types() []EventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]EventType, len(r.Events))
	for i, e := range r.Events {
		out[i] = e.Type
	}
	return out
}
