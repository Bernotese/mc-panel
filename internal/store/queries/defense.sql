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
