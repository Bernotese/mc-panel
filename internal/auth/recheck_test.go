package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestRecheck(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(sessionStart)
	q := store.New(db)
	s := NewSessions(q, clk, config.DefaultPanel().Session)
	ctx := context.Background()
	alice := storetest.CreateUser(t, db, "alice", "user")
	bob := storetest.CreateUser(t, db, "bob", "user")
	root := storetest.CreateUser(t, db, "root", "admin") // admin without 2FA → effective stage setup

	_, full := mustCreate(t, s, alice, principal.StageFull)
	u, err := s.Recheck(ctx, full.ID, alice)
	if err != nil || u.ID != alice || u.Username != "alice" {
		t.Fatalf("valid session: %+v, %v", u, err)
	}
	_, pre := mustCreate(t, s, alice, principal.StagePreAuth)
	_, adminSess := mustCreate(t, s, root, principal.StageFull)
	_, revoked := mustCreate(t, s, alice, principal.StageFull)
	if err := s.Revoke(ctx, revoked.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		sessID, user int64
	}{
		{"unknown session", 99999, alice},
		{"other user's session", full.ID, bob},
		{"pre-auth stage", pre.ID, alice},
		{"admin without 2FA (setup)", adminSess.ID, root},
		{"revoked", revoked.ID, alice},
	} {
		if _, err := s.Recheck(ctx, tc.sessID, tc.user); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s: err = %v, want ErrNoSession", tc.name, err)
		}
	}

	// idle timeout (30 min without a request)
	clk.Advance(31 * time.Minute)
	if _, err := s.Recheck(ctx, full.ID, alice); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle session: err = %v", err)
	}

	// absolute expiry (12 h) even though the session was active a moment ago
	clk.Set(sessionStart)
	_, busy := mustCreate(t, s, alice, principal.StageFull)
	touch := func(at time.Time) {
		t.Helper()
		clk.Set(at)
		if err := q.AuthTouchSession(ctx, store.AuthTouchSessionParams{LastSeenAt: at.UnixMilli(), ID: busy.ID}); err != nil {
			t.Fatal(err)
		}
	}
	touch(sessionStart.Add(12*time.Hour - time.Minute))
	if _, err := s.Recheck(ctx, busy.ID, alice); err != nil {
		t.Fatalf("active session before the absolute timeout: err = %v", err)
	}
	touch(sessionStart.Add(12 * time.Hour))
	if _, err := s.Recheck(ctx, busy.ID, alice); !errors.Is(err, ErrNoSession) {
		t.Fatalf("absolute timeout: err = %v", err)
	}

	// deleted user: ON DELETE CASCADE removes the sessions too (session-missing path) ...
	clk.Set(sessionStart)
	_, bobSess := mustCreate(t, s, bob, principal.StageFull)
	if _, err := db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recheck(ctx, bobSess.ID, bob); !errors.Is(err, ErrNoSession) {
		t.Fatalf("deleted user: err = %v", err)
	}
	// ... so reach the user-missing branch with a session row whose user is gone (foreign keys
	// off on one connection; PRAGMA foreign_keys is a no-op inside a transaction).
	carol := storetest.CreateUser(t, db, "carol", "user")
	_, carolSess := mustCreate(t, s, carol, principal.StageFull)
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []struct {
		sql  string
		args []any
	}{
		{`PRAGMA foreign_keys = OFF`, nil},
		{`DELETE FROM users WHERE id = ?`, []any{carol}},
		{`PRAGMA foreign_keys = ON`, nil},
	} {
		if _, err := conn.ExecContext(ctx, st.sql, st.args...); err != nil {
			t.Fatalf("%s: %v", st.sql, err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := q.AuthGetSessionByID(ctx, carolSess.ID); err != nil {
		t.Fatalf("session row must survive for this case: %v", err)
	}
	if _, err := s.Recheck(ctx, carolSess.ID, carol); !errors.Is(err, ErrNoSession) {
		t.Fatalf("session of a missing user: err = %v", err)
	}
}
