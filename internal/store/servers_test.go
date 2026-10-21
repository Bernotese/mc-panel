package store_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func createInstance(t *testing.T, q *store.Queries, serverID int64) store.Instance {
	t.Helper()
	in, err := q.ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: serverID, Type: "paper", Version: "1.21.4", Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: 10, UpdatedAt: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func TestInstanceLifecycle(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := context.Background()
	sid := storetest.CreateServer(t, db, "survival")

	in := createInstance(t, q, sid)
	if in.Revision != 1 || in.DesiredState != "running" || in.LoaderVersion.Valid || in.Modpack.Valid {
		t.Fatalf("created = %+v", in)
	}
	up, err := q.ServersUpdateInstance(ctx, store.ServersUpdateInstanceParams{
		Version: "1.21.5", Java: "java21", MemoryMib: 6144, Properties: `{"motd":"x"}`, UpdatedAt: 20, ServerID: sid,
	})
	if err != nil {
		t.Fatal(err)
	}
	if up.Revision != 2 || up.Version != "1.21.5" || up.MemoryMib != 6144 || up.UpdatedAt != 20 {
		t.Fatalf("updated = %+v", up)
	}
	n, err := q.ServersSetDesiredState(ctx, store.ServersSetDesiredStateParams{DesiredState: "stopped", UpdatedAt: 30, ServerID: sid})
	if err != nil || n != 1 {
		t.Fatalf("set desired: n=%d err=%v", n, err)
	}
	if err := q.ServersBumpAllRevisions(ctx, 40); err != nil {
		t.Fatal(err)
	}
	got, err := q.ServersGetInstance(ctx, sid)
	if err != nil || got.Revision != 3 || got.DesiredState != "stopped" {
		t.Fatalf("get = %+v, %v", got, err)
	}
	list, err := q.ServersListInstances(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "survival" || list[0].ServerID != sid {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM servers WHERE id = ?", sid); err != nil {
		t.Fatal(err)
	}
	if _, err := q.ServersGetInstance(ctx, sid); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("instance must be deleted with its server, got %v", err)
	}
}

func TestInstanceChecks(t *testing.T) {
	db := storetest.Open(t)
	ctx := context.Background()
	sid := storetest.CreateServer(t, db, "survival")
	bad := []string{
		`INSERT INTO instances VALUES (?, 'bukkit', 'x', NULL, NULL, 'java21', 4096, '{}', 'running', 1, 0, 0)`,
		`INSERT INTO instances VALUES (?, 'paper', 'x', NULL, NULL, 'java11', 4096, '{}', 'running', 1, 0, 0)`,
		`INSERT INTO instances VALUES (?, 'paper', 'x', NULL, NULL, 'java21', 512, '{}', 'running', 1, 0, 0)`,
		`INSERT INTO instances VALUES (?, 'paper', 'x', NULL, NULL, 'java21', 4096, '{}', 'paused', 1, 0, 0)`,
	}
	for _, stmt := range bad {
		if _, err := db.ExecContext(ctx, stmt, sid); err == nil {
			t.Errorf("CHECK must reject: %s", stmt)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO instances VALUES (999, 'paper', 'x', NULL, NULL, 'java21', 4096, '{}', 'running', 1, 0, 0)`); err == nil {
		t.Error("instance without server must be rejected")
	}
}

func TestInstanceSettingsRow(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := context.Background()
	s, err := q.ServersGetSettings(ctx)
	if err != nil || s.ID != 1 || s.BaseDomain.Valid || s.CurseforgeApiKey != nil {
		t.Fatalf("default settings = %+v, %v", s, err)
	}
	if err := q.ServersUpdateSettings(ctx, store.ServersUpdateSettingsParams{
		BaseDomain: sql.NullString{String: "mc.example.com", Valid: true}, CurseforgeApiKey: []byte{1, 2}, UpdatedAt: 5,
	}); err != nil {
		t.Fatal(err)
	}
	s, err = q.ServersGetSettings(ctx)
	if err != nil || s.BaseDomain.String != "mc.example.com" || len(s.CurseforgeApiKey) != 2 || s.UpdatedAt != 5 {
		t.Fatalf("settings = %+v, %v", s, err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO instance_settings (id, updated_at) VALUES (2, 0)"); err == nil {
		t.Fatal("only one settings row (id = 1) is allowed")
	}
}

func TestWebRoutes(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := context.Background()
	sid := storetest.CreateServer(t, db, "survival")
	other := storetest.CreateServer(t, db, "creative")
	in := createInstance(t, q, sid)
	createInstance(t, q, other)

	insert := func(server int64, path string, port int64) error {
		return q.ServersInsertWebRoute(ctx, store.ServersInsertWebRouteParams{ServerID: server, Path: path, Port: port})
	}
	for _, r := range []store.WebRoute{{ServerID: sid, Path: "afk-smoke", Port: 8200}, {ServerID: other, Path: "map", Port: 8100}, {ServerID: sid, Path: "", Port: 8100}} {
		if err := insert(r.ServerID, r.Path, r.Port); err != nil {
			t.Fatalf("insert %+v: %v", r, err)
		}
	}
	all, err := q.ServersListWebRoutes(ctx)
	want := []store.WebRoute{{ServerID: sid, Path: "", Port: 8100}, {ServerID: sid, Path: "afk-smoke", Port: 8200}, {ServerID: other, Path: "map", Port: 8100}}
	if err != nil || !slices.Equal(all, want) {
		t.Fatalf("all = %+v, %v; want %+v", all, err, want)
	}
	one, err := q.ServersListServerWebRoutes(ctx, sid)
	if err != nil || !slices.Equal(one, want[:2]) {
		t.Fatalf("server = %+v, %v", one, err)
	}
	if got, err := q.ServersGetInstance(ctx, sid); err != nil || got.Revision != in.Revision || got.UpdatedAt != in.UpdatedAt {
		t.Fatalf("routes changed the instance: %+v, %v", got, err)
	}

	// The CHECKs of the path and port rules (web port paths spec §2), unique paths and ports.
	for _, tc := range []struct {
		path string
		port int64
	}{
		{"Afk", 8300}, {"-a", 8300}, {"a-", 8300}, {"a/b", 8300}, {"a.b", 8300}, {"a_b", 8300},
		{strings.Repeat("a", 33), 8300}, {"x", 25565}, {"x", 25575}, {"x", 1023}, {"x", 65536},
		{"x", 8200}, {"afk-smoke", 8300},
	} {
		if err := insert(sid, tc.path, tc.port); err == nil {
			t.Errorf("route %q %d accepted", tc.path, tc.port)
		}
	}
	for _, tc := range []struct {
		path string
		port int64
	}{{"0", 1024}, {strings.Repeat("a", 32), 65535}, {"a-b", 25566}} {
		if err := insert(sid, tc.path, tc.port); err != nil {
			t.Errorf("route %q %d: %v", tc.path, tc.port, err)
		}
	}
	if err := insert(sid+100, "", 8100); err == nil {
		t.Error("route of an unknown server accepted")
	}

	if err := q.ServersDeleteWebRoutes(ctx, other); err != nil {
		t.Fatal(err)
	}
	if got, err := q.ServersListServerWebRoutes(ctx, other); err != nil || len(got) != 0 || got == nil {
		t.Fatalf("after delete = %#v, %v", got, err)
	}
	// Deleting the server removes its routes.
	if _, err := q.RbacDeleteServer(ctx, sid); err != nil {
		t.Fatal(err)
	}
	if got, err := q.ServersListWebRoutes(ctx); err != nil || len(got) != 0 {
		t.Fatalf("after server delete = %+v, %v", got, err)
	}
}
