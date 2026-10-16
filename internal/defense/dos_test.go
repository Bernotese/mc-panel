package defense

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// failPassword records a failed password attempt for username from ip.
func (e *env) failPassword(t *testing.T, ip, username string) {
	t.Helper()
	if err := e.svc.RecordAttempt(context.Background(), Attempt{
		IP: addr(ip), Username: username, Stage: StagePassword, Reason: "bad_password",
	}); err != nil {
		t.Fatal(err)
	}
}

// knownDevice records a full login of admin from ip one day ago.
func (e *env) knownDevice(t *testing.T, ip string) {
	t.Helper()
	uid := e.adminID
	e.clk.Advance(-24 * time.Hour)
	defer e.clk.Advance(24 * time.Hour)
	if err := e.svc.RecordAttempt(context.Background(), Attempt{
		IP: addr(ip), Username: "admin", UserID: &uid, Stage: StagePassword, Success: true, Reason: ReasonComplete,
	}); err != nil {
		t.Fatal(err)
	}
}

// The username token is taken before the password check, so parallel requests cannot make
// more guesses than the bucket holds.
func TestCheckLoginUsernameBucketIsAtomic(t *testing.T) {
	e := newEnv(t)
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e.svc.CheckLogin(context.Background(), addr(fmt.Sprintf("2001:db8:%x::1", i+1)), "admin") == nil {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != LoginPerUserLimit.Burst {
		t.Fatalf("parallel checks allowed = %d, want %d", allowed, LoginPerUserLimit.Burst)
	}
}

// SEC-09 (a), (b): attempts from many /64s exhaust the username bucket; a known device of the
// user still gets through, an unknown address gets 429.
func TestKnownDeviceSkipsExhaustedUsernameBucket(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.knownDevice(t, "2001:db8:aaaa:1::10")
	for i := range 10 { // ten /64s in ten /48s, five failed logins each
		ip := fmt.Sprintf("2001:db8:%x::1", i+1)
		for range 5 {
			_ = e.svc.CheckLogin(ctx, addr(ip), "admin")
			e.failPassword(t, ip, "admin")
		}
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("2001:db8:bbbb:1::10"), "admin"), 12*time.Second)
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:aaaa:1::99"), "Admin"); err != nil {
		t.Fatalf("known device (same /64): %v", err)
	}
	if !e.svc.KnownDevice(ctx, addr("2001:db8:aaaa:1::99"), "admin") || e.svc.KnownDevice(ctx, addr("2001:db8:bbbb:1::10"), "admin") {
		t.Fatal("KnownDevice must match the /64 of the earlier full login only")
	}
	if e.svc.KnownDevice(ctx, addr("2001:db8:aaaa:1::99"), "bob") {
		t.Fatal("known devices are per user")
	}
	e.clk.Advance(KnownDeviceWindow) // the full login is older than 30 days now
	for i := range 5 {
		_ = e.svc.CheckLogin(ctx, addr(fmt.Sprintf("2001:db8:c%x::1", i)), "admin")
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("2001:db8:aaaa:1::99"), "admin"), 12*time.Second)
}

// SEC-09 (c): failures spread over one /48 jail the /48 for logins and second factors of every
// /64 inside it; the IP filter and the firewall are not involved, and known devices are exempt.
func TestAggregatedJailSlash48(t *testing.T) {
	ag := newFakeAgent()
	e := newEnv(t, withAgent(ag))
	ctx := context.Background()
	e.knownDevice(t, "2001:db8:5:eeee::1")
	for i := range Net48FailureThreshold { // one failure per /56, so no /56 reaches its threshold
		e.failPassword(t, fmt.Sprintf("2001:db8:5:%x::1", i<<8), fmt.Sprintf("user%d", i))
	}
	jails := e.activeJails(t)
	if len(jails) != 1 || jails[0].IpKey != "2001:db8:5::/48" {
		t.Fatalf("jails = %+v, want one /48 jail", jails)
	}
	for _, ip := range []string{"2001:db8:5:ffff::1", "2001:db8:5:1234:5678::9"} {
		if err := e.svc.CheckLogin(ctx, addr(ip), "admin"); !errors.Is(err, ErrJailed) {
			t.Fatalf("CheckLogin %s = %v, want ErrJailed", ip, err)
		}
		if err := e.svc.CheckSecondFactor(ctx, addr(ip), "s1", "admin"); !errors.Is(err, ErrJailed) {
			t.Fatalf("CheckSecondFactor %s = %v, want ErrJailed", ip, err)
		}
		if rec := e.filter(ip, "/"); rec.Code != http.StatusNoContent {
			t.Fatalf("the IP filter ignores aggregated jails, %s got %d", ip, rec.Code)
		}
	}
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:5:eeee::2"), "admin"); err != nil {
		t.Fatalf("known device inside the jailed /48: %v", err)
	}
	if err := e.svc.CheckSecondFactor(ctx, addr("2001:db8:5:eeee::2"), "s2", "admin"); err != nil {
		t.Fatalf("known device second factor inside the jailed /48: %v", err)
	}
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:6::1"), "admin"); err != nil {
		t.Fatalf("the next /48 must not be jailed: %v", err)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := ag.calls(); len(bans) != 0 {
		t.Fatalf("aggregated jails must not reach the firewall, bans = %v", bans)
	}
	if n, err := e.svc.pendingSync(ctx, e.clk.Now()); err != nil || n != 0 {
		t.Fatalf("pending sync = %d, %v; aggregated jails are never pending", n, err)
	}
}

