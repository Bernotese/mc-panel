package defense

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

func wantRateLimited(t *testing.T, err error, limit time.Duration) {
	t.Helper()
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if rl.RetryAfter <= 0 || rl.RetryAfter > limit {
		t.Fatalf("RetryAfter = %v, want (0, %v]", rl.RetryAfter, limit)
	}
}

func TestCheckLoginRateLimitPerIP(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ip := addr("203.0.113.7")
	for i := 0; i < 5; i++ {
		if err := e.svc.CheckLogin(ctx, ip, fmt.Sprintf("user%d", i)); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, ip, "user5"), 6*time.Second)
	// IPv4-mapped form shares the bucket
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("::ffff:203.0.113.7"), "user6"), 6*time.Second)
	e.clk.Advance(6*time.Second + time.Millisecond)
	if err := e.svc.CheckLogin(ctx, ip, "user7"); err != nil {
		t.Fatalf("after refill: %v", err)
	}
}

func TestCheckLoginIPv6Slash64SharesBucket(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		ip := addr(fmt.Sprintf("2001:db8:1:2::%d", i))
		if err := e.svc.CheckLogin(ctx, ip, fmt.Sprintf("user%d", i)); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("2001:db8:1:2:ffff::9"), "user9"), 6*time.Second)
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:1:3::1"), "user10"); err != nil {
		t.Fatalf("other /64 must have its own bucket: %v", err)
	}
}

func TestCheckLoginRateLimitPerUsername(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	names := []string{"admin", "Admin", " ADMIN ", "admin", "aDmIn"}
	for i, n := range names {
		ip := addr(fmt.Sprintf("198.51.100.%d", i+1))
		if err := e.svc.CheckLogin(ctx, ip, n); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("198.51.100.99"), "admin"), 12*time.Second)
}

func TestCheckLoginJailed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.addJail(netip.MustParsePrefix("203.0.113.7/32"), t0.Add(15*time.Minute))
	for _, ip := range []string{"203.0.113.7", "::ffff:203.0.113.7"} {
		if err := e.svc.CheckLogin(ctx, addr(ip), "admin"); !errors.Is(err, ErrJailed) {
			t.Fatalf("%s: err = %v, want ErrJailed", ip, err)
		}
		if err := e.svc.CheckSecondFactor(ctx, addr(ip), "s1", "admin"); !errors.Is(err, ErrJailed) {
			t.Fatalf("%s 2FA: err = %v, want ErrJailed", ip, err)
		}
	}
	e.clk.Advance(15 * time.Minute)
	if err := e.svc.CheckLogin(ctx, addr("203.0.113.7"), "admin"); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	// an allowlisted address is never treated as jailed
	// (jail persisted in the DB so that the reload triggered by addRule keeps it)
	e.insertJail(t, "203.0.113.8/32", e.clk.Now(), e.clk.Now().Add(time.Hour))
	e.addRule(t, "203.0.113.8/32", "allow", nil)
	if _, ok := e.svc.snap.Load().jailedUntil(netip.MustParsePrefix("203.0.113.8/32"), e.clk.Now()); !ok {
		t.Fatal("precondition: jail must be in the snapshot")
	}
	if err := e.svc.CheckLogin(ctx, addr("203.0.113.8"), "bob"); err != nil {
		t.Fatalf("allowlisted: %v", err)
	}
}

func TestCheckSecondFactorRateLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ip := addr("203.0.113.7")
	for i := 0; i < 5; i++ {
		if err := e.svc.CheckSecondFactor(ctx, ip, "session-1", "admin"); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	wantRateLimited(t, e.svc.CheckSecondFactor(ctx, ip, "session-1", "admin"), 12*time.Second)
	if err := e.svc.CheckSecondFactor(ctx, ip, "session-2", "admin"); err != nil {
		t.Fatalf("other session: %v", err)
	}
	if err := e.svc.CheckSecondFactor(ctx, addr("203.0.113.8"), "session-1", "admin"); err != nil {
		t.Fatalf("same session from other IP has its own bucket: %v", err)
	}
}

