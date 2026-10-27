package devinst

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Last sync states (dev_instances.last_sync_status).
const (
	statusOK      = "ok"
	statusFailed  = "failed"
	statusRunning = "running"
)

// Fixed chat texts (plan, Global Constraints); the only variable part is the source's server
// name, which the server name rules restrict to lowercase letters, digits and hyphens.
const deferText = "say [mc-panel] Dev-Sync verschoben: es sind noch Spieler online (nächster Versuch in 15 Minuten)."

func warnText(sourceName string) string {
	return "say [mc-panel] Dev wird in 60 Sekunden mit " + sourceName + " synchronisiert, Änderungen gehen verloren."
}

// claim marks a sync of devID as pending; false if one already is.
func (h *Handler) claim(devID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.state(devID)
	if st.pending {
		return false
	}
	st.pending = true
	return true
}

func (h *Handler) release(devID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if st := h.devs[devID]; st != nil {
		st.pending = false
	}
}

func (h *Handler) isPending(devID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.devs[devID]
	return st != nil && st.pending
}

// players is the player count of the last pass while devID runs (0 when it does not run or
// the count is unknown).
func (h *Handler) players(devID int64) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.devs[devID]
	if st == nil || !st.running || st.players == nil {
		return 0
	}
	return *st.players
}

// queueSync claims and queues a sync of devID now.
func (h *Handler) queueSync(ctx context.Context, devID int64, trigger string) error {
	if !h.claim(devID) {
		return errSyncBusy
	}
	d, err := h.getDev(ctx, devID)
	if err != nil {
		h.release(devID)
		return err
	}
	return h.queueClaimed(ctx, d, trigger)
}

// queueClaimed queues the claimed sync of d in the backup queue. The status is "running" from
// here until Done; a refused job gets its previous status back and releases the claim.
func (h *Handler) queueClaimed(ctx context.Context, d dev, trigger string) error {
	id := d.row.ServerID
	now := h.clock.Now()
	h.writeMu.Lock()
	old, err := h.q.DevGet(ctx, id)
	if err == nil {
		err = h.q.DevSetSyncRunning(ctx, store.DevSetSyncRunningParams{UpdatedAt: now.UnixMilli(), ServerID: id})
	}
	if err == nil && old.DeferredSince.Valid { // the wait is over
		err = h.q.DevSetDeferred(ctx, store.DevSetDeferredParams{NextSyncAt: old.NextSyncAt, UpdatedAt: now.UnixMilli(), ServerID: id})
	}
	h.writeMu.Unlock()
	if err != nil {
		h.release(id)
		return err
	}
	err = h.syncer.QueueDevSync(ctx, backups.DevSync{
		DevID: id, SourceID: d.row.SourceID, DevName: d.name, SourceName: d.sourceName, Trigger: trigger,
		Apply: func(ctx context.Context) error { return h.servers.ApplyDevSpec(ctx, id) },
		Done:  func(snapshot string, err error) { h.syncDone(ctx, id, trigger, snapshot, err) },
	})
	if err != nil {
		h.writeMu.Lock()
		if rerr := h.q.DevSetSyncResult(ctx, store.DevSetSyncResultParams{
			LastSyncStatus: old.LastSyncStatus, LastSyncError: old.LastSyncError, LastSyncSnapshot: old.LastSyncSnapshot,
			LastSyncAt: old.LastSyncAt, UpdatedAt: now.UnixMilli(), ServerID: id,
		}); rerr != nil {
			h.log.ErrorContext(ctx, "devinst: restore the sync status", "server_id", id, "err", rerr)
		}
		h.writeMu.Unlock()
		h.release(id)
	}
	return err
}

// delay queues the claimed sync of d warnDelay from now (after a chat warning). The wait
// shows as deferred_since, like a scheduled sync that waits for players.
func (h *Handler) delay(ctx context.Context, d dev, trigger string) error {
	now := h.clock.Now()
	since := d.row.DeferredSince
	if !since.Valid {
		since = nullTime(now)
	}
	h.writeMu.Lock()
	err := h.q.DevSetDeferred(ctx, store.DevSetDeferredParams{
		NextSyncAt: d.row.NextSyncAt, DeferredSince: since, UpdatedAt: now.UnixMilli(), ServerID: d.row.ServerID,
	})
	h.writeMu.Unlock()
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.delayed = append(h.delayed, delayedSync{devID: d.row.ServerID, trigger: trigger, at: now.Add(warnDelay), ctx: ctx})
	h.mu.Unlock()
	return nil
}

