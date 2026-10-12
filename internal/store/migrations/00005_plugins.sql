-- +goose Up
-- Sub-project 3c: origin of the jars the panel installed. The disk is the source of truth; a row
-- only counts while a jar of this name (with or without ".disabled") with exactly this sha512
-- lies in plugins/ resp. mods/ (spec §6.1), otherwise the inventory deletes it.
CREATE TABLE plugin_installs (
    server_id    INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    filename     TEXT    NOT NULL,   -- file name without ".disabled"
    sha512       TEXT    NOT NULL,
    source       TEXT    NOT NULL CHECK (source IN ('modrinth', 'hangar')),
    project_id   TEXT    NOT NULL,   -- Modrinth ID resp. Hangar ID (number as text)
    project_slug TEXT    NOT NULL,
    title        TEXT    NOT NULL,
    version_id   TEXT    NOT NULL,   -- Modrinth version ID resp. Hangar version name
    version_name TEXT    NOT NULL,
    channel      TEXT    NOT NULL CHECK (channel IN ('release', 'beta', 'alpha')),
    installed_at INTEGER NOT NULL,
    PRIMARY KEY (server_id, filename)
);

-- +goose Down
DROP TABLE plugin_installs;
