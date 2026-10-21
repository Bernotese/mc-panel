//go:build linux

package firewall

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"reflect"
	"slices"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// The port restriction of mcpanel-web lives in its own table "bridge mcpanel_web": the
// bridge family's forward hook sees frames between two ports of one Docker bridge, which
// the inet family only sees with br_netfilter. It is stateless on purpose: replies come
// from the servers' addresses and match no rule, and a connection from mcpanel-web is only
// possible to Dest on one of Ports.

func webTable() *nftables.Table {
	return &nftables.Table{Family: nftables.TableFamilyBridge, Name: WebTableName}
}

func webChain(table *nftables.Table) *nftables.Chain {
	policy := nftables.ChainPolicyAccept
	return &nftables.Chain{
		Name:     WebChainName,
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityRef(WebChainPriority),
		Policy:   &policy,
	}
}

// SetWebRules replaces the whole table "bridge mcpanel_web" in one batch: table, chain
// (filter, hook forward, priority -10, policy accept), flushed, then per rule (sorted)
// an accept for Dest and each of Ports and a drop for everything else from Source. An empty list
// deletes the table if it exists.
func (n *NFTables) SetWebRules(_ context.Context, rules []agentapi.WebRule) error {
	sorted, err := sortedWebRules(rules)
	if err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	c, err := nftables.New(n.opts...)
	if err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	table := webTable()
	if len(sorted) == 0 {
		exists, err := webTableExists(c)
		if err != nil || !exists {
			return err
		}
		c.DelTable(table)
		if err := c.Flush(); err != nil {
			return fmt.Errorf("nftables: delete %s: %w", WebTableName, err)
		}
		return nil
	}
	c.AddTable(table)
	chain := c.AddChain(webChain(table))
	c.FlushChain(chain)
	for _, exprs := range webRuleExprs(sorted) {
		c.AddRule(&nftables.Rule{Table: table, Chain: chain, Exprs: exprs})
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("nftables: web rules: %w", err)
	}
	return nil
}

// WebRulesInPlace compares the installed chain (definition, rule count and expressions)
// with what SetWebRules writes for rules. For an empty list the table must be absent.
func (n *NFTables) WebRulesInPlace(_ context.Context, rules []agentapi.WebRule) (bool, error) {
	sorted, err := sortedWebRules(rules)
	if err != nil {
		return false, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	c, err := nftables.New(n.opts...)
	if err != nil {
		return false, fmt.Errorf("nftables: %w", err)
	}
	exists, err := webTableExists(c)
	if err != nil {
		return false, err
	}
	if len(sorted) == 0 || !exists {
		return len(sorted) == 0 && !exists, nil
	}
	table := webTable()
	// An error means the chain is missing.
	have, err := c.ListChain(table, WebChainName)
	if err != nil || !sameChain(have, webChain(table)) {
		return false, nil
	}
	installed, err := c.GetRules(table, have)
	if err != nil {
		return false, fmt.Errorf("nftables: list %s: %w", WebTableName, err)
	}
	want := webRuleExprs(sorted)
	if len(installed) != len(want) {
		return false, nil
	}
	for i, r := range installed {
		if !reflect.DeepEqual(r.Exprs, want[i]) {
			return false, nil
		}
	}
	return true, nil
}

func webTableExists(c *nftables.Conn) (bool, error) {
	tables, err := c.ListTablesOfFamily(nftables.TableFamilyBridge)
	if err != nil {
		return false, fmt.Errorf("nftables: list bridge tables: %w", err)
	}
	return slices.ContainsFunc(tables, func(t *nftables.Table) bool { return t.Name == WebTableName }), nil
}

func sameChain(have, want *nftables.Chain) bool {
	return have.Type == want.Type &&
		have.Hooknum != nil && *have.Hooknum == *want.Hooknum &&
		have.Priority != nil && *have.Priority == *want.Priority &&
		have.Policy != nil && *have.Policy == *want.Policy
}

// webRuleExprs returns the chain's rules for sorted rules: per rule an accept for each of
// its Ports (ascending), then its drop.
func webRuleExprs(sorted []agentapi.WebRule) [][]expr.Any {
	var out [][]expr.Any
	for _, r := range sorted {
		for _, p := range r.Ports {
			out = append(out, webAcceptExprs(r, p))
		}
		out = append(out, webDropExprs(r))
	}
	return out
}

// webSourceExprs builds "meta protocol ip ip saddr <Source>".
func webSourceExprs(r agentapi.WebRule) []expr.Any {
	src := r.Source.As4()
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyPROTOCOL, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binary.BigEndian.AppendUint16(nil, unix.ETH_P_IP)},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: src[:]},
	}
}

// webAcceptExprs builds "meta protocol ip ip saddr <Source> ip daddr <Dest> tcp dport
// <port> accept" for one of the rule's Ports (Dest masked by sortedWebRules).
func webAcceptExprs(r agentapi.WebRule, port uint16) []expr.Any {
	dst := r.Dest.Addr().As4()
	return append(webSourceExprs(r),
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: net.CIDRMask(r.Dest.Bits(), 32), Xor: []byte{0, 0, 0, 0}},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: dst[:]},
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binary.BigEndian.AppendUint16(nil, port)},
		&expr.Verdict{Kind: expr.VerdictAccept},
	)
}

// webDropExprs builds "meta protocol ip ip saddr <Source> drop".
func webDropExprs(r agentapi.WebRule) []expr.Any {
	return append(webSourceExprs(r), &expr.Verdict{Kind: expr.VerdictDrop})
}
