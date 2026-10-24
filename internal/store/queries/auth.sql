-- Stream A (auth). All query names carry the prefix "Auth".

-- name: AuthGetUserByUsername :one
SELECT * FROM users WHERE username = sqlc.arg(username);

-- name: AuthGetUserByID :one
SELECT * FROM users WHERE id = sqlc.arg(id);

-- name: AuthRehashPassword :exec
UPDATE users SET password_hash = sqlc.arg(password_hash), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: AuthChangePassword :exec
-- The user's own change; it also ends a forced change (must_change_password, SEC-45).
UPDATE users SET password_hash = sqlc.arg(password_hash), must_change_password = 0, password_changed_at = sqlc.arg(now), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: AuthUpdateLocale :exec
UPDATE users SET locale = sqlc.arg(locale), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: AuthSetPendingTOTP :execrows
UPDATE users SET totp_secret_enc = sqlc.arg(totp_secret_enc), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND totp_enabled = 0;

-- name: AuthEnableTOTP :execrows
-- Compare-and-swap on the pending blob: a concurrent setup that replaced the secret makes this a no-op.
UPDATE users SET totp_enabled = 1, totp_secret_enc = sqlc.arg(secret_enc), totp_last_step = sqlc.arg(step), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND totp_enabled = 0 AND totp_secret_enc = sqlc.arg(pending_enc) AND totp_last_step < sqlc.arg(step);

-- name: AuthDisableTOTP :execrows
UPDATE users SET totp_enabled = 0, totp_secret_enc = NULL, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND totp_enabled = 1;

-- name: AuthAdvanceTOTPStep :execrows
UPDATE users SET totp_last_step = sqlc.arg(step)
WHERE id = sqlc.arg(id) AND totp_enabled = 1 AND totp_last_step < sqlc.arg(step);

-- name: AuthListCredentials :many
SELECT * FROM webauthn_credentials WHERE user_id = sqlc.arg(user_id) ORDER BY id;

-- name: AuthCountCredentials :one
SELECT count(*) FROM webauthn_credentials WHERE user_id = sqlc.arg(user_id);

-- name: AuthInsertCredential :one
INSERT INTO webauthn_credentials (user_id, credential_id, name, credential_json, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(credential_id), sqlc.arg(name), sqlc.arg(credential_json), sqlc.arg(created_at))
RETURNING *;

-- name: AuthUpdateCredentialUse :execrows
-- Conditional on the previous credential_json: of two assertions that were validated against the
-- same stored sign counter exactly one wins.
UPDATE webauthn_credentials SET credential_json = sqlc.arg(credential_json), last_used_at = sqlc.arg(last_used_at)
WHERE id = sqlc.arg(id) AND credential_json = sqlc.arg(old_credential_json);

-- name: AuthGetCredential :one
SELECT * FROM webauthn_credentials WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: AuthDeleteCredential :execrows
DELETE FROM webauthn_credentials WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: AuthDeleteRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id = sqlc.arg(user_id);

-- name: AuthInsertRecoveryCode :exec
INSERT INTO recovery_codes (user_id, code_hash, created_at)
VALUES (sqlc.arg(user_id), sqlc.arg(code_hash), sqlc.arg(created_at));

-- name: AuthUseRecoveryCode :execrows
UPDATE recovery_codes SET used_at = sqlc.arg(used_at)
WHERE user_id = sqlc.arg(user_id) AND code_hash = sqlc.arg(code_hash) AND used_at IS NULL;

-- name: AuthCountUnusedRecoveryCodes :one
SELECT count(*) FROM recovery_codes WHERE user_id = sqlc.arg(user_id) AND used_at IS NULL;

-- name: AuthCreateSession :one
INSERT INTO sessions (token_hash, user_id, stage, csrf_token, ip, user_agent, country, created_at, last_seen_at, expires_at)
VALUES (sqlc.arg(token_hash), sqlc.arg(user_id), sqlc.arg(stage), sqlc.arg(csrf_token), sqlc.arg(ip), sqlc.arg(user_agent),
        sqlc.arg(country), sqlc.arg(created_at), sqlc.arg(last_seen_at), sqlc.arg(expires_at))
RETURNING *;

-- name: AuthGetSessionByTokenHash :one
SELECT * FROM sessions WHERE token_hash = sqlc.arg(token_hash);

-- name: AuthGetSessionByID :one
SELECT * FROM sessions WHERE id = sqlc.arg(id);

-- name: AuthRotateSession :execrows
-- Compare-and-swap on the presented token hash and stage: concurrent rotations of one session
-- (e.g. two second-factor completions) yield exactly one winner.
UPDATE sessions
SET token_hash = sqlc.arg(token_hash), stage = sqlc.arg(stage), csrf_token = sqlc.arg(csrf_token),
    sudo_until = sqlc.arg(sudo_until), expires_at = sqlc.arg(expires_at), last_seen_at = sqlc.arg(last_seen_at),
    webauthn_state = NULL
WHERE id = sqlc.arg(id) AND revoked_at IS NULL AND token_hash = sqlc.arg(old_token_hash) AND stage = sqlc.arg(old_stage);

-- name: AuthTouchSession :exec
UPDATE sessions SET last_seen_at = sqlc.arg(last_seen_at) WHERE id = sqlc.arg(id);

-- name: AuthSetWebAuthnState :exec
UPDATE sessions SET webauthn_state = sqlc.arg(webauthn_state) WHERE id = sqlc.arg(id);

-- name: AuthGetWebAuthnState :one
SELECT webauthn_state FROM sessions WHERE id = sqlc.arg(id);

-- name: AuthClearWebAuthnState :execrows
-- Compare-and-swap on the stored value: of several concurrent takers exactly one gets rows = 1.
-- (SQLite's RETURNING yields post-update values and sqlc rejects UPDATE ... FROM, so a plain
-- "UPDATE ... RETURNING" cannot hand back the old state.)
UPDATE sessions SET webauthn_state = NULL
WHERE id = sqlc.arg(id) AND webauthn_state = sqlc.arg(state);

-- name: AuthRevokeSession :execrows
UPDATE sessions SET revoked_at = sqlc.arg(revoked_at) WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: AuthRevokeUserSession :execrows
UPDATE sessions SET revoked_at = sqlc.arg(revoked_at)
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- name: AuthRevokeOtherSessions :execrows
UPDATE sessions SET revoked_at = sqlc.arg(revoked_at)
WHERE user_id = sqlc.arg(user_id) AND id != sqlc.arg(keep_id) AND revoked_at IS NULL;

-- name: AuthListActiveSessionsForUser :many
SELECT s.id, s.user_id, u.username, s.ip, s.country, s.user_agent, s.created_at, s.last_seen_at, s.expires_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.revoked_at IS NULL AND s.expires_at > sqlc.arg(now) AND s.last_seen_at > sqlc.arg(idle_cutoff)
  AND s.user_id = sqlc.arg(user_id)
ORDER BY s.last_seen_at DESC, s.id DESC;

-- name: AuthListAllActiveSessions :many
SELECT s.id, s.user_id, u.username, s.ip, s.country, s.user_agent, s.created_at, s.last_seen_at, s.expires_at
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.revoked_at IS NULL AND s.expires_at > sqlc.arg(now) AND s.last_seen_at > sqlc.arg(idle_cutoff)
ORDER BY s.last_seen_at DESC, s.id DESC;

-- name: AuthPruneSessions :execrows
-- Removes sessions that ended (revoked or expired) before the cutoff.
DELETE FROM sessions
WHERE expires_at < sqlc.arg(cutoff) OR (revoked_at IS NOT NULL AND revoked_at < sqlc.arg(cutoff));
