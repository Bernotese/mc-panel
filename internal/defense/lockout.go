package defense

import (
	"net/netip"
	"slices"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

// allowedPermanently is like allowed but ignores allow rules that expire.
func (s *snapshot) allowedPermanently(ip netip.Addr, now time.Time) bool {
	ip = normalizeIP(ip)
	for _, r := range s.allow {
		if r.expires.IsZero() && r.active(now) && r.prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// locksOut reports why ip would be locked out of the panel under this snapshot. loopbackAdmin
// mirrors CheckAdminNetwork: loopback is exempt from the admin allowlist only in dev mode.
func (s *snapshot) locksOut(ip netip.Addr, now time.Time, geo GeoResolver, loopbackAdmin bool) string {
	ip = normalizeIP(ip)
	if !ip.IsValid() {
		return "unknown_client_ip" // fail closed: cannot prove the caller stays reachable
	}
	if r := s.blockReason(ip, now, geo); r != "" {
		return r
	}
	if s.settings.AdminAllowlistOnly && !(loopbackAdmin && ip.IsLoopback()) && !s.allowedPermanently(ip, now) {
		// a rule that expires would silently lock the admin out later
		return "admin_allowlist"
	}
	// removing an allow rule must not expose the caller to a jail it currently masks
	if _, ok := s.jailedFor(ip, now); ok && !s.allowed(ip, now) {
		return "jailed"
	}
	return ""
}

func (s *snapshot) withRule(kind string, e ruleEntry) *snapshot {
	c := s.clone()
	if kind == "allow" {
		c.allow = append(c.allow, e)
	} else {
		c.deny = append(c.deny, e)
	}
	return c
}

func (s *snapshot) withoutRule(id int64) *snapshot {
	c := s.clone()
	match := func(r ruleEntry) bool { return r.id == id }
	c.allow = slices.DeleteFunc(c.allow, match)
	c.deny = slices.DeleteFunc(c.deny, match)
	return c
}

func (s *snapshot) withSettings(st store.SecuritySettings) *snapshot {
	c := s.clone()
	c.settings = st
	c.settings.AllowedCountries = slices.Clone(st.AllowedCountries)
	return c
}

func lockoutMessage(reason string) string {
	return "this change would block your current IP address (" + reason + "); use `panel admin unlock` on the server if you are locked out"
}
