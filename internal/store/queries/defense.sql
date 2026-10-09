-- Stream B (defense + notify). All query names are prefixed with "Defense".

-- name: DefenseListIPRules :many
SELECT * FROM ip_rules ORDER BY kind, cidr;

-- name: DefenseInsertIPRule :one
INSERT INTO ip_rules (cidr, kind, comment, expires_at, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DefenseListActiveJails :many
SELECT * FROM jails
WHERE lifted_at IS NULL AND expires_at > sqlc.arg(now)
ORDER BY created_at DESC, id DESC;

-- name: DefenseInsertJail :one
INSERT INTO jails (ip_key, reason, level, manual, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DefenseInsertAttempt :exec
INSERT INTO login_attempts (ts, ip, ip_key, username, user_id, stage, success, reason, user_agent, country)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DefenseCountFullLoginsFrom :one
SELECT count(*) FROM login_attempts
WHERE user_id = sqlc.arg(user_id) AND ip_key = sqlc.arg(ip_key) AND success = 1
  AND reason = 'complete';

-- name: DefenseLastJailCreatedAt :one
SELECT CAST(COALESCE(MAX(created_at), 0) AS INTEGER) AS last_created_at FROM jails WHERE ip_key = ?;

-- name: DefenseCountFailuresSince :one
SELECT count(*) FROM login_attempts
WHERE ip_key = sqlc.arg(ip_key) AND success = 0 AND ts >= sqlc.arg(since);

-- name: DefenseCountJailsSince :one
SELECT count(*) FROM jails
WHERE ip_key = sqlc.arg(ip_key) AND created_at >= sqlc.arg(since);

-- name: DefenseJailsToBan :many
SELECT * FROM jails
WHERE fw_banned_at IS NULL AND lifted_at IS NULL AND expires_at > sqlc.arg(now)
ORDER BY id;

-- name: DefenseJailsToUnban :many
SELECT * FROM jails
WHERE lifted_at IS NOT NULL AND fw_banned_at IS NOT NULL AND fw_unbanned_at IS NULL
ORDER BY id;

-- name: DefenseActiveBannedJails :many
SELECT * FROM jails
WHERE fw_banned_at IS NOT NULL AND lifted_at IS NULL AND expires_at > sqlc.arg(now)
ORDER BY id;

-- name: DefenseMarkJailBanned :exec
UPDATE jails SET fw_banned_at = sqlc.arg(fw_banned_at) WHERE id = sqlc.arg(id);

-- name: DefenseMarkJailUnbanned :exec
UPDATE jails SET fw_unbanned_at = sqlc.arg(fw_unbanned_at) WHERE id = sqlc.arg(id);

-- name: DefenseDeleteAttemptsBefore :execrows
DELETE FROM login_attempts WHERE ts < sqlc.arg(before);

-- name: DefenseListJails :many
SELECT * FROM jails ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(lim);

-- name: DefenseGetJail :one
SELECT * FROM jails WHERE id = sqlc.arg(id);

-- name: DefenseLiftJail :execrows
UPDATE jails SET lifted_at = sqlc.arg(lifted_at), lifted_by = sqlc.arg(lifted_by)
WHERE id = sqlc.arg(id) AND lifted_at IS NULL;

-- name: DefenseGetIPRule :one
SELECT * FROM ip_rules WHERE id = sqlc.arg(id);

-- name: DefenseDeleteIPRule :execrows
DELETE FROM ip_rules WHERE id = sqlc.arg(id);

-- name: DefenseAttemptSeries :many
SELECT CAST((ts - sqlc.arg(start)) / sqlc.arg(bucket_ms) AS INTEGER) AS bucket,
       count(CASE WHEN success = 1 THEN 1 END) AS successes,
       count(CASE WHEN success = 0 THEN 1 END) AS failures
FROM login_attempts
WHERE ts >= sqlc.arg(since)
GROUP BY bucket
ORDER BY bucket;

-- name: DefenseTopFailingIPs :many
SELECT ip_key, CAST(MAX(country) AS TEXT) AS country, count(*) AS failures
FROM login_attempts
WHERE success = 0 AND ts >= sqlc.arg(since)
GROUP BY ip_key
ORDER BY failures DESC, ip_key
LIMIT 10;

-- name: DefenseTopFailingCountries :many
SELECT country, count(*) AS failures
FROM login_attempts
WHERE success = 0 AND ts >= sqlc.arg(since) AND country <> ''
GROUP BY country
ORDER BY failures DESC, country
LIMIT 10;

-- name: DefenseCountFailuresTotal :one
SELECT count(*) FROM login_attempts WHERE success = 0 AND ts >= sqlc.arg(since);

-- name: DefenseCountActiveJails :one
SELECT count(*) FROM jails WHERE lifted_at IS NULL AND expires_at > sqlc.arg(now);

-- name: DefenseNewDeviceLogins :many
SELECT a.ts, a.username, a.ip, a.country, a.user_agent
FROM login_attempts a
WHERE a.success = 1 AND a.reason = 'complete' AND a.user_id IS NOT NULL
  AND a.ts >= sqlc.arg(since)
  AND NOT EXISTS (
      SELECT 1 FROM login_attempts b
      WHERE b.user_id = a.user_id AND b.ip_key = a.ip_key AND b.success = 1
        AND b.reason = 'complete' AND b.id < a.id)
ORDER BY a.ts DESC, a.id DESC
LIMIT 50;

-- name: DefenseListAttempts :many
SELECT * FROM login_attempts
WHERE id < sqlc.arg(before_id)
  AND (sqlc.arg(any_success) = 1 OR success = sqlc.arg(success))
  AND (sqlc.arg(any_ip) = 1 OR ip = sqlc.arg(ip) OR ip_key = sqlc.arg(ip_key))
ORDER BY id DESC
LIMIT sqlc.arg(lim);

-- name: DefenseLiftAllActiveJails :execrows
UPDATE jails SET lifted_at = sqlc.arg(lifted_at), lifted_by = sqlc.arg(lifted_by)
WHERE lifted_at IS NULL AND expires_at > sqlc.arg(now);

-- name: DefenseListEnabledNotifiers :many
SELECT * FROM notifiers WHERE enabled = 1 ORDER BY id;

-- name: DefenseGetNotifier :one
SELECT * FROM notifiers WHERE id = sqlc.arg(id);

-- name: DefenseInsertNotifier :one
INSERT INTO notifiers (kind, name, config_enc, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DefenseListNotifiers :many
SELECT * FROM notifiers ORDER BY name;

-- name: DefenseUpdateNotifier :one
UPDATE notifiers
SET name = sqlc.arg(name), config_enc = sqlc.arg(config_enc), enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DefenseDeleteNotifier :execrows
DELETE FROM notifiers WHERE id = sqlc.arg(id);
