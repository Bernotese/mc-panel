package store

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

// TestMigrationUserIDs: 00008 starts the user ID counter above every ID still referenced, also
// when the highest user was deleted before the upgrade (SEC-40).
func TestMigrationUserIDs(t *testing.T) {
	cases := map[string]struct {
		seed string
		want int64
	}{
		"empty":       {``, 0},
		"users only":  {`INSERT INTO users (id, username, password_hash, role, webauthn_handle, password_changed_at, created_at, updated_at) VALUES (3, 'a', '!', 'user', x'01', 0, 0, 0)`, 3},
		"login":       {`INSERT INTO login_attempts (ts, ip, ip_key, user_id, stage, success) VALUES (0, '203.0.113.7', '203.0.113.7/32', 9, 'password', 1)`, 9},
		"audit actor": {`INSERT INTO audit_log (id, ts, actor_type, actor_id, actor_name, action, result, prev_hash, hash) VALUES (1, 0, 'user', 12, 'gone', 'login', 'success', '', 'h1')`, 12},
		"audit cli":   {`INSERT INTO audit_log (id, ts, actor_type, actor_id, actor_name, action, result, prev_hash, hash) VALUES (1, 0, 'cli', 99, 'cli', 'user.create', 'success', '', 'h1')`, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
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
			if _, err := p.UpTo(ctx, 7); err != nil {
				t.Fatal(err)
			}
			if tc.seed != "" {
				if _, err := db.ExecContext(ctx, tc.seed); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.Up(ctx); err != nil {
				t.Fatal(err)
			}
			var last int64
			if err := db.QueryRowContext(ctx, `SELECT last_id FROM user_ids WHERE id = 1`).Scan(&last); err != nil {
				t.Fatal(err)
			}
			if last != tc.want {
				t.Fatalf("last_id = %d, want %d", last, tc.want)
			}
			next, err := New(db).RbacNextUserID(ctx)
			if err != nil || next != tc.want+1 {
				t.Fatalf("next user ID = %d, %v; want %d", next, err, tc.want+1)
			}
		})
	}
}
