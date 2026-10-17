package agentapi

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	PathWebRoutes = "/v1/web/routes"
	// MaxWebRoutes bounds one request (web ports spec §4.1).
	MaxWebRoutes = 500
	// MaxWebRequestBytes is the body limit of PUT /v1/web/routes (500 routes × ~300 bytes).
	MaxWebRequestBytes = 256 << 10

	minWebLabels = 3
)

// WebRoute publishes port of the container of server ID under https://<FQDN>.
type WebRoute struct {
	ID   int64  `json:"id"`
	FQDN string `json:"fqdn"`
	Port int    `json:"port"`
}

// WebRoutesRequest is the complete desired route list: a server missing from it has no route.
type WebRoutesRequest struct {
	Routes []WebRoute `json:"routes"` // never null on the wire; nil is rejected
}

type WebRouteState string

const (
	WebPending WebRouteState = "pending"
	WebActive  WebRouteState = "active"
	WebError   WebRouteState = "error"
)

type WebRouteStatus struct {
	ID     int64         `json:"id"`
	Status WebRouteState `json:"status"`
	Error  string        `json:"error,omitempty"` // sanitized, like last_error
}

type WebRoutesResponse struct {
	Routes []WebRouteStatus `json:"routes"` // one per requested id, in request order
}

// WebClient is the panel-side view of the web router endpoint.
type WebClient interface {
	WebRoutes(ctx context.Context, req WebRoutesRequest) (WebRoutesResponse, error)
}

// ValidWebPort is the port rule of the web ports spec §1.1 (shared by panel and agent).
func ValidWebPort(p int) bool { return p >= 1024 && p <= 65535 && p != 25565 && p != 25575 }

// Validate checks the list like EnsureRequest.Validate; every error wraps instancespec.ErrInvalid.
func (r WebRoutesRequest) Validate() error {
	if r.Routes == nil {
		return fmt.Errorf("%w: routes: required", instancespec.ErrInvalid)
	}
	if len(r.Routes) > MaxWebRoutes {
		return fmt.Errorf("%w: routes: at most %d", instancespec.ErrInvalid, MaxWebRoutes)
	}
	ids := make(map[int64]struct{}, len(r.Routes))
	fqdns := make(map[string]struct{}, len(r.Routes))
	for i, rt := range r.Routes {
		if rt.ID <= 0 {
			return fmt.Errorf("%w: routes[%d].id: must be > 0", instancespec.ErrInvalid, i)
		}
		if _, dup := ids[rt.ID]; dup {
			return fmt.Errorf("%w: routes[%d].id: duplicate %d", instancespec.ErrInvalid, i, rt.ID)
		}
		ids[rt.ID] = struct{}{}
		if err := instancespec.CheckDNSName(rt.FQDN); err != nil {
			return fmt.Errorf("routes[%d]: %w", i, err)
		}
		if strings.Count(rt.FQDN, ".")+1 < minWebLabels {
			return fmt.Errorf("%w: routes[%d].fqdn: needs at least %d labels", instancespec.ErrInvalid, i, minWebLabels)
		}
		if _, dup := fqdns[rt.FQDN]; dup {
			return fmt.Errorf("%w: routes[%d].fqdn: duplicate %q", instancespec.ErrInvalid, i, rt.FQDN)
		}
		fqdns[rt.FQDN] = struct{}{}
		if !ValidWebPort(rt.Port) {
			return fmt.Errorf("%w: routes[%d].port: must be 1024..65535 and not 25565 or 25575", instancespec.ErrInvalid, i)
		}
	}
	return nil
}

// WebRule is one allow rule of the bridge port restriction (not on the wire; shared by
// internal/agent/instances and internal/agent/firewall): mcpanel-web at Source may open TCP
// connections to Port on the server network Dest; everything else from Source is dropped.
type WebRule struct {
	Source netip.Addr   // fixed address of mcpanel-web on the server network
	Dest   netip.Prefix // the server network's subnet
	Port   uint16
}
