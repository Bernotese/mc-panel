-- Sub-project 3c: origin of panel-installed plugins and mods. All query names start with "Plugins".

-- name: PluginsListInstalls :many
SELECT * FROM plugin_installs WHERE server_id = ? ORDER BY filename;

-- name: PluginsUpsertInstall :exec
INSERT INTO plugin_installs (server_id, filename, sha512, source, project_id, project_slug, title,
                             version_id, version_name, channel, installed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (server_id, filename) DO UPDATE SET
    sha512 = excluded.sha512, source = excluded.source, project_id = excluded.project_id,
    project_slug = excluded.project_slug, title = excluded.title, version_id = excluded.version_id,
    version_name = excluded.version_name, channel = excluded.channel, installed_at = excluded.installed_at;

-- name: PluginsDeleteInstall :exec
DELETE FROM plugin_installs WHERE server_id = ? AND filename = ?;
