package monitoring

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func cF64(v float64) *float64 { return &v }
func cI64(v int64) *int64     { return &v }
func cBp(v bool) *bool        { return &v }

// cCountRaw counts the raw rows of one series.
func cCountRaw(t *testing.T, db *sql.DB, series int64) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM metrics_raw WHERE series = ?", series).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func cCountMetric(t *testing.T, db *sql.DB, series int64, metric string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM metrics_raw WHERE series = ? AND metric = ?`, series, metric).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriteSnapshotOnlySetValues(t *testing.T) {
	db := storetest.Open(t)
	at := time.UnixMilli(1_800_000_000_000)
	s := Snapshot{At: at, Agent: true, Docker: true,
		Servers: []ServerSample{
			{ID: 7, Up: cBp(true), Players: cIntp(3), PlayersMax: cIntp(20), TPS: cF64(19.9), MSPT: cF64(4.2),
				CPUPercent: cF64(12.5), MemoryBytes: cI64(1 << 30), MemoryLimit: cI64(4 << 30)},
			{ID: 8, Up: cBp(false)}, // running, ping failed: only up=0
			{ID: 9},                 // stopped: nothing
		},
		Host: HostSample{CPUPercent: cF64(33), DiskFree: cI64(10), DiskTotal: cI64(100)},
	}
	if err := writeSnapshot(context.Background(), db, s); err != nil {
		t.Fatal(err)
	}
	for series, want := range map[int64]int{7: 8, 8: 1, 9: 0, HostSeries: 3} {
		if got := cCountRaw(t, db, series); got != want {
			t.Errorf("series %d: %d rows, want %d", series, got, want)
		}
	}
	for _, c := range []struct {
		series int64
		metric string
		want   float64
	}{{8, MetricUp, 0}, {7, MetricUp, 1}, {7, MetricMemory, 1 << 30}, {HostSeries, MetricDiskTotal, 100}} {
		var v float64
		if err := db.QueryRowContext(context.Background(),
			"SELECT value FROM metrics_raw WHERE series = ? AND metric = ? AND at = ?", c.series, c.metric, at.UnixMilli()).Scan(&v); err != nil || v != c.want {
			t.Errorf("%s of series %d = %v (%v), want %v", c.metric, c.series, v, err, c.want)
		}
	}
}

func TestWriteSnapshotTwiceReplaces(t *testing.T) {
	db := storetest.Open(t)
	s := Snapshot{At: time.UnixMilli(1_800_000_000_000), Servers: []ServerSample{{ID: 7, Players: cIntp(1)}}}
	if err := writeSnapshot(context.Background(), db, s); err != nil {
		t.Fatal(err)
	}
	s.Servers[0].Players = cIntp(2)
	if err := writeSnapshot(context.Background(), db, s); err != nil {
		t.Fatal(err)
	}
	if got := cCountRaw(t, db, 7); got != 1 {
		t.Fatalf("rows = %d, the same at must replace", got)
	}
}

func TestWriteSnapshotCancelled(t *testing.T) {
	db := storetest.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := Snapshot{At: time.UnixMilli(1_800_000_000_000), Servers: []ServerSample{{ID: 7, Players: cIntp(1)}}}
	if err := writeSnapshot(ctx, db, s); err == nil {
		t.Fatal("a cancelled context must fail the write")
	}
	if got := cCountRaw(t, db, 7); got != 0 {
		t.Fatalf("rows = %d after a failed write", got)
	}
}
