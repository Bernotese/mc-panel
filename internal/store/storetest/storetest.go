// Package storetest provides a migrated temporary database and fixtures for tests.
package storetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store"
)

func Open(t testing.TB) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

// CreateUser inserts a user with an unusable password hash ("!") and returns its id.
func CreateUser(t testing.TB, db *sql.DB, username, role string) int64 {
	t.Helper()
	handle := make([]byte, 32)
	_, _ = rand.Read(handle)
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO users (username, password_hash, role, webauthn_handle, password_changed_at, created_at, updated_at)
		 VALUES (?, '!', ?, ?, 0, 0, 0)`, username, role, handle)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func CreateServer(t testing.TB, db *sql.DB, name string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `INSERT INTO servers (name, created_at) VALUES (?, 0)`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}
