-- +goose Up
-- Durable server deletion (issue #148): DELETE /servers/{id} removes the server's rows and
-- leaves this tombstone in the same transaction. The panel retries the cleanup from it (the
-- container through the agent, the backups through the files service) until the agent has
-- confirmed the removal and, if requested, the backups are purged. No foreign key: the server
-- row is gone, and its ID is never handed out again (server_ids).
CREATE TABLE server_deletions (
    server_id       INTEGER PRIMARY KEY,
    name            TEXT    NOT NULL,
    -- none: nothing to remove (no instance, or an agent that manages no containers, make dev
    -- without Docker); pending: not confirmed yet; removed: the agent confirmed it (container
    -- gone, data archived to deleted/); foreign: the container belongs to another server and is
    -- left alone
    container       TEXT    NOT NULL CHECK (container IN ('none', 'pending', 'removed', 'foreign')),
    container_error TEXT    NOT NULL DEFAULT '',
    -- none: not requested; pending/failed: to purge (failed = the last attempt failed);
    -- purged: repository and key are gone
    backups         TEXT    NOT NULL CHECK (backups IN ('none', 'pending', 'purged', 'failed')),
    backups_error   TEXT    NOT NULL DEFAULT '',
    -- 1: an admin closed the deletion without finishing it (leftovers are the operator's)
    abandoned       INTEGER NOT NULL DEFAULT 0,
    -- 1: the server.delete_failed notification for this deletion was sent
    notified        INTEGER NOT NULL DEFAULT 0,
    requested_at    INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,
    completed_at    INTEGER
);
CREATE INDEX server_deletions_open ON server_deletions (completed_at);

-- +goose Down
DROP TABLE server_deletions;
