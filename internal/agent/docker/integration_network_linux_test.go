//go:build linux && dockerintegration

package docker_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

// TestIntegrationNetworkCalls checks the network calls against the real daemon: the
// answers the client maps (pool overlap, already connected, not connected, missing).
func TestIntegrationNetworkCalls(t *testing.T) {
	c := requireDocker(t)
	ctx := context.Background()
	name := fmt.Sprintf("mcpanel-it-%d", os.Getpid())
	netA, netB := name+"-a", name+"-b"
	t.Cleanup(func() {
		forceRemove(t, c, name)
		removeNetworks(t, c, netA, netB)
	})
	labels := map[string]string{"mcpanel.integration": "1"}
	subnet := netip.MustParsePrefix("10.213.255.224/28")
	if _, err := c.CreateNetwork(ctx, netA, labels, subnet); err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	if _, err := c.CreateNetwork(ctx, netB, labels, netip.MustParsePrefix("10.213.255.192/26")); !docker.IsPoolOverlap(err) {
		t.Fatalf("overlapping subnet: %v, want a pool overlap", err)
	}
	if _, err := c.CreateNetwork(ctx, netA, labels, netip.MustParsePrefix("10.213.255.240/28")); !docker.IsConflict(err) {
		t.Fatalf("existing name: %v, want a conflict", err)
	}
	nets, err := c.ListNetworks(ctx, "mcpanel.integration")
	if err != nil || len(nets) != 1 || nets[0].Name != netA || !slices.Equal(nets[0].Subnets, []netip.Prefix{subnet}) {
		t.Fatalf("ListNetworks = %+v, %v", nets, err)
	}

	cfg := docker.ContainerConfig{
		Image: pinnedRef(t, testImage), Cmd: testCmd, Labels: labels,
		HostConfig: docker.HostConfig{NetworkMode: "bridge", RestartPolicy: docker.RestartPolicy{Name: "no"},
			LogConfig: docker.LogConfig{Type: "json-file"}},
	}
	if _, err := c.CreateContainer(ctx, name, cfg); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(ctx, name); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := c.NetworkConnect(ctx, netA, name); err != nil {
			t.Fatalf("NetworkConnect: %v", err)
		}
	}
	info, err := c.Inspect(ctx, name)
	if err != nil || !info.NetworkSettings.OnNetwork(netA) || !subnet.Contains(netip.MustParseAddr(info.NetworkSettings.Networks[netA].IPAddress)) {
		t.Fatalf("inspect networks = %+v, %v", info.NetworkSettings, err)
	}
	if n, err := c.InspectNetwork(ctx, netA); err != nil || !slices.Equal(n.Containers, []string{name}) || n.HasIPv6() ||
		n.ID == "" || n.HostInterface() != "br-"+n.ID[:12] {
		t.Fatalf("InspectNetwork = %+v, %v", n, err)
	}
	if err := c.RemoveNetwork(ctx, netA); err == nil {
		t.Fatal("a network with a running container was removed")
	}
	for range 2 {
		if err := c.NetworkDisconnect(ctx, netA, name); err != nil {
			t.Fatalf("NetworkDisconnect: %v", err)
		}
	}
	if err := c.NetworkDisconnect(ctx, netB, name); err != nil {
		t.Fatalf("disconnect from a missing network: %v", err)
	}
	for range 2 {
		if err := c.RemoveNetwork(ctx, netA); err != nil {
			t.Fatalf("RemoveNetwork: %v", err)
		}
	}
}

// listenCmd serves port 25575 (busybox httpd) and exits on SIGTERM.
var listenCmd = []string{"sh", "-c", "trap 'exit 0' TERM; httpd -p 25575 -h /tmp; while true; do sleep 1; done"}

