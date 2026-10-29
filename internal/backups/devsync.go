package backups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// devReuseMax is the age up to which a scheduled or dev-sync snapshot of the source is reused
// for a dev sync instead of taking a new one (dev instances spec §4.1).
const devReuseMax = 6 * time.Hour

var (
	// errNoDevBackups answers every backup endpoint of a dev instance (dev instances spec §1.3).
	errNoDevBackups = errors.New("dev instances have no backups")
	// errNotDevOf: the target of a dev sync is not the dev of its source. The direction of a
	// sync is checked against the DB when it is queued and again when it runs, so swapped ids
	// can never restore into the source (dev instances spec §1.2).
	errNotDevOf = errors.New("dev sync: the target is not the dev instance of the source")
)

// QueueDevSync queues the sync of dev instance s.DevID from its source s.SourceID (dev
// instances spec §4.1) as a job of the backup queue, keyed by the dev: while it waits or runs,
// Holding(DevID) reports it (the dev's data is busy: no start, no wake, no reconcile start);
// the source is never held, stopped or restored. A dev with a queued or running job answers
// ErrBackupRunning (409 backup_running).
//
// The job takes a snapshot of the source (or reuses a fresh scheduled or dev-sync one), stops
// the dev, restores the snapshot from the source's repository into the dev's data directory,
// runs s.Apply and forgets the source's older dev-sync snapshots. s.Done runs exactly once
// when the job has ended in any way (also when it was dropped before it ran) with the
// snapshot used and nil, or an error whose text is safe to show; a job that was cancelled (the
// dev was deleted, the panel shut down) has an error matching context.Canceled.
func (h *Handler) QueueDevSync(ctx context.Context, s DevSync) error {
	switch {
	case s.DevID <= 0 || s.SourceID <= 0 || s.DevID == s.SourceID:
		return fmt.Errorf("dev sync: invalid ids (dev %d, source %d)", s.DevID, s.SourceID)
	case s.Apply == nil || s.Done == nil:
		return errors.New("dev sync: Apply and Done must be set")
	}
	if _, err := h.devOf(ctx, s); err != nil {
		return err
	}
	trigger := triggerManual // the job's trigger (scheduled or manual); "create" is a manual one
	if s.Trigger == triggerScheduled {
		trigger = triggerScheduled
	}
	_, err := h.queue.add(ctx, jobSpec{
		serverID: s.DevID, name: s.DevName, kind: kindDevSync, trigger: trigger,
		run: func(ctx context.Context, j *job) (string, error) {
			return h.runDevSync(ctx, j, s)
		},
		after: h.reconcile, // the gate is open again
		ended: func(snap string, err error) {
			s.Done(snap, devSyncErr(err))
		},
	})
	return err
}

// devSyncErr turns the final error of a dev sync job into the one Done gets: nil, an error
// matching context.Canceled, or a jobError with a text safe to show (jobErrorOf; the queue has
// logged the original).
func devSyncErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, errServerDeleted), errors.Is(err, errShuttingDown):
		return fmt.Errorf("%w: %s", context.Canceled, msgCanceled)
	}
	code, msg := jobErrorOf(err)
	return &jobError{code: code, msg: msg}
}

// runDevSync is the flow of dev instances spec §4.1 (steps 1, 3 and 4; the player check of
// step 2 is the caller's). Every write goes to the dev: the source only gets the backup of
// runBackup (save-off/save-all flush/save-on as in every backup) and the forget of its older
// dev-sync snapshots.
func (h *Handler) runDevSync(ctx context.Context, j *job, s DevSync) (snapshot string, err error) {
	defer func() { err = canceledOr(ctx, err) }()
	if h.files == nil {
		return "", errNoFiles
	}
	if st, err := h.files.BackupsStatus(ctx); err != nil {
		return "", err
	} else if !st.Available {
		return "", failf(codeBackups, "restic is not available on this host, the dev cannot be synced")
	}
	if err := h.step(ctx, j, stepSnapshot); err != nil {
		return "", err
	}
	used, err := h.devSnapshot(ctx, j, s)
	if err != nil {
		return "", err
	}
	if err := h.step(ctx, j, stepStopping); err != nil {
		return used.ID, err
	}
	// Admitted like a restore (data safety spec §3): the dev is stopped and settled and the
	// service takes the restore job under the dev's admission lock.
	var rj filesapi.Job
	err = h.admit(ctx, s.DevID, func(ctx context.Context) error {
		if err := h.stopSettled(ctx, s.DevID); err != nil {
			return err
		}
		if err := h.step(ctx, j, stepRestoring); err != nil {
			return err
		}
		rj, err = h.files.RestoreFrom(ctx, s.DevID, s.SourceID, used.ID)
		return err
	})
	if err != nil {
		return used.ID, err
	}
	// the queue's hold stays until a poll shows that the service's restore ended
	if _, err := h.waitService(ctx, j, s.DevID, rj.ID, waitRestore); err != nil {
		return used.ID, err
	}
	if err := h.step(ctx, j, stepApplying); err != nil {
		return used.ID, err
	}
	if err := s.Apply(ctx); err != nil {
		if ctx.Err() == nil {
			h.log.WarnContext(ctx, "backups: dev sync: applying the source's spec failed", "dev_id", s.DevID, "err", err)
		}
		return used.ID, failf(codeInternal, "the data was copied, but applying the settings of %s failed", s.SourceName)
	}
	h.forgetOldDevSync(ctx, j, s.SourceID, used)
	return used.ID, nil
}

