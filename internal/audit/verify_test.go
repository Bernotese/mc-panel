package audit_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// seed records n entries through the real recorder (one second apart).
func seed(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	clk := clock.NewFake(t0)
	rec := audit.NewSQLRecorder(db, clk)
	for i := range n {
		if err := rec.Record(userCtx(1, "root"), audit.Entry{
			Action: audit.ActionUserCreate, Target: fmt.Sprintf("user:%d", i+2),
			Result: audit.ResultSuccess, Details: map[string]any{"i": i},
		}); err != nil {
			t.Fatal(err)
		}
		clk.Advance(time.Second)
	}
}

// insertChain writes n valid entries in one transaction (fast path for large chains).
func insertChain(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	q := store.New(tx)
	prev := ""
	for i := 1; i <= n; i++ {
		row := store.AuditLog{
			ID: int64(i), Ts: t0.UnixMilli() + int64(i), ActorType: "system", ActorName: "system",
			Action: audit.ActionJailAuto, Result: "success", Details: "{}", PrevHash: prev,
		}
		row.Hash = audit.ComputeHash(prev, row)
		if err := q.AuditInsert(ctx, store.AuditInsertParams(row)); err != nil {
			t.Fatal(err)
		}
		prev = row.Hash
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func verify(t *testing.T, db *sql.DB) audit.VerifyReport {
	t.Helper()
	rep, err := audit.Verify(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestVerifyEmptyLog(t *testing.T) {
	db := storetest.Open(t)
	if rep := verify(t, db); !rep.OK || rep.Checked != 0 || rep.FirstBadID != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyValidChain(t *testing.T) {
	db := storetest.Open(t)
	seed(t, db, 5)
	if rep := verify(t, db); !rep.OK || rep.Checked != 5 || rep.FirstBadID != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyLargeChainAcrossBatches(t *testing.T) {
	db := storetest.Open(t)
	insertChain(t, db, 2500)
	if rep := verify(t, db); !rep.OK || rep.Checked != 2500 {
		t.Fatalf("report = %+v", rep)
	}
}

// Spec §12.6: manual manipulation of a row must make verify fail at that row.
func TestVerifyDetectsModifiedRow(t *testing.T) {
	db := storetest.Open(t)
	seed(t, db, 3)
	mustExec(t, db, "DROP TRIGGER audit_log_no_update")
	mustExec(t, db, "UPDATE audit_log SET target = 'user:999' WHERE id = 2")
	if rep := verify(t, db); rep.OK || rep.Checked != 1 || rep.FirstBadID != 2 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyDetectsRecomputedRow(t *testing.T) {
	db := storetest.Open(t)
	seed(t, db, 3)
	row := readAll(t, db)[1]
	row.Action = audit.ActionUserDelete
	newHash := audit.ComputeHash(row.PrevHash, row) // attacker fixes the row's own hash ...
	mustExec(t, db, "DROP TRIGGER audit_log_no_update")
	mustExec(t, db, "UPDATE audit_log SET action = ?, hash = ? WHERE id = 2", row.Action, newHash)
	// ... but the successor still points to the old hash.
	if rep := verify(t, db); rep.OK || rep.Checked != 2 || rep.FirstBadID != 3 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestVerifyDetectsDeletedRow(t *testing.T) {
	db := storetest.Open(t)
	seed(t, db, 3)
	mustExec(t, db, "DROP TRIGGER audit_log_no_delete")
	mustExec(t, db, "DELETE FROM audit_log WHERE id = 2")
	if rep := verify(t, db); rep.OK || rep.Checked != 1 || rep.FirstBadID != 3 {
		t.Fatalf("report = %+v", rep)
	}
}

// A row whose stored text is not valid UTF-8 must not verify: encoding/json hashes invalid
// bytes like U+FFFD, so swapping U+FFFD for raw bytes would otherwise keep the hash valid.
func TestVerifyRejectsInvalidUTF8(t *testing.T) {
	db := storetest.Open(t)
	rec := audit.NewSQLRecorder(db, clock.NewFake(t0))
	for _, target := range []string{"a", "t\uFFFD", "c"} {
		if err := rec.Record(userCtx(1, "root"), audit.Entry{
			Action: audit.ActionUserCreate, Target: target, Result: audit.ResultSuccess,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if rep := verify(t, db); !rep.OK {
		t.Fatalf("baseline report = %+v", rep)
	}
	mustExec(t, db, "DROP TRIGGER audit_log_no_update")
	mustExec(t, db, "UPDATE audit_log SET target = CAST(x'74ff' AS TEXT) WHERE id = 2")
	if rep := verify(t, db); rep.OK || rep.Checked != 1 || rep.FirstBadID != 2 {
		t.Fatalf("report = %+v", rep)
	}
}
