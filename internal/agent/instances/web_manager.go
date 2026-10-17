package instances

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
)

// The web routes (web ports spec §4): the panel sends the complete desired list with
// PUT /v1/web/routes; SetWebRoutes only stores it and wakes the apply loop, so the request
// never waits for Docker (an image pull can take minutes). applyWeb brings mcpanel-web, its
// networks, the bridge port restriction and the Caddy config in line with the list, and runs
// again on every tick, which repairs anything changed from outside.

const (
	// webAskTimeout bounds one check of the ask endpoint (wget's own -T is 2 s; the rest is
	// for the Docker exec around it).
	webAskTimeout = 5 * time.Second
	// webAskWget is wget's network timeout in seconds inside mcpanel-web.
	webAskWget = "2"
	// webConfirmWindow is how long a route may stay unconfirmed by the ask endpoint after its
	// config was written or the container was (re)created before it reports an error: a
	// freshly created Caddy needs a moment to listen, and its file watch polls once per second.
	webConfirmWindow = 30 * time.Second
	// webRechecks bounds the quick rechecks of pending routes after a new list.
	webRechecks = 25
	// webAskOutputBytes bounds what the agent reads of wget's output.
	webAskOutputBytes = 4 << 10

	webErrContainer = "server container missing"
	webErrNetwork   = "server network missing"
	webErrRules     = "port restriction not applied"
	webErrNotLoaded = "web router did not load the config"
)

// webRecheck is the pause before the apply loop checks a pending route again (Caddy's
// file watch polls once per second).
const webRecheck = 1500 * time.Millisecond

// WebFilter is the bridge port restriction of mcpanel-web (firewall.Backend): for every rule,
// mcpanel-web at Source may open TCP connections to Port in Dest, and nothing else from Source.
type WebFilter interface {
	SetWebRules(ctx context.Context, rules []agentapi.WebRule) error
	WebRulesInPlace(ctx context.Context, rules []agentapi.WebRule) (bool, error)
}

// webRouteState is the last applied state of one route.
type webRouteState struct {
	route  agentapi.WebRoute
	status agentapi.WebRouteStatus
	// unconfirmed is when the ask endpoint stopped confirming the route (or when its config
	// was written); zero while it is confirmed.
	unconfirmed time.Time
}

// SetWebRoutes implements agent.Web. It validates req, stores it as the desired list, wakes
// the apply loop and returns at once: a route answers its last status if it is unchanged
// since it was applied, else pending.
func (m *Manager) SetWebRoutes(ctx context.Context, req agentapi.WebRoutesRequest) (agentapi.WebRoutesResponse, error) {
	if err := req.Validate(); err != nil {
		return agentapi.WebRoutesResponse{}, err
	}
	if !m.dockerAvailable(ctx) {
		return agentapi.WebRoutesResponse{}, agentapi.ErrDockerUnavailable
	}
	resp := agentapi.WebRoutesResponse{Routes: make([]agentapi.WebRouteStatus, 0, len(req.Routes))}
	m.webMu.Lock()
	m.webDesired = append([]agentapi.WebRoute{}, req.Routes...) // never nil: a list was received
	for _, r := range req.Routes {
		st, ok := m.webStatus[r.ID]
		if ok && st.route == r {
			resp.Routes = append(resp.Routes, st.status)
		} else {
			resp.Routes = append(resp.Routes, agentapi.WebRouteStatus{ID: r.ID, Status: agentapi.WebPending})
		}
	}
	m.webMu.Unlock()
	select {
	case m.webWake <- struct{}{}:
	default: // a round is already due
	}
	return resp, nil
}

// webLoop applies the routes whenever SetWebRoutes stored a new list, and checks pending
// routes again shortly after instead of waiting for the next tick.
func (m *Manager) webLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.webWake:
		}
		for range webRechecks {
			if !m.applyWeb(ctx) {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(webRecheck):
			}
		}
	}
}

