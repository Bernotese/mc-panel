package notify

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/clock"
	"mcpanel/internal/secretbox"
	"mcpanel/internal/store"
	"mcpanel/internal/store/storetest"
)

var dT0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

type dispEnv struct {
	db  *sql.DB
	q   *store.Queries
	box *secretbox.Box
	clk *clock.Fake
	d   *Dispatcher
}

func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New(bytes.Repeat([]byte{9}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newDispEnv(t *testing.T, opts DispatcherOptions) *dispEnv {
	t.Helper()
	db := storetest.Open(t)
	e := &dispEnv{db: db, q: store.New(db), box: testBox(t), clk: clock.NewFake(dT0)}
	if opts.Backoff == nil {
		opts.Backoff = []time.Duration{0, 0, 0}
	}
	opts.Dev = true // tests deliver to httptest servers on 127.0.0.1
	e.d = NewDispatcher(db, e.box, e.clk, opts)
	return e
}

func (e *dispEnv) addNotifier(t *testing.T, kind Kind, name string, cfg Config, enabled bool) int64 {
	t.Helper()
	enc, err := SealConfig(e.box, kind, cfg)
	if err != nil {
		t.Fatal(err)
	}
	en := int64(0)
	if enabled {
		en = 1
	}
	row, err := e.q.DefenseInsertNotifier(context.Background(), store.DefenseInsertNotifierParams{
		Kind: string(kind), Name: name, ConfigEnc: enc, Enabled: en, CreatedAt: dT0.UnixMilli(), UpdatedAt: dT0.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func TestNotifyDedup(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{QueueSize: 20})
	ctx := context.Background()
	jailed := func(ip string) Event { return Event{Type: EventJailed, IP: ip} }
	e.d.Notify(ctx, jailed("203.0.113.7/32"))
	e.d.Notify(ctx, jailed("203.0.113.7/32"))
	if n := len(e.d.ch); n != 1 {
		t.Fatalf("duplicate within 5 min must be dropped, queued %d", n)
	}
	e.d.Notify(ctx, jailed("203.0.113.8/32"))
	e.d.Notify(ctx, Event{Type: EventNewDeviceLogin, IP: "203.0.113.7/32"})
	e.clk.Advance(DedupWindow)
	e.d.Notify(ctx, jailed("203.0.113.7/32"))
	if n := len(e.d.ch); n != 4 {
		t.Fatalf("queued %d, want 4", n)
	}
	e.d.Notify(ctx, Event{Type: EventUserCreated, Username: "a"})
	e.d.Notify(ctx, Event{Type: EventUserCreated, Username: "b"})
	e.d.Notify(ctx, Event{Type: EventTest, IP: "x"})
	e.d.Notify(ctx, Event{Type: EventTest, IP: "x"})
	if n := len(e.d.ch) + len(e.d.prio); n != 8 { // test events use the reserved queue
		t.Fatalf("events without IP and test events are never deduplicated, queued %d", n)
	}
}

func TestNotifyNeverBlocks(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{QueueSize: 1})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			e.d.Notify(context.Background(), Event{Type: EventUserCreated})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Notify blocked on a full queue")
	}
	if n := len(e.d.ch); n != 1 {
		t.Fatalf("queued %d, want 1", n)
	}
	ev := <-e.d.ch
	if !ev.Time.Equal(dT0) {
		t.Fatalf("event time = %v, want clock time", ev.Time)
	}
}

func TestDeliverOnlyToEnabledNotifiers(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{})
	a := newHookServer(t, http.StatusNoContent)
	b := newHookServer(t, http.StatusOK)
	e.addNotifier(t, KindDiscord, "discord", Config{URL: a.srv.URL + "/api/webhooks/1/tok"}, true)
	e.addNotifier(t, KindNtfy, "ntfy-off", Config{URL: b.srv.URL, Topic: "alerts"}, false)
	e.d.deliver(context.Background(), Event{Type: EventJailed, Title: "IP jailed"})
	if len(a.requests()) != 1 || len(b.requests()) != 0 {
		t.Fatalf("a=%d b=%d", len(a.requests()), len(b.requests()))
	}
}

func TestDeliverRetries(t *testing.T) {
	cases := []struct {
		name     string
		statuses []int
		want     int
	}{
		{"transient then ok", []int{500, 503, 204}, 3},
		{"permanent", []int{400}, 1},
		{"always failing", []int{500}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newDispEnv(t, DispatcherOptions{})
			h := newHookServer(t, tc.statuses...)
			e.addNotifier(t, KindNtfy, "n", Config{URL: h.srv.URL, Topic: "alerts"}, true)
			e.d.deliver(context.Background(), Event{Type: EventTest})
			if n := len(h.requests()); n != tc.want {
				t.Fatalf("requests = %d, want %d", n, tc.want)
			}
		})
	}
}

func TestDeliverSkipsUndecryptableNotifier(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{})
	h := newHookServer(t, http.StatusOK)
	if _, err := e.q.DefenseInsertNotifier(context.Background(), store.DefenseInsertNotifierParams{
		Kind: "ntfy", Name: "broken", ConfigEnc: []byte("garbage"), Enabled: 1, CreatedAt: 0, UpdatedAt: 0,
	}); err != nil {
		t.Fatal(err)
	}
	e.addNotifier(t, KindNtfy, "ok", Config{URL: h.srv.URL, Topic: "alerts"}, true)
	e.d.deliver(context.Background(), Event{Type: EventTest})
	if len(h.requests()) != 1 {
		t.Fatal("a broken notifier must not stop delivery to the others")
	}
}

