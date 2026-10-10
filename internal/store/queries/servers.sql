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

-- name: ServersBumpAllRevisions :exec
UPDATE instances SET revision = revision + 1, updated_at = ?;

-- name: ServersGetSettings :one
SELECT * FROM instance_settings WHERE id = 1;

-- name: ServersUpdateSettings :exec
UPDATE instance_settings SET base_domain = ?, curseforge_api_key = ?, updated_at = ? WHERE id = 1;
