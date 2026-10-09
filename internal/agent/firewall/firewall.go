// Package firewall contains the agent-side firewall backends: nftables
// (Linux) and noop (in-memory, logs only; used on macOS and when configured).
package firewall

import (
	"cmp"
	"net/netip"
	"slices"
	"time"

	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/agent/banrules"
)

// Names of the nftables objects owned by mc-panel. Nothing outside the table
// "inet mcpanel" is ever created, modified or deleted.
const (
	TableName     = "mcpanel"
	Set4Name      = "mcpanel_jail4"
	Set6Name      = "mcpanel_jail6"
	ChainName     = "input"
	ChainPriority = -10
)

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
