package instances

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

const (
	// SampleInterval is the period of the background loop (Docker check, router, stats).
	SampleInterval = 10 * time.Second

	// DefaultStatusTimeout bounds Status below the panel's agent client timeout
	// (agent.DefaultTimeout, 3 s), so a hanging Docker call gives the panel
	// docker "unavailable" instead of a timeout.
	DefaultStatusTimeout = 2 * time.Second

	// pingTimeout bounds the Docker check of CheckDocker and of every queued write
	// (503 docker_unavailable), also below the panel's 3 s client timeout.
	pingTimeout = 2 * time.Second
)

// Config wires a Manager. Docker, Clock and Log are required.
type Config struct {
	Docker       *docker.Client
	InstancesDir string
	UID, GID     int
	Images       config.Images
	Clock        clock.Clock
	Log          *slog.Logger
	// Customize may adjust a container configuration right before it is created.
	// Only the dockerintegration test sets it (small test image); nil in production.
	Customize func(name string, cfg *docker.ContainerConfig)
	// StatusTimeout bounds Status (GET /v1/instances); 0 means DefaultStatusTimeout.
	StatusTimeout time.Duration
	// RconCommand is the program run for console commands; "" means DefaultRconCommand.
	// Only the dockerintegration test changes it (busybox has no rcon-cli).
	RconCommand string
	// RconTimeout bounds one console command; 0 means RconTimeout.
	RconTimeout time.Duration
	// Egress is the container egress filter, given the subnets of the mcpanel-mc
	// network whenever they change; nil = none (tests).
	Egress EgressFilter
	// NanoCPUs caps every instance container (cpu_per_instance); 0 = no cap.
	NanoCPUs int64
	// RouterConnectionRateLimit is mc-router's -connection-rate-limit; 0 = default.
	RouterConnectionRateLimit int
}

// EgressFilter is the firewall part that filters the containers' new connections
// (firewall.Backend).
type EgressFilter interface {
	SetEgressSubnets(ctx context.Context, subnets []netip.Prefix) error
}

type sample struct {
	cpuTotal, systemTotal uint64
	memory, limit         int64
	cpuPercent            float64
}

// Manager executes instance operations asynchronously, one serial queue per
// instance, and reports the actual state read from Docker. It keeps no
// persistent state: managed containers are found via their labels.
type Manager struct {
	dc        *docker.Client
	host      Host
	clock     clock.Clock
	log       *slog.Logger
	customize func(string, *docker.ContainerConfig)

	ctx    context.Context // cancelled by Close; bounds all queued work
	cancel context.CancelFunc

	statusTimeout time.Duration
	rconCmd       string
	rconTimeout   time.Duration
	console       consoleState

	egress   EgressFilter
	egressMu sync.Mutex // serialises egress syncs
	// egressApplied are the subnets the filter was last given (nil: not yet since the
	// agent started). Guarded by egressMu.
	egressApplied []netip.Prefix
	egressErr     string // last sync error, logged on change; guarded by egressMu

	routerMu sync.Mutex // serialises routes file + router container changes
	// lastApplied is the routes file content the running router has loaded (nil:
	// unknown, e.g. after an agent restart). Guarded by routerMu.
	lastApplied []byte

	mu        sync.Mutex
	idle      *sync.Cond
	active    int // running workers
	closed    bool
	inst      map[int64]*instanceState
	stats     map[int64]sample
	dockerOK  bool
	routerErr string
	errSeq    uint64 // orders error-only tracker entries for eviction
}