func TestCheckAdminNetwork(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	public := addr("203.0.113.7")
	if err := e.svc.CheckAdminNetwork(ctx, public); err != nil {
		t.Fatalf("restriction off: %v", err)
	}
	e.saveSettings(t, store.SecuritySettings{AdminAllowlistOnly: true, AllowedCountries: []string{}})
	if err := e.svc.CheckAdminNetwork(ctx, public); !errors.Is(err, ErrAdminNetwork) {
		t.Fatalf("not allowlisted: err = %v", err)
	}
	for _, lo := range []string{"127.0.0.1", "::1"} {
		// production: the panel sits behind a proxy, a loopback client IP is not trustworthy
		if err := e.svc.CheckAdminNetwork(ctx, addr(lo)); !errors.Is(err, ErrAdminNetwork) {
			t.Fatalf("prod: loopback %s must not be exempt: %v", lo, err)
		}
	}
	dev := newEnv(t, func(d *Deps) { d.Dev = true })
	dev.saveSettings(t, store.SecuritySettings{AdminAllowlistOnly: true, AllowedCountries: []string{}})
	for _, lo := range []string{"127.0.0.1", "::1"} {
		if err := dev.svc.CheckAdminNetwork(ctx, addr(lo)); err != nil {
			t.Fatalf("dev: loopback %s must be allowed: %v", lo, err)
		}
	}
	if err := dev.svc.CheckAdminNetwork(ctx, public); !errors.Is(err, ErrAdminNetwork) {
		t.Fatalf("dev: public IP must still be checked: %v", err)
	}
	e.addRule(t, "203.0.113.0/24", "allow", nil)
	if err := e.svc.CheckAdminNetwork(ctx, public); err != nil {
		t.Fatalf("allowlisted: %v", err)
	}
}

// An unparsable client IP (zero netip.Addr) must fail closed everywhere.
func TestInvalidIPFailsClosed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var zero netip.Addr
	if err := e.svc.CheckLogin(ctx, zero, "admin"); !errors.Is(err, ErrIPBlocked) {
		t.Fatalf("CheckLogin: err = %v, want ErrIPBlocked", err)
	}
	if err := e.svc.CheckSecondFactor(ctx, zero, "s1", "admin"); !errors.Is(err, ErrIPBlocked) {
		t.Fatalf("CheckSecondFactor: err = %v, want ErrIPBlocked", err)
	}
	e.saveSettings(t, store.SecuritySettings{AdminAllowlistOnly: true, AllowedCountries: []string{}})
	if err := e.svc.CheckAdminNetwork(ctx, zero); !errors.Is(err, ErrAdminNetwork) {
		t.Fatalf("CheckAdminNetwork: err = %v, want ErrAdminNetwork", err)
	}
	if !e.svc.jailed(zero, e.clk.Now()) {
		t.Fatal("jailed(zero) must be true")
	}
}

func TestAllowlistedIPSkipsOnlyUsernameBucket(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addRule(t, "192.0.2.0/24", "allow", nil)
	// others exhaust the username bucket of the admin
	for i := 0; i < 5; i++ {
		if err := e.svc.CheckLogin(ctx, addr(fmt.Sprintf("198.51.100.%d", i+1)), "admin"); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("198.51.100.99"), "admin"), 12*time.Second)
	// the allowlisted address is not blocked by the exhausted username bucket ...
	for i := 0; i < 5; i++ {
		if err := e.svc.CheckLogin(ctx, addr("192.0.2.7"), "admin"); err != nil {
			t.Fatalf("allowlisted login %d must not be blocked by the username bucket: %v", i, err)
		}
	}
	// ... but the per-IP limiter still bounds brute force from the allowlisted network
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("192.0.2.7"), "admin"), 6*time.Second)
}

func TestLoginUsernameBucketKeyIsCapped(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	long := strings.Repeat("a", 64)
	for i := 0; i < 5; i++ {
		if err := e.svc.CheckLogin(ctx, addr(fmt.Sprintf("198.51.100.%d", i+1)), long+fmt.Sprint(i)); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// names that only differ after 64 runes share a bucket
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("198.51.100.99"), long+"zzz"), 12*time.Second)
	if n := e.svc.loginUser.size(); n != 1 {
		t.Fatalf("username buckets = %d, want 1", n)
	}
}
