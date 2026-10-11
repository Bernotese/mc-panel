package console_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type fakeSessions struct {
	user store.User
	err  error
}

func (f fakeSessions) Recheck(context.Context, int64, int64) (store.User, error) {
	return f.user, f.err
}

type failingChecker struct{}

func (failingChecker) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, errors.New("db down")
}

func TestAccess(t *testing.T) {
	db := storetest.Open(t)
	ctx := context.Background()
	srv := storetest.CreateServer(t, db, "survival")
	alice := storetest.CreateUser(t, db, "alice", "user")
	grant(t, db, alice, srv, "server.view", "server.console")
	bob := storetest.CreateUser(t, db, "bob", "user")
	grant(t, db, bob, srv, "server.view")
	checker := rbac.NewDBChecker(db)
	p := principal.Principal{UserID: alice, SessionID: 7, Role: principal.RoleUser, Stage: principal.StageFull}
	sessionGone := errors.New("auth: no valid session")

	for _, tc := range []struct {
		name    string
		s       console.SessionChecker
		c       rbac.Checker
		wantErr error // nil = allowed
		anyErr  bool
	}{
		{"user with permission", fakeSessions{user: store.User{ID: alice, Role: "user"}}, checker, nil, false},
		{"session gone", fakeSessions{err: sessionGone}, checker, sessionGone, false},
		{"permission revoked", fakeSessions{user: store.User{ID: bob, Role: "user"}}, checker, console.ErrAccessRevoked, false},
		{"admin needs no grant", fakeSessions{user: store.User{ID: bob, Role: "admin"}}, checker, nil, false},
		{"checker fails", fakeSessions{user: store.User{ID: alice, Role: "user"}}, failingChecker{}, nil, true},
	} {
		err := console.NewAccess(tc.s, tc.c).Recheck(ctx, p, srv, rbac.PermServerConsole)
		switch {
		case tc.anyErr:
			if err == nil {
				t.Errorf("%s: err = nil, want an error", tc.name)
			}
		case tc.wantErr == nil && err != nil:
			t.Errorf("%s: err = %v, want nil", tc.name, err)
		case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
		}
	}
}
