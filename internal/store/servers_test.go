package store_test

import (
	"context"
	"database/sql"
	"errors"
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
