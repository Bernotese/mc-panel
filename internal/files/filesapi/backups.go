package filesapi

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Backups (sub-project 4 §4): restic snapshots of a server's data directory. The service makes
// and reads them as tar streams; restic never opens a path in the data directory.
const (
	PathBackupsStatus   = "/v1/backups/status"   // GET -> BackupsStatus
	PathBackupsList     = "/v1/backups/list"     // POST IDRequest -> SnapshotsResponse
	PathBackupsCreate   = "/v1/backups/create"   // POST CreateBackupRequest -> 202 Job
	PathBackupsRestore  = "/v1/backups/restore"  // POST SnapshotRequest -> 202 Job (pre-check synchronous)
	PathBackupsForget   = "/v1/backups/forget"   // POST ForgetRequest -> 202 Job
	PathBackupsPin      = "/v1/backups/pin"      // POST PinRequest -> SnapshotResponse
	PathBackupsDownload = "/v1/backups/download" // GET query id, snapshot -> zip stream (HeaderKind "dir")
	PathBackupsPurge    = "/v1/backups/purge"    // POST IDRequest -> 204
	PathJobsCancel      = "/v1/jobs/cancel"      // POST IDRequest -> 204

	MinResticVersion = "0.17.0"
	// MaxNoteRunes bounds the note of a manual snapshot (a restic tag, base64url encoded).
	MaxNoteRunes = 64
	// MaxForgetIDs bounds the snapshots of one forget request.
	MaxForgetIDs = 100
	// MaxKeep bounds every keep_* value of a retention policy.
	MaxKeep = 1000
	// MaxTimezoneLen bounds an IANA time zone name.
	MaxTimezoneLen = 64

	// BackupCallTimeout bounds list, pin, forget and the restore pre-check (restic reads the
	// repository index).
	BackupCallTimeout = 2 * time.Minute
)

// JobKind is the kind of a service job; "" (an older service) counts as JobExtract.
type JobKind string

const (
	JobExtract JobKind = "extract"
	JobBackup  JobKind = "backup"
	JobRestore JobKind = "restore"
	JobForget  JobKind = "forget"
)

// SnapshotKind is the restic tag kind=<kind> of a snapshot.
type SnapshotKind string

const (
	SnapshotScheduled  SnapshotKind = "scheduled"
	SnapshotManual     SnapshotKind = "manual"
	SnapshotPreRestore SnapshotKind = "pre-restore"
	// SnapshotDevSync is a snapshot a dev sync took of the dev's source (dev instances spec §4.1).
	SnapshotDevSync SnapshotKind = "dev-sync"
)

func (k SnapshotKind) Valid() bool {
	switch k {
	case SnapshotScheduled, SnapshotManual, SnapshotPreRestore, SnapshotDevSync:
		return true
	}
	return false
}

// Snapshot is one restic snapshot of a server's repository.
type Snapshot struct {
	ID      string       `json:"id"`       // 64 hex
	ShortID string       `json:"short_id"` // 8 hex
	Time    int64        `json:"time"`     // Unix ms
	Kind    SnapshotKind `json:"kind"`
	Pinned  bool         `json:"pinned"`
	Note    string       `json:"note"`  // "" = none
	Bytes   int64        `json:"bytes"` // size of data.tar (summary.total_bytes_processed)
}

// BackupsStatus says whether the service can run restic.
type BackupsStatus struct {
	Available bool   `json:"available"`
	Version   string `json:"version"` // restic version, "" if unavailable
	Reason    string `json:"reason"`  // "" if available; never a host path
}

type IDRequest struct {
	ID int64 `json:"id"`
}

type CreateBackupRequest struct {
	ID       int64        `json:"id"`
	Kind     SnapshotKind `json:"kind"`
	Note     string       `json:"note"`               // only for SnapshotManual; "" = none
	Timezone string       `json:"timezone,omitempty"` // backup_timezone: restic stores the snapshot time in this zone, which sets the day/week/month buckets of forget; "" = the service's zone
}

type SnapshotRequest struct {
	ID       int64  `json:"id"`
	Snapshot string `json:"snapshot"`
	// SourceID names the repository the snapshot comes from when it is not ID's own: a dev
	// sync restores a snapshot of the source into the dev (dev instances spec §4.2). 0 = ID.
	SourceID int64 `json:"source_id,omitempty"`
}

// ForgetPolicy is the retention of the scheduled snapshots (restic forget --keep-*).
type ForgetPolicy struct {
	KeepLast    int `json:"keep_last"`
	KeepDaily   int `json:"keep_daily"`
	KeepWeekly  int `json:"keep_weekly"`
	KeepMonthly int `json:"keep_monthly"`
}

// ForgetRequest carries exactly one of Snapshots (1..MaxForgetIDs IDs) or Policy (then Timezone
// is required, an IANA name accepted by ValidTimezone).
type ForgetRequest struct {
	ID        int64         `json:"id"`
	Snapshots []string      `json:"snapshots,omitempty"`
	Policy    *ForgetPolicy `json:"policy,omitempty"`
	Timezone  string        `json:"timezone,omitempty"`
}

