package monitoring

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var aReadNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func aInsertRaw(t *testing.T, db *sql.DB, series int64, metric string, at time.Time, v float64) {
	t.Helper()
	if err := store.New(db).MonitoringInsertRaw(context.Background(), store.MonitoringInsertRawParams{
		Series: series, Metric: metric, At: at.UnixMilli(), Value: v,
	}); err != nil {
		t.Fatal(err)
	}
}

func aInsertAgg(t *testing.T, db *sql.DB, table string, series int64, metric string, at time.Time, avg, lo, hi float64) {
	t.Helper()
	query := map[string]string{
		"metrics_5m": `INSERT INTO metrics_5m (series, metric, at, avg, min, max, samples) VALUES (?, ?, ?, ?, ?, ?, 10)`,
		"metrics_1h": `INSERT INTO metrics_1h (series, metric, at, avg, min, max, samples) VALUES (?, ?, ?, ?, ?, ?, 10)`,
	}[table]
	if query == "" {
		t.Fatalf("unknown table %q", table)
	}
	if _, err := db.ExecContext(context.Background(), query, series, metric, at.UnixMilli(), avg, lo, hi); err != nil {
		t.Fatal(err)
	}
}

func TestParseRange(t *testing.T) {
	for name, step := range map[string]time.Duration{"1h": 30 * time.Second, "24h": 5 * time.Minute, "7d": time.Hour, "30d": time.Hour} {
		rg, ok := parseRange(name)
		if !ok || rg.step != step || rg.name != name {
			t.Errorf("parseRange(%q) = %+v %v", name, rg, ok)
		}
	}
	for _, bad := range []string{"", "2h", "1H", "30d ", "all"} {
		if _, ok := parseRange(bad); ok {
			t.Errorf("parseRange(%q) accepted", bad)
		}
	}
}

func TestReadSeriesPicksTableAndWindow(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	aInsertRaw(t, db, 7, MetricPlayers, aReadNow.Add(-30*time.Minute), 3)
	aInsertRaw(t, db, 7, MetricPlayers, aReadNow.Add(-2*time.Hour), 5) // outside 1h
	aInsertRaw(t, db, 7, "foo", aReadNow.Add(-time.Minute), 1)         // unknown metric
	aInsertRaw(t, db, 8, MetricPlayers, aReadNow.Add(-time.Minute), 9) // other series
	aInsertAgg(t, db, "metrics_5m", 7, MetricTPS, aReadNow.Add(-23*time.Hour), 19.5, 18, 20)
	aInsertAgg(t, db, "metrics_5m", 7, MetricTPS, aReadNow.Add(-25*time.Hour), 10, 10, 10) // outside 24h
	aInsertAgg(t, db, "metrics_1h", 7, MetricCPU, aReadNow.Add(-6*24*time.Hour), 40, 10, 90)
	aInsertAgg(t, db, "metrics_1h", 7, MetricCPU, aReadNow.Add(-20*24*time.Hour), 30, 5, 60)

	rg, _ := parseRange("1h")
	got, err := readSeries(context.Background(), q, 7, rg, aReadNow, serverMetrics)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(serverMetrics) {
		t.Fatalf("metrics = %v", got)
	}
	if p := got[MetricPlayers]; len(p) != 1 || p[0] != (point{float64(aReadNow.Add(-30 * time.Minute).UnixMilli()), 3, 3, 3}) {
		t.Fatalf("players 1h = %v", p)
	}
	if _, ok := got["foo"]; ok {
		t.Fatal("unknown metric returned")
	}

	rg, _ = parseRange("24h")
	got, _ = readSeries(context.Background(), q, 7, rg, aReadNow, serverMetrics)
	if p := got[MetricTPS]; len(p) != 1 || p[0][1] != 19.5 || p[0][2] != 18 || p[0][3] != 20 {
		t.Fatalf("tps 24h = %v", p)
	}
	if len(got[MetricPlayers]) != 0 {
		t.Fatalf("24h reads raw values: %v", got[MetricPlayers])
	}

	rg, _ = parseRange("7d")
	got, _ = readSeries(context.Background(), q, 7, rg, aReadNow, serverMetrics)
	if p := got[MetricCPU]; len(p) != 1 || p[0][1] != 40 {
		t.Fatalf("cpu 7d = %v", p)
	}
	rg, _ = parseRange("30d")
	got, _ = readSeries(context.Background(), q, 7, rg, aReadNow, serverMetrics)
	if p := got[MetricCPU]; len(p) != 2 || p[0][1] != 30 {
		t.Fatalf("cpu 30d = %v", p)
	}
}

func TestThirtyDaysAtMost721Points(t *testing.T) {
	db := storetest.Open(t)
	start := aReadNow.Add(-31 * 24 * time.Hour)
	for at := start; at.Before(aReadNow); at = at.Add(time.Hour) {
		aInsertAgg(t, db, "metrics_1h", HostSeries, MetricLoad1, at, 1, 1, 1)
	}
	rg, _ := parseRange("30d")
	got, err := readSeries(context.Background(), store.New(db), HostSeries, rg, aReadNow, hostMetrics)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got[MetricLoad1]); n == 0 || n > 721 {
		t.Fatalf("points = %d", n)
	}
}

func TestSparklineOnlyPlayersAtMost120(t *testing.T) {
	db := storetest.Open(t)
	for i := range 200 {
		at := aReadNow.Add(-time.Duration(i) * 18 * time.Second)
		aInsertRaw(t, db, 3, MetricPlayers, at, float64(i%5))
		aInsertRaw(t, db, 3, MetricTPS, at, 20)
	}
	got, err := readSparkline(context.Background(), store.New(db), 3, aReadNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxSparkline {
		t.Fatalf("sparkline has %d points", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i][0] <= got[i-1][0] {
			t.Fatalf("not ascending at %d: %v", i, got[i-1:i+1])
		}
	}
	if got[len(got)-1][0] != float64(aReadNow.UnixMilli()) {
		t.Fatalf("newest point = %v", got[len(got)-1])
	}
}

func TestReadRawMetricOneMetricInWindow(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	for i := range 5 {
		at := aReadNow.Add(-time.Duration(i) * 10 * time.Minute)
		aInsertRaw(t, db, 4, MetricPlayers, at, float64(i))
		aInsertRaw(t, db, 4, MetricTPS, at, 20)
		aInsertRaw(t, db, 5, MetricPlayers, at, 99)
	}
	rows, err := readRawMetric(context.Background(), q, 4, MetricPlayers, aReadNow.Add(-30*time.Minute).UnixMilli(), aReadNow.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	// [now-30m, now): i = 3, 2, 1, oldest first; now itself is excluded
	if len(rows) != 3 || rows[0].Value != 3 || rows[2].Value != 1 || rows[0].At >= rows[1].At {
		t.Fatalf("rows = %+v", rows)
	}
}