func TestAggregatedJailSlash56(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range Net56FailureThreshold - 1 {
		e.failPassword(t, fmt.Sprintf("2001:db8:5:%x::1", i), "admin")
	}
	if n := len(e.activeJails(t)); n != 0 {
		t.Fatalf("jails below the /56 threshold = %d", n)
	}
	e.failPassword(t, "2001:db8:5:ff::1", "admin")
	jails := e.activeJails(t)
	if len(jails) != 1 || jails[0].IpKey != "2001:db8:5::/56" {
		t.Fatalf("jails = %+v, want one /56 jail", jails)
	}
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:5:80::1"), "bob"); !errors.Is(err, ErrJailed) {
		t.Fatalf("inside the /56: %v", err)
	}
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:5:100::1"), "bob"); err != nil {
		t.Fatalf("outside the /56: %v", err)
	}
}

// SEC-09 (d): failures of an allowlisted address never count towards an aggregate, and an
// allowlisted address is never affected by one.
func TestAllowlistedNeverAggregated(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.addRule(t, "2001:db8:7:1::/64", "allow", nil)
	for range 2 * Net48FailureThreshold {
		e.failPassword(t, "2001:db8:7:1::5", "admin")
	}
	for i := range Net48FailureThreshold - 1 {
		e.failPassword(t, fmt.Sprintf("2001:db8:7:%x::1", (i+2)<<8), "admin")
	}
	if n := len(e.activeJails(t)); n != 0 {
		t.Fatalf("allowlisted failures must not count, got %d jails", n)
	}
	e.failPassword(t, "2001:db8:7:ff00::1", "admin")
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:7:9999::1"), "bob"); !errors.Is(err, ErrJailed) {
		t.Fatalf("precondition: the /48 must be jailed: %v", err)
	}
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:7:1::5"), "bob"); err != nil {
		t.Fatalf("an allowlisted address must never be jailed by an aggregated jail: %v", err)
	}
}

// A jailed /56 does not end the check: the /64 of a known device inside it is still jailed
// after its own five failures (the known-device exemption must not allow endless guessing).
func TestKnownDeviceInsideAggregateGetsSlash64Jail(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.knownDevice(t, "2001:db8:5:ee::1")
	for i := range Net56FailureThreshold {
		e.failPassword(t, fmt.Sprintf("2001:db8:5:%x::1", i), "admin")
	}
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:5:ee::1"), "admin"); err != nil {
		t.Fatalf("precondition: the known device passes the /56 jail: %v", err)
	}
	for range FailureThreshold {
		e.failPassword(t, "2001:db8:5:ee::1", "admin")
	}
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:5:ee::1"), "admin"); !errors.Is(err, ErrJailed) {
		t.Fatalf("known device after five failures = %v, want ErrJailed", err)
	}
	if rec := e.filter("2001:db8:5:ee::1", "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("the /64 jail applies in the IP filter, got %d", rec.Code)
	}
}

// A /64 that crosses its threshold with the failure that creates the aggregate jail is jailed
// too, and synced to the firewall.
func TestSlash64JailedTogetherWithAggregate(t *testing.T) {
	ag := newFakeAgent()
	e := newEnv(t, withAgent(ag))
	ctx := context.Background()
	for i := range Net56FailureThreshold - FailureThreshold {
		e.failPassword(t, fmt.Sprintf("2001:db8:5:%x::1", i+1), "admin")
	}
	for range FailureThreshold { // the fifth failure is also the twentieth in the /56
		e.failPassword(t, "2001:db8:5:ff::1", "admin")
	}
	keys := map[string]bool{}
	for _, j := range e.activeJails(t) {
		keys[j.IpKey] = true
	}
	if !keys["2001:db8:5::/56"] || !keys["2001:db8:5:ff::/64"] || len(keys) != 2 {
		t.Fatalf("jails = %v, want the /56 and the /64", keys)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	bans, _, _ := ag.calls()
	if len(bans) != 1 || bans[0].String() != "2001:db8:5:ff::/64" {
		t.Fatalf("firewall bans = %v, want only the /64", bans)
	}
}
