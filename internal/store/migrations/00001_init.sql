-- +goose Up
CREATE TABLE users (
    id                  INTEGER PRIMARY KEY,
    username            TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    password_hash       TEXT    NOT NULL,
    role                TEXT    NOT NULL CHECK (role IN ('admin', 'user')),
    locale              TEXT    NOT NULL DEFAULT 'de' CHECK (locale IN ('de', 'en')),
    webauthn_handle     BLOB    NOT NULL UNIQUE,
    totp_secret_enc     BLOB,
    totp_enabled        INTEGER NOT NULL DEFAULT 0,
    totp_last_step      INTEGER NOT NULL DEFAULT 0,
    password_changed_at INTEGER NOT NULL,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

CREATE TABLE webauthn_credentials (
    id              INTEGER PRIMARY KEY,
    user_id         INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id   BLOB    NOT NULL UNIQUE,
    name            TEXT    NOT NULL,
    credential_json TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    last_used_at    INTEGER
);
CREATE INDEX webauthn_credentials_user ON webauthn_credentials (user_id);

CREATE TABLE recovery_codes (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  BLOB    NOT NULL,
    used_at    INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX recovery_codes_user ON recovery_codes (user_id);

CREATE TABLE sessions (
    id             INTEGER PRIMARY KEY,
    token_hash     BLOB    NOT NULL UNIQUE,
    user_id        INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    stage          TEXT    NOT NULL CHECK (stage IN ('preauth', 'setup', 'full')),
    csrf_token     TEXT    NOT NULL,
    ip             TEXT    NOT NULL,
    user_agent     TEXT    NOT NULL,
    country        TEXT    NOT NULL DEFAULT '',
    webauthn_state TEXT,
    sudo_until     INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    last_seen_at   INTEGER NOT NULL,
    expires_at     INTEGER NOT NULL,
    revoked_at     INTEGER
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE servers (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);

CREATE TABLE permissions (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    server_id  INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    permission TEXT    NOT NULL CHECK (permission IN ('server.view', 'server.power', 'server.console',
                                                      'server.files', 'server.plugins', 'server.backups')),
    PRIMARY KEY (user_id, server_id, permission)
);

CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY,
    ts         INTEGER NOT NULL,
    actor_type TEXT    NOT NULL CHECK (actor_type IN ('user', 'system', 'cli')),
    actor_id   INTEGER,
    actor_name TEXT    NOT NULL,
    action     TEXT    NOT NULL,
    target     TEXT    NOT NULL DEFAULT '',
    ip         TEXT    NOT NULL DEFAULT '',
    result     TEXT    NOT NULL CHECK (result IN ('success', 'failure', 'denied')),
    details    TEXT    NOT NULL DEFAULT '{}',
    prev_hash  TEXT    NOT NULL,
    hash       TEXT    NOT NULL UNIQUE
);
CREATE INDEX audit_log_ts ON audit_log (ts);

-- +goose StatementBegin
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;
-- +goose StatementEnd

CREATE TABLE login_attempts (
    id         INTEGER PRIMARY KEY,
    ts         INTEGER NOT NULL,
    ip         TEXT    NOT NULL,
    ip_key     TEXT    NOT NULL,
    username   TEXT    NOT NULL DEFAULT '',
    user_id    INTEGER,
    stage      TEXT    NOT NULL CHECK (stage IN ('password', 'totp', 'webauthn', 'recovery', 'sudo')),
    success    INTEGER NOT NULL,
    reason     TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT '',
    country    TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX login_attempts_ts ON login_attempts (ts);
CREATE INDEX login_attempts_ipkey_ts ON login_attempts (ip_key, ts);
CREATE INDEX login_attempts_user_ts ON login_attempts (user_id, ts);

CREATE TABLE jails (
    id             INTEGER PRIMARY KEY,
    ip_key         TEXT    NOT NULL,
    reason         TEXT    NOT NULL,
    level          INTEGER NOT NULL,
    manual         INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    expires_at     INTEGER NOT NULL,
    lifted_at      INTEGER,
    lifted_by      TEXT,
    fw_banned_at   INTEGER,
    fw_unbanned_at INTEGER
);
CREATE INDEX jails_ipkey_expires ON jails (ip_key, expires_at);

CREATE TABLE ip_rules (
    id         INTEGER PRIMARY KEY,
    cidr       TEXT    NOT NULL,
    kind       TEXT    NOT NULL CHECK (kind IN ('allow', 'deny')),
    comment    TEXT    NOT NULL DEFAULT '',
    expires_at INTEGER,
    created_by TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (cidr, kind)
);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE notifiers (
    id         INTEGER PRIMARY KEY,
    kind       TEXT    NOT NULL CHECK (kind IN ('discord', 'ntfy')),
    name       TEXT    NOT NULL UNIQUE,
    config_enc BLOB    NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE notifiers;
DROP TABLE settings;
DROP TABLE ip_rules;
DROP TABLE jails;
DROP TABLE login_attempts;
DROP TRIGGER audit_log_no_delete;
DROP TRIGGER audit_log_no_update;
DROP TABLE audit_log;
DROP TABLE permissions;
DROP TABLE servers;
DROP TABLE sessions;
DROP TABLE recovery_codes;
DROP TABLE webauthn_credentials;
DROP TABLE users;
