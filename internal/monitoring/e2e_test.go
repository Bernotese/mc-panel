package monitoring

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// TestEndToEndFirstPass runs NewHandler + Run with the real collector against the shared fakes
// (helpers_test.go: fakeAgent, fakeConsole, pingServer) and reads the result through the API.
func TestEndToEndFirstPass(t *testing.T) {
	slp := newPingServer(t)
	e := newEnv(t, func(d *Deps) { d.PingAddr = slp.addr(); d.InstancesDir = t.TempDir() })
	ctx := context.Background()
	id := e.server("survival")
	for _, q := range []string{
		fmt.Sprintf(`UPDATE instances SET desired_state = 'running' WHERE server_id = %d`, id),
		`UPDATE instance_settings SET base_domain = 'mc.example.com' WHERE id = 1`,
		// the disk of the test machine must not fire host.disk_low
		`UPDATE monitoring_settings SET disk_min_percent = 1, disk_min_bytes = 1073741824 WHERE id = 1`,
	} {
		if _, err := e.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	slp.set("survival.mc.example.com", pingAnswer{json: statusJSON(3, 20, "Alex")})
	cpu := 12.5
	e.agent.set(agentapi.InstanceStatus{
		ID: id, Name: "survival", Revision: 1, State: agentapi.StateRunning,
		StartedAt: t0.Add(-time.Hour).UnixMilli(), MemoryBytes: 2 << 30, MemoryLimitBytes: 5 << 30, CPUPercent: cpu,
	})
	e.console.reply(id, "tick query", "The game is running normally\nTarget tick rate: 20.0 per second.\nAverage time per tick: 1.2ms (Target: 50.0ms)", nil)

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { e.h.Run(runCtx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	admin := e.admin()
	livePath := fmt.Sprintf("/servers/%d/metrics/live", id)
	var live liveJSON
	eventually(t, "the first pass", func() bool {
		rec := e.do(http.MethodGet, livePath, nil, &admin)
		if rec.Code != http.StatusOK {
			t.Fatalf("live = %d %s", rec.Code, rec.Body)
		}
		live = decode[liveJSON](t, rec)
		return live.Players != nil
	})
	if *live.Players != 3 || live.PlayersMax == nil || *live.PlayersMax != 20 || live.Up == nil || !*live.Up ||
		live.CPUPercent == nil || *live.CPUPercent != cpu || live.At == nil || *live.At != t0.UnixMilli() ||
		!slices.Equal(live.PlayerNames, []string{"Alex"}) {
		t.Fatalf("live = %+v", live)
	}
	if slp.hits("survival.mc.example.com") == 0 {
		t.Fatal("the collector never pinged survival.mc.example.com")
	}
	metrics := decode[metricsJSON](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d/metrics?range=1h", id), nil, &admin))
	if p := metrics.Series[MetricPlayers]; len(p) != 1 || p[0] != (point{float64(t0.UnixMilli()), 3, 3, 3}) {
		t.Fatalf("stored players = %+v", p)
	}
	if p := metrics.Series[MetricUp]; len(p) != 1 || p[0][1] != 1 {
		t.Fatalf("stored up = %+v", p)
	}
	ov := decode[overviewJSON](t, e.do(http.MethodGet, "/monitoring/overview", nil, &admin))
	if len(ov.Servers) != 1 || ov.Servers[0].Players == nil || *ov.Servers[0].Players != 3 || len(ov.Servers[0].Sparkline) != 1 ||
		len(ov.Servers[0].Alerts) != 0 || ov.Host == nil || ov.Host.At == nil || ov.Host.DiskTotal == nil || *ov.Host.DiskTotal <= 0 {
		t.Fatalf("overview = %+v", ov)
	}
	settings := decode[settingsJSON](t, e.do(http.MethodGet, "/monitoring/settings", nil, &admin))
	if len(settings.Alerts) != 0 {
		t.Fatalf("a healthy server fires alerts: %+v", settings.Alerts)
	}
	if got := e.notes.Types(); len(got) != 0 {
		t.Fatalf("a healthy server sent notifications: %v", got)
	}
}
