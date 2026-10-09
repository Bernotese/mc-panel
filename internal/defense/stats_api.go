package defense

import (
	"context"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"mcpanel/internal/httpx"
	"mcpanel/internal/store"
)

type rangeSpec struct{ total, bucket time.Duration }

var overviewRanges = map[string]rangeSpec{
	"24h": {24 * time.Hour, time.Hour},
	"7d":  {7 * 24 * time.Hour, 6 * time.Hour},
	"30d": {30 * 24 * time.Hour, 24 * time.Hour},
}

const (
	defaultAttemptLimit = 50
	maxAttemptLimit     = 200
)

type seriesPoint struct {
	T       int64 `json:"t"`
	Success int64 `json:"success"`
	Failure int64 `json:"failure"`
}

type ipCount struct {
	IPKey    string `json:"ip_key"`
	Country  string `json:"country"`
	Failures int64  `json:"failures"`
}

type countryCount struct {
	Country  string `json:"country"`
	Failures int64  `json:"failures"`
}

type newDeviceLogin struct {
	Ts        int64  `json:"ts"`
	Username  string `json:"username"`
	IP        string `json:"ip"`
	Country   string `json:"country"`
	UserAgent string `json:"user_agent"`
}

type overviewResponse struct {
	Range           string           `json:"range"`
	Series          []seriesPoint    `json:"series"`
	TopIPs          []ipCount        `json:"top_ips"`
	TopCountries    []countryCount   `json:"top_countries"`
	ActiveJails     int64            `json:"active_jails"`
	FailuresTotal   int64            `json:"failures_total"`
	NewDeviceLogins []newDeviceLogin `json:"new_device_logins"`
}

// seriesWindow returns the first bucket start (Unix ms), the number of buckets
// and the bucket length (ms); buckets are aligned to the Unix epoch and the last one contains now.
func seriesWindow(now time.Time, total, bucket time.Duration) (start, n, bucketMs int64) {
	bucketMs = bucket.Milliseconds()
	n = int64(total / bucket)
	start = (now.UnixMilli()/bucketMs)*bucketMs - (n-1)*bucketMs
	return start, n, bucketMs
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "24h"
	}
	spec, ok := overviewRanges[rng]
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "range must be 24h, 7d or 30d")
		return
	}
	resp, err := h.buildOverview(r.Context(), rng, spec)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) buildOverview(ctx context.Context, rng string, spec rangeSpec) (overviewResponse, error) {
	now := h.s.clock.Now()
	start, n, bucketMs := seriesWindow(now, spec.total, spec.bucket)
	resp := overviewResponse{
		Range: rng, Series: make([]seriesPoint, n), TopIPs: []ipCount{},
		TopCountries: []countryCount{}, NewDeviceLogins: []newDeviceLogin{},
	}
	for i := range resp.Series {
		resp.Series[i].T = start + int64(i)*bucketMs
	}
	rows, err := h.s.q.DefenseAttemptSeries(ctx, store.DefenseAttemptSeriesParams{Start: start, BucketMs: bucketMs, Since: start})
	if err != nil {
		return resp, err
	}
	for _, row := range rows {
		if row.Bucket >= 0 && row.Bucket < n {
			resp.Series[row.Bucket].Success = row.Successes
			resp.Series[row.Bucket].Failure = row.Failures
		}
	}
	ips, err := h.s.q.DefenseTopFailingIPs(ctx, start)
	if err != nil {
		return resp, err
	}
	for _, row := range ips {
		resp.TopIPs = append(resp.TopIPs, ipCount{IPKey: row.IpKey, Country: row.Country, Failures: row.Failures})
	}
	countries, err := h.s.q.DefenseTopFailingCountries(ctx, start)
	if err != nil {
		return resp, err
	}
	for _, row := range countries {
		resp.TopCountries = append(resp.TopCountries, countryCount{Country: row.Country, Failures: row.Failures})
	}
	if resp.FailuresTotal, err = h.s.q.DefenseCountFailuresTotal(ctx, start); err != nil {
		return resp, err
	}
	if resp.ActiveJails, err = h.s.q.DefenseCountActiveJails(ctx, now.UnixMilli()); err != nil {
		return resp, err
	}
	devices, err := h.s.q.DefenseNewDeviceLogins(ctx, start)
	if err != nil {
		return resp, err
	}
	for _, row := range devices {
		resp.NewDeviceLogins = append(resp.NewDeviceLogins, newDeviceLogin{
			Ts: row.Ts, Username: row.Username, IP: row.Ip, Country: row.Country, UserAgent: row.UserAgent,
		})
	}
	return resp, nil
}

