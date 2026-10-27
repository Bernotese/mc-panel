-- Dev instances (dev instances spec). All query names start with "Dev".

-- name: DevCreate :one
INSERT INTO dev_instances (server_id, source_id, sync_time, idle_minutes, property_overrides,
    next_sync_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DevGet :one
SELECT * FROM dev_instances WHERE server_id = ?;

-- name: DevGetBySource :one
SELECT * FROM dev_instances WHERE source_id = ?;

-- name: DevList :many
SELECT d.*, s.name AS name, src.name AS source_name
FROM dev_instances d
JOIN servers s ON s.id = d.server_id
JOIN servers src ON src.id = d.source_id
ORDER BY d.server_id;

-- name: DevListDue :many
SELECT * FROM dev_instances
WHERE sync_paused = 0 AND next_sync_at IS NOT NULL AND next_sync_at <= ?
ORDER BY next_sync_at, server_id;

-- name: DevUpdateSettings :exec
UPDATE dev_instances SET sync_paused = ?, paused_at = ?, sync_time = ?, idle_minutes = ?,
    property_overrides = ?, next_sync_at = ?, updated_at = ?
WHERE server_id = ?;

-- name: DevSetNextSync :exec
UPDATE dev_instances SET next_sync_at = ?, deferred_since = NULL, updated_at = ? WHERE server_id = ?;

-- name: DevSetDeferred :exec
UPDATE dev_instances SET next_sync_at = ?, deferred_since = ?, updated_at = ? WHERE server_id = ?;

-- name: DevSetSyncRunning :exec
UPDATE dev_instances SET last_sync_status = 'running', updated_at = ? WHERE server_id = ?;

-- name: DevSetSyncResult :exec
UPDATE dev_instances SET last_sync_status = ?, last_sync_error = ?, last_sync_snapshot = ?,
    last_sync_at = ?, updated_at = ?
WHERE server_id = ?;

-- name: DevSetSleeping :exec
UPDATE dev_instances SET sleeping = ?, last_wake_at = ?, updated_at = ? WHERE server_id = ?;

-- name: DevListSleeping :many
SELECT d.server_id, s.name
FROM dev_instances d JOIN servers s ON s.id = d.server_id
WHERE d.sleeping = 1
ORDER BY d.server_id;

-- name: DevOpsList :many
SELECT name FROM dev_ops WHERE server_id = ? ORDER BY name COLLATE NOCASE;

-- name: DevOpsAdd :exec
INSERT INTO dev_ops (server_id, name, created_at) VALUES (?, ?, ?) ON CONFLICT (server_id, name) DO NOTHING;

-- name: DevOpsRemove :execrows
DELETE FROM dev_ops WHERE server_id = ? AND name = ?;
