package users_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/httpx"
	"mcpanel/internal/notify"
	"mcpanel/internal/principal"
	"mcpanel/internal/rbac"
	"mcpanel/internal/store/storetest"
	"mcpanel/internal/users"
)

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// goodPassword passes password.Validate for every username used in these tests.
const goodPassword = "correct horse battery"

// env is a real database plus the users handler mounted on a real rbac.Router
// (with the DB-backed checker), so every request also exercises the route rule.
type env struct {
	t      *testing.T
	db     *sql.DB
	clk    *clock.Fake
	rec    *audit.MemoryRecorder
	notes  *notify.Recording
	mux    http.Handler
	rootID int64
}

func newEnv(t *testing.T) *env { return newEnvWithRecorder(t, nil) }

// newEnvWithRecorder uses mk to build the audit recorder; nil means e.rec (MemoryRecorder).
func newEnvWithRecorder(t *testing.T, mk func(*sql.DB, clock.Clock) audit.Recorder) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{}, notes: &notify.Recording{}}
	var rec audit.Recorder = e.rec
	if mk != nil {
		rec = mk(db, e.clk)
	}
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk)
	users.NewHandler(db, e.clk, rec, e.notes).Routes(rt)
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

// admin returns the principal of the seeded admin "root" with a valid sudo window.
func (e *env) admin() principal.Principal {
	return principal.Principal{
		UserID: e.rootID, Username: "root", Role: principal.RoleAdmin,
		Stage: principal.StageFull, SudoUntil: t0.Add(5 * time.Minute),
	}
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull}
}

// do sends a request with client IP 203.0.113.7 and the given principal (nil = anonymous).
func (e *env) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	ctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if p != nil {
		ctx = principal.With(ctx, *p)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[httpx.ErrorBody](t, rec).Error
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

type userResp struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	TotpEnabled  bool   `json:"totp_enabled"`
	PasskeyCount int    `json:"passkey_count"`
	CreatedAt    int64  `json:"created_at"`
	LastLoginAt  *int64 `json:"last_login_at"`
}
