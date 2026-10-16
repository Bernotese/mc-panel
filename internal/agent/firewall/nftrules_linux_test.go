//go:build linux

package firewall

import (
	"bytes"
	"net/netip"
	"testing"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"
)

// Pure rule building (no netlink): runs on every Linux CI runner without root.

func TestSetDefinitionsHaveSizeLimit(t *testing.T) {
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: TableName}
	s4, s6 := newSets(table)
	if s4.Size != SetSize || s6.Size != SetSize || SetSize != 65536 {
		t.Fatalf("sizes = %d/%d, want %d", s4.Size, s6.Size, SetSize)
	}
	if s4.Name != Set4Name || !s4.HasTimeout || s4.Interval || s4.KeyType.Name != nftables.TypeIPAddr.Name {
		t.Fatalf("set4 = %+v", s4)
	}
	if s6.Name != Set6Name || !s6.HasTimeout || !s6.Interval || s6.KeyType.Name != nftables.TypeIP6Addr.Name {
		t.Fatalf("set6 = %+v", s6)
	}
}

// The drop rule must only match new connections ("ct state new"), so replies to the host's
// own outbound connections to a jailed address are not cut.
func TestDropExprsMatchOnlyNewConnections(t *testing.T) {
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: TableName}
	s4, s6 := newSets(table)
	for _, tc := range []struct {
		proto          byte
		offset, length uint32
		set            *nftables.Set
	}{
		{unix.NFPROTO_IPV4, 12, 4, s4},
		{unix.NFPROTO_IPV6, 8, 16, s6},
	} {
		ex := dropExprs(tc.proto, tc.offset, tc.length, tc.set)
		var ct, newMask, nonZero, lookup, drop bool
		for i, e := range ex {
			switch e := e.(type) {
			case *expr.Ct:
				ct = e.Key == expr.CtKeySTATE && !e.SourceRegister
			case *expr.Bitwise:
				newMask = bytes.Equal(e.Mask, binaryutil.NativeEndian.PutUint32(expr.CtStateBitNEW)) &&
					bytes.Equal(e.Xor, []byte{0, 0, 0, 0}) && e.Len == 4
			case *expr.Cmp:
				if e.Op == expr.CmpOpNeq && bytes.Equal(e.Data, []byte{0, 0, 0, 0}) {
					nonZero = true
				}
			case *expr.Lookup:
				lookup = e.SetName == tc.set.Name
			case *expr.Verdict:
				drop = e.Kind == expr.VerdictDrop && i == len(ex)-1
			}
		}
		if !ct || !newMask || !nonZero || !lookup || !drop {
			t.Fatalf("set %s: ct=%v new=%v nonzero=%v lookup=%v drop=%v: %#v", tc.set.Name, ct, newMask, nonZero, lookup, drop, ex)
		}
	}
}

func TestEgressSetDefinitions(t *testing.T) {
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: TableName}
	s4, s6 := newEgressSets(table)
	if s4.Name != EgressSet4Name || !s4.Interval || s4.HasTimeout || s4.KeyType.Name != nftables.TypeIPAddr.Name {
		t.Fatalf("egress set4 = %+v", s4)
	}
	if s6.Name != EgressSet6Name || !s6.Interval || s6.HasTimeout || s6.KeyType.Name != nftables.TypeIP6Addr.Name {
		t.Fatalf("egress set6 = %+v", s6)
	}
}

// The egress rule: source in the container subnet, destination outside it, new
// connection, destination in the deny set -> drop.
func TestEgressExprs(t *testing.T) {
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: TableName}
	s4, s6 := newEgressSets(table)
	for _, tc := range []struct {
		subnet        string
		set           *nftables.Set
		proto         byte
		saddr, daddr  uint32
		length        uint32
		mask, network []byte
	}{
		{"172.18.0.0/16", s4, unix.NFPROTO_IPV4, 12, 16, 4, []byte{255, 255, 0, 0}, []byte{172, 18, 0, 0}},
		{"fd00:1:2::/48", s6, unix.NFPROTO_IPV6, 8, 24, 16,
			[]byte{255, 255, 255, 255, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, netip.MustParseAddr("fd00:1:2::").AsSlice()},
	} {
		ex := egressExprs(netip.MustParsePrefix(tc.subnet), tc.set)
		var proto, srcIn, dstOut, newOnly, lookup, drop bool
		var lastOffset uint32
		var lastMask []byte
		for i, e := range ex {
			switch e := e.(type) {
			case *expr.Cmp:
				switch {
				case bytes.Equal(e.Data, []byte{tc.proto}) && e.Op == expr.CmpOpEq:
					proto = true
				case bytes.Equal(e.Data, tc.network) && bytes.Equal(lastMask, tc.mask):
					if e.Op == expr.CmpOpEq && lastOffset == tc.saddr {
						srcIn = true
					}
					if e.Op == expr.CmpOpNeq && lastOffset == tc.daddr {
						dstOut = true
					}
				case e.Op == expr.CmpOpNeq && bytes.Equal(e.Data, []byte{0, 0, 0, 0}):
					newOnly = true
				}
			case *expr.Payload:
				if e.Len != tc.length {
					t.Fatalf("%s: payload len %d", tc.subnet, e.Len)
				}
				lastOffset, lastMask = e.Offset, nil
			case *expr.Bitwise:
				if e.Len == tc.length {
					lastMask = e.Mask
				} else if !bytes.Equal(e.Mask, binaryutil.NativeEndian.PutUint32(expr.CtStateBitNEW)) {
					t.Fatalf("%s: ct mask %v", tc.subnet, e.Mask)
				}
			case *expr.Lookup:
				lookup = e.SetName == tc.set.Name && lastOffset == tc.daddr && !e.Invert
			case *expr.Verdict:
				drop = e.Kind == expr.VerdictDrop && i == len(ex)-1
			}
		}
		if !proto || !srcIn || !dstOut || !newOnly || !lookup || !drop {
			t.Fatalf("%s: proto=%v srcIn=%v dstOut=%v new=%v lookup=%v drop=%v: %#v", tc.subnet, proto, srcIn, dstOut, newOnly, lookup, drop, ex)
		}
	}
}