type PinRequest struct {
	ID       int64  `json:"id"`
	Snapshot string `json:"snapshot"`
	Pinned   bool   `json:"pinned"`
}

type SnapshotsResponse struct {
	Snapshots []Snapshot `json:"snapshots"` // never nil; newest first
}

type SnapshotResponse struct {
	Snapshot Snapshot `json:"snapshot"`
}

// ValidSnapshotID reports whether s is a full restic snapshot ID: exactly 64 lowercase hex
// characters. Only such IDs are ever passed to restic.
func ValidSnapshotID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// ValidNote reports whether s may be the note of a manual snapshot: "" (none), or at most
// MaxNoteRunes runes of valid UTF-8 without control characters and not only spaces.
func ValidNote(s string) bool {
	if s == "" {
		return true
	}
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxNoteRunes || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ValidTimezone reports whether tz is an IANA time zone name the schedules and restic's
// retention buckets can use. "" and "Local" are refused: both mean the host's zone, which the
// panel and the service may not share.
func ValidTimezone(tz string) bool {
	if tz == "" || tz == "Local" || len(tz) > MaxTimezoneLen {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

// Validate checks every keep value (0..MaxKeep) and that at least one is > 0 (restic forget
// without any keep option would remove every snapshot of the group).
func (p ForgetPolicy) Validate() error {
	some := false
	for _, v := range []int{p.KeepLast, p.KeepDaily, p.KeepWeekly, p.KeepMonthly} {
		if v < 0 || v > MaxKeep {
			return fmt.Errorf("%w: keep values must be 0..%d", ErrInvalid, MaxKeep)
		}
		if v > 0 {
			some = true
		}
	}
	if !some {
		return fmt.Errorf("%w: at least one keep value must be > 0", ErrInvalid)
	}
	return nil
}

func (r ForgetRequest) Validate() error {
	if err := checkID(r.ID); err != nil {
		return err
	}
	switch {
	case len(r.Snapshots) > 0 && r.Policy != nil:
		return fmt.Errorf("%w: snapshots and policy exclude each other", ErrInvalid)
	case r.Policy != nil:
		if err := r.Policy.Validate(); err != nil {
			return err
		}
		if !ValidTimezone(r.Timezone) {
			return fmt.Errorf("%w: a policy needs an IANA time zone", ErrInvalid)
		}
		return nil
	case len(r.Snapshots) == 0:
		return fmt.Errorf("%w: snapshots or policy required", ErrInvalid)
	case len(r.Snapshots) > MaxForgetIDs:
		return fmt.Errorf("%w: at most %d snapshots per request", ErrInvalid, MaxForgetIDs)
	case r.Timezone != "":
		return fmt.Errorf("%w: a time zone only goes with a policy", ErrInvalid)
	}
	seen := make(map[string]bool, len(r.Snapshots))
	for _, s := range r.Snapshots {
		if !ValidSnapshotID(s) || seen[s] {
			return fmt.Errorf("%w: snapshots must be distinct 64-character hex IDs", ErrInvalid)
		}
		seen[s] = true
	}
	return nil
}

func (r CreateBackupRequest) Validate() error {
	if err := checkID(r.ID); err != nil {
		return err
	}
	if !r.Kind.Valid() {
		return fmt.Errorf("%w: kind must be scheduled, manual or pre-restore", ErrInvalid)
	}
	if !ValidNote(r.Note) {
		return fmt.Errorf("%w: note: at most %d characters, no control characters", ErrInvalid, MaxNoteRunes)
	}
	if r.Note != "" && r.Kind != SnapshotManual {
		return fmt.Errorf("%w: only manual snapshots have a note", ErrInvalid)
	}
	if r.Timezone != "" && !ValidTimezone(r.Timezone) {
		return fmt.Errorf("%w: timezone must be an IANA time zone name", ErrInvalid)
	}
	return nil
}

func checkSnapshot(id int64, snapshot string) error {
	if err := checkID(id); err != nil {
		return err
	}
	if !ValidSnapshotID(snapshot) {
		return fmt.Errorf("%w: snapshot must be 64 lowercase hex characters", ErrInvalid)
	}
	return nil
}

// BackupClient is the panel's view of the backup endpoints (implemented by *UnixClient).
type BackupClient interface {
	BackupsStatus(ctx context.Context) (BackupsStatus, error)
	Snapshots(ctx context.Context, id int64) ([]Snapshot, error)
	CreateBackup(ctx context.Context, req CreateBackupRequest) (Job, error)
	RestoreBackup(ctx context.Context, id int64, snapshot string) (Job, error)
	RestoreFrom(ctx context.Context, targetID, sourceID int64, snapshot string) (Job, error)
	ForgetBackups(ctx context.Context, req ForgetRequest) (Job, error)
	PinBackup(ctx context.Context, id int64, snapshot string, pinned bool) (Snapshot, error)
	DownloadBackup(ctx context.Context, id int64, snapshot string) (Download, error)
	PurgeBackups(ctx context.Context, id int64) error
	CancelJob(ctx context.Context, id int64) error
	Job(ctx context.Context, id int64) (*Job, error)
}
