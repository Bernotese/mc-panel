//go:build linux

package firewall

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
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
// -10 that drops new connections from jailed sources. It never touches any
// other table.
type NFTables struct {
	mu    sync.Mutex // serialises read-modify-write sequences (list -> delete/add)
	opts  []nftables.ConnOption
	log   *slog.Logger
	table *nftables.Table
	set4  *nftables.Set
	set6  *nftables.Set
}

var _ agentapi.Firewall = (*NFTables)(nil)

// NewNFTables creates the backend and runs the idempotent setup. opts are
// passed to every nftables.New call (tests use nftables.WithNetNSFd).
func NewNFTables(log *slog.Logger, opts ...nftables.ConnOption) (*NFTables, error) {
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: TableName}
	set4, set6 := newSets(table)
	n := &NFTables{opts: opts, log: log, table: table, set4: set4, set6: set6}
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
	return c.Flush()
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

func toRaw(els []nftables.SetElement) []rawElem {
	out := make([]rawElem, 0, len(els))
	for _, e := range els {
		out = append(out, rawElem{Key: e.Key, IntervalEnd: e.IntervalEnd, Timeout: e.Timeout, Expires: e.Expires})
	}
	return out
}
