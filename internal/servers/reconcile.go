package servers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// ReconcileInterval is the period of the reconcile loop (spec §5.3). Every user action
// triggers an additional pass right away.
const ReconcileInterval = 10 * time.Second

// maxBackoff caps the delay between repeats of an action that does not take effect.
const maxBackoff = 5 * time.Minute

type action int

const (
	actNone     action = iota
	actEnsure          // container missing: create it in the current revision
	actRecreate        // outdated container that does not run: recreate it
	actStart           // power start
	actStop            // power stop
)

func (a action) String() string {
	return [...]string{"none", "ensure", "recreate", "start", "stop"}[a]
}

// decide implements the reconcile table of spec §5.3 for one instance. A container that is
// up with an older revision is left alone (restart_required); one that is not up is
// recreated before it is started, so it never starts with an old configuration.
func decide(desired string, revision int64, st agentapi.InstanceStatus) action {
	switch {
	case st.Operation != agentapi.OpNone:
		return actNone // the agent is working on it
	case st.State == agentapi.StateMissing:
		return actEnsure
	case !isUp(st.State) && st.Revision < revision:
		return actRecreate
	case desired == desiredStopped && isUp(st.State):
		return actStop
	case desired == desiredRunning && (st.State == agentapi.StateExited || st.State == agentapi.StateCreated):
		return actStart
	default:
		return actNone
	}
}

// attempt remembers the last action per instance for the backoff.
type attempt struct {
	act      action
	revision int64
	desired  string
	count    int
	next     time.Time
}

// reconcileState is only touched under mu (ReconcileOnce, delete, restart).
type reconcileState struct {
	mu        sync.Mutex
	attempts  map[int64]attempt
	warned    map[int64]bool // unknown containers already logged
	foreign   map[int64]bool // foreign containers already logged
	agentDown bool
}

// panelError is a reconcile error that happened before the agent was called (for example no
// base domain). The agent knows nothing about it, so the panel shows it as last_error.
type panelError struct{ err error }

func (e panelError) Error() string { return e.err.Error() }
func (e panelError) Unwrap() error { return e.err }

func isUnavailable(err error) bool {
	return errors.Is(err, agentapi.ErrUnavailable) || errors.Is(err, agentapi.ErrDockerUnavailable) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// Run runs the reconcile loop until ctx is cancelled: one pass right away, then every
// ReconcileInterval and after every Trigger.
func (h *Handler) Run(ctx context.Context) {
	t := time.NewTicker(ReconcileInterval)
	defer t.Stop()
	for {
		h.ReconcileOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-h.kick:
		}
	}
}