// applyWeb is one apply round; it reports whether a route is still pending. Rounds are
// serialised; network changes take netMu.
func (m *Manager) applyWeb(ctx context.Context) bool {
	m.webApplyMu.Lock()
	defer m.webApplyMu.Unlock()
	return m.applyWebLocked(ctx)
}

// tryApplyWeb runs a round unless one is running (Tick must not wait behind an image pull of
// a round the apply loop started; that round already works on the current list).
func (m *Manager) tryApplyWeb(ctx context.Context) {
	if !m.webApplyMu.TryLock() {
		return
	}
	defer m.webApplyMu.Unlock()
	m.applyWebLocked(ctx)
}

func (m *Manager) applyWebLocked(ctx context.Context) (pending bool) {
	m.webMu.Lock()
	desired := slices.Clone(m.webDesired)
	received := m.webDesired != nil
	prev := make(map[int64]webRouteState, len(m.webStatus))
	for id, st := range m.webStatus {
		prev[id] = st
	}
	m.webMu.Unlock()
	// After an agent start nothing is known until the panel's first PUT: mcpanel-web, its
	// networks and rules stay as they are (they keep serving the last list).
	if !received {
		return false
	}
	var next map[int64]webRouteState
	if len(desired) == 0 {
		next = map[int64]webRouteState{}
		if err := m.removeWeb(ctx); err != nil {
			m.noteWebErr(fmt.Errorf("remove the web router: %w", err))
		} else {
			m.noteWebErr(nil)
		}
	} else {
		next = m.applyWebRoutes(ctx, desired, prev)
	}
	m.webMu.Lock()
	// A newer list may have arrived meanwhile; its round follows and keeps only its ids.
	m.webStatus = next
	m.webMu.Unlock()
	for _, r := range desired {
		old, had := prev[r.ID]
		st := next[r.ID].status
		pending = pending || st.Status == agentapi.WebPending
		if !had || old.status != st {
			if st.Status == agentapi.WebError {
				m.log.Warn("web route not applied", "id", r.ID, "fqdn", r.FQDN, "err", st.Error)
			} else {
				m.log.Info("web route", "id", r.ID, "fqdn", r.FQDN, "port", r.Port, "status", st.Status)
			}
		}
	}
	return pending
}

// noteWebErr logs a change of the general apply error once.
func (m *Manager) noteWebErr(err error) {
	msg := ""
	if err != nil {
		msg = sanitizeError(err)
	}
	m.webMu.Lock()
	changed := msg != m.webErr
	m.webErr = msg
	m.webMu.Unlock()
	if changed && msg != "" {
		m.log.Warn("web router not ready", "err", msg)
	}
}

// removeWeb removes mc-panel's mcpanel-web container, its network and the port restriction
// (the list is empty). A container or network that only shares the name stays.
func (m *Manager) removeWeb(ctx context.Context) error {
	info, err := m.dc.Inspect(ctx, WebName)
	switch {
	case docker.IsNotFound(err):
	case err != nil:
		return err
	case info.Config.Labels[LabelWeb] != "1":
		m.log.Warn("container not managed by mc-panel; leaving it untouched", "container", WebName)
	default:
		if err := m.dc.Remove(ctx, WebName, true); err != nil && !docker.IsNotFound(err) {
			return err
		}
		m.log.Info("web router container removed")
	}
	m.netMu.Lock()
	n, err := m.dc.InspectNetwork(ctx, WebNetwork)
	switch {
	case docker.IsNotFound(err):
		err = nil
	case err != nil:
	case !isWebNetwork(n):
		m.log.Warn("network not managed by mc-panel; leaving it untouched", "network", WebNetwork)
	default:
		if err = m.dc.RemoveNetwork(ctx, WebNetwork); err == nil {
			m.log.Info("web router network removed")
		}
	}
	m.netMu.Unlock()
	if err != nil {
		return err
	}
	_ = m.syncEgress(ctx) // the subnet is gone; logged inside
	return m.setWebRules(ctx, nil)
}

