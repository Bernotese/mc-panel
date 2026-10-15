package notify

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var dT0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

type dispEnv struct {
	db  *sql.DB
	q   *store.Queries
	box *secretbox.Box
	clk *clock.Fake
	d   *Dispatcher
}

// JSON-decoded booleans arrive as any; named values avoid bool literals in comparisons.
var jsonTrue any = true

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
	if n := len(e.d.ch) + len(e.d.ops); n != 4 { // login.new_device uses the operational queue
		t.Fatalf("queued %d, want 4", n)
	}
	e.d.Notify(ctx, Event{Type: EventUserCreated, Username: "a"})
	e.d.Notify(ctx, Event{Type: EventUserCreated, Username: "b"})
	e.d.Notify(ctx, Event{Type: EventTest, IP: "x"})
	e.d.Notify(ctx, Event{Type: EventTest, IP: "x"})
	if n := len(e.d.ch) + len(e.d.ops) + len(e.d.prio); n != 8 { // test events use the reserved queue
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
		"100.64.0.1": true, "100.100.100.200": true, "100.127.255.255": true, "0.1.2.3": true,
		"198.18.0.1": true, "198.19.255.255": true, "240.0.0.1": true, "255.255.255.255": true,
		"64:ff9b::7f00:1": true, "64:ff9b::a00:1": true, "64:ff9b:1::1": true,
		"100.63.255.255": false, "100.128.0.1": false, "198.17.255.255": false, "198.20.0.1": false,
		"64:ff9b::808:808": true, "203.0.113.7": false, "2001:db8::1": false, "8.8.8.8": false,
		// SEC-22: 6to4 (here 127.0.0.1), Teredo, site-local and IPv4-compatible addresses
		"2002:7f00:1::1": true, "2002:cb00:7107::1": true, "2001:0:4136:e378::1": true, "fec0::1": true,
		"::7f00:1": true, "::a00:1": true, "2001:4860:4860::8888": false, "2003::1": false,
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

// SEC-21: a flood of jails cannot crowd out operational events. Jails are limited to JailBurst
// per window plus one summary, and server.* has its own queue and worker.
func TestJailFloodDoesNotDropOperationalEvents(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{QueueSize: 3})
	h := newHookServer(t, http.StatusOK)
	e.addNotifier(t, KindNtfy, "n", Config{URL: h.srv.URL, Topic: "alerts"}, true)
	ctx := context.Background()
	for i := range 500 {
		e.d.Notify(ctx, Event{Type: EventJailed, Title: "IP jailed", IP: netip.AddrFrom4([4]byte{203, 0, byte(i / 256), byte(i)}).String() + "/32"})
	}
	e.d.Notify(ctx, Event{Type: EventServerDown, Title: "server down"})
	if n := len(e.d.ops); n != 1 {
		t.Fatalf("operational queue holds %d events, want 1", n)
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go e.d.Run(rctx)
	deadline := time.Now().Add(2 * time.Second)
	for !slicesContainsTitle(h.requests(), "server down") {
		if time.Now().After(deadline) {
			t.Fatal("server.down was not delivered")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func slicesContainsTitle(reqs []capturedReq, title string) bool {
	for _, r := range reqs {
		if r.title == title {
			return true
		}
	}
	return false
}

func TestJailAggregation(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{QueueSize: 1000})
	ctx := context.Background()
	jail := func(i int) {
		e.d.Notify(ctx, Event{Type: EventJailed, IP: netip.AddrFrom4([4]byte{198, 51, byte(i / 256), byte(i)}).String() + "/32"})
	}
	for i := range 500 {
		jail(i)
	}
	if n := len(e.d.ch); n != JailBurst {
		t.Fatalf("queued %d jail events, want %d", n, JailBurst)
	}
	e.d.flushJailSummary() // window not over yet
	if n := len(e.d.ch); n != JailBurst {
		t.Fatalf("summary before the window ended: queued %d", n)
	}
	e.clk.Advance(JailWindow)
	e.d.flushJailSummary()
	if n := len(e.d.ch); n != JailBurst+1 {
		t.Fatalf("queued %d, want the burst plus one summary", n)
	}
	for range JailBurst {
		<-e.d.ch
	}
	if s := <-e.d.ch; !strings.Contains(s.Message, "495 more addresses") {
		t.Fatalf("summary = %+v", s)
	}
	e.d.flushJailSummary()
	if n := len(e.d.ch); n != 0 {
		t.Fatalf("a second summary without new jails: queued %d", n)
	}
	jail(1000) // a new window starts after the summary
	if n := len(e.d.ch); n != 1 {
		t.Fatalf("jail after the summary: queued %d, want 1", n)
	}
}

func TestDeliverHonoursRetryAfter(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{})
	var waits []time.Duration
	e.d.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.Header().Set("Retry-After", "2.5")
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.Header().Set("Retry-After", "600")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)
	e.addNotifier(t, KindDiscord, "d", Config{URL: srv.URL + "/api/webhooks/1/tok"}, true)
	e.d.deliver(context.Background(), Event{Type: EventServerDown})
	if calls.Load() != 3 || len(waits) != 2 || waits[0] != 2500*time.Millisecond || waits[1] != MaxRetryAfter {
		t.Fatalf("calls = %d, waits = %v; want Retry-After 2.5s, then capped at %s", calls.Load(), waits, MaxRetryAfter)
	}
}

func TestLocalAddrs(t *testing.T) {
	clk := clock.NewFake(dT0)
	calls := 0
	var fail bool
	list := func() ([]net.Addr, error) {
		calls++
		if fail {
			return nil, errors.New("no interfaces")
		}
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("203.0.113.5"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("2001:db8::5"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}
	l := newLocalAddrs(clk, list)
	for ip, local := range map[string]bool{
		"203.0.113.5": true, "::ffff:203.0.113.5": true, "2001:db8::5": true, "203.0.113.6": false, "2001:db8::6": false,
	} {
		if got := l.contains(netip.MustParseAddr(ip)); got != local {
			t.Errorf("contains(%s) = %v, want %v", ip, got, local)
		}
	}
	if calls != 1 {
		t.Fatalf("interfaces listed %d times, want 1 (cached)", calls)
	}
	fail = true
	clk.Advance(localAddrsTTL)
	if !l.contains(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("without an interface list every address must count as local (fail closed)")
	}
	fail = false
	if l.contains(netip.MustParseAddr("8.8.8.8")) || calls != 3 {
		t.Fatalf("after a failed listing the next call must list again (calls = %d)", calls)
	}
}
