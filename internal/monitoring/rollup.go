package monitoring

import (
	"context"
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
// recomputes a window of complete intervals (INSERT OR REPLACE: idempotent); the first run
// after a start covers the whole retention of the source table.
type roller struct {
	d     Deps
	log   *slog.Logger
	first bool
}

func newRoller(d Deps) *roller {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &roller{d: d, log: log, first: true}
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
// rolled up (raw samples live 24 h, the 5-min window is 30 min). time.Truncate counts from
// the zero time in UTC; 5 min and 1 h divide a day, so the bounds match (at / 300000) * 300000.
func (r *roller) once(ctx context.Context) error {
	q := store.New(r.d.DB)
	now := r.d.Clock.Now()
	to5 := now.Truncate(5 * time.Minute)
	to1 := now.Truncate(time.Hour)
	from5, from1 := to5.Add(-window5m), to1.Add(-window1h)
	if r.first {
		// from the first complete interval the source table still holds: an interval that
		// earlier prunes cut is not recomputed from its rest
		from5 = now.Add(-retainRaw).Truncate(5 * time.Minute).Add(5 * time.Minute)
		from1 = now.Add(-retain5m).Truncate(time.Hour).Add(time.Hour)
	}
	err := errors.Join(
		q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: from5.UnixMilli(), ToAt: to5.UnixMilli()}),
		q.MonitoringRollup1h(ctx, store.MonitoringRollup1hParams{FromAt: from1.UnixMilli(), ToAt: to1.UnixMilli()}),
	)
	if err != nil {
		return err // nothing is pruned before its rollup succeeded
	}
	if err := errors.Join(
		q.MonitoringPruneRaw(ctx, now.Add(-retainRaw).UnixMilli()),
		q.MonitoringPrune5m(ctx, now.Add(-retain5m).UnixMilli()),
		q.MonitoringPrune1h(ctx, now.Add(-retain1h).UnixMilli()),
	); err != nil {
		return err
	}
	r.first = false
	return nil
}
