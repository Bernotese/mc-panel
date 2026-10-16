package agentapi_test

import (
	"net/netip"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func TestValidateBanPrefix(t *testing.T) {
	ok := []string{"203.0.113.7/32", "2001:db8:1:2::/64", "2001:db8::1/128"}
	bad := []string{
		"0.0.0.0/0", "10.0.0.0/8", "203.0.113.0/24", "2001:db8::/48", "::/0", "127.0.0.1/32", "::1/128",
		// IPv4-mapped IPv6 must not bypass the IPv4 rules.
		"::ffff:10.0.0.0/104", "::ffff:0:0/96",
		// Covers loopback/unspecified even though the stored address is not.
		"::2/64",
		// Unmasked prefix (host bits set).
		"2001:db8::1/64",
		// Link-local and multicast.
		"fe80::/64", "ff02::1/128", "224.0.0.1/32",
	}
	for _, s := range ok {
		if err := agentapi.ValidateBanPrefix(netip.MustParsePrefix(s)); err != nil {
			t.Errorf("%s: unexpected error %v", s, err)
		}
	}
	for _, s := range bad {
		if err := agentapi.ValidateBanPrefix(netip.MustParsePrefix(s)); err == nil {
			t.Errorf("%s: expected error", s)
		}
	}
}
