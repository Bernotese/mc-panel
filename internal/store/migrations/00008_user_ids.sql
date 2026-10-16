-- +goose Up
-- Like server_ids (00003): without AUTOINCREMENT SQLite reuses the ID of the highest deleted
-- user, so a new user inherited its login history (known devices, last login) and audit
-- actor_ids became ambiguous. A counter row keeps user IDs monotonic. It starts above every ID
-- still referenced, also by a user deleted before this migration (login attempts, audit log).
CREATE TABLE user_ids (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    last_id INTEGER NOT NULL
);
INSERT INTO user_ids (id, last_id) SELECT 1, max(
    COALESCE((SELECT MAX(id) FROM users), 0),
    COALESCE((SELECT MAX(user_id) FROM login_attempts), 0),
    COALESCE((SELECT MAX(actor_id) FROM audit_log WHERE actor_type = 'user'), 0));

-- +goose Down
DROP TABLE user_ids;