// runDelayed queues the delayed syncs whose wait is over; one the queue refuses is recorded
// as a failed sync.
func (h *Handler) runDelayed(ctx context.Context) {
	now := h.clock.Now()
	h.mu.Lock()
	var due []delayedSync
	keep := h.delayed[:0]
	for _, s := range h.delayed {
		if now.Before(s.at) {
			keep = append(keep, s)
		} else {
			due = append(due, s)
		}
	}
	h.delayed = keep
	h.mu.Unlock()
	for _, s := range due {
		if ctx.Err() != nil {
			h.release(s.devID)
			return
		}
		d, err := h.getDev(s.ctx, s.devID)
		if errors.Is(err, sql.ErrNoRows) {
			h.release(s.devID) // deleted meanwhile
			continue
		}
		if err == nil {
			err = h.queueClaimed(s.ctx, d, s.trigger)
		}
		if err != nil {
			h.claim(s.devID) // syncDone releases it again
			h.syncDone(s.ctx, s.devID, s.trigger, "", err)
		}
	}
}

// nextSync is the next daily slot at syncTime (minutes) in the panel time zone after now.
func (h *Handler) nextSync(ctx context.Context, syncTime int64, now time.Time) time.Time {
	loc, _ := h.location(ctx)
	if t, ok := backups.NextRun(backups.Schedule{Mode: backups.ModeDaily, TimeOfDay: int(syncTime)}, loc, now, time.Time{}); ok {
		return t
	}
	return now.Add(24 * time.Hour) // unreachable for a valid sync time
}

// location is instance_settings.backup_timezone (UTC when unreadable or invalid) and its name.
func (h *Handler) location(ctx context.Context) (*time.Location, string) {
	name, err := h.q.BackupsGetTimezone(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "devinst: read the time zone", "err", err)
		return time.UTC, time.UTC.String()
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC, name
	}
	return loc, name
}

// syncDone records the end of a sync (backups.DevSync.Done): the result (a failure keeps the
// last successful sync and snapshot), the next slot, the audit entry and, on failure, the
// throttled dev.sync_failed notification. A deleted dev leaves nothing to record.
func (h *Handler) syncDone(ctx context.Context, devID int64, trigger, snapshot string, syncErr error) {
	defer h.release(devID)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), callTimeout)
	defer cancel()
	now := h.clock.Now()
	h.writeMu.Lock()
	row, err := h.q.DevGet(ctx, devID)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeMu.Unlock()
		h.log.InfoContext(ctx, "devinst: sync of a deleted dev ended", "server_id", devID, "err", syncErr)
		return
	}
	if err == nil {
		p := store.DevSetSyncResultParams{
			LastSyncStatus: sql.NullString{String: statusOK, Valid: true}, LastSyncSnapshot: sql.NullString{String: snapshot, Valid: snapshot != ""},
			LastSyncAt: nullTime(now), UpdatedAt: now.UnixMilli(), ServerID: devID,
		}
		if syncErr != nil {
			p.LastSyncStatus = sql.NullString{String: statusFailed, Valid: true}
			p.LastSyncError = sql.NullString{String: syncErr.Error(), Valid: true}
			p.LastSyncSnapshot, p.LastSyncAt = row.LastSyncSnapshot, row.LastSyncAt
		}
		err = h.q.DevSetSyncResult(ctx, p)
	}
	if err == nil {
		var next sql.NullInt64
		if row.SyncPaused == 0 {
			next = nullTime(h.nextSync(ctx, row.SyncTime, now))
		}
		err = h.q.DevSetNextSync(ctx, store.DevSetNextSyncParams{NextSyncAt: next, UpdatedAt: now.UnixMilli(), ServerID: devID})
	}
	h.writeMu.Unlock()
	if err != nil {
		h.log.ErrorContext(ctx, "devinst: record the sync result", "server_id", devID, "err", err)
	}
	details := map[string]any{"trigger": trigger, "source_id": row.SourceID}
	if snapshot != "" {
		details["snapshot"] = snapshot
	}
	if syncErr != nil {
		details["error"] = syncErr.Error()
	}
	h.record(ctx, audit.ActionDevSync, devID, result(syncErr), details)
	if syncErr == nil {
		h.mu.Lock()
		h.state(devID).lastAlert = time.Time{}
		h.mu.Unlock()
		return
	}
	h.alertFailed(ctx, devID, syncErr, now)
}

// alertFailed sends dev.sync_failed unless one went out for devID within alertEvery.
func (h *Handler) alertFailed(ctx context.Context, devID int64, syncErr error, now time.Time) {
	h.mu.Lock()
	st := h.state(devID)
	if !st.lastAlert.IsZero() && now.Sub(st.lastAlert) < alertEvery {
		h.mu.Unlock()
		return
	}
	st.lastAlert = now
	h.mu.Unlock()
	name := "server " + strconv.FormatInt(devID, 10)
	if d, err := h.getDev(ctx, devID); err == nil {
		name = d.name
	}
	p, _ := principal.From(ctx) // a manual sync keeps the user who started it
	h.notifier.Notify(context.WithoutCancel(ctx), notify.Event{
		Type: notify.EventDevSyncFailed, Title: "Dev sync failed: " + name, Message: syncErr.Error(), Username: p.Username, Time: now,
	})
}

