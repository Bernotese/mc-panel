-- +goose Up
-- IPv6 login failures are also counted per /56 and /48 (SEC-09): an attacker with a whole
-- prefix rotates /64s, each of which stays below the per-ip_key threshold. Empty for IPv4 and
-- for attempts recorded before this migration.
ALTER TABLE login_attempts ADD COLUMN net56 TEXT NOT NULL DEFAULT '';
ALTER TABLE login_attempts ADD COLUMN net48 TEXT NOT NULL DEFAULT '';
CREATE INDEX login_attempts_net56_ts ON login_attempts (net56, ts);
CREATE INDEX login_attempts_net48_ts ON login_attempts (net48, ts);

-- +goose Down
DROP INDEX login_attempts_net48_ts;
DROP INDEX login_attempts_net56_ts;
ALTER TABLE login_attempts DROP COLUMN net48;
ALTER TABLE login_attempts DROP COLUMN net56;
