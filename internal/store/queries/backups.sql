-- name: BackupsEnsureSchedule :exec
INSERT INTO backup_schedules (server_id, updated_at) VALUES (?, ?) ON CONFLICT (server_id) DO NOTHING;

-- name: BackupsGetSchedule :one
SELECT * FROM backup_schedules WHERE server_id = ?;

-- name: BackupsUpdateSchedule :exec
UPDATE backup_schedules SET mode = ?, interval_hours = ?, time_of_day = ?, weekday = ?,
    keep_last = ?, keep_daily = ?, keep_weekly = ?, keep_monthly = ?, next_run_at = ?, updated_at = ?
WHERE server_id = ?;

-- name: BackupsListDue :many
SELECT * FROM backup_schedules
WHERE mode != 'off' AND next_run_at IS NOT NULL AND next_run_at <= ?
ORDER BY next_run_at, server_id;

-- name: BackupsListEnabled :many
SELECT * FROM backup_schedules WHERE mode != 'off' ORDER BY server_id;

-- name: BackupsSetNextRun :exec
UPDATE backup_schedules SET next_run_at = ? WHERE server_id = ?;

-- name: BackupsStartRun :exec
UPDATE backup_schedules SET last_status = 'running', last_run_at = ?, last_error = NULL WHERE server_id = ?;

-- name: BackupsFinishRun :exec
UPDATE backup_schedules SET last_status = ?, last_error = ?, last_snapshot_id = ?, next_run_at = ?
WHERE server_id = ?;

-- name: BackupsFailRunning :exec
UPDATE backup_schedules SET last_status = 'failed', last_error = ? WHERE last_status = 'running';

-- name: BackupsSetSaveOff :exec
UPDATE backup_schedules SET saveoff_since = ? WHERE server_id = ?;

-- name: BackupsListSaveOff :many
SELECT server_id, saveoff_since FROM backup_schedules WHERE saveoff_since IS NOT NULL ORDER BY server_id;

-- name: BackupsGetTimezone :one
SELECT backup_timezone FROM instance_settings WHERE id = 1;
