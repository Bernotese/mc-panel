package defense

import (
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
)

// blockReason returns why ip is blocked by deny rules or the country filter
// ("" if it is not). Allowlisted IPs are never blocked.
func (s *snapshot) blockReason(ip netip.Addr, now time.Time, geo GeoResolver) string {
	ip = normalizeIP(ip)
	if s.allowed(ip, now) {
		return ""
	}
	if s.denied(ip, now) {
		return "deny_rule"
	}
	if len(s.settings.AllowedCountries) > 0 && !isLocal(ip) {
		if c := geo.Country(ip); c != "" && !slices.Contains(s.settings.AllowedCountries, c) {
			return "country:" + c
		}
	}
	return ""
}

// IPFilter rejects jailed and blocked clients for every request (API and static
// files). It only reads the in-memory snapshot.
func (s *Service) IPFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := normalizeIP(httpx.ClientIP(r.Context()))
		if !ip.IsValid() {
			// fail closed: without a usable client IP no rule or jail can be evaluated
			httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeIPBlocked, "access from your network is not allowed")
			return
		}
		now := s.clock.Now()
		snap := s.snap.Load()
		if snap.allowed(ip, now) {
			next.ServeHTTP(w, r)
			return
		}
		if until, ok := snap.jailedUntil(httpx.IPKey(ip), now); ok {
			w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds(until.Sub(now))))
			httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeJailed,
				"too many failed login attempts; access is temporarily blocked")
			return
		}
		if reason := snap.blockReason(ip, now, s.geo); reason != "" {
			httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeIPBlocked, "access from your network is not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// APIRateLimit limits /api/ requests per session (or per IP key for anonymous requests).
func (s *Service) APIRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		key := "ip:invalid" // dedicated bucket for requests without a usable client IP
		if k := httpx.IPKey(normalizeIP(httpx.ClientIP(r.Context()))); k.IsValid() {
			key = "ip:" + k.String()
		}
		if p, ok := principal.From(r.Context()); ok && p.SessionID != 0 {
			key = "session:" + strconv.FormatInt(p.SessionID, 10)
		}
		if d := s.api.take(key, s.clock.Now()); d > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds(d)))
			httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}