func New(cfg Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		dc: cfg.Docker,
		host: Host{InstancesDir: cfg.InstancesDir, UID: cfg.UID, GID: cfg.GID, Images: cfg.Images,
			NanoCPUs: cfg.NanoCPUs, RouterConnectionRateLimit: cfg.RouterConnectionRateLimit},
		egress:        cfg.Egress,
		clock:         cfg.Clock,
		log:           cfg.Log,
		customize:     cfg.Customize,
		ctx:           ctx,
		statusTimeout: cmp.Or(cfg.StatusTimeout, DefaultStatusTimeout),
		rconCmd:       cmp.Or(cfg.RconCommand, DefaultRconCommand),
		rconTimeout:   cmp.Or(cfg.RconTimeout, RconTimeout),
		cancel:        cancel,
		inst:          map[int64]*instanceState{},
		stats:         map[int64]sample{},
	}
	m.idle = sync.NewCond(&m.mu)
	return m
}

// Ensure queues an ensure operation. It returns instancespec.ErrInvalid,
// agentapi.ErrBusy or agentapi.ErrDockerUnavailable without queueing anything.
func (m *Manager) Ensure(ctx context.Context, req agentapi.EnsureRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	return m.enqueue(ctx, req.ID, job{kind: jobEnsure, ensure: req})
}

// Power queues start, stop or restart of an existing container.
func (m *Manager) Power(ctx context.Context, id int64, action agentapi.PowerAction) error {
	if id <= 0 || !agentapi.ValidPowerAction(action) {
		return fmt.Errorf("%w: power: id must be > 0 and action start, stop or restart", instancespec.ErrInvalid)
	}
	return m.enqueue(ctx, id, job{kind: jobPower, action: action})
}

// Remove queues stop + removal of the container and archiving of its data.
func (m *Manager) Remove(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: remove: id must be > 0", instancespec.ErrInvalid)
	}
	return m.enqueue(ctx, id, job{kind: jobRemove})
}

// checkManaged refuses containers that only share the name: mc-panel never
// stops, removes or replaces a container without its own mcpanel.instance label.
func checkManaged(info docker.ContainerInfo, id int64) error {
	if got, ok := managedID(info.Config.Labels, info.Name); !ok || got != id {
		return fmt.Errorf("container %s exists but is not managed by mc-panel (label %s=%d missing); leaving it untouched",
			ContainerName(id), LabelInstance, id)
	}
	return nil
}

// checkOwner refuses a managed container of another server. Server IDs can be
// reused after a delete; a leftover mcpanel-mc-<id> whose name label differs from
// the server the panel now means (want; "" = unknown) is never adopted, started,
// stopped or recreated. The FQDN is not compared: it changes with the base domain
// while the (immutable) name stays.
func checkOwner(info docker.ContainerInfo, id int64, want string) error {
	have := info.Config.Labels[LabelName]
	if want == "" || have == want {
		return nil
	}
	if !instancespec.ValidName(have) {
		have = strconv.Quote(have)
	}
	return fmt.Errorf("container %s belongs to another server (%s); remove it first", ContainerName(id), have)
}

// expectedName is the server name the last ensure for id named ("" if unknown).
func (m *Manager) expectedName(id int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.inst[id]; st != nil {
		return st.name
	}
	return ""
}

func labelRevision(labels map[string]string) int64 {
	r, err := strconv.ParseInt(labels[LabelRevision], 10, 64)
	if err != nil {
		return 0
	}
	return r
}

