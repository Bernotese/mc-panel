package store_test

import (
	"math"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const (
	min5 = int64(300_000)
	hour = int64(3_600_000)
)

func insertRaw(t *testing.T, q *store.Queries, series int64, metric string, at int64, v float64) {
	t.Helper()
	if err := q.MonitoringInsertRaw(t.Context(), store.MonitoringInsertRawParams{Series: series, Metric: metric, At: at, Value: v}); err != nil {
		t.Fatal(err)
	}
}

func TestMonitoringRawRollupAndPrune(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	base := 100 * hour // aligned to an hour
	// series 7 players: 1, 3 in the first 5 min; 5 in the second; host cpu in the first
	insertRaw(t, q, 7, "players", base, 1)
	insertRaw(t, q, 7, "players", base+30_000, 3)
	insertRaw(t, q, 7, "players", base+min5, 5)
	insertRaw(t, q, 0, "cpu_percent", base, 50)
	// INSERT OR REPLACE: the same point twice keeps the newer value
	insertRaw(t, q, 7, "players", base+30_000, 3)

	raw, err := q.MonitoringListRaw(ctx, store.MonitoringListRawParams{Series: 7, FromAt: base, ToAt: base + hour})
	if err != nil || len(raw) != 3 || raw[0].At != base || raw[2].Value != 5 {
		t.Fatalf("raw = %+v %v", raw, err)
	}

	if err := q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: base, ToAt: base + hour}); err != nil {
		t.Fatal(err)
	}
	// idempotent
	if err := q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: base, ToAt: base + hour}); err != nil {
		t.Fatal(err)
	}
	m5, err := q.MonitoringList5m(ctx, store.MonitoringList5mParams{Series: 7, FromAt: base, ToAt: base + hour})
	if err != nil || len(m5) != 2 {
		t.Fatalf("5m = %+v %v", m5, err)
	}
	if m5[0].At != base || m5[0].Avg != 2 || m5[0].Min != 1 || m5[0].Max != 3 || m5[1].At != base+min5 || m5[1].Avg != 5 {
		t.Fatalf("5m values = %+v", m5)
	}

	if err := q.MonitoringRollup1h(ctx, store.MonitoringRollup1hParams{FromAt: base, ToAt: base + hour}); err != nil {
		t.Fatal(err)
	}
	h1, err := q.MonitoringList1h(ctx, store.MonitoringList1hParams{Series: 7, FromAt: base, ToAt: base + hour})
	if err != nil || len(h1) != 1 {
		t.Fatalf("1h = %+v %v", h1, err)
	}
	// weighted by samples: (2*2 + 5*1) / 3 = 3
	if math.Abs(h1[0].Avg-3) > 1e-9 || h1[0].Min != 1 || h1[0].Max != 5 {
		t.Fatalf("1h values = %+v", h1[0])
	}

	if err := q.MonitoringPruneRaw(ctx, base+min5); err != nil {
		t.Fatal(err)
	}
	raw, _ = q.MonitoringListRaw(ctx, store.MonitoringListRawParams{Series: 7, FromAt: 0, ToAt: math.MaxInt64})
	if len(raw) != 1 || raw[0].At != base+min5 {
		t.Fatalf("after prune: %+v", raw)
	}
	if err := q.MonitoringPrune5m(ctx, base+hour); err != nil {
		t.Fatal(err)
	}
	if m5, _ = q.MonitoringList5m(ctx, store.MonitoringList5mParams{Series: 7, FromAt: 0, ToAt: math.MaxInt64}); len(m5) != 0 {
		t.Fatalf("5m after prune: %+v", m5)
	}
	if err := q.MonitoringPrune1h(ctx, base+hour); err != nil {
		t.Fatal(err)
	}
	if h1, _ = q.MonitoringList1h(ctx, store.MonitoringList1hParams{Series: 7, FromAt: 0, ToAt: math.MaxInt64}); len(h1) != 0 {
		t.Fatalf("1h after prune: %+v", h1)
	}
}

