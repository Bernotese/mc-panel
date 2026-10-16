package firewall

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
	"github.com/Bernotese/mc-panel/internal/clock"
)

// Noop keeps bans in memory and only logs them. No packet is ever dropped.
// State is lost when the agent restarts.
type Noop struct {
	mu   sync.Mutex
	clk  clock.Clock
	log  *slog.Logger
	bans map[netip.Prefix]time.Time // prefix -> expiry
}

var _ Backend = (*Noop)(nil)

func NewNoop(clk clock.Clock, log *slog.Logger) *Noop {
	return &Noop{clk: clk, log: log, bans: make(map[netip.Prefix]time.Time)}
}

func (n *Noop) Name() string { return "noop" }

func (n *Noop) Ban(_ context.Context, p netip.Prefix, d time.Duration) error {
	if err := banrules.CheckPrefix(p); err != nil {
		return err
	}
	if _, err := banrules.SecondsFromDuration(d); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.clk.Now()
	n.purgeLocked(now)
	existing := make([]netip.Prefix, 0, len(n.bans))
	for q := range n.bans {
		existing = append(existing, q)
	}
	if q, ok := banrules.FindOverlap(p, existing); ok {
		return fmt.Errorf("%w: %s overlaps %s", banrules.ErrConflict, p, q)
	}
	n.bans[p] = now.Add(d)
	n.log.Info("noop firewall: ban recorded, packets are NOT dropped", "prefix", p.String(), "timeout", d.String())
	return nil
}

func (n *Noop) Unban(_ context.Context, p netip.Prefix) error {
	if err := banrules.CheckPrefix(p); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.bans, p)
	n.log.Info("noop firewall: unban recorded", "prefix", p.String())
	return nil
}

func (n *Noop) List(_ context.Context) ([]agentapi.Ban, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.clk.Now()
	n.purgeLocked(now)
	entries := make([]entry, 0, len(n.bans))
	for p, exp := range n.bans {
		entries = append(entries, entry{prefix: p, remaining: exp.Sub(now)})
	}
	return toBans(entries), nil
}

func (n *Noop) purgeLocked(now time.Time) {
	for p, exp := range n.bans {
		if !now.Before(exp) {
			delete(n.bans, p)
		}
	}
}

// SetEgressSubnets only logs: without nftables container egress is not filtered.
func (n *Noop) SetEgressSubnets(_ context.Context, subnets []netip.Prefix) error {
	n.log.Info("noop firewall: container egress filter NOT enforced", "subnets", fmt.Sprint(subnets))
	return nil
}

// EgressInPlace is always true: there is nothing that could have been removed.
func (n *Noop) EgressInPlace(context.Context, []netip.Prefix) (bool, error) { return true, nil }