func (m *Manager) ensure(ctx context.Context, req agentapi.EnsureRequest) error {
	cfg, err := InstanceContainer(m.host, req)
	if err != nil {
		return err
	}
	name := ContainerName(req.ID)
	if m.customize != nil {
		m.customize(name, &cfg)
	}
	info, err := m.dc.Inspect(ctx, name)
	exists := err == nil
	if err != nil && !docker.IsNotFound(err) {
		return err
	}
	if exists {
		if err := checkManaged(info, req.ID); err != nil {
			return err
		}
		if err := checkOwner(info, req.ID, req.Name); err != nil {
			return err
		}
		// Never downgrade: an older revision (a stale or replayed request) is a no-op,
		// even with recreate.
		if have := labelRevision(info.Config.Labels); have >= req.Revision || !req.Recreate {
			if have > req.Revision {
				m.log.Info("ensure with an older revision ignored", "id", req.ID, "revision", req.Revision, "container_revision", have)
			}
			m.refreshRouterAfter(ctx)
			return nil
		}
	}
	// Pull before touching the old container: keeps the downtime of a recreate short.
	if err := m.ensureImage(ctx, req.ID, cfg.Image); err != nil {
		return err
	}
	if err := m.ensureDataDir(req.ID); err != nil {
		return err
	}
	if err := m.ensureNetwork(ctx); err != nil {
		return err
	}
	wasRunning := false
	if exists {
		wasRunning = info.State.Running || info.State.Restarting
		m.setOp(req.ID, agentapi.OpStopping)
		if err := m.dc.Stop(ctx, name, StopTimeoutSeconds); err != nil && !docker.IsNotFound(err) {
			return err
		}
		m.setOp(req.ID, agentapi.OpRemoving)
		if err := m.dc.Remove(ctx, name, true); err != nil && !docker.IsNotFound(err) {
			return err
		}
	}
	m.setOp(req.ID, agentapi.OpCreating)
	if _, err := m.dc.CreateContainer(ctx, name, cfg); err != nil {
		return err
	}
	if wasRunning {
		m.setOp(req.ID, agentapi.OpStarting)
		if err := m.dc.Start(ctx, name); err != nil {
			return err
		}
	}
	m.refreshRouterAfter(ctx)
	return nil
}

// ensureImage pulls ref if it is missing; id < 0 means "no instance operation to report".
func (m *Manager) ensureImage(ctx context.Context, id int64, ref string) error {
	ok, err := m.dc.ImageExists(ctx, ref)
	if err != nil || ok {
		return err
	}
	m.setOp(id, agentapi.OpPulling)
	defer m.setOp(id, agentapi.OpCreating)
	m.log.Info("pulling image", "image", ref)
	return m.dc.PullImage(ctx, ref)
}

func (m *Manager) power(ctx context.Context, id int64, action agentapi.PowerAction) error {
	name := ContainerName(id)
	info, err := m.dc.Inspect(ctx, name)
	if docker.IsNotFound(err) {
		return fmt.Errorf("container %s does not exist", name)
	}
	if err != nil {
		return err
	}
	if err := checkManaged(info, id); err != nil {
		return err
	}
	if err := checkOwner(info, id, m.expectedName(id)); err != nil {
		return err
	}
	switch action {
	case agentapi.PowerStart:
		m.setOp(id, agentapi.OpStarting)
		return m.dc.Start(ctx, name)
	case agentapi.PowerStop:
		m.setOp(id, agentapi.OpStopping)
		return m.dc.Stop(ctx, name, StopTimeoutSeconds)
	default: // restart: stop and start separately so the reported operation is accurate
		m.setOp(id, agentapi.OpStopping)
		if err := m.dc.Stop(ctx, name, StopTimeoutSeconds); err != nil {
			return err
		}
		m.setOp(id, agentapi.OpStarting)
		return m.dc.Start(ctx, name)
	}
}

func (m *Manager) remove(ctx context.Context, id int64) error {
	name := ContainerName(id)
	info, err := m.dc.Inspect(ctx, name)
	exists := err == nil
	if err != nil && !docker.IsNotFound(err) {
		return err
	}
	m.mu.Lock()
	instName := ""
	if st := m.inst[id]; st != nil {
		instName = st.name
	}
	m.mu.Unlock()
	if exists {
		if err := checkManaged(info, id); err != nil {
			return err
		}
	}
	if err := m.ensureLayout(); err != nil {
		return err
	}
	if exists {
		if n := info.Config.Labels[LabelName]; instancespec.ValidName(n) {
			instName = n
		}
		m.setOp(id, agentapi.OpStopping)
		if err := m.dc.Stop(ctx, name, StopTimeoutSeconds); err != nil && !docker.IsNotFound(err) {
			return err
		}
		m.setOp(id, agentapi.OpRemoving)
		if err := m.dc.Remove(ctx, name, true); err != nil && !docker.IsNotFound(err) {
			return err
		}
	}
	m.setOp(id, agentapi.OpRemoving)
	if err := m.archiveData(id, instName); err != nil {
		return err
	}
	m.refreshRouterAfter(ctx)
	return nil
}

