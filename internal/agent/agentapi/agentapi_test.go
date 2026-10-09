package agentapi_test

import (
	"net/netip"
	"testing"

	"mcpanel/internal/agent/agentapi"
)

func TestValidateBanPrefix(t *testing.T) {
	ok := []string{"203.0.113.7/32", "2001:db8:1:2::/64", "2001:db8::1/128"}
	bad := []string{"0.0.0.0/0", "10.0.0.0/8", "203.0.113.0/24", "2001:db8::/48", "::/0", "127.0.0.1/32", "::1/128"}
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
