package firewall_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/agent/banrules"
	"mcpanel/internal/agent/firewall"
	"mcpanel/internal/clock"
)

func newNoop(t *testing.T) (*firewall.Noop, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
	return firewall.NewNoop(clk, slog.New(slog.NewTextHandler(io.Discard, nil))), clk
}

func assertBans(t *testing.T, fw agentapi.Firewall, want []agentapi.Ban) {
	t.Helper()
	got, err := fw.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Fatal("List returned nil, want non-nil slice")
	}
	if !slices.Equal(got, want) {
		t.Fatalf("List = %+v, want %+v", got, want)
	}
}

func TestNoopName(t *testing.T) {
	fw, _ := newNoop(t)
	if fw.Name() != "noop" {
		t.Fatalf("Name = %q", fw.Name())
	}
}

func TestNoopBanListExpire(t *testing.T) {
	fw, clk := newNoop(t)
	ctx := context.Background()
	if err := fw.Ban(ctx, netip.MustParsePrefix("2001:db8:1:2::/64"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := fw.Ban(ctx, netip.MustParsePrefix("203.0.113.7/32"), 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	assertBans(t, fw, []agentapi.Ban{
		{Prefix: "203.0.113.7/32", ExpiresInSeconds: 900},
		{Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 3600},
	})
	clk.Advance(10 * time.Minute)
	assertBans(t, fw, []agentapi.Ban{
		{Prefix: "203.0.113.7/32", ExpiresInSeconds: 300},
		{Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 3000},
	})
	clk.Advance(5 * time.Minute) // IPv4 ban expires exactly now
	assertBans(t, fw, []agentapi.Ban{{Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 2700}})
}

func TestNoopRebanReplacesTimeout(t *testing.T) {
	fw, _ := newNoop(t)
	ctx := context.Background()
	p := netip.MustParsePrefix("203.0.113.7/32")
	for _, step := range []struct {
		d    time.Duration
		want int64
	}{{time.Minute, 60}, {2 * time.Hour, 7200}, {30 * time.Second, 30}} {
		if err := fw.Ban(ctx, p, step.d); err != nil {
			t.Fatal(err)
		}
		assertBans(t, fw, []agentapi.Ban{{Prefix: "203.0.113.7/32", ExpiresInSeconds: step.want}})
	}
}

func TestNoopUnbanIsIdempotent(t *testing.T) {
	fw, _ := newNoop(t)
	ctx := context.Background()
	p := netip.MustParsePrefix("203.0.113.7/32")
	if err := fw.Unban(ctx, p); err != nil {
		t.Fatalf("Unban of unknown prefix: %v", err)
	}
	if err := fw.Ban(ctx, p, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := fw.Unban(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := fw.Unban(ctx, p); err != nil {
		t.Fatalf("second Unban: %v", err)
	}
	assertBans(t, fw, []agentapi.Ban{})
}

func TestNoopRejectsInvalid(t *testing.T) {
	fw, _ := newNoop(t)
	ctx := context.Background()
	cases := []struct {
		p string
		d time.Duration
	}{
		{"203.0.113.0/24", time.Minute},
		{"::ffff:203.0.113.7/128", time.Minute},
		{"203.0.113.7/32", 0},
		{"203.0.113.7/32", agentapi.MaxBanDuration + time.Second},
	}
	for _, tc := range cases {
		if err := fw.Ban(ctx, netip.MustParsePrefix(tc.p), tc.d); !errors.Is(err, banrules.ErrInvalid) {
			t.Errorf("Ban(%s, %v) = %v, want ErrInvalid", tc.p, tc.d, err)
		}
	}
	if err := fw.Unban(ctx, netip.MustParsePrefix("0.0.0.0/0")); !errors.Is(err, banrules.ErrInvalid) {
		t.Errorf("Unban(0.0.0.0/0) = %v, want ErrInvalid", err)
	}
	assertBans(t, fw, []agentapi.Ban{})
}

func TestNoopRejectsOverlap(t *testing.T) {
	fw, _ := newNoop(t)
	ctx := context.Background()
	if err := fw.Ban(ctx, netip.MustParsePrefix("2001:db8:1:2::/64"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := fw.Ban(ctx, netip.MustParsePrefix("2001:db8:1:2::1/128"), time.Hour); !errors.Is(err, banrules.ErrConflict) {
		t.Fatalf("overlapping ban = %v, want ErrConflict", err)
	}
	assertBans(t, fw, []agentapi.Ban{{Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 3600}})
}

func TestNoopConcurrent(t *testing.T) {
	fw, _ := newNoop(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range byte(50) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := netip.PrefixFrom(netip.AddrFrom4([4]byte{203, 0, 113, i}), 32)
			if err := fw.Ban(ctx, p, time.Hour); err != nil {
				t.Errorf("Ban %s: %v", p, err)
				return
			}
			if i%2 == 0 {
				if err := fw.Unban(ctx, p); err != nil {
					t.Errorf("Unban %s: %v", p, err)
				}
			}
			if _, err := fw.List(ctx); err != nil {
				t.Errorf("List: %v", err)
			}
		}()
	}
	wg.Wait()
	bans, err := fw.List(ctx)
	if err != nil || len(bans) != 25 {
		t.Fatalf("List = %d bans, %v; want 25", len(bans), err)
	}
}
