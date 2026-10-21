package firewall

import (
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func webRule(src, dest string, port uint16) agentapi.WebRule {
	return agentapi.WebRule{Source: netip.MustParseAddr(src), Dest: netip.MustParsePrefix(dest), Ports: []uint16{port}}
}

func TestSortedWebRulesSortsBySourceDestPort(t *testing.T) {
	in := []agentapi.WebRule{
		webRule("172.30.2.2", "172.30.2.0/24", 8100),
		webRule("172.30.1.2", "172.30.1.0/24", 8123),
		webRule("10.0.0.2", "10.0.0.0/16", 1024),
	}
	got, err := sortedWebRules(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []agentapi.WebRule{in[2], in[1], in[0]}
	if !slices.EqualFunc(got, want, agentapi.WebRule.Equal) {
		t.Fatalf("sorted = %+v, want %+v", got, want)
	}
	if in[0].Source != netip.MustParseAddr("172.30.2.2") {
		t.Fatal("input was modified")
	}
}

func TestSortedWebRulesSortsPorts(t *testing.T) {
	in := []agentapi.WebRule{{Source: netip.MustParseAddr("172.30.1.2"), Dest: netip.MustParsePrefix("172.30.1.0/24"), Ports: []uint16{8200, 8100}}}
	got, err := sortedWebRules(in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[0].Ports, []uint16{8100, 8200}) {
		t.Fatalf("ports = %v, want [8100 8200]", got[0].Ports)
	}
	if !slices.Equal(in[0].Ports, []uint16{8200, 8100}) {
		t.Fatalf("the caller's ports were modified: %v", in[0].Ports)
	}
	// Eight ports are the most a rule may have.
	in[0].Ports = []uint16{8107, 8106, 8105, 8104, 8103, 8102, 8101, 8100}
	if got, err := sortedWebRules(in); err != nil || got[0].Ports[0] != 8100 || got[0].Ports[7] != 8107 {
		t.Fatalf("eight ports = %v, %v", got, err)
	}
}

func TestSortedWebRulesMasksDest(t *testing.T) {
	got, err := sortedWebRules([]agentapi.WebRule{webRule("172.30.1.2", "172.30.1.9/24", 8100)})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Dest != netip.MustParsePrefix("172.30.1.0/24") {
		t.Fatalf("dest = %s", got[0].Dest)
	}
}

func TestSortedWebRulesRejectsInvalid(t *testing.T) {
	for name, r := range map[string]agentapi.WebRule{
		"zero source":        {Dest: netip.MustParsePrefix("172.30.1.0/24"), Ports: []uint16{8100}},
		"ipv6 source":        webRule("fd00::2", "fd00::/64", 8100),
		"ipv4-mapped source": webRule("::ffff:172.30.1.2", "172.30.1.0/24", 8100),
		"zero dest":          {Source: netip.MustParseAddr("172.30.1.2"), Ports: []uint16{8100}},
		"ipv6 dest":          webRule("172.30.1.2", "fd00::/64", 8100),
		"source outside":     webRule("172.30.2.2", "172.30.1.0/24", 8100),
		"port 0":             webRule("172.30.1.2", "172.30.1.0/24", 0),
		"port 1023":          webRule("172.30.1.2", "172.30.1.0/24", 1023),
		"no ports":           {Source: netip.MustParseAddr("172.30.1.2"), Dest: netip.MustParsePrefix("172.30.1.0/24")},
		"empty ports":        {Source: netip.MustParseAddr("172.30.1.2"), Dest: netip.MustParsePrefix("172.30.1.0/24"), Ports: []uint16{}},
		"9 ports": {Source: netip.MustParseAddr("172.30.1.2"), Dest: netip.MustParsePrefix("172.30.1.0/24"),
			Ports: []uint16{8100, 8101, 8102, 8103, 8104, 8105, 8106, 8107, 8108}},
		"second port 1023": {Source: netip.MustParseAddr("172.30.1.2"), Dest: netip.MustParsePrefix("172.30.1.0/24"), Ports: []uint16{8100, 1023}},
		"duplicate port":   {Source: netip.MustParseAddr("172.30.1.2"), Dest: netip.MustParsePrefix("172.30.1.0/24"), Ports: []uint16{8200, 8100, 8200}},
	} {
		if _, err := sortedWebRules([]agentapi.WebRule{r}); !errors.Is(err, ErrInvalidWebRule) {
			t.Errorf("%s: err = %v, want ErrInvalidWebRule", name, err)
		}
	}
}

// One source per server network: a second rule for the same source would be shadowed
// by the first rule's drop.
func TestSortedWebRulesRejectsDuplicateSource(t *testing.T) {
	_, err := sortedWebRules([]agentapi.WebRule{
		webRule("172.30.1.2", "172.30.1.0/24", 8100),
		webRule("172.30.1.2", "172.30.1.0/24", 8200),
	})
	if !errors.Is(err, ErrInvalidWebRule) {
		t.Fatalf("err = %v, want ErrInvalidWebRule", err)
	}
}

func TestSortedWebRulesEmpty(t *testing.T) {
	for _, in := range [][]agentapi.WebRule{nil, {}} {
		got, err := sortedWebRules(in)
		if err != nil || len(got) != 0 {
			t.Fatalf("sortedWebRules(%v) = %v, %v", in, got, err)
		}
	}
}
