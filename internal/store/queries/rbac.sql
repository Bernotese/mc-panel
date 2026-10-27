-- Stream D: users, servers, permissions. All query names start with "Rbac".

-- name: RbacListUsers :many
SELECT * FROM users ORDER BY username;

-- name: RbacGetUser :one
SELECT * FROM users WHERE id = ?;

-- name: RbacGetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: RbacNextUserID :one
-- Allocates the ID of a new user: never one that was used before (see migration 00008).
UPDATE user_ids
SET last_id = max(last_id, (SELECT COALESCE(MAX(id), 0) FROM users)) + 1
WHERE id = 1
RETURNING last_id;

-- name: RbacCreateUser :one
INSERT INTO users (id, username, password_hash, role, webauthn_handle, must_change_password, password_changed_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: RbacUpdateUserRole :exec
UPDATE users SET role = ?, updated_at = ? WHERE id = ?;

-- name: RbacUpdateUserPassword :exec
UPDATE users SET password_hash = ?, must_change_password = ?, password_changed_at = ?, updated_at = ? WHERE id = ?;

-- name: RbacDeleteUser :execrows
DELETE FROM users WHERE id = ?;

-- name: RbacCountAdmins :one
SELECT count(*) FROM users WHERE role = 'admin';

-- name: RbacClearTOTP :exec
UPDATE users SET totp_secret_enc = NULL, totp_enabled = 0, totp_last_step = 0, updated_at = ? WHERE id = ?;

-- name: RbacDeleteUserPasskeys :exec
DELETE FROM webauthn_credentials WHERE user_id = ?;

-- name: RbacDeleteUserRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id = ?;

-- name: RbacRevokeUserSessions :exec
UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL;

-- name: RbacRevokeUserSessionsExcept :execrows
UPDATE sessions SET revoked_at = sqlc.arg(revoked_at)
WHERE user_id = sqlc.arg(user_id) AND id <> sqlc.arg(keep_id) AND revoked_at IS NULL;

-- name: RbacCountUserPasskeys :one
SELECT count(*) FROM webauthn_credentials WHERE user_id = ?;

-- name: RbacLastLogin :one
SELECT ts FROM login_attempts
WHERE user_id = ? AND success = 1 AND stage <> 'sudo'
ORDER BY ts DESC
LIMIT 1;

-- name: RbacListServers :many
SELECT * FROM servers ORDER BY name;

-- name: RbacListViewableServers :many
-- A dev instance has no permission rows of its own: it is viewable when its source is.
SELECT * FROM servers
WHERE id IN (SELECT g.server_id FROM permissions g WHERE g.user_id = sqlc.arg(user_id) AND g.permission = 'server.view')
   OR id IN (SELECT d.server_id FROM dev_instances d JOIN permissions p ON p.server_id = d.source_id
             WHERE p.user_id = sqlc.arg(user_id) AND p.permission = 'server.view')
ORDER BY name;

-- name: RbacGetServer :one
SELECT * FROM servers WHERE id = ?;

-- name: RbacGetServerByName :one
SELECT * FROM servers WHERE name = ?;

-- name: RbacCreateServer :one
INSERT INTO servers (name, created_at) VALUES (?, ?)
RETURNING *;

-- name: RbacDeleteServer :execrows
DELETE FROM servers WHERE id = ?;

-- name: RbacHasPermission :one
-- On a dev instance the permissions of its source apply, except server.backups (a dev has no
-- backups); a dev never has permission rows of its own.
SELECT count(*) FROM permissions
WHERE permissions.user_id = sqlc.arg(user_id)
  AND permissions.server_id = COALESCE((SELECT d.source_id FROM dev_instances d WHERE d.server_id = sqlc.arg(server_id)), sqlc.arg(server_id))
  AND permissions.permission = sqlc.arg(permission)
  AND NOT (permissions.permission = 'server.backups'
           AND EXISTS (SELECT 1 FROM dev_instances d WHERE d.server_id = sqlc.arg(server_id)));

-- name: RbacUserHasAnyPermission :one
-- Whether the user holds one of the given permissions on any server (mandatory 2FA, SEC-10).
SELECT EXISTS (
  SELECT 1 FROM permissions WHERE user_id = sqlc.arg(user_id) AND permission IN (sqlc.slice(perms))
);

-- name: RbacListUserGrants :many
-- The stored permission rows of a user (the user editor); RbacListUserPermissions adds the
-- permissions a user has on dev instances through their sources.
SELECT server_id, permission FROM permissions WHERE user_id = ? ORDER BY server_id, permission;

-- name: RbacListUserPermissions :many
-- The effective permissions of a user per server: the stored rows plus, for every dev
-- instance, the rows of its source without server.backups (see RbacHasPermission).
SELECT server_id, permission FROM (
    SELECT p.server_id AS server_id, p.permission AS permission
    FROM permissions p WHERE p.user_id = sqlc.arg(user_id)
    UNION ALL
    SELECT d.server_id AS server_id, p.permission AS permission
    FROM dev_instances d JOIN permissions p ON p.server_id = d.source_id
    WHERE p.user_id = sqlc.arg(user_id) AND p.permission <> 'server.backups'
)
ORDER BY server_id, permission;

-- name: RbacListUserServerPermissions :many
-- The effective permissions of a user on one server (see RbacHasPermission).
SELECT permissions.permission FROM permissions
WHERE permissions.user_id = sqlc.arg(user_id)
  AND permissions.server_id = COALESCE((SELECT d.source_id FROM dev_instances d WHERE d.server_id = sqlc.arg(server_id)), sqlc.arg(server_id))
  AND NOT (permissions.permission = 'server.backups'
           AND EXISTS (SELECT 1 FROM dev_instances d WHERE d.server_id = sqlc.arg(server_id)))
ORDER BY permission;

-- name: RbacDeleteUserPermissions :exec
DELETE FROM permissions WHERE user_id = ?;

-- name: RbacInsertPermission :exec
INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?);