// setWebRules hands rules to the filter when they differ from the last ones applied, when
// they were not applied since the agent started, or when they were removed from outside.
func (m *Manager) setWebRules(ctx context.Context, rules []agentapi.WebRule) error {
	if m.web == nil {
		return nil
	}
	if rules == nil {
		rules = []agentapi.WebRule{}
	}
	if m.webRules != nil && slices.Equal(rules, m.webRules) {
		ok, err := m.web.WebRulesInPlace(ctx, rules)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		m.log.Warn("web port restriction was changed from outside; reinstalling it")
	}
	m.webRules = nil // unknown until the filter accepted the new rules
	if err := m.web.SetWebRules(ctx, rules); err != nil {
		return err
	}
	m.webRules = slices.Clone(rules)
	return nil
}

// webTarget is a route whose server network is ready for mcpanel-web.
type webTarget struct {
	route   agentapi.WebRoute
	network string
	rule    agentapi.WebRule
}

// applyWebRoutes applies a non-empty list and returns the state of every route. The order
// keeps the port restriction fail-closed: mcpanel-web is never on a server network without
// its rule (disconnect before the rule goes, rule before the connect).
func (m *Manager) applyWebRoutes(ctx context.Context, desired []agentapi.WebRoute, prev map[int64]webRouteState) map[int64]webRouteState {
	next := make(map[int64]webRouteState, len(desired))
	fail := func(r agentapi.WebRoute, msg string) {
		next[r.ID] = webRouteState{route: r, status: agentapi.WebRouteStatus{ID: r.ID, Status: agentapi.WebError, Error: msg}}
	}
	failAll := func(err error) map[int64]webRouteState {
		m.noteWebErr(err)
		msg := sanitizeError(err)
		for _, r := range desired {
			if _, done := next[r.ID]; !done {
				fail(r, msg)
			}
		}
		return next
	}

	// 1. The web router's own network, covered by the egress filter.
	webNet, err := m.ensureWebNetwork(ctx)
	if err != nil {
		return failAll(fmt.Errorf("web router network: %w", err))
	}
	// 2. The container, if it exists, must be mc-panel's own.
	info, err := m.dc.Inspect(ctx, WebName)
	exists := err == nil
	switch {
	case docker.IsNotFound(err):
	case err != nil:
		return failAll(fmt.Errorf("web router: %w", err))
	case info.Config.Labels[LabelWeb] != "1":
		return failAll(fmt.Errorf("container %s exists but is not managed by mc-panel (label %s=1 missing); leaving it untouched",
			WebName, LabelWeb))
	}
	// 3. The server networks of the routes.
	targets, byID, err := m.webTargets(ctx, desired, fail)
	if err != nil {
		return failAll(err)
	}
	// 4. Leave the server networks that have no route any more.
	var stay []docker.Network
	if exists {
		stay = m.disconnectWeb(ctx, info.NetworkSettings, byID, targets, false)
	}
	// 5. The port restriction: a rule for every target, and the old rule for every network
	// mcpanel-web is still on (busy server, failed disconnect) until it is gone.
	rules := make([]agentapi.WebRule, 0, len(targets)+len(stay))
	for _, t := range targets {
		rules = append(rules, t.rule)
	}
	for _, n := range stay {
		for _, r := range m.webRules {
			if slices.Contains(n.Subnets, r.Dest) && !slices.Contains(rules, r) {
				rules = append(rules, r)
			}
		}
	}
	if err := m.setWebRules(ctx, rules); err != nil {
		m.log.Warn("web port restriction not applied; disconnecting the web router from every server network",
			"err", sanitizeError(err))
		if exists {
			m.disconnectWeb(ctx, info.NetworkSettings, byID, nil, true)
		}
		return failAll(fmt.Errorf("%s: %w", webErrRules, err))
	}
	// 6. The Caddy config (only the targets), then the container.
	routes := make([]agentapi.WebRoute, 0, len(targets))
	for _, t := range targets {
		routes = append(routes, t.route)
	}
	data, err := RenderWebConfig(routes, webNet.Subnets[0])
	if err != nil {
		return failAll(fmt.Errorf("web router config: %w", err))
	}
	changed, err := writeReadable(WebDir(m.host.InstancesDir), webConfigFile, data)
	if err != nil {
		return failAll(fmt.Errorf("web router config: %w", err))
	}
	created, err := m.ensureWebContainer(ctx, info, exists)
	if err != nil {
		if errors.Is(err, docker.ErrUnavailable) {
			m.setDocker(false, err)
		}
		return failAll(fmt.Errorf("web router not running: %w", err))
	}
	// 7. Connect to the server networks with the fixed address the rules name.
	info, err = m.dc.Inspect(ctx, WebName)
	if err != nil {
		return failAll(fmt.Errorf("web router: %w", err))
	}
	var ready []webTarget
	for _, t := range targets {
		if err := m.connectWeb(ctx, info.NetworkSettings, t); err != nil {
			fail(t.route, sanitizeError(fmt.Errorf("connect the web router: %w", err)))
			continue
		}
		ready = append(ready, t)
	}
	m.noteWebErr(nil)
	// 8. Confirm through the ask endpoint that Caddy serves the route.
	m.checkWeb(ctx, ready, prev, changed || created, next)
	return next
}

