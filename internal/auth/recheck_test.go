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

	// deleted user
	clk.Set(sessionStart)
	_, bobSess := mustCreate(t, s, bob, principal.StageFull)
	if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recheck(ctx, bobSess.ID, bob); !errors.Is(err, ErrNoSession) {
		t.Fatalf("deleted user: err = %v", err)
	}
}
