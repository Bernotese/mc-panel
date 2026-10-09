package auth

import (
	"context"
	"testing"
	"time"
)

// PruneSessions deletes sessions that were revoked or expired more than 30 days ago and keeps
// everything else (active sessions and recently ended ones stay visible for investigation).
func TestPruneSessions(t *testing.T) {
	e := newTestEnv(t)
	uid := e.createUser("alice", "user")
	now := e.clock.Now()
	ms := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }
	day := 24 * time.Hour
	insert := func(name string, expires int64, revoked any) {
		t.Helper()
		if _, err := e.db.Exec(`INSERT INTO sessions (token_hash, user_id, stage, csrf_token, ip, user_agent,
			created_at, last_seen_at, expires_at, revoked_at) VALUES (?, ?, 'full', 'x', '203.0.113.1', 'ua', ?, ?, ?, ?)`,
			[]byte(name), uid, ms(-40*day), ms(-40*day), expires, revoked); err != nil {
			t.Fatal(err)
		}
	}
	insert("active", ms(time.Hour), nil)
	insert("expired-recently", ms(-29*day), nil)
	insert("expired-long-ago", ms(-31*day), nil)
	insert("revoked-recently", ms(time.Hour), ms(-29*day))
	insert("revoked-long-ago", ms(time.Hour), ms(-31*day))

	n, err := e.h.PruneSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("pruned %d, want 2", n)
	}
	rows, err := e.db.Query(`SELECT CAST(token_hash AS TEXT) FROM sessions ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var left []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		left = append(left, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(left) != 3 || left[0] != "active" || left[1] != "expired-recently" || left[2] != "revoked-recently" {
		t.Fatalf("remaining = %v", left)
	}
}
