//go:build linux

package firewall

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
)

// NFTables manages the table "inet mcpanel" with the sets mcpanel_jail4
// (ipv4_addr, timeout) and mcpanel_jail6 (ipv6_addr, interval+timeout), both
// limited to SetSize elements, and a filter chain on the input hook at priority
// -10 that drops new connections from jailed sources. Unless the egress deny list
// is empty, it also keeps the interval sets mcpanel_egress_deny4/6 and a filter
// chain on the forward hook at priority -10 that drops new connections from the
// container subnets to those sets. It never touches any other table.
type NFTables struct {
	mu         sync.Mutex // serialises read-modify-write sequences (list -> delete/add)
	opts       []nftables.ConnOption
	log        *slog.Logger
	table      *nftables.Table
	set4       *nftables.Set
	set6       *nftables.Set
	egressDeny []netip.Prefix
	egress4    *nftables.Set
	egress6    *nftables.Set
}

var _ Backend = (*NFTables)(nil)

// NewNFTables creates the backend and runs the idempotent setup. egressDeny is the
// container egress deny list (empty: no egress filter, leftovers are removed). opts
// are passed to every nftables.New call (tests use nftables.WithNetNSFd).
func NewNFTables(log *slog.Logger, egressDeny []netip.Prefix, opts ...nftables.ConnOption) (*NFTables, error) {
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: TableName}
	set4, set6 := newSets(table)
	egress4, egress6 := newEgressSets(table)
	n := &NFTables{opts: opts, log: log, table: table, set4: set4, set6: set6,
		egressDeny: slices.Clone(egressDeny), egress4: egress4, egress6: egress6}
	if err := n.setup(); err != nil {
		return nil, fmt.Errorf("nftables: setup: %w", err)
	}
	return n, nil
}

// newSets returns the definitions of the two jail sets.
func newSets(table *nftables.Table) (set4, set6 *nftables.Set) {
	set4 = &nftables.Set{Table: table, Name: Set4Name, KeyType: nftables.TypeIPAddr, HasTimeout: true, Size: SetSize}
	set6 = &nftables.Set{Table: table, Name: Set6Name, KeyType: nftables.TypeIP6Addr, HasTimeout: true, Interval: true, Size: SetSize}
	return set4, set6
}

// newEgressSets returns the definitions of the two egress deny sets.
func newEgressSets(table *nftables.Table) (set4, set6 *nftables.Set) {
	set4 = &nftables.Set{Table: table, Name: EgressSet4Name, KeyType: nftables.TypeIPAddr, Interval: true}
	set6 = &nftables.Set{Table: table, Name: EgressSet6Name, KeyType: nftables.TypeIP6Addr, Interval: true}
	return set4, set6
}

// egressChain is the forward chain of the egress filter.
func (n *NFTables) egressChain() *nftables.Chain {
	policy := nftables.ChainPolicyAccept
	return &nftables.Chain{
		Name:     EgressChainName,
		Table:    n.table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityRef(ChainPriority),
		Policy:   &policy,
	}
}

func (n *NFTables) Name() string { return "nftables" }

// setup creates table, sets and chain if missing and replaces the chain's
// rules with exactly the two drop rules, atomically in one batch. Existing
// set elements (active bans) are kept, except when a set from an older
// version has a different definition (e.g. no size limit): it is recreated in
// the same batch and the panel's firewall sync re-adds the active bans.
func (n *NFTables) setup() error {
	c, err := nftables.New(n.opts...)
	if err != nil {
		return err
	}
	var stale []*nftables.Set
	for _, want := range []*nftables.Set{n.set4, n.set6} {
		// An error means the table or set does not exist yet: nothing to migrate.
		if have, err := c.GetSetByName(n.table, want.Name); err == nil && have.Size != want.Size {
			stale = append(stale, have)
		}
	}
	c.AddTable(n.table)
	policy := nftables.ChainPolicyAccept
	chain := c.AddChain(&nftables.Chain{
		Name:     ChainName,
		Table:    n.table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookInput,
		Priority: nftables.ChainPriorityRef(ChainPriority),
		Policy:   &policy,
	})
	c.FlushChain(chain) // drops the rules referencing the sets, so stale sets can be deleted
	for _, s := range stale {
		n.log.Warn("nftables: recreating set with a new definition; the panel re-syncs active bans", "set", s.Name)
		c.DelSet(s)
	}
	if err := c.AddSet(n.set4, nil); err != nil {
		return err
	}
	if err := c.AddSet(n.set6, nil); err != nil {
		return err
	}
	c.AddRule(&nftables.Rule{Table: n.table, Chain: chain, Exprs: dropExprs(unix.NFPROTO_IPV4, 12, 4, n.set4)})
	c.AddRule(&nftables.Rule{Table: n.table, Chain: chain, Exprs: dropExprs(unix.NFPROTO_IPV6, 8, 16, n.set6)})
	if err := n.setupEgress(c); err != nil {
		return err
	}
	return c.Flush()
}

