-- +goose Up
-- Like server_ids (00003): without AUTOINCREMENT SQLite reuses the ID of the highest deleted
-- user, so a new user inherited its login history (known devices, last login) and audit
-- actor_ids became ambiguous. A counter row keeps user IDs monotonic.
CREATE TABLE user_ids (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    last_id INTEGER NOT NULL
);
INSERT INTO user_ids (id, last_id) SELECT 1, COALESCE(MAX(id), 0) FROM users;

-- +goose Down
DROP TABLE user_ids;
