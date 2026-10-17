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
       i.properties, i.desired_state, i.revision, i.created_at, i.updated_at, i.web_port
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

-- name: ServersSetWebPort :one
UPDATE instances SET web_port = ? WHERE server_id = ? RETURNING *;

-- name: ServersBumpAllRevisions :exec
UPDATE instances SET revision = revision + 1, updated_at = ?;

-- name: ServersGetSettings :one
SELECT * FROM instance_settings WHERE id = 1;

-- name: ServersUpdateSettings :exec
UPDATE instance_settings SET base_domain = ?, curseforge_api_key = ?, updated_at = ? WHERE id = 1;

-- name: ServersSetBackupTimezone :exec
UPDATE instance_settings SET backup_timezone = ?, updated_at = ? WHERE id = 1;

-- name: ServersNextServerID :one
-- Allocates the ID of a new server: never one that was used before (see migration 00003).
UPDATE server_ids
SET last_id = max(last_id, (SELECT COALESCE(MAX(id), 0) FROM servers)) + 1
WHERE id = 1
RETURNING last_id;

-- name: ServersCreateServer :one
INSERT INTO servers (id, name, created_at) VALUES (?, ?, ?)
RETURNING *;

-- name: ServersBumpCurseForgeRevisions :exec
UPDATE instances SET revision = revision + 1, updated_at = ? WHERE type = 'curseforge';
