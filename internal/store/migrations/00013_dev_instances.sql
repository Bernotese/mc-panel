-- +goose Up
-- Dev instances (dev instances spec): a server that is a nightly copy of another one (the
-- source, "prod"). Whether a server is a dev is decided only by a row here.
CREATE TABLE dev_instances (
    server_id          INTEGER PRIMARY KEY REFERENCES servers (id) ON DELETE CASCADE,
    -- no cascade: deleting a prod that has a dev is refused in the handler (409 dev_exists)
    source_id          INTEGER NOT NULL UNIQUE REFERENCES servers (id),
    sync_paused        INTEGER NOT NULL DEFAULT 0,
    paused_at          INTEGER,
    sync_time          INTEGER NOT NULL DEFAULT 300 CHECK (sync_time BETWEEN 0 AND 1439), -- minutes
    idle_minutes       INTEGER NOT NULL DEFAULT 15 CHECK (idle_minutes BETWEEN 5 AND 120),
    property_overrides TEXT    NOT NULL,  -- JSON {"view_distance":4,"simulation_distance":3}
    next_sync_at       INTEGER,
    deferred_since     INTEGER,           -- set while a scheduled sync waits for players to leave
    last_sync_at       INTEGER,
    last_sync_status   TEXT CHECK (last_sync_status IN ('ok', 'failed', 'running')),
    last_sync_error    TEXT,
    last_sync_snapshot TEXT,
    sleeping           INTEGER NOT NULL DEFAULT 1,
    last_wake_at       INTEGER,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    CHECK (server_id <> source_id)
);

-- The dev's own ops list (dev instances spec §5): re-applied after every start, never
-- overwritten by a sync.
CREATE TABLE dev_ops (
    server_id  INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (server_id, name)
);

-- +goose Down
DROP TABLE dev_ops;
DROP TABLE dev_instances;
