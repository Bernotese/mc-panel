package servers

import (
	"math"
	"net/http"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

type powerRequest struct {
	Action agentapi.PowerAction `json:"action"`
}

// handleServerPower is still the sub-project 1 stub: it validates, audits and accepts.
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
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	if d := h.power.take(p.UserID, h.clock.Now()); d > 0 {
		secs := int(math.Ceil(d.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many power actions, try again later")
		return
	}
	h.record(ctx, audit.ActionServerPower, serverTarget(s.ID), audit.ResultSuccess,
		map[string]any{"action": string(req.Action), "name": s.Name})
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
