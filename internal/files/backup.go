package files

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	resticHost     = "mcpanel"
	resticPanelTag = "mcpanel"
	kindTagPrefix  = "kind="
	noteTagPrefix  = "note="
	pinnedTag      = "pinned"
	stdinFilename  = "data.tar"
)

// noteTag encodes a note as a restic tag (base64url without padding: no ',' that restic would
// split at, spec 4 §4.1).
func noteTag(note string) string {
	return noteTagPrefix + base64.RawURLEncoding.EncodeToString([]byte(note))
}

// parseNote decodes the value of a note tag; anything broken is no note.
func parseNote(v string) string {
	b, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil || !filesapi.ValidNote(string(b)) {
		return ""
	}
	return string(b)
}

// tzEnv is the TZ variable for restic (empty zone = none).
func tzEnv(zone string) []string {
	if zone == "" {
		return nil
	}
	return []string{"TZ=" + zone}
}

// CreateBackup checks the request and the free space and starts a backup job (spec 4 §4.2).
func (s *Service) CreateBackup(ctx context.Context, req filesapi.CreateBackupRequest) (filesapi.Job, error) {
	// ID, kind, note and time zone (TZ of restic) with the shared rules of filesapi (V1)
	if err := req.Validate(); err != nil {
		return filesapi.Job{}, err
	}
	if err := s.requireBackups(req.ID); err != nil {
		return filesapi.Job{}, err
	}
	if err := s.jobs.acquire(req.ID, filesapi.JobBackup); err != nil {
		return filesapi.Job{}, err
	}
	ownSlot := true
	defer func() {
		if ownSlot {
			s.jobs.release(req.ID)
		}
	}()
	root, err := s.open(req.ID)
	if err != nil {
		return filesapi.Job{}, err
	}
	handedOver := false
	defer func() {
		if !handedOver {
			_ = root.Close()
		}
	}()
	// Deduplication makes the growth of the repository unknown up front: only the reserve is
	// checked (spec 4 §4.2); while the stream runs, backupSpace aborts it when the free space
	// drops below half the reserve (SEC-04).
	avail, keep, err := s.backupSpace()
	if err != nil {
		return filesapi.Job{}, err
	}
	if avail < keep {
		return filesapi.Job{}, fmt.Errorf("%w: %d bytes free for backups, the reserve is %d bytes",
			filesapi.ErrInsufficientStorage, avail, keep)
	}
	ownSlot = false
	job, err := s.jobs.start(req.ID, jobSpec{kind: filesapi.JobBackup}, s.clock.Now, func(jctx context.Context) error {
		defer root.Close()
		err := s.runBackup(jctx, root, req)
		if err != nil {
			s.log.Warn("files: backup failed", "id", req.ID, "kind", req.Kind, "err", err)
		}
		return err
	})
	if err != nil {
		return filesapi.Job{}, err
	}
	handedOver = true
	return job, nil
}

