package monitoring

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const cMinute = int64(time.Minute / time.Millisecond)

// cBase is a full hour (Unix ms), so 5-min and 1-h boundaries are easy to read.
var cBase = time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC).UnixMilli()

func cInsertRaw(t *testing.T, db *sql.DB, at int64, v float64) {
	t.Helper()
	if err := store.New(db).MonitoringInsertRaw(context.Background(), store.MonitoringInsertRawParams{
		Series: 7, Metric: MetricPlayers, At: at, Value: v}); err != nil {
		t.Fatal(err)
	}
}

// cAgg reads the players row of series 7 at "at" from metrics_5m or metrics_1h.
func cAgg(t *testing.T, db *sql.DB, table string, at int64) (avg, mn, mx float64, n int64, ok bool) {
	t.Helper()
	if table != "metrics_5m" && table != "metrics_1h" {
		t.Fatalf("table %q", table)
	}
	err := db.QueryRowContext(context.Background(),
		"SELECT avg, min, max, samples FROM "+table+" WHERE series = 7 AND metric = 'players' AND at = ?", at). //nolint:gosec // G202: table is one of two fixed names (checked above)
		Scan(&avg, &mn, &mx, &n)
	return avg, mn, mx, n, err == nil
}

func cInsertAgg(t *testing.T, db *sql.DB, table, metric string, at int64, avg float64, n int64) {
	t.Helper()
	if table != "metrics_5m" && table != "metrics_1h" {
		t.Fatalf("table %q", table)
	}
	if _, err := db.ExecContext(context.Background(),
		"INSERT INTO "+table+" (series, metric, at, avg, min, max, samples) VALUES (7, ?, ?, ?, ?, ?, ?)", //nolint:gosec // G202: table is one of two fixed names (checked above)
		metric, at, avg, avg-1, avg+1, n); err != nil {
		t.Fatal(err)
	}
}

