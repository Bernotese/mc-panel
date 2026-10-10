// Package agentapi is the wire contract between panel and agent (JSON over HTTP on a Unix socket).
package agentapi

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

const (
	PathHealth = "/v1/health"
	PathBan    = "/v1/firewall/ban"
	PathUnban  = "/v1/firewall/unban"
	PathBans   = "/v1/firewall/bans"

	MaxBanDuration = 8 * 24 * time.Hour
)

type HealthResponse struct {
	Status   string `json:"status"` // "ok"
	Version  string `json:"version"`
	Firewall string `json:"firewall"` // "nftables" | "noop"
	Docker   string `json:"docker"`   // DockerOK | DockerUnavailable
}

type BanRequest struct {
	Prefix         string `json:"prefix"` // e.g. "203.0.113.7/32" or "2001:db8:1:2::/64"
	TimeoutSeconds int64  `json:"timeout_seconds"`
}

type UnbanRequest struct {
	Prefix string `json:"prefix"`
}

type Ban struct {
	Prefix           string `json:"prefix"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
}

type BansResponse struct {
	Bans []Ban `json:"bans"`
}

type ErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"` // ErrorCodeBusy | ErrorCodeDockerUnavailable
}

var ErrUnavailable = errors.New("agent unavailable")

// Client is used by the panel (implemented in stream C).
type Client interface {
	Health(ctx context.Context) (HealthResponse, error)
	Ban(ctx context.Context, prefix netip.Prefix, d time.Duration) error
	Unban(ctx context.Context, prefix netip.Prefix) error
	Bans(ctx context.Context) ([]Ban, error)
}

// Firewall is the agent-side backend (nftables on Linux, noop on macOS).
type Firewall interface {
	Name() string
	Ban(ctx context.Context, prefix netip.Prefix, d time.Duration) error
	Unban(ctx context.Context, prefix netip.Prefix) error
	List(ctx context.Context) ([]Ban, error)
}

// ValidateBanPrefix only allows single IPv4 hosts (/32) and IPv6 /64../128,
// never loopback – so a compromised panel cannot firewall whole networks.
// IPv4-mapped IPv6 addresses, unmasked prefixes, and link-local or multicast
// addresses are refused as well.
func ValidateBanPrefix(p netip.Prefix) error {
	if !p.IsValid() {
		return errors.New("invalid prefix")
	}
	a := p.Addr()
	if a.Is4In6() {
		return fmt.Errorf("refusing IPv4-mapped IPv6 prefix %s", p)
	}
	m := p.Masked()
	if p != m {
		return fmt.Errorf("prefix %s has host bits set, want %s", p, m)
	}
	ma := m.Addr()
	if ma.IsLoopback() || ma.IsUnspecified() || ma.IsLinkLocalUnicast() || ma.IsMulticast() {
		return fmt.Errorf("refusing to ban %s", p)
	}
	if p.Contains(netip.MustParseAddr("::1")) || p.Contains(netip.IPv6Unspecified()) {
		return fmt.Errorf("refusing to ban %s", p)
	}
	if ma.Is4() && m.Bits() != 32 {
		return fmt.Errorf("IPv4 bans must be /32, got %s", p)
	}
	if ma.Is6() && m.Bits() < 64 {
		return fmt.Errorf("IPv6 bans must be /64 or narrower, got %s", p)
	}
	return nil
}