// runBackup streams the data directory into `restic backup --stdin`. On every error restic is
// killed before its stdin is closed: a stream that ended early must never become a snapshot.
func (s *Service) runBackup(ctx context.Context, root *os.Root, req filesapi.CreateBackupRequest) error {
	if err := s.ensureRepo(ctx, req.ID); err != nil {
		return s.ctxErr(ctx, err)
	}
	if _, err := s.restic(ctx, req.ID, nil, "unlock"); err != nil {
		return s.ctxErr(ctx, err)
	}
	args := []string{"--no-cache", "--json", "backup", "--stdin", "--stdin-filename", stdinFilename,
		"--host", resticHost, "--tag", resticPanelTag, "--tag", kindTagPrefix + string(req.Kind)}
	if req.Note != "" {
		args = append(args, "--tag", noteTag(req.Note))
	}
	// restic prints a status line every second for the whole run: only the summary is kept.
	out := &summaryWriter{}
	// TZ: restic stores the snapshot time in this zone; forget buckets days/weeks/months by it.
	p, err := s.runner.Start(ctx, resticCmd{Args: args, Env: append(s.resticEnv(req.ID), tzEnv(req.Timezone)...), Stdin: true, Stdout: out})
	if err != nil {
		return s.startErr(err)
	}
	stdin := p.Stdin()
	werr := writeBackupTar(ctx, root, &spaceCheckWriter{w: stdin, s: s}, s.cfg.Limits, s.backupHook,
		func(n int64) { s.jobs.progress(req.ID, n) },
		func(rel string, cause error) {
			s.jobs.skip(req.ID)
			if cause != nil {
				s.log.Warn("files: backup left out an entry it cannot read", "id", req.ID, "path", rel, "err", s.publicMessage(cause.Error()))
			}
		})
	if werr == nil && ctx.Err() != nil {
		werr = ctx.Err() // cancelled after the last entry: restic must still not see the end
	}
	if werr != nil {
		p.Kill() // first: restic must not see the end of the stream
		perr := p.Wait()
		_ = stdin.Close()
		var ex *resticExit
		switch {
		case ctx.Err() != nil:
			return s.jobs.stopErr()
		case errors.As(perr, &ex) && ex.Code != -1:
			return s.resticErr("backup", perr) // restic ended on its own: its error explains the broken pipe
		}
		return werr
	}
	if err := stdin.Close(); err != nil {
		p.Kill()
		_ = p.Wait()
		return fmt.Errorf("finish the backup stream: %w", err)
	}
	if err := p.Wait(); err != nil {
		return s.ctxErr(ctx, s.resticErr("backup", err))
	}
	sid, err := backupSnapshotID(out.Summary())
	if err != nil {
		return err
	}
	s.jobs.setSnapshot(req.ID, sid)
	return nil
}

// backupSpace returns the free space of the backups file system and its reserve.
func (s *Service) backupSpace() (avail, reserve uint64, err error) {
	dir := s.backupsDir()
	avail, err = s.disk.Available(dir)
	if err != nil {
		return 0, 0, fmt.Errorf("check free space: %w", err)
	}
	reserve, err = s.reserveBytes(dir)
	if err != nil {
		return 0, 0, err
	}
	return avail, reserve, nil
}

// backupSpaceEvery is how many stream bytes pass between two free-space checks of a running
// backup (a variable so tests can lower it).
var backupSpaceEvery int64 = 64 << 20

// spaceCheckWriter checks the free space of the backups file system every backupSpaceEvery
// bytes of the backup stream: below half the reserve the backup ends with
// ErrInsufficientStorage (restic is killed, no snapshot), so one server's backup cannot fill
// the disk the panel, the DB and Docker live on.
type spaceCheckWriter struct {
	w     io.Writer
	s     *Service
	since int64
}

func (c *spaceCheckWriter) Write(p []byte) (int, error) {
	if c.since += int64(len(p)); c.since >= backupSpaceEvery {
		c.since = 0
		avail, keep, err := c.s.backupSpace()
		if err != nil {
			return 0, err
		}
		if avail < keep/2 {
			return 0, fmt.Errorf("%w: only %d bytes left for backups, half the reserve is %d bytes",
				filesapi.ErrInsufficientStorage, avail, keep/2)
		}
	}
	return c.w.Write(p)
}

// ctxErr prefers the job's stop reason when its context ended (restic was interrupted).
func (s *Service) ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return s.jobs.stopErr()
	}
	return err
}

// backupSnapshotID reads the summary line of `restic backup --json` (summaryWriter.Summary).
func backupSnapshotID(summary []byte) (string, error) {
	if summary == nil {
		return "", errors.New("restic printed no backup summary")
	}
	var m struct {
		SnapshotID string `json:"snapshot_id"`
	}
	if err := json.Unmarshal(summary, &m); err != nil || !filesapi.ValidSnapshotID(m.SnapshotID) {
		return "", errors.New("restic finished without a snapshot")
	}
	return m.SnapshotID, nil
}

// maxResticLine bounds one line of restic's JSON output that summaryWriter looks at; longer
// lines are dropped (status and summary lines are a few hundred bytes).
const maxResticLine = 1 << 20

// summaryWriter takes the stdout of `restic backup --json` line by line and keeps only the
// last summary line, so a backup that runs for days holds no growing buffer.
type summaryWriter struct {
	mu       sync.Mutex
	line     []byte // the current, unfinished line
	overlong bool   // the current line exceeded maxResticLine
	summary  []byte
}

