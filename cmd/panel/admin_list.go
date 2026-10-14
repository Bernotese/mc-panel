package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// adminListCommand is `panel admin list`: it opens the database read-only (no data directory,
// no migration, no new database file) and prints the admins. Without a database there is no
// admin yet: no output, no error (installer spec §4.10).
func adminListCommand(ctx context.Context, cfg config.Panel, w io.Writer) error {
	path := cfg.DBPath()
	fi, err := os.Lstat(path) //nolint:gosec // G703: data_dir from the operator's panel.toml, like openDB
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("admin list: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("admin list: %s is not a regular file", path)
	}
	q := url.Values{}
	q.Set("mode", "ro")
	// A read-only connection still creates -wal/-shm next to a WAL database that has none
	// (the panel is stopped and checkpointed on close). Then nothing writes the database and
	// it is opened as immutable, which creates nothing; with a running panel the existing
	// -wal/-shm are used, so admins it just created are seen.
	if _, err := os.Lstat(path + "-wal"); errors.Is(err, fs.ErrNotExist) { //nolint:gosec // G703: see above
		q.Set("immutable", "1")
	}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "query_only(1)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode()) // driver registered by internal/store
	if err != nil {
		return fmt.Errorf("admin list: %w", err)
	}
	defer func() { _ = db.Close() }()
	return runAdminList(ctx, db, w)
}

// runAdminList prints the username of every admin, one per line, sorted (installer spec
// §4.10: install.sh asks for a first admin only when this prints nothing). Read-only.
func runAdminList(ctx context.Context, db *sql.DB, w io.Writer) error {
	users, err := store.New(db).RbacListUsers(ctx) // ORDER BY username
	if err != nil {
		return fmt.Errorf("admin list: %w", err)
	}
	for _, u := range users {
		if u.Role == string(principal.RoleAdmin) {
			if _, err := fmt.Fprintln(w, u.Username); err != nil {
				return err
			}
		}
	}
	return nil
}
