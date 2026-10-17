//go:build linux

package firewall

import (
	"reflect"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// Pure rule building (no netlink): runs on every Linux CI runner without root.

func TestWebChainDefinition(t *testing.T) {
	ch := webChain(webTable())
	if ch.Table.Family != nftables.TableFamilyBridge || ch.Table.Name != WebTableName || WebTableName != "mcpanel_web" {
		t.Fatalf("table = %+v", ch.Table)
	}
	if ch.Name != WebChainName || ch.Type != nftables.ChainTypeFilter || *ch.Hooknum != *nftables.ChainHookForward ||
		*ch.Priority != nftables.ChainPriority(WebChainPriority) || WebChainPriority != -10 ||
		*ch.Policy != nftables.ChainPolicyAccept {
		t.Fatalf("chain = %+v", ch)
	}
}

// "meta protocol ip ip saddr 172.30.1.2 ip daddr 172.30.1.0/24 tcp dport 8100 accept",
// then "meta protocol ip ip saddr 172.30.1.2 drop".
func TestWebExprs(t *testing.T) {
	r := webRule("172.30.1.2", "172.30.0.0/23", 8100)
	ipv4 := []expr.Any{
		&expr.Meta{Key: expr.MetaKeyPROTOCOL, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{0x08, 0x00}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 12, Len: 4},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{172, 30, 1, 2}},
	}
	accept := append(append([]expr.Any{}, ipv4...),
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: 16, Len: 4},
		&expr.Bitwise{SourceRegister: 1, DestRegister: 1, Len: 4, Mask: []byte{255, 255, 254, 0}, Xor: []byte{0, 0, 0, 0}},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{172, 30, 0, 0}},
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{0x1f, 0xa4}},
		&expr.Verdict{Kind: expr.VerdictAccept},
	)
	drop := append(append([]expr.Any{}, ipv4...), &expr.Verdict{Kind: expr.VerdictDrop})
	if got := webAcceptExprs(r); !reflect.DeepEqual(got, accept) {
		t.Errorf("accept = %#v", got)
	}
	if got := webDropExprs(r); !reflect.DeepEqual(got, drop) {
		t.Errorf("drop = %#v", got)
	}
}

// Every rule contributes its accept, then its drop, in sorted order.
func TestWebRuleExprsOrder(t *testing.T) {
	a := webRule("172.30.1.2", "172.30.1.0/24", 8100)
	b := webRule("172.30.2.2", "172.30.2.0/24", 8123)
	got := webRuleExprs([]agentapi.WebRule{a, b})
	want := [][]expr.Any{webAcceptExprs(a), webDropExprs(a), webAcceptExprs(b), webDropExprs(b)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rules = %#v", got)
	}
}
