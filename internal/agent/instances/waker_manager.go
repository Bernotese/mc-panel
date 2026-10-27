package instances

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	// wakerTimeout bounds the Docker work of one PUT /v1/waker, below the panel's 3 s
	// client timeout. The waker's image is the router's, which the background loop pulls;
	// a waker call never pulls.
	wakerTimeout = 2 * time.Second
	// wakerLogLimit bounds the waker log read per call; the rest follows in the next call.
	wakerLogLimit = 64 << 10
)

// wakeLineRe is the only line of the waker log the agent acts on.
var wakeLineRe = regexp.MustCompile(`^wake ([a-z0-9.-]{1,253})$`)

// wakerSupported: the bind-mounted agent binary must be a Linux binary.
var wakerSupported = runtime.GOOS == "linux"

type wakerState struct {
	binary, version string

	mu sync.Mutex // serialises SetWaker; guards the fields up to setMu
	// wanted: a non-empty list was received since the agent started; from then on the
	// container is kept up.
	wanted bool
	// since is the log timestamp of the last waker log entry read (zero: no read yet; the
	// first call starts at the current time, so wakes from before an agent start are dropped).
	since time.Time

	setMu sync.Mutex // guards sleeping; never held across Docker calls
	// sleeping maps the FQDN of every listed dev instance whose container matches to its id;
	// nil until the first PUT /v1/waker since the agent started (routes as without a waker).
	sleeping map[string]int64
}

// sleepingSet returns a copy of the stored set (nil before the first PUT /v1/waker).
func (m *Manager) sleepingSet() map[string]int64 {
	m.waker.setMu.Lock()
	defer m.waker.setMu.Unlock()
	return maps.Clone(m.waker.sleeping)
}

// SetWaker implements agent.Waker (PUT /v1/waker). It keeps only the listed devs whose
// managed container exists with that id and FQDN, stores them as the sleeping set, keeps
// the waker container up once a non-empty set was received, updates the router when the
// routes changed and returns the ids of sleeping devs the waker logged a login for since
// the previous call.
func (m *Manager) SetWaker(ctx context.Context, req agentapi.WakerRequest) (agentapi.WakerResponse, error) {
	if err := req.Validate(); err != nil {
		return agentapi.WakerResponse{}, err
	}
	if !m.dockerAvailable(ctx) {
		return agentapi.WakerResponse{}, agentapi.ErrDockerUnavailable
	}
	m.waker.mu.Lock()
	defer m.waker.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, wakerTimeout)
	defer cancel()

	list, err := m.dc.List(ctx, LabelInstance)
	if err != nil {
		if errors.Is(err, docker.ErrUnavailable) {
			m.setDocker(false, err)
			return agentapi.WakerResponse{}, fmt.Errorf("%w: %w", agentapi.ErrDockerUnavailable, err)
		}
		return agentapi.WakerResponse{}, err
	}
	set := matchSleeping(list, req.Sleeping)
	m.waker.setMu.Lock()
	m.waker.sleeping = set
	m.waker.setMu.Unlock()
	if len(set) > 0 {
		m.waker.wanted = true
	}

	resp := agentapi.WakerResponse{Wakes: []int64{}}
	exists := false
	if m.waker.wanted && wakerSupported {
		var running bool
		exists, running, err = m.ensureWaker(ctx)
		if err != nil {
			m.log.Warn("waker not ready", "err", sanitizeError(err))
		}
		resp.Ready = running && err == nil
	}
	m.refreshRouterForWaker(ctx, list, set)

	if m.waker.since.IsZero() {
		m.waker.since = m.clock.Now()
	} else if exists {
		resp.Wakes = m.readWakes(ctx, set)
	}
	return resp, nil
}

// matchSleeping maps the FQDN of every target to its id if a managed container of that id
// carries exactly that FQDN (for its name); other targets are dropped.
func matchSleeping(list []docker.ContainerSummary, targets []agentapi.WakerTarget) map[string]int64 {
	fqdns := map[int64]string{}
	for _, c := range list {
		id, ok := managedID(c.Labels, c.Names...)
		if !ok || instancespec.CheckFQDN(c.Labels[LabelName], c.Labels[LabelFQDN]) != nil {
			continue
		}
		fqdns[id] = c.Labels[LabelFQDN]
	}
	set := make(map[string]int64, len(targets))
	for _, t := range targets {
		if f, ok := fqdns[t.ID]; ok && f == t.FQDN {
			set[t.FQDN] = t.ID
		}
	}
	return set
}

