package defense

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"mcpanel/internal/audit"
	"mcpanel/internal/httpx"
	"mcpanel/internal/store"
)

const (
	maxManualJailMinutes = 10080
	maxJailReasonLen     = 200
	jailListLimit        = 200
)

var errAlreadyJailed = errors.New("defense: already jailed")

// createManualJail inserts a manual jail unless the key is already jailed.
func (s *Service) createManualJail(ctx context.Context, key netip.Prefix, reason string, d time.Duration, now time.Time) (store.Jail, error) {
	s.jailMu.Lock()
	defer s.jailMu.Unlock()
	if _, ok := s.snap.Load().jailedUntil(key, now); ok {
		return store.Jail{}, errAlreadyJailed
	}
	prior, err := s.q.DefenseCountJailsSince(ctx, store.DefenseCountJailsSinceParams{
		IpKey: key.String(), Since: now.Add(-RepeatWindow).UnixMilli(),
	})
	if err != nil {
		return store.Jail{}, fmt.Errorf("defense: count jails: %w", err)
	}
	expires := now.Add(d)
	j, err := s.q.DefenseInsertJail(ctx, store.DefenseInsertJailParams{
		IpKey: key.String(), Reason: reason, Level: prior + 1, Manual: 1,
		CreatedAt: now.UnixMilli(), ExpiresAt: expires.UnixMilli(),
	})
	if err != nil {
		return store.Jail{}, fmt.Errorf("defense: insert jail: %w", err)
	}
	s.addJail(key, expires)
	return j, nil
}

func (h *Handler) listJails(w http.ResponseWriter, r *http.Request) {
	active := true
	if v := r.URL.Query().Get("active"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "active must be true or false")
			return
		}
		active = b
	}
	now := h.s.clock.Now()
	var rows []store.Jail
	var err error
	if active {
		rows, err = h.s.q.DefenseListActiveJails(r.Context(), now.UnixMilli())
	} else {
		rows, err = h.s.q.DefenseListJails(r.Context(), jailListLimit)
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]jailResponse, 0, len(rows))
	for _, j := range rows {
		out = append(out, toJailResponse(j, now))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createJailRequest struct {
	IP              string `json:"ip"`
	DurationMinutes int64  `json:"duration_minutes"`
	Reason          string `json:"reason"`
}

func (h *Handler) createJail(w http.ResponseWriter, r *http.Request) {
	var req createJailRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(req.IP))
	if err != nil {
		validation(w, r, "ip must be a single IP address")
		return
	}
	ip = normalizeIP(ip)
	if ip.IsLoopback() || ip.IsUnspecified() {
		validation(w, r, "loopback and unspecified addresses cannot be jailed")
		return
	}
	if req.DurationMinutes < 1 || req.DurationMinutes > maxManualJailMinutes {
		validation(w, r, "duration_minutes must be between 1 and 10080")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if utf8.RuneCountInString(reason) > maxJailReasonLen {
		validation(w, r, "reason must be at most 200 characters")
		return
	}
	if reason == "" {
		reason = "manual"
	}
	ctx := r.Context()
	now := h.s.clock.Now()
	key := httpx.IPKey(ip)
	if h.s.snap.Load().allowOverlaps(key, now) {
		conflict(w, r, "the address is covered by an allow rule")
		return
	}
	caller := normalizeIP(httpx.ClientIP(ctx))
	if !caller.IsValid() {
		conflict(w, r, "cannot determine your own address; refusing to jail") // fail closed
		return
	}
	if httpx.IPKey(caller) == key {
		conflict(w, r, "refusing to jail your own address")
		return
	}
	j, err := h.s.createManualJail(ctx, key, reason, time.Duration(req.DurationMinutes)*time.Minute, now)
	if errors.Is(err, errAlreadyJailed) {
		conflict(w, r, "the address is already jailed")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.s.kickSync()
	h.record(ctx, audit.Entry{
		Action: audit.ActionJailCreate, Target: key.String(),
		Details: map[string]any{"jail_id": j.ID, "duration_minutes": req.DurationMinutes, "reason": reason},
	})
	httpx.WriteJSON(w, http.StatusCreated, toJailResponse(j, now))
}

func (h *Handler) liftJail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "jail")
	if !ok {
		return
	}
	ctx := r.Context()
	j, err := h.s.q.DefenseGetJail(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && j.LiftedAt.Valid) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "jail not found or already lifted")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.s.clock.Now()
	n, err := h.s.q.DefenseLiftJail(ctx, store.DefenseLiftJailParams{
		LiftedAt: msNull(now), LiftedBy: sql.NullString{String: callerName(ctx), Valid: true}, ID: id,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "jail not found or already lifted")
		return
	}
	h.s.reloadOrLog(ctx)
	h.s.kickSync()
	h.record(ctx, audit.Entry{Action: audit.ActionJailLift, Target: j.IpKey, Details: map[string]any{"jail_id": id}})
	w.WriteHeader(http.StatusNoContent)
}
