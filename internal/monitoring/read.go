package monitoring

import (
	"context"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

// metricsRange is one of the history ranges of the API (spec §6.1).
type metricsRange struct {
	name string
	span time.Duration
	step time.Duration // 30 s = metrics_raw, 5 min = metrics_5m, 1 h = metrics_1h
}

var ranges = map[string]metricsRange{
	"1h":  {name: "1h", span: time.Hour, step: 30 * time.Second},
	"24h": {name: "24h", span: 24 * time.Hour, step: 5 * time.Minute},
	"7d":  {name: "7d", span: 7 * 24 * time.Hour, step: time.Hour},
	"30d": {name: "30d", span: 30 * 24 * time.Hour, step: time.Hour},
}

func parseRange(s string) (metricsRange, bool) {
	rg, ok := ranges[s]
	return rg, ok
}

// The metrics of the API (V3); other names in the tables are never returned.
var (
	serverMetrics = []string{MetricUp, MetricPlayers, MetricPlayersMax, MetricTPS, MetricMSPT, MetricCPU, MetricMemory, MetricMemoryLimit}
	hostMetrics   = []string{MetricHostCPU, MetricMemUsed, MetricMemTotal, MetricLoad1, MetricDiskFree, MetricDiskTotal}
)

const (
	sparklineSpan = time.Hour
	maxSparkline  = 120
)

// point is [at, avg, min, max] (V3 MetricPoint).
type point [4]float64

// readSeries returns every allowed metric of series in (now-span, now], each with at least an
// empty slice.
func readSeries(ctx context.Context, q *store.Queries, series int64, rg metricsRange, now time.Time, allowed []string) (map[string][]point, error) {
	out := make(map[string][]point, len(allowed))
	for _, m := range allowed {
		out[m] = []point{}
	}
	add := func(metric string, p point) {
		if s, ok := out[metric]; ok {
			out[metric] = append(s, p)
		}
	}
	from, to := now.Add(-rg.span).UnixMilli(), now.UnixMilli()+1
	switch rg.step {
	case 30 * time.Second:
		rows, err := q.MonitoringListRaw(ctx, store.MonitoringListRawParams{Series: series, FromAt: from, ToAt: to})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			add(r.Metric, point{float64(r.At), r.Value, r.Value, r.Value})
		}
	case 5 * time.Minute:
		rows, err := q.MonitoringList5m(ctx, store.MonitoringList5mParams{Series: series, FromAt: from, ToAt: to})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			add(r.Metric, point{float64(r.At), r.Avg, r.Min, r.Max})
		}
	default:
		rows, err := q.MonitoringList1h(ctx, store.MonitoringList1hParams{Series: series, FromAt: from, ToAt: to})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			add(r.Metric, point{float64(r.At), r.Avg, r.Min, r.Max})
		}
	}
	return out, nil
}

// readSparkline returns the players of the last hour as [at, players], oldest first, at most
// maxSparkline points (the newest).
func readSparkline(ctx context.Context, q *store.Queries, id int64, now time.Time) ([][2]float64, error) {
	rows, err := q.MonitoringListRaw(ctx, store.MonitoringListRawParams{
		Series: id, FromAt: now.Add(-sparklineSpan).UnixMilli(), ToAt: now.UnixMilli() + 1,
	})
	if err != nil {
		return nil, err
	}
	out := [][2]float64{}
	for _, r := range rows {
		if r.Metric == MetricPlayers {
			out = append(out, [2]float64{float64(r.At), r.Value})
		}
	}
	if len(out) > maxSparkline {
		out = out[len(out)-maxSparkline:]
	}
	return out, nil
}
