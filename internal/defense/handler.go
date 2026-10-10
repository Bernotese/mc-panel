package defense

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Handler serves the security-center API (/api/security/...).
type Handler struct{ s *Service }

func NewHandler(s *Service) *Handler { return &Handler{s: s} }

func pathID(w http.ResponseWriter, r *http.Request, what string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, what+" not found")
		return 0, false
	}
	return id, true
}

func validation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func conflict(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, msg)
}

func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func callerName(ctx context.Context) string {
	if p, ok := principal.From(ctx); ok {
		return p.Username
	}
	return ""
}

// record writes an audit entry after the change has been committed; failures are logged.
func (h *Handler) record(ctx context.Context, e audit.Entry) {
	if e.Result == "" {
		e.Result = audit.ResultSuccess
	}
	if err := h.s.audit.Record(ctx, e); err != nil {
		h.s.log.Error("defense: audit", "action", e.Action, "err", err)
	}
}

type jailResponse struct {
	ID             int64   `json:"id"`
	IPKey          string  `json:"ip_key"`
	Reason         string  `json:"reason"`
	Level          int64   `json:"level"`
	Manual         bool    `json:"manual"`
	CreatedAt      int64   `json:"created_at"`
	ExpiresAt      int64   `json:"expires_at"`
	LiftedAt       *int64  `json:"lifted_at"`
	LiftedBy       *string `json:"lifted_by"`
	FirewallSynced bool    `json:"firewall_synced"`
}

func firewallSynced(j store.Jail, now time.Time) bool {
	if j.LiftedAt.Valid {
		return !j.FwBannedAt.Valid || j.FwUnbannedAt.Valid
	}
	if j.ExpiresAt <= now.UnixMilli() {
		return true
	}
	return j.FwBannedAt.Valid
}

func toJailResponse(j store.Jail, now time.Time) jailResponse {
	resp := jailResponse{
		ID: j.ID, IPKey: j.IpKey, Reason: j.Reason, Level: j.Level, Manual: j.Manual != 0,
		CreatedAt: j.CreatedAt, ExpiresAt: j.ExpiresAt, LiftedAt: nullInt64Ptr(j.LiftedAt),
		FirewallSynced: firewallSynced(j, now),
	}
	if j.LiftedBy.Valid {
		by := j.LiftedBy.String
		resp.LiftedBy = &by
	}
	return resp
}
