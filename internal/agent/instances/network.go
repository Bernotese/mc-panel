package instances

import (
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/config"
)

// Every instance container sits alone on its own bridge network mcpanel-net-<id>
// (security review SEC-03): servers cannot reach each other's RCON, game or plugin
// ports. mc-router keeps its home network mcpanel-mc (the published port) and is
// connected to every instance network, where it reaches the backend by container name.

// InstanceNetworkPrefix starts the name of every per-instance network.
const InstanceNetworkPrefix = "mcpanel-net-"

// maxAllocationTries bounds the subnet candidates tried after Docker reports an overlap
// the agent could not see (a network created meanwhile by someone else).
const maxAllocationTries = 16

// ErrPoolExhausted reports that instance_network_pool has no free subnet left.
var ErrPoolExhausted = errors.New("instance network pool exhausted")

// InstanceNetwork is the name of the network of instance id.
func InstanceNetwork(id int64) string { return InstanceNetworkPrefix + strconv.FormatInt(id, 10) }

// routerNetworkLabels mark the router's network mcpanel-mc as mc-panel's own.
func routerNetworkLabels() map[string]string { return map[string]string{LabelNetwork: "1"} }

// instanceNetworkLabels mark the network of instance id.
func instanceNetworkLabels(id int64) map[string]string {
	return map[string]string{LabelNetwork: "1", LabelInstance: strconv.FormatInt(id, 10)}
}

// managedNetworkID returns the instance of a network mc-panel created for it: a bridge
// called mcpanel-net-<id> carrying both labels for that id. Anything else is foreign.
func managedNetworkID(n docker.Network) (int64, bool) {
	rest, ok := strings.CutPrefix(n.Name, InstanceNetworkPrefix)
	if !ok || n.Driver != "bridge" || n.Labels[LabelNetwork] != "1" {
		return 0, false
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != rest || n.Labels[LabelInstance] != rest {
		return 0, false
	}
	return id, true
}

// isRouterNetwork reports whether n is mc-panel's own mcpanel-mc network.
func isRouterNetwork(n docker.Network) bool {
	return n.Name == RouterNetwork && n.Driver == "bridge" && n.Labels[LabelNetwork] == "1"
}

// NextSubnet returns the first /bits block of pool that overlaps none of used.
func NextSubnet(pool netip.Prefix, bits int, used []netip.Prefix) (netip.Prefix, bool) {
	if !pool.Addr().Is4() || bits < pool.Bits() || bits > 32 {
		return netip.Prefix{}, false
	}
	base := binary.BigEndian.Uint32(pool.Masked().Addr().AsSlice())
	size := uint64(1) << (32 - bits)
	for i := uint64(0); i < uint64(1)<<(bits-pool.Bits()); i++ {
		var a [4]byte
		binary.BigEndian.PutUint32(a[:], base+uint32(i*size))
		candidate := netip.PrefixFrom(netip.AddrFrom4(a), bits)
		if !slices.ContainsFunc(used, candidate.Overlaps) {
			return candidate, true
		}
	}
	return netip.Prefix{}, false
}

// HostPrefixes returns the IPv4 networks of the host's interfaces, except Docker's own
// bridges and veths (Docker networks are taken from Docker itself) and loopback.
func HostPrefixes() ([]netip.Prefix, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagLoopback != 0 || strings.HasPrefix(ifc.Name, "br-") ||
			strings.HasPrefix(ifc.Name, "docker") || strings.HasPrefix(ifc.Name, "veth") {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			p, err := netip.ParsePrefix(ipn.String())
			if err == nil && p.Addr().Is4() {
				out = append(out, p.Masked())
			}
		}
	}
	return out, nil
}

// CheckNetworkPool warns when instance_network_pool overlaps a network of the host:
// containers could then not reach those addresses, and the host might route them wrong.
func (m *Manager) CheckNetworkPool() {
	hp, err := m.hostPrefixes()
	if err != nil {
		m.log.Warn("cannot read the host's networks to check instance_network_pool", "err", err)
		return
	}
	for _, p := range hp {
		if p.Overlaps(m.pool) {
			m.log.Warn("instance_network_pool overlaps a network of this host; set another range in agent.toml",
				"instance_network_pool", m.pool.String(), "host_network", p.String())
		}
	}
}

