package docker_test

import (
	"context"
	"net/netip"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
)

func TestCreateNetworkWithSubnet(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	ctx := context.Background()
	labels := map[string]string{"mcpanel.network": "1", "mcpanel.instance": "1"}
	if _, err := c.CreateNetwork(ctx, "mcpanel-net-1", labels, netip.MustParsePrefix("10.213.0.0/28")); err != nil {
		t.Fatal(err)
	}
	if got := d.NetworkSubnets("mcpanel-net-1"); !slices.Equal(got, []string{"10.213.0.0/28"}) {
		t.Fatalf("subnets = %v", got)
	}
	_, err := c.CreateNetwork(ctx, "mcpanel-net-2", labels, netip.MustParsePrefix("10.213.0.0/24"))
	if !docker.IsPoolOverlap(err) {
		t.Fatalf("overlapping subnet: %v, want a pool overlap", err)
	}
	_, err = c.CreateNetwork(ctx, "mcpanel-net-1", labels, netip.MustParsePrefix("10.213.0.16/28"))
	if !docker.IsConflict(err) || docker.IsPoolOverlap(err) {
		t.Fatalf("existing name: %v, want a conflict", err)
	}
	for name, subnet := range map[string]string{
		"x/../y":        "10.213.1.0/28",
		"":              "10.213.1.0/28",
		"mcpanel-net-3": "fd00::/64",
		"mcpanel-net-4": "10.213.1.1/28",
	} {
		if _, err := c.CreateNetwork(ctx, name, labels, netip.MustParsePrefix(subnet)); err == nil {
			t.Errorf("CreateNetwork(%q, %s) = nil, want error", name, subnet)
		}
	}
	for _, call := range d.Calls() {
		if call != "network create" {
			t.Fatalf("invalid requests reached Docker: %v", d.Calls())
		}
	}
}

func TestListNetworks(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	d.AddNetwork("mcpanel-net-1", "bridge", map[string]string{"mcpanel.network": "1"}, "10.213.0.0/28")
	d.AddNetwork("other", "bridge", nil, "172.20.0.0/16", "fd00:1::/64")
	all, err := c.ListNetworks(context.Background(), "")
	if err != nil || len(all) != 2 {
		t.Fatalf("all = %+v, %v", all, err)
	}
	if all[1].Name != "other" || !slices.Equal(all[1].Subnets, []netip.Prefix{netip.MustParsePrefix("172.20.0.0/16"), netip.MustParsePrefix("fd00:1::/64")}) {
		t.Fatalf("other = %+v", all[1])
	}
	ours, err := c.ListNetworks(context.Background(), "mcpanel.network")
	if err != nil || len(ours) != 1 || ours[0].Name != "mcpanel-net-1" || ours[0].Driver != "bridge" || ours[0].Labels["mcpanel.network"] != "1" {
		t.Fatalf("labelled = %+v, %v", ours, err)
	}
	d.SetNetworkSubnets("other", "garbage")
	if _, err := c.ListNetworks(context.Background(), ""); err == nil {
		t.Fatal("an invalid subnet must fail the list")
	}
}

