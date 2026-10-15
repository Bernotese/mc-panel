package defense

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"testing"
	"time"
)

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

// SEC-09 (a), (b): failures from many /64s exhaust the username bucket; a known device of the
// user still gets through, an unknown address gets 429.
func TestKnownDeviceSkipsExhaustedUsernameBucket(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.knownDevice(t, "2001:db8:aaaa:1::10")
	for i := range 10 { // ten /64s in ten different /48s: no aggregated jail
		for range 5 {
			e.failPassword(t, fmt.Sprintf("2001:db8:%x::1", i+1), "admin")
		}
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("2001:db8:bbbb:1::10"), "admin"), 12*time.Second)
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:aaaa:1::99"), "Admin"); err != nil {
		t.Fatalf("known device (same /64): %v", err)
	}
	if !e.svc.KnownDevice(ctx, addr("2001:db8:aaaa:1::99"), "admin") || e.svc.KnownDevice(ctx, addr("2001:db8:bbbb:1::10"), "admin") {
		t.Fatal("KnownDevice must match the /64 of the earlier full login only")
	}
	// a known device of another user does not help
	if e.svc.KnownDevice(ctx, addr("2001:db8:aaaa:1::99"), "bob") {
		t.Fatal("known devices are per user")
	}
	// older than 30 days: no longer known
	e.clk.Advance(KnownDeviceWindow)
	for range 5 {
		e.failPassword(t, "2001:db8:cccc:1::1", "admin")
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("2001:db8:aaaa:1::99"), "admin"), 12*time.Second)
}

// SEC-09 (c): failures spread over one /48 jail the /48, and every /64 inside it is jailed.
func TestAggregatedJailSlash48(t *testing.T) {
	e := newEnv(t)
	for i := range Net48FailureThreshold { // one failure per /56, so no /56 reaches its threshold
		e.failPassword(t, fmt.Sprintf("2001:db8:5:%x::1", i<<8), fmt.Sprintf("user%d", i))
	}
	jails := e.activeJails(t)
	if len(jails) != 1 || jails[0].IpKey != "2001:db8:5::/48" {
		t.Fatalf("jails = %+v, want one /48 jail", jails)
	}
	now := e.clk.Now()
	for _, ip := range []string{"2001:db8:5:ffff::1", "2001:db8:5:1234:5678::9"} {
		if !e.svc.jailed(addr(ip), now) {
			t.Fatalf("%s inside the jailed /48 must be jailed", ip)
		}
		if rec := e.filter(ip, "/"); rec.Code != http.StatusForbidden {
			t.Fatalf("IP filter for %s = %d", ip, rec.Code)
		}
		if err := e.svc.CheckLogin(context.Background(), addr(ip), "admin"); !errors.Is(err, ErrJailed) {
			t.Fatalf("CheckLogin %s = %v, want ErrJailed", ip, err)
		}
	}
	if e.svc.jailed(addr("2001:db8:6::1"), now) {
		t.Fatal("the next /48 must not be jailed")
	}
}

func TestAggregatedJailSlash56(t *testing.T) {
	e := newEnv(t)
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
	if !e.svc.jailed(addr("2001:db8:5:80::1"), e.clk.Now()) || e.svc.jailed(addr("2001:db8:5:100::1"), e.clk.Now()) {
		t.Fatal("the /56 jail must cover exactly its /56")
	}
}

// SEC-09 (d): an allowlisted address is never aggregated into a jail.
func TestAllowlistedNeverAggregated(t *testing.T) {
	e := newEnv(t)
	e.addRule(t, "2001:db8:7:1::/64", "allow", nil)
	for range 2 * Net48FailureThreshold {
		e.failPassword(t, "2001:db8:7:1::5", "admin")
	}
	if n := len(e.activeJails(t)); n != 0 {
		t.Fatalf("failures of an allowlisted address must not jail anything, got %d jails", n)
	}
	// others in the same /48 get it jailed, the allowlisted address stays reachable
	for i := range Net48FailureThreshold {
		e.failPassword(t, fmt.Sprintf("2001:db8:7:%x::1", (i+2)<<8), "admin")
	}
	if !e.svc.jailed(addr("2001:db8:7:9999::1"), e.clk.Now()) {
		t.Fatal("precondition: the /48 must be jailed")
	}
	if e.svc.jailed(addr("2001:db8:7:1::5"), e.clk.Now()) {
		t.Fatal("an allowlisted address must never be jailed by an aggregated jail")
	}
	if rec := e.filter("2001:db8:7:1::5", "/"); rec.Code != http.StatusNoContent {
		t.Fatalf("IP filter for the allowlisted address = %d", rec.Code)
	}
}

// An aggregated jail replaces the narrower bans inside it in the firewall: the agent refuses
// overlapping bans, so the /64s are unbanned first and not re-banned while the /48 is active.
func TestSyncAggregatedJailReplacesNarrowerBans(t *testing.T) {
	ag := newFakeAgent()
	ag.overlap = true
	e := newEnv(t, withAgent(ag))
	ctx := context.Background()
	e.failPassword(t, "2001:db8:5::1", "a") // a /64 jail first
	for range FailureThreshold - 1 {
		e.failPassword(t, "2001:db8:5::1", "a")
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := ag.bans[netip.MustParsePrefix("2001:db8:5::/64")]; !ok {
		t.Fatalf("precondition: /64 must be banned, bans = %v", ag.bans)
	}
	for i := range Net48FailureThreshold {
		e.failPassword(t, fmt.Sprintf("2001:db8:5:%x::1", (i+1)<<8), "a")
	}
	for range 2 { // the second pass must not re-ban the covered /64
		if err := e.svc.SyncOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(ag.bans) != 1 {
		t.Fatalf("bans = %v, want only the /48", ag.bans)
	}
	if _, ok := ag.bans[netip.MustParsePrefix("2001:db8:5::/48")]; !ok {
		t.Fatalf("bans = %v, want the /48", ag.bans)
	}
	if n, err := e.svc.pendingSync(ctx, e.clk.Now()); err != nil || n != 0 {
		t.Fatalf("pending sync = %d, %v; covered jails are not pending", n, err)
	}
}
