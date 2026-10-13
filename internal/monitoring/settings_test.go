package monitoring

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store"
)

func validSettings() map[string]any {
	return map[string]any{"disk_min_percent": 15, "disk_min_bytes": 20 << 30, "down_after_min": 5, "crash_restarts": 4, "crash_window_min": 15}
}

func TestSettingsDefaultsAndUpdate(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	got := decode[settingsJSON](t, e.do(http.MethodGet, "/monitoring/settings", nil, &admin))
	if got.DiskMinPercent != 10 || got.DiskMinBytes != 10<<30 || got.DownAfterMin != 3 || got.CrashRestarts != 3 ||
		got.CrashWindowMin != 10 || got.Alerts == nil || len(got.Alerts) != 0 {
		t.Fatalf("defaults = %+v", got)
	}
	rec := e.do(http.MethodPut, "/monitoring/settings", validSettings(), &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	got = decode[settingsJSON](t, rec)
	if got.DiskMinPercent != 15 || got.DiskMinBytes != 20<<30 || got.DownAfterMin != 5 || got.CrashRestarts != 4 || got.CrashWindowMin != 15 {
		t.Fatalf("after put = %+v", got)
	}
	row, err := store.New(e.db).MonitoringGetSettings(t.Context())
	if err != nil || row.DiskMinPercent != 15 || row.UpdatedAt != t0.UnixMilli() {
		t.Fatalf("row = %+v %v", row, err)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if last.Action != audit.ActionMonitoringSettingsUpdate || last.Target != auditTarget || last.Result != audit.ResultSuccess {
		t.Fatalf("audit = %+v", last)
	}
	from, okFrom := last.Details["from"].(updateSettingsRequest)
	to, okTo := last.Details["to"].(updateSettingsRequest)
	if !okFrom || !okTo || from.DiskMinPercent != 10 || to.DiskMinPercent != 15 || to.CrashWindowMin != 15 {
		t.Fatalf("audit details = %v", last.Details)
	}
}

func TestSettingsValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	for field, bad := range map[string][]int64{
		"disk_min_percent": {0, 51},
		"disk_min_bytes":   {1<<30 - 1, 10<<40 + 1},
		"down_after_min":   {0, 61},
		"crash_restarts":   {1, 21},
		"crash_window_min": {0, 121},
	} {
		for _, v := range bad {
			body := validSettings()
			body[field] = v
			rec := e.do(http.MethodPut, "/monitoring/settings", body, &admin)
			if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
				t.Errorf("%s=%d: %d %s", field, v, rec.Code, rec.Body)
			}
		}
	}
	body := validSettings()
	body["extra"] = 1
	if rec := e.do(http.MethodPut, "/monitoring/settings", body, &admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", rec.Code)
	}
	if n := len(e.rec.Entries); n != 0 {
		t.Fatalf("rejected updates were audited: %d", n)
	}
}

func TestServerAlertsSwitch(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := e.server("survival")
	path := fmt.Sprintf("/servers/%d/alerts", id)
	if rec := e.do(http.MethodPut, path, map[string]any{"down_alerts": false}, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if v, err := store.New(e.db).MonitoringGetServerAlerts(t.Context(), id); err != nil || v != 0 {
		t.Fatalf("row = %d %v", v, err)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if on, ok := last.Details["down_alerts"].(bool); last.Action != audit.ActionServerAlertsUpdate || last.Target != serverTarget(id) || !ok || on {
		t.Fatalf("audit = %+v", last)
	}
	if rec := e.do(http.MethodPut, path, map[string]any{}, &admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing down_alerts: %d", rec.Code)
	}
	if rec := e.do(http.MethodPut, "/servers/999/alerts", map[string]any{"down_alerts": true}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown server: %d", rec.Code)
	}
}

func TestFiringJSON(t *testing.T) {
	since := t0.Add(-time.Minute)
	got := firingJSON([]FiringAlert{
		{Kind: alertServerDown, ServerID: 7, ServerName: "survival", Since: since, Detail: "not reachable"},
		{Kind: alertDiskLow, Since: since, Detail: "3 GiB free"},
	})
	if len(got) != 2 || *got[0].ServerID != 7 || *got[0].ServerName != "survival" || got[0].Since != since.UnixMilli() ||
		got[1].ServerID != nil || got[1].ServerName != nil {
		t.Fatalf("firingJSON = %+v", got)
	}
	if firingJSON(nil) == nil {
		t.Fatal("firingJSON(nil) must be an empty slice")
	}
	if alertKey(alertServerDown, 7) != "server.down:7" || alertKey(alertDiskLow, 0) != "host.disk_low" {
		t.Fatal("alertKey format differs from the keys servers deletes (V2)")
	}
}
