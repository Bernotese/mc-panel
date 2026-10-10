package users_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/password"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// A saturated argon2 limiter answers 429 rate_limited with Retry-After: 1 and changes nothing.
func TestCreateUserArgonBusyReturns429(t *testing.T) {
	e := newEnv(t)
	adm := e.admin()
	unblock := password.BlockForTesting(10 * time.Millisecond)
	rec := e.do(http.MethodPost, "/users", map[string]string{
		"username": "carol", "password": "a brand new passphrase", "role": "user",
	}, &adm)
	unblock()
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if n := countRows(t, e.db, `SELECT COUNT(*) FROM users WHERE username = 'carol'`); n != 0 {
		t.Fatalf("user created despite 429")
	}
}

func TestUpdatePasswordArgonBusyReturns429(t *testing.T) {
	e := newEnv(t)
	adm := e.admin()
	id := storetest.CreateUser(t, e.db, "dave", "user")
	unblock := password.BlockForTesting(10 * time.Millisecond)
	rec := e.do(http.MethodPatch, fmt.Sprintf("/users/%d", id), map[string]string{"password": "a brand new passphrase"}, &adm)
	unblock()
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}
