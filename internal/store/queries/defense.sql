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

-- name: DefenseCountPendingSync :one
SELECT count(*) FROM jails
WHERE (fw_banned_at IS NULL AND lifted_at IS NULL AND expires_at > sqlc.arg(now))
   OR (lifted_at IS NOT NULL AND fw_banned_at IS NOT NULL AND fw_unbanned_at IS NULL);

-- name: DefenseDeleteAttemptsBefore :execrows
DELETE FROM login_attempts WHERE ts < sqlc.arg(before);
