package console

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// handlePlayerLists returns ops, whitelist and bans as the server stores them (spec §6.1).
func (h *Handler) handlePlayerLists(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	res, err := h.agent.Players(r.Context(), s.ID)
	switch {
	case errors.Is(err, agentapi.ErrNotFound):
		res = agentapi.PlayersResponse{} // data directory not created yet: nothing stored
	case err != nil:
		writeAgentError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, normalizeLists(res))
}

// normalizeLists turns nil slices into empty ones (JSON [] instead of null).
func normalizeLists(p agentapi.PlayersResponse) agentapi.PlayersResponse {
	if p.Ops == nil {
		p.Ops = []agentapi.PlayerOp{}
	}
	if p.Whitelist == nil {
		p.Whitelist = []agentapi.PlayerEntry{}
	}
	if p.Bans == nil {
		p.Bans = []agentapi.PlayerBan{}
	}
	if p.Errors == nil {
		p.Errors = []string{}
	}
	return p
}

type actionRequest struct {
	Action string  `json:"action"`
	Name   string  `json:"name"`
	Reason *string `json:"reason"`
}

const maxReasonRunes = 200

var playerName = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

// actionVerbs maps the PlayerAction enum to the Minecraft command (spec §6.5). User text never
// becomes a verb.
var actionVerbs = map[string]string{
	"kick":             "kick",
	"ban":              "ban",
	"pardon":           "pardon",
	"op":               "op",
	"deop":             "deop",
	"whitelist_add":    "whitelist add",
	"whitelist_remove": "whitelist remove",
}

func takesReason(action string) bool { return action == "kick" || action == "ban" }

// validReason: at most 200 runes, valid UTF-8, only printable runes (no control characters,
// no line or paragraph separators).
func validReason(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxReasonRunes {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// actionCommand builds the RCON command of req, or returns why req is invalid.
func actionCommand(req actionRequest) (cmd, problem string) {
	verb, ok := actionVerbs[req.Action]
	if !ok {
		return "", "action must be one of kick, ban, pardon, op, deop, whitelist_add, whitelist_remove"
	}
	if !playerName.MatchString(req.Name) {
		return "", "name must be 3-16 letters, digits or underscores"
	}
	cmd = verb + " " + req.Name
	if req.Reason != nil {
		if !takesReason(req.Action) {
			return "", "reason is only allowed for kick and ban"
		}
		if !validReason(*req.Reason) {
			return "", "reason must be at most 200 printable characters"
		}
		if reason := strings.TrimSpace(*req.Reason); reason != "" {
			cmd += " " + reason
		}
	}
	// Defence in depth: the result must be a valid single command like any console input.
	if err := agentapi.ValidateCommand(cmd); err != nil {
		return "", err.Error()
	}
	return cmd, ""
}

// handlePlayerAction runs one player action over RCON (spec §6.5).
func (h *Handler) handlePlayerAction(w http.ResponseWriter, r *http.Request) {
	var req actionRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	cmd, problem := actionCommand(req)
	if problem != "" {
		writeValidation(w, r, problem)
		return
	}
	details := map[string]any{"action": req.Action, "name": req.Name}
	if req.Reason != nil {
		if reason := strings.TrimSpace(*req.Reason); reason != "" {
			details["reason"] = reason
		}
	}
	if h.run(w, r, s.ID, cmd, audit.ActionServerPlayerAction, details) {
		h.online.forget(s.ID)
	}
}
