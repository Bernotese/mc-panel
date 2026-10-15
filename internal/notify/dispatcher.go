package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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

	// JailBurst security.jailed events are sent per JailWindow; further jails in that window
	// are only counted and reported in one summary at its end (SEC-21).
	JailBurst  = 5
	JailWindow = 10 * time.Minute
	// MaxRetryAfter caps how long a delivery waits for a target's Retry-After.
	MaxRetryAfter = 30 * time.Second
	// jailFlushInterval is how often Run checks whether a jail summary is due.
	jailFlushInterval = time.Minute
)

// critical events use the reserved queue and are never deduplicated.
func critical(t EventType) bool { return t == EventAuditChainBroken || t == EventTest }

// operational events have their own queue and worker, so a flood of security events (jails
// from thousands of addresses) cannot delay or drop them.
func operational(t EventType) bool {
	s := string(t)
	return strings.HasPrefix(s, "server.") || strings.HasPrefix(s, "host.") ||
		t == EventBackupFailed || t == EventNewDeviceLogin
}

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
	ops     chan Event // operational events, delivered by their own worker
	prio    chan Event // reserved for events that must not be starved by floods
	// sleep waits between delivery attempts; tests replace it.
	sleep func(ctx context.Context, d time.Duration) error

	mu   sync.Mutex
	last map[string]time.Time
	jail jailAggregate
}

// jailAggregate limits security.jailed notifications to JailBurst per JailWindow.
type jailAggregate struct {
	start      time.Time
	sent       int
	suppressed int
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
			opts.Client = NewRestrictedHTTPClient(c)
		}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Dispatcher{
		q: store.New(db), box: box, clock: c, client: opts.Client, backoff: opts.Backoff, log: opts.Logger,
		ch: make(chan Event, opts.QueueSize), ops: make(chan Event, opts.QueueSize), prio: make(chan Event, prioQueueSize),
		last: make(map[string]time.Time), sleep: sleepContext,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
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
	if e.Type == EventJailed && !d.admitJail(now) {
		return
	}
	queue := d.ch
	if operational(e.Type) {
		queue = d.ops
	}
	select {
	case queue <- e:
		if key != "" {
			d.remember(key, now) // only events that were really queued suppress later ones
		}
	default:
		d.log.Warn("notify: queue full, dropping event", "type", e.Type)
	}
}

// admitJail reports whether a security.jailed event may be sent now; otherwise it is counted
// for the summary. Must be called with d.mu held.
func (d *Dispatcher) admitJail(now time.Time) bool {
	a := &d.jail
	if a.start.IsZero() || (now.Sub(a.start) >= JailWindow && a.suppressed == 0) {
		*a = jailAggregate{start: now}
	}
	if a.sent < JailBurst && now.Sub(a.start) < JailWindow {
		a.sent++
		return true
	}
	a.suppressed++
	return false
}

// flushJailSummary sends one summary for the jails suppressed in a window that has ended and
// starts a new window. Run calls it periodically.
func (d *Dispatcher) flushJailSummary() {
	now := d.clock.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	a := &d.jail
	if a.suppressed == 0 || now.Sub(a.start) < JailWindow {
		return
	}
	e := Event{
		Type:  EventJailed,
		Title: "More IPs jailed",
		Message: fmt.Sprintf("%d more addresses were jailed since %s. See the security center for the list.",
			a.suppressed, a.start.UTC().Format(time.RFC3339)),
		Time: now,
	}
	select {
	case d.ch <- e:
		*a = jailAggregate{start: now}
	default:
		d.log.Warn("notify: queue full, jail summary postponed", "suppressed", a.suppressed)
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

// Run delivers queued events until ctx is cancelled: one worker for the security events and
// one for the operational ones; both serve the reserved queue first.
func (d *Dispatcher) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); d.work(ctx, d.ch) }()
	go func() { defer wg.Done(); d.work(ctx, d.ops) }()
	t := time.NewTicker(jailFlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-t.C:
			d.flushJailSummary()
		}
	}
}

func (d *Dispatcher) work(ctx context.Context, queue <-chan Event) {
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
		case e := <-queue:
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
		wait := d.backoff[attempt]
		var ra *retryAfterError
		if errors.As(err, &ra) && ra.after > wait {
			wait = min(ra.after, MaxRetryAfter)
		}
		if err := d.sleep(ctx, wait); err != nil {
			return err
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