// setupEgress adds the egress filter to the setup batch. The forward chain keeps its
// rules: after an agent restart the running containers stay filtered until the
// instance manager calls SetEgressSubnets again. The deny sets get exactly the
// configured elements. With an empty deny list chain and sets are removed.
func (n *NFTables) setupEgress(c *nftables.Conn) error {
	if len(n.egressDeny) == 0 {
		// Errors mean the chain or set does not exist: nothing to remove.
		if ch, err := c.ListChain(n.table, EgressChainName); err == nil {
			c.FlushChain(ch)
			c.DelChain(ch)
		}
		for _, name := range []string{EgressSet4Name, EgressSet6Name} {
			if s, err := c.GetSetByName(n.table, name); err == nil {
				c.DelSet(s)
			}
		}
		return nil
	}
	c.AddChain(n.egressChain())
	v4, v6 := egressElements(n.egressDeny)
	for _, e := range []struct {
		set *nftables.Set
		els []rawElem
	}{{n.egress4, v4}, {n.egress6, v6}} {
		if err := c.AddSet(e.set, nil); err != nil {
			return err
		}
		c.FlushSet(e.set)
		if len(e.els) == 0 {
			continue
		}
		if err := c.SetAddElements(e.set, fromRaw(e.els)); err != nil {
			return err
		}
	}
	return nil
}

// SetEgressSubnets replaces the forward chain's rules with one drop rule per
// container subnet, atomically in one batch.
func (n *NFTables) SetEgressSubnets(_ context.Context, subnets []netip.Prefix) error {
	if len(n.egressDeny) == 0 {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	c, err := nftables.New(n.opts...)
	if err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	chain := n.egressChain()
	c.FlushChain(chain)
	for _, p := range egressSubnets(subnets) {
		set := n.egress4
		if p.Addr().Is6() {
			set = n.egress6
		}
		c.AddRule(&nftables.Rule{Table: n.table, Chain: chain, Exprs: egressExprs(p, set)})
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("nftables: egress filter: %w", err)
	}
	return nil
}

// egressExprs builds "meta nfproto <proto> <ip|ip6> saddr <subnet> <ip|ip6> daddr !=
// <subnet> ct state new <ip|ip6> daddr @<deny set> counter drop". Only new
// connections are dropped: replies of connections that came in through the router's
// published port (e.g. from a LAN client) must still leave. Traffic inside the
// subnet (router -> server) is exempt.
func egressExprs(subnet netip.Prefix, set *nftables.Set) []expr.Any {
	proto, saddr, daddr, length := byte(unix.NFPROTO_IPV4), uint32(12), uint32(16), uint32(4)
	if subnet.Addr().Is6() {
		proto, saddr, daddr, length = unix.NFPROTO_IPV6, 8, 24, 16
	}
	mask := net.CIDRMask(subnet.Bits(), int(length)*8)
	network := subnet.Addr().AsSlice()
	zero := make([]byte, length)
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: saddr, Len: length},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: length, Mask: mask, Xor: zero},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: network},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: daddr, Len: length},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: length, Mask: mask, Xor: zero},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: network},
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{
			SourceRegister: 1, DestRegister: 1, Len: 4,
			Mask: binaryutil.NativeEndian.PutUint32(expr.CtStateBitNEW), Xor: []byte{0, 0, 0, 0},
		},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: daddr, Len: length},
		&expr.Lookup{SourceRegister: 1, SetName: set.Name, SetID: set.ID},
		&expr.Counter{},
		&expr.Verdict{Kind: expr.VerdictDrop},
	}
}

// dropExprs builds "ct state new meta nfproto <proto> <ip|ip6> saddr @set counter drop".
// Only new connections are dropped, so replies to the host's own outbound
// connections to a jailed address still arrive. offset/length address the
// source address in the network header (IPv4: 12/4, IPv6: 8/16).
func dropExprs(proto byte, offset, length uint32, set *nftables.Set) []expr.Any {
	return []expr.Any{
		&expr.Ct{Register: 1, Key: expr.CtKeySTATE},
		&expr.Bitwise{
			SourceRegister: 1, DestRegister: 1, Len: 4,
			Mask: binaryutil.NativeEndian.PutUint32(expr.CtStateBitNEW), Xor: []byte{0, 0, 0, 0},
		},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: []byte{0, 0, 0, 0}},
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: length},
		&expr.Lookup{SourceRegister: 1, SetName: set.Name, SetID: set.ID},
		&expr.Counter{},
		&expr.Verdict{Kind: expr.VerdictDrop},
	}
}

