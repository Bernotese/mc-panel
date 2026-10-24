-- +goose Up
-- Set when an admin sets the password of another user (POST /users, PATCH /users/{id}); the
-- user's session is then limited to changing it (stage "password", SEC-45). Cleared by
-- POST /me/password.
ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN must_change_password;
