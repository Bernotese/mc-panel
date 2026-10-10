package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"mcpanel/internal/store"
	"mcpanel/internal/store/storetest"
)

func TestIsUniqueViolation(t *testing.T) {
	db := storetest.Open(t)
	ctx := context.Background()
	uid := storetest.CreateUser(t, db, "alice", "user")
	sid := storetest.CreateServer(t, db, "survival")

	_, unique := db.ExecContext(ctx, "INSERT INTO servers (name, created_at) VALUES ('survival', 0)")
	if !store.IsUniqueViolation(unique) {
		t.Fatalf("UNIQUE violation not detected: %v", unique)
	}
	if !store.IsUniqueViolation(fmt.Errorf("wrapped: %w", unique)) {
		t.Fatal("wrapped UNIQUE violation not detected")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, 'server.view')", uid, sid); err != nil {
		t.Fatal(err)
	}
	_, pk := db.ExecContext(ctx, "INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, 'server.view')", uid, sid)
	if !store.IsUniqueViolation(pk) {
		t.Fatalf("PRIMARY KEY violation not detected: %v", pk)
	}
	_, fk := db.ExecContext(ctx, "INSERT INTO permissions (user_id, server_id, permission) VALUES (999, ?, 'server.view')", sid)
	if fk == nil || store.IsUniqueViolation(fk) {
		t.Fatalf("FOREIGN KEY violation must not count as unique: %v", fk)
	}
	if store.IsUniqueViolation(nil) || store.IsUniqueViolation(errors.New("UNIQUE constraint failed: fake")) {
		t.Fatal("only real SQLite constraint errors count (no string matching)")
	}
}
