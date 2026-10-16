package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ErrChainBroken is returned by RunAuditVerify when the hash chain is invalid or disagrees with
// the journal anchors.
var ErrChainBroken = errors.New("audit: hash chain broken")

// maxListedIDs bounds the ids printed per category.
const maxListedIDs = 20

// RunAuditVerify implements `panel audit verify [--journal file|-]`. It prints the result to
// stdout and returns nil (exit 0) for a valid chain that agrees with the journal, an error
// wrapping ErrChainBroken (exit 1) otherwise. journal is nil without --journal.
func RunAuditVerify(ctx context.Context, db *sql.DB, stdout io.Writer, journal io.Reader) error {
	rep, err := Verify(ctx, db)
	if err != nil {
		return fmt.Errorf("audit verify: %w", err)
	}
	broken := !rep.OK
	if rep.OK {
		_, _ = fmt.Fprintf(stdout, "audit chain OK (%d entries)\n", rep.Checked)
	} else {
		_, _ = fmt.Fprintf(stdout, "audit chain BROKEN at entry id %d (%d entries verified before it)\n", rep.FirstBadID, rep.Checked)
	}
	if journal != nil {
		anchors, err := ParseJournal(journal)
		if err != nil {
			return fmt.Errorf("audit verify: %w", err)
		}
		jr, err := VerifyJournal(ctx, db, anchors)
		if err != nil {
			return fmt.Errorf("audit verify: %w", err)
		}
		printJournalReport(stdout, jr)
		broken = broken || !jr.OK()
	}
	if broken {
		if !rep.OK {
			return fmt.Errorf("%w at entry id %d", ErrChainBroken, rep.FirstBadID)
		}
		return fmt.Errorf("%w: database disagrees with the journal", ErrChainBroken)
	}
	return nil
}

func printJournalReport(w io.Writer, r JournalReport) {
	if r.Anchors == 0 {
		_, _ = fmt.Fprintln(w, "journal: no audit anchors found (pass the output of `journalctl -u mcpanel-panel -o cat`)")
		return
	}
	status := "OK"
	if !r.OK() {
		status = "MISMATCH"
	}
	_, _ = fmt.Fprintf(w, "journal %s: %d anchors, %d entries match, %d entries without anchor\n", status, r.Anchors, r.Matched, r.Unanchored)
	if len(r.Changed) > 0 {
		_, _ = fmt.Fprintf(w, "  changed entries (hash differs from the journal): %s\n", idList(r.Changed))
	}
	if len(r.Missing) > 0 {
		_, _ = fmt.Fprintf(w, "  missing entries (in the journal, not in the database): %s\n", idList(r.Missing))
	}
	if r.Truncated() {
		_, _ = fmt.Fprintf(w, "  newest entries removed: the journal knows entry %d, the database ends at %d\n", r.MaxAnchorID, r.MaxDBID)
	}
	if len(r.Restored) > 0 {
		_, _ = fmt.Fprintf(w, "  warning: entries match a later anchor of their id (the database was restored or rolled back; verify with `journalctl --since <restore time>`): %s\n", idList(r.Restored))
	}
}

func idList(ids []int64) string {
	parts := make([]string, 0, min(len(ids), maxListedIDs))
	for _, id := range ids[:min(len(ids), maxListedIDs)] {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	s := strings.Join(parts, ", ")
	if len(ids) > maxListedIDs {
		s += fmt.Sprintf(" and %d more", len(ids)-maxListedIDs)
	}
	return s
}
