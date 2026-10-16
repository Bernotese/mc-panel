package users_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const newPassword = "violet tractor 42 umbrella"

func insertSession(t *testing.T, db *sql.DB, userID int64, revoked sql.NullInt64) int64 {
	t.Helper()
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`INSERT INTO sessions (token_hash, user_id, stage, csrf_token, ip, user_agent,
		created_at, last_seen_at, expires_at, revoked_at)
		VALUES (?, ?, 'full', 'csrf', '203.0.113.7', 'test', 0, 0, ?, ?)`,
		tok, userID, t0.Add(time.Hour).UnixMilli(), revoked)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func sessionRevokedAt(t *testing.T, db *sql.DB, id int64) sql.NullInt64 {
	t.Helper()
	var v sql.NullInt64
	if err := db.QueryRow(`SELECT revoked_at FROM sessions WHERE id = ?`, id).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func userRole(t *testing.T, db *sql.DB, id int64) string {
	t.Helper()
	var role string
	if err := db.QueryRow(`SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil {
		t.Fatal(err)
	}
	return role
}

// Review Focus 2: the only admin cannot be demoted.
func TestDemoteLastAdminIsRejected(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", e.rootID), map[string]string{"role": "user"}, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if role := userRole(t, e.db, e.rootID); role != "admin" {
		t.Fatalf("role changed to %q", role)
	}
	if len(e.rec.Entries) != 1 || e.rec.Entries[0].Action != audit.ActionUserUpdate || e.rec.Entries[0].Result != audit.ResultDenied {
		t.Fatalf("audit = %+v", e.rec.Entries)
	}
	if n := len(e.notes.Events); n != 0 {
		t.Fatalf("denied change must not notify, got %d", n)
	}
}

func TestChangeRole(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	second := storetest.CreateUser(t, e.db, "second", "admin")

	rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", second), map[string]string{"role": "user"}, &admin)
	if rec.Code != http.StatusOK || decode[userResp](t, rec).Role != "user" {
		t.Fatalf("demote second: %d %s", rec.Code, rec.Body)
	}
	en := e.rec.Entries[0]
	if en.Action != audit.ActionUserUpdate || en.Result != audit.ResultSuccess ||
		en.Details["role_from"] != "admin" || en.Details["role_to"] != "user" || en.Target != fmt.Sprintf("user:%d", second) {
		t.Fatalf("audit = %+v", en)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventPermissionsChanged}) {
		t.Fatalf("notifications = %v", got)
	}

	// Review Focus 2: root is the only admin again.
	rec = e.do(http.MethodPatch, fmt.Sprintf("/users/%d", e.rootID), map[string]string{"role": "user"}, &admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("demote last admin: %d", rec.Code)
	}

	rec = e.do(http.MethodPatch, fmt.Sprintf("/users/%d", second), map[string]string{"role": "admin"}, &admin)
	if rec.Code != http.StatusOK || userRole(t, e.db, second) != "admin" {
		t.Fatalf("promote: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(http.MethodPatch, fmt.Sprintf("/users/%d", second), map[string]string{"role": "admin"}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-op role change: %d", rec.Code)
	}
	if n := len(e.notes.Events); n != 2 {
		t.Fatalf("expected 2 role notifications (demote, promote), got %d", n)
	}
}

func TestUpdatePassword(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	sess := insertSession(t, e.db, alice, sql.NullInt64{})

	rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", alice), map[string]string{"password": newPassword}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	var hash string
	var changedAt int64
	if err := e.db.QueryRow(`SELECT password_hash, password_changed_at FROM users WHERE id = ?`, alice).Scan(&hash, &changedAt); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := password.Verify(newPassword, hash); !ok || err != nil || changedAt != t0.UnixMilli() {
		t.Fatalf("password not updated: ok=%v err=%v changedAt=%d", ok, err, changedAt)
	}
	if got := sessionRevokedAt(t, e.db, sess); got != (sql.NullInt64{Int64: t0.UnixMilli(), Valid: true}) {
		t.Fatalf("session revoked_at = %+v", got)
	}
	if changed, _ := e.rec.Entries[0].Details["password_changed"].(bool); !changed {
		t.Fatalf("audit details = %+v", e.rec.Entries[0].Details)
	}
	if n := len(e.notes.Events); n != 0 {
		t.Fatalf("password change by admin must not send a role notification, got %d", n)
	}

	for _, body := range []map[string]string{{"password": "short"}, {}, {"role": "root"}} {
		rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", alice), body, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("body %v: code = %d, body %s", body, rec.Code, rec.Body)
		}
	}
	if rec := e.do(http.MethodPatch, "/users/9999", map[string]string{"role": "user"}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user: %d", rec.Code)
	}
}

// Review Focus 2: an admin cannot delete their own account.
func TestDeleteSelfIsRejected(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	storetest.CreateUser(t, e.db, "second", "admin")
	rec := e.do(http.MethodDelete, fmt.Sprintf("/users/%d", e.rootID), nil, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if n := countRows(t, e.db, `SELECT count(*) FROM users WHERE id = ?`, e.rootID); n != 1 {
		t.Fatal("root was deleted")
	}
	if e.rec.Entries[0].Result != audit.ResultDenied {
		t.Fatalf("audit = %+v", e.rec.Entries)
	}
}

// Review Focus 2: the last admin cannot be deleted, even by a principal that is not that admin.
func TestDeleteLastAdminIsRejected(t *testing.T) {
	e := newEnv(t)
	ghost := principal.Principal{UserID: 9999, Username: "ghost", Role: principal.RoleAdmin,
		Stage: principal.StageFull, SudoUntil: t0.Add(time.Minute)}
	rec := e.do(http.MethodDelete, fmt.Sprintf("/users/%d", e.rootID), nil, &ghost)
	if rec.Code != http.StatusConflict {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if n := countRows(t, e.db, `SELECT count(*) FROM users WHERE id = ?`, e.rootID); n != 1 {
		t.Fatal("last admin was deleted")
	}
}

func TestDeleteUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	srv := storetest.CreateServer(t, e.db, "alpha")
	mustExec(t, e.db, `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, 'server.view')`, alice, srv)
	insertSession(t, e.db, alice, sql.NullInt64{})

	rec := e.do(http.MethodDelete, fmt.Sprintf("/users/%d", alice), nil, &admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	for _, q := range []string{
		`SELECT count(*) FROM users WHERE id = ?`,
		`SELECT count(*) FROM permissions WHERE user_id = ?`,
		`SELECT count(*) FROM sessions WHERE user_id = ?`,
	} {
		if n := countRows(t, e.db, q, alice); n != 0 {
			t.Errorf("%s → %d, want 0", q, n)
		}
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionUserDelete}) || e.rec.Entries[0].Details["username"] != "alice" {
		t.Fatalf("audit = %+v", e.rec.Entries)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventUserDeleted}) {
		t.Fatalf("notifications = %v", got)
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/users/%d", alice), nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
}

func TestReset2FA(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	mustExec(t, e.db, `UPDATE users SET totp_secret_enc = x'01', totp_enabled = 1, totp_last_step = 99 WHERE id = ?`, alice)
	mustExec(t, e.db, `INSERT INTO webauthn_credentials (user_id, credential_id, name, credential_json, created_at)
		VALUES (?, x'0102', 'key', '{}', 0)`, alice)
	mustExec(t, e.db, `INSERT INTO recovery_codes (user_id, code_hash, created_at) VALUES (?, x'aa', 0)`, alice)
	active := insertSession(t, e.db, alice, sql.NullInt64{})
	old := insertSession(t, e.db, alice, sql.NullInt64{Int64: 5, Valid: true})
	rootSession := insertSession(t, e.db, e.rootID, sql.NullInt64{})

	rec := e.do(http.MethodPost, fmt.Sprintf("/users/%d/reset-2fa", alice), nil, &admin)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	var secret []byte
	var enabled, step int64
	if err := e.db.QueryRow(`SELECT totp_secret_enc, totp_enabled, totp_last_step FROM users WHERE id = ?`, alice).
		Scan(&secret, &enabled, &step); err != nil {
		t.Fatal(err)
	}
	if secret != nil || enabled != 0 || step != 0 {
		t.Fatalf("totp not cleared: secret=%v enabled=%d step=%d", secret, enabled, step)
	}
	if n := countRows(t, e.db, `SELECT count(*) FROM webauthn_credentials WHERE user_id = ?`, alice); n != 0 {
		t.Fatalf("passkeys left: %d", n)
	}
	if n := countRows(t, e.db, `SELECT count(*) FROM recovery_codes WHERE user_id = ?`, alice); n != 0 {
		t.Fatalf("recovery codes left: %d", n)
	}
	if got := sessionRevokedAt(t, e.db, active); got != (sql.NullInt64{Int64: t0.UnixMilli(), Valid: true}) {
		t.Fatalf("active session revoked_at = %+v", got)
	}
	if got := sessionRevokedAt(t, e.db, old); got != (sql.NullInt64{Int64: 5, Valid: true}) {
		t.Fatalf("already revoked session changed: %+v", got)
	}
	if got := sessionRevokedAt(t, e.db, rootSession); got.Valid {
		t.Fatalf("other user's session revoked: %+v", got)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionUserReset2FA}) {
		t.Fatalf("audit = %v", got)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.Event2FAChanged}) {
		t.Fatalf("notifications = %v", got)
	}
	if rec := e.do(http.MethodPost, "/users/9999/reset-2fa", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown user: %d", rec.Code)
	}
}

// Review Focus (stream): handlers must write audit entries after their own BEGIN IMMEDIATE
// transaction has committed; otherwise the SQLRecorder blocks for busy_timeout and fails.
func TestHandlersWithSQLRecorder(t *testing.T) {
	e := newEnvWithRecorder(t, func(db *sql.DB, c clock.Clock) audit.Recorder { return audit.NewSQLRecorder(db, c) })
	admin := e.admin()
	start := time.Now()

	rec := e.do(http.MethodPost, "/users", map[string]string{"username": "alice", "password": goodPassword, "role": "user"}, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	alice := decode[userResp](t, rec).ID
	if rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", alice), map[string]string{"role": "admin"}, &admin); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodPost, fmt.Sprintf("/users/%d/reset-2fa", alice), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("reset: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/users/%d", alice), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("requests took %v – audit write probably waited for the handler's transaction", d)
	}
	rep, err := audit.Verify(context.Background(), e.db)
	if err != nil || !rep.OK || rep.Checked != 4 {
		t.Fatalf("audit chain = %+v, %v (want 4 valid entries)", rep, err)
	}
	var actor, ip string
	if err := e.db.QueryRow(`SELECT actor_name, ip FROM audit_log WHERE id = 4`).Scan(&actor, &ip); err != nil {
		t.Fatal(err)
	}
	if actor != "root" || ip != "203.0.113.7" {
		t.Fatalf("actor/ip = %q/%q", actor, ip)
	}
}

// Spec §4.3: a role change ends the target's sessions; the caller's own session stays.
func TestChangeRoleRevokesSessions(t *testing.T) {
	e := newEnv(t)
	second := storetest.CreateUser(t, e.db, "second", "admin")
	secondSess := insertSession(t, e.db, second, sql.NullInt64{})
	rootOwn := insertSession(t, e.db, e.rootID, sql.NullInt64{})
	rootOther := insertSession(t, e.db, e.rootID, sql.NullInt64{})
	admin := e.admin()
	admin.SessionID = rootOwn

	rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", e.rootID), map[string]string{"role": "user"}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("self-demote: %d %s", rec.Code, rec.Body)
	}
	if sessionRevokedAt(t, e.db, rootOwn).Valid || !sessionRevokedAt(t, e.db, rootOther).Valid {
		t.Fatal("self-demote must keep the caller's session and end the other one")
	}
	if got := e.rec.Entries[0].Details["revoked_sessions"]; got != int64(1) {
		t.Fatalf("audit revoked_sessions = %v", got)
	}
	if sessionRevokedAt(t, e.db, secondSess).Valid {
		t.Fatal("other users' sessions must stay")
	}

	other := principal.Principal{UserID: second, Username: "second", Role: principal.RoleAdmin, Stage: principal.StageFull, SudoUntil: t0.Add(5 * time.Minute)}
	rec = e.do(http.MethodPatch, fmt.Sprintf("/users/%d", e.rootID), map[string]string{"role": "admin"}, &other)
	if rec.Code != http.StatusOK || !sessionRevokedAt(t, e.db, rootOwn).Valid {
		t.Fatalf("promote by another admin must end the target's sessions: %d %s", rec.Code, rec.Body)
	}
}

// Spec §7.2: an admin setting a user's password lifts that user's second-factor lockout
// (cmd/panel wires OnPasswordSet to the auth handler).
func TestUpdatePasswordCallsHook(t *testing.T) {
	e := newEnv(t)
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	var got []int64
	e.h.OnPasswordSet(func(id int64) { got = append(got, id) })
	admin := e.admin()
	if rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", alice), map[string]string{"role": "admin"}, &admin); rec.Code != http.StatusOK {
		t.Fatalf("role change: %d", rec.Code)
	}
	if rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", alice), map[string]string{"password": newPassword}, &admin); rec.Code != http.StatusOK {
		t.Fatalf("password change: %d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(got, []int64{alice}) {
		t.Fatalf("hook calls = %v, want [%d]", got, alice)
	}
}
