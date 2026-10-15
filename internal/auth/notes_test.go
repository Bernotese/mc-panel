package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/store"
)

func countNotes(e *testEnv, t notify.EventType) int {
	n := 0
	for _, typ := range e.notes.Types() {
		if typ == t {
			n++
		}
	}
	return n
}

// SEC-20: a wrong second factor after a correct password notifies once per user and IP key per
// hour; wrong sudo codes do not (the session already passed the second factor).
func TestNotifySecondFactorFailed(t *testing.T) {
	e := newTestEnv(t)
	c, _, secret := e.preauthClient("alice", "user", "203.0.113.10")
	bad := wrongCode(e.code(secret))
	for range 3 {
		c.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": bad})
	}
	if n := countNotes(e, notify.Event2FAFailed); n != 1 {
		t.Fatalf("auth.2fa_failed = %d, want 1 (deduplicated)", n)
	}
	sameNet := e.newClient("203.0.113.10") // another session from the same address
	sameNet.login("alice", testPassword)
	sameNet.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": bad})
	if n := countNotes(e, notify.Event2FAFailed); n != 1 {
		t.Fatalf("same IP key: auth.2fa_failed = %d, want 1", n)
	}
	other := e.newClient("198.51.100.20")
	other.login("alice", testPassword)
	other.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": bad})
	if n := countNotes(e, notify.Event2FAFailed); n != 2 {
		t.Fatalf("other IP: auth.2fa_failed = %d, want 2", n)
	}
	e.clock.Advance(time.Hour)
	again := e.newClient("203.0.113.10")
	again.login("alice", testPassword)
	again.do(http.MethodPost, "/api/auth/2fa/totp", map[string]string{"code": bad})
	if n := countNotes(e, notify.Event2FAFailed); n != 3 {
		t.Fatalf("after an hour: auth.2fa_failed = %d, want 3", n)
	}
	if ev := e.notes.Events[len(e.notes.Events)-1]; ev.Username != "alice" || ev.IP != "203.0.113.10" {
		t.Fatalf("event = %+v", ev)
	}

	e2 := newTestEnv(t)
	full, _, _ := e2.fullClient("bob", "user", "203.0.113.10")
	full.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": "000000"})
	if n := countNotes(e2, notify.Event2FAFailed); n != 0 {
		t.Fatalf("wrong sudo code: auth.2fa_failed = %d, want 0", n)
	}
}

func TestNotifySecondFactorLockout(t *testing.T) {
	e := newTestEnv(t)
	c, _, _ := e.fullClient("alice", "user", "203.0.113.10")
	for range 12 {
		c.do(http.MethodPost, "/api/auth/sudo/totp", map[string]string{"code": "000000"})
	}
	if n := countNotes(e, notify.Event2FALockout); n != 1 {
		t.Fatalf("auth.2fa_lockout = %d, want 1", n)
	}
}

func TestNotifyRecoveryCodeUsed(t *testing.T) {
	e := newTestEnv(t)
	c, uid, _ := e.preauthClient("alice", "user", "203.0.113.10")
	if err := e.q.AuthInsertRecoveryCode(context.Background(), store.AuthInsertRecoveryCodeParams{
		UserID: uid, CodeHash: e.h.hashRecoveryCode("abcde-fghjk"), CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if res := c.do(http.MethodPost, "/api/auth/2fa/recovery", map[string]string{"code": "abcde-fghjk"}); res.code != 200 {
		t.Fatalf("recovery login = %d %s", res.code, res.body)
	}
	if n := countNotes(e, notify.EventRecoveryCodeUsed); n != 1 {
		t.Fatalf("auth.recovery_code_used = %d, want 1", n)
	}
}

func TestNotifyPasswordChanged(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", "user")
	c := e.newClient("203.0.113.10")
	c.login("alice", testPassword)
	if res := c.do(http.MethodPost, "/api/me/password", map[string]string{
		"current_password": testPassword, "new_password": "a brand new passphrase",
	}); res.code != http.StatusNoContent {
		t.Fatalf("change = %d %s", res.code, res.body)
	}
	if n := countNotes(e, notify.EventPasswordChanged); n != 1 {
		t.Fatalf("auth.password_changed = %d, want 1", n)
	}
}

func TestNoteDedupIsBounded(t *testing.T) {
	d := newNoteDedup(time.Hour)
	now := testNow
	for i := range maxNoteDedupEntries {
		if !d.allow(string(rune(i+1)), now) {
			t.Fatalf("entry %d refused", i)
		}
	}
	if d.allow("one more", now) {
		t.Fatal("a full table of fresh entries must suppress new keys")
	}
	if !d.allow("one more", now.Add(time.Hour)) {
		t.Fatal("expired entries must make room again")
	}
}