// refreshRouterForWaker rewrites the routes and reloads the router when the routes for list
// and set differ from what the router has loaded. It never waits behind a running router
// round (whose image pull may take minutes): that round reads the stored set, and the next
// call compares again.
func (m *Manager) refreshRouterForWaker(ctx context.Context, list []docker.ContainerSummary, set map[string]int64) {
	want, err := RenderRoutes(RoutesWith(list, set))
	if err != nil || !m.routerMu.TryLock() {
		return
	}
	defer m.routerMu.Unlock()
	if bytes.Equal(want, m.lastApplied) {
		return
	}
	list, err = m.refreshRouter(ctx)
	if err == nil {
		err = m.syncRouterNetworks(ctx, list)
	}
	if err != nil {
		m.log.Debug("router not updated for the waker; the background loop retries", "err", sanitizeError(err))
	}
}

// ensureWaker makes sure mcpanel-waker exists with the current template, image and agent
// version and is running. It reports whether the container exists and runs afterwards.
func (m *Manager) ensureWaker(ctx context.Context) (exists, running bool, err error) {
	cfg, err := WakerContainer(m.host, m.waker.binary)
	if err != nil {
		return false, false, err
	}
	if m.customize != nil {
		m.customize(WakerName, &cfg)
	}
	template, err := wakerTemplateHash(cfg, m.waker.version)
	if err != nil {
		return false, false, err
	}
	cfg.Labels[LabelWakerTemplate] = template
	info, err := m.dc.Inspect(ctx, WakerName)
	exists = err == nil
	if err != nil && !docker.IsNotFound(err) {
		return false, false, err
	}
	if exists && info.Config.Labels[LabelWaker] != "1" {
		return false, false, fmt.Errorf("container %s exists but is not managed by mc-panel (label %s=1 missing); leaving it untouched",
			WakerName, LabelWaker)
	}
	if exists && (info.Config.Labels[LabelWakerTemplate] != template || !sameImage(info.Config.Image, cfg.Image)) {
		m.log.Info("replacing waker container", "old_image", info.Config.Image, "new_image", cfg.Image,
			"old_template", info.Config.Labels[LabelWakerTemplate], "new_template", template)
		if err := m.dc.Remove(ctx, WakerName, true); err != nil && !docker.IsNotFound(err) {
			return true, false, err
		}
		exists = false
	}
	if !exists {
		// Docker would create a missing bind source as an empty root-owned directory.
		fi, err := os.Stat(m.waker.binary)
		if err != nil {
			return false, false, fmt.Errorf("agent binary: %w", err)
		}
		if !fi.Mode().IsRegular() {
			return false, false, fmt.Errorf("agent binary %s is not a regular file", m.waker.binary)
		}
		ok, err := m.dc.ImageExists(ctx, cfg.Image)
		if err != nil {
			return false, false, err
		}
		if !ok {
			return false, false, fmt.Errorf("image %s not present yet (pulled with the router)", cfg.Image)
		}
		if _, err := m.dc.CreateContainer(ctx, WakerName, cfg); err != nil {
			return false, false, err
		}
		m.log.Info("waker container created", "image", cfg.Image)
		if err := m.dc.Start(ctx, WakerName); err != nil {
			return true, false, err
		}
		return true, true, nil
	}
	if !info.State.Running {
		if err := m.dc.Start(ctx, WakerName); err != nil {
			return true, false, err
		}
	}
	return true, true, nil
}

// readWakes reads at most wakerLogLimit bytes of the waker's stdout written after the last
// read and returns the sorted ids of the sleeping devs with a wake line. Lines that are not
// exactly "wake <fqdn>", stderr and entries at or before the last read are ignored. Errors
// are logged; the next call reads on from the last complete entry.
func (m *Manager) readWakes(ctx context.Context, set map[string]int64) []int64 {
	rc, err := m.dc.ContainerLogs(ctx, WakerName, docker.LogsOptions{Tail: -1, Since: m.waker.since.UnixNano()})
	if err != nil {
		m.log.Debug("waker log not read", "err", sanitizeError(err))
		return []int64{}
	}
	defer func() { _ = rc.Close() }()
	d := docker.NewDemuxer(io.LimitReader(rc, wakerLogLimit))
	ids := []int64{}
	for {
		f, err := d.Next()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				m.log.Debug("waker log read stopped", "err", sanitizeError(err))
			}
			break
		}
		ts, line, ok := bytes.Cut(f.Data, []byte{' '})
		if !ok {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, string(ts))
		if err != nil || !at.After(m.waker.since) {
			continue
		}
		m.waker.since = at
		if f.Stream != docker.Stdout {
			continue
		}
		sub := wakeLineRe.FindSubmatch(bytes.TrimSuffix(line, []byte{'\n'}))
		if sub == nil {
			continue
		}
		if id, ok := set[string(sub[1])]; ok && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, cmp.Compare[int64])
	return ids
}
