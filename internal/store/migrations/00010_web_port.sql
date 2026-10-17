-- +goose Up
-- Optional web port per server (web ports spec §3.1); NULL = off. Not part of the instance
-- spec: changing it never bumps revision.
ALTER TABLE instances ADD COLUMN web_port INTEGER
  CHECK (web_port IS NULL OR (web_port BETWEEN 1024 AND 65535 AND web_port NOT IN (25565, 25575)));

-- +goose Down
ALTER TABLE instances DROP COLUMN web_port;