// ensureInstanceNetwork makes sure the network of instance id exists, the egress filter
// covers its subnet (fail closed: no container is created on or connected to a network
// the filter does not cover) and the router, if it exists, is connected to it.
func (m *Manager) ensureInstanceNetwork(ctx context.Context, id int64) error {
	m.netMu.Lock()
	defer m.netMu.Unlock()
	name := InstanceNetwork(id)
	nets, err := m.dc.ListNetworks(ctx, "")
	if err != nil {
		return err
	}
	var subnets []netip.Prefix
	i := slices.IndexFunc(nets, func(n docker.Network) bool { return n.Name == name })
	if i >= 0 {
		if got, ok := managedNetworkID(nets[i]); !ok || got != id {
			return fmt.Errorf("docker: network %s exists but is not managed by mc-panel; leaving it untouched", name)
		}
		subnets = nets[i].Subnets
	} else {
		subnet, err := m.createInstanceNetwork(ctx, id, nets)
		if err != nil {
			return err
		}
		subnets = []netip.Prefix{subnet}
	}
	if len(subnets) == 0 {
		return fmt.Errorf("docker: network %s has no subnet", name)
	}
	if err := m.syncEgress(ctx); err != nil {
		return fmt.Errorf("container egress filter: %w", err)
	}
	if !m.egressCovers(subnets) {
		return fmt.Errorf("container egress filter: network %s (%v) is not covered", name, subnets)
	}
	if err := m.dc.NetworkConnect(ctx, name, RouterName); err != nil && !docker.IsNotFound(err) {
		return fmt.Errorf("connect %s to %s: %w", RouterName, name, err)
	}
	return nil
}

// createInstanceNetwork creates the network of instance id with the first free subnet of
// the pool; nets are all existing networks. The caller holds netMu.
func (m *Manager) createInstanceNetwork(ctx context.Context, id int64, nets []docker.Network) (netip.Prefix, error) {
	used, err := m.hostPrefixes()
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("read the host's networks: %w", err)
	}
	for _, n := range nets {
		used = append(used, n.Subnets...)
	}
	for range maxAllocationTries {
		subnet, ok := NextSubnet(m.pool, config.InstanceNetworkBits, used)
		if !ok {
			return netip.Prefix{}, fmt.Errorf("%w (%s, /%d per server)", ErrPoolExhausted, m.pool, config.InstanceNetworkBits)
		}
		err := m.dc.CreateNetwork(ctx, InstanceNetwork(id), instanceNetworkLabels(id), subnet)
		if docker.IsPoolOverlap(err) {
			used = append(used, subnet)
			continue
		}
		if err != nil {
			return netip.Prefix{}, err
		}
		m.log.Info("instance network created", "id", id, "network", InstanceNetwork(id), "subnet", subnet.String())
		return subnet, nil
	}
	return netip.Prefix{}, fmt.Errorf("network %s: every tried subnet overlaps another network", InstanceNetwork(id))
}

// egressCovers reports whether the filter was last applied with all of subnets.
func (m *Manager) egressCovers(subnets []netip.Prefix) bool {
	if m.egress == nil {
		return true
	}
	m.egressMu.Lock()
	defer m.egressMu.Unlock()
	for _, s := range subnets {
		if !slices.Contains(m.egressApplied, s.Masked()) {
			return false
		}
	}
	return true
}

// removeInstanceNetwork disconnects the router from the network of instance id and
// removes it. A failure is logged; the loop's cleanup retries.
func (m *Manager) removeInstanceNetwork(ctx context.Context, id int64) {
	m.netMu.Lock()
	defer m.netMu.Unlock()
	name := InstanceNetwork(id)
	err := m.dc.NetworkDisconnect(ctx, name, RouterName)
	if err == nil {
		err = m.dc.RemoveNetwork(ctx, name)
	}
	if err != nil {
		m.log.Warn("instance network not removed; the loop retries", "id", id, "network", name, "err", sanitizeError(err))
		return
	}
	m.log.Info("instance network removed", "id", id, "network", name)
}

// managedNetworks returns the subnets of mc-panel's own networks (mcpanel-mc and every
// mcpanel-net-<id>) in a fixed order, and the instance networks by instance ID.
func (m *Manager) managedNetworks(ctx context.Context) ([]netip.Prefix, map[int64]docker.Network, error) {
	nets, err := m.dc.ListNetworks(ctx, LabelNetwork)
	if err != nil {
		return nil, nil, err
	}
	var subnets []netip.Prefix
	byID := map[int64]docker.Network{}
	for _, n := range nets {
		id, ok := managedNetworkID(n)
		if !ok && !isRouterNetwork(n) {
			continue
		}
		if ok {
			byID[id] = n
		}
		for _, s := range n.Subnets {
			if !slices.Contains(subnets, s) {
				subnets = append(subnets, s)
			}
		}
	}
	slices.SortFunc(subnets, func(a, b netip.Prefix) int {
		return cmp.Or(a.Addr().Compare(b.Addr()), cmp.Compare(a.Bits(), b.Bits()))
	})
	return subnets, byID, nil
}

