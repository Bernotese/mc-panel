package audit_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// recordWithLog writes n entries and returns what SQLRecorder logged, as journald would keep it.
func recordWithLog(t *testing.T, n int, handler func(*bytes.Buffer) slog.Handler) (*bytes.Buffer, *audit.SQLRecorder) {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(handler(&buf)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	db := storetest.Open(t)
	rec := audit.NewSQLRecorder(db, clock.NewFake(t0))
	for i := range n {
		if err := rec.Record(userCtx(1, "root"), audit.Entry{
			Action: audit.ActionUserCreate, Target: fmt.Sprintf("user:%d", i+2), Result: audit.ResultSuccess,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return &buf, rec
}

func jsonHandler(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) }
func textHandler(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) }

func TestRecordLogsAnchors(t *testing.T) {
	for name, h := range map[string]func(*bytes.Buffer) slog.Handler{"json": jsonHandler, "text": textHandler} {
		t.Run(name, func(t *testing.T) {
			buf, rec := recordWithLog(t, 3, h)
			anchors, err := audit.ParseJournal(strings.NewReader("noise line\n" + buf.String()))
			if err != nil {
				t.Fatal(err)
			}
			if len(anchors.Hashes) != 3 || anchors.Hashes[3] != rec.LastWritten().Hash || rec.LastWritten().ID != 3 {
				t.Fatalf("anchors = %+v, last = %+v", anchors, rec.LastWritten())
			}
		})
	}
}

// journalFor builds journal lines (journalctl -o short prefix + JSON) for the current rows.
func journalFor(t *testing.T, rows [][2]any) string {
	t.Helper()
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "Oct 15 12:00:00 host panel[42]: {\"time\":\"x\",\"level\":\"INFO\",\"msg\":\"audit\",\"id\":%d,\"hash\":%q,\"action\":\"user.create\"}\n", r[0], r[1])
	}
	return b.String()
}

// rehash recomputes the whole chain, as an attacker with write access to the database would.
func rehash(t *testing.T, db *sql.DB) {
	t.Helper()
	prev := ""
	for _, row := range readAll(t, db) {
		row.PrevHash = prev
		row.Hash = audit.ComputeHash(prev, row)
		mustExec(t, db, "UPDATE audit_log SET prev_hash = ?, hash = ? WHERE id = ?", row.PrevHash, row.Hash, row.ID)
		prev = row.Hash
	}
}

// SEC-17: rewriting the database (with a recomputed chain) or deleting the newest entries is
// detected against the journal.
func TestVerifyJournal(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t)
	seed(t, db, 5)
	var lines [][2]any
	for _, row := range readAll(t, db) {
		lines = append(lines, [2]any{row.ID, row.Hash})
	}
	journal := journalFor(t, lines)
	check := func() (audit.JournalReport, string, error) {
		var out bytes.Buffer
		err := audit.RunAuditVerify(ctx, db, &out, strings.NewReader(journal))
		anchors, perr := audit.ParseJournal(strings.NewReader(journal))
		if perr != nil {
			t.Fatal(perr)
		}
		rep, verr := audit.VerifyJournal(ctx, db, anchors)
		if verr != nil {
			t.Fatal(verr)
		}
		return rep, out.String(), err
	}

	rep, out, err := check()
	if err != nil || !rep.OK() || rep.Matched != 5 || !strings.Contains(out, "journal OK: 5 anchors, 5 entries match") {
		t.Fatalf("intact: err=%v rep=%+v out=%q", err, rep, out)
	}

	// An attacker rewrites entry 3 and recomputes the chain: the chain verifies, the journal does not.
	mustExec(t, db, "DROP TRIGGER audit_log_no_update")
	mustExec(t, db, "DROP TRIGGER audit_log_no_delete")
	mustExec(t, db, "UPDATE audit_log SET target = 'tampered' WHERE id = 3")
	rehash(t, db)
	if r := verify(t, db); !r.OK {
		t.Fatalf("the recomputed chain must verify on its own: %+v", r)
	}
	rep, out, err = check()
	if !errors.Is(err, audit.ErrChainBroken) || !slices.Equal(rep.Changed, []int64{3, 4, 5}) || !strings.Contains(out, "changed entries") {
		t.Fatalf("rewritten: err=%v rep=%+v out=%q", err, rep, out)
	}

	// Deleting the newest entry is truncation.
	db2 := storetest.Open(t)
	seed(t, db2, 5)
	lines = nil
	for _, row := range readAll(t, db2) {
		lines = append(lines, [2]any{row.ID, row.Hash})
	}
	journal = journalFor(t, lines)
	mustExec(t, db2, "DROP TRIGGER audit_log_no_delete")
	mustExec(t, db2, "DELETE FROM audit_log WHERE id = 5")
	db = db2
	rep, out, err = check()
	if !errors.Is(err, audit.ErrChainBroken) || !slices.Equal(rep.Missing, []int64{5}) || !rep.Truncated() ||
		!strings.Contains(out, "newest entries removed: the journal knows entry 5, the database ends at 4") {
		t.Fatalf("truncated: err=%v rep=%+v out=%q", err, rep, out)
	}
}

func TestParseJournalKeepsFirstAnchor(t *testing.T) {
	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	j := `{"msg":"audit","id":1,"hash":"` + a + `"}
{"msg":"audit","id":1,"hash":"` + b + `"}
{"msg":"other","id":2,"hash":"` + a + `"}
{"msg":"audit","id":3,"hash":"not-a-hash"}
time=x level=INFO msg=audit id=4 hash=` + b + ` action=login
`
	anchors, err := audit.ParseJournal(strings.NewReader(j))
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors.Hashes) != 2 || anchors.Hashes[1] != a || anchors.Hashes[4] != b || !slices.Equal(anchors.Later[1], []string{b}) {
		t.Fatalf("anchors = %+v", anchors)
	}
}

// A database restored from a backup writes ids again: entries that match only a later anchor of
// their id are reported as restored (ErrRestored, exit 3), never as a clean result.
func TestVerifyJournalRestoredIsWarning(t *testing.T) {
	ctx := context.Background()
	db := storetest.Open(t)
	seed(t, db, 3)
	rows := readAll(t, db)
	other := strings.Repeat("c", 64)
	// the journal saw entry 3 first with another hash (written before the restore), then the current one
	var lines [][2]any
	lines = append(lines, [2]any{rows[0].ID, rows[0].Hash}, [2]any{rows[1].ID, rows[1].Hash}, [2]any{int64(3), other}, [2]any{rows[2].ID, rows[2].Hash})
	var out bytes.Buffer
	err := audit.RunAuditVerify(ctx, db, &out, strings.NewReader(journalFor(t, lines)))
	if !errors.Is(err, audit.ErrRestored) || errors.Is(err, audit.ErrChainBroken) {
		t.Fatalf("err = %v, want ErrRestored (%s)", err, out.String())
	}
	if !strings.Contains(out.String(), "matching only a later anchor of their id") || !strings.Contains(out.String(), "--since") ||
		!strings.Contains(out.String(), "journal MISMATCH") {
		t.Fatalf("output = %q", out.String())
	}
}
