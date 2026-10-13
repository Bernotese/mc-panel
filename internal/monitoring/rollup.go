package monitoring

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

// rollupEvery, retainRaw, retain5m and retain1h come from types.go.
const (
	window5m = 30 * time.Minute // recomputed on every run: late samples and missed runs
	window1h = 3 * time.Hour
)

// roller rolls raw samples up to 5-min and 1-h rows and prunes old rows (spec §4). Every run
// recomputes the complete intervals since the end of the last successful run, minus a window
// for late samples (INSERT OR REPLACE: idempotent): a clock jump back, a long pause or missed
// runs lose nothing. The first run after a start begins at the earliest row of the source
// table, rounded up to a full interval (that interval may have been cut by an earlier prune).
type roller struct {
	d     Deps
	log   *slog.Logger
	done5 time.Time // end of the last successful 5-min rollup; zero before the first
	done1 time.Time
}

func newRoller(d Deps) *roller {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &roller{d: d, log: log}
}

// runRollup runs once right away and then every rollupEvery until ctx is done.
func runRollup(ctx context.Context, d Deps) {
	r := newRoller(d)
	t := time.NewTicker(rollupEvery)
	defer t.Stop()
	for {
		if err := r.once(ctx); err != nil && ctx.Err() == nil {
			r.log.ErrorContext(ctx, "monitoring: rollup", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// once rolls up before it prunes, so a raw sample is never deleted before its interval is
// rolled up. time.Truncate counts from the zero time in UTC; 5 min and 1 h divide a day, so
// the bounds match (at / 300000) * 300000 in SQL.
func (r *roller) once(ctx context.Context) error {
	q := store.New(r.d.DB)
	now := r.d.Clock.Now()
	to5 := now.Truncate(5 * time.Minute)
	to1 := now.Truncate(time.Hour)
	from5, err := from(r.done5, to5, window5m, 5*time.Minute, func() (int64, error) { return q.MonitoringEarliestRaw(ctx) })
	if err != nil {
		return err
	}
	if err := q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: from5.UnixMilli(), ToAt: to5.UnixMilli()}); err != nil {
		return err // nothing is pruned before its rollup succeeded
	}
	// after the 5-min rollup: a first run also sees the 5-min rows it has just written
	from1, err := from(r.done1, to1, window1h, time.Hour, func() (int64, error) { return q.MonitoringEarliest5m(ctx) })
	if err != nil {
		return err
	}
	if err := q.MonitoringRollup1h(ctx, store.MonitoringRollup1hParams{FromAt: from1.UnixMilli(), ToAt: to1.UnixMilli()}); err != nil {
		return err
	}
	r.done5, r.done1 = to5, to1
	return errors.Join(
		q.MonitoringPruneRaw(ctx, now.Add(-retainRaw).UnixMilli()),
		q.MonitoringPrune5m(ctx, now.Add(-retain5m).UnixMilli()),
		q.MonitoringPrune1h(ctx, now.Add(-retain1h).UnixMilli()),
	)
}

// from is the start of the rollup window: min(last end, to) - window, or for the first run
// the earliest row of the source table (earliest; sql.ErrNoRows = empty) rounded up to a full
// interval.
func from(done, to time.Time, window, step time.Duration, earliest func() (int64, error)) (time.Time, error) {
	if !done.IsZero() {
		if done.Before(to) {
			to = done
		}
		return to.Add(-window), nil
	}
	at, err := earliest()
	if errors.Is(err, sql.ErrNoRows) {
		return to, nil // nothing to roll up yet
	}
	if err != nil {
		return time.Time{}, err
	}
	t := time.UnixMilli(at)
	if c := t.Truncate(step); !c.Equal(t) {
		t = c.Add(step)
	}
	return t, nil
}