// Connect and disconnect are idempotent; summaries and inspect show the networks.
func TestNetworkConnectDisconnect(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	ctx := context.Background()
	d.AddNetwork("mcpanel-mc", "bridge", nil, "172.18.0.0/16")
	d.AddNetwork("mcpanel-net-1", "bridge", nil, "10.213.0.0/28")
	d.AddContainer("mcpanel-mc-1", docker.ContainerConfig{Image: "x", Labels: map[string]string{"mcpanel.instance": "1"}, HostConfig: docker.HostConfig{NetworkMode: "mcpanel-mc"}}, true)
	for range 2 {
		if err := c.NetworkConnect(ctx, "mcpanel-net-1", "mcpanel-mc-1"); err != nil {
			t.Fatalf("NetworkConnect: %v", err)
		}
	}
	info, err := c.Inspect(ctx, "mcpanel-mc-1")
	if err != nil || !info.NetworkSettings.OnNetwork("mcpanel-mc") || !info.NetworkSettings.OnNetwork("mcpanel-net-1") {
		t.Fatalf("inspect networks = %+v, %v", info.NetworkSettings, err)
	}
	n, err := c.InspectNetwork(ctx, "mcpanel-net-1")
	if err != nil || !slices.Equal(n.Containers, []string{"mcpanel-mc-1"}) || n.ID == "" {
		t.Fatalf("InspectNetwork = %+v, %v", n, err)
	}
	if ep := info.NetworkSettings.Networks["mcpanel-net-1"]; ep.NetworkID != n.ID {
		t.Fatalf("endpoint network ID = %q, want %q", ep.NetworkID, n.ID)
	}
	if err := c.RemoveNetwork(ctx, "mcpanel-net-1"); err == nil {
		t.Fatal("a network with a running container must not be removed")
	}
	for range 2 {
		if err := c.NetworkDisconnect(ctx, "mcpanel-mc", "mcpanel-mc-1"); err != nil {
			t.Fatalf("NetworkDisconnect: %v", err)
		}
	}
	list, err := c.List(ctx, "mcpanel.instance")
	if err != nil || len(list) != 1 || list[0].NetworkSettings.OnNetwork("mcpanel-mc") || !list[0].NetworkSettings.OnNetwork("mcpanel-net-1") {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := c.NetworkDisconnect(ctx, "gone", "mcpanel-mc-1"); err != nil {
		t.Fatalf("disconnect from a missing network: %v", err)
	}
	if err := c.NetworkConnect(ctx, "mcpanel-net-1", "gone"); !docker.IsNotFound(err) {
		t.Fatalf("connect a missing container: %v", err)
	}
	if err := c.NetworkConnect(ctx, "mcpanel-net-1", "../x"); err == nil {
		t.Fatal("invalid container name accepted")
	}
	if _, err := c.InspectNetwork(ctx, "gone"); !docker.IsNotFound(err) {
		t.Fatalf("members of a missing network: %v", err)
	}
}

// CreateNetwork and EnsureNetwork ask for IPv4 only, whatever the daemon's defaults.
func TestNetworksAreIPv4Only(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	ctx := context.Background()
	if _, err := c.CreateNetwork(ctx, "mcpanel-net-1", nil, netip.MustParsePrefix("10.213.0.0/28")); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureNetwork(ctx, "mcpanel-mc", nil); err != nil {
		t.Fatal(err)
	}
	nets, err := c.ListNetworks(ctx, "")
	if err != nil || len(nets) != 2 || nets[0].HasIPv6() || nets[1].HasIPv6() {
		t.Fatalf("networks = %+v, %v", nets, err)
	}
	d.SetNetworkIPv6("mcpanel-mc", true)
	if n, err := c.InspectNetwork(ctx, "mcpanel-mc"); err != nil || !n.HasIPv6() {
		t.Fatalf("IPv6 network = %+v, %v", n, err)
	}
	n := docker.Network{Driver: "bridge", Subnets: []netip.Prefix{netip.MustParsePrefix("fd00::/64")}}
	if !n.HasIPv6() {
		t.Fatal("an IPv6 subnet must count as IPv6")
	}
}

// Only running containers have an endpoint in a network inspect, as in Docker.
func TestInspectNetworkListsRunningContainersOnly(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	d.AddNetwork("mcpanel-net-1", "bridge", nil, "10.213.0.0/28")
	d.AddContainer("mcpanel-mc-1", docker.ContainerConfig{Image: "x", HostConfig: docker.HostConfig{NetworkMode: "mcpanel-net-1"}}, false)
	if n, err := c.InspectNetwork(context.Background(), "mcpanel-net-1"); err != nil || len(n.Containers) != 0 {
		t.Fatalf("created container listed: %+v, %v", n, err)
	}
}

// A container that ran on a network deleted and created again does not start (stale
// endpoint) until it is disconnected and connected again, as in Docker 29.
func TestStaleEndpointAfterNetworkRecreate(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	ctx := context.Background()
	d.AddNetwork("mcpanel-net-1", "bridge", nil, "10.213.0.0/28")
	d.AddContainer("mcpanel-mc-1", docker.ContainerConfig{Image: "x", HostConfig: docker.HostConfig{NetworkMode: "mcpanel-net-1"}}, false)
	if err := c.Start(ctx, "mcpanel-mc-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(ctx, "mcpanel-mc-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveNetwork(ctx, "mcpanel-net-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, "mcpanel-mc-1"); !docker.IsNotFound(err) {
		t.Fatalf("start without the network = %v", err)
	}
	if _, err := c.CreateNetwork(ctx, "mcpanel-net-1", nil, netip.MustParsePrefix("10.213.0.0/28")); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, "mcpanel-mc-1"); !docker.IsNotFound(err) {
		t.Fatalf("start with a stale endpoint = %v", err)
	}
	if err := c.NetworkDisconnect(ctx, "mcpanel-net-1", "mcpanel-mc-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.NetworkConnect(ctx, "mcpanel-net-1", "mcpanel-mc-1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, "mcpanel-mc-1"); err != nil {
		t.Fatalf("start after reconnect = %v", err)
	}
}

func TestRemoveNetwork(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	d.AddNetwork("mcpanel-net-1", "bridge", nil, "10.213.0.0/28")
	for range 2 {
		if err := c.RemoveNetwork(context.Background(), "mcpanel-net-1"); err != nil {
			t.Fatalf("RemoveNetwork: %v", err)
		}
	}
	if d.HasNetwork("mcpanel-net-1") {
		t.Fatal("network still there")
	}
}
