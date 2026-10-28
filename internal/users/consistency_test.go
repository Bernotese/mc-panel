package users_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// Every admin write re-checks, inside its transaction, the authorization the request started
// with: the admin's session still fully authenticates the admin and the caller is still an admin.
func TestAdminWritesNeedCurrentAdminAuthorization(t *testing.T) {
	type target struct {
		alice, aliceSess, server int64
		hash                     string
	}
	unchangedAlice := func(e *env, tg target) {
		var hash, role string
		var totp int
		if err := e.db.QueryRow(`SELECT password_hash, role, totp_enabled FROM users WHERE id = ?`, tg.alice).Scan(&hash, &role, &totp); err != nil {
			e.t.Fatalf("alice: %v", err)
		}
		if hash != tg.hash || role != "user" || totp != 1 || sessionRevokedAt(e.t, e.db, tg.aliceSess).Valid {
			e.t.Fatalf("target changed: hash kept=%v role=%s totp=%d", hash == tg.hash, role, totp)
		}
	}
	writes := map[string]struct {
		method, path string
		body         func(tg target) any
		check        func(e *env, tg target)
	}{
		"password":  {http.MethodPatch, "/users/%d", func(target) any { return map[string]string{"password": newPassword} }, unchangedAlice},
		"role":      {http.MethodPatch, "/users/%d", func(target) any { return map[string]string{"role": "admin"} }, unchangedAlice},
		"reset 2fa": {http.MethodPost, "/users/%d/reset-2fa", func(target) any { return nil }, unchangedAlice},
		"delete":    {http.MethodDelete, "/users/%d", func(target) any { return nil }, unchangedAlice},
		"permissions": {http.MethodPut, "/users/%d/permissions", func(tg target) any { return grantsBody(grant(tg.server, "server.files")) },
			func(e *env, tg target) {
				if n := countRows(e.t, e.db, `SELECT count(*) FROM permissions WHERE user_id = ?`, tg.alice); n != 0 {
					e.t.Fatalf("permissions written: %d", n)
				}
			}},
		"create": {http.MethodPost, "/users", func(target) any {
			return map[string]string{"username": "mallory", "password": goodPassword, "role": "admin"}
		}, func(e *env, _ target) {
			if n := countRows(e.t, e.db, `SELECT count(*) FROM users WHERE username = 'mallory'`); n != 0 {
				e.t.Fatal("user created")
			}
		}},
	}
	callers := map[string]func(e *env){
		"session revoked": func(e *env) {
			mustExec(e.t, e.db, `UPDATE sessions SET revoked_at = 1 WHERE id = ?`, e.rootSession)
		},
		"session expired": func(e *env) {
			mustExec(e.t, e.db, `UPDATE sessions SET expires_at = ? WHERE id = ?`, t0.UnixMilli(), e.rootSession)
		},
		"session idle": func(e *env) { e.clk.Advance(31 * time.Minute) },
		"second factor gone": func(e *env) { // mandatory 2FA: the session is back in the setup stage
			mustExec(e.t, e.db, `UPDATE users SET totp_enabled = 0 WHERE id = ?`, e.rootID)
		},
		"caller demoted": func(e *env) {
			storetest.CreateUser(e.t, e.db, "second", "admin")
			mustExec(e.t, e.db, `UPDATE users SET role = 'user' WHERE id = ?`, e.rootID)
		},
	}
	for wname, wr := range writes {
		for cname, change := range callers {
			t.Run(wname+"/"+cname, func(t *testing.T) {
				e := newEnv(t)
				tg := target{alice: storetest.CreateUser(t, e.db, "alice", "user"), server: storetest.CreateServer(t, e.db, "alpha")}
				var err error
				if tg.hash, err = password.Hash(goodPassword); err != nil {
					t.Fatal(err)
				}
				mustExec(t, e.db, `UPDATE users SET password_hash = ?, totp_enabled = 1, totp_secret_enc = x'01' WHERE id = ?`, tg.hash, tg.alice)
				tg.aliceSess = insertSession(t, e.db, tg.alice, sql.NullInt64{})
				admin := e.admin() // authorized when the request started ...
				change(e)          // ... and no longer when it writes
				admin.SudoUntil = e.clk.Now().Add(5 * time.Minute)
				path := wr.path
				if wname != "create" {
					path = fmt.Sprintf(wr.path, tg.alice)
				}
				rec := e.do(wr.method, path, wr.body(tg), &admin)
				if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "unauthorized" {
					t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
				}
				wr.check(e, tg)
			})
		}
	}
}

// Without a session manager the admin writes fail closed.
func TestAdminWritesFailClosedWithoutSessionCheck(t *testing.T) {
	e := newEnv(t)
	e.h.CheckSessionsWith(nil)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/users", map[string]string{"username": "mallory", "password": goodPassword, "role": "user"}, &admin)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if n := countRows(t, e.db, `SELECT count(*) FROM users WHERE username = 'mallory'`); n != 0 {
		t.Fatal("user created")
	}
}
