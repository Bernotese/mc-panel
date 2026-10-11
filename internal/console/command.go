package console

import (
	"context"
	"net/http"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

type commandRequest struct {
	Command string `json:"command"`
}

// commandResponse is the ConsoleCommandResponse schema (also the answer of player actions).
type commandResponse struct {
	Output string `json:"output"`
}

// handleCommand sends one console command over RCON (spec §6.4).
func (h *Handler) handleCommand(w http.ResponseWriter, r *http.Request) {
	var req commandRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	cmd := strings.TrimPrefix(req.Command, "/") // exactly one leading slash
	if err := agentapi.ValidateCommand(cmd); err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	h.run(w, r, s.ID, cmd, audit.ActionServerConsoleCommand, map[string]any{"command": cmd})
}

// run sends an accepted command: rate limit, agent call, audit entry, answer. Console commands
// and player actions share it (and the limiter, spec §6.5). It reports whether the agent
// accepted the command.
func (h *Handler) run(w http.ResponseWriter, r *http.Request, serverID int64, cmd, action string, details map[string]any) bool {
	ctx := r.Context()
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	if !h.allow(w, r, p.UserID) {
		return false
	}
	// Not cancelled with the request: once sent, the command runs on the server whether the
	// browser waits or not, and the audit entry must say how it ended (RconTimeout bounds it).
	out, err := h.agent.Rcon(context.WithoutCancel(ctx), serverID, cmd)
	result := audit.ResultSuccess
	if err != nil {
		result = audit.ResultFailure
	}
	h.record(ctx, action, serverTarget(serverID), result, details)
	if err != nil {
		h.writeAgentError(w, r, err)
		return false
	}
	httpx.WriteJSON(w, http.StatusOK, commandResponse{Output: out})
	return true
}
