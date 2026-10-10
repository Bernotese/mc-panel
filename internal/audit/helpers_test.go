package audit_test

import (
	"context"
	"database/sql"
	"net/netip"
	"testing"
	"time"

	"mcpanel/internal/audit"
	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
	"mcpanel/internal/store"
)

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// userCtx returns a request-like context with an authenticated principal and client IP 203.0.113.7.
func userCtx(id int64, name string) context.Context {
	ctx := principal.With(context.Background(), principal.Principal{
		UserID: id, Username: name, Role: principal.RoleAdmin, Stage: principal.StageFull,
	})
	return httpx.WithClientIP(ctx, netip.MustParseAddr("203.0.113.7"))
}

// readAll returns every audit row in id order.
func readAll(t *testing.T, db *sql.DB) []store.AuditLog {
	t.Helper()
	rows, err := store.New(db).AuditListAfter(context.Background(), store.AuditListAfterParams{ID: 0, Limit: 100000})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// assertChain checks that ids are 1..n without gaps and every hash links to its predecessor.
func assertChain(t *testing.T, rows []store.AuditLog) {
	t.Helper()
	prev := ""
	for i, row := range rows {
		if row.ID != int64(i+1) {
			t.Fatalf("row %d has id %d, want %d", i, row.ID, i+1)
		}
		if row.PrevHash != prev {
			t.Fatalf("row %d prev_hash = %q, want %q", row.ID, row.PrevHash, prev)
		}
		if want := audit.ComputeHash(prev, row); row.Hash != want {
			t.Fatalf("row %d hash = %q, want %q", row.ID, row.Hash, want)
		}
		prev = row.Hash
	}
}
