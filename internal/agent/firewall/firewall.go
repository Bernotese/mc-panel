// Package firewall contains the agent-side firewall backends: nftables
// (Linux) and noop (in-memory, logs only; used on macOS and when configured).
// Besides the IP jail they filter the egress of the Minecraft containers.
package firewall

import (
	"cmp"
	"context"
	"net/netip"
	"slices"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
)

// Names of the nftables objects owned by mc-panel. Nothing outside the tables
// "inet mcpanel" and "bridge mcpanel_web" is ever created, modified or deleted.
const (
	TableName     = "mcpanel"
	Set4Name      = "mcpanel_jail4"
	Set6Name      = "mcpanel_jail6"
	ChainName     = "input"
	ChainPriority = -10
	// SetSize bounds each jail set so a flood of bans cannot exhaust kernel memory.
	SetSize = 65536

	// EgressChainName is the filter chain on the forward hook (also priority -10, so it
	// runs before Docker's own forward rules) that drops new connections from the
	// containers' subnets to the deny sets.
	EgressChainName = "forward"
	EgressSet4Name  = "mcpanel_egress_deny4"
	EgressSet6Name  = "mcpanel_egress_deny6"

	// WebTableName is the table (family bridge) of the port restriction of mcpanel-web:
	// the bridge family sees traffic between two ports of one Docker bridge without
	// br_netfilter. Its filter chain on the forward hook lets mcpanel-web reach only the
	// published ports of each server network.
	WebTableName     = "mcpanel_web"
	WebChainName     = "forward"
	WebChainPriority = -10
)

// Backend is a firewall backend: the IP jail plus the egress filter of the
// Minecraft containers.
type Backend interface {
	agentapi.Firewall
	// SetEgressSubnets makes the egress filter apply to exactly these container
	// subnets (the mcpanel-mc network), replacing earlier ones. With an empty deny
	// list (container_egress_deny = []) it does nothing.
	SetEgressSubnets(ctx context.Context, subnets []netip.Prefix) error
	// EgressInPlace reports whether the filter set by SetEgressSubnets for these
	// subnets is still installed (it may have been flushed from outside).
	EgressInPlace(ctx context.Context, subnets []netip.Prefix) (bool, error)
	// SetWebRules replaces the port restriction of mcpanel-web with exactly these rules;
	// an empty list removes it. Invalid rules are an error and change nothing.
	SetWebRules(ctx context.Context, rules []agentapi.WebRule) error
	// WebRulesInPlace reports whether exactly the restriction SetWebRules writes for these
	// rules is installed (it may have been flushed from outside).
	WebRulesInPlace(ctx context.Context, rules []agentapi.WebRule) (bool, error)
}

// entry is a ban as seen by a backend: prefix plus remaining lifetime.
type entry struct {
	prefix    netip.Prefix
	remaining time.Duration
}

// toBans sorts entries (IPv4 before IPv6, then address, then prefix length)
// and converts them to the wire type. The result is never nil.
func toBans(entries []entry) []agentapi.Ban {
	slices.SortFunc(entries, func(a, b entry) int {
		if c := a.prefix.Addr().Compare(b.prefix.Addr()); c != 0 {
			return c
		}
		return cmp.Compare(a.prefix.Bits(), b.prefix.Bits())
	})
	bans := make([]agentapi.Ban, 0, len(entries))
	for _, e := range entries {
		bans = append(bans, agentapi.Ban{Prefix: e.prefix.String(), ExpiresInSeconds: banrules.RemainingSeconds(e.remaining)})
	}
	return bans
}
