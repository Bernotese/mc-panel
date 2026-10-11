package store

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrationPlayersInGame: 00004 removes ops and whitelist from the stored properties and
// bumps the revision of exactly the instances that had one of them (spec 3a §3.1).
func TestMigrationPlayersInGame(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		id    int64
		props string
	}{
		{1, `{"motd":"Hallo","whitelist":["Notch"],"whitelist_enabled":true}`},
		{2, `{"ops":["jeb_"],"max_players":8}`},
		{3, `{"motd":"keep","whitelist_enabled":false}`},
		{4, `{}`},
		{5, `{"whitelist":[],"ops":["Notch"]}`},
	}
	for _, r := range rows {
		if _, err := db.ExecContext(ctx, `INSERT INTO servers (id, name, created_at) VALUES (?, ?, 0)`, r.id, fmt.Sprintf("s%d", r.id)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx,
			`INSERT INTO instances (server_id, type, version, java, memory_mib, properties, desired_state, revision, created_at, updated_at)
			 VALUES (?, 'paper', '1.21.4', 'java21', 4096, ?, 'running', 5, 10, 10)`, r.id, r.props); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	want := map[int64]struct {
		props    string
		revision int64
		touched  bool
	}{
		1: {`{"motd":"Hallo","whitelist_enabled":true}`, 6, true},
		2: {`{"max_players":8}`, 6, true},
		3: {`{"motd":"keep","whitelist_enabled":false}`, 5, false},
		4: {`{}`, 5, false},
		5: {`{}`, 6, true},
	}
	for id, w := range want {
		var props string
		var revision, updatedAt int64
		if err := db.QueryRowContext(ctx, `SELECT properties, revision, updated_at FROM instances WHERE server_id = ?`, id).
			Scan(&props, &revision, &updatedAt); err != nil {
			t.Fatal(err)
		}
		if props != w.props || revision != w.revision || (updatedAt > 10) != w.touched {
			t.Errorf("instance %d: properties %s revision %d updated_at %d, want %s %d touched=%v",
				id, props, revision, updatedAt, w.props, w.revision, w.touched)
		}
	}
}
