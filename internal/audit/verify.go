package audit

import (
	"context"
	"database/sql"
	"fmt"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/store"
)

const verifyBatch = 1000

// VerifyReport is the result of checking the whole hash chain.
type VerifyReport struct {
	OK         bool
	Checked    int   // entries verified successfully (before FirstBadID, if any)
	FirstBadID int64 // 0 when OK
}

// Verify recomputes the chain from the first to the last entry. An entry is bad if its id
// is not the predecessor's id + 1, its prev_hash differs from the predecessor's hash, or its
// hash differs from ComputeHash. Removing the newest entries cannot be detected without an
// external anchor; that is out of scope for sub-project 1.
func Verify(ctx context.Context, db *sql.DB) (VerifyReport, error) {
	q := store.New(db)
	var (
		prevHash string
		lastID   int64
		checked  int
	)
	for {
		rows, err := q.AuditListAfter(ctx, store.AuditListAfterParams{ID: lastID, Limit: verifyBatch})
		if err != nil {
			return VerifyReport{}, fmt.Errorf("audit: verify: %w", err)
		}
		if len(rows) == 0 {
			return VerifyReport{OK: true, Checked: checked}, nil
		}
		for _, row := range rows {
			if row.ID != lastID+1 || !validRow(row) || row.PrevHash != prevHash || row.Hash != ComputeHash(prevHash, row) {
				return VerifyReport{Checked: checked, FirstBadID: row.ID}, nil
			}
			prevHash, lastID = row.Hash, row.ID
			checked++
		}
	}
}

// validRow reports whether every stored text field is valid UTF-8. The canonical form
// (encoding/json) hashes invalid bytes as U+FFFD, so such rows could be altered unnoticed.
func validRow(row store.AuditLog) bool {
	for _, s := range []string{row.ActorType, row.ActorName, row.Action, row.Target, row.Ip, row.Result, row.Details} {
		if !utf8.ValidString(s) {
			return false
		}
	}
	return true
}
