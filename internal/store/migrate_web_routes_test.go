package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrationWebPortToRoutes: 00011 turns a stored web_port into exactly one root route,
// and its Down keeps only the root route's port (web port paths spec §3.1).
func TestMigrationWebPortToRoutes(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		id   int64
		port any
	}{{1, 8123}, {2, nil}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO servers (id, name, created_at) VALUES (?, ?, 0)`, r.id, fmt.Sprintf("s%d", r.id)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO instances (server_id, type, version, java, memory_mib, properties, desired_state, revision, created_at, updated_at, web_port)
			 VALUES (?, 'paper', '1.21.4', 'java21', 4096, '{}', 'running', 5, 10, 10, ?)`, r.id, r.port); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	routes, err := New(db).ServersListWebRoutes(ctx)
	if want := []WebRoute{{ServerID: 1, Path: "", Port: 8123}}; err != nil || !slices.Equal(routes, want) {
		t.Fatalf("routes = %+v, %v; want %+v", routes, err, want)
	}
	if in, err := New(db).ServersGetInstance(ctx, 1); err != nil || in.Revision != 5 || in.UpdatedAt != 10 {
		t.Fatalf("instance after up = %+v, %v", in, err)
	}

	// Down: an extra path route is dropped, the root port goes back into web_port.
	if err := New(db).ServersInsertWebRoute(ctx, ServersInsertWebRouteParams{ServerID: 1, Path: "afk-smoke", Port: 8200}); err != nil {
		t.Fatal(err)
	}
	if err := New(db).ServersInsertWebRoute(ctx, ServersInsertWebRouteParams{ServerID: 2, Path: "map", Port: 8100}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	want := map[int64]sql.NullInt64{1: {Int64: 8123, Valid: true}, 2: {}}
	for id, w := range want {
		var got sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT web_port FROM instances WHERE server_id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("instance %d: web_port %+v, want %+v", id, got, w)
		}
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'web_routes'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("web_routes after down: %d, %v", n, err)
	}
}
