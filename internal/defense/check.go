package defense

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"mcpanel/internal/httpx"
)

func normalizeUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

// jailed reports whether ip's key has an active jail. Allowlisted IPs are never jailed.
func (s *Service) jailed(ip netip.Addr, now time.Time) bool {
	if !ip.IsValid() {
		return true // fail closed: an unparsable client IP is never let through
	}
	snap := s.snap.Load()
	ip = normalizeIP(ip)
	if snap.allowed(ip, now) {
		return false
	}
	_, ok := snap.jailedUntil(httpx.IPKey(ip), now)
	return ok
}

func (s *Service) CheckLogin(_ context.Context, ip netip.Addr, username string) error {
	if !ip.IsValid() {
		return ErrIPBlocked // fail closed
	}
	now := s.clock.Now()
	if s.jailed(ip, now) {
		return ErrJailed
	}
	if d := s.loginIP.take(httpx.IPKey(normalizeIP(ip)).String(), now); d > 0 {
		return &RateLimitError{RetryAfter: d}
	}
	if s.snap.Load().allowed(normalizeIP(ip), now) {
		// allowlisted addresses skip the per-username bucket, so others cannot lock
		// them out by exhausting it; the per-IP bucket keeps brute force bounded
		return nil
	}
	if d := s.loginUser.take(truncateRunes(normalizeUsername(username), maxUsernameLen), now); d > 0 {
		return &RateLimitError{RetryAfter: d}
	}
	return nil
}

func (s *Service) CheckSecondFactor(_ context.Context, ip netip.Addr, key string) error {
	if !ip.IsValid() {
		return ErrIPBlocked // fail closed
	}
	now := s.clock.Now()
	if s.jailed(ip, now) {
		return ErrJailed
	}
	if d := s.secondFactor.take(key+"|"+httpx.IPKey(normalizeIP(ip)).String(), now); d > 0 {
		return &RateLimitError{RetryAfter: d}
	}
	return nil
}

func (s *Service) CheckAdminNetwork(_ context.Context, ip netip.Addr) error {
	snap := s.snap.Load()
	if !snap.settings.AdminAllowlistOnly {
		return nil
	}
	if !ip.IsValid() {
		return ErrAdminNetwork // fail closed
	}
	ip = normalizeIP(ip)
	if ip.IsLoopback() || snap.allowed(ip, s.clock.Now()) {
		return nil
	}
	return ErrAdminNetwork
}
