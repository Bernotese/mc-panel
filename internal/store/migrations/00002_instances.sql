-- +goose Up
CREATE TABLE instances (
    server_id      INTEGER PRIMARY KEY REFERENCES servers (id) ON DELETE CASCADE,
    type           TEXT    NOT NULL CHECK (type IN ('vanilla', 'paper', 'purpur', 'fabric',
                                                    'neoforge', 'modrinth', 'curseforge')),
    version        TEXT    NOT NULL,
    loader_version TEXT,
    modpack        TEXT,
    java           TEXT    NOT NULL CHECK (java IN ('java8', 'java17', 'java21', 'java25')),
    memory_mib     INTEGER NOT NULL CHECK (memory_mib BETWEEN 1024 AND 32768),
    properties     TEXT    NOT NULL,
    desired_state  TEXT    NOT NULL CHECK (desired_state IN ('running', 'stopped')),
    revision       INTEGER NOT NULL,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);

CREATE TABLE instance_settings (
    id                 INTEGER PRIMARY KEY CHECK (id = 1),
    base_domain        TEXT,
    curseforge_api_key BLOB,
    updated_at         INTEGER NOT NULL
);
INSERT INTO instance_settings (id, base_domain, curseforge_api_key, updated_at) VALUES (1, NULL, NULL, 0);

-- +goose Down
DROP TABLE instance_settings;
DROP TABLE instances;
