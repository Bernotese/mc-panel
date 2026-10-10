package servers

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"strconv"

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
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	if d := h.power.take(p.UserID, h.clock.Now()); d > 0 {
		secs := int(math.Ceil(d.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many power actions, try again later")
		return
	}
	if req.Action == agentapi.PowerRestart {
		// Like a reconcile pass, a restart must not overlap a delete: under the lock the row is
		// read again, so a server deleted meanwhile gets no agent call (no orphan container).
		h.reconcile.mu.Lock()
		defer h.reconcile.mu.Unlock()
		in, err := h.q.ServersGetInstance(ctx, s.ID)
		if errors.Is(err, sql.ErrNoRows) {
			writeNotFound(w, r)
			return
		}
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		if err := h.restart(ctx, s, in); err != nil {
			writeAgentError(w, r, err)
			return
		}
	}
	desired := desiredRunning
	if req.Action == agentapi.PowerStop {
		desired = desiredStopped
	}
	n, err := h.q.ServersSetDesiredState(ctx, store.ServersSetDesiredStateParams{
		DesiredState: desired, UpdatedAt: h.clock.Now().UnixMilli(), ServerID: s.ID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 { // deleted concurrently
		writeNotFound(w, r)
		return
	}
	h.record(ctx, audit.ActionServerPower, serverTarget(s.ID), audit.ResultSuccess,
		map[string]any{"action": string(req.Action), "name": s.Name})
	h.Trigger()
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
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
	case st.Revision >= in.Revision:
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
