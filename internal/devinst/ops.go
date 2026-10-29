package devinst

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/timescale"
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

// opsWindow is how long after a dev's start a listed name missing from ops.json is sent
// again on every pass; after it, missing names are sent once more and the run counts as done
// (an unknown account is not sent forever, and a dev seen late, after a panel restart, still
// gets its ops). Shortened in acceptance builds only (internal/timescale).
var opsWindow = timescale.D(15 * time.Minute)

// applyOps brings the dev ops list into the server's ops.json for the run started at
// startedAt: it sends op <name> for every listed name ops.json lacks, and the run counts as
// done once none is missing. A successful RCON round trip proves nothing, since the server
// answers a failed profile lookup at Mojang with a message, not an error. An agent or RCON
// error is retried on the next pass.
func (h *Handler) applyOps(ctx context.Context, devID, startedAt int64, now time.Time) {
	missing, err := h.missingOps(ctx, devID)
	if err != nil {
		h.log.WarnContext(ctx, "devinst: re-apply dev ops", "server_id", devID, "err", err)
		return
	}
	for _, n := range missing {
		if err := h.rcon(ctx, devID, "op "+n); err != nil {
			h.log.WarnContext(ctx, "devinst: re-apply dev ops", "server_id", devID, "err", err)
			return
		}
	}
	if len(missing) > 0 {
		if now.Sub(time.UnixMilli(startedAt)) < opsWindow {
			return // checked against ops.json on the next pass
		}
		h.log.WarnContext(ctx, "devinst: dev ops still missing from ops.json, sent a last time", "server_id", devID, "names", missing)
	}
	h.mu.Lock()
	if st := h.devs[devID]; st != nil {
		st.opsFor = startedAt
	}
	h.mu.Unlock()
}

// missingOps returns the names of the dev ops list that the server's ops.json lacks (names
// compare case-insensitively, as on the server); ops.json is not read for an empty list.
func (h *Handler) missingOps(ctx context.Context, devID int64) ([]string, error) {
	names, err := h.q.DevOpsList(ctx, devID)
	if err != nil || len(names) == 0 {
		return nil, err
	}
	if h.console == nil {
		return nil, agentapi.ErrUnavailable
	}
	pctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	pl, err := h.console.Players(pctx, devID)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, n := range names {
		if !playerName.MatchString(n) {
			continue // never stored by the API; skipped rather than sent
		}
		if !slices.ContainsFunc(pl.Ops, func(o agentapi.PlayerOp) bool { return strings.EqualFold(o.Name, n) }) {
			missing = append(missing, n)
		}
	}
	return missing, nil
}
