package agentapi

import (
	"context"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	PathWebRoutes = "/v1/web/routes"
	// MaxWebRoutes bounds one request (web ports spec §4.1).
	MaxWebRoutes = 500
	// MaxWebTargets bounds the targets of one route (web port paths spec §2).
	MaxWebTargets = 8
	// MaxWebRequestBytes is the body limit of PUT /v1/web/routes (500 routes × 8 targets).
	MaxWebRequestBytes = 1 << 20

	minWebLabels = 3
)

// WebTarget publishes Port of the container under https://<fqdn>/ (Path "") or
// https://<fqdn>/<Path>/, the prefix stripped (web port paths spec §1.1).
type WebTarget struct {
	Path string `json:"path"`
	Port int    `json:"port"`
}

// WebRoute publishes the targets of the container of server ID under https://<FQDN>.
type WebRoute struct {
	ID      int64       `json:"id"`
	FQDN    string      `json:"fqdn"`
	Targets []WebTarget `json:"targets"` // sorted by Path, root ("") first
}

// Equal reports whether r and o publish the same targets under the same name.
func (r WebRoute) Equal(o WebRoute) bool {
	return r.ID == o.ID && r.FQDN == o.FQDN && slices.Equal(r.Targets, o.Targets)
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

var webPathRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)

// ValidWebPath is the path rule of the web port paths spec §2: "" (the root) or one
// segment of 1–32 lowercase letters, digits and dashes, starting and ending with no dash.
func ValidWebPath(p string) bool { return p == "" || webPathRE.MatchString(p) }

// CheckWebTargets checks the targets of one route (web port paths spec §2): 1 to
// MaxWebTargets, valid paths and ports, strictly ascending by path (so unique, the root
// first) and unique ports. Every error wraps instancespec.ErrInvalid.
func CheckWebTargets(ts []WebTarget) error {
	if len(ts) == 0 || len(ts) > MaxWebTargets {
		return fmt.Errorf("%w: targets: 1 to %d required", instancespec.ErrInvalid, MaxWebTargets)
	}
	ports := make(map[int]struct{}, len(ts))
	for i, t := range ts {
		if !ValidWebPath(t.Path) {
			return fmt.Errorf("%w: targets[%d].path: must be empty or 1..32 of a-z, 0-9 and -, not starting or ending with -", instancespec.ErrInvalid, i)
		}
		if i > 0 && ts[i-1].Path >= t.Path {
			return fmt.Errorf("%w: targets[%d].path: duplicate or not sorted", instancespec.ErrInvalid, i)
		}
		if !ValidWebPort(t.Port) {
			return fmt.Errorf("%w: targets[%d].port: must be 1024..65535 and not 25565 or 25575", instancespec.ErrInvalid, i)
		}
		if _, dup := ports[t.Port]; dup {
			return fmt.Errorf("%w: targets[%d].port: duplicate %d", instancespec.ErrInvalid, i, t.Port)
		}
		ports[t.Port] = struct{}{}
	}
	return nil
}

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
		if err := CheckWebTargets(rt.Targets); err != nil {
			return fmt.Errorf("routes[%d]: %w", i, err)
		}
	}
	return nil
}

// WebRule is one allow rule of the bridge port restriction (not on the wire; shared by
// internal/agent/instances and internal/agent/firewall): mcpanel-web at Source may open TCP
// connections to Ports on the server network Dest; everything else from Source is dropped.
type WebRule struct {
	Source netip.Addr   // fixed address of mcpanel-web on the server network
	Dest   netip.Prefix // the server network's subnet
	Ports  []uint16     // the server's published ports
}

// Equal reports whether r and o are the same rule (ports in the same order).
func (r WebRule) Equal(o WebRule) bool {
	return r.Source == o.Source && r.Dest == o.Dest && slices.Equal(r.Ports, o.Ports)
}
