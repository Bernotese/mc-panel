package filesapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

var _ BackupClient = (*UnixClient)(nil)

// BackupsStatus asks whether the service can run restic (always answered, also without it).
func (c *UnixClient) BackupsStatus(ctx context.Context) (BackupsStatus, error) {
	var out BackupsStatus
	err := c.call(ctx, HealthTimeout, http.MethodGet, PathBackupsStatus, nil, &out)
	return out, err
}

// Snapshots lists the snapshots of a server, newest first; never nil.
func (c *UnixClient) Snapshots(ctx context.Context, id int64) ([]Snapshot, error) {
	if err := checkID(id); err != nil {
		return []Snapshot{}, err
	}
	var out SnapshotsResponse
	if err := c.call(ctx, c.backupTimeout, http.MethodPost, PathBackupsList, IDRequest{ID: id}, &out); err != nil {
		return []Snapshot{}, err
	}
	if out.Snapshots == nil {
		out.Snapshots = []Snapshot{}
	}
	return out.Snapshots, nil
}

// CreateBackup starts a backup job (ErrNotFound without data directory, ErrBusy with a job of
// the server running, ErrInsufficientStorage).
func (c *UnixClient) CreateBackup(ctx context.Context, req CreateBackupRequest) (Job, error) {
	if err := req.Validate(); err != nil {
		return Job{}, err
	}
	var out Job
	err := c.call(ctx, c.callTimeout, http.MethodPost, PathBackupsCreate, req, &out)
	return out, err
}

// RestoreBackup checks snapshot and free space synchronously and then starts the restore job.
func (c *UnixClient) RestoreBackup(ctx context.Context, id int64, snapshot string) (Job, error) {
	if err := checkSnapshot(id, snapshot); err != nil {
		return Job{}, err
	}
	var out Job
	err := c.call(ctx, c.backupTimeout, http.MethodPost, PathBackupsRestore, SnapshotRequest{ID: id, Snapshot: snapshot}, &out)
	return out, err
}

// ForgetBackups starts a forget job (retention policy or explicit IDs; restic forget --prune).
func (c *UnixClient) ForgetBackups(ctx context.Context, req ForgetRequest) (Job, error) {
	if err := req.Validate(); err != nil {
		return Job{}, err
	}
	var out Job
	err := c.call(ctx, c.backupTimeout, http.MethodPost, PathBackupsForget, req, &out)
	return out, err
}

// PinBackup sets or clears the tag "pinned" of a scheduled snapshot. restic rewrites the
// snapshot: the answer carries its new ID.
func (c *UnixClient) PinBackup(ctx context.Context, id int64, snapshot string, pinned bool) (Snapshot, error) {
	if err := checkSnapshot(id, snapshot); err != nil {
		return Snapshot{}, err
	}
	var out SnapshotResponse
	if err := c.call(ctx, c.backupTimeout, http.MethodPost, PathBackupsPin, PinRequest{ID: id, Snapshot: snapshot, Pinned: pinned}, &out); err != nil {
		return Snapshot{}, err
	}
	if !ValidSnapshotID(out.Snapshot.ID) {
		return Snapshot{}, fmt.Errorf("%w: pin answer without a valid snapshot id", ErrUnavailable)
	}
	return out.Snapshot, nil
}

// DownloadBackup opens a snapshot as a zip stream (like Download of a folder; Name stays "",
// the caller names the file).
func (c *UnixClient) DownloadBackup(ctx context.Context, id int64, snapshot string) (Download, error) {
	if err := checkSnapshot(id, snapshot); err != nil {
		return Download{}, err
	}
	q := url.Values{"id": {strconv.FormatInt(id, 10)}, "snapshot": {snapshot}}
	d, err := c.openDownload(ctx, PathBackupsDownload+"?"+q.Encode())
	if err != nil {
		return Download{}, err
	}
	if !d.IsDir {
		_ = d.Body.Close()
		return Download{}, fmt.Errorf("%w: a backup download must be a zip stream", ErrUnavailable)
	}
	return d, nil
}

// PurgeBackups deletes the repository and key of a server (a missing one is no error).
func (c *UnixClient) PurgeBackups(ctx context.Context, id int64) error {
	if err := checkID(id); err != nil {
		return err
	}
	return c.call(ctx, c.deleteTimeout, http.MethodPost, PathBackupsPurge, IDRequest{ID: id}, nil)
}

// CancelJob cancels the running backup, restore (until its swap) or forget job of a server.
func (c *UnixClient) CancelJob(ctx context.Context, id int64) error {
	if err := checkID(id); err != nil {
		return err
	}
	return c.call(ctx, c.callTimeout, http.MethodPost, PathJobsCancel, IDRequest{ID: id}, nil)
}
