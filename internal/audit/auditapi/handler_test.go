package auditapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/audit"
	"mcpanel/internal/audit/auditapi"
	"mcpanel/internal/clock"
	"mcpanel/internal/httpx"
	"mcpanel/internal/notify"
	"mcpanel/internal/principal"
	"mcpanel/internal/rbac"
	"mcpanel/internal/store/storetest"
)

var rootAdmin = principal.Principal{UserID: 1, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull}

type auditEnv struct {
	db    *sql.DB
	notes *notify.Recording
	mux   http.Handler
}

// newAuditEnv seeds five entries, one minute apart starting at t0:
//
//	1 root  user.create        success  2 root  user.delete  success
//	3 system security.jail_auto success 4 alice server.power denied {"action":"start"}
//	5 cli   user.create        success
func newAuditEnv(t *testing.T) *auditEnv {
	t.Helper()
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	notes := &notify.Recording{}
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	auditapi.NewHandler(db, clk, notes).Routes(rt)

	rec := audit.NewSQLRecorder(db, clk)
	entries := []struct {
		ctx context.Context
		e   audit.Entry
	}{
		{userCtx(1, "root"), audit.Entry{Action: audit.ActionUserCreate, Target: "user:2", Result: audit.ResultSuccess, Details: map[string]any{"username": "alice"}}},
		{userCtx(1, "root"), audit.Entry{Action: audit.ActionUserDelete, Target: "user:3", Result: audit.ResultSuccess}},
		{context.Background(), audit.Entry{Action: audit.ActionJailAuto, Target: "198.51.100.1/32", Result: audit.ResultSuccess}},
		{userCtx(2, "alice"), audit.Entry{Action: audit.ActionServerPower, Target: "server:1", Result: audit.ResultDenied, Details: map[string]any{"action": "start"}}},
		{context.Background(), audit.Entry{Action: audit.ActionUserCreate, Target: "user:4", Result: audit.ResultSuccess, ActorType: audit.ActorCLI}},
	}
	for i, en := range entries {
		if i > 0 {
			clk.Advance(time.Minute)
		}
		if err := rec.Record(en.ctx, en.e); err != nil {
			t.Fatal(err)
		}
	}
	return &auditEnv{db: db, notes: notes, mux: mux}
}

