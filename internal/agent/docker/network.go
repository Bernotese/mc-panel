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

// Network is a bridge network as GET /networks reports it.
type Network struct {
	Name    string
	Driver  string
	Labels  map[string]string
	Subnets []netip.Prefix // IPAM config, masked
}

type networkIPAM struct {
	Config []networkIPAMConfig `json:"Config"`
}

type networkIPAMConfig struct {
	Subnet string `json:"Subnet"`
}

type networkCreateIPAM struct {
	Name   string            `json:"Name"`
	Driver string            `json:"Driver"`
	Labels map[string]string `json:"Labels,omitempty"`
	IPAM   networkIPAM       `json:"IPAM"`
}

type networkListEntry struct {
	Name   string            `json:"Name"`
	Driver string            `json:"Driver"`
	Labels map[string]string `json:"Labels"`
	IPAM   networkIPAM       `json:"IPAM"`
	// Containers is filled only by GET /networks/{name}, keyed by container ID.
	Containers map[string]struct {
		Name string `json:"Name"`
	} `json:"Containers"`
}

func (e networkListEntry) network() (Network, error) {
	n := Network{Name: e.Name, Driver: e.Driver, Labels: e.Labels}
	for _, cfg := range e.IPAM.Config {
		p, err := netip.ParsePrefix(cfg.Subnet)
		if err != nil {
			return Network{}, fmt.Errorf("docker: network %s has an invalid subnet %q", e.Name, cfg.Subnet)
		}
		n.Subnets = append(n.Subnets, p.Masked())
	}
	return n, nil
}

// CreateNetwork creates the bridge network name with exactly one IPv4 subnet. A
// subnet that overlaps an existing network fails with an error for which
// IsPoolOverlap reports true; an existing name fails with IsConflict.
func (c *Client) CreateNetwork(ctx context.Context, name string, labels map[string]string, subnet netip.Prefix) error {
	if err := checkObjectName("network", name); err != nil {
		return err
	}
	if !subnet.IsValid() || !subnet.Addr().Is4() || subnet != subnet.Masked() {
		return fmt.Errorf("docker: network %s: subnet %s is not a canonical IPv4 prefix", name, subnet)
	}
	return c.call(ctx, c.timeout, http.MethodPost, "/networks/create", nil, networkCreateIPAM{
		Name: name, Driver: "bridge", Labels: labels,
		IPAM: networkIPAM{Config: []networkIPAMConfig{{Subnet: subnet.String()}}},
	}, nil)
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

// NetworkContainers returns the names of the containers connected to the network
// name. A missing network is an error for which IsNotFound reports true.
func (c *Client) NetworkContainers(ctx context.Context, name string) ([]string, error) {
	if err := checkObjectName("network", name); err != nil {
		return nil, err
	}
	var e networkListEntry
	if err := c.call(ctx, c.timeout, http.MethodGet, "/networks/"+url.PathEscape(name), nil, nil, &e); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(e.Containers))
	for _, ct := range e.Containers {
		names = append(names, strings.TrimPrefix(ct.Name, "/"))
	}
	return names, nil
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
