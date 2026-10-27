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

-- name: PluginsDeleteDevInstalls :exec
-- Dev instances: forgets the origins of a dev's jars before PluginsCopyInstalls. Only matches a
-- dev, never prod.
DELETE FROM plugin_installs
WHERE plugin_installs.server_id = sqlc.arg(server_id)
  AND plugin_installs.server_id IN (SELECT d.server_id FROM dev_instances d);

-- name: PluginsCopyInstalls :exec
-- Dev instances: copies the origins of prod's jars to its dev after a sync. Only writes rows of
-- target_id when it is the dev of source_id.
INSERT INTO plugin_installs (server_id, filename, sha512, source, project_id, project_slug, title,
                             version_id, version_name, channel, installed_at)
SELECT sqlc.arg(target_id), p.filename, p.sha512, p.source, p.project_id, p.project_slug, p.title,
       p.version_id, p.version_name, p.channel, p.installed_at
FROM plugin_installs p
WHERE p.server_id = sqlc.arg(source_id)
  AND EXISTS (SELECT 1 FROM dev_instances d WHERE d.server_id = sqlc.arg(target_id) AND d.source_id = sqlc.arg(source_id))
ON CONFLICT (server_id, filename) DO UPDATE SET
    sha512 = excluded.sha512, source = excluded.source, project_id = excluded.project_id,
    project_slug = excluded.project_slug, title = excluded.title, version_id = excluded.version_id,
    version_name = excluded.version_name, channel = excluded.channel, installed_at = excluded.installed_at;
