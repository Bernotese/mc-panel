package auditapi_test

import (
	"context"
	"database/sql"
	"net/netip"
	"testing"
	"time"

	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
)

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// userCtx returns a request-like context with an authenticated principal and client IP 203.0.113.7.
func userCtx(id int64, name string) context.Context {
	ctx := principal.With(context.Background(), principal.Principal{
		UserID: id, Username: name, Role: principal.RoleAdmin, Stage: principal.StageFull,
	})
	return httpx.WithClientIP(ctx, netip.MustParseAddr("203.0.113.7"))
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}
