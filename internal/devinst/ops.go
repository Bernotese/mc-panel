package devinst

import (
	"context"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// playerName is the Minecraft name rule of the Players tab; only such a name ever follows
// the fixed verbs op/deop (user text never becomes a command).
var playerName = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

// rcon sends one fixed command to devID; the command is checked like console input.
func (h *Handler) rcon(ctx context.Context, devID int64, cmd string) error {
	if h.console == nil {
		return agentapi.ErrUnavailable
	}
	if err := agentapi.ValidateCommand(cmd); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, rconTimeout)
	defer cancel()
	_, err := h.console.Rcon(ctx, devID, cmd)
	return err
}

// say sends a chat warning; a failure is only logged (the sync runs anyway).
func (h *Handler) say(ctx context.Context, devID int64, cmd string) {
	if err := h.rcon(ctx, devID, cmd); err != nil {
		h.log.WarnContext(ctx, "devinst: chat warning", "server_id", devID, "err", err)
	}
}

// isRunning reports whether devID ran in the last monitoring pass.
func (h *Handler) isRunning(devID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.devs[devID]
	return st != nil && st.running
}

type opsView struct {
	Names []string `json:"names"`
}

func (h *Handler) writeOps(w http.ResponseWriter, r *http.Request, devID int64) {
	names, err := h.q.DevOpsList(r.Context(), devID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if names == nil {
		names = []string{}
	}
	httpx.WriteJSON(w, http.StatusOK, opsView{Names: names})
}

// handleListOps answers GET /servers/{id}/dev/ops.
func (h *Handler) handleListOps(w http.ResponseWriter, r *http.Request) {
	d, ok := h.loadDev(w, r)
	if !ok {
		return
	}
	h.writeOps(w, r, d.row.ServerID)
}

type opRequest struct {
	Name string `json:"name"`
}

// handleAddOp answers POST /servers/{id}/dev/ops: store the name and, while the dev runs,
// send op <name> (a failure there is logged; the next start applies the list anyway).
func (h *Handler) handleAddOp(w http.ResponseWriter, r *http.Request) {
	var req opRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	d, ok := h.loadDev(w, r)
	if !ok {
		return
	}
	if !playerName.MatchString(req.Name) {
		writeValidation(w, r, "name must be 3-16 letters, digits or underscores")
		return
	}
	ctx := r.Context()
	id := d.row.ServerID
	err := h.q.DevOpsAdd(ctx, store.DevOpsAddParams{ServerID: id, Name: req.Name, CreatedAt: h.clock.Now().UnixMilli()})
	h.record(ctx, audit.ActionDevOpAdd, id, result(err), map[string]any{"name": req.Name})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.sendOp(ctx, id, "op", req.Name)
	h.writeOps(w, r, id)
}

// handleRemoveOp answers DELETE /servers/{id}/dev/ops/{name}: remove the name and, while the
// dev runs, send deop <name>.
func (h *Handler) handleRemoveOp(w http.ResponseWriter, r *http.Request) {
	d, ok := h.loadDev(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "name")
	if !playerName.MatchString(name) {
		writeValidation(w, r, "name must be 3-16 letters, digits or underscores")
		return
	}
	ctx := r.Context()
	id := d.row.ServerID
	n, err := h.q.DevOpsRemove(ctx, store.DevOpsRemoveParams{ServerID: id, Name: name})
	if err == nil && n == 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "the name is not on the dev ops list")
		return
	}
	h.record(ctx, audit.ActionDevOpRemove, id, result(err), map[string]any{"name": name})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.sendOp(ctx, id, "deop", name)
	h.writeOps(w, r, id)
}

// sendOp sends verb (op or deop) for a validated name while devID runs.
func (h *Handler) sendOp(ctx context.Context, devID int64, verb, name string) {
	if !h.isRunning(devID) {
		return
	}
	if err := h.rcon(context.WithoutCancel(ctx), devID, verb+" "+name); err != nil {
		h.log.WarnContext(ctx, "devinst: send "+verb, "server_id", devID, "err", err)
	}
}

// applyOps sends op <name> for every entry of the dev ops list; it reports whether all went
// through (otherwise the next pass tries again).
func (h *Handler) applyOps(ctx context.Context, devID int64) bool {
	names, err := h.q.DevOpsList(ctx, devID)
	if err != nil {
		h.log.ErrorContext(ctx, "devinst: list dev ops", "server_id", devID, "err", err)
		return false
	}
	for _, n := range names {
		if !playerName.MatchString(n) {
			continue // never stored by the API; skipped rather than sent
		}
		if err := h.rcon(ctx, devID, "op "+n); err != nil {
			h.log.WarnContext(ctx, "devinst: re-apply dev ops", "server_id", devID, "err", err)
			return false
		}
	}
	return true
}