// refreshRouterAfter updates routes and router after an instance operation. Router
// problems are reported in RouterStatus, not as the instance's last_error.
func (m *Manager) refreshRouterAfter(ctx context.Context) {
	_ = m.RefreshRouter(ctx)
}

// RefreshRouter rewrites the routes file from the managed containers, makes sure
// the router container exists, runs the pinned image and is started, and sends
// SIGHUP when the routes changed under a running router.
func (m *Manager) RefreshRouter(ctx context.Context) error {
	m.routerMu.Lock()
	err := m.refreshRouter(ctx)
	m.routerMu.Unlock()

	msg := ""
	if err != nil {
		msg = sanitizeError(err)
		if errors.Is(err, docker.ErrUnavailable) {
			m.setDocker(false, err)
		}
	}
	m.mu.Lock()
	changed := m.routerErr != msg
	m.routerErr = msg
	m.mu.Unlock()
	if changed && err != nil {
		m.log.Warn("router not ready", "err", msg)
	} else if changed {
		m.log.Info("router ready")
	}
	return err
}

func (m *Manager) refreshRouter(ctx context.Context) error {
	list, err := m.dc.List(ctx, LabelInstance)
	if err != nil {
		return err
	}
	data, err := RenderRoutes(Routes(list))
	if err != nil {
		return err
	}
	// Always write the file before the router container exists: without the watch
	// flag a missing file is silently skipped at start.
	if _, err := WriteRoutes(RouterDir(m.host.InstancesDir), data); err != nil {
		return err
	}
	cfg, err := RouterContainer(m.host)
	if err != nil {
		return err
	}
	if m.customize != nil {
		m.customize(RouterName, &cfg)
	}
	template, err := templateHash(cfg)
	if err != nil {
		return err
	}
	if cfg.Labels == nil {
		cfg.Labels = map[string]string{}
	}
	cfg.Labels[LabelRouterTemplate] = template
	info, err := m.dc.Inspect(ctx, RouterName)
	switch {
	case docker.IsNotFound(err):
		return m.createRouter(ctx, cfg, data)
	case err != nil:
		return err
	}
	if info.Config.Labels[LabelRouter] != "1" {
		return fmt.Errorf("container %s exists but is not managed by mc-panel (label %s=1 missing); leaving it untouched",
			RouterName, LabelRouter)
	}
	// Replace the router after an agent upgrade with a new pinned image or template.
	if info.Config.Labels[LabelRouterTemplate] != template || !sameImage(info.Config.Image, cfg.Image) {
		m.log.Info("replacing router container", "old_image", info.Config.Image, "new_image", cfg.Image,
			"old_template", info.Config.Labels[LabelRouterTemplate], "new_template", template)
		if err := m.ensureImage(ctx, -1, cfg.Image); err != nil {
			return err
		}
		if err := m.dc.Remove(ctx, RouterName, true); err != nil && !docker.IsNotFound(err) {
			return err
		}
		return m.createRouter(ctx, cfg, data)
	}
	if !info.State.Running {
		if err := m.dc.Start(ctx, RouterName); err != nil { // a fresh start reads the current file
			return err
		}
		m.lastApplied = data
		return nil
	}
	// Reload until the running router has seen this content: a failed SIGHUP is
	// retried in the next round even though the file no longer changes.
	if !bytes.Equal(data, m.lastApplied) {
		if err := m.dc.Kill(ctx, RouterName, "SIGHUP"); err != nil {
			if docker.IsConflict(err) { // stopped meanwhile; the next round starts it
				return nil
			}
			return err
		}
		m.lastApplied = data
		m.log.Info("router reloaded", "bytes", len(data))
	}
	return nil
}

