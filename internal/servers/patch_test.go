package servers_test

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
)

type revisionResp struct {
	Instance *struct {
		Revision  int64 `json:"revision"`
		MemoryMiB int   `json:"memory_mib"`
	} `json:"instance"`
}

// A PATCH with expected_revision is only applied to the revision the client has seen; a
// stale one is refused as a whole and leaves no trace (issue #150).
func TestUpdateServerExpectedRevision(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, paper("survival"))
	path := fmt.Sprintf("/servers/%d", s.ID)

	got := decode[revisionResp](t, e.do(http.MethodGet, path, nil, &admin))
	if got.Instance == nil || got.Instance.Revision != 1 {
		t.Fatalf("GET revision = %+v", got.Instance)
	}
	rec := e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "expected_revision": 1}, &admin)
	if rec.Code != http.StatusOK || decode[revisionResp](t, rec).Instance.Revision != 2 {
		t.Fatalf("matching revision: %d %s", rec.Code, rec.Body)
	}
	// A no-op with the current revision passes and keeps it.
	rec = e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "expected_revision": 2}, &admin)
	if rec.Code != http.StatusOK || decode[revisionResp](t, rec).Instance.Revision != 2 {
		t.Fatalf("no-op with revision: %d %s", rec.Code, rec.Body)
	}

	before := len(e.rec.Entries)
	for name, body := range map[string]map[string]any{
		"spec":       {"version": "1.21.10", "expected_revision": 1},
		"web routes": {"web_routes": []agentapi.WebTarget{tg("", 8100)}, "expected_revision": 1},
		"no-op":      {"memory_mib": 6144, "expected_revision": 1},
		"future":     {"memory_mib": 2048, "expected_revision": 3},
	} {
		rec := e.do(http.MethodPatch, path, body, &admin)
		if rec.Code != http.StatusConflict || errCode(t, rec) != "server_modified" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	row := instanceRow(t, e.db, s.ID)
	if row.Revision != 2 || row.Version != "1.21.11" || row.MemoryMib != 6144 || len(storedRoutes(t, e, s.ID)) != 0 {
		t.Fatalf("stale patches wrote: %+v, routes %+v", row, storedRoutes(t, e, s.ID))
	}
	if len(e.rec.Entries) != before {
		t.Fatalf("stale patches audited: %v", e.rec.Actions()[before:])
	}
	// Without expected_revision the request is applied to whatever is stored.
	rec = e.do(http.MethodPatch, path, map[string]any{"version": "1.21.10"}, &admin)
	if rec.Code != http.StatusOK || instanceRow(t, e.db, s.ID).Version != "1.21.10" {
		t.Fatalf("without expected_revision: %d %s", rec.Code, rec.Body)
	}
	// A wrong type is a bad request, not a conflict.
	if rec := e.do(http.MethodPatch, path, map[string]any{"expected_revision": "3"}, &admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("string revision: %d %s", rec.Code, rec.Body)
	}
}

// Concurrent PATCHes of disjoint fields never revert each other: every request reads, merges
// and writes in one write transaction, and each audit entry names the value it replaced.
func TestUpdateServerConcurrentDisjointChanges(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	s := e.create(t, paper("survival"))
	path := fmt.Sprintf("/servers/%d", s.ID)
	bodies := []map[string]any{
		{"memory_mib": 6144},
		{"version": "1.21.10"},
		{"java": "java25"},
		{"properties": map[string]any{"motd": "Neu"}},
		{"web_routes": []agentapi.WebTarget{tg("", 8100)}},
	}
	const rounds = 10
	for round := range rounds {
		if round%2 == 1 { // back to the initial values, again all at once
			bodies = []map[string]any{
				{"memory_mib": 4096}, {"version": "1.21.11"}, {"java": "java21"}, {"properties": map[string]any{}},
				{"web_routes": []agentapi.WebTarget{}},
			}
		} else if round > 0 {
			bodies = []map[string]any{
				{"memory_mib": 6144}, {"version": "1.21.10"}, {"java": "java25"}, {"properties": map[string]any{"motd": "Neu"}},
				{"web_routes": []agentapi.WebTarget{tg("", 8100)}},
			}
		}
		var wg sync.WaitGroup
		codes := make([]int, len(bodies))
		start := make(chan struct{})
		for i, b := range bodies {
			wg.Go(func() {
				admin := e.admin()
				<-start
				codes[i] = e.do(http.MethodPatch, path, b, &admin).Code
			})
		}
		close(start)
		wg.Wait()
		for i, c := range codes {
			if c != http.StatusOK {
				t.Fatalf("round %d: patch %v: %d", round, bodies[i], c)
			}
		}
		row := instanceRow(t, e.db, s.ID)
		routes := storedRoutes(t, e, s.ID)
		if round%2 == 0 {
			if row.MemoryMib != 6144 || row.Version != "1.21.10" || row.Java != "java25" || row.Properties != `{"motd":"Neu"}` || len(routes) != 1 {
				t.Fatalf("round %d: a change got lost: %+v routes %+v", round, row, routes)
			}
		} else if row.MemoryMib != 4096 || row.Version != "1.21.11" || row.Java != "java21" || row.Properties != `{}` || len(routes) != 0 {
			t.Fatalf("round %d: a change got lost: %+v routes %+v", round, row, routes)
		}
		if want := int64(1 + 4*(round+1)); row.Revision != want {
			t.Fatalf("round %d: revision %d, want %d", round, row.Revision, want)
		}
	}
	// Each entry changed exactly one field, from the value the previous writer left.
	updates := 0
	for _, en := range e.rec.Entries {
		if en.Action != audit.ActionServerUpdate {
			continue
		}
		updates++
		changes, _ := en.Details["changes"].(map[string]any)
		if len(changes) != 1 {
			t.Fatalf("an update replaced more than its own field: %+v", en.Details)
		}
	}
	if updates != 4*rounds {
		t.Fatalf("%d update entries, want %d", updates, 4*rounds)
	}
}

// A request that changes the spec and the web routes is one transaction: when the routes
// cannot be written, the spec stays as it was and nothing is audited (issue #150).
func TestUpdateServerMixedFailureIsAtomic(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, paper("survival"))
	path := fmt.Sprintf("/servers/%d", s.ID)
	mustExec(t, e.db, `CREATE TRIGGER fail_web_routes BEFORE INSERT ON web_routes BEGIN SELECT RAISE(ABORT, 'injected'); END`)
	before := len(e.rec.Entries)

	rec := e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "web_routes": []agentapi.WebTarget{tg("", 8100)}}, &admin)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("mixed with failing routes: %d %s", rec.Code, rec.Body)
	}
	if row := instanceRow(t, e.db, s.ID); row.MemoryMib != 4096 || row.Revision != 1 {
		t.Fatalf("the spec part was committed: %+v", row)
	}
	if acts := e.rec.Actions()[before:]; len(acts) != 0 {
		t.Fatalf("failed request audited: %v", acts)
	}

	mustExec(t, e.db, `DROP TRIGGER fail_web_routes`)
	rec = e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "web_routes": []agentapi.WebTarget{tg("", 8100)}}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("mixed: %d %s", rec.Code, rec.Body)
	}
	if acts := e.rec.Actions()[before:]; !slices.Equal(acts, []string{audit.ActionServerUpdate, audit.ActionServerWebRoutesUpdate}) {
		t.Fatalf("mixed audit = %v", acts)
	}
}
