package defense

import (
	"database/sql"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"mcpanel/internal/audit"
	"mcpanel/internal/httpx"
	"mcpanel/internal/store"
)

const (
	maxRuleCommentLen = 200
	minAllowBitsV4    = 8
	minAllowBitsV6    = 32
)

var errBadCIDR = errors.New("cidr must be an IPv4 or IPv6 address or network, e.g. 203.0.113.0/24")

func parseRuleCIDR(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	var p netip.Prefix
	if strings.Contains(s, "/") {
		var err error
		if p, err = netip.ParsePrefix(s); err != nil {
			return netip.Prefix{}, errBadCIDR
		}
	} else {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return netip.Prefix{}, errBadCIDR
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	if p.Addr().Is4In6() {
		return netip.Prefix{}, errors.New("use plain IPv4 notation instead of IPv4-mapped IPv6")
	}
	return p.Masked(), nil
}

type ipRuleResponse struct {
	ID        int64  `json:"id"`
	CIDR      string `json:"cidr"`
	Kind      string `json:"kind"`
	Comment   string `json:"comment"`
	ExpiresAt *int64 `json:"expires_at"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
}

func toIPRuleResponse(r store.IpRule) ipRuleResponse {
	return ipRuleResponse{
		ID: r.ID, CIDR: r.Cidr, Kind: r.Kind, Comment: r.Comment, ExpiresAt: nullInt64Ptr(r.ExpiresAt),
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
	}
}

func (h *Handler) listIPRules(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.q.DefenseListIPRules(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]ipRuleResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toIPRuleResponse(row))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createIPRuleRequest struct {
	CIDR      string `json:"cidr"`
	Kind      string `json:"kind"`
	Comment   string `json:"comment"`
	ExpiresAt *int64 `json:"expires_at"`
}

func (h *Handler) createIPRule(w http.ResponseWriter, r *http.Request) {
	var req createIPRuleRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	p, err := parseRuleCIDR(req.CIDR)
	if err != nil {
		validation(w, r, err.Error())
		return
	}
	if req.Kind != "allow" && req.Kind != "deny" {
		validation(w, r, "kind must be allow or deny")
		return
	}
	if req.Kind == "allow" && (p.Addr().Is4() && p.Bits() < minAllowBitsV4 || p.Addr().Is6() && p.Bits() < minAllowBitsV6) {
		// an allow rule for (nearly) everything would disable jails, deny rules and the country filter
		validation(w, r, "allow rules must cover at most a /8 (IPv4) or /32 (IPv6) network")
		return
	}
	comment := strings.TrimSpace(req.Comment)
	if utf8.RuneCountInString(comment) > maxRuleCommentLen {
		validation(w, r, "comment must be at most 200 characters")
		return
	}
	ctx := r.Context()
	now := h.s.clock.Now()
	entry := ruleEntry{prefix: p}
	var exp sql.NullInt64
	if req.ExpiresAt != nil {
		if *req.ExpiresAt <= now.UnixMilli() {
			validation(w, r, "expires_at must be in the future")
			return
		}
		exp = sql.NullInt64{Int64: *req.ExpiresAt, Valid: true}
		entry.expires = time.UnixMilli(*req.ExpiresAt)
	}
	if reason := h.s.snap.Load().withRule(req.Kind, entry).locksOut(httpx.ClientIP(ctx), now, h.s.geo, h.s.dev); reason != "" {
		conflict(w, r, lockoutMessage(reason))
		return
	}
	row, err := h.s.q.DefenseInsertIPRule(ctx, store.DefenseInsertIPRuleParams{
		Cidr: p.String(), Kind: req.Kind, Comment: comment, ExpiresAt: exp,
		CreatedBy: callerName(ctx), CreatedAt: now.UnixMilli(),
	})
	if store.IsUniqueViolation(err) {
		conflict(w, r, "a rule for this network and kind already exists")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.s.reloadOrLog(ctx)
	if req.Kind == "allow" {
		h.s.kickSync() // firewall bans overlapping the new allow rule must be removed
	}
	h.record(ctx, audit.Entry{
		Action: audit.ActionIPRuleCreate, Target: row.Cidr,
		Details: map[string]any{"rule_id": row.ID, "kind": row.Kind, "comment": row.Comment, "expires_at": nullInt64Ptr(row.ExpiresAt)},
	})
	httpx.WriteJSON(w, http.StatusCreated, toIPRuleResponse(row))
}

func (h *Handler) deleteIPRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "ip rule")
	if !ok {
		return
	}
	ctx := r.Context()
	row, err := h.s.q.DefenseGetIPRule(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "ip rule not found")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.s.clock.Now()
	if reason := h.s.snap.Load().withoutRule(id).locksOut(httpx.ClientIP(ctx), now, h.s.geo, h.s.dev); reason != "" {
		conflict(w, r, lockoutMessage(reason))
		return
	}
	n, err := h.s.q.DefenseDeleteIPRule(ctx, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "ip rule not found")
		return
	}
	h.s.reloadOrLog(ctx)
	h.record(ctx, audit.Entry{
		Action: audit.ActionIPRuleDelete, Target: row.Cidr,
		Details: map[string]any{"rule_id": row.ID, "kind": row.Kind},
	})
	w.WriteHeader(http.StatusNoContent)
}
