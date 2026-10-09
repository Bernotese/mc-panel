package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/audit"
	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
	"mcpanel/internal/rbac"
)

type noPerms struct{}

func (noPerms) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, nil
}

type apiEnv struct {
	*dispEnv
	audit *audit.MemoryRecorder
	rt    *rbac.Router
	mux   http.Handler
}

func newAPIEnv(t *testing.T, dev bool) *apiEnv {
	t.Helper()
	de := newDispEnv(t, DispatcherOptions{Backoff: []time.Duration{}})
	rec := &audit.MemoryRecorder{}
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, noPerms{}, de.clk)
	NewHandler(HandlerDeps{DB: de.db, Box: de.box, Dispatcher: de.d, Audit: rec, Clock: de.clk, Dev: dev}).Routes(rt)
	return &apiEnv{dispEnv: de, audit: rec, rt: rt, mux: mux}
}

func (e *apiEnv) call(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
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
	ctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("198.51.100.10"))
	ctx = principal.With(ctx, principal.Principal{UserID: 1, Username: "admin", Role: principal.RoleAdmin,
		Stage: principal.StageFull, SessionID: 1, SudoUntil: e.clk.Now().Add(5 * time.Minute)})
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func apiErr(rec *httptest.ResponseRecorder) httpx.ErrorBody {
	var b httpx.ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	return b
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, code int, errCode string) {
	t.Helper()
	if rec.Code != code || (errCode != "" && apiErr(rec).Error != errCode) {
		t.Fatalf("status %d %s, want %d %q", rec.Code, rec.Body, code, errCode)
	}
}

func discordBody(name string) map[string]any {
	return map[string]any{"kind": "discord", "name": name, "enabled": true, "url": testWebhook}
}

func TestNotifierRoutesDeclareRules(t *testing.T) {
	e := newAPIEnv(t, false)
	want := map[string]string{
		"GET /security/notifiers":            "admin[full]",
		"POST /security/notifiers":           "admin+sudo[full]",
		"PATCH /security/notifiers/{id}":     "admin+sudo[full]",
		"DELETE /security/notifiers/{id}":    "admin+sudo[full]",
		"POST /security/notifiers/{id}/test": "admin[full]",
	}
	got := map[string]string{}
	for _, ri := range e.rt.Routes() {
		got[ri.Method+" "+ri.Pattern] = ri.Rule.String()
	}
	if !maps.Equal(got, want) {
		t.Fatalf("routes = %v", got)
	}
}

