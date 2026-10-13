package monitoring

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/store"
)

// writeSnapshot stores every set value of one pass in one transaction (unset = gap, V5).
// Metric names and HostSeries come from types.go.
func writeSnapshot(ctx context.Context, db *sql.DB, s Snapshot) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := store.New(tx)
	at := s.At.UnixMilli()
	put := func(series int64, metric string, v *float64) error {
		if v == nil {
			return nil
		}
		if err := q.MonitoringInsertRaw(ctx, store.MonitoringInsertRawParams{Series: series, Metric: metric, At: at, Value: *v}); err != nil {
			return fmt.Errorf("monitoring: write %s/%d: %w", metric, series, err)
		}
		return nil
	}
	type kv struct {
		metric string
		v      *float64
	}
	for _, sv := range s.Servers {
		for _, m := range []kv{
			{MetricUp, boolf(sv.Up)}, {MetricPlayers, intf(sv.Players)}, {MetricPlayersMax, intf(sv.PlayersMax)},
			{MetricTPS, sv.TPS}, {MetricMSPT, sv.MSPT}, {MetricCPU, sv.CPUPercent},
			{MetricMemory, int64f(sv.MemoryBytes)}, {MetricMemoryLimit, int64f(sv.MemoryLimit)},
		} {
			if err := put(sv.ID, m.metric, m.v); err != nil {
				return err
			}
		}
	}
	h := s.Host
	for _, m := range []kv{
		{MetricHostCPU, h.CPUPercent}, {MetricLoad1, h.Load1}, {MetricMemUsed, int64f(h.MemoryUsed)},
		{MetricMemTotal, int64f(h.MemoryTotal)}, {MetricDiskFree, int64f(h.DiskFree)}, {MetricDiskTotal, int64f(h.DiskTotal)},
	} {
		if err := put(HostSeries, m.metric, m.v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// boolf stores up as 0/1.
func boolf(p *bool) *float64 {
	if p == nil {
		return nil
	}
	v := 0.0
	if *p {
		v = 1
	}
	return &v
}

func intf(p *int) *float64 {
	if p == nil {
		return nil
	}
	v := float64(*p)
	return &v
}

func int64f(p *int64) *float64 {
	if p == nil {
		return nil
	}
	v := float64(*p)
	return &v
}
