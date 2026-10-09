package users_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/password"
	"mcpanel/internal/store/storetest"
	"mcpanel/internal/users"
)

func TestRunAdminCreate(t *testing.T) {
	db := storetest.Open(t)
	rec := &audit.MemoryRecorder{}
	var out bytes.Buffer
	err := users.RunAdminCreate(context.Background(), db, clock.NewFake(t0), rec,
		[]string{"-username", "jannes"}, strings.NewReader(goodPassword+"\r\n"), &out)
	if err != nil {
		t.Fatalf("RunAdminCreate: %v", err)
	}
	var id int64
	var role, hash string
	var handle []byte
	if err := db.QueryRow(`SELECT id, role, password_hash, webauthn_handle FROM users WHERE username = 'jannes'`).
		Scan(&id, &role, &hash, &handle); err != nil {
		t.Fatal(err)
	}
	if ok, _, err := password.Verify(goodPassword, hash); role != "admin" || !ok || err != nil || len(handle) != 32 {
		t.Fatalf("role=%q verify=%v err=%v handle=%d", role, ok, err, len(handle))
	}
	if !strings.Contains(out.String(), `created admin user "jannes"`) {
		t.Fatalf("stdout = %q", out.String())
	}
	if len(rec.Entries) != 1 {
		t.Fatalf("audit entries = %d", len(rec.Entries))
	}
	en := rec.Entries[0]
	if en.Action != audit.ActionUserCreate || en.ActorType != audit.ActorCLI || en.ActorName != "cli" ||
		en.Result != audit.ResultSuccess || en.Target != fmt.Sprintf("user:%d", id) || en.Details["role"] != "admin" {
		t.Fatalf("audit entry = %+v", en)
	}
}

func TestRunAdminCreateErrors(t *testing.T) {
	db := storetest.Open(t)
	storetest.CreateUser(t, db, "taken", "admin")
	cases := []struct {
		name   string
		args   []string
		stdin  string
		target error  // checked with errors.Is when non-nil
		substr string // checked in err.Error() when non-empty
	}{
		{"missing username", nil, goodPassword + "\n", nil, "-username"},
		{"unknown flag", []string{"-bogus"}, goodPassword + "\n", nil, "bogus"},
		{"extra argument", []string{"-username", "jannes", "extra"}, goodPassword + "\n", nil, "unexpected arguments"},
		{"empty stdin", []string{"-username", "jannes"}, "", nil, "empty password"},
		{"weak password", []string{"-username", "jannes"}, "short\n", password.ErrTooShort, ""},
		{"duplicate username", []string{"-username", "Taken"}, goodPassword + "\n", users.ErrUsernameTaken, ""},
		{"invalid username", []string{"-username", "x y"}, goodPassword + "\n", users.ErrInvalidUsername, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &audit.MemoryRecorder{}
			err := users.RunAdminCreate(context.Background(), db, clock.NewFake(t0), rec, tc.args, strings.NewReader(tc.stdin), &bytes.Buffer{})
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.target != nil && !errors.Is(err, tc.target) {
				t.Fatalf("err = %v, want %v", err, tc.target)
			}
			if tc.substr != "" && !strings.Contains(err.Error(), tc.substr) {
				t.Fatalf("err = %v, want substring %q", err, tc.substr)
			}
			if len(rec.Entries) != 0 {
				t.Fatalf("failed run must not audit, got %+v", rec.Entries)
			}
		})
	}
	if n := countRows(t, db, `SELECT count(*) FROM users WHERE username = 'jannes'`); n != 0 {
		t.Fatalf("user created despite errors")
	}
}

// countingReader serves 'a' bytes without ever sending a newline, up to total bytes.
type countingReader struct{ read, total int }

func (c *countingReader) Read(p []byte) (int, error) {
	if c.read >= c.total {
		return 0, io.EOF
	}
	n := min(len(p), c.total-c.read)
	for i := range n {
		p[i] = 'a'
	}
	c.read += n
	return n, nil
}

func TestRunAdminCreateBoundsStdin(t *testing.T) {
	db := storetest.Open(t)
	rec := &audit.MemoryRecorder{}
	in := &countingReader{total: 64 << 20}
	err := users.RunAdminCreate(context.Background(), db, clock.NewFake(t0), rec,
		[]string{"-username", "jannes"}, in, io.Discard)
	if !errors.Is(err, password.ErrTooLong) {
		t.Fatalf("err = %v, want password.ErrTooLong", err)
	}
	if limit := password.MaxLength*4 + 2; in.read > limit {
		t.Fatalf("read %d bytes from stdin, want at most %d", in.read, limit)
	}
	if n := countRows(t, db, `SELECT count(*) FROM users`); n != 0 || len(rec.Entries) != 0 {
		t.Fatalf("users=%d audit=%d, want none", n, len(rec.Entries))
	}
}
