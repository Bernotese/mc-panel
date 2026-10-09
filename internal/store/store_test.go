package store_test

import (
	"context"
	"testing"
	"time"

	"mcpanel/internal/store"
	"mcpanel/internal/store/storetest"
)

func TestMigrateIsIdempotentAndCreatesTables(t *testing.T) {
	db := storetest.Open(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	for _, table := range []string{"users", "webauthn_credentials", "recovery_codes", "sessions", "servers",
		"permissions", "audit_log", "login_attempts", "jails", "ip_rules", "settings", "notifiers"} {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d err=%v)", table, n, err)
		}
	}
}

func TestForeignKeysAndCaseInsensitiveUsernames(t *testing.T) {
	db := storetest.Open(t)
	uid := storetest.CreateUser(t, db, "Admin", "admin")
	sid := storetest.CreateServer(t, db, "survival")
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "INSERT INTO users (username, password_hash, role, webauthn_handle, password_changed_at, created_at, updated_at) VALUES ('admin','x','user',x'01',0,0,0)"); err == nil {
		t.Fatal("username uniqueness must be case-insensitive")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, 'server.power')", uid, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", uid); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM permissions").Scan(&n)
	if n != 0 {
		t.Fatalf("ON DELETE CASCADE not applied (foreign_keys off?), n=%d", n)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	db := storetest.Open(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO audit_log (ts, actor_type, actor_name, action, result, prev_hash, hash)
		VALUES (1, 'system', 'system', 'test', 'success', '', 'h1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE audit_log SET action = 'x'"); err == nil {
		t.Fatal("UPDATE on audit_log must fail")
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM audit_log"); err == nil {
		t.Fatal("DELETE on audit_log must fail")
	}
}

func TestSecuritySettingsRoundTrip(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := context.Background()
	s, err := store.LoadSecuritySettings(ctx, q)
	if err != nil || s.Enforce2FAForUsers || s.AdminAllowlistOnly || s.AllowedCountries == nil || len(s.AllowedCountries) != 0 {
		t.Fatalf("defaults = %+v, %v", s, err)
	}
	want := store.SecuritySettings{Enforce2FAForUsers: true, AllowedCountries: []string{"DE", "AT"}}
	if err := store.SaveSecuritySettings(ctx, q, want, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadSecuritySettings(ctx, q)
	if err != nil || !got.Enforce2FAForUsers || len(got.AllowedCountries) != 2 || got.AllowedCountries[1] != "AT" {
		t.Fatalf("got %+v, %v", got, err)
	}
}