func (n *NFTables) Ban(_ context.Context, p netip.Prefix, d time.Duration) error {
	if err := banrules.CheckPrefix(p); err != nil {
		return err
	}
	if _, err := banrules.SecondsFromDuration(d); err != nil {
		return err
	}
	spec, err := elementFor(p)
	if err != nil {
		return fmt.Errorf("%w: %w", banrules.ErrInvalid, err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	c, err := nftables.New(n.opts...)
	if err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	current, err := n.entries(c)
	if err != nil {
		return err
	}
	exists, others := splitExisting(p, current)
	if q, ok := banrules.FindOverlap(p, others); ok {
		return fmt.Errorf("%w: %s overlaps %s", banrules.ErrConflict, p, q)
	}
	set := n.setFor(spec)
	if exists {
		// The kernel ignores "add element" for an existing element, so a
		// re-ban deletes and re-adds it in the same atomic batch.
		if err := c.SetDeleteElements(set, setElements(spec, 0)); err != nil {
			return fmt.Errorf("nftables: %w", err)
		}
	}
	if err := c.SetAddElements(set, setElements(spec, d)); err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("nftables: ban %s: %w", p, err)
	}
	n.log.Debug("nftables: element added", "set", spec.Set, "prefix", p.String(), "timeout", d.String(), "refreshed", exists)
	return nil
}

func (n *NFTables) Unban(_ context.Context, p netip.Prefix) error {
	if err := banrules.CheckPrefix(p); err != nil {
		return err
	}
	spec, err := elementFor(p)
	if err != nil {
		return fmt.Errorf("%w: %w", banrules.ErrInvalid, err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	c, err := nftables.New(n.opts...)
	if err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	current, err := n.entries(c)
	if err != nil {
		return err
	}
	if exists, _ := splitExisting(p, current); !exists {
		n.log.Debug("nftables: unban of absent element ignored", "prefix", p.String())
		return nil
	}
	if err := c.SetDeleteElements(n.setFor(spec), setElements(spec, 0)); err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("nftables: unban %s: %w", p, err)
	}
	n.log.Debug("nftables: element deleted", "set", spec.Set, "prefix", p.String())
	return nil
}

func (n *NFTables) List(_ context.Context) ([]agentapi.Ban, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	c, err := nftables.New(n.opts...)
	if err != nil {
		return nil, fmt.Errorf("nftables: %w", err)
	}
	current, err := n.entries(c)
	if err != nil {
		return nil, err
	}
	return toBans(current), nil
}

func (n *NFTables) setFor(spec elementSpec) *nftables.Set {
	if spec.Set == Set4Name {
		return n.set4
	}
	return n.set6
}

func (n *NFTables) entries(c *nftables.Conn) ([]entry, error) {
	e4, err := c.GetSetElements(n.set4)
	if err != nil {
		return nil, fmt.Errorf("nftables: list %s: %w", Set4Name, err)
	}
	e6, err := c.GetSetElements(n.set6)
	if err != nil {
		return nil, fmt.Errorf("nftables: list %s: %w", Set6Name, err)
	}
	entries, skipped := entriesFromElements(toRaw(e4), toRaw(e6))
	if skipped > 0 {
		n.log.Warn("nftables: ignoring unexpected set elements", "count", skipped)
	}
	return entries, nil
}

// setElements returns the element(s) for spec. Only the interval start
// carries the timeout; the end element must not have one.
func setElements(spec elementSpec, timeout time.Duration) []nftables.SetElement {
	els := []nftables.SetElement{{Key: spec.Key, Timeout: timeout}}
	if spec.End != nil {
		els = append(els, nftables.SetElement{Key: spec.End, IntervalEnd: true})
	}
	return els
}

func fromRaw(els []rawElem) []nftables.SetElement {
	out := make([]nftables.SetElement, 0, len(els))
	for _, e := range els {
		out = append(out, nftables.SetElement{Key: e.Key, IntervalEnd: e.IntervalEnd})
	}
	return out
}

func toRaw(els []nftables.SetElement) []rawElem {
	out := make([]rawElem, 0, len(els))
	for _, e := range els {
		out = append(out, rawElem{Key: e.Key, IntervalEnd: e.IntervalEnd, Timeout: e.Timeout, Expires: e.Expires})
	}
	return out
}
