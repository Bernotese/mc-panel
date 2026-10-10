package defense

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type overviewJSON struct {
	Range  string `json:"range"`
	Series []struct {
		T       int64 `json:"t"`
		Success int64 `json:"success"`
		Failure int64 `json:"failure"`
	} `json:"series"`
	TopIPs []struct {
		IPKey    string `json:"ip_key"`
		Country  string `json:"country"`
		Failures int64  `json:"failures"`
	} `json:"top_ips"`
	TopCountries []struct {
		Country  string `json:"country"`
		Failures int64  `json:"failures"`
	} `json:"top_countries"`
	ActiveJails     int64 `json:"active_jails"`
	FailuresTotal   int64 `json:"failures_total"`
	NewDeviceLogins []struct {
		Ts        int64  `json:"ts"`
		Username  string `json:"username"`
		IP        string `json:"ip"`
		Country   string `json:"country"`
		UserAgent string `json:"user_agent"`
	} `json:"new_device_logins"`
}

func TestOverview24h(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.geo["203.0.113.7"] = "US"
	e.geo["198.51.100.1"] = "DE"
	uid := storetest.CreateUser(t, e.db, "alice", "user")

	e.clk.Set(t0.Add(-25 * time.Hour))
	e.fail(t, "203.0.113.9", 1) // outside the range
	e.clk.Set(t0.Add(-30 * time.Minute))
	e.fail(t, "203.0.113.7", 3)
	e.fail(t, "198.51.100.1", 1)
	e.clk.Set(t0)
	for i := 0; i < 2; i++ {
		if err := e.svc.RecordAttempt(ctx, Attempt{IP: addr("198.51.100.1"), Username: "alice", UserID: &uid,
			Stage: StageTOTP, Success: true, Reason: ReasonComplete, UserAgent: "Firefox"}); err != nil {
			t.Fatal(err)
		}
	}
	e.insertJail(t, "192.0.2.1/32", t0, t0.Add(time.Hour))

	rec := e.call(t, http.MethodGet, "/security/overview", nil, callerIP)
	wantStatus(t, rec, http.StatusOK, "")
	type point struct{ T, Success, Failure int64 }
	got := decode[overviewJSON](t, rec)
	start := t0.Add(-23 * time.Hour).UnixMilli()
	if got.Range != "24h" || len(got.Series) != 24 || got.Series[0].T != start ||
		got.Series[23].T != t0.UnixMilli() {
		t.Fatalf("range/series = %q len=%d first=%d", got.Range, len(got.Series), got.Series[0].T)
	}
	if p := got.Series[22]; (point{p.T, p.Success, p.Failure}) != (point{t0.Add(-time.Hour).UnixMilli(), 0, 4}) {
		t.Fatalf("series[22] = %+v", p)
	}
	if p := got.Series[23]; p.Success != 2 || p.Failure != 0 {
		t.Fatalf("series[23] = %+v", p)
	}
	if got.FailuresTotal != 4 || got.ActiveJails != 1 {
		t.Fatalf("failures_total=%d active_jails=%d", got.FailuresTotal, got.ActiveJails)
	}
	if len(got.TopIPs) != 2 || got.TopIPs[0].IPKey != "203.0.113.7/32" || got.TopIPs[0].Country != "US" ||
		got.TopIPs[0].Failures != 3 || got.TopIPs[1].IPKey != "198.51.100.1/32" {
		t.Fatalf("top_ips = %+v", got.TopIPs)
	}
	if len(got.TopCountries) != 2 || got.TopCountries[0].Country != "US" || got.TopCountries[1].Failures != 1 {
		t.Fatalf("top_countries = %+v", got.TopCountries)
	}
	if len(got.NewDeviceLogins) != 1 || got.NewDeviceLogins[0].Username != "alice" ||
		got.NewDeviceLogins[0].IP != "198.51.100.1" || got.NewDeviceLogins[0].Country != "DE" ||
		got.NewDeviceLogins[0].UserAgent != "Firefox" {
		t.Fatalf("new_device_logins = %+v", got.NewDeviceLogins)
	}
}

func TestOverviewRanges(t *testing.T) {
	e := newEnv(t)
	for rng, n := range map[string]int{"7d": 28, "30d": 30} {
		got := decode[map[string]any](t, e.call(t, http.MethodGet, "/security/overview?range="+rng, nil, callerIP))
		series, _ := got["series"].([]any)
		if got["range"] != rng || len(series) != n {
			t.Errorf("%s: range=%v len=%d", rng, got["range"], len(series))
		}
		for _, k := range []string{"top_ips", "top_countries", "new_device_logins"} {
			if arr, ok := got[k].([]any); !ok || arr == nil {
				t.Errorf("%s: %s must be an array, got %#v", rng, k, got[k])
			}
		}
	}
	wantStatus(t, e.call(t, http.MethodGet, "/security/overview?range=1y", nil, callerIP), http.StatusBadRequest, httpx.CodeBadRequest)
}