// ensureWebNetwork makes sure mcpanel-web's own network exists (IPv4, one subnet from the
// pool) and the egress filter covers it, like the server networks (fail closed).
func (m *Manager) ensureWebNetwork(ctx context.Context) (docker.Network, error) {
	m.netMu.Lock()
	defer m.netMu.Unlock()
	nets, err := m.dc.ListNetworks(ctx, "")
	if err != nil {
		return docker.Network{}, err
	}
	var n docker.Network
	if i := slices.IndexFunc(nets, func(n docker.Network) bool { return n.Name == WebNetwork }); i >= 0 {
		n = nets[i]
		if !isWebNetwork(n) {
			return docker.Network{}, fmt.Errorf("docker: network %s exists but is not managed by mc-panel; leaving it untouched", WebNetwork)
		}
	} else {
		if n, err = m.createPoolNetwork(ctx, WebNetwork, webNetworkLabels(), nets); err != nil {
			return docker.Network{}, err
		}
		m.log.Info("web router network created", "network", WebNetwork, "subnet", n.Subnets[0].String())
	}
	if n.HasIPv6() || len(n.Subnets) != 1 {
		return docker.Network{}, fmt.Errorf("docker: network %s must have exactly one IPv4 subnet and no IPv6 (remove it: docker network rm %s)", WebNetwork, WebNetwork)
	}
	if err := m.syncEgress(ctx); err != nil {
		return docker.Network{}, fmt.Errorf("container egress filter: %w", err)
	}
	if !m.egressCovers(n.Subnets) {
		return docker.Network{}, fmt.Errorf("container egress filter: network %s (%v) is not covered", WebNetwork, n.Subnets)
	}
	return n, nil
}

