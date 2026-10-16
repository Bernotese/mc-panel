package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// objectNameRe is what the agent accepts as a network or container name in a network
// call (Docker's own rule for names, without the leading "/").
var objectNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func checkObjectName(kind, name string) error {
	if !objectNameRe.MatchString(name) {
		return fmt.Errorf("docker: invalid %s name %q", kind, name)
	}
	return nil
}

// Network is a network as GET /networks reports it.
type Network struct {
	ID         string
	Name       string
	Driver     string
	Labels     map[string]string
	EnableIPv6 bool
	Subnets    []netip.Prefix // IPAM config, masked
	// BridgeName is the host interface of a bridge that sets it explicitly (option
	// com.docker.network.bridge.name, e.g. docker0); otherwise Docker uses "br-<ID[:12]>".
	BridgeName string
	// Containers are the names of the running containers on it; only InspectNetwork
	// fills it (Docker lists no endpoint for a created or stopped container).
	Containers []string
}

// HasIPv6 reports whether the network has IPv6 enabled or an IPv6 subnet.
func (n Network) HasIPv6() bool {
	return n.EnableIPv6 || slices.ContainsFunc(n.Subnets, func(p netip.Prefix) bool { return p.Addr().Is6() })
}

// HostInterface is the name of the network's bridge interface on the host ("" for
// non-bridge networks or an unknown ID).
func (n Network) HostInterface() string {
	switch {
	case n.Driver != "bridge":
		return ""
	case n.BridgeName != "":
		return n.BridgeName
	case len(n.ID) >= 12:
		return "br-" + n.ID[:12]
	}
	return ""
}

type networkIPAM struct {
	Config []networkIPAMConfig `json:"Config"`
}

type networkIPAMConfig struct {
	Subnet string `json:"Subnet"`
}

// networkCreateIPAM always sets both IP families explicitly: IPv4 only, whatever the
// daemon's defaults (daemon.json "ipv6" or default-network-opts) say.
type networkCreateIPAM struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Labels     map[string]string `json:"Labels,omitempty"`
	EnableIPv4 bool              `json:"EnableIPv4"`
	EnableIPv6 bool              `json:"EnableIPv6"`
	IPAM       networkIPAM       `json:"IPAM"`
}

type networkListEntry struct {
	ID         string            `json:"Id"`
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Labels     map[string]string `json:"Labels"`
	EnableIPv6 bool              `json:"EnableIPv6"`
	Options    map[string]string `json:"Options"`
	IPAM       networkIPAM       `json:"IPAM"`
	// Containers is filled only by GET /networks/{name}, keyed by container ID.
	Containers map[string]struct {
		Name string `json:"Name"`
	} `json:"Containers"`
}

func (e networkListEntry) network() (Network, error) {
	n := Network{ID: e.ID, Name: e.Name, Driver: e.Driver, Labels: e.Labels, EnableIPv6: e.EnableIPv6,
		BridgeName: e.Options["com.docker.network.bridge.name"]}
	for _, ct := range e.Containers {
		n.Containers = append(n.Containers, strings.TrimPrefix(ct.Name, "/"))
	}
	slices.Sort(n.Containers)
	for _, cfg := range e.IPAM.Config {
		p, err := netip.ParsePrefix(cfg.Subnet)
		if err != nil {
			return Network{}, fmt.Errorf("docker: network %s has an invalid subnet %q", e.Name, cfg.Subnet)
		}
		n.Subnets = append(n.Subnets, p.Masked())
	}
	return n, nil
}

// CreateNetwork creates the bridge network name with exactly one IPv4 subnet and no IPv6,
// and returns its ID. A subnet that overlaps an existing network fails with an error for
// which IsPoolOverlap reports true; an existing name fails with IsConflict.
func (c *Client) CreateNetwork(ctx context.Context, name string, labels map[string]string, subnet netip.Prefix) (string, error) {
	if err := checkObjectName("network", name); err != nil {
		return "", err
	}
	if !subnet.IsValid() || !subnet.Addr().Is4() || subnet != subnet.Masked() {
		return "", fmt.Errorf("docker: network %s: subnet %s is not a canonical IPv4 prefix", name, subnet)
	}
	var out struct {
		ID string `json:"Id"`
	}
	err := c.call(ctx, c.timeout, http.MethodPost, "/networks/create", nil, networkCreateIPAM{
		Name: name, Driver: "bridge", Labels: labels, EnableIPv4: true, EnableIPv6: false,
		IPAM: networkIPAM{Config: []networkIPAMConfig{{Subnet: subnet.String()}}},
	}, &out)
	return out.ID, err
}

// IsPoolOverlap reports Docker's refusal of a subnet that overlaps another network.
func IsPoolOverlap(err error) bool {
	return statusIs(err, http.StatusForbidden) && strings.Contains(err.Error(), "overlaps")
}

// ListNetworks returns every network carrying the label key ("" = all networks).
func (c *Client) ListNetworks(ctx context.Context, label string) ([]Network, error) {
	q := url.Values{}
	if label != "" {
		filters, err := json.Marshal(map[string][]string{"label": {label}})
		if err != nil {
			return nil, err
		}
		q.Set("filters", string(filters))
	}
	var entries []networkListEntry
	if err := c.call(ctx, c.timeout, http.MethodGet, "/networks", q, nil, &entries); err != nil {
		return nil, err
	}
	out := make([]Network, 0, len(entries))
	for _, e := range entries {
		n, err := e.network()
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// InspectNetwork returns the network name with its running containers. A missing network
// is an error for which IsNotFound reports true.
func (c *Client) InspectNetwork(ctx context.Context, name string) (Network, error) {
	if err := checkObjectName("network", name); err != nil {
		return Network{}, err
	}
	var e networkListEntry
	if err := c.call(ctx, c.timeout, http.MethodGet, "/networks/"+url.PathEscape(name), nil, nil, &e); err != nil {
		return Network{}, err
	}
	return e.network()
}

type networkMember struct {
	Container string `json:"Container"`
	Force     bool   `json:"Force,omitempty"`
}

// NetworkConnect connects container to network; an existing connection is not an error.
func (c *Client) NetworkConnect(ctx context.Context, network, container string) error {
	if err := checkObjectName("network", network); err != nil {
		return err
	}
	if err := checkObjectName("container", container); err != nil {
		return err
	}
	err := c.call(ctx, c.timeout, http.MethodPost, "/networks/"+url.PathEscape(network)+"/connect", nil,
		networkMember{Container: container}, nil)
	if statusIs(err, http.StatusForbidden) && strings.Contains(err.Error(), "already exists in network") {
		return nil
	}
	return err
}

// NetworkDisconnect disconnects container from network; a container that is not
// connected, or a missing container or network, is not an error.
func (c *Client) NetworkDisconnect(ctx context.Context, network, container string) error {
	if err := checkObjectName("network", network); err != nil {
		return err
	}
	if err := checkObjectName("container", container); err != nil {
		return err
	}
	err := c.call(ctx, c.timeout, http.MethodPost, "/networks/"+url.PathEscape(network)+"/disconnect", nil,
		networkMember{Container: container}, nil)
	var de *Error
	if IsNotFound(err) || (errors.As(err, &de) && strings.Contains(de.Message, "is not connected to")) {
		return nil
	}
	return err
}

// RemoveNetwork deletes the network name; a missing network is not an error. Docker
// refuses a network with running containers (HTTP 403).
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	if err := checkObjectName("network", name); err != nil {
		return err
	}
	err := c.call(ctx, c.timeout, http.MethodDelete, "/networks/"+url.PathEscape(name), nil, nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