func TestMonitoringDeleteSeriesKeepsOthers(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	insertRaw(t, q, 7, "players", 0, 1)
	insertRaw(t, q, 8, "players", 0, 2)
	insertRaw(t, q, 0, "load1", 0, 0.5)
	if err := q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: 0, ToAt: hour}); err != nil {
		t.Fatal(err)
	}
	if err := q.MonitoringRollup1h(ctx, store.MonitoringRollup1hParams{FromAt: 0, ToAt: hour}); err != nil {
		t.Fatal(err)
	}
	for _, del := range []func() error{
		func() error { return q.MonitoringDeleteSeriesRaw(ctx, 7) },
		func() error { return q.MonitoringDeleteSeries5m(ctx, 7) },
		func() error { return q.MonitoringDeleteSeries1h(ctx, 7) },
	} {
		if err := del(); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []int64{7, 8, 0} {
		raw, _ := q.MonitoringListRaw(ctx, store.MonitoringListRawParams{Series: s, FromAt: 0, ToAt: hour})
		m5, _ := q.MonitoringList5m(ctx, store.MonitoringList5mParams{Series: s, FromAt: 0, ToAt: hour})
		h1, _ := q.MonitoringList1h(ctx, store.MonitoringList1hParams{Series: s, FromAt: 0, ToAt: hour})
		want := 1
		if s == 7 {
			want = 0
		}
		if len(raw) != want || len(m5) != want || len(h1) != want {
			t.Errorf("series %d: raw %d 5m %d 1h %d, want %d", s, len(raw), len(m5), len(h1), want)
		}
	}
}

func TestMonitoringSettingsServerAlertsAndState(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := t.Context()
	s, err := q.MonitoringGetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := store.MonitoringSetting{ID: 1, DiskMinPercent: 10, DiskMinBytes: 10 << 30, DownAfterMin: 3, CrashRestarts: 3, CrashWindowMin: 10, UpdatedAt: 0}
	if s != want {
		t.Fatalf("defaults = %+v, want %+v", s, want)
	}
	if err := q.MonitoringUpdateSettings(ctx, store.MonitoringUpdateSettingsParams{
		DiskMinPercent: 20, DiskMinBytes: 5 << 30, DownAfterMin: 1, CrashRestarts: 2, CrashWindowMin: 5, UpdatedAt: 9,
	}); err != nil {
		t.Fatal(err)
	}
	if s, _ = q.MonitoringGetSettings(ctx); s.DiskMinPercent != 20 || s.CrashWindowMin != 5 || s.UpdatedAt != 9 {
		t.Fatalf("updated = %+v", s)
	}

	a := storetest.CreateServer(t, db, "survival")
	if _, err := q.MonitoringGetServerAlerts(ctx, a); err == nil {
		t.Fatal("a server without row must answer sql.ErrNoRows")
	}
	for _, v := range []int64{0, 1, 0} {
		if err := q.MonitoringSetServerAlerts(ctx, store.MonitoringSetServerAlertsParams{ServerID: a, DownAlerts: v}); err != nil {
			t.Fatal(err)
		}
	}
	if v, err := q.MonitoringGetServerAlerts(ctx, a); err != nil || v != 0 {
		t.Fatalf("down_alerts = %d %v", v, err)
	}
	if rows, _ := q.MonitoringListServerAlerts(ctx); len(rows) != 1 || rows[0].ServerID != a || rows[0].DownAlerts != 0 {
		t.Fatalf("list = %+v", rows)
	}
	// ON DELETE CASCADE
	if _, err := db.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", a); err != nil {
		t.Fatal(err)
	}
	if rows, _ := q.MonitoringListServerAlerts(ctx); len(rows) != 0 {
		t.Fatalf("server_alerts survived the server: %+v", rows)
	}

	key := "server.down:7"
	if err := q.MonitoringUpsertAlertState(ctx, store.MonitoringUpsertAlertStateParams{Key: key, Firing: 1, Since: 5, LastNotified: 5, Detail: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := q.MonitoringUpsertAlertState(ctx, store.MonitoringUpsertAlertStateParams{Key: key, Firing: 0, Since: 5, LastNotified: 8, Detail: ""}); err != nil {
		t.Fatal(err)
	}
	st, err := q.MonitoringListAlertState(ctx)
	if err != nil || len(st) != 1 || st[0] != (store.AlertState{Key: key, Firing: 0, Since: 5, LastNotified: 8, Detail: ""}) {
		t.Fatalf("state = %+v %v", st, err)
	}
	if err := q.MonitoringDeleteAlertState(ctx, key); err != nil {
		t.Fatal(err)
	}
	if st, _ = q.MonitoringListAlertState(ctx); len(st) != 0 {
		t.Fatalf("state after delete = %+v", st)
	}
}