func TestSeriesWindow(t *testing.T) {
	now := time.Date(2026, 10, 8, 14, 37, 0, 0, time.UTC)
	start, n, bucket := seriesWindow(now, 7*24*time.Hour, 6*time.Hour)
	wantStart := time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC).UnixMilli()
	if n != 28 || bucket != (6*time.Hour).Milliseconds() || start != wantStart {
		t.Fatalf("start=%v n=%d bucket=%d", time.UnixMilli(start).UTC(), n, bucket)
	}
}

func TestListAttempts(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 3)     // ids 1-3
	e.fail(t, "2001:db8:1:2::1", 2) // ids 4-5
	if err := e.svc.RecordAttempt(context.Background(), Attempt{IP: addr("198.51.100.1"), Username: "bob",
		Stage: StagePassword, Success: true}); err != nil { // id 6
		t.Fatal(err)
	}
	type page struct {
		Items []struct {
			ID      int64  `json:"id"`
			IP      string `json:"ip"`
			Stage   string `json:"stage"`
			Success bool   `json:"success"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	ids := func(p page) []int64 {
		out := []int64{}
		for _, it := range p.Items {
			out = append(out, it.ID)
		}
		return out
	}
	p1 := decode[page](t, e.call(t, http.MethodGet, "/security/attempts?limit=4", nil, callerIP))
	if got := ids(p1); len(got) != 4 || got[0] != 6 || got[3] != 3 || p1.NextCursor == nil || *p1.NextCursor != "3" {
		t.Fatalf("page 1 = %v next=%v", got, p1.NextCursor)
	}
	if !p1.Items[0].Success || p1.Items[0].Stage != "password" || p1.Items[0].IP != "198.51.100.1" {
		t.Fatalf("item = %+v", p1.Items[0])
	}
	p2 := decode[page](t, e.call(t, http.MethodGet, "/security/attempts?limit=4&cursor=3", nil, callerIP))
	if got := ids(p2); len(got) != 2 || got[0] != 2 || got[1] != 1 || p2.NextCursor != nil {
		t.Fatalf("page 2 = %v next=%v", got, p2.NextCursor)
	}
	for q, want := range map[string]int{
		"success=true":                 1,
		"success=false":                5,
		"ip=203.0.113.7":               3,
		"ip=::ffff:203.0.113.7":        3,
		"ip=2001:db8:1:2::/64":         2,
		"ip=2001:db8:1:2::1&success=0": 2,
	} {
		if got := decode[page](t, e.call(t, http.MethodGet, "/security/attempts?"+q, nil, callerIP)); len(got.Items) != want {
			t.Errorf("%s: %d items, want %d", q, len(got.Items), want)
		}
	}
	for _, q := range []string{"limit=0", "limit=201", "cursor=abc", "cursor=0", "success=maybe", "ip=nope"} {
		wantStatus(t, e.call(t, http.MethodGet, "/security/attempts?"+q, nil, callerIP), http.StatusBadRequest, httpx.CodeBadRequest)
	}
}

func TestAgentStatus(t *testing.T) {
	t.Run("no agent configured", func(t *testing.T) {
		e := newEnv(t)
		e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
		got := decode[map[string]any](t, e.call(t, http.MethodGet, "/security/agent", nil, callerIP))
		if got["reachable"] != jsonFalse || got["firewall"] != "" || got["version"] != "" || got["pending_sync"] != float64(1) {
			t.Fatalf("status = %v", got)
		}
	})
	t.Run("healthy agent", func(t *testing.T) {
		fa := newFakeAgent()
		e := newEnv(t, withAgent(fa))
		got := decode[map[string]any](t, e.call(t, http.MethodGet, "/security/agent", nil, callerIP))
		if got["reachable"] != jsonTrue || got["firewall"] != "noop" || got["version"] != "test" || got["pending_sync"] != float64(0) {
			t.Fatalf("status = %v", got)
		}
	})
	t.Run("agent down", func(t *testing.T) {
		fa := newFakeAgent()
		fa.setErr(errAgentDown)
		e := newEnv(t, withAgent(fa))
		rec := e.call(t, http.MethodGet, "/security/agent", nil, callerIP)
		wantStatus(t, rec, http.StatusOK, "")
		if got := decode[map[string]any](t, rec); got["reachable"] != jsonFalse {
			t.Fatalf("status = %v", got)
		}
	})
}

func TestAgentStatusPendingSyncIgnoresJailsMaskedByAllowRule(t *testing.T) {
	e := newEnv(t)
	e.insertJail(t, "2001:db8:1:2::/64", t0, t0.Add(time.Hour))
	e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
	e.addRule(t, "2001:db8:1:2::5/128", "allow", nil) // masks the /64: app-only by design
	got := decode[map[string]any](t, e.call(t, http.MethodGet, "/security/agent", nil, callerIP))
	if got["pending_sync"] != float64(1) {
		t.Fatalf("pending_sync = %v, want 1", got["pending_sync"])
	}
}
