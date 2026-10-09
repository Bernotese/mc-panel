package defense

import (
	"net/netip"
	"slices"
	"time"

	"mcpanel/internal/store"
)

// locksOut reports why ip would be locked out of the panel under this snapshot.
func (s *snapshot) locksOut(ip netip.Addr, now time.Time, geo GeoResolver) string {
	ip = normalizeIP(ip)
	if !ip.IsValid() {
		return "unknown_client_ip" // fail closed: cannot prove the caller stays reachable
	}
	if r := s.blockReason(ip, now, geo); r != "" {
		return r
	}
	if s.settings.AdminAllowlistOnly && !ip.IsLoopback() && !s.allowed(ip, now) {
		return "admin_allowlist"
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