// devSnapshot returns the snapshot of the source a dev sync restores: for a scheduled sync or
// the first one of a new dev, the newest scheduled or dev-sync snapshot that is newer than the
// dev's last sync and at most devReuseMax old; else (and always for a manual "Sync now", which
// wants the source's current state) a new dev-sync snapshot (runBackup on the source). Manual
// and pre-restore snapshots are never used: they may hold a state the operator did not want to
// keep.
func (h *Handler) devSnapshot(ctx context.Context, j *job, s DevSync) (filesapi.Snapshot, error) {
	dev, err := h.devOf(ctx, s)
	if errors.Is(err, errNotDevOf) && h.serverGone(ctx, s.DevID) {
		return filesapi.Snapshot{}, errServerDeleted // the dev went away while its job waited
	}
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	if s.Trigger == triggerManual {
		return h.newDevSnapshot(ctx, j, s)
	}
	list, err := h.files.Snapshots(ctx, s.SourceID)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	now := h.clock.Now()
	var best *filesapi.Snapshot
	for i, sn := range list {
		if sn.Kind != filesapi.SnapshotScheduled && sn.Kind != filesapi.SnapshotDevSync {
			continue
		}
		at := time.UnixMilli(sn.Time)
		if now.Sub(at) > devReuseMax || (dev.LastSyncAt.Valid && sn.Time <= dev.LastSyncAt.Int64) ||
			(dev.LastSyncSnapshot.Valid && sn.ID == dev.LastSyncSnapshot.String) {
			continue
		}
		if best == nil || sn.Time > best.Time {
			best = &list[i]
		}
	}
	if best != nil {
		return *best, nil
	}
	return h.newDevSnapshot(ctx, j, s)
}

// newDevSnapshot takes a new dev-sync snapshot of the source.
func (h *Handler) newDevSnapshot(ctx context.Context, j *job, s DevSync) (filesapi.Snapshot, error) {
	snap, err := h.runBackup(ctx, j, s.SourceID, filesapi.SnapshotDevSync, "")
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	// The service only reports the id; the forget step needs its time.
	return filesapi.Snapshot{ID: snap, Time: h.clock.Now().UnixMilli(), Kind: filesapi.SnapshotDevSync}, nil
}

// devOf returns the dev_instances row of s.DevID; errNotDevOf unless it is the dev of
// s.SourceID.
func (h *Handler) devOf(ctx context.Context, s DevSync) (store.DevInstance, error) {
	dev, err := h.q.DevGet(ctx, s.DevID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return store.DevInstance{}, fmt.Errorf("%w (server %d is no dev instance)", errNotDevOf, s.DevID)
	case err != nil:
		return store.DevInstance{}, err
	case dev.SourceID != s.SourceID:
		return store.DevInstance{}, fmt.Errorf("%w (dev %d copies server %d, not %d)", errNotDevOf, s.DevID, dev.SourceID, s.SourceID)
	}
	return dev, nil
}

// forgetOldDevSync forgets the source's dev-sync snapshots that are older than the one just
// used (dev instances spec §4.1); pinned ones are kept like everywhere else (backups spec). It
// only logs a failure: the sync itself succeeded.
func (h *Handler) forgetOldDevSync(ctx context.Context, j *job, sourceID int64, used filesapi.Snapshot) {
	err := func() error {
		list, err := h.files.Snapshots(ctx, sourceID)
		if err != nil {
			return err
		}
		for _, sn := range list {
			if sn.ID == used.ID {
				used.Time = sn.Time // a new snapshot: its time as restic recorded it
			}
		}
		var old []string
		for _, sn := range list {
			if sn.Kind == filesapi.SnapshotDevSync && !sn.Pinned && sn.ID != used.ID && sn.Time <= used.Time && len(old) < filesapi.MaxForgetIDs {
				old = append(old, sn.ID)
			}
		}
		if len(old) == 0 {
			return nil
		}
		j.setStep(stepForget)
		sj, err := h.files.ForgetBackups(ctx, filesapi.ForgetRequest{ID: sourceID, Snapshots: old})
		if err != nil {
			return err
		}
		_, err = h.waitService(ctx, j, sourceID, sj.ID, waitForget)
		return err
	}()
	if err != nil && ctx.Err() == nil {
		h.log.WarnContext(ctx, "backups: dev sync: forgetting older dev-sync snapshots failed", "server_id", sourceID, "err", err)
	}
}

// devManaged writes 409 dev_managed and returns true when server id is a dev instance: no
// backup endpoint serves a dev, also not for admins (dev instances spec §1.3, §7).
func (h *Handler) devManaged(w http.ResponseWriter, r *http.Request, id int64) bool {
	dev, err := h.isDev(r.Context(), id)
	if err != nil {
		httpx.Internal(w, r, err)
		return true
	}
	if dev {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeDevManaged, errNoDevBackups.Error())
	}
	return dev
}

// isDev reports whether server id is a dev instance (it has a dev_instances row).
func (h *Handler) isDev(ctx context.Context, id int64) (bool, error) {
	_, err := h.q.DevGet(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
