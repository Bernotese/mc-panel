package rbac_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestDBChecker(t *testing.T) {
	db := storetest.Open(t)
	alice := storetest.CreateUser(t, db, "alice", "user")
	bob := storetest.CreateUser(t, db, "bob", "user")
	srvA := storetest.CreateServer(t, db, "alpha")
	srvB := storetest.CreateServer(t, db, "beta")
	if _, err := db.Exec(`INSERT INTO permissions (user_id, server_id, permission)
		VALUES (?, ?, 'server.view'), (?, ?, 'server.power')`, alice, srvA, alice, srvA); err != nil {
		t.Fatal(err)
	}
	c := rbac.NewDBChecker(db)
	cases := []struct {
		name         string
		user, server int64
		perm         rbac.Permission
		want         bool
	}{
		{"granted view", alice, srvA, rbac.PermServerView, true},
		{"granted power", alice, srvA, rbac.PermServerPower, true},
		{"not granted console", alice, srvA, rbac.PermServerConsole, false},
		{"other server", alice, srvB, rbac.PermServerView, false},
		{"other user", bob, srvA, rbac.PermServerView, false},
		{"unknown server", alice, 9999, rbac.PermServerView, false},
		{"invalid permission", alice, srvA, rbac.Permission("server.root"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.HasServerPermission(context.Background(), tc.user, tc.server, tc.perm)
			if err != nil || got != tc.want {
				t.Fatalf("HasServerPermission = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

// A dev instance has no permission rows: every check resolves against its source (prod),
// except server.backups, which grants nothing on a dev (dev instances spec §2).
func TestDevPermissionsFollowProd(t *testing.T) {
	db := storetest.Open(t)
	alice := storetest.CreateUser(t, db, "alice", "user")
	bob := storetest.CreateUser(t, db, "bob", "user")
	prod := storetest.CreateServer(t, db, "survival")
	dev := storetest.CreateServer(t, db, "dev-survival")
	other := storetest.CreateServer(t, db, "creative")
	if _, err := db.Exec(`INSERT INTO dev_instances (server_id, source_id, property_overrides, created_at, updated_at)
		VALUES (?, ?, '{}', 0, 0)`, dev, prod); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO permissions (user_id, server_id, permission)
		VALUES (?, ?, 'server.view'), (?, ?, 'server.files'), (?, ?, 'server.backups'), (?, ?, 'server.view')`,
		alice, prod, alice, prod, alice, prod, bob, other); err != nil {
		t.Fatal(err)
	}
	c := rbac.NewDBChecker(db)
	cases := []struct {
		name         string
		user, server int64
		perm         rbac.Permission
		want         bool
	}{
		{"view on dev through prod", alice, dev, rbac.PermServerView, true},
		{"files on dev through prod", alice, dev, rbac.PermServerFiles, true},
		{"backups on dev never", alice, dev, rbac.PermServerBackups, false},
		{"backups on prod unchanged", alice, prod, rbac.PermServerBackups, true},
		{"not granted on prod", alice, dev, rbac.PermServerConsole, false},
		{"other user", bob, dev, rbac.PermServerView, false},
		{"rows of another server do not leak", bob, prod, rbac.PermServerView, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.HasServerPermission(context.Background(), tc.user, tc.server, tc.perm)
			if err != nil || got != tc.want {
				t.Fatalf("HasServerPermission = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	// A stray row on the dev id itself is ignored: the dev resolves to prod only.
	if _, err := db.Exec(`INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, 'server.view')`, bob, dev); err != nil {
		t.Fatal(err)
	}
	if got, err := c.HasServerPermission(context.Background(), bob, dev, rbac.PermServerView); err != nil || got {
		t.Fatalf("row on the dev id = %v, %v; want false", got, err)
	}
}

func TestDBCheckerThroughRouter(t *testing.T) {
	db := storetest.Open(t)
	alice := storetest.CreateUser(t, db, "alice", "user")
	bob := storetest.CreateUser(t, db, "bob", "user")
	srv := storetest.CreateServer(t, db, "alpha")
	if _, err := db.Exec(`INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, 'server.power')`, alice, srv); err != nil {
		t.Fatal(err)
	}
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clock.NewFake(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)))
	rt.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	for _, tc := range []struct {
		name string
		uid  int64
		code int
	}{
		{"alice has server.power", alice, http.StatusNoContent},
		{"bob has nothing", bob, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/servers/%d/power", srv), nil)
			req = req.WithContext(principal.With(req.Context(), principal.Principal{
				UserID: tc.uid, Role: principal.RoleUser, Stage: principal.StageFull,
			}))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("code = %d, want %d (body %s)", rec.Code, tc.code, rec.Body)
			}
		})
	}
}
