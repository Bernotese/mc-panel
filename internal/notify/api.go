package notify

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/httpx"
	"mcpanel/internal/rbac"
	"mcpanel/internal/secretbox"
	"mcpanel/internal/store"
)

const maxNotifierNameLen = 64

type HandlerDeps struct {
	DB         *sql.DB
	Box        *secretbox.Box
	Dispatcher *Dispatcher
	Audit      audit.Recorder
	Clock      clock.Clock
	Dev        bool
	Logger     *slog.Logger
}

// Handler serves /api/security/notifiers.
type Handler struct {
	q     *store.Queries
	box   *secretbox.Box
	disp  *Dispatcher
	audit audit.Recorder
	clock clock.Clock
	dev   bool
	log   *slog.Logger
}

func NewHandler(d HandlerDeps) *Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Handler{q: store.New(d.DB), box: d.Box, disp: d.Dispatcher, audit: d.Audit, clock: d.Clock, dev: d.Dev, log: d.Logger}
}

// Routes registers the notifier endpoints with full paths relative to /api.
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/security/notifiers", rbac.Admin(), h.list)
	api.Post("/security/notifiers", rbac.Admin().WithSudo(), h.create)
	api.Patch("/security/notifiers/{id}", rbac.Admin().WithSudo(), h.update)
	api.Delete("/security/notifiers/{id}", rbac.Admin().WithSudo(), h.delete)
	api.Post("/security/notifiers/{id}/test", rbac.Admin(), h.test)
}

type notifierResponse struct {
	ID           int64  `json:"id"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	TargetMasked string `json:"target_masked"`
	CreatedAt    int64  `json:"created_at"`
}

func (h *Handler) toResponse(n store.Notifier) notifierResponse {
	masked := "(unreadable)"
	if cfg, err := OpenConfig(h.box, Kind(n.Kind), n.ConfigEnc); err == nil {
		masked = MaskTarget(Kind(n.Kind), cfg)
	} else {
		h.log.Error("notify: cannot decrypt notifier config", "notifier", n.Name, "err", err)
	}
	return notifierResponse{ID: n.ID, Kind: n.Kind, Name: n.Name, Enabled: n.Enabled != 0, TargetMasked: masked, CreatedAt: n.CreatedAt}
}

func validation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "notifier not found")
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		notFound(w, r)
		return 0, false
	}
	return id, true
}

func cleanName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	return name, n >= 1 && n <= maxNotifierNameLen
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// record writes the audit entry after the change was committed. Details never contain secrets.
func (h *Handler) record(ctx context.Context, action, target string, details map[string]any) {
	if err := h.audit.Record(ctx, audit.Entry{Action: action, Target: target, Result: audit.ResultSuccess, Details: details}); err != nil {
		h.log.Error("notify: audit", "action", action, "err", err)
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.DefenseListNotifiers(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]notifierResponse, 0, len(rows))
	for _, n := range rows {
		out = append(out, h.toResponse(n))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createRequest struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled"`
	URL     string `json:"url"`
	Topic   string `json:"topic"`
	Token   string `json:"token"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	kind := Kind(req.Kind)
	if !kind.Valid() {
		validation(w, r, "kind must be discord or ntfy")
		return
	}
	name, ok := cleanName(req.Name)
	if !ok {
		validation(w, r, "name must be 1 to 64 characters")
		return
	}
	if req.Enabled == nil {
		validation(w, r, "enabled is required")
		return
	}
	cfg := Config{URL: strings.TrimSpace(req.URL), Topic: strings.TrimSpace(req.Topic), Token: strings.TrimSpace(req.Token)}
	if err := ValidateConfig(kind, cfg, h.dev); err != nil {
		validation(w, r, err.Error())
		return
	}
	enc, err := SealConfig(h.box, kind, cfg)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now().UnixMilli()
	row, err := h.q.DefenseInsertNotifier(r.Context(), store.DefenseInsertNotifierParams{
		Kind: string(kind), Name: name, ConfigEnc: enc, Enabled: boolInt(*req.Enabled), CreatedAt: now, UpdatedAt: now,
	})
	if isUniqueViolation(err) {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "a notifier with this name already exists")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(r.Context(), audit.ActionNotifierCreate, row.Name,
		map[string]any{"notifier_id": row.ID, "kind": row.Kind, "enabled": row.Enabled != 0})
	httpx.WriteJSON(w, http.StatusCreated, h.toResponse(row))
}

type updateRequest struct {
	Name    *string `json:"name"`
	Enabled *bool   `json:"enabled"`
	URL     *string `json:"url"`
	Topic   *string `json:"topic"`
	Token   *string `json:"token"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req updateRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	row, err := h.q.DefenseGetNotifier(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, r)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	kind := Kind(row.Kind)
	cfg, err := OpenConfig(h.box, kind, row.ConfigEnc)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	name, enabled := row.Name, row.Enabled
	var fields []string
	if req.Name != nil {
		n, ok := cleanName(*req.Name)
		if !ok {
			validation(w, r, "name must be 1 to 64 characters")
			return
		}
		name = n
		fields = append(fields, "name")
	}
	if req.Enabled != nil {
		enabled = boolInt(*req.Enabled)
		fields = append(fields, "enabled")
	}
	if req.URL != nil {
		cfg.URL = strings.TrimSpace(*req.URL)
		fields = append(fields, "url")
	}
	if req.Topic != nil {
		cfg.Topic = strings.TrimSpace(*req.Topic)
		fields = append(fields, "topic")
	}
	if req.Token != nil {
		cfg.Token = strings.TrimSpace(*req.Token)
		fields = append(fields, "token")
	}
	if err := ValidateConfig(kind, cfg, h.dev); err != nil {
		validation(w, r, err.Error())
		return
	}
	enc, err := SealConfig(h.box, kind, cfg)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	updated, err := h.q.DefenseUpdateNotifier(ctx, store.DefenseUpdateNotifierParams{
		Name: name, ConfigEnc: enc, Enabled: enabled, UpdatedAt: h.clock.Now().UnixMilli(), ID: id,
	})
	if isUniqueViolation(err) {
		validation(w, r, "a notifier with this name already exists")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, r)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionNotifierUpdate, updated.Name, map[string]any{"notifier_id": id, "fields": fields})
	httpx.WriteJSON(w, http.StatusOK, h.toResponse(updated))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	row, err := h.q.DefenseGetNotifier(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, r)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	n, err := h.q.DefenseDeleteNotifier(ctx, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		notFound(w, r)
		return
	}
	h.record(ctx, audit.ActionNotifierDelete, row.Name, map[string]any{"notifier_id": id, "kind": row.Kind})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := h.disp.SendTest(r.Context(), id)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrNotFound):
		notFound(w, r)
	case errors.Is(err, ErrConfig):
		httpx.Internal(w, r, err)
	default:
		// Sender errors are sanitized (no URL, topic or token).
		validation(w, r, "test notification failed: "+err.Error())
	}
}
