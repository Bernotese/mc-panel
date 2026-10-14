package monitoring

import (
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestOverviewFiltersByPermission(t *testing.T) {
	e := newAPIEnv(t)
	a := e.server("alpha")
	b := e.server("beta")
	disk := int64(50 << 30)
	e.src.setLatest(Snapshot{At: t0, Agent: true, Docker: true, Servers: []ServerSample{aLiveRunning(a, 2), aLiveRunning(b, 5)},
		Host: HostSample{DiskFree: &disk}})
	aInsertRaw(t, e.db, a, MetricPlayers, t0.Add(-time.Minute), 2)
	aInsertRaw(t, e.db, a, MetricTPS, t0.Add(-time.Minute), 20) // not in the sparkline
	e.firing.list = []FiringAlert{
		{Kind: alertDiskLow, Since: t0},
		{Kind: alertServerDown, ServerID: b, ServerName: "beta", Since: t0},
	}

	bob := e.user("bob", map[int64][]string{a: {"server.view"}})
	got := decode[overviewJSON](t, e.must(http.StatusOK, http.MethodGet, "/monitoring/overview", bob))
	if len(got.Servers) != 1 || got.Servers[0].ID != a || got.Servers[0].Players == nil || *got.Servers[0].Players != 2 || got.Host != nil {
		t.Fatalf("bob overview = %+v", got)
	}
	s := got.Servers[0]
	if len(s.Sparkline) != 1 || s.Sparkline[0] != [2]float64{float64(t0.Add(-time.Minute).UnixMilli()), 2} || len(s.Alerts) != 0 ||
		*s.CPUPercent != 12.5 || *s.MemoryBytes != 2<<30 || *s.MemoryLimitBytes != 5<<30 {
		t.Fatalf("bob server = %+v", s)
	}

	// a user without any server.view sees an empty list, never the host
	carol := e.user("carol", nil)
	if got := decode[overviewJSON](t, e.must(http.StatusOK, http.MethodGet, "/monitoring/overview", carol)); got.Servers == nil || len(got.Servers) != 0 || got.Host != nil {
		t.Fatalf("carol overview = %+v", got)
	}

	got = decode[overviewJSON](t, e.must(http.StatusOK, http.MethodGet, "/monitoring/overview", e.admin()))
	if len(got.Servers) != 2 || got.Host == nil || got.Host.At == nil || *got.Host.At != t0.UnixMilli() || *got.Host.DiskFree != disk ||
		len(got.Host.Alerts) != 1 || got.Host.Alerts[0] != alertDiskLow {
		t.Fatalf("admin overview = %+v", got)
	}
	for _, s := range got.Servers {
		if s.ID == b && (len(s.Alerts) != 1 || s.Alerts[0] != alertServerDown || len(s.Sparkline) != 0) {
			t.Fatalf("beta = %+v", s)
		}
	}
}

func TestOverviewServerWithoutSample(t *testing.T) {
	e := newAPIEnv(t)
	storetest.CreateServer(t, e.db, "lobby") // no instance, not in the snapshot
	got := decode[overviewJSON](t, e.must(http.StatusOK, http.MethodGet, "/monitoring/overview", e.admin()))
	if len(got.Servers) != 1 || got.Servers[0].Up != nil || got.Servers[0].Players != nil || got.Servers[0].Sparkline == nil ||
		got.Servers[0].Alerts == nil {
		t.Fatalf("overview = %+v", got)
	}
	if got.Host == nil || got.Host.At != nil || got.Host.Alerts == nil {
		t.Fatalf("host before the first pass = %+v", got.Host)
	}
}
