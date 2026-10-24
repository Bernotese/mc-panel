package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrationMustChangePassword: 00012 leaves existing users without a forced password change
// (SEC-45) and its Down drops the column again.
func TestMigrationMustChangePassword(t *testing.T) {
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
	if _, err := p.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, webauthn_handle, password_changed_at, created_at, updated_at)
		VALUES (1, 'alice', '!', 'user', x'01', 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 12); err != nil {
		t.Fatal(err)
	}
	u, err := New(db).AuthGetUserByID(ctx, 1)
	if err != nil || u.MustChangePassword != 0 || u.Username != "alice" {
		t.Fatalf("user after up = %+v, %v", u, err)
	}
	if _, err := p.DownTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('users') WHERE name = 'must_change_password'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("column after down: n=%d err=%v", n, err)
	}
}
