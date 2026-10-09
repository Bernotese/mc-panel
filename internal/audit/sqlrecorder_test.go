package audit_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/store"
	"mcpanel/internal/store/storetest"
)

func TestRecordFillsActorAndChains(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	rec := audit.NewSQLRecorder(db, clk)

	if err := rec.Record(userCtx(7, "jannes"), audit.Entry{
		Action: audit.ActionUserCreate, Target: "user:8", Result: audit.ResultSuccess,
		Details: map[string]any{"username": "alice"},
	}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	if err := rec.Record(context.Background(), audit.Entry{
		Action: audit.ActionJailAuto, Target: "203.0.113.9/32", Result: audit.ResultSuccess,
	}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Record(context.Background(), audit.Entry{
		Action: audit.ActionUserCreate, Target: "user:9", Result: audit.ResultSuccess, ActorType: audit.ActorCLI,
	}); err != nil {
		t.Fatal(err)
	}

	rows := readAll(t, db)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	assertChain(t, rows)
	u, s, c := rows[0], rows[1], rows[2]
	if u.Ts != t0.UnixMilli() || u.ActorType != "user" || u.ActorID != (sql.NullInt64{Int64: 7, Valid: true}) ||
		u.ActorName != "jannes" || u.Ip != "203.0.113.7" || u.Details != `{"username":"alice"}` ||
		u.PrevHash != "" || len(u.Hash) != 64 || u.Result != "success" || u.Target != "user:8" {
		t.Fatalf("user row = %+v", u)
	}
	if s.Ts != t0.Add(time.Second).UnixMilli() || s.ActorType != "system" || s.ActorID.Valid ||
		s.ActorName != "system" || s.Ip != "" || s.Details != "{}" {
		t.Fatalf("system row = %+v", s)
	}
	if c.ActorType != "cli" || c.ActorName != "cli" || c.ActorID.Valid {
		t.Fatalf("cli row = %+v", c)
	}
}

func TestRecordRejectsInvalidEntries(t *testing.T) {
	db := storetest.Open(t)
	rec := audit.NewSQLRecorder(db, clock.NewFake(t0))
	cases := map[string]audit.Entry{
		"missing action":      {Result: audit.ResultSuccess},
		"empty result":        {Action: "x.y"},
		"invalid result":      {Action: "x.y", Result: "ok"},
		"unknown actor type":  {Action: "x.y", Result: audit.ResultSuccess, ActorType: "robot"},
		"user without name":   {Action: "x.y", Result: audit.ResultSuccess, ActorType: audit.ActorUser},
		"unencodable details": {Action: "x.y", Result: audit.ResultSuccess, Details: map[string]any{"c": make(chan int)}},
	}
	for name, e := range cases {
		if err := rec.Record(context.Background(), e); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if n := len(readAll(t, db)); n != 0 {
		t.Fatalf("invalid entries were written: %d rows", n)
	}
}

func TestRecordSurvivesCancelledContext(t *testing.T) {
	db := storetest.Open(t)
	rec := audit.NewSQLRecorder(db, clock.NewFake(t0))
	ctx, cancel := context.WithCancel(userCtx(1, "root"))
	cancel() // client went away after the action was performed
	if err := rec.Record(ctx, audit.Entry{Action: audit.ActionUserDelete, Target: "user:2", Result: audit.ResultSuccess}); err != nil {
		t.Fatalf("Record with cancelled context: %v", err)
	}
	if n := len(readAll(t, db)); n != 1 {
		t.Fatalf("got %d rows, want 1", n)
	}
}

// Review Focus 5: 50 concurrent writers must produce a gap-free, valid chain.
func TestRecordConcurrent(t *testing.T) {
	db := storetest.Open(t)
	rec := audit.NewSQLRecorder(db, clock.NewFake(t0))
	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rec.Record(userCtx(int64(i+1), fmt.Sprintf("user%d", i)), audit.Entry{
				Action: audit.ActionServerPower, Target: fmt.Sprintf("server:%d", i), Result: audit.ResultSuccess,
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows := readAll(t, db)
	if len(rows) != n {
		t.Fatalf("got %d rows, want %d", len(rows), n)
	}
	assertChain(t, rows)
}

// Review Focus (stream): panel and CLI are separate processes with separate mutexes.
// Only BEGIN IMMEDIATE (store.Open sets _txlock=immediate) keeps the chain linear.
func TestRecordConcurrentAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	open := func() *sql.DB {
		db, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	panelDB := open()
	if err := store.Migrate(context.Background(), panelDB); err != nil {
		t.Fatal(err)
	}
	cliDB := open()
	recorders := []*audit.SQLRecorder{
		audit.NewSQLRecorder(panelDB, clock.NewFake(t0)),
		audit.NewSQLRecorder(cliDB, clock.NewFake(t0)),
	}
	const perRecorder = 25
	var wg sync.WaitGroup
	errs := make(chan error, len(recorders)*perRecorder)
	for ri, rec := range recorders {
		for i := range perRecorder {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- rec.Record(context.Background(), audit.Entry{
					Action: audit.ActionUserCreate, Target: fmt.Sprintf("r%d:%d", ri, i),
					Result: audit.ResultSuccess, ActorType: audit.ActorCLI,
				})
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows := readAll(t, panelDB)
	if len(rows) != len(recorders)*perRecorder {
		t.Fatalf("got %d rows, want %d", len(rows), len(recorders)*perRecorder)
	}
	assertChain(t, rows)
}
