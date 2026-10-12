package backups_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)

var (
	backupsRule = rbac.Server(rbac.PermServerBackups, "id")
	snap        = strings.Repeat("ab", 32)
)

// wantRoutes are exactly the backups operations of api/openapi.yaml (V3).
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers/{id}/backups":                     backupsRule,
	"POST /api/servers/{id}/backups":                    backupsRule,
	"GET /api/servers/{id}/backups/job":                 backupsRule,
	"PUT /api/servers/{id}/backups/schedule":            backupsRule,
	"POST /api/servers/{id}/backups/{snapshot}/pin":     backupsRule,
	"POST /api/servers/{id}/backups/{snapshot}/restore": backupsRule.WithSudo(),
	"DELETE /api/servers/{id}/backups/{snapshot}":       backupsRule.WithSudo(),
	"GET /api/servers/{id}/backups/{snapshot}/download": backupsRule,
}

type env struct {
	db  *sql.DB
	mux *chi.Mux
	rt  *rbac.Router
	h   *backups.Handler
}

// newEnv mounts the handler under /api on a real rbac.Router; no files service, agent or
// notifier: the defaults must build.
func newEnv(t *testing.T) env {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	h := backups.NewHandler(backups.Deps{
		DB: db, Clock: clk, Audit: &audit.MemoryRecorder{},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) { h.Routes(api) })
	mux.Handle("/*", http.NotFoundHandler())
	return env{db: db, mux: mux, rt: rt, h: h}
}

func (e env) do(method, path string, p *principal.Principal) *httptest.ResponseRecorder {
	var body io.Reader
	if method == http.MethodPost || method == http.MethodPut {
		body = strings.NewReader(`{}`)
	}
	req := httptest.NewRequest(method, path, body)
	if p != nil {
		req = req.WithContext(principal.With(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func expand(pattern string) string {
	return strings.NewReplacer("{id}", "1", "{snapshot}", snap).Replace(pattern)
}

func TestRouteContract(t *testing.T) {
	e := newEnv(t)
	got := e.rt.Routes()
	if len(got) != len(wantRoutes) {
		t.Errorf("registered %d routes, want %d", len(got), len(wantRoutes))
	}
	seen := map[string]bool{}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		seen[key] = true
		w, ok := wantRoutes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	for key := range wantRoutes {
		if !seen[key] {
			t.Errorf("missing route %s", key)
		}
	}
	if un := rbac.Uncovered(e.mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}

// Phase A: every route answers 503 backups_unavailable until phase B2 implements it.
func TestStubsAnswerNotImplemented(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "root", "admin")
	admin := principal.Principal{
		UserID: uid, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1,
		SudoUntil: t0.Add(5 * time.Minute),
	}
	for key := range wantRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		path := expand(pattern)
		rec := e.do(method, path, &admin)
		var body httpx.ErrorBody
		if rec.Code != http.StatusServiceUnavailable || json.Unmarshal(rec.Body.Bytes(), &body) != nil ||
			body.Error != httpx.CodeBackupsUnavailable || body.Message != "backups not implemented yet" {
			t.Errorf("%s: %d %s", key, rec.Code, rec.Body)
		}
		if rec := e.do(method, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", key, rec.Code)
		}
	}
}

// A user without server.backups is refused; restore and delete need sudo even for admins.
func TestStubsCheckPermissionAndSudo(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "bob", "user")
	bob := principal.Principal{UserID: uid, Username: "bob", Role: principal.RoleUser, Stage: principal.StageFull, SessionID: 2}
	for key := range wantRoutes {
		method, pattern, _ := strings.Cut(key, " ")
		if rec := e.do(method, expand(pattern), &bob); rec.Code != http.StatusForbidden {
			t.Errorf("%s without server.backups: %d", key, rec.Code)
		}
	}
	aid := storetest.CreateUser(t, e.db, "root", "admin")
	noSudo := principal.Principal{UserID: aid, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 3}
	for _, key := range []string{"POST /api/servers/{id}/backups/{snapshot}/restore", "DELETE /api/servers/{id}/backups/{snapshot}"} {
		method, pattern, _ := strings.Cut(key, " ")
		rec := e.do(method, expand(pattern), &noSudo)
		var body httpx.ErrorBody
		if rec.Code != http.StatusForbidden || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error != httpx.CodeSudoRequired {
			t.Errorf("%s without sudo: %d %s", key, rec.Code, rec.Body)
		}
	}
}

func TestStubGateAndPurge(t *testing.T) {
	e := newEnv(t)
	if e.h.DataJobRunning(context.Background(), 1) {
		t.Fatal("the stub holds starts")
	}
	if err := e.h.PurgeBackups(context.Background(), 1); err != nil {
		t.Fatalf("PurgeBackups = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.h.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
