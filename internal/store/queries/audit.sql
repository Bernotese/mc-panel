-- Stream D: audit log. All query names start with "Audit".

-- name: AuditLast :one
SELECT id, hash FROM audit_log ORDER BY id DESC LIMIT 1;

-- name: AuditInsert :exec
INSERT INTO audit_log (id, ts, actor_type, actor_id, actor_name, action, target, ip, result, details, prev_hash, hash)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: AuditListAfter :many
SELECT * FROM audit_log WHERE id > ? ORDER BY id LIMIT ?;

-- name: AuditList :many
SELECT * FROM audit_log
WHERE actor_name GLOB sqlc.arg(actor_glob)
  AND action GLOB sqlc.arg(action_glob)
  AND result GLOB sqlc.arg(result_glob)
  AND ts >= sqlc.arg(from_ts)
  AND ts <= sqlc.arg(to_ts)
  AND id < sqlc.arg(before_id)
ORDER BY id DESC
LIMIT sqlc.arg(max_rows);
