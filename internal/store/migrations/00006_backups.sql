-- +goose Up
-- Sub-project 4: backup schedule, retention and the last scheduled run per server. The
-- snapshots themselves live in the restic repository of the server (the source of truth).
CREATE TABLE backup_schedules (
    server_id        INTEGER PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    mode             TEXT    NOT NULL DEFAULT 'off' CHECK (mode IN ('off','interval','daily','weekly')),
    interval_hours   INTEGER NOT NULL DEFAULT 24 CHECK (interval_hours BETWEEN 1 AND 168),
    time_of_day      INTEGER NOT NULL DEFAULT 240 CHECK (time_of_day BETWEEN 0 AND 1439), -- minutes
    weekday          INTEGER NOT NULL DEFAULT 0 CHECK (weekday BETWEEN 0 AND 6),          -- 0 = Sunday
    keep_last        INTEGER NOT NULL DEFAULT 24,
    keep_daily       INTEGER NOT NULL DEFAULT 7,
    keep_weekly      INTEGER NOT NULL DEFAULT 4,
    keep_monthly     INTEGER NOT NULL DEFAULT 6,
    next_run_at      INTEGER,          -- NULL with mode = 'off'
    last_run_at      INTEGER,
    last_status      TEXT CHECK (last_status IN ('ok','failed','running')),
    last_error       TEXT,
    last_snapshot_id TEXT,
    saveoff_since    INTEGER,          -- set while the panel has sent save-off
    updated_at       INTEGER NOT NULL
);
ALTER TABLE instance_settings ADD COLUMN backup_timezone TEXT NOT NULL DEFAULT 'Europe/Berlin';

-- +goose Down
ALTER TABLE instance_settings DROP COLUMN backup_timezone;
DROP TABLE backup_schedules;
