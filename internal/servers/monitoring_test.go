package servers_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type alertsView struct {
	ID         int64 `json:"id"`
	DownAlerts *bool `json:"down_alerts"`
}

// TP5 Abweichung 3: admins see down_alerts (missing row = true), users null.
func TestServerJSONCarriesDownAlertsForAdmins(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	on := createInstance(t, e.db, "survival")
	off := createInstance(t, e.db, "creative")
	if err := store.New(e.db).MonitoringSetServerAlerts(t.Context(), store.MonitoringSetServerAlertsParams{ServerID: off, DownAlerts: 0}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]bool{on: true, off: false} {
		got := decode[alertsView](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &admin))
		if got.DownAlerts == nil || *got.DownAlerts != want {
			t.Errorf("server %d: down_alerts = %v, want %v", id, got.DownAlerts, want)
		}
	}
	list := decode[[]alertsView](t, e.do(http.MethodGet, "/servers", nil, &admin))
	for _, v := range list {
		if v.DownAlerts == nil || *v.DownAlerts != (v.ID != off) {
			t.Errorf("list %d: down_alerts = %v", v.ID, v.DownAlerts)
		}
	}

	uid := storetest.CreateUser(t, e.db, "bob", "user")
	grant(t, e.db, uid, on, "server.view")
	bob := userP(uid, "bob")
	if got := decode[alertsView](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d", on), nil, &bob)); got.DownAlerts != nil {
		t.Fatalf("user sees down_alerts = %v", *got.DownAlerts)
	}
}

// TP5 V2: deleting a server removes its monitoring series and alert states, nothing else.
func TestDeleteRemovesMonitoringData(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	gone := storetest.CreateServer(t, e.db, "gone")
	keep := storetest.CreateServer(t, e.db, "keep")
	q := store.New(e.db)
	ctx := t.Context()
	for _, s := range []int64{gone, keep, 0} {
		if err := q.MonitoringInsertRaw(ctx, store.MonitoringInsertRawParams{Series: s, Metric: "players", At: 1, Value: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: 0, ToAt: 300_000}); err != nil {
		t.Fatal(err)
	}
	if err := q.MonitoringRollup1h(ctx, store.MonitoringRollup1hParams{FromAt: 0, ToAt: 3_600_000}); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{fmt.Sprintf("server.down:%d", gone), fmt.Sprintf("server.crash_loop:%d", gone), fmt.Sprintf("server.down:%d", keep), "host.disk_low"} {
		if err := q.MonitoringUpsertAlertState(ctx, store.MonitoringUpsertAlertStateParams{Key: k, Firing: 1, Since: 1, LastNotified: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []int64{gone, keep} {
		if err := q.MonitoringSetServerAlerts(ctx, store.MonitoringSetServerAlertsParams{ServerID: s, DownAlerts: 0}); err != nil {
			t.Fatal(err)
		}
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", gone), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		table  string
		series int64
		want   int
	}{
		{"metrics_raw", gone, 0}, {"metrics_5m", gone, 0}, {"metrics_1h", gone, 0},
		{"metrics_raw", keep, 1}, {"metrics_5m", keep, 1}, {"metrics_1h", keep, 1},
		{"metrics_raw", 0, 1},
	} {
		var n int
		//nolint:gosec // G202: table name comes from the fixed list above
		if err := e.db.QueryRowContext(ctx, "SELECT count(*) FROM "+tc.table+" WHERE series = ?", tc.series).Scan(&n); err != nil || n != tc.want {
			t.Errorf("%s series %d: %d rows (err %v), want %d", tc.table, tc.series, n, err, tc.want)
		}
	}
	// server_alerts goes by ON DELETE CASCADE
	if rows, err := q.MonitoringListServerAlerts(ctx); err != nil || len(rows) != 1 || rows[0].ServerID != keep {
		t.Fatalf("server_alerts after delete: %+v %v", rows, err)
	}
	st, _ := q.MonitoringListAlertState(ctx)
	keys := []string{}
	for _, s := range st {
		keys = append(keys, s.Key)
	}
	if fmt.Sprint(keys) != fmt.Sprintf("[host.disk_low server.down:%d]", keep) {
		t.Fatalf("alert states after delete: %v", keys)
	}
}
