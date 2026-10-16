package audit

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/store"
)

// AnchorLogMessage is the log message under which SQLRecorder logs every committed entry.
const AnchorLogMessage = "audit"

var (
	hashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// text handler (dev): ... msg=audit id=12 hash=… action=…
	textAnchorRE = regexp.MustCompile(`(?:^|\s)msg=audit\s+id=(\d+)\s+hash=([0-9a-f]{64})(?:\s|$)`)
)

// maxJournalLine bounds one log line; longer lines (not anchors) are skipped.
const maxJournalLine = 1 << 20

// JournalAnchors are the (id, hash) pairs read from the panel's log. The first anchor of an
// id counts: a compromised panel can log more lines, but it cannot remove earlier ones from
// the journal. Later holds further hashes logged for an id afterwards, which happens when the
// database was restored from a backup or rolled back and the ids were written again.
type JournalAnchors struct {
	Hashes map[int64]string
	Later  map[int64][]string
}

// ParseJournal reads `journalctl -o cat` (or any prefix format) output of the panel and
// extracts the audit anchors, in the JSON (production) and text (dev) log formats.
func ParseJournal(r io.Reader) (JournalAnchors, error) {
	out := JournalAnchors{Hashes: make(map[int64]string), Later: make(map[int64][]string)}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxJournalLine)
	for sc.Scan() {
		id, hash, ok := parseAnchorLine(sc.Text())
		if !ok {
			continue
		}
		if prev, seen := out.Hashes[id]; !seen {
			out.Hashes[id] = hash
		} else if prev != hash && !slices.Contains(out.Later[id], hash) {
			out.Later[id] = append(out.Later[id], hash)
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return out, fmt.Errorf("audit: read journal: %w", err)
	}
	return out, nil
}

func parseAnchorLine(line string) (int64, string, bool) {
	if i := strings.IndexByte(line, '{'); i >= 0 {
		var v struct {
			Msg  string `json:"msg"`
			ID   int64  `json:"id"`
			Hash string `json:"hash"`
		}
		if json.Unmarshal([]byte(line[i:]), &v) == nil && v.Msg == AnchorLogMessage && v.ID > 0 && hashRE.MatchString(v.Hash) {
			return v.ID, v.Hash, true
		}
	}
	if m := textAnchorRE.FindStringSubmatch(line); m != nil {
		if id, err := strconv.ParseInt(m[1], 10, 64); err == nil && id > 0 {
			return id, m[2], true
		}
	}
	return 0, "", false
}

// JournalReport compares the database with the journal anchors.
type JournalReport struct {
	Anchors     int     // ids found in the journal
	Matched     int     // anchors whose entry exists with the same hash
	Changed     []int64 // entries whose hash differs from every anchor of their id
	Missing     []int64 // anchored ids without an entry (deleted; newest ones: truncated)
	Restored    []int64 // entries that match a later anchor of their id (warning: restore or rollback)
	Unanchored  int     // entries not in the journal (written by the CLI, or the journal was rotated)
	MaxDBID     int64
	MaxAnchorID int64
}

// OK reports whether the database agrees with the anchors. Restored entries are warnings: after
// a restore or rollback, check only the journal written since then (journalctl --since).
func (r JournalReport) OK() bool {
	return len(r.Changed) == 0 && len(r.Missing) == 0
}

// Truncated reports whether the newest anchored entries are gone from the database.
func (r JournalReport) Truncated() bool { return r.MaxAnchorID > r.MaxDBID }

// VerifyJournal checks every audit entry against the journal anchors.
func VerifyJournal(ctx context.Context, db *sql.DB, j JournalAnchors) (JournalReport, error) {
	q := store.New(db)
	rep := JournalReport{Anchors: len(j.Hashes)}
	seen := make(map[int64]bool, len(j.Hashes))
	var lastID int64
	for {
		rows, err := q.AuditListAfter(ctx, store.AuditListAfterParams{ID: lastID, Limit: verifyBatch})
		if err != nil {
			return JournalReport{}, fmt.Errorf("audit: verify journal: %w", err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			lastID = row.ID
			want, ok := j.Hashes[row.ID]
			switch {
			case !ok:
				rep.Unanchored++
			case want == row.Hash:
				rep.Matched++
			case slices.Contains(j.Later[row.ID], row.Hash):
				rep.Restored = append(rep.Restored, row.ID)
			default:
				rep.Changed = append(rep.Changed, row.ID)
			}
			if ok {
				seen[row.ID] = true
			}
		}
	}
	rep.MaxDBID = lastID
	for id := range j.Hashes {
		rep.MaxAnchorID = max(rep.MaxAnchorID, id)
		if !seen[id] {
			rep.Missing = append(rep.Missing, id)
		}
	}
	slices.Sort(rep.Missing)
	return rep, nil
}
