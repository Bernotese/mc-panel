package firewall

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// ErrInvalidWebRule marks a web rule no restriction can be built for.
var ErrInvalidWebRule = errors.New("invalid web rule")

// minWebPort is the lowest port mcpanel-web may be allowed to reach (agentapi.ValidWebPort).
const minWebPort = 1024

// sortedWebRules checks rules and returns them with masked Dest, sorted by (Source, Dest,
// Port), so equal input gives equal rule sets. Source must be an IPv4 address inside the
// IPv4 Dest, Port at least 1024, and every Source unique: the drop rule of a source would
// shadow a second accept for it. The input is not modified.
func sortedWebRules(rules []agentapi.WebRule) ([]agentapi.WebRule, error) {
	out := make([]agentapi.WebRule, 0, len(rules))
	for i, r := range rules {
		switch {
		case !r.Source.Is4():
			return nil, fmt.Errorf("%w: rules[%d]: source %s is not an IPv4 address", ErrInvalidWebRule, i, r.Source)
		case !r.Dest.IsValid() || !r.Dest.Addr().Is4():
			return nil, fmt.Errorf("%w: rules[%d]: dest %s is not an IPv4 prefix", ErrInvalidWebRule, i, r.Dest)
		case !r.Dest.Contains(r.Source):
			return nil, fmt.Errorf("%w: rules[%d]: source %s is not inside %s", ErrInvalidWebRule, i, r.Source, r.Dest)
		case r.Port < minWebPort:
			return nil, fmt.Errorf("%w: rules[%d]: port %d is below %d", ErrInvalidWebRule, i, r.Port, minWebPort)
		}
		r.Dest = r.Dest.Masked()
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b agentapi.WebRule) int {
		return cmp.Or(
			a.Source.Compare(b.Source),
			a.Dest.Addr().Compare(b.Dest.Addr()),
			cmp.Compare(a.Dest.Bits(), b.Dest.Bits()),
			cmp.Compare(a.Port, b.Port),
		)
	})
	for i := 1; i < len(out); i++ {
		if out[i].Source == out[i-1].Source {
			return nil, fmt.Errorf("%w: source %s appears twice", ErrInvalidWebRule, out[i].Source)
		}
	}
	return out, nil
}
