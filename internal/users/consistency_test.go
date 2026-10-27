package users_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// Credential writes for another user re-check, inside their transaction, the authorization the
// request started with: the admin's session is still active and the caller still an admin.
func TestCredentialResetsNeedCurrentAdminAuthorization(t *testing.T) {
	writes := map[string]struct {
		method, path string
		body         any
	}{
		"password":  {http.MethodPatch, "/users/%d", map[string]string{"password": newPassword}},
		"role":      {http.MethodPatch, "/users/%d", map[string]string{"role": "admin"}},
		"reset 2fa": {http.MethodPost, "/users/%d/reset-2fa", nil},
	}
	callers := map[string]func(e *env){
		"session revoked": func(e *env) {
			mustExec(e.t, e.db, `UPDATE sessions SET revoked_at = 1 WHERE id = ?`, e.rootSession)
		},
		"session expired": func(e *env) {
			mustExec(e.t, e.db, `UPDATE sessions SET expires_at = ? WHERE id = ?`, t0.UnixMilli(), e.rootSession)
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
				alice := storetest.CreateUser(t, e.db, "alice", "user")
				hash, err := password.Hash(goodPassword)
				if err != nil {
					t.Fatal(err)
				}
				mustExec(t, e.db, `UPDATE users SET password_hash = ?, totp_enabled = 1, totp_secret_enc = x'01' WHERE id = ?`, hash, alice)
				aliceSess := insertSession(t, e.db, alice, sql.NullInt64{})
				admin := e.admin() // authorized when the request started ...
				change(e)          // ... and no longer when it writes
				rec := e.do(wr.method, fmt.Sprintf(wr.path, alice), wr.body, &admin)
				if rec.Code != http.StatusUnauthorized || errCode(t, rec) != "unauthorized" {
					t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
				}
				var gotHash, role string
				var totp int
				if err := e.db.QueryRow(`SELECT password_hash, role, totp_enabled FROM users WHERE id = ?`, alice).Scan(&gotHash, &role, &totp); err != nil {
					t.Fatal(err)
				}
				if gotHash != hash || role != "user" || totp != 1 || sessionRevokedAt(t, e.db, aliceSess).Valid {
					t.Fatalf("target changed: hash kept=%v role=%s totp=%d", gotHash == hash, role, totp)
				}
			})
		}
	}
}
