package users_test

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"mcpanel/internal/audit"
	"mcpanel/internal/notify"
	"mcpanel/internal/rbac"
	"mcpanel/internal/store/storetest"
)

type grantResp struct {
	ServerID    int64    `json:"server_id"`
	Permissions []string `json:"permissions"`
}

type permsResp struct {
	Grants []grantResp `json:"grants"`
}

func grantsBody(grants ...map[string]any) map[string]any {
	if grants == nil {
		grants = []map[string]any{}
	}
	return map[string]any{"grants": grants}
}

func grant(serverID int64, perms ...string) map[string]any {
	if perms == nil {
		perms = []string{}
	}
	return map[string]any{"server_id": serverID, "permissions": perms}
}

func TestSetAndGetPermissions(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := storetest.CreateServer(t, e.db, "alpha")
	b := storetest.CreateServer(t, e.db, "beta")
	path := fmt.Sprintf("/users/%d/permissions", alice)

	rec := e.do(http.MethodPut, path, grantsBody(grant(b, "server.view"), grant(a, "server.power")), &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
	}
	// server.power implies server.view; grants sorted by server id, permissions in rbac order.
	want := permsResp{Grants: []grantResp{
		{ServerID: a, Permissions: []string{"server.view", "server.power"}},
		{ServerID: b, Permissions: []string{"server.view"}},
	}}
	if got := decode[permsResp](t, rec); !reflect.DeepEqual(got, want) {
		t.Fatalf("PUT response = %+v, want %+v", got, want)
	}
	rec = e.do(http.MethodGet, path, nil, &admin)
	if got := decode[permsResp](t, rec); rec.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("GET = %d %+v", rec.Code, got)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionPermissionsSet}) || e.rec.Entries[0].Target != fmt.Sprintf("user:%d", alice) {
		t.Fatalf("audit = %+v", e.rec.Entries)
	}
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventPermissionsChanged}) {
		t.Fatalf("notifications = %v", got)
	}
	ok, err := rbac.NewDBChecker(e.db).HasServerPermission(context.Background(), alice, a, rbac.PermServerView)
	if err != nil || !ok {
		t.Fatalf("implied server.view not enforceable: %v %v", ok, err)
	}

	// PUT replaces the full set.
	rec = e.do(http.MethodPut, path, grantsBody(), &admin)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"grants":[]`) {
		t.Fatalf("PUT empty: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(http.MethodGet, path, nil, &admin)
	if !strings.Contains(rec.Body.String(), `"grants":[]`) {
		t.Fatalf("GET after clear: %s", rec.Body)
	}
}

func TestSetPermissionsMergesAndDropsEmpty(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := storetest.CreateServer(t, e.db, "alpha")
	b := storetest.CreateServer(t, e.db, "beta")
	rec := e.do(http.MethodPut, fmt.Sprintf("/users/%d/permissions", alice),
		grantsBody(grant(a, "server.console"), grant(a, "server.power"), grant(b)), &admin)
	want := permsResp{Grants: []grantResp{{ServerID: a, Permissions: []string{"server.view", "server.power", "server.console"}}}}
	if got := decode[permsResp](t, rec); rec.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %d %+v, want %+v", rec.Code, got, want)
	}
}

// Review Focus (stream): invalid input leaves the previous permission set untouched.
func TestSetPermissionsValidationIsAtomic(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := storetest.CreateServer(t, e.db, "alpha")
	path := fmt.Sprintf("/users/%d/permissions", alice)
	if rec := e.do(http.MethodPut, path, grantsBody(grant(a, "server.power")), &admin); rec.Code != http.StatusOK {
		t.Fatalf("initial PUT: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]map[string]any{
		"unknown server":     grantsBody(grant(a, "server.view"), grant(9999, "server.view")),
		"unknown permission": grantsBody(grant(a, "server.root")),
		"server id zero":     grantsBody(grant(0, "server.view")),
	} {
		rec := e.do(http.MethodPut, path, body, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	want := permsResp{Grants: []grantResp{{ServerID: a, Permissions: []string{"server.view", "server.power"}}}}
	if got := decode[permsResp](t, e.do(http.MethodGet, path, nil, &admin)); !reflect.DeepEqual(got, want) {
		t.Fatalf("permissions changed by rejected PUTs: %+v", got)
	}
	if n := len(e.rec.Entries); n != 1 {
		t.Fatalf("rejected PUTs must not be audited as success: %d entries", n)
	}
}

func TestPermissionsUnknownUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	if rec := e.do(http.MethodGet, "/users/9999/permissions", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("GET: %d", rec.Code)
	}
	if rec := e.do(http.MethodPut, "/users/9999/permissions", grantsBody(), &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("PUT: %d", rec.Code)
	}
}
