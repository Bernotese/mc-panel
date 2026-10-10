package users_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestCreateUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/users", map[string]string{"username": "alice", "password": goodPassword, "role": "user"}, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	u := decode[userResp](t, rec)
	if u.ID == 0 || u.Username != "alice" || u.Role != "user" || u.TotpEnabled || u.PasskeyCount != 0 ||
		u.LastLoginAt != nil || u.CreatedAt != t0.UnixMilli() {
		t.Fatalf("user = %+v", u)
	}
	if !strings.Contains(rec.Body.String(), `"last_login_at":null`) {
		t.Fatalf("last_login_at must be null: %s", rec.Body)
	}
	var hash string
	var handle []byte
	if err := e.db.QueryRow(`SELECT password_hash, webauthn_handle FROM users WHERE id = ?`, u.ID).Scan(&hash, &handle); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := password.Verify(goodPassword, hash); !ok || err != nil {
		t.Fatalf("stored hash does not verify: %v", err)
	}
	if len(handle) != 32 {
		t.Fatalf("webauthn_handle has %d bytes, want 32", len(handle))
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionUserCreate}) {
		t.Fatalf("audit actions = %v", got)
	}
	en := e.rec.Entries[0]
	if en.Target != fmt.Sprintf("user:%d", u.ID) || en.Result != audit.ResultSuccess ||
		en.Details["username"] != "alice" || en.Details["role"] != "user" {
		t.Fatalf("audit entry = %+v", en)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventUserCreated}) {
		t.Fatalf("notifications = %v", got)
	}
	if ev := e.notes.Events[0]; ev.Username != "alice" || ev.IP != "203.0.113.7" || !ev.Time.Equal(t0) {
		t.Fatalf("event = %+v", ev)
	}
}

func TestCreateUserValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	cases := []struct {
		name               string
		username, pw, role string
	}{
		{"username too short", "ab", goodPassword, "user"},
		{"username with space", "bad name", goodPassword, "user"},
		{"username too long", strings.Repeat("a", 33), goodPassword, "user"},
		{"unknown role", "alice", goodPassword, "root"},
		{"password too short", "alice", "short", "user"},
		{"common password", "alice", "passwordpassword", "user"},
		{"password contains username", "alice", "alice-correct-horse", "user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do(http.MethodPost, "/users", map[string]string{"username": tc.username, "password": tc.pw, "role": tc.role}, &admin)
			if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
				t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
			}
		})
	}
	rec := e.do(http.MethodPost, "/users", map[string]any{"username": "alice", "password": goodPassword, "role": "user", "admin": true}, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "bad_request" {
		t.Fatalf("unknown field: code = %d, body %s", rec.Code, rec.Body)
	}
	if n := len(e.rec.Entries); n != 0 {
		t.Fatalf("rejected requests must not be audited as success, got %d entries", n)
	}
}

func TestCreateUserDuplicateIsCaseInsensitive(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	storetest.CreateUser(t, e.db, "Alice", "user")
	rec := e.do(http.MethodPost, "/users", map[string]string{"username": "alice", "password": goodPassword, "role": "user"}, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
}

func TestListAndGetUsers(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	mustExec(t, e.db, `UPDATE users SET totp_enabled = 1 WHERE id = ?`, alice)
	mustExec(t, e.db, `INSERT INTO webauthn_credentials (user_id, credential_id, name, credential_json, created_at)
		VALUES (?, x'0102', 'yubikey', '{}', 0)`, alice)
	for _, a := range []struct {
		at      time.Time
		stage   string
		success int
	}{
		{t0.Add(-3 * time.Hour), "password", 1},
		{t0.Add(-2 * time.Hour), "totp", 1},    // newest successful login
		{t0.Add(-1 * time.Hour), "sudo", 1},    // sudo is not a login
		{t0.Add(-30 * time.Minute), "totp", 0}, // failed
	} {
		mustExec(t, e.db, `INSERT INTO login_attempts (ts, ip, ip_key, username, user_id, stage, success)
			VALUES (?, '203.0.113.7', '203.0.113.7/32', 'alice', ?, ?, ?)`, a.at.UnixMilli(), alice, a.stage, a.success)
	}

	rec := e.do(http.MethodGet, "/users", nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code = %d, body %s", rec.Code, rec.Body)
	}
	list := decode[[]userResp](t, rec)
	if len(list) != 2 || list[0].Username != "alice" || list[1].Username != "root" {
		t.Fatalf("list = %+v", list)
	}
	a := list[0]
	if !a.TotpEnabled || a.PasskeyCount != 1 || a.LastLoginAt == nil || *a.LastLoginAt != t0.Add(-2*time.Hour).UnixMilli() {
		t.Fatalf("alice = %+v", a)
	}
	if list[1].LastLoginAt != nil || list[1].Role != "admin" {
		t.Fatalf("root = %+v", list[1])
	}

	rec = e.do(http.MethodGet, fmt.Sprintf("/users/%d", alice), nil, &admin)
	got := decode[userResp](t, rec)
	if rec.Code != http.StatusOK || got.ID != a.ID || got.Username != a.Username || got.TotpEnabled != a.TotpEnabled ||
		got.PasskeyCount != a.PasskeyCount || got.LastLoginAt == nil || *got.LastLoginAt != *a.LastLoginAt {
		t.Fatalf("get: code = %d, got %+v, want %+v", rec.Code, got, a)
	}
	for _, path := range []string{"/users/9999", "/users/abc", "/users/0"} {
		rec = e.do(http.MethodGet, path, nil, &admin)
		if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
			t.Errorf("%s: code = %d, body %s", path, rec.Code, rec.Body)
		}
	}
}

func TestUserRoutesEnforceRules(t *testing.T) {
	e := newEnv(t)
	bob := userP(42, "bob")
	if rec := e.do(http.MethodGet, "/users", nil, &bob); rec.Code != http.StatusForbidden {
		t.Fatalf("user on GET /users: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/users", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous on GET /users: %d", rec.Code)
	}
	noSudo := principal.Principal{UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull}
	rec := e.do(http.MethodPost, "/users", map[string]string{"username": "alice", "password": goodPassword, "role": "user"}, &noSudo)
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("admin without sudo: %d %s", rec.Code, rec.Body)
	}
}
