package users_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"mcpanel/internal/audit"
	"mcpanel/internal/store/storetest"
)

type serverResp struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

var allPermStrings = []string{"server.view", "server.power", "server.console", "server.files", "server.plugins", "server.backups"}

func TestServerCRUDAsAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", map[string]string{"name": "survival"}, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	s := decode[serverResp](t, rec)
	if s.ID == 0 || s.Name != "survival" || !slices.Equal(s.Permissions, allPermStrings) {
		t.Fatalf("created = %+v", s)
	}
	if rec := e.do(http.MethodPost, "/servers", map[string]string{"name": "survival"}, &admin); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d", rec.Code)
	}
	for _, bad := range []string{"-bad", "a", "Upper", "has_underscore", "with space", strings.Repeat("a", 33)} {
		rec := e.do(http.MethodPost, "/servers", map[string]string{"name": bad}, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("name %q: %d %s", bad, rec.Code, rec.Body)
		}
	}
	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &admin))
	if len(list) != 1 || list[0].ID != s.ID || !slices.Equal(list[0].Permissions, allPermStrings) {
		t.Fatalf("list = %+v", list)
	}
	path := fmt.Sprintf("/servers/%d", s.ID)
	if rec := e.do(http.MethodGet, path, nil, &admin); rec.Code != http.StatusOK || decode[serverResp](t, rec).Name != "survival" {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, path, nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: %d", rec.Code)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionServerCreate, audit.ActionServerDelete}) {
		t.Fatalf("audit = %v", got)
	}
	if en := e.rec.Entries[0]; en.Target != fmt.Sprintf("server:%d", s.ID) || en.Details["name"] != "survival" {
		t.Fatalf("audit entry = %+v", en)
	}
}

// Spec §12.4: a restricted user with server.power on server A sees only A; B → 403.
func TestServerVisibilityForRestrictedUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := storetest.CreateServer(t, e.db, "alpha")
	b := storetest.CreateServer(t, e.db, "beta")
	if rec := e.do(http.MethodPut, fmt.Sprintf("/users/%d/permissions", alice), grantsBody(grant(a, "server.power")), &admin); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}
	ap := userP(alice, "alice")

	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &ap))
	want := []string{"server.view", "server.power"}
	if len(list) != 1 || list[0].ID != a || list[0].Name != "alpha" || !slices.Equal(list[0].Permissions, want) {
		t.Fatalf("list = %+v", list)
	}
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", a), nil, &ap)
	if rec.Code != http.StatusOK || !slices.Equal(decode[serverResp](t, rec).Permissions, want) {
		t.Fatalf("get A: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, fmt.Sprintf("/servers/%d", b), nil},
		{http.MethodPost, fmt.Sprintf("/servers/%d/power", b), map[string]string{"action": "start"}},
		{http.MethodPost, "/servers", map[string]string{"name": "gamma"}},
		{http.MethodDelete, fmt.Sprintf("/servers/%d", a), nil},
	} {
		if rec := e.do(tc.method, tc.path, tc.body, &ap); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", tc.method, tc.path, rec.Code)
		}
	}

	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "start"}, &ap)
	if rec.Code != http.StatusAccepted || strings.TrimSpace(rec.Body.String()) != `{"status":"accepted"}` {
		t.Fatalf("power A: %d %s", rec.Code, rec.Body)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if last.Action != audit.ActionServerPower || last.Target != fmt.Sprintf("server:%d", a) ||
		last.Details["action"] != "start" || last.Result != audit.ResultSuccess {
		t.Fatalf("power audit = %+v", last)
	}
	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "explode"}, &ap)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("invalid action: %d %s", rec.Code, rec.Body)
	}
}

func TestServerListWithoutPermissionsIsEmptyArray(t *testing.T) {
	e := newEnv(t)
	storetest.CreateServer(t, e.db, "alpha")
	bob := userP(storetest.CreateUser(t, e.db, "bob", "user"), "bob")
	rec := e.do(http.MethodGet, "/servers", nil, &bob)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("list: %d %q", rec.Code, rec.Body)
	}
}

func TestServerPowerUnknownServerAsAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	if rec := e.do(http.MethodPost, "/servers/9999/power", map[string]string{"action": "stop"}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("power: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/servers/9999", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("get: %d", rec.Code)
	}
}

// If the row vanishes between the lookup and the delete (concurrent delete), the handler must
// answer 404 and not audit a deletion that did not happen. A BEFORE DELETE trigger with
// RAISE(IGNORE) makes the DELETE affect 0 rows deterministically.
func TestDeleteServerZeroRowsIs404(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := storetest.CreateServer(t, e.db, "alpha")
	mustExec(t, e.db, `CREATE TRIGGER servers_ignore_delete BEFORE DELETE ON servers BEGIN SELECT RAISE(IGNORE); END`)
	rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil, &admin)
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if got := e.rec.Actions(); slices.Contains(got, audit.ActionServerDelete) {
		t.Fatalf("unexpected audit entries: %v", got)
	}
}