// execExit runs cmd in container and returns its exit code.
func execExit(t *testing.T, c *docker.Client, container string, cmd ...string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := c.ExecCreate(ctx, container, docker.ExecConfig{Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.ExecStart(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, out)
	_ = out.Close()
	for {
		info, err := c.ExecInspect(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Running && info.ExitCode != nil {
			return *info.ExitCode
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestIntegrationNetworkIsolation (SEC-03): two servers on their own networks cannot
// reach each other, the router reaches both by container name, and a container of an
// older agent on the shared network is moved off it at its next stop.
func TestIntegrationNetworkIsolation(t *testing.T) {
	c := requireDocker(t)
	ctx := context.Background()
	requireCleanHost(t, c)
	t.Cleanup(func() {
		forceRemove(t, c, instances.ContainerName(1), instances.ContainerName(2), instances.RouterName)
		removeNetworks(t, c, instances.RouterNetwork, instances.InstanceNetwork(1), instances.InstanceNetwork(2))
	})
	dir := t.TempDir()
	images := config.Images{Java8: testImage, Java17: testImage, Java21: testImage, Java25: testImage, Router: testImage}
	legacy := true // the first ensure builds a container of an older agent (shared network)
	m := instances.New(instances.Config{
		Docker: c, InstancesDir: dir, UID: nobody, GID: nobody, Images: images, Clock: clock.Real{},
		Log:         slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
		NetworkPool: netip.MustParsePrefix("10.213.254.0/24"),
		Customize: func(name string, cfg *docker.ContainerConfig) {
			if name == instances.RouterName {
				cfg.Cmd = testCmd
				cfg.HostConfig.PortBindings = nil // keep the host port free on developer machines
				cfg.ExposedPorts = nil
				return
			}
			cfg.Cmd = listenCmd
			if legacy && name == instances.ContainerName(1) {
				cfg.HostConfig.NetworkMode = instances.RouterNetwork
				cfg.NetworkingConfig = docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{instances.RouterNetwork: {}}}
			}
		},
	})
	t.Cleanup(m.Close)
	run := func(op func() error) {
		t.Helper()
		if err := op(); err != nil {
			t.Fatal(err)
		}
		m.Wait()
	}
	req := func(id int64, name string) agentapi.EnsureRequest {
		return agentapi.EnsureRequest{ID: id, Name: name, FQDN: name + ".mc.localhost", Revision: 1,
			Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 1024}}
	}
	// The shared network must exist before the legacy container is created on it.
	run(func() error { return m.Ensure(ctx, req(2, "two")) })
	run(func() error { return m.Ensure(ctx, req(1, "one")) })
	legacy = false
	run(func() error { return m.Power(ctx, 2, agentapi.PowerStart) })
	if err := c.Start(ctx, instances.ContainerName(1)); err != nil { // as Docker's restart policy would
		t.Fatal(err)
	}
	m.Tick(ctx)

	inspect := func(name string) docker.ContainerInfo {
		t.Helper()
		info, err := c.Inspect(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	one, two := inspect(instances.ContainerName(1)), inspect(instances.ContainerName(2))
	if !one.NetworkSettings.OnNetwork(instances.RouterNetwork) || !one.NetworkSettings.OnNetwork(instances.InstanceNetwork(1)) {
		t.Fatalf("running legacy container: %v", one.NetworkSettings.Networks)
	}
	if len(two.NetworkSettings.Networks) != 1 || !two.NetworkSettings.OnNetwork(instances.InstanceNetwork(2)) {
		t.Fatalf("server 2 networks: %v", two.NetworkSettings.Networks)
	}
	// The router reaches both servers by name.
	for _, id := range []int64{1, 2} {
		if code := execExit(t, c, instances.RouterName, "nc", "-z", "-w", "3", instances.ContainerName(id), "25575"); code != 0 {
			t.Fatalf("router cannot reach %s: exit %d", instances.ContainerName(id), code)
		}
	}
	ipOne := one.NetworkSettings.Networks[instances.InstanceNetwork(1)].IPAddress
	if code := execExit(t, c, instances.ContainerName(2), "nc", "-z", "-w", "3", ipOne, "25575"); code == 0 {
		t.Fatalf("server 2 reaches server 1's own network at %s", ipOne)
	}

	// Its next stop takes server 1 off the shared network.
	run(func() error { return m.Power(ctx, 1, agentapi.PowerRestart) })
	one = inspect(instances.ContainerName(1))
	if len(one.NetworkSettings.Networks) != 1 || !one.NetworkSettings.OnNetwork(instances.InstanceNetwork(1)) || !one.State.Running {
		t.Fatalf("after restart: %v, %+v", one.NetworkSettings.Networks, one.State)
	}
	for _, ip := range []string{one.NetworkSettings.Networks[instances.InstanceNetwork(1)].IPAddress, instances.ContainerName(1)} {
		if code := execExit(t, c, instances.ContainerName(2), "nc", "-z", "-w", "3", ip, "25575"); code == 0 {
			t.Fatalf("server 2 reaches server 1 at %s", ip)
		}
	}
	if code := execExit(t, c, instances.RouterName, "nc", "-z", "-w", "3", instances.ContainerName(1), "25575"); code != 0 {
		t.Fatalf("router cannot reach server 1 after the restart: exit %d", code)
	}

	// A network deleted from outside while its server and the router were off (docker
	// network prune) is repaired by the next start: Docker 29 refuses to start a container
	// whose endpoint points to the deleted network, so it is reconnected.
	run(func() error { return m.Power(ctx, 2, agentapi.PowerStop) })
	if err := c.NetworkDisconnect(ctx, instances.InstanceNetwork(2), instances.RouterName); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveNetwork(ctx, instances.InstanceNetwork(2)); err != nil {
		t.Fatal(err)
	}
	run(func() error { return m.Power(ctx, 2, agentapi.PowerStart) })
	if two := inspect(instances.ContainerName(2)); !two.State.Running {
		t.Fatalf("server 2 after the network repair: %+v", two.State)
	}
	if code := execExit(t, c, instances.RouterName, "nc", "-z", "-w", "3", instances.ContainerName(2), "25575"); code != 0 {
		t.Fatalf("router cannot reach server 2 after the network repair: exit %d", code)
	}

	run(func() error { return m.Remove(ctx, 1) })
	if n, err := c.InspectNetwork(ctx, instances.InstanceNetwork(1)); !docker.IsNotFound(err) {
		t.Fatalf("network of a removed server: %+v, %v", n, err)
	}
}
