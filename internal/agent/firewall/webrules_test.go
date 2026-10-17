package firewall

import (
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func webRule(src, dest string, port uint16) agentapi.WebRule {
	return agentapi.WebRule{Source: netip.MustParseAddr(src), Dest: netip.MustParsePrefix(dest), Port: port}
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
	if !slices.Equal(got, want) {
		t.Fatalf("sorted = %+v, want %+v", got, want)
	}
	if in[0].Source != netip.MustParseAddr("172.30.2.2") {
		t.Fatal("input was modified")
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
		"zero source":        {Dest: netip.MustParsePrefix("172.30.1.0/24"), Port: 8100},
		"ipv6 source":        webRule("fd00::2", "fd00::/64", 8100),
		"ipv4-mapped source": webRule("::ffff:172.30.1.2", "172.30.1.0/24", 8100),
		"zero dest":          {Source: netip.MustParseAddr("172.30.1.2"), Port: 8100},
		"ipv6 dest":          webRule("172.30.1.2", "fd00::/64", 8100),
		"source outside":     webRule("172.30.2.2", "172.30.1.0/24", 8100),
		"port 0":             webRule("172.30.1.2", "172.30.1.0/24", 0),
		"port 1023":          webRule("172.30.1.2", "172.30.1.0/24", 1023),
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