// webTargets checks the server container and network of every route; a route that is not
// ready is failed with fail. It also returns the server networks by instance ID. The error
// is for the whole round (Docker unreachable).
func (m *Manager) webTargets(ctx context.Context, desired []agentapi.WebRoute, fail func(agentapi.WebRoute, string)) ([]webTarget, map[int64]docker.Network, error) {
	_, byID, err := m.managedNetworks(ctx)
	if err != nil {
		return nil, nil, err
	}
	var targets []webTarget
	for _, r := range desired {
		info, err := m.dc.Inspect(ctx, ContainerName(r.ID))
		if err != nil && !docker.IsNotFound(err) {
			return nil, nil, err
		}
		if err != nil || checkManaged(info, r.ID) != nil {
			fail(r, webErrContainer)
			continue
		}
		n, ok := byID[r.ID]
		if !ok {
			// The F1 repair: the network of an existing server is created again, with the
			// egress filter covering it before anything is connected.
			if n, err = m.ensureInstanceNetwork(ctx, r.ID); err != nil {
				m.log.Warn("web route: server network not ready", "id", r.ID, "err", sanitizeError(err))
				fail(r, webErrNetwork)
				continue
			}
			byID[r.ID] = n
		}
		if n.HasIPv6() || len(n.Subnets) != 1 || !m.egressCovers(n.Subnets) {
			fail(r, webErrNetwork)
			continue
		}
		addr, ok := WebAddress(n.Subnets[0])
		if !ok {
			fail(r, webErrNetwork)
			continue
		}
		targets = append(targets, webTarget{route: r, network: n.Name,
			rule: agentapi.WebRule{Source: addr, Dest: n.Subnets[0].Masked(), Port: uint16(r.Port)}}) //nolint:gosec // G115: Validate keeps the port in 1024..65535
	}
	return targets, byID, nil
}

// disconnectWeb takes mcpanel-web off every server network it is on (mc-panel's own
// mcpanel-net-<id>, byID) that has no target; networks it does not manage are never touched.
// Unless force, a busy server (recreate in progress) keeps it until the next round. It
// returns the server networks mcpanel-web stays on without a target.
func (m *Manager) disconnectWeb(ctx context.Context, ns docker.NetworkSettings, byID map[int64]docker.Network, targets []webTarget, force bool) []docker.Network {
	var stay []docker.Network
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		n := byID[id]
		if !ns.OnNetwork(n.Name) || slices.ContainsFunc(targets, func(t webTarget) bool { return t.network == n.Name }) {
			continue
		}
		if !force && m.busy(id) {
			stay = append(stay, n)
			continue
		}
		if err := m.dc.NetworkDisconnect(ctx, n.Name, WebName); err != nil {
			m.log.Warn("web router not disconnected", "network", n.Name, "err", sanitizeError(err))
			stay = append(stay, n)
			continue
		}
		m.log.Info("web router disconnected", "network", n.Name)
	}
	return stay
}

// connectWeb connects mcpanel-web to the target's network with the rule's address; an
// endpoint with another address is replaced.
func (m *Manager) connectWeb(ctx context.Context, ns docker.NetworkSettings, t webTarget) error {
	if ep, ok := ns.Networks[t.network]; ok {
		if ep.IPAddress == t.rule.Source.String() {
			return nil
		}
		if err := m.dc.NetworkDisconnect(ctx, t.network, WebName); err != nil {
			return err
		}
	}
	if err := m.dc.NetworkConnectIP(ctx, t.network, WebName, t.rule.Source); err != nil {
		return err
	}
	m.log.Info("web router connected", "network", t.network, "address", t.rule.Source.String())
	return nil
}

// ensureWebContainer makes sure mcpanel-web exists with the current template and pinned
// image and is running; info is its inspect result when exists (label already checked). It
// reports whether it created or started the container.
func (m *Manager) ensureWebContainer(ctx context.Context, info docker.ContainerInfo, exists bool) (bool, error) {
	cfg, err := WebContainer(m.host)
	if err != nil {
		return false, err
	}
	if m.customize != nil {
		m.customize(WebName, &cfg)
	}
	template, err := templateHash(cfg)
	if err != nil {
		return false, err
	}
	if cfg.Labels == nil {
		cfg.Labels = map[string]string{}
	}
	cfg.Labels[LabelWebTemplate] = template
	if exists && (info.Config.Labels[LabelWebTemplate] != template || !sameImage(info.Config.Image, cfg.Image)) {
		m.log.Info("replacing web router container", "old_image", info.Config.Image, "new_image", cfg.Image,
			"old_template", info.Config.Labels[LabelWebTemplate], "new_template", template)
		if err := m.ensureImage(ctx, -1, cfg.Image); err != nil {
			return false, err
		}
		if err := m.dc.Remove(ctx, WebName, true); err != nil && !docker.IsNotFound(err) {
			return false, err
		}
		exists = false
	}
	if !exists {
		if err := m.ensureImage(ctx, -1, cfg.Image); err != nil {
			return false, err
		}
		if _, err := m.dc.CreateContainer(ctx, WebName, cfg); err != nil {
			return false, err
		}
		m.log.Info("web router container created", "image", cfg.Image)
		return true, m.dc.Start(ctx, WebName)
	}
	if !info.State.Running {
		return true, m.dc.Start(ctx, WebName)
	}
	return false, nil
}

