package defense

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
	"mcpanel/internal/rbac"
)

type denyChecker struct{}

func (denyChecker) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, nil
}

const callerIP = "198.51.100.10"

func (e *env) router() http.Handler {
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, denyChecker{}, e.clk)
	NewHandler(e.svc).Routes(rt)
	return mux
}

// call performs a request as admin "admin" with valid sudo from ip.
// body may be nil, a raw JSON string or a value that is marshalled.
func (e *env) call(t *testing.T, method, path string, body any, ip string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	ctx := httpx.WithClientIP(req.Context(), addr(ip))
	ctx = principal.With(ctx, principal.Principal{
		UserID: e.adminID, Username: "admin", Role: principal.RoleAdmin, Stage: principal.StageFull,
		SessionID: 1, SudoUntil: e.clk.Now().Add(5 * time.Minute),
	})
	rec := httptest.NewRecorder()
	e.router().ServeHTTP(rec, req.WithContext(ctx))
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

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, code int, errCodeWant string) {
	t.Helper()
	if rec.Code != code || (errCodeWant != "" && errCode(rec) != errCodeWant) {
		t.Fatalf("status = %d err = %q (body %s), want %d %q", rec.Code, errCode(rec), rec.Body, code, errCodeWant)
	}
}