// tick runs once per tickEvery (and once when Run starts): a changed time zone, then the due
// syncs. With players on the dev a scheduled sync waits deferStep at a time, at most deferMax
// after its slot; then it runs after a final warning.
func (h *Handler) tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	h.tickMu.Lock()
	defer h.tickMu.Unlock()
	now := h.clock.Now()
	h.checkTimezone(ctx, now)
	rows, err := h.q.DevListDue(ctx, nullTime(now))
	if err != nil {
		h.log.ErrorContext(ctx, "devinst: list due syncs", "err", err)
		return
	}
	jobCtx := context.WithoutCancel(ctx)
	for _, row := range rows {
		id := row.ServerID
		if h.isPending(id) {
			continue
		}
		if h.players(id) == 0 {
			if err := h.queueSync(jobCtx, id, triggerScheduled); err != nil && !errors.Is(err, errSyncBusy) {
				h.log.ErrorContext(ctx, "devinst: queue a scheduled sync", "server_id", id, "err", err)
			}
			continue // a refused job stays due and is tried again next tick
		}
		origin := row.NextSyncAt
		if row.DeferredSince.Valid {
			origin = row.DeferredSince
		}
		if now.Sub(time.UnixMilli(origin.Int64)) < deferMax {
			h.say(ctx, id, deferText)
			h.writeMu.Lock()
			err := h.q.DevSetDeferred(ctx, store.DevSetDeferredParams{
				NextSyncAt: nullTime(now.Add(deferStep)), DeferredSince: origin, UpdatedAt: now.UnixMilli(), ServerID: id,
			})
			h.writeMu.Unlock()
			if err != nil {
				h.log.ErrorContext(ctx, "devinst: defer a scheduled sync", "server_id", id, "err", err)
			}
			continue
		}
		d, err := h.getDev(ctx, id)
		if err != nil || !h.claim(id) {
			continue
		}
		h.say(ctx, id, warnText(d.sourceName))
		if err := h.delay(jobCtx, d, triggerScheduled); err != nil {
			h.release(id)
			h.log.ErrorContext(ctx, "devinst: delay a scheduled sync", "server_id", id, "err", err)
		}
	}
}

// checkTimezone recomputes next_sync_at of every dev that waits for its slot when the panel
// time zone changed since the last tick (the first tick only remembers it).
func (h *Handler) checkTimezone(ctx context.Context, now time.Time) {
	_, name := h.location(ctx)
	if h.lastTZ == "" || h.lastTZ == name {
		h.lastTZ = name
		return
	}
	h.lastTZ = name
	rows, err := h.q.DevList(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "devinst: list devs", "err", err)
		return
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	for _, r := range rows {
		if r.SyncPaused != 0 || !r.NextSyncAt.Valid || r.DeferredSince.Valid {
			continue
		}
		next := nullTime(h.nextSync(ctx, r.SyncTime, now))
		if err := h.q.DevSetNextSync(ctx, store.DevSetNextSyncParams{NextSyncAt: next, UpdatedAt: now.UnixMilli(), ServerID: r.ServerID}); err != nil {
			h.log.ErrorContext(ctx, "devinst: set the next sync", "server_id", r.ServerID, "err", err)
		}
	}
}

// recover runs when Run starts: no sync is queued yet, so a status still "running" belongs to
// a sync the previous panel process did not finish.
func (h *Handler) recover(ctx context.Context) {
	rows, err := h.q.DevList(ctx)
	if err != nil {
		h.log.ErrorContext(ctx, "devinst: list devs", "err", err)
		return
	}
	now := h.clock.Now()
	for _, r := range rows {
		if r.LastSyncStatus.String != statusRunning {
			continue
		}
		if err := h.q.DevSetSyncResult(ctx, store.DevSetSyncResultParams{
			LastSyncStatus:   sql.NullString{String: statusFailed, Valid: true},
			LastSyncError:    sql.NullString{String: "interrupted by a panel restart", Valid: true},
			LastSyncSnapshot: r.LastSyncSnapshot, LastSyncAt: r.LastSyncAt, UpdatedAt: now.UnixMilli(), ServerID: r.ServerID,
		}); err != nil {
			h.log.ErrorContext(ctx, "devinst: reset an interrupted sync", "server_id", r.ServerID, "err", err)
		}
	}
}
