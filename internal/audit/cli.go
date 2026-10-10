package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
)

// ErrChainBroken is returned by RunAuditVerify when the hash chain is invalid.
var ErrChainBroken = errors.New("audit: hash chain broken")

// RunAuditVerify implements `panel audit verify`. It prints a one-line result to stdout and
// returns nil (exit 0) for a valid chain, an error wrapping ErrChainBroken (exit 1) otherwise.
func RunAuditVerify(ctx context.Context, db *sql.DB, stdout io.Writer) error {
	rep, err := Verify(ctx, db)
	if err != nil {
		return fmt.Errorf("audit verify: %w", err)
	}
	if rep.OK {
		_, _ = fmt.Fprintf(stdout, "audit chain OK (%d entries)\n", rep.Checked)
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "audit chain BROKEN at entry id %d (%d entries verified before it)\n", rep.FirstBadID, rep.Checked)
	return fmt.Errorf("%w at entry id %d", ErrChainBroken, rep.FirstBadID)
}
