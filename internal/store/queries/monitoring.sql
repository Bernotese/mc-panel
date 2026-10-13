-- name: MonitoringInsertRaw :exec
INSERT OR REPLACE INTO metrics_raw (series, metric, at, value) VALUES (?, ?, ?, ?);

-- name: MonitoringListRaw :many
SELECT metric, at, value FROM metrics_raw
WHERE series = sqlc.arg(series) AND at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at)
ORDER BY metric, at;

-- name: MonitoringListRawMetric :many
SELECT at, value FROM metrics_raw
WHERE series = sqlc.arg(series) AND metric = sqlc.arg(metric) AND at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at)
ORDER BY at;

-- name: MonitoringList5m :many
SELECT metric, at, avg, min, max FROM metrics_5m
WHERE series = sqlc.arg(series) AND at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at)
ORDER BY metric, at;

-- name: MonitoringList1h :many
SELECT metric, at, avg, min, max FROM metrics_1h
WHERE series = sqlc.arg(series) AND at >= sqlc.arg(from_at) AND at < sqlc.arg(to_at)
ORDER BY metric, at;

-- name: MonitoringRollup5m :exec
INSERT OR REPLACE INTO metrics_5m (series, metric, at, avg, min, max, samples)
SELECT r.series, r.metric, (r.at / 300000) * 300000, avg(r.value), min(r.value), max(r.value), count(*)
FROM metrics_raw r WHERE r.at >= sqlc.arg(from_at) AND r.at < sqlc.arg(to_at)
GROUP BY r.series, r.metric, (r.at / 300000) * 300000;

-- name: MonitoringRollup1h :exec
INSERT OR REPLACE INTO metrics_1h (series, metric, at, avg, min, max, samples)
SELECT f.series, f.metric, (f.at / 3600000) * 3600000, sum(f.avg * f.samples) / sum(f.samples), min(f.min), max(f.max), sum(f.samples)
FROM metrics_5m f WHERE f.at >= sqlc.arg(from_at) AND f.at < sqlc.arg(to_at)
GROUP BY f.series, f.metric, (f.at / 3600000) * 3600000;

-- name: MonitoringPruneRaw :exec
DELETE FROM metrics_raw WHERE at < sqlc.arg(before);

-- name: MonitoringPrune5m :exec
DELETE FROM metrics_5m WHERE at < sqlc.arg(before);

-- name: MonitoringPrune1h :exec
DELETE FROM metrics_1h WHERE at < sqlc.arg(before);

-- name: MonitoringDeleteSeriesRaw :exec
DELETE FROM metrics_raw WHERE series = ?;

-- name: MonitoringDeleteSeries5m :exec
DELETE FROM metrics_5m WHERE series = ?;

-- name: MonitoringDeleteSeries1h :exec
DELETE FROM metrics_1h WHERE series = ?;

-- name: MonitoringGetSettings :one
SELECT * FROM monitoring_settings WHERE id = 1;

-- name: MonitoringUpdateSettings :exec
UPDATE monitoring_settings SET disk_min_percent = ?, disk_min_bytes = ?, down_after_min = ?,
    crash_restarts = ?, crash_window_min = ?, updated_at = ? WHERE id = 1;

-- name: MonitoringListServerAlerts :many
SELECT server_id, down_alerts FROM server_alerts ORDER BY server_id;

-- name: MonitoringGetServerAlerts :one
SELECT down_alerts FROM server_alerts WHERE server_id = ?;

-- name: MonitoringSetServerAlerts :exec
INSERT INTO server_alerts (server_id, down_alerts) VALUES (?, ?)
ON CONFLICT (server_id) DO UPDATE SET down_alerts = excluded.down_alerts;

-- name: MonitoringListAlertState :many
SELECT * FROM alert_state ORDER BY key;

-- name: MonitoringUpsertAlertState :exec
INSERT INTO alert_state (key, firing, since, last_notified, detail) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (key) DO UPDATE SET firing = excluded.firing, since = excluded.since,
    last_notified = excluded.last_notified, detail = excluded.detail;

-- name: MonitoringDeleteAlertState :exec
DELETE FROM alert_state WHERE key = ?;

-- Earliest timestamp for the first rollup after a start; sql.ErrNoRows = empty table (sqlc types
-- min(at) as interface{}, ORDER BY at LIMIT 1 keeps it an int64).
-- name: MonitoringEarliestRaw :one
SELECT at FROM metrics_raw ORDER BY at LIMIT 1;

-- name: MonitoringEarliest5m :one
SELECT at FROM metrics_5m ORDER BY at LIMIT 1;