// createRouter creates and starts the router; data is the routes file it will read.
func (m *Manager) createRouter(ctx context.Context, cfg docker.ContainerConfig, data []byte) error {
	if err := m.ensureImage(ctx, -1, cfg.Image); err != nil {
		return err
	}
	if err := m.ensureNetwork(ctx); err != nil {
		return err
	}
	if _, err := m.dc.CreateContainer(ctx, RouterName, cfg); err != nil {
		return err
	}
	m.log.Info("router container created", "image", cfg.Image)
	if err := m.dc.Start(ctx, RouterName); err != nil {
		return err
	}
	m.lastApplied = data
	return nil
}

// networkLabels mark the mcpanel-mc network as mc-panel's own.
func networkLabels() map[string]string { return map[string]string{LabelNetwork: "1"} }

// ensureNetwork creates the mcpanel-mc network if needed and applies the egress
// filter to it. It fails closed: no container is created on a network the filter
// does not cover.
func (m *Manager) ensureNetwork(ctx context.Context) error {
	if err := m.dc.EnsureNetwork(ctx, NetworkName, networkLabels()); err != nil {
		return err
	}
	if err := m.syncEgress(ctx); err != nil {
		return fmt.Errorf("container egress filter: %w", err)
	}
	return nil
}

// syncEgress hands the current subnets of the mcpanel-mc network to the egress
// filter when they differ from the last ones applied (and once after the agent
// started). A missing network has nothing to filter yet.
func (m *Manager) syncEgress(ctx context.Context) error {
	if m.egress == nil {
		return nil
	}
	m.egressMu.Lock()
	defer m.egressMu.Unlock()
	subnets, err := m.dc.NetworkSubnets(ctx, NetworkName, networkLabels())
	if docker.IsNotFound(err) {
		return nil
	}
	if err == nil && (m.egressApplied == nil || !slices.Equal(subnets, m.egressApplied)) {
		if err = m.egress.SetEgressSubnets(ctx, subnets); err == nil {
			m.egressApplied = slices.Clone(subnets)
			if m.egressApplied == nil {
				m.egressApplied = []netip.Prefix{}
			}
			m.log.Info("container egress filter applied", "network", NetworkName, "subnets", fmt.Sprint(subnets))
		}
	}
	msg := ""
	if err != nil {
		msg = sanitizeError(err)
	}
	if msg != m.egressErr && msg != "" {
		m.log.Warn("container egress filter not applied", "err", msg)
	}
	m.egressErr = msg
	return err
}

// templateHash is the SHA-256 of the router template's canonical JSON
// (encoding/json: fixed field order, sorted map keys).
func templateHash(cfg docker.ContainerConfig) (string, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// sameImage compares two image references after Docker's normalisation
// ("docker.io/", "library/"). A reference that does not parse (e.g. an image ID)
// is not a reason to replace anything: the template hash covers the image as well.
func sameImage(have, want string) bool {
	h, err := docker.ParseImageRef(have)
	if err != nil {
		return true
	}
	w, err := docker.ParseImageRef(want)
	if err != nil {
		return true
	}
	return h.Digest == w.Digest && normalizeRepo(h.Repo) == normalizeRepo(w.Repo)
}

func normalizeRepo(repo string) string {
	for _, p := range []string{"docker.io/", "index.docker.io/", "registry-1.docker.io/"} {
		if r, ok := strings.CutPrefix(repo, p); ok {
			repo = r
			break
		}
	}
	return strings.TrimPrefix(repo, "library/")
}

// dockerAvailable uses the cached state and re-checks right away when it is bad,
// so writes succeed as soon as Docker is back.
func (m *Manager) dockerAvailable(ctx context.Context) bool {
	m.mu.Lock()
	ok := m.dockerOK
	m.mu.Unlock()
	return ok || m.CheckDocker(ctx)
}

// CheckDocker pings Docker and updates the cached state.
func (m *Manager) CheckDocker(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	err := m.dc.Ping(ctx)
	m.setDocker(err == nil, err)
	return err == nil
}

func (m *Manager) setDocker(ok bool, err error) {
	m.mu.Lock()
	changed := m.dockerOK != ok
	m.dockerOK = ok
	m.mu.Unlock()
	switch {
	case changed && ok:
		m.log.Info("docker available")
	case changed:
		m.log.Warn("docker unavailable", "err", err)
	}
}

// DockerStatus returns the cached state: agentapi.DockerOK or agentapi.DockerUnavailable.
func (m *Manager) DockerStatus() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dockerOK {
		return agentapi.DockerOK
	}
	return agentapi.DockerUnavailable
}

