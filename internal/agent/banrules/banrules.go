// Package banrules holds the agent's validation rules for firewall bans. They
// are stricter than agentapi.ValidateBanPrefix and are shared by the agent
// server, the panel-side client and both firewall backends, so that every
// layer enforces exactly the same rules.
package banrules

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"mcpanel/internal/agent/agentapi"
)

var (
	// ErrInvalid marks requests that can never succeed (bad prefix or
	// timeout). Callers must not retry them.
	ErrInvalid = errors.New("invalid ban request")
	// ErrConflict marks a ban whose prefix overlaps a different, already
	// banned prefix (e.g. a /128 inside a banned /64).
	ErrConflict = errors.New("ban overlaps an existing ban")
)

// MaxTimeoutSeconds is agentapi.MaxBanDuration in whole seconds (691200).
const MaxTimeoutSeconds = int64(agentapi.MaxBanDuration / time.Second)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// CheckPrefix accepts exactly the prefixes the agent is willing to firewall:
// everything agentapi.ValidateBanPrefix accepts, minus IPv4-mapped IPv6
// prefixes, prefixes with host bits set, link-local and multicast prefixes.
func CheckPrefix(p netip.Prefix) error {
	if err := agentapi.ValidateBanPrefix(p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	a := p.Addr()
	switch {
	case a.Is4In6():
		return invalidf("IPv4-mapped IPv6 prefix %s is not allowed, send the IPv4 address instead", p)
	case p != p.Masked():
		return invalidf("prefix %s has host bits set, expected %s", p, p.Masked())
	case a.IsLinkLocalUnicast():
		return invalidf("refusing to ban link-local prefix %s", p)
	case a.IsMulticast():
		return invalidf("refusing to ban multicast prefix %s", p)
	}
	return nil
}

// ParsePrefix parses a prefix from the wire. It must pass CheckPrefix and be
// written in canonical form (netip.Prefix.String), so log lines and set
// elements are unambiguous.
func ParsePrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, invalidf("%v", err)
	}
	if err := CheckPrefix(p); err != nil {
		return netip.Prefix{}, err
	}
	if p.String() != s {
		return netip.Prefix{}, invalidf("prefix %q is not in canonical form %q", s, p.String())
	}
	return p, nil
}

// TimeoutFromSeconds converts timeout_seconds from the wire. The range is
// checked before multiplying, so huge values cannot overflow time.Duration.
func TimeoutFromSeconds(s int64) (time.Duration, error) {
	if s < 1 || s > MaxTimeoutSeconds {
		return 0, invalidf("timeout_seconds must be between 1 and %d, got %d", MaxTimeoutSeconds, s)
	}
	return time.Duration(s) * time.Second, nil
}

// SecondsFromDuration converts a ban duration for the wire, rounding up to
// whole seconds.
func SecondsFromDuration(d time.Duration) (int64, error) {
	if d < time.Second || d > agentapi.MaxBanDuration {
		return 0, invalidf("ban duration must be between 1s and %s, got %s", agentapi.MaxBanDuration, d)
	}
	return RemainingSeconds(d), nil
}

// RemainingSeconds rounds d up to whole seconds; non-positive durations give 0.
func RemainingSeconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	s := int64(d / time.Second)
	if d%time.Second != 0 {
		s++
	}
	return s
}

// FindOverlap returns the first prefix in existing that overlaps p without
// being identical to it.
func FindOverlap(p netip.Prefix, existing []netip.Prefix) (netip.Prefix, bool) {
	for _, q := range existing {
		if q != p && q.Overlaps(p) {
			return q, true
		}
	}
	return netip.Prefix{}, false
}