// isolate moves one container of an older agent from the shared mcpanel-mc onto its own
// network: it is connected to mcpanel-net-<id> at once (the router reaches it there), and
// disconnected from mcpanel-mc only while stopped, so online players are never cut off.
// It reports whether anything changed.
func (m *Manager) isolate(ctx context.Context, id int64, ns docker.NetworkSettings, stopped bool) (bool, error) {
	name, own := ContainerName(id), InstanceNetwork(id)
	changed := false
	if !ns.OnNetwork(own) {
		if err := m.ensureInstanceNetwork(ctx, id); err != nil {
			return false, err
		}
		if err := m.dc.NetworkConnect(ctx, own, name); err != nil {
			return false, err
		}
		m.log.Info("instance connected to its own network", "id", id, "network", own)
		changed = true
	}
	if stopped && ns.OnNetwork(RouterNetwork) {
		if err := m.dc.NetworkDisconnect(ctx, RouterNetwork, name); err != nil {
			return changed, err
		}
		m.log.Info("instance disconnected from the shared network", "id", id, "network", RouterNetwork)
		changed = true
	}
	return changed, nil
}

// busy reports whether a worker owns instance id right now (queued or running work).
func (m *Manager) busy(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.inst[id]
	return st != nil && (st.running || len(st.pending) > 0)
}

func stoppedState(state string) bool {
	return state == "created" || state == "exited" || state == "dead"
}

// reconcileNetworks is one round of the loop: containers of an older agent are moved
// onto their own network (see isolate), networks whose instance is gone are removed, and
// the instances still reachable over mcpanel-mc are logged whenever that list changes.
func (m *Manager) reconcileNetworks(ctx context.Context) error {
	list, err := m.dc.List(ctx, LabelInstance)
	if err != nil {
		return err
	}
	var errs []error
	var shared []int64
	present := map[int64]bool{}
	for _, c := range list {
		id, ok := managedID(c.Labels, c.Names...)
		if !ok {
			continue
		}
		present[id] = true
		if m.busy(id) {
			continue
		}
		stopped := stoppedState(c.State)
		if _, err := m.isolate(ctx, id, c.NetworkSettings, stopped); err != nil {
			errs = append(errs, fmt.Errorf("instance %d: %w", id, err))
		}
		if c.NetworkSettings.OnNetwork(RouterNetwork) && !stopped {
			shared = append(shared, id)
		}
	}
	m.reportShared(shared)
	_, byID, err := m.managedNetworks(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for id := range byID {
		if !present[id] && !m.busy(id) {
			if err := m.removeOrphanNetwork(ctx, id); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// removeOrphanNetwork removes the network of an instance without container, unless a
// container other than the router is still connected to it.
func (m *Manager) removeOrphanNetwork(ctx context.Context, id int64) error {
	name := InstanceNetwork(id)
	members, err := m.dc.NetworkContainers(ctx, name)
	if docker.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if slices.ContainsFunc(members, func(c string) bool { return c != RouterName }) {
		return fmt.Errorf("network %s has no instance but other containers (%v); leaving it", name, members)
	}
	m.removeInstanceNetwork(ctx, id)
	return nil
}

// reportShared logs the running instances that are still on the shared network whenever
// that list changes.
func (m *Manager) reportShared(ids []int64) {
	slices.Sort(ids)
	m.netMu.Lock()
	changed := !slices.Equal(ids, m.shared)
	m.shared = ids
	m.netMu.Unlock()
	switch {
	case !changed:
	case len(ids) > 0:
		m.log.Warn("servers still share the network mcpanel-mc with each other; each is isolated at its next stop or restart",
			"ids", fmt.Sprint(ids))
	default:
		m.log.Info("every server is on its own network")
	}
}

// syncRouterNetworks connects mc-router to the network of every instance whose
// container is on it and disconnects it from the networks of instances that are gone.
func (m *Manager) syncRouterNetworks(ctx context.Context, list []docker.ContainerSummary) error {
	info, err := m.dc.Inspect(ctx, RouterName)
	if docker.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, c := range list {
		if id, ok := managedID(c.Labels, c.Names...); ok && c.NetworkSettings.OnNetwork(InstanceNetwork(id)) {
			want[InstanceNetwork(id)] = true
		}
	}
	var errs []error
	for _, n := range slices.Sorted(maps.Keys(want)) {
		if !info.NetworkSettings.OnNetwork(n) {
			if err := m.dc.NetworkConnect(ctx, n, RouterName); err != nil {
				errs = append(errs, fmt.Errorf("connect %s to %s: %w", RouterName, n, err))
				continue
			}
			m.log.Info("router connected", "network", n)
		}
	}
	for n := range info.NetworkSettings.Networks {
		rest, ok := strings.CutPrefix(n, InstanceNetworkPrefix)
		if !ok || want[n] {
			continue
		}
		// A recreate removes the container for a moment; its worker reconnects the router.
		if id, err := strconv.ParseInt(rest, 10, 64); err == nil && m.busy(id) {
			continue
		}
		if err := m.dc.NetworkDisconnect(ctx, n, RouterName); err != nil {
			errs = append(errs, fmt.Errorf("disconnect %s from %s: %w", RouterName, n, err))
		}
	}
	return errors.Join(errs...)
}