// checkWeb asks the ask endpoint for every ready target: a confirmation makes it active;
// otherwise it stays pending and turns into an error once it has been unconfirmed for
// webConfirmWindow. restarted (new config or container) starts that window again. After the
// first unreachable ask endpoint the remaining targets are not asked in this round (a
// stopped router would cost webAskTimeout per route).
func (m *Manager) checkWeb(ctx context.Context, ready []webTarget, prev map[int64]webRouteState, restarted bool, next map[int64]webRouteState) {
	now := m.clock.Now()
	reachable := true
	for _, t := range ready {
		ok := false
		if reachable {
			var err error
			if ok, err = m.webAsk(ctx, t.route.FQDN); err != nil {
				m.log.Debug("web router ask endpoint not reachable", "err", sanitizeError(err))
				reachable = false
			}
		}
		st := webRouteState{route: t.route}
		switch p, had := prev[t.route.ID]; {
		case ok:
		case had && p.route == t.route && !restarted && !p.unconfirmed.IsZero():
			st.unconfirmed = p.unconfirmed
		default:
			st.unconfirmed = now
		}
		switch {
		case ok:
			st.status = agentapi.WebRouteStatus{ID: t.route.ID, Status: agentapi.WebActive}
		case now.Sub(st.unconfirmed) >= webConfirmWindow:
			st.status = agentapi.WebRouteStatus{ID: t.route.ID, Status: agentapi.WebError, Error: webErrNotLoaded}
		default:
			st.status = agentapi.WebRouteStatus{ID: t.route.ID, Status: agentapi.WebPending}
		}
		next[t.route.ID] = st
	}
}

// askWeb is the default ask check. It runs wget inside mcpanel-web against Caddy's ask server
// (argv, no shell, as Caddy's own user), so the agent itself opens no network socket (its
// unit allows only AF_UNIX and AF_NETLINK). Exit 0 confirms the name; busybox wget exits 1
// on every failure, so only a "404" answer counts as "not served", anything else (no
// listener, exec or Docker error) as unreachable.
func (m *Manager) askWeb(ctx context.Context, fqdn string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, webAskTimeout)
	defer cancel()
	execID, err := m.dc.ExecCreate(ctx, WebName, docker.ExecConfig{
		Cmd: []string{"wget", "-q", "-O", "/dev/null", "-T", webAskWget,
			"http://127.0.0.1:" + portNumber(webAskPort) + "/?domain=" + url.QueryEscape(fqdn)},
		User:         webUser,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return false, err
	}
	body, err := m.dc.ExecStart(ctx, execID)
	if err != nil {
		return false, err
	}
	out, _, err := readExecOutput(body, webAskOutputBytes)
	_ = body.Close()
	if err != nil {
		return false, err
	}
	code, err := m.execExitCode(ctx, execID)
	switch {
	case err != nil:
		return false, err
	case code == 0:
		return true, nil
	case code == 1 && strings.Contains(out, " 404 "):
		return false, nil
	default:
		return false, fmt.Errorf("ask endpoint: wget exited with code %d: %s", code, firstRunes(out, maxRconErrorRunes))
	}
}