func (e *auditEnv) get(path string, p principal.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(principal.With(req.Context(), p))
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

type entryResp struct {
	ID        int64          `json:"id"`
	Ts        int64          `json:"ts"`
	ActorType string         `json:"actor_type"`
	ActorID   *int64         `json:"actor_id"`
	ActorName string         `json:"actor_name"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	IP        string         `json:"ip"`
	Result    string         `json:"result"`
	Details   map[string]any `json:"details"`
	Hash      string         `json:"hash"`
}

type pageResp struct {
	Items      []entryResp `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

func decodePage(t *testing.T, rec *httptest.ResponseRecorder) pageResp {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	var p pageResp
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func pageIDs(p pageResp) []int64 {
	out := make([]int64, 0, len(p.Items))
	for _, it := range p.Items {
		out = append(out, it.ID)
	}
	return out
}

func TestListAuditFilters(t *testing.T) {
	e := newAuditEnv(t)
	ms := func(d time.Duration) int64 { return t0.Add(d).UnixMilli() }
	cases := []struct {
		query string
		want  []int64
	}{
		{"", []int64{5, 4, 3, 2, 1}},
		{"?actor=root", []int64{2, 1}},
		{"?actor=cli", []int64{5}},
		{"?action=user.", []int64{5, 2, 1}},
		{"?action=user.create", []int64{5, 1}},
		{"?action=user*", []int64{}}, // GLOB metacharacters are matched literally
		{"?result=denied", []int64{4}},
		{fmt.Sprintf("?from=%d&to=%d", ms(time.Minute), ms(3*time.Minute)), []int64{4, 3, 2}},
		{"?actor=alice&result=success", []int64{}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			p := decodePage(t, e.get("/audit"+tc.query, rootAdmin))
			if got := pageIDs(p); !slices.Equal(got, tc.want) {
				t.Fatalf("ids = %v, want %v", got, tc.want)
			}
			if p.NextCursor != nil {
				t.Fatalf("next_cursor = %q, want null", *p.NextCursor)
			}
		})
	}
}

func TestListAuditPagination(t *testing.T) {
	e := newAuditEnv(t)
	p := decodePage(t, e.get("/audit?limit=2", rootAdmin))
	if got := pageIDs(p); !slices.Equal(got, []int64{5, 4}) || p.NextCursor == nil || *p.NextCursor != "4" {
		t.Fatalf("page 1 = %v cursor %v", got, p.NextCursor)
	}
	p = decodePage(t, e.get("/audit?limit=2&cursor=4", rootAdmin))
	if got := pageIDs(p); !slices.Equal(got, []int64{3, 2}) || p.NextCursor == nil || *p.NextCursor != "2" {
		t.Fatalf("page 2 = %v cursor %v", got, p.NextCursor)
	}
	p = decodePage(t, e.get("/audit?limit=2&cursor=2", rootAdmin))
	if got := pageIDs(p); !slices.Equal(got, []int64{1}) || p.NextCursor != nil {
		t.Fatalf("page 3 = %v cursor %v", got, p.NextCursor)
	}
	rec := e.get("/audit?actor=nobody", rootAdmin)
	if !strings.Contains(rec.Body.String(), `"items":[]`) || !strings.Contains(rec.Body.String(), `"next_cursor":null`) {
		t.Fatalf("empty page body = %s", rec.Body)
	}
}

func TestListAuditInvalidQuery(t *testing.T) {
	e := newAuditEnv(t)
	for _, q := range []string{
		"?result=bogus", "?limit=0", "?limit=201", "?limit=abc", "?cursor=abc", "?cursor=0",
		"?from=yesterday", "?to=1.5", "?actor=" + strings.Repeat("a", 65), "?action=" + strings.Repeat("a", 65),
	} {
		rec := e.get("/audit"+q, rootAdmin)
		var body httpx.ErrorBody
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusBadRequest || body.Error != httpx.CodeValidation {
			t.Errorf("%s: code=%d error=%q", q, rec.Code, body.Error)
		}
	}
}

func TestListAuditEntryFields(t *testing.T) {
	e := newAuditEnv(t)
	p := decodePage(t, e.get("/audit", rootAdmin))
	byID := map[int64]entryResp{}
	for _, it := range p.Items {
		byID[it.ID] = it
	}
	denied := byID[4]
	if denied.ActorType != "user" || denied.ActorID == nil || *denied.ActorID != 2 || denied.ActorName != "alice" ||
		denied.IP != "203.0.113.7" || denied.Result != "denied" || denied.Details["action"] != "start" ||
		denied.Ts != t0.Add(3*time.Minute).UnixMilli() || len(denied.Hash) != 64 || denied.Target != "server:1" {
		t.Fatalf("entry 4 = %+v", denied)
	}
	sys := byID[3]
	if sys.ActorType != "system" || sys.ActorID != nil || sys.ActorName != "system" || sys.IP != "" || len(sys.Details) != 0 {
		t.Fatalf("entry 3 = %+v", sys)
	}
}

func TestVerifyEndpoint(t *testing.T) {
	e := newAuditEnv(t)
	type verifyResp struct {
		OK         bool   `json:"ok"`
		Checked    int    `json:"checked"`
		FirstBadID *int64 `json:"first_bad_id"`
	}
	rec := e.get("/audit/verify", rootAdmin)
	var v verifyResp
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("code=%d err=%v", rec.Code, err)
	}
	if !v.OK || v.Checked != 5 || v.FirstBadID != nil || !strings.Contains(rec.Body.String(), `"first_bad_id":null`) {
		t.Fatalf("valid chain = %s", rec.Body)
	}
	if n := len(e.notes.Types()); n != 0 {
		t.Fatalf("valid chain must not notify, got %d events", n)
	}

	mustExec(t, e.db, "DROP TRIGGER audit_log_no_update")
	mustExec(t, e.db, "UPDATE audit_log SET target = 'tampered' WHERE id = 3")
	rec = e.get("/audit/verify", rootAdmin)
	v = verifyResp{}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.OK || v.Checked != 2 || v.FirstBadID == nil || *v.FirstBadID != 3 {
		t.Fatalf("broken chain = %s", rec.Body)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventAuditChainBroken}) {
		t.Fatalf("notifications = %v", got)
	}
}

func TestAuditEndpointsRequireAdmin(t *testing.T) {
	e := newAuditEnv(t)
	user := principal.Principal{UserID: 2, Username: "alice", Role: principal.RoleUser, Stage: principal.StageFull}
	for _, path := range []string{"/audit", "/audit/verify"} {
		if rec := e.get(path, user); rec.Code != http.StatusForbidden {
			t.Errorf("%s as user: code = %d, want 403", path, rec.Code)
		}
	}
}
