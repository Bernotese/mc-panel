package servers

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

type powerRequest struct {
	Action agentapi.PowerAction `json:"action"`
}

var errBaseDomainMissing = errors.New("no base domain configured: set one in the server settings")

// errRateLimited: the user's power limiter refused the action (retryAfter in Retry-After).
type errRateLimited struct{ retryAfter time.Duration }

func (errRateLimited) Error() string { return "too many power actions" }

// handleServerPower stores the user's wish (desired_state) and lets the reconcile loop apply
// it, so start and stop work while the agent is down. Restart needs the agent right away.
func (h *Handler) handleServerPower(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req powerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if !agentapi.ValidPowerAction(req.Action) {
		writeValidation(w, r, "action must be start, stop or restart")
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	if _, ok := h.loadInstance(w, r, s); !ok { // 409 for a server without instance
		return
	}
	if req.Action == agentapi.PowerStop {
		// A stop is always allowed (data safety spec §1.1) and needs no admission.
		if !h.takePower(w, r) {
			return
		}
	} else if !h.admitPower(w, r, s, req.Action) {
		return
	}
	desired := desiredRunning
	if req.Action == agentapi.PowerStop {
		desired = desiredStopped
	}
	if err := h.setDesired(ctx, s.ID, desired); errors.Is(err, ErrNoInstance) { // deleted concurrently
		writeNotFound(w, r)
		return
	} else if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerPower, serverTarget(s.ID), audit.ResultSuccess,
		map[string]any{"action": string(req.Action), "name": s.Name})
	h.Trigger()
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// takePower takes a token of the user's power limiter; without one it writes 429.
func (h *Handler) takePower(w http.ResponseWriter, r *http.Request) bool {
	p, _ := principal.From(r.Context()) // guaranteed by the rbac rule
	if d := h.power.take(p.UserID, h.clock.Now()); d > 0 {
		writeRateLimited(w, r, d)
		return false
	}
	return true
}

func writeRateLimited(w http.ResponseWriter, r *http.Request, d time.Duration) {
	secs := int(math.Ceil(d.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many power actions, try again later")
}

// admitPower admits a start or restart under the server's admission lock (data safety spec
// §3): only a safe data state takes a limiter token (a refusal costs none and is not audited),
// and a restart reaches the agent while the lock is held. A start only stores the wish; the
// reconcile loop admits the actual start again. On failure it has written the answer.
func (h *Handler) admitPower(w http.ResponseWriter, r *http.Request, s store.Server, action agentapi.PowerAction) bool {
	ctx := r.Context()
	if action == agentapi.PowerRestart {
		// Like a reconcile pass, a restart must not overlap a delete: under the lock the row is
		// read again, so a server deleted meanwhile gets no agent call (no orphan container).
		// Lock order: reconcile.mu, then the admission lock.
		h.reconcile.mu.Lock()
		defer h.reconcile.mu.Unlock()
	}
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	err := h.Admit(ctx, s.ID, nil, func(ctx context.Context) error {
		if d := h.power.take(p.UserID, h.clock.Now()); d > 0 {
			return errRateLimited{d}
		}
		if action != agentapi.PowerRestart {
			return nil
		}
		in, err := h.q.ServersGetInstance(ctx, s.ID)
		if err != nil {
			return err
		}
		return h.restart(ctx, s, in)
	})
	var rl errRateLimited
	switch {
	case err == nil:
		return true
	case WriteDataStateError(w, r, err):
	case errors.As(err, &rl):
		writeRateLimited(w, r, rl.retryAfter)
	case errors.Is(err, sql.ErrNoRows):
		writeNotFound(w, r)
	case r.Context().Err() != nil:
		h.log.DebugContext(ctx, "servers: power admission ended with the request", "err", err)
	default:
		writeAgentError(w, r, err)
	}
	return false
}

// setDesired stores a user's power wish. On a dev instance a stop is falling asleep (the waker
// takes its address) and a start or restart wakes it without counting as a wake by the waker
// (dev instances spec §2); both are written with the desired state.
func (h *Handler) setDesired(ctx context.Context, id int64, desired string) error {
	now := h.clock.Now()
	return h.inTx(ctx, func(q *store.Queries) error {
		d, err := q.DevGet(ctx, id)
		switch {
		case err == nil && desired == desiredStopped:
			return setDevState(ctx, q, id, desired, true, d.LastWakeAt, now)
		case err == nil:
			return setDevState(ctx, q, id, desired, false, sql.NullInt64{}, now)
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		n, err := q.ServersSetDesiredState(ctx, store.ServersSetDesiredStateParams{
			DesiredState: desired, UpdatedAt: now.UnixMilli(), ServerID: id,
		})
		if err == nil && n == 0 {
			return ErrNoInstance
		}
		return err
	})
}

// restart applies a user's restart (spec §5.3); the caller holds the reconcile lock: a container in the current revision is
// restarted, an outdated one is recreated with the new configuration (it starts again
// because it ran before). A missing container is left to the reconcile loop, which creates
// and starts it because the desired state becomes "running".
func (h *Handler) restart(ctx context.Context, s store.Server, in store.Instance) error {
	resp, snap, err := h.refreshStatus(ctx)
	if err != nil {
		return err
	}
	if resp.Docker != agentapi.DockerOK {
		return agentapi.ErrDockerUnavailable
	}
	st := snap.actualStatus(in.ServerID)
	switch {
	case foreignContainer(st, s.Name):
		return errForeignContainer
	case st.State == agentapi.StateMissing:
		return nil
	case !needsRecreate(in.Revision, st):
		return h.inst.Power(ctx, in.ServerID, agentapi.PowerRestart)
	}
	set, err := h.loadSettings(ctx)
	if err != nil {
		return err
	}
	req, err := ensureRequest(s.Name, in, set, true)
	if err != nil {
		return err
	}
	return h.inst.Ensure(ctx, req)
}

// ensureRequest builds the agent request for the stored instance.
func ensureRequest(name string, in store.Instance, set instanceSettings, recreate bool) (agentapi.EnsureRequest, error) {
	if set.BaseDomain == nil {
		return agentapi.EnsureRequest{}, errBaseDomainMissing
	}
	fqdn, err := instancespec.FQDN(name, *set.BaseDomain)
	if err != nil {
		return agentapi.EnsureRequest{}, err
	}
	c, err := configFromInstance(in)
	if err != nil {
		return agentapi.EnsureRequest{}, err
	}
	if err := c.validate(set.CurseForgeKey); err != nil {
		return agentapi.EnsureRequest{}, err
	}
	return agentapi.EnsureRequest{
		ID: in.ServerID, Name: name, FQDN: fqdn, Revision: in.Revision,
		Spec: c.spec(set.CurseForgeKey), Recreate: recreate,
	}, nil
}