func TestRunDeliversQueuedEvents(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{})
	h := newHookServer(t, http.StatusOK)
	e.addNotifier(t, KindNtfy, "n", Config{URL: h.srv.URL, Topic: "alerts"}, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { e.d.Run(ctx); close(done) }()
	e.d.Notify(ctx, Event{Type: EventJailed, Title: "IP jailed", IP: "203.0.113.7/32"})
	deadline := time.Now().Add(2 * time.Second)
	for len(h.requests()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("event was not delivered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestSendTest(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{})
	ctx := context.Background()
	if err := e.d.SendTest(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	ok := newHookServer(t, http.StatusOK)
	id := e.addNotifier(t, KindNtfy, "ok", Config{URL: ok.srv.URL, Topic: "alerts"}, false)
	if err := e.d.SendTest(ctx, id); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	reqs := ok.requests()
	if len(reqs) != 1 || reqs[0].title != "mc-panel test notification" || !strings.Contains(string(reqs[0].body), "notifications work") {
		t.Fatalf("test request = %+v", reqs)
	}
	bad := newHookServer(t, http.StatusNotFound)
	badID := e.addNotifier(t, KindNtfy, "bad", Config{URL: bad.srv.URL, Topic: "alerts"}, true)
	var pe *PermanentError
	if err := e.d.SendTest(ctx, badID); !errors.As(err, &pe) || len(bad.requests()) != 1 {
		t.Fatalf("SendTest = %v (requests %d), want single permanent failure", err, len(bad.requests()))
	}
	row, err := e.q.DefenseInsertNotifier(ctx, store.DefenseInsertNotifierParams{
		Kind: "ntfy", Name: "broken", ConfigEnc: []byte("garbage"), Enabled: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.d.SendTest(ctx, row.ID); !errors.Is(err, ErrConfig) {
		t.Fatalf("broken config: %v", err)
	}
}

func TestRestrictedClientRejectsInternalDestinations(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{})
	h := newHookServer(t, http.StatusOK) // listens on 127.0.0.1
	id := e.addNotifier(t, KindNtfy, "internal", Config{URL: h.srv.URL, Topic: "alerts"}, true)
	prod := NewDispatcher(e.db, e.box, e.clk, DispatcherOptions{Backoff: []time.Duration{}}) // Dev=false
	err := prod.SendTest(context.Background(), id)
	var pe *PermanentError
	if err == nil || !errors.As(err, &pe) || !IsTransportError(err) {
		t.Fatalf("SendTest = %v, want permanent transport error", err)
	}
	if len(h.requests()) != 0 {
		t.Fatalf("no connection to loopback may be made outside dev, got %d requests", len(h.requests()))
	}
}

func TestBlockedDestinations(t *testing.T) {
	for ip, blocked := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "192.168.0.1": true, "172.16.0.1": true, "fd00::1": true,
		"169.254.169.254": true, "fe80::1": true, "0.0.0.0": true, "224.0.0.1": true, "::ffff:127.0.0.1": true,
		"203.0.113.7": false, "2001:db8::1": false, "8.8.8.8": false,
	} {
		if got := blockedDestination(netip.MustParseAddr(ip)); got != blocked {
			t.Errorf("blockedDestination(%s) = %v, want %v", ip, got, blocked)
		}
	}
}

func TestCriticalEventsAreNotStarvedByFullQueue(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{QueueSize: 1})
	h := newHookServer(t, http.StatusOK)
	e.addNotifier(t, KindNtfy, "n", Config{URL: h.srv.URL, Topic: "alerts"}, true)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		e.d.Notify(ctx, Event{Type: EventUserCreated}) // fills the normal queue
	}
	e.d.Notify(ctx, Event{Type: EventAuditChainBroken, Title: "chain"})
	e.d.Notify(ctx, Event{Type: EventTest, Title: "test"})
	if n := len(e.d.prio); n != 2 {
		t.Fatalf("critical events must use the reserved queue, queued %d", n)
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go e.d.Run(rctx)
	deadline := time.Now().Add(2 * time.Second)
	for len(h.requests()) < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("delivered %d, want 3", len(h.requests()))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if first := h.requests()[0]; first.title != "chain" {
		t.Fatalf("critical events are delivered first, got %q", first.title)
	}
}

func TestDedupRecordedOnlyWhenQueued(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{QueueSize: 1})
	ctx := context.Background()
	e.d.Notify(ctx, Event{Type: EventUserCreated})                  // occupies the queue
	e.d.Notify(ctx, Event{Type: EventJailed, IP: "203.0.113.7/32"}) // dropped: queue full
	<-e.d.ch
	e.d.Notify(ctx, Event{Type: EventJailed, IP: "203.0.113.7/32"}) // must not be suppressed by the dropped one
	if n := len(e.d.ch); n != 1 {
		t.Fatalf("event dropped on a full queue must not count for deduplication, queued %d", n)
	}
}
