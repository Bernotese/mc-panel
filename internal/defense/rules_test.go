package defense

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

func TestReloadBuildsSnapshot(t *testing.T) {
	e := newEnv(t)
	exp := t0.Add(time.Hour)
	e.addRule(t, "203.0.113.0/24", "allow", nil)
	e.addRule(t, "198.51.100.0/24", "deny", nil)
	e.addRule(t, "192.0.2.0/24", "deny", &exp)
	e.addRule(t, "2001:db8:1::/48", "deny", nil)
	e.saveSettings(t, store.SecuritySettings{AdminAllowlistOnly: true, AllowedCountries: []string{"DE"}})

	s := e.svc.snap.Load()
	checks := []struct {
		name      string
		got, want bool
	}{
		{"allow v4", s.allowed(addr("203.0.113.9"), t0), true},
		{"allow mapped v4", s.allowed(addr("::ffff:203.0.113.9"), t0), true},
		{"not allowed", s.allowed(addr("203.0.114.1"), t0), false},
		{"deny v4", s.denied(addr("198.51.100.7"), t0), true},
		{"deny v6", s.denied(addr("2001:db8:1:ff::1"), t0), true},
		{"deny v6 zone stripped", s.denied(addr("2001:db8:1::1%en0"), t0), true},
		{"expiring deny before expiry", s.denied(addr("192.0.2.1"), t0), true},
		{"expiring deny at expiry", s.denied(addr("192.0.2.1"), exp), false},
		{"allow overlaps /64 key", s.allowOverlaps(netip.MustParsePrefix("203.0.113.9/32"), t0), true},
		{"no overlap", s.allowOverlaps(netip.MustParsePrefix("198.51.100.7/32"), t0), false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
	if !s.settings.AdminAllowlistOnly || len(s.settings.AllowedCountries) != 1 || s.settings.AllowedCountries[0] != "DE" {
		t.Fatalf("settings not loaded: %+v", s.settings)
	}
}

func TestReloadLoadsOnlyActiveJails(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.insertJail(t, "203.0.113.1/32", t0, t0.Add(15*time.Minute))
	e.insertJail(t, "203.0.113.2/32", t0.Add(-time.Hour), t0.Add(-time.Minute))
	lifted := e.insertJail(t, "203.0.113.3/32", t0, t0.Add(time.Hour))
	if _, err := e.db.ExecContext(ctx, "UPDATE jails SET lifted_at = ?, lifted_by = 'x' WHERE id = ?", t0.UnixMilli(), lifted); err != nil {
		t.Fatal(err)
	}
	e.reload(t)
	s := e.svc.snap.Load()
	until, ok := s.jailedUntil(netip.MustParsePrefix("203.0.113.1/32"), t0)
	if !ok || !until.Equal(t0.Add(15*time.Minute)) {
		t.Fatalf("active jail: until=%v ok=%v", until, ok)
	}
	for _, k := range []string{"203.0.113.2/32", "203.0.113.3/32"} {
		if _, ok := s.jailedUntil(netip.MustParsePrefix(k), t0); ok {
			t.Errorf("%s must not be jailed", k)
		}
	}
	if _, ok := s.jailedUntil(netip.MustParsePrefix("203.0.113.1/32"), t0.Add(15*time.Minute)); ok {
		t.Error("expiry must be evaluated against now, not only at reload")
	}
}

func TestAddJailIsCopyOnWrite(t *testing.T) {
	e := newEnv(t)
	before := e.svc.snap.Load()
	key := netip.MustParsePrefix("2001:db8:1:2::/64")
	e.svc.addJail(key, t0.Add(time.Hour))
	if _, ok := before.jailedUntil(key, t0); ok {
		t.Fatal("previous snapshot must not be mutated")
	}
	if _, ok := e.svc.snap.Load().jailedUntil(key, t0); !ok {
		t.Fatal("current snapshot must contain the new jail")
	}
}

func TestIsLocal(t *testing.T) {
	for s, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "192.168.1.1": true, "fd00::1": true,
		"fe80::1": true, "0.0.0.0": true, "203.0.113.7": false, "2001:db8::1": false, "::ffff:10.0.0.1": true,
	} {
		if got := isLocal(addr(s)); got != want {
			t.Errorf("isLocal(%s) = %v, want %v", s, got, want)
		}
	}
}
