package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/store"
)

const (
	DedupWindow      = 5 * time.Minute
	defaultQueueSize = 100
	maxDedupEntries  = 1024
	prioQueueSize    = 16
)

// critical events use the reserved queue and are never deduplicated.
func critical(t EventType) bool { return t == EventAuditChainBroken || t == EventTest }

// DefaultBackoff are the delays between delivery attempts (1 attempt + 3 retries).
var DefaultBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

var (
	ErrNotFound = errors.New("notify: notifier not found")
	ErrConfig   = errors.New("notify: notifier configuration is unreadable")
	ErrInternal = errors.New("notify: internal error")
)

type DispatcherOptions struct {
	QueueSize int             // default 100
	Backoff   []time.Duration // nil → DefaultBackoff; empty → no retries
	Client    *http.Client    // default NewHTTPClient()
	Logger    *slog.Logger    // default slog.Default()
	// Dev allows notifier targets on internal addresses (default client only);
	// leave false in production.
	Dev bool
}

// Dispatcher implements Notifier: events are queued without blocking and
// delivered asynchronously by Run to all enabled notifiers.
type Dispatcher struct {
	q       *store.Queries
	box     *secretbox.Box
	clock   clock.Clock
	client  *http.Client
	backoff []time.Duration
	log     *slog.Logger
	ch      chan Event
	prio    chan Event // reserved for events that must not be starved by floods

	mu   sync.Mutex
	last map[string]time.Time
}

var _ Notifier = (*Dispatcher)(nil)

func NewDispatcher(db *sql.DB, box *secretbox.Box, c clock.Clock, opts DispatcherOptions) *Dispatcher {
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultQueueSize
	}
	if opts.Backoff == nil {
		opts.Backoff = DefaultBackoff
	}
	if opts.Client == nil {
		if opts.Dev {
			opts.Client = NewHTTPClient()
		} else {
			opts.Client = NewRestrictedHTTPClient()
		}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Dispatcher{
		q: store.New(db), box: box, clock: c, client: opts.Client, backoff: opts.Backoff, log: opts.Logger,
		ch: make(chan Event, opts.QueueSize), prio: make(chan Event, prioQueueSize), last: make(map[string]time.Time),
	}
}

func (d *Dispatcher) Notify(_ context.Context, e Event) {
	now := d.clock.Now()
	if e.Time.IsZero() {
		e.Time = now
	}
	if critical(e.Type) {
		select {
		case d.prio <- e:
		default:
			d.log.Warn("notify: priority queue full, dropping event", "type", e.Type)
		}
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key := ""
	if e.IP != "" {
		key = string(e.Type) + "|" + e.IP
		if d.isDuplicate(key, now) {
			return
		}
	}
	select {
	case d.ch <- e:
		if key != "" {
			d.remember(key, now) // only events that were really queued suppress later ones
		}
	default:
		d.log.Warn("notify: queue full, dropping event", "type", e.Type)
	}
}

// isDuplicate must be called with d.mu held.
func (d *Dispatcher) isDuplicate(key string, now time.Time) bool {
	t, ok := d.last[key]
	return ok && now.Sub(t) < DedupWindow
}

// remember must be called with d.mu held.
func (d *Dispatcher) remember(key string, now time.Time) {
	d.last[key] = now
	if len(d.last) > maxDedupEntries {
		for k, t := range d.last {
			if now.Sub(t) >= DedupWindow {
				delete(d.last, k)
			}
		}
	}
}

// Run delivers queued events until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select { // reserved queue first
		case e := <-d.prio:
			d.deliver(ctx, e)
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return
		case e := <-d.prio:
			d.deliver(ctx, e)
		case e := <-d.ch:
			d.deliver(ctx, e)
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, e Event) {
	rows, err := d.q.DefenseListEnabledNotifiers(ctx)
	if err != nil {
		d.log.Error("notify: load notifiers", "err", err)
		return
	}
	for _, n := range rows {
		s, err := d.senderFor(n)
		if err != nil {
			d.log.Error("notify: skipping notifier", "notifier", n.Name, "err", err)
			continue
		}
		if err := d.sendWithRetry(ctx, s, e); err != nil {
			d.log.Warn("notify: delivery failed", "notifier", n.Name, "type", e.Type, "err", err)
		}
	}
}

func (d *Dispatcher) senderFor(n store.Notifier) (Sender, error) {
	cfg, err := OpenConfig(d.box, Kind(n.Kind), n.ConfigEnc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfig, err)
	}
	return NewSender(Kind(n.Kind), cfg, d.client)
}

func (d *Dispatcher) sendWithRetry(ctx context.Context, s Sender, e Event) error {
	for attempt := 0; ; attempt++ {
		err := s.Send(ctx, e)
		if err == nil {
			return nil
		}
		var pe *PermanentError
		if errors.As(err, &pe) || attempt >= len(d.backoff) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.backoff[attempt]):
		}
	}
}

// SendTest synchronously sends EventTest to one notifier (single attempt, also if disabled).
func (d *Dispatcher) SendTest(ctx context.Context, id int64) error {
	n, err := d.q.DefenseGetNotifier(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("%w: load notifier: %w", ErrInternal, err)
	}
	s, err := d.senderFor(n)
	if err != nil {
		return err
	}
	return s.Send(ctx, Event{
		Type:    EventTest,
		Title:   "mc-panel test notification",
		Message: "If you can read this, notifications work.",
		Time:    d.clock.Now(),
	})
}