func mapState(dockerStatus string) agentapi.ContainerState {
	switch dockerStatus {
	case "running", "paused":
		return agentapi.StateRunning
	case "restarting":
		return agentapi.StateRestarting
	case "created":
		return agentapi.StateCreated
	default: // exited, dead, removing
		return agentapi.StateExited
	}
}

func startedAtMillis(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() <= 1 {
		return 0
	}
	return t.UnixMilli()
}

// Status reads the actual state from Docker and merges the worker state and the
// cached stats. Instances with queued work or a last_error but no container are
// reported with state "missing".
func (m *Manager) Status(ctx context.Context) agentapi.InstancesResponse {
	ctx, cancel := context.WithTimeout(ctx, m.statusTimeout)
	defer cancel()
	resp := agentapi.InstancesResponse{
		Docker:    agentapi.DockerUnavailable,
		Instances: []agentapi.InstanceStatus{},
		Router:    agentapi.RouterStatus{State: agentapi.StateMissing},
	}
	list, listErr := m.dc.List(ctx, LabelInstance)

	m.mu.Lock()
	work := make(map[int64]instanceState, len(m.inst))
	for id, st := range m.inst {
		op := st.op
		if op == agentapi.OpNone && len(st.pending) > 0 { // between two jobs: work is still queued
			op = initialOp(st.pending[0])
		}
		work[id] = instanceState{op: op, lastError: st.lastError, name: st.name}
	}
	stats := make(map[int64]sample, len(m.stats))
	for id, s := range m.stats {
		stats[id] = s
	}
	resp.Router.LastError = m.routerErr
	m.mu.Unlock()

	if listErr != nil {
		m.setDocker(false, listErr)
		return resp
	}
	m.setDocker(true, nil)
	resp.Docker = agentapi.DockerOK

	seen := map[int64]bool{}
	for _, c := range list {
		id, ok := managedID(c.Labels, c.Names...)
		if !ok || seen[id] {
			continue
		}
		st := agentapi.InstanceStatus{
			ID: id, Name: c.Labels[LabelName], Revision: labelRevision(c.Labels), State: mapState(c.State),
		}
		info, err := m.dc.Inspect(ctx, ContainerName(id))
		switch {
		case docker.IsNotFound(err):
			continue // removed since the listing
		case err == nil:
			st.State = mapState(info.State.Status)
			st.ExitCode = info.State.ExitCode
			st.RestartCount = info.RestartCount
			if st.State == agentapi.StateRunning || st.State == agentapi.StateRestarting {
				st.StartedAt = startedAtMillis(info.State.StartedAt)
			}
		default:
			m.log.Debug("inspect failed while listing", "id", id, "err", err)
		}
		if s, ok := stats[id]; ok && st.State == agentapi.StateRunning {
			st.MemoryBytes, st.MemoryLimitBytes, st.CPUPercent = s.memory, s.limit, s.cpuPercent
		}
		if w, ok := work[id]; ok {
			st.Operation, st.LastError = w.op, w.lastError
		}
		seen[id] = true
		resp.Instances = append(resp.Instances, st)
	}
	for id, w := range work {
		if !seen[id] {
			resp.Instances = append(resp.Instances, agentapi.InstanceStatus{
				ID: id, Name: w.name, State: agentapi.StateMissing, Operation: w.op, LastError: w.lastError,
			})
		}
	}
	slices.SortFunc(resp.Instances, func(a, b agentapi.InstanceStatus) int { return cmp.Compare(a.ID, b.ID) })

	if info, err := m.dc.Inspect(ctx, RouterName); err == nil && info.Config.Labels[LabelRouter] == "1" {
		resp.Router.State = mapState(info.State.Status)
	}
	// A Docker call ran into the deadline: the data may be partial, so report
	// Docker as unavailable rather than a half-filled answer.
	if err := ctx.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			m.setDocker(false, fmt.Errorf("instance status: %w", err))
		}
		return agentapi.InstancesResponse{
			Docker:    agentapi.DockerUnavailable,
			Instances: []agentapi.InstanceStatus{},
			Router:    agentapi.RouterStatus{State: agentapi.StateMissing, LastError: resp.Router.LastError},
		}
	}
	return resp
}

