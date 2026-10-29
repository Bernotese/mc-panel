-- Sub-project 2: Minecraft instances and their settings. All query names start with "Servers".

-- name: ServersCreateInstance :one
INSERT INTO instances (server_id, type, version, loader_version, modpack, java, memory_mib, properties,
                       desired_state, revision, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'running', 1, ?, ?)
RETURNING *;

-- name: ServersGetInstance :one
SELECT * FROM instances WHERE server_id = ?;

-- name: ServersListInstances :many
SELECT s.name, i.server_id, i.type, i.version, i.loader_version, i.modpack, i.java, i.memory_mib,
       i.properties, i.desired_state, i.revision, i.created_at, i.updated_at
FROM instances i JOIN servers s ON s.id = i.server_id
ORDER BY s.name;

-- name: ServersUpdateInstance :one
UPDATE instances
SET version = ?, loader_version = ?, modpack = ?, java = ?, memory_mib = ?, properties = ?,
    revision = revision + 1, updated_at = ?
WHERE server_id = ?
RETURNING *;

-- name: ServersSetDesiredState :execrows
UPDATE instances SET desired_state = ?, updated_at = ? WHERE server_id = ?;

-- name: ServersListWebRoutes :many
SELECT * FROM web_routes ORDER BY server_id, path;

-- name: ServersListServerWebRoutes :many
SELECT * FROM web_routes WHERE server_id = ? ORDER BY path;

-- name: ServersDeleteWebRoutes :exec
DELETE FROM web_routes WHERE server_id = ?;

-- name: ServersInsertWebRoute :exec
INSERT INTO web_routes (server_id, path, port) VALUES (?, ?, ?);

-- name: ServersBumpAllRevisions :exec
UPDATE instances SET revision = revision + 1, updated_at = ?;

-- name: ServersGetSettings :one
SELECT * FROM instance_settings WHERE id = 1;

-- name: ServersUpdateSettings :exec
UPDATE instance_settings SET base_domain = ?, curseforge_api_key = ?, updated_at = ? WHERE id = 1;

-- name: ServersSetBackupTimezone :exec
UPDATE instance_settings SET backup_timezone = ?, updated_at = ? WHERE id = 1;

-- name: ServersNextServerID :one
-- Allocates the ID of a new server: never one that was used before (see migration 00003), also
-- not the ID of a deleted server whose tombstone is still kept (migration 00015).
UPDATE server_ids
SET last_id = max(last_id, (SELECT COALESCE(MAX(id), 0) FROM servers),
                  (SELECT COALESCE(MAX(server_id), 0) FROM server_deletions)) + 1
WHERE id = 1
RETURNING last_id;

-- name: ServersCreateServer :one
INSERT INTO servers (id, name, created_at) VALUES (?, ?, ?)
RETURNING *;

-- name: ServersBumpCurseForgeRevisions :exec
UPDATE instances SET revision = revision + 1, updated_at = ? WHERE type = 'curseforge';

-- name: ServersUpdateDevInstance :one
-- Dev instances: copies a spec onto a dev's row and bumps its revision. The WHERE clause only
-- matches a server that is a dev of the given source, so this query can never write prod.
UPDATE instances
SET type = sqlc.arg(type), version = sqlc.arg(version), loader_version = sqlc.arg(loader_version),
    modpack = sqlc.arg(modpack), java = sqlc.arg(java), properties = sqlc.arg(properties),
    revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE instances.server_id = sqlc.arg(server_id)
  AND instances.server_id IN (SELECT d.server_id FROM dev_instances d WHERE d.source_id = sqlc.arg(source_id))
RETURNING *;

-- name: ServersUpdateDevMemory :one
-- Dev instances: the only field of a dev an admin sets (PATCH /servers/{id}); everything else
-- follows its source (ServersUpdateDevInstance). Only matches a dev.
UPDATE instances SET memory_mib = sqlc.arg(memory_mib), revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE instances.server_id = sqlc.arg(server_id)
  AND instances.server_id IN (SELECT d.server_id FROM dev_instances d)
RETURNING *;

-- name: ServersUpdateDevProperties :one
-- Dev instances: replaces only the properties of a dev (its overrides changed) and bumps its
-- revision; type, version, loader, modpack, java and memory stay. Only matches a dev.
UPDATE instances SET properties = sqlc.arg(properties), revision = revision + 1, updated_at = sqlc.arg(updated_at)
WHERE instances.server_id = sqlc.arg(server_id)
  AND instances.server_id IN (SELECT d.server_id FROM dev_instances d)
RETURNING *;

-- name: ServersCreateDeletion :exec
-- Durable deletion (issue #148): written in the transaction that removes the server's rows.
INSERT INTO server_deletions (server_id, name, container, backups, requested_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ServersGetDeletion :one
SELECT * FROM server_deletions WHERE server_id = ?;

-- name: ServersListDeletions :many
-- Open deletions and those completed at or after the given time, newest first.
SELECT * FROM server_deletions
WHERE completed_at IS NULL OR completed_at >= ?
ORDER BY requested_at DESC, server_id DESC;

-- name: ServersListOpenDeletions :many
SELECT * FROM server_deletions WHERE completed_at IS NULL ORDER BY server_id;

-- name: ServersUpdateDeletion :exec
UPDATE server_deletions
SET container = ?, container_error = ?, backups = ?, backups_error = ?, updated_at = ?, completed_at = ?
WHERE server_id = ?;

-- name: ServersPruneDeletions :exec
-- Completed deletions are kept for a week to be listed, then forgotten.
DELETE FROM server_deletions WHERE completed_at IS NOT NULL AND completed_at < ?;

-- name: ServersOpenDeletionByName :one
-- A server name is taken until its deletion is complete: the old container may still be routed
-- under the same address.
SELECT count(*) FROM server_deletions WHERE completed_at IS NULL AND name = ?;