func (w *summaryWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.add(p)
			break
		}
		w.add(p[:i])
		w.endLine()
		p = p[i+1:]
	}
	return n, nil
}

func (w *summaryWriter) add(b []byte) {
	if w.overlong {
		return
	}
	if len(w.line)+len(b) > maxResticLine {
		w.overlong, w.line = true, w.line[:0]
		return
	}
	w.line = append(w.line, b...)
}

func (w *summaryWriter) endLine() {
	var m struct {
		MessageType string `json:"message_type"`
	}
	if !w.overlong && json.Unmarshal(w.line, &m) == nil && m.MessageType == "summary" {
		w.summary = bytes.Clone(w.line)
	}
	w.line, w.overlong = w.line[:0], false
}

// Summary returns the last summary line (also one without a final newline), or nil.
func (w *summaryWriter) Summary() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.line) > 0 {
		w.endLine()
	}
	return w.summary
}

// resticSnapshot is one element of `restic snapshots --json`.
type resticSnapshot struct {
	ID      string    `json:"id"`
	Time    time.Time `json:"time"`
	Tags    []string  `json:"tags"`
	Summary *struct {
		TotalBytesProcessed int64 `json:"total_bytes_processed"`
	} `json:"summary"`
}

// toSnapshot converts a restic snapshot; snapshots without the panel tag or a known kind are
// not the panel's (false).
func toSnapshot(r resticSnapshot) (filesapi.Snapshot, bool) {
	if !filesapi.ValidSnapshotID(r.ID) || !slices.Contains(r.Tags, resticPanelTag) {
		return filesapi.Snapshot{}, false
	}
	sn := filesapi.Snapshot{ID: r.ID, ShortID: r.ID[:8], Time: r.Time.UnixMilli()}
	for _, t := range r.Tags {
		switch {
		case strings.HasPrefix(t, kindTagPrefix):
			sn.Kind = filesapi.SnapshotKind(strings.TrimPrefix(t, kindTagPrefix))
		case strings.HasPrefix(t, noteTagPrefix):
			sn.Note = parseNote(strings.TrimPrefix(t, noteTagPrefix))
		case t == pinnedTag:
			sn.Pinned = true
		}
	}
	if !sn.Kind.Valid() {
		return filesapi.Snapshot{}, false
	}
	if r.Summary != nil {
		sn.Bytes = r.Summary.TotalBytesProcessed
	}
	return sn, true
}

// Snapshots lists the panel's snapshots of server id, newest first (spec 4 §4.1); without a
// repository the list is empty and restic is not asked.
func (s *Service) Snapshots(ctx context.Context, id int64) ([]filesapi.Snapshot, error) {
	if err := s.requireBackups(id); err != nil {
		return nil, err
	}
	if !s.repoExists(id) {
		return []filesapi.Snapshot{}, nil
	}
	out, err := s.restic(ctx, id, nil, "--json", "--no-lock", "snapshots", "--host", resticHost, "--tag", resticPanelTag)
	if err != nil {
		return nil, err
	}
	var raw []resticSnapshot
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("restic snapshots printed invalid JSON: %w", err)
	}
	list := make([]filesapi.Snapshot, 0, len(raw))
	for _, r := range raw {
		if sn, ok := toSnapshot(r); ok {
			list = append(list, sn)
		}
	}
	slices.SortFunc(list, func(a, b filesapi.Snapshot) int {
		if c := cmp.Compare(b.Time, a.Time); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return list, nil
}

// findSnapshot returns snapshot sid of server id (ErrNotFound if this repository has none).
func (s *Service) findSnapshot(ctx context.Context, id int64, sid string) (filesapi.Snapshot, error) {
	if !filesapi.ValidSnapshotID(sid) {
		return filesapi.Snapshot{}, fmt.Errorf("%w: snapshot id must be 64 hex characters", filesapi.ErrInvalid)
	}
	list, err := s.Snapshots(ctx, id)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	for _, sn := range list {
		if sn.ID == sid {
			return sn, nil
		}
	}
	return filesapi.Snapshot{}, fmt.Errorf("%w: no such snapshot", filesapi.ErrNotFound)
}
