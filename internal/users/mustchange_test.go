package users_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// SEC-45: a password an admin chose for someone else must be changed by its owner at the next sign-in.
func TestAdminSetPasswordForcesChange(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", alice), map[string]string{"password": newPassword}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", rec.Code, rec.Body)
	}
	if out := decode[userResp](t, rec); out.MustChange == nil || !*out.MustChange {
		t.Fatalf("User.must_change_password = %v, want true", out.MustChange)
	}
	if mustChangePassword(t, e.db, alice) != 1 {
		t.Fatal("flag not set")
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if forced, _ := en.Details["must_change_password"].(bool); en.Action != audit.ActionUserUpdate || !forced {
		t.Fatalf("audit = %+v", en)
	}

	// A role change alone leaves the flag as it is.
	mustExec(t, e.db, `UPDATE users SET must_change_password = 0 WHERE id = ?`, alice)
	if rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", alice), map[string]string{"role": "admin"}, &admin); rec.Code != http.StatusOK {
		t.Fatalf("role change = %d %s", rec.Code, rec.Body)
	}
	if mustChangePassword(t, e.db, alice) != 0 {
		t.Fatal("a role change must not force a password change")
	}
}

// Review Focus 3: an admin setting their own password through the user management is not forced.
func TestAdminSetsOwnPasswordNoForcedChange(t *testing.T) {
	e := newEnv(t)
	// (An admin with a pending forced change is limited to POST /me/password, so the flag starts at 0.)
	admin := e.admin()
	rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", e.rootID), map[string]string{"password": newPassword}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s", rec.Code, rec.Body)
	}
	if out := decode[userResp](t, rec); out.MustChange == nil || *out.MustChange {
		t.Fatalf("User.must_change_password = %v, want false", out.MustChange)
	}
	if mustChangePassword(t, e.db, e.rootID) != 0 {
		t.Fatal("own password must not force a change")
	}
	if en := e.rec.Entries[len(e.rec.Entries)-1]; en.Details["must_change_password"] != nil {
		t.Fatalf("audit details = %v", en.Details)
	}
}

func TestCreateUserForcesChange(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/users", map[string]string{"username": "alice", "password": goodPassword, "role": "user"}, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body)
	}
	out := decode[userResp](t, rec)
	if out.MustChange == nil || !*out.MustChange {
		t.Fatalf("User.must_change_password = %v, want true", out.MustChange)
	}
	if mustChangePassword(t, e.db, out.ID) != 1 {
		t.Fatal("flag not set")
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if forced, _ := en.Details["must_change_password"].(bool); en.Action != audit.ActionUserCreate || !forced {
		t.Fatalf("audit = %+v", en)
	}
	list := decode[[]userResp](t, e.do(http.MethodGet, "/users", nil, &admin))
	for _, u := range list {
		if u.MustChange == nil || *u.MustChange != (u.ID == out.ID) {
			t.Fatalf("list: %s must_change_password = %v", u.Username, u.MustChange)
		}
	}
}