type attemptResponse struct {
	ID        int64  `json:"id"`
	Ts        int64  `json:"ts"`
	IP        string `json:"ip"`
	Username  string `json:"username"`
	Stage     string `json:"stage"`
	Success   bool   `json:"success"`
	Reason    string `json:"reason"`
	Country   string `json:"country"`
	UserAgent string `json:"user_agent"`
}

type attemptPage struct {
	Items      []attemptResponse `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

// normalizeIPFilter accepts a single IP (→ unmapped string) or a prefix (→ masked string).
func normalizeIPFilter(v string) (string, bool) {
	if a, err := netip.ParseAddr(v); err == nil {
		return normalizeIP(a).String(), true
	}
	if p, err := netip.ParsePrefix(v); err == nil && !p.Addr().Is4In6() {
		return p.Masked().String(), true
	}
	return "", false
}

func (h *Handler) attempts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bad := func(msg string) { httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, msg) }
	limit := int64(defaultAttemptLimit)
	if v := q.Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > maxAttemptLimit {
			bad("limit must be between 1 and 200")
			return
		}
		limit = n
	}
	before := int64(math.MaxInt64)
	if v := q.Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			bad("invalid cursor")
			return
		}
		before = n
	}
	anySuccess, success := int64(1), int64(0)
	if v := q.Get("success"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			bad("success must be true or false")
			return
		}
		anySuccess, success = 0, boolInt(b)
	}
	anyIP, ipFilter := int64(1), ""
	if v := strings.TrimSpace(q.Get("ip")); v != "" {
		f, ok := normalizeIPFilter(v)
		if !ok {
			bad("ip must be an IP address or prefix")
			return
		}
		anyIP, ipFilter = 0, f
	}
	rows, err := h.s.q.DefenseListAttempts(r.Context(), store.DefenseListAttemptsParams{
		BeforeID: before, AnySuccess: anySuccess, Success: success,
		AnyIp: anyIP, Ip: ipFilter, IpKey: ipFilter, Lim: limit + 1,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	page := attemptPage{Items: make([]attemptResponse, 0, len(rows))}
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		c := strconv.FormatInt(rows[len(rows)-1].ID, 10)
		page.NextCursor = &c
	}
	for _, a := range rows {
		page.Items = append(page.Items, attemptResponse{
			ID: a.ID, Ts: a.Ts, IP: a.Ip, Username: a.Username, Stage: a.Stage, Success: a.Success != 0,
			Reason: a.Reason, Country: a.Country, UserAgent: a.UserAgent,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

type agentStatusResponse struct {
	Reachable   bool   `json:"reachable"`
	Firewall    string `json:"firewall"`
	Version     string `json:"version"`
	PendingSync int64  `json:"pending_sync"`
}

func (h *Handler) agentStatus(w http.ResponseWriter, r *http.Request) {
	var resp agentStatusResponse
	if h.s.agent != nil {
		ctx, cancel := context.WithTimeout(r.Context(), AgentStatusTimeout)
		hr, err := h.s.agent.Health(ctx)
		cancel()
		if err == nil {
			resp.Reachable, resp.Firewall, resp.Version = true, hr.Firewall, hr.Version
		}
	}
	n, err := h.s.q.DefenseCountPendingSync(r.Context(), h.s.clock.Now().UnixMilli())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	resp.PendingSync = n
	httpx.WriteJSON(w, http.StatusOK, resp)
}
