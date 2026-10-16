package defense

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/netip"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

const maxAllowedCountries = 300

type settingsRequest struct {
	Enforce2FAForUsers *bool    `json:"enforce_2fa_for_users"`
	AdminAllowlistOnly *bool    `json:"admin_allowlist_only"`
	AllowedCountries   []string `json:"allowed_countries"`
}

type settingsResponse struct {
	Enforce2FAForUsers bool     `json:"enforce_2fa_for_users"`
	AdminAllowlistOnly bool     `json:"admin_allowlist_only"`
	AllowedCountries   []string `json:"allowed_countries"`
	GeoIPAvailable     bool     `json:"geoip_available"`
}

func (h *Handler) toSettingsResponse(st store.SecuritySettings) settingsResponse {
	countries := st.AllowedCountries
	if countries == nil {
		countries = []string{}
	}
	return settingsResponse{
		Enforce2FAForUsers: st.Enforce2FAForUsers, AdminAllowlistOnly: st.AdminAllowlistOnly,
		AllowedCountries: countries, GeoIPAvailable: h.s.GeoAvailable(),
	}
}

func settingsDetails(st store.SecuritySettings) map[string]any {
	return map[string]any{
		"enforce_2fa_for_users": st.Enforce2FAForUsers,
		"admin_allowlist_only":  st.AdminAllowlistOnly,
		"allowed_countries":     st.AllowedCountries,
	}
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := store.LoadSecuritySettings(r.Context(), h.s.q)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.toSettingsResponse(st))
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Enforce2FAForUsers == nil || req.AdminAllowlistOnly == nil || req.AllowedCountries == nil {
		validation(w, r, "enforce_2fa_for_users, admin_allowlist_only and allowed_countries are required")
		return
	}
	if len(req.AllowedCountries) > maxAllowedCountries {
		validation(w, r, "too many allowed countries")
		return
	}
	if len(req.AllowedCountries) > 0 && !h.s.GeoAvailable() {
		validation(w, r, "country filter requires the GeoIP database, which is not available")
		return
	}
	countries := make([]string, 0, len(req.AllowedCountries))
	seen := make(map[string]bool, len(req.AllowedCountries))
	for _, c := range req.AllowedCountries {
		if !countryCodeRE.MatchString(c) {
			validation(w, r, fmt.Sprintf("invalid country code %q (expected ISO 3166-1 alpha-2 in upper case)", c))
			return
		}
		if !seen[c] {
			seen[c] = true
			countries = append(countries, c)
		}
	}
	st := store.SecuritySettings{
		Enforce2FAForUsers: *req.Enforce2FAForUsers,
		AdminAllowlistOnly: *req.AdminAllowlistOnly,
		AllowedCountries:   countries,
	}
	ctx := r.Context()
	now := h.s.clock.Now()
	if reason := h.s.snap.Load().withSettings(st).locksOut(httpx.ClientIP(ctx), now, h.s.geo, h.s.dev); reason != "" {
		conflict(w, r, lockoutMessage(reason))
		return
	}
	before, err := store.LoadSecuritySettings(ctx, h.s.q)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := store.SaveSecuritySettings(ctx, h.s.q, st, now); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.s.reloadOrLog(ctx)
	details := map[string]any{"before": settingsDetails(before), "after": settingsDetails(st)}
	if st.AdminAllowlistOnly {
		n, err := h.s.revokeOffAllowlistAdminSessions(ctx, now)
		if err != nil {
			h.s.log.Error("defense: revoke admin sessions outside the allowlist", "err", err)
		}
		details["revoked_admin_sessions"] = n
	}
	h.record(ctx, audit.Entry{Action: audit.ActionSettingsUpdate, Target: "security", Details: details})
	httpx.WriteJSON(w, http.StatusOK, h.toSettingsResponse(st))
}

// revokeOffAllowlistAdminSessions revokes the admin sessions that were created from an address
// outside the admin allowlist (SEC-16), except the caller's own session. Requests are refused
// per request anyway (auth middleware); revoking also ends such sessions for good and closes
// their console streams.
func (s *Service) revokeOffAllowlistAdminSessions(ctx context.Context, now time.Time) (int, error) {
	own := int64(0)
	if p, ok := principal.From(ctx); ok {
		own = p.SessionID
	}
	sessions, err := s.q.DefenseActiveAdminSessions(ctx, now.UnixMilli())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sess := range sessions {
		if sess.ID == own {
			continue
		}
		if ip, err := netip.ParseAddr(sess.Ip); err == nil && s.CheckAdminNetwork(ctx, ip) == nil {
			continue
		}
		if err := s.q.DefenseRevokeSession(ctx, store.DefenseRevokeSessionParams{
			RevokedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, ID: sess.ID,
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