// ReconcileOnce runs one pass: it reads the actual state from the agent and makes at most one
// agent call per instance. Failures are logged, never returned; an unreachable agent or
// Docker ends the pass and the next pass retries.
func (h *Handler) ReconcileOnce(ctx context.Context) {
	rs := &h.reconcile
	rs.mu.Lock()
	defer rs.mu.Unlock()
	resp, snap, err := h.refreshStatus(ctx)
	if err == nil && resp.Docker != agentapi.DockerOK {
		err = agentapi.ErrDockerUnavailable
	}
	if err != nil {
		if !rs.agentDown && ctx.Err() == nil {
			h.log.Warn("servers: reconcile paused, agent or docker unavailable", "err", err)
		}
		rs.agentDown = true
		return
	}
	if rs.agentDown {
		h.log.Info("servers: reconcile resumed, agent reachable again")
		rs.agentDown = false
	}
	rows, err := h.q.ServersListInstances(ctx)
	if err != nil {
		h.log.Error("servers: reconcile: list instances", "err", err)
		return
	}
	set, err := h.loadSettings(ctx)
	if err != nil {
		h.log.Error("servers: reconcile: load settings", "err", err)
		return
	}
	h.warnUnknown(resp, rows)
	now := h.clock.Now()
	seen := make(map[int64]bool, len(rows))
	acted := false
	for _, row := range rows {
		in := instanceFromListRow(row)
		seen[in.ServerID] = true
		st := snap.actualStatus(in.ServerID)
		if foreignContainer(st, row.Name) {
			delete(rs.attempts, in.ServerID)
			h.setPanelError(in.ServerID, "")
			if !rs.foreign[in.ServerID] {
				rs.foreign[in.ServerID] = true
				h.log.Warn("servers: container belongs to another server, leaving it alone",
					"server_id", in.ServerID, "name", row.Name, "container_name", st.Name)
			}
			continue
		}
		delete(rs.foreign, in.ServerID)
		act := decide(in.DesiredState, in.Revision, st)
		if act == actNone {
			// An operation in progress is the agent working on the last action, not its
			// success: keep the attempt, or a permanently failing action loses its backoff.
			if st.Operation == agentapi.OpNone {
				delete(rs.attempts, in.ServerID)
				h.setPanelError(in.ServerID, "")
			}
			continue
		}
		if !rs.allow(in, act, now) {
			continue
		}
		err := h.apply(ctx, row.Name, in, act, set)
		var pe panelError
		switch {
		case err == nil:
			acted = true
			h.setPanelError(in.ServerID, "")
		case isUnavailable(err):
			// The action never reached the agent: it must not count for the backoff, so the
			// stored wish is applied on the first pass after the agent answers again.
			delete(rs.attempts, in.ServerID)
			h.log.Warn("servers: reconcile aborted", "server_id", in.ServerID, "action", act.String(), "err", err)
			return
		case errors.As(err, &pe):
			h.setPanelError(in.ServerID, pe.Error())
			h.log.Error("servers: reconcile action failed", "server_id", in.ServerID, "action", act.String(), "err", err)
		default:
			h.setPanelError(in.ServerID, "")
			h.log.Error("servers: reconcile action failed", "server_id", in.ServerID, "action", act.String(), "err", err)
		}
	}
	for id := range rs.attempts {
		if !seen[id] {
			delete(rs.attempts, id)
		}
	}
	for id := range rs.foreign {
		if !seen[id] {
			delete(rs.foreign, id)
		}
	}
	h.keepPanelErrors(seen)
	if acted {
		h.Trigger() // look again soon: the agent may already be done
	}
}

// allow applies an exponential backoff to an action that repeats without effect (10 s, 20 s,
// 40 s … at most 5 min), so a permanent last_error never becomes a tight loop. A new
// revision or desired state starts over.
func (rs *reconcileState) allow(in store.Instance, act action, now time.Time) bool {
	a, ok := rs.attempts[in.ServerID]
	same := ok && a.act == act && a.revision == in.Revision && a.desired == in.DesiredState
	if same && now.Before(a.next) {
		return false
	}
	count := 1
	if same {
		count = a.count + 1
	}
	delay := ReconcileInterval << min(count-1, 10)
	if delay > maxBackoff {
		delay = maxBackoff
	}
	rs.attempts[in.ServerID] = attempt{act: act, revision: in.Revision, desired: in.DesiredState, count: count, next: now.Add(delay)}
	return true
}

func (h *Handler) apply(ctx context.Context, name string, in store.Instance, act action, set instanceSettings) error {
	switch act {
	case actEnsure, actRecreate:
		req, err := ensureRequest(name, in, set, act == actRecreate)
		if err != nil {
			return panelError{err}
		}
		return h.inst.Ensure(ctx, req)
	case actStart:
		return h.inst.Power(ctx, in.ServerID, agentapi.PowerStart)
	case actStop:
		return h.inst.Power(ctx, in.ServerID, agentapi.PowerStop)
	}
	return nil
}

// warnUnknown logs every container with the mcpanel.instance label that the database does
// not know, once per container. Such containers are never touched (spec §5.3). Entries in
// state "missing" are queued work or a remembered error of the agent, not containers.
func (h *Handler) warnUnknown(resp agentapi.InstancesResponse, rows []store.ServersListInstancesRow) {
	known := make(map[int64]bool, len(rows))
	for _, r := range rows {
		known[r.ServerID] = true
	}
	for _, st := range resp.Instances {
		if st.State != agentapi.StateMissing && !known[st.ID] && !h.reconcile.warned[st.ID] {
			h.reconcile.warned[st.ID] = true
			h.log.Warn("servers: container without database entry, leaving it alone", "server_id", st.ID, "name", st.Name)
		}
	}
}
