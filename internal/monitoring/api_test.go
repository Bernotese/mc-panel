package monitoring

import (
	"fmt"
	"net/http"
	"testing"
)

// Phase A stubs answer valid empty structures (B2 replaces this file).
func TestStubReadRoutes(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := e.server("survival")
	for _, r := range []string{"1h", "24h", "7d", "30d"} {
		got := decode[seriesJSON](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=%s", id, r), nil, &admin))
		if got.Range != r || got.StepMS != stepFor(r).Milliseconds() || got.Series == nil {
			t.Errorf("metrics %s = %+v", r, got)
		}
	}
	if rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=2h", id), nil, &admin); rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("bad range: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodGet, "/servers/999/metrics?range=1h", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown server: %d", rec.Code)
	}
	live := decode[liveJSON](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d/metrics/live", id), nil, &admin))
	if live.At != nil || live.Up != nil || live.PlayerNames == nil || live.TPSSource != TPSSourceUnknown {
		t.Fatalf("live = %+v", live)
	}
	ov := decode[overviewJSON](t, e.do(http.MethodGet, "/monitoring/overview", nil, &admin))
	if ov.Servers == nil || ov.Host == nil || ov.Host.Alerts == nil {
		t.Fatalf("overview (admin) = %+v", ov)
	}
	bob := e.user("bob", map[int64][]string{id: {"server.view"}})
	if ov := decode[overviewJSON](t, e.do(http.MethodGet, "/monitoring/overview", nil, &bob)); ov.Host != nil {
		t.Fatalf("overview (user) has host values: %+v", ov.Host)
	}
	if got := decode[seriesJSON](t, e.do(http.MethodGet, "/monitoring/host?range=24h", nil, &admin)); got.StepMS != 300_000 {
		t.Fatalf("host = %+v", got)
	}
}
