package users

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/password"
	"mcpanel/internal/principal"
	"mcpanel/internal/store"
)

// RunAdminCreate implements `panel admin create -username <name>`. The password is the first
// line of stdin; prompting without echo is the caller's (cmd/panel) job. The new user gets the
// admin role and must set up 2FA at first login (enforced by stream A). db must be migrated.
func RunAdminCreate(ctx context.Context, db *sql.DB, c clock.Clock, rec audit.Recorder,
	args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	username := fs.String("username", "", "username of the new admin")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("admin create: %w", err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("admin create: unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *username == "" {
		return errors.New("admin create: -username is required")
	}
	pw, err := readPasswordLine(stdin)
	if err != nil {
		return fmt.Errorf("admin create: %w", err)
	}
	u, err := newUser(ctx, store.New(db), c.Now(), *username, pw, string(principal.RoleAdmin))
	if err != nil {
		return fmt.Errorf("admin create: %w", err)
	}
	if err := rec.Record(ctx, audit.Entry{
		Action: audit.ActionUserCreate, Target: userTarget(u.ID), Result: audit.ResultSuccess,
		ActorType: audit.ActorCLI, ActorName: string(audit.ActorCLI),
		Details: map[string]any{"username": u.Username, "role": u.Role},
	}); err != nil {
		return fmt.Errorf("admin create: user %q created, but writing the audit entry failed: %w", u.Username, err)
	}
	_, _ = fmt.Fprintf(stdout, "created admin user %q (id %d); two-factor setup is required at first login\n", u.Username, u.ID)
	return nil
}

// readPasswordLine returns the first line of r without the trailing line terminator.
// Spaces are significant and kept.
func readPasswordLine(r io.Reader) (string, error) {
	// A password has at most password.MaxLength runes (up to 4 bytes each) plus the line
	// terminator; never read more, so a missing newline cannot make this block or grow.
	line, err := bufio.NewReader(io.LimitReader(r, password.MaxLength*4+2)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("empty password on stdin")
	}
	return line, nil
}
