package defense

import (
	"context"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

type ruleEntry struct {
	id      int64
	prefix  netip.Prefix
	expires time.Time // zero = never expires
}

func (r ruleEntry) active(now time.Time) bool { return r.expires.IsZero() || now.Before(r.expires) }

// snapshot is an immutable view of rules, active jails and settings.
// Writers build a new snapshot (copy-on-write) and store it atomically.
type snapshot struct {
	allow    []ruleEntry
	deny     []ruleEntry
	jails    map[netip.Prefix]time.Time // ip_key -> latest expiry
	settings store.SecuritySettings
}

func normalizeIP(ip netip.Addr) netip.Addr { return ip.Unmap().WithZone("") }

// isLocal reports addresses that are never subject to the country filter.
func isLocal(ip netip.Addr) bool {
	ip = normalizeIP(ip)
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

func matchAny(rules []ruleEntry, ip netip.Addr, now time.Time) bool {
	ip = normalizeIP(ip)
	for _, r := range rules {
		if r.active(now) && r.prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *snapshot) allowed(ip netip.Addr, now time.Time) bool { return matchAny(s.allow, ip, now) }
func (s *snapshot) denied(ip netip.Addr, now time.Time) bool  { return matchAny(s.deny, ip, now) }

// allowOverlaps reports whether any active allow rule overlaps p (used for manual jails of /64 keys).
func (s *snapshot) allowOverlaps(p netip.Prefix, now time.Time) bool {
	for _, r := range s.allow {
		if r.active(now) && r.prefix.Overlaps(p) {
			return true
		}
	}
	return false
}

func (s *snapshot) jailedUntil(key netip.Prefix, now time.Time) (time.Time, bool) {
	until, ok := s.jails[key]
	if !ok || !now.Before(until) {
		return time.Time{}, false
	}
	return until, true
}

// aggregateJailedUntil returns the latest expiry of the active /56 and /48 jails covering an
// IPv6 address (SEC-09).
func (s *snapshot) aggregateJailedUntil(ip netip.Addr, now time.Time) (time.Time, bool) {
	ip = normalizeIP(ip)
	if !ip.Is6() {
		return time.Time{}, false
	}
	var (
		until  time.Time
		jailed bool
	)
	for _, key := range []netip.Prefix{netPrefix(ip, 56), netPrefix(ip, 48)} {
		if u, ok := s.jailedUntil(key, now); ok && (!jailed || u.After(until)) {
			until, jailed = u, true
		}
	}
	return until, jailed
}

// isAggregateKey reports a /56 or /48 jail (app-level only, never sent to the firewall).
func isAggregateKey(p netip.Prefix) bool { return p.Addr().Is6() && p.Bits() < 64 }

func netPrefix(ip netip.Addr, bits int) netip.Prefix {
	p, _ := ip.Prefix(bits)
	return p
}

func (s *snapshot) clone() *snapshot {
	c := &snapshot{
		allow:    slices.Clone(s.allow),
		deny:     slices.Clone(s.deny),
		jails:    maps.Clone(s.jails),
		settings: s.settings,
	}
	if c.jails == nil {
		c.jails = make(map[netip.Prefix]time.Time)
	}
	c.settings.AllowedCountries = slices.Clone(s.settings.AllowedCountries)
	return c
}

// Reload rebuilds the snapshot from the database.
func (s *Service) Reload(ctx context.Context) error {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	now := s.clock.Now()
	rules, err := s.q.DefenseListIPRules(ctx)
	if err != nil {
		return fmt.Errorf("defense: load ip rules: %w", err)
	}
	jails, err := s.q.DefenseListActiveJails(ctx, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("defense: load jails: %w", err)
	}
	settings, err := store.LoadSecuritySettings(ctx, s.q)
	if err != nil {
		return fmt.Errorf("defense: load settings: %w", err)
	}
	snap := &snapshot{jails: make(map[netip.Prefix]time.Time, len(jails)), settings: settings}
	for _, r := range rules {
		p, err := netip.ParsePrefix(r.Cidr)
		if err != nil {
			s.log.Error("defense: skipping invalid ip rule", "id", r.ID, "cidr", r.Cidr)
			continue
		}
		e := ruleEntry{id: r.ID, prefix: p.Masked()}
		if r.ExpiresAt.Valid {
			e.expires = time.UnixMilli(r.ExpiresAt.Int64)
		}
		if r.Kind == "allow" {
			snap.allow = append(snap.allow, e)
		} else {
			snap.deny = append(snap.deny, e)
		}
	}
	for _, j := range jails {
		p, err := netip.ParsePrefix(j.IpKey)
		if err != nil {
			s.log.Error("defense: skipping jail with invalid ip_key", "id", j.ID, "ip_key", j.IpKey)
			continue
		}
		exp := time.UnixMilli(j.ExpiresAt)
		if cur, ok := snap.jails[p]; !ok || exp.After(cur) {
			snap.jails[p] = exp
		}
	}
	s.snap.Store(snap)
	return nil
}

// reloadOrLog is used after successful mutations: the change is committed, so a
// failed reload is only logged (the periodic refresh repairs the cache).
func (s *Service) reloadOrLog(ctx context.Context) {
	if err := s.Reload(ctx); err != nil {
		s.log.Error("defense: reload failed", "err", err)
	}
}

// addJail makes a new jail visible to the middleware immediately.
func (s *Service) addJail(key netip.Prefix, expires time.Time) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	c := s.snap.Load().clone()
	if cur, ok := c.jails[key]; !ok || expires.After(cur) {
		c.jails[key] = expires
	}
	s.snap.Store(c)
}
