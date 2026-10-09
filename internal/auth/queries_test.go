package auth

import (
	"context"
	"database/sql"
	"testing"

	"mcpanel/internal/store"
	"mcpanel/internal/store/storetest"
)

func TestAuthQueries(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := context.Background()
	uid := storetest.CreateUser(t, db, "alice", "user")

	t.Run("username lookup is case-insensitive", func(t *testing.T) {
		u, err := q.AuthGetUserByUsername(ctx, "ALICE")
		if err != nil || u.ID != uid {
			t.Fatalf("got %+v, %v", u, err)
		}
	})

	t.Run("totp step only advances", func(t *testing.T) {
		if _, err := db.Exec(`UPDATE users SET totp_enabled = 1 WHERE id = ?`, uid); err != nil {
			t.Fatal(err)
		}
		n, err := q.AuthAdvanceTOTPStep(ctx, store.AuthAdvanceTOTPStepParams{Step: 100, ID: uid})
		if err != nil || n != 1 {
			t.Fatalf("first advance: n=%d err=%v", n, err)
		}
		for _, step := range []int64{100, 99} {
			if n, _ := q.AuthAdvanceTOTPStep(ctx, store.AuthAdvanceTOTPStepParams{Step: step, ID: uid}); n != 0 {
				t.Fatalf("step %d must not be accepted again", step)
			}
		}
		u, _ := q.AuthGetUserByID(ctx, uid)
		if u.TotpLastStep != 100 {
			t.Fatalf("TotpLastStep = %d", u.TotpLastStep)
		}
	})

	t.Run("recovery code is one-time", func(t *testing.T) {
		h := []byte("0123456789abcdef0123456789abcdef")
		if err := q.AuthInsertRecoveryCode(ctx, store.AuthInsertRecoveryCodeParams{UserID: uid, CodeHash: h, CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		if c, _ := q.AuthCountUnusedRecoveryCodes(ctx, uid); c != 1 {
			t.Fatalf("unused = %d", c)
		}
		use := store.AuthUseRecoveryCodeParams{UsedAt: sql.NullInt64{Int64: 2, Valid: true}, UserID: uid, CodeHash: h}
		if n, err := q.AuthUseRecoveryCode(ctx, use); err != nil || n != 1 {
			t.Fatalf("first use n=%d err=%v", n, err)
		}
		if n, _ := q.AuthUseRecoveryCode(ctx, use); n != 0 {
			t.Fatal("second use must fail")
		}
		if c, _ := q.AuthCountUnusedRecoveryCodes(ctx, uid); c != 0 {
			t.Fatalf("unused = %d", c)
		}
	})

	t.Run("webauthn state is taken once", func(t *testing.T) {
		s, err := q.AuthCreateSession(ctx, store.AuthCreateSessionParams{
			TokenHash: []byte("hash-1"), UserID: uid, Stage: "full", CsrfToken: "c", Ip: "203.0.113.7",
			UserAgent: "ua", Country: "", CreatedAt: 1000, LastSeenAt: 1000, ExpiresAt: 99999,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.AuthSetWebAuthnState(ctx, store.AuthSetWebAuthnStateParams{
			WebauthnState: sql.NullString{String: `{"kind":"login"}`, Valid: true}, ID: s.ID,
		}); err != nil {
			t.Fatal(err)
		}
		state, err := q.AuthGetWebAuthnState(ctx, s.ID)
		if err != nil || state.String != `{"kind":"login"}` {
			t.Fatalf("get = %v, %v", state, err)
		}
		clear := store.AuthClearWebAuthnStateParams{ID: s.ID, State: state}
		if n, err := q.AuthClearWebAuthnState(ctx, clear); err != nil || n != 1 {
			t.Fatalf("first clear n=%d err=%v", n, err)
		}
		if n, _ := q.AuthClearWebAuthnState(ctx, clear); n != 0 {
			t.Fatal("second clear of the same state must affect no row")
		}
		if got, _ := q.AuthGetWebAuthnState(ctx, s.ID); got.Valid {
			t.Fatalf("state must be NULL after clear, got %v", got)
		}
	})

	t.Run("active sessions exclude revoked, expired and idle", func(t *testing.T) {
		mk := func(hash string, lastSeen, expires int64) int64 {
			s, err := q.AuthCreateSession(ctx, store.AuthCreateSessionParams{
				TokenHash: []byte(hash), UserID: uid, Stage: "full", CsrfToken: "c", Ip: "203.0.113.7",
				UserAgent: "ua", CreatedAt: 0, LastSeenAt: lastSeen, ExpiresAt: expires,
			})
			if err != nil {
				t.Fatal(err)
			}
			return s.ID
		}
		active := mk("a", 5000, 10000)
		revoked := mk("r", 5000, 10000)
		mk("e", 5000, 4000) // expired
		mk("i", 100, 10000) // idle
		if _, err := q.AuthRevokeSession(ctx, store.AuthRevokeSessionParams{RevokedAt: sql.NullInt64{Int64: 1, Valid: true}, ID: revoked}); err != nil {
			t.Fatal(err)
		}
		rows, err := q.AuthListActiveSessionsForUser(ctx, store.AuthListActiveSessionsForUserParams{Now: 4500, IdleCutoff: 1000, UserID: uid})
		if err != nil {
			t.Fatal(err)
		}
		// The session from "webauthn state is taken once" has last_seen_at = 1000, which is
		// not > IdleCutoff (1000), so exactly one session ("a") is active.
		if len(rows) != 1 || rows[0].ID != active || rows[0].Username != "alice" {
			t.Fatalf("unexpected active sessions: %+v", rows)
		}
		all, err := q.AuthListAllActiveSessions(ctx, store.AuthListAllActiveSessionsParams{Now: 4500, IdleCutoff: 1000})
		if err != nil || len(all) != 1 || all[0].ID != active {
			t.Fatalf("all = %+v, err=%v", all, err)
		}
	})

}
