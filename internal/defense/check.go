package defense

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
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
	_, ok := snap.jailedFor(ip, now)
	return ok
}

func usernameKey(username string) string {
	return truncateRunes(normalizeUsername(username), maxUsernameLen)
}

// CheckLogin runs before a password is verified. The per-username bucket only counts failed
// attempts (RecordAttempt consumes it), and a known device of the user (see KnownDevice) passes
// even when it is empty: rotating addresses can slow down guessing a password, but never lock
// the owner out (spec §7.2, SEC-09).
func (s *Service) CheckLogin(ctx context.Context, ip netip.Addr, username string) error {
	if !ip.IsValid() {
		return ErrIPBlocked // fail closed
	}
	now := s.clock.Now()
	if s.jailed(ip, now) {
		return ErrJailed
	}
	allowlisted := s.snap.Load().allowed(normalizeIP(ip), now)
	ipKey := httpx.IPKey(normalizeIP(ip)).String()
	take := s.loginIP.take
	if allowlisted {
		take = s.loginIP.takeExempt // never fall into the shared overflow bucket
	}
	if d := take(ipKey, now); d > 0 {
		return &RateLimitError{RetryAfter: d}
	}
	if allowlisted {
		// allowlisted addresses skip the per-username bucket, so others cannot lock
		// them out by exhausting it; the per-IP bucket keeps brute force bounded
		return nil
	}
	if d := s.loginUser.wait(usernameKey(username), now); d > 0 {
		if s.knownDevice(ctx, ip, username, now) {
			return nil
		}
		return &RateLimitError{RetryAfter: d}
	}
	return nil
}

// KnownDevice reports whether ip's key completed a full login as username within
// KnownDeviceWindow. Such logins skip the username bucket and may use the reserved argon2 slot.
func (s *Service) KnownDevice(ctx context.Context, ip netip.Addr, username string) bool {
	return s.knownDevice(ctx, ip, username, s.clock.Now())
}

func (s *Service) knownDevice(ctx context.Context, ip netip.Addr, username string, now time.Time) bool {
	key := httpx.IPKey(normalizeIP(ip))
	if !key.IsValid() || username == "" {
		return false
	}
	n, err := s.q.DefenseKnownDevice(ctx, store.DefenseKnownDeviceParams{
		Username: username, IpKey: key.String(), Since: now.Add(-KnownDeviceWindow).UnixMilli(),
	})
	if err != nil {
		s.log.Error("defense: known device lookup", "err", err)
		return false // fail closed: the bucket applies
	}
	return n > 0
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
	if (s.dev && ip.IsLoopback()) || snap.allowed(ip, s.clock.Now()) {
		return nil
	}
	return ErrAdminNetwork
}
