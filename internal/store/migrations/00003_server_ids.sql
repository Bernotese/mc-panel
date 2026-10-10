-- +goose Up
-- SQLite hands out MAX(rowid) + 1 for INTEGER PRIMARY KEY without AUTOINCREMENT, so the ID of
-- the highest deleted server came back for the next new one, which then adopted the deleted
-- server's leftover container and world. Rebuilding the referenced servers table would
-- cascade-delete permissions and instances; a counter row keeps server IDs monotonic instead.
CREATE TABLE server_ids (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    last_id INTEGER NOT NULL
);
INSERT INTO server_ids (id, last_id) SELECT 1, COALESCE(MAX(id), 0) FROM servers;

-- +goose Down
DROP TABLE server_ids;