func TestCreateAndListNotifiers(t *testing.T) {
	e := newAPIEnv(t, false)
	rec := e.call(t, http.MethodPost, "/security/notifiers", discordBody(" Ops "))
	expect(t, rec, http.StatusCreated, "")
	var n map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &n)
	if n["name"] != "Ops" || n["kind"] != "discord" || n["enabled"] != true ||
		n["target_masked"] != "https://discord.com/api/webhooks/1234…/****" || n["created_at"] != float64(dT0.UnixMilli()) {
		t.Fatalf("notifier = %v", n)
	}
	for _, k := range []string{"url", "topic", "token"} {
		if _, ok := n[k]; ok {
			t.Errorf("response must not contain %q", k)
		}
	}
	expect(t, e.call(t, http.MethodPost, "/security/notifiers", discordBody("Ops")), http.StatusConflict, httpx.CodeConflict)
	invalid := []any{
		map[string]any{"kind": "discord", "name": "x", "enabled": true, "url": "https://evil.example/api/webhooks/1/a"},
		map[string]any{"kind": "slack", "name": "x", "enabled": true, "url": testWebhook},
		map[string]any{"kind": "discord", "name": "", "enabled": true, "url": testWebhook},
		map[string]any{"kind": "discord", "name": strings.Repeat("n", 65), "enabled": true, "url": testWebhook},
		map[string]any{"kind": "discord", "name": "x", "url": testWebhook},
		map[string]any{"kind": "ntfy", "name": "x", "enabled": true, "url": "https://ntfy.sh"},
	}
	for i, body := range invalid {
		if rec := e.call(t, http.MethodPost, "/security/notifiers", body); rec.Code != http.StatusBadRequest || apiErr(rec).Error != httpx.CodeValidation {
			t.Errorf("case %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	var list []map[string]any
	_ = json.Unmarshal(e.call(t, http.MethodGet, "/security/notifiers", nil).Body.Bytes(), &list)
	if len(list) != 1 || list[0]["name"] != "Ops" {
		t.Fatalf("list = %v", list)
	}
	if !slices.Equal(e.audit.Actions(), []string{audit.ActionNotifierCreate}) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestNotifierAPINeverReturnsSecrets(t *testing.T) {
	e := newAPIEnv(t, false)
	var bodies []string
	rec := e.call(t, http.MethodPost, "/security/notifiers",
		map[string]any{"kind": "ntfy", "name": "phone", "enabled": true, "url": "https://ntfy.sh", "topic": "secrettopicname", "token": "tk_secret"})
	expect(t, rec, http.StatusCreated, "")
	bodies = append(bodies, rec.Body.String())
	bodies = append(bodies, e.call(t, http.MethodPost, "/security/notifiers", discordBody("discord")).Body.String())
	var created map[string]any
	_ = json.Unmarshal([]byte(bodies[0]), &created)
	id := int64(created["id"].(float64))
	rec = e.call(t, http.MethodPatch, fmt.Sprintf("/security/notifiers/%d", id), map[string]any{"name": "phone2"})
	expect(t, rec, http.StatusOK, "")
	bodies = append(bodies, rec.Body.String(), e.call(t, http.MethodGet, "/security/notifiers", nil).Body.String())
	for _, en := range e.audit.Entries {
		bodies = append(bodies, fmt.Sprintf("%s %v", en.Target, en.Details))
	}
	for _, b := range bodies {
		for _, secret := range []string{"SuperSecretToken", "secrettopicname", "tk_secret", "123456789012345678"} {
			if strings.Contains(b, secret) {
				t.Errorf("%q leaks %q", b, secret)
			}
		}
	}
	if created["target_masked"] != "https://ntfy.sh/sec**** (token)" {
		t.Fatalf("target_masked = %v", created["target_masked"])
	}
}

func TestUpdateNotifier(t *testing.T) {
	e := newAPIEnv(t, false)
	ctx := context.Background()
	rec := e.call(t, http.MethodPost, "/security/notifiers",
		map[string]any{"kind": "ntfy", "name": "phone", "enabled": true, "url": "https://ntfy.sh", "topic": "alerts", "token": "tk_secret"})
	expect(t, rec, http.StatusCreated, "")
	expect(t, e.call(t, http.MethodPost, "/security/notifiers", discordBody("other")), http.StatusCreated, "")
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := int64(created["id"].(float64))
	path := fmt.Sprintf("/security/notifiers/%d", id)

	rec = e.call(t, http.MethodPatch, path, map[string]any{"enabled": false})
	expect(t, rec, http.StatusOK, "")
	row, err := e.q.DefenseGetNotifier(ctx, id)
	if err != nil || row.Enabled != 0 {
		t.Fatalf("row = %+v, %v", row, err)
	}
	cfg, err := OpenConfig(e.box, KindNtfy, row.ConfigEnc)
	if err != nil || cfg.Token != "tk_secret" || cfg.Topic != "alerts" {
		t.Fatalf("config must be preserved: %+v, %v", cfg, err)
	}
	rec = e.call(t, http.MethodPatch, path, map[string]any{"token": ""})
	expect(t, rec, http.StatusOK, "")
	var upd map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &upd)
	if strings.Contains(upd["target_masked"].(string), "(token)") {
		t.Fatalf("token must be removed: %v", upd)
	}
	expect(t, e.call(t, http.MethodPatch, path, map[string]any{"url": "http://ntfy.sh"}), http.StatusBadRequest, httpx.CodeValidation)
	expect(t, e.call(t, http.MethodPatch, path, map[string]any{"name": "other"}), http.StatusBadRequest, httpx.CodeValidation)
	expect(t, e.call(t, http.MethodPatch, path, map[string]any{"name": ""}), http.StatusBadRequest, httpx.CodeValidation)
	expect(t, e.call(t, http.MethodPatch, "/security/notifiers/999", map[string]any{"enabled": true}), http.StatusNotFound, httpx.CodeNotFound)
	if n := strings.Count(strings.Join(e.audit.Actions(), ","), audit.ActionNotifierUpdate); n != 2 {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestDeleteNotifier(t *testing.T) {
	e := newAPIEnv(t, false)
	rec := e.call(t, http.MethodPost, "/security/notifiers", discordBody("ops"))
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	path := fmt.Sprintf("/security/notifiers/%d", int64(created["id"].(float64)))
	expect(t, e.call(t, http.MethodDelete, path, nil), http.StatusNoContent, "")
	expect(t, e.call(t, http.MethodDelete, path, nil), http.StatusNotFound, httpx.CodeNotFound)
	expect(t, e.call(t, http.MethodDelete, "/security/notifiers/abc", nil), http.StatusNotFound, httpx.CodeNotFound)
	if !slices.Contains(e.audit.Actions(), audit.ActionNotifierDelete) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestTestNotifierEndpoint(t *testing.T) {
	e := newAPIEnv(t, true) // dev: http and 127.0.0.1 targets allowed
	ok := newHookServer(t, http.StatusOK)
	rec := e.call(t, http.MethodPost, "/security/notifiers",
		map[string]any{"kind": "ntfy", "name": "ok", "enabled": false, "url": ok.srv.URL, "topic": "alerts"})
	expect(t, rec, http.StatusCreated, "")
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	expect(t, e.call(t, http.MethodPost, fmt.Sprintf("/security/notifiers/%d/test", int64(created["id"].(float64))), nil),
		http.StatusNoContent, "")
	if len(ok.requests()) != 1 {
		t.Fatalf("requests = %d", len(ok.requests()))
	}
	bad := newHookServer(t, http.StatusNotFound)
	rec = e.call(t, http.MethodPost, "/security/notifiers",
		map[string]any{"kind": "ntfy", "name": "bad", "enabled": true, "url": bad.srv.URL, "topic": "secrettopicname"})
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	rec = e.call(t, http.MethodPost, fmt.Sprintf("/security/notifiers/%d/test", int64(created["id"].(float64))), nil)
	expect(t, rec, http.StatusBadRequest, httpx.CodeValidation)
	if msg := apiErr(rec).Message; !strings.Contains(msg, "status 404") || strings.Contains(msg, "secrettopicname") || strings.Contains(msg, bad.srv.URL) {
		t.Fatalf("message = %q", msg)
	}
	expect(t, e.call(t, http.MethodPost, "/security/notifiers/999/test", nil), http.StatusNotFound, httpx.CodeNotFound)
}
