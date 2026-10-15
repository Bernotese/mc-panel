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
// the journal. Conflicting lists ids that appear later with a different hash.
type JournalAnchors struct {
	Hashes      map[int64]string
	Conflicting []int64
}

// ParseJournal reads `journalctl -o cat` (or any prefix format) output of the panel and
// extracts the audit anchors, in the JSON (production) and text (dev) log formats.
func ParseJournal(r io.Reader) (JournalAnchors, error) {
	out := JournalAnchors{Hashes: make(map[int64]string)}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxJournalLine)
	for sc.Scan() {
		id, hash, ok := parseAnchorLine(sc.Text())
		if !ok {
			continue
		}
		if prev, seen := out.Hashes[id]; !seen {
			out.Hashes[id] = hash
		} else if prev != hash && !slices.Contains(out.Conflicting, id) {
			out.Conflicting = append(out.Conflicting, id)
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return out, fmt.Errorf("audit: read journal: %w", err)
	}
	slices.Sort(out.Conflicting)
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
	Changed     []int64 // entries whose hash differs from the journal
	Missing     []int64 // anchored ids without an entry (deleted; newest ones: truncated)
	Conflicting []int64 // ids logged twice with different hashes
	Unanchored  int     // entries not in the journal (written by the CLI, or the journal was rotated)
	MaxDBID     int64
	MaxAnchorID int64
}

// OK reports whether the database agrees with every anchor.
func (r JournalReport) OK() bool {
	return len(r.Changed) == 0 && len(r.Missing) == 0 && len(r.Conflicting) == 0
}

// Truncated reports whether the newest anchored entries are gone from the database.
func (r JournalReport) Truncated() bool { return r.MaxAnchorID > r.MaxDBID }

// VerifyJournal checks every audit entry against the journal anchors.
func VerifyJournal(ctx context.Context, db *sql.DB, j JournalAnchors) (JournalReport, error) {
	q := store.New(db)
	rep := JournalReport{Anchors: len(j.Hashes), Conflicting: j.Conflicting}
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
