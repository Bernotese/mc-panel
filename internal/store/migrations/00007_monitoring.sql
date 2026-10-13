-- +goose Up
-- Sub-project 5: monitoring history in three resolutions, alert settings, the per-server
-- alert switch and the persistent alert state.
CREATE TABLE metrics_raw (
    series INTEGER NOT NULL,   -- 0 = host, otherwise servers.id
    metric TEXT    NOT NULL,
    at     INTEGER NOT NULL,   -- Unix ms, tick time
    value  REAL    NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE metrics_5m (
    series INTEGER NOT NULL, metric TEXT NOT NULL, at INTEGER NOT NULL, -- start of the interval
    avg REAL NOT NULL, min REAL NOT NULL, max REAL NOT NULL, samples INTEGER NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE metrics_1h (
    series INTEGER NOT NULL, metric TEXT NOT NULL, at INTEGER NOT NULL,
    avg REAL NOT NULL, min REAL NOT NULL, max REAL NOT NULL, samples INTEGER NOT NULL,
    PRIMARY KEY (series, metric, at)
) WITHOUT ROWID;
CREATE TABLE monitoring_settings (
    id               INTEGER PRIMARY KEY CHECK (id = 1),
    disk_min_percent INTEGER NOT NULL DEFAULT 10,           -- 1–50
    disk_min_bytes   INTEGER NOT NULL DEFAULT 10737418240,  -- 1 GiB – 10 TiB
    down_after_min   INTEGER NOT NULL DEFAULT 3,            -- 1–60
    crash_restarts   INTEGER NOT NULL DEFAULT 3,            -- 2–20
    crash_window_min INTEGER NOT NULL DEFAULT 10,           -- 1–120
    updated_at       INTEGER NOT NULL
);
INSERT INTO monitoring_settings (id, updated_at) VALUES (1, 0);
CREATE TABLE server_alerts (
    server_id   INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    down_alerts INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE alert_state (
    key           TEXT PRIMARY KEY,  -- e.g. "server.down:7", "server.crash_loop:7", "host.disk_low"
    firing        INTEGER NOT NULL,
    since         INTEGER NOT NULL,
    last_notified INTEGER NOT NULL,
    detail        TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE alert_state;
DROP TABLE server_alerts;
DROP TABLE monitoring_settings;
DROP TABLE metrics_1h;
DROP TABLE metrics_5m;
DROP TABLE metrics_raw;