// SampleStats takes one stats snapshot of every running managed container and
// computes CPU% from the previous snapshot (one-shot stats carry no precpu).
func (m *Manager) SampleStats(ctx context.Context) {
	list, err := m.dc.List(ctx, LabelInstance)
	if err != nil {
		m.setDocker(false, err)
		return
	}
	m.mu.Lock()
	prev := m.stats
	m.mu.Unlock()
	next := map[int64]sample{}
	for _, c := range list {
		id, ok := managedID(c.Labels, c.Names...)
		if !ok || c.State != "running" {
			continue
		}
		s, err := m.dc.Stats(ctx, c.ID)
		if err != nil {
			m.log.Debug("stats failed", "id", id, "err", err)
			continue
		}
		cur := sample{
			cpuTotal:    s.CPUStats.CPUUsage.TotalUsage,
			systemTotal: s.CPUStats.SystemCPUUsage,
			memory:      memoryUsed(s.MemoryStats),
			limit:       int64(s.MemoryStats.Limit),
		}
		if p, ok := prev[id]; ok {
			cur.cpuPercent = cpuPercent(p, cur, s.CPUStats.OnlineCPUs)
		}
		next[id] = cur
	}
	m.mu.Lock()
	m.stats = next
	m.mu.Unlock()
}

// memoryUsed follows the Docker CLI: usage minus page cache (inactive_file on cgroup v2, cache on v1).
func memoryUsed(s docker.MemoryStats) int64 {
	used := s.Usage
	if v, ok := s.Stats["inactive_file"]; ok && v <= used {
		used -= v
	} else if v, ok := s.Stats["cache"]; ok && v <= used {
		used -= v
	}
	return int64(used)
}

// cpuPercent is the Docker API formula: Δcontainer / Δsystem × online CPUs × 100.
func cpuPercent(prev, cur sample, onlineCPUs uint32) float64 {
	if cur.cpuTotal < prev.cpuTotal || cur.systemTotal <= prev.systemTotal {
		return 0
	}
	cpus := float64(max(onlineCPUs, 1))
	return float64(cur.cpuTotal-prev.cpuTotal) / float64(cur.systemTotal-prev.systemTotal) * cpus * 100
}

// Tick is one round of the background loop: Docker check, routes and router, stats.
func (m *Manager) Tick(ctx context.Context) {
	if !m.CheckDocker(ctx) {
		return
	}
	_ = m.RefreshRouter(ctx)
	_ = m.syncEgress(ctx) // after an agent restart or a recreated network; logged inside
	m.SampleStats(ctx)
}

// Run calls Tick at start and every SampleInterval until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	m.Tick(ctx)
	t := time.NewTicker(SampleInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Tick(ctx)
		}
	}
}
