package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

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
