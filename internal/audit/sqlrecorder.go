package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"mcpanel/internal/clock"
	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
	"mcpanel/internal/store"
)

// SQLRecorder appends entries to audit_log and links them with a SHA-256 hash chain.
// db must come from store.Open (it sets _txlock=immediate, so BeginTx issues
// BEGIN IMMEDIATE). Use exactly one SQLRecorder per *sql.DB and process.
type SQLRecorder struct {
	db    *sql.DB
	clock clock.Clock
	mu    sync.Mutex
}

var _ Recorder = (*SQLRecorder)(nil)

func NewSQLRecorder(db *sql.DB, c clock.Clock) *SQLRecorder {
	return &SQLRecorder{db: db, clock: c}
}

// Record writes one entry. It must not be called while the caller holds an open write
// transaction on the same database (it would wait for busy_timeout and fail).
func (r *SQLRecorder) Record(ctx context.Context, e Entry) error {
	e, err := normalize(ctx, e)
	if err != nil {
		return err
	}
	details, err := CanonicalDetails(e.Details)
	if err != nil {
		return err
	}
	// The action already happened; the entry must be written even if the request is cancelled.
	ctx = context.WithoutCancel(ctx)

	r.mu.Lock()
	defer r.mu.Unlock()
	// BEGIN IMMEDIATE: reading the last hash and inserting the next row is serialised
	// across processes (panel + CLI); the mutex avoids lock contention inside this process.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("audit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := store.New(tx)

	last, err := q.AuditLast(ctx)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		last = store.AuditLastRow{}
	case err != nil:
		return fmt.Errorf("audit: read last entry: %w", err)
	}

	row := store.AuditLog{
		ID:        last.ID + 1,
		Ts:        r.clock.Now().UnixMilli(),
		ActorType: string(e.ActorType),
		ActorName: e.ActorName,
		Action:    e.Action,
		Target:    e.Target,
		Ip:        e.IP,
		Result:    string(e.Result),
		Details:   details,
		PrevHash:  last.Hash,
	}
	if e.ActorID != nil {
		row.ActorID = sql.NullInt64{Int64: *e.ActorID, Valid: true}
	}
	row.Hash = ComputeHash(row.PrevHash, row)

	if err := q.AuditInsert(ctx, store.AuditInsertParams{
		ID: row.ID, Ts: row.Ts, ActorType: row.ActorType, ActorID: row.ActorID, ActorName: row.ActorName,
		Action: row.Action, Target: row.Target, Ip: row.Ip, Result: row.Result, Details: row.Details,
		PrevHash: row.PrevHash, Hash: row.Hash,
	}); err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("audit: commit: %w", err)
	}
	return nil
}

// normalize validates e and fills actor and IP fields from the context.
func normalize(ctx context.Context, e Entry) (Entry, error) {
	if e.Action == "" {
		return e, errors.New("audit: action is required")
	}
	switch e.Result {
	case ResultSuccess, ResultFailure, ResultDenied:
	default:
		return e, fmt.Errorf("audit: invalid result %q", e.Result)
	}
	switch e.ActorType {
	case "":
		if p, ok := principal.From(ctx); ok {
			id := p.UserID
			e.ActorType, e.ActorID, e.ActorName = ActorUser, &id, p.Username
		} else {
			e.ActorType, e.ActorID, e.ActorName = ActorSystem, nil, string(ActorSystem)
		}
	case ActorUser:
		if e.ActorName == "" {
			return e, errors.New("audit: user actor requires a name")
		}
	case ActorSystem, ActorCLI:
		if e.ActorName == "" {
			e.ActorName = string(e.ActorType)
		}
	default:
		return e, fmt.Errorf("audit: invalid actor type %q", e.ActorType)
	}
	if e.IP == "" {
		if ip := httpx.ClientIP(ctx); ip.IsValid() {
			e.IP = ip.String()
		}
	}
	return e, nil
}