func cCount(t *testing.T, db *sql.DB, query string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRollupFiveMinutes(t *testing.T) {
	db := storetest.Open(t)
	// 10:00–10:05: 0, 2, 4 …; 10:05:00 belongs to the next interval
	for i, v := range []float64{0, 2, 4, 6, 8, 10, 12, 14, 16, 18} {
		cInsertRaw(t, db, cBase+int64(i)*cMinute/2, v)
	}
	cInsertRaw(t, db, cBase+5*cMinute, 100)
	fc := clock.NewFake(time.UnixMilli(cBase + 5*cMinute + 10_000)) // 10:05:10: only 10:00–10:05 is complete
	r := newRoller(Deps{DB: db, Clock: fc})
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	avg, mn, mx, n, ok := cAgg(t, db, "metrics_5m", cBase)
	if !ok || avg != 9 || mn != 0 || mx != 18 || n != 10 {
		t.Fatalf("5m = %v %v %v %d %v", avg, mn, mx, n, ok)
	}
	if _, _, _, _, ok := cAgg(t, db, "metrics_5m", cBase+5*cMinute); ok {
		t.Fatal("the running interval must not be rolled up")
	}
	if _, _, _, _, ok := cAgg(t, db, "metrics_1h", cBase); ok {
		t.Fatal("the running hour must not be rolled up")
	}
	// the next run (not the first) picks the interval up once it is complete
	fc.Advance(5 * time.Minute)
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if avg, _, _, n, ok := cAgg(t, db, "metrics_5m", cBase+5*cMinute); !ok || avg != 100 || n != 1 {
		t.Fatalf("second interval = %v (%d) %v", avg, n, ok)
	}
}

func TestRollupIsIdempotentAndCatchesUp(t *testing.T) {
	db := storetest.Open(t)
	cInsertRaw(t, db, cBase, 4)
	fc := clock.NewFake(time.UnixMilli(cBase + 6*cMinute))
	r := newRoller(Deps{DB: db, Clock: fc})
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	cInsertRaw(t, db, cBase+cMinute, 8) // late sample in an already rolled-up interval
	for range 2 {
		if err := r.once(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if avg, _, _, n, _ := cAgg(t, db, "metrics_5m", cBase); avg != 6 || n != 2 {
		t.Fatalf("recomputed 5m = %v (%d samples)", avg, n)
	}
	if n := cCount(t, db, "SELECT count(*) FROM metrics_5m"); n != 1 {
		t.Fatalf("5m rows = %d, repeated runs must not duplicate", n)
	}
}

func TestRollupCatchesUpAGapWithinTheWindow(t *testing.T) {
	db := storetest.Open(t)
	fc := clock.NewFake(time.UnixMilli(cBase))
	r := newRoller(Deps{DB: db, Clock: fc})
	if err := r.once(context.Background()); err != nil { // first run, nothing yet
		t.Fatal(err)
	}
	for i := range 5 { // 10:00, 10:05, … 10:20
		cInsertRaw(t, db, cBase+int64(i)*5*cMinute, float64(i))
	}
	fc.Advance(26 * time.Minute) // four runs missed
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if avg, _, _, _, ok := cAgg(t, db, "metrics_5m", cBase+int64(i)*5*cMinute); !ok || avg != float64(i) {
			t.Fatalf("interval %d lost after a gap: %v %v", i, avg, ok)
		}
	}
}

func TestRollupHourWeighted(t *testing.T) {
	db := storetest.Open(t)
	// two 5-min rows: avg 10 over 10 samples, avg 40 over 2 samples → weighted 15
	cInsertAgg(t, db, "metrics_5m", MetricPlayers, cBase, 10, 10)
	cInsertAgg(t, db, "metrics_5m", MetricPlayers, cBase+5*cMinute, 40, 2)
	cInsertAgg(t, db, "metrics_5m", MetricPlayers, cBase+60*cMinute, 99, 1) // next hour, still running
	fc := clock.NewFake(time.UnixMilli(cBase + 61*cMinute))
	if err := newRoller(Deps{DB: db, Clock: fc}).once(context.Background()); err != nil {
		t.Fatal(err)
	}
	avg, mn, mx, n, ok := cAgg(t, db, "metrics_1h", cBase)
	if !ok || avg != 15 || mn != 9 || mx != 41 || n != 12 {
		t.Fatalf("1h = %v %v %v %d %v", avg, mn, mx, n, ok)
	}
	if _, _, _, _, ok := cAgg(t, db, "metrics_1h", cBase+60*cMinute); ok {
		t.Fatal("the running hour must not be rolled up")
	}
}

func TestPruneKeepsWindows(t *testing.T) {
	db := storetest.Open(t)
	now := cBase + 40*24*60*cMinute
	cInsertRaw(t, db, now-25*60*cMinute, 1) // older than 24 h: gone
	cInsertRaw(t, db, now-23*60*cMinute, 1) // stays
	// separate metrics per table, so rows rolled up by this run do not mix in
	cInsertAgg(t, db, "metrics_5m", MetricCPU, now-8*24*60*cMinute, 1, 1)
	cInsertAgg(t, db, "metrics_5m", MetricCPU, now-6*24*60*cMinute, 1, 1)
	cInsertAgg(t, db, "metrics_1h", MetricMemory, now-31*24*60*cMinute, 1, 1)
	cInsertAgg(t, db, "metrics_1h", MetricMemory, now-29*24*60*cMinute, 1, 1)
	r := newRoller(Deps{DB: db, Clock: clock.NewFake(time.UnixMilli(now))})
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := cCount(t, db, "SELECT count(*) FROM metrics_raw"); n != 1 {
		t.Errorf("raw rows = %d, want 1", n)
	}
	if n := cCount(t, db, "SELECT count(*) FROM metrics_5m WHERE metric = 'cpu_percent'"); n != 1 {
		t.Errorf("5m rows = %d, want 1", n)
	}
	if n := cCount(t, db, "SELECT count(*) FROM metrics_1h WHERE metric = 'memory_bytes'"); n != 1 {
		t.Errorf("1h rows = %d, want 1", n)
	}
	// the first run rolled the kept rows up before pruning
	if _, _, _, _, ok := cAgg(t, db, "metrics_5m", now-23*60*cMinute); !ok {
		t.Error("the kept raw sample must be rolled up")
	}
	if n := cCount(t, db, "SELECT count(*) FROM metrics_1h WHERE metric = 'cpu_percent'"); n != 1 {
		t.Errorf("1h rows from the kept 5m row = %d, want 1", n)
	}
}

func TestFirstRunDoesNotRecomputeACutInterval(t *testing.T) {
	db := storetest.Open(t)
	now := cBase + 24*60*cMinute + 2*cMinute // 24 h after 10:02
	// the 10:00 interval was rolled up from 10 samples; a prune since kept only 10:04
	cInsertAgg(t, db, "metrics_5m", MetricPlayers, cBase, 5, 10)
	cInsertRaw(t, db, cBase+4*cMinute, 50)
	if err := newRoller(Deps{DB: db, Clock: clock.NewFake(time.UnixMilli(now))}).once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if avg, _, _, n, _ := cAgg(t, db, "metrics_5m", cBase); avg != 5 || n != 10 {
		t.Fatalf("cut interval recomputed: %v (%d)", avg, n)
	}
}

func TestRollupSurvivesClockGoingBack(t *testing.T) {
	db := storetest.Open(t)
	cInsertRaw(t, db, cBase, 2)
	fc := clock.NewFake(time.UnixMilli(cBase + 10*cMinute))
	r := newRoller(Deps{DB: db, Clock: fc})
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	fc.Set(time.UnixMilli(cBase - 60*cMinute)) // clock jumps back an hour
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if avg, _, _, _, ok := cAgg(t, db, "metrics_5m", cBase); !ok || avg != 2 {
		t.Fatal("a clock jump must not lose rolled-up intervals")
	}
	if n := cCount(t, db, "SELECT count(*) FROM metrics_raw"); n != 1 {
		t.Fatalf("raw rows = %d after a clock jump back", n)
	}
}

func TestRunRollupStopsWithTheContext(t *testing.T) {
	db := storetest.Open(t)
	cInsertRaw(t, db, cBase, 3)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runRollup(ctx, Deps{DB: db, Clock: clock.NewFake(time.UnixMilli(cBase + 6*cMinute))})
		close(done)
	}()
	eventually(t, "first rollup", func() bool { _, _, _, _, ok := cAgg(t, db, "metrics_5m", cBase); return ok })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runRollup did not return after cancel")
	}
}
