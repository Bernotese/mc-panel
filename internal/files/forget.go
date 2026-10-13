package files

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// forgetArgs builds the restic arguments of a forget job (spec 4 §3.3, §5.4): explicit IDs, or
// the policy over the unpinned kind=scheduled snapshots of the host "mcpanel". Zero values are
// left out (restic would read 0 as "keep none of this kind").
func forgetArgs(req filesapi.ForgetRequest) []string {
	args := []string{"forget", "--prune", "--max-unused", "10%"}
	if req.Policy == nil {
		return append(args, req.Snapshots...)
	}
	args = append(args, "--host", resticHost, "--tag", kindTagPrefix+string(filesapi.SnapshotScheduled),
		"--keep-tag", pinnedTag, "--group-by", "host")
	for _, k := range []struct {
		flag string
		n    int
	}{
		{"--keep-last", req.Policy.KeepLast}, {"--keep-daily", req.Policy.KeepDaily},
		{"--keep-weekly", req.Policy.KeepWeekly}, {"--keep-monthly", req.Policy.KeepMonthly},
	} {
		if k.n > 0 {
			args = append(args, k.flag, strconv.Itoa(k.n))
		}
	}
	return args
}

// ForgetBackups starts a forget job (spec 4 §4.1): with IDs every one must exist (404 before the
// job); with a policy and no repository the job ends done at once.
func (s *Service) ForgetBackups(ctx context.Context, req filesapi.ForgetRequest) (filesapi.Job, error) {
	if err := req.Validate(); err != nil {
		return filesapi.Job{}, err
	}
	if err := s.requireBackups(req.ID); err != nil {
		return filesapi.Job{}, err
	}
	if err := s.jobs.acquire(req.ID, filesapi.JobForget); err != nil {
		return filesapi.Job{}, err
	}
	ownSlot := true
	defer func() {
		if ownSlot {
			s.jobs.release(req.ID)
		}
	}()
	if len(req.Snapshots) > 0 {
		if !s.repoExists(req.ID) {
			return filesapi.Job{}, fmt.Errorf("%w: this server has no backups", filesapi.ErrNotFound)
		}
		list, err := s.Snapshots(ctx, req.ID)
		if err != nil {
			return filesapi.Job{}, err
		}
		known := map[string]bool{}
		for _, sn := range list {
			known[sn.ID] = true
		}
		for _, id := range req.Snapshots {
			if !known[id] {
				return filesapi.Job{}, fmt.Errorf("%w: no such snapshot %s", filesapi.ErrNotFound, id[:8])
			}
		}
	}
	ownSlot = false
	return s.jobs.start(req.ID, jobSpec{kind: filesapi.JobForget}, s.clock.Now, func(jctx context.Context) error {
		if !s.repoExists(req.ID) {
			return nil // policy, nothing to forget yet
		}
		if _, err := s.restic(jctx, req.ID, nil, "unlock"); err != nil {
			return s.ctxErr(jctx, err)
		}
		var env []string
		if req.Policy != nil {
			env = tzEnv(req.Timezone)
		}
		_, err := s.restic(jctx, req.ID, env, forgetArgs(req)...)
		if err != nil {
			s.log.Warn("files: forget failed", "id", req.ID, "err", err)
		}
		return s.ctxErr(jctx, err)
	})
}

// PinBackup adds or removes the pinned tag of a scheduled snapshot (spec 4 §4.1). restic
// rewrites the snapshot: the answer carries the new ID (read from `restic tag --json`).
func (s *Service) PinBackup(ctx context.Context, req filesapi.PinRequest) (filesapi.Snapshot, error) {
	if !filesapi.ValidSnapshotID(req.Snapshot) {
		return filesapi.Snapshot{}, fmt.Errorf("%w: snapshot id must be 64 hex characters", filesapi.ErrInvalid)
	}
	if err := s.requireBackups(req.ID); err != nil {
		return filesapi.Snapshot{}, err
	}
	release, err := s.jobs.lock(req.ID)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	defer release()
	sn, err := s.findSnapshot(ctx, req.ID, req.Snapshot)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	if sn.Kind != filesapi.SnapshotScheduled {
		return filesapi.Snapshot{}, fmt.Errorf("%w: only scheduled snapshots can be pinned", filesapi.ErrInvalid)
	}
	if sn.Pinned == req.Pinned {
		return sn, nil
	}
	if _, err := s.restic(ctx, req.ID, nil, "unlock"); err != nil {
		return filesapi.Snapshot{}, err
	}
	op := "--add"
	if !req.Pinned {
		op = "--remove"
	}
	out, err := s.restic(ctx, req.ID, nil, "--json", "tag", op, pinnedTag, sn.ID)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	if newID := changedSnapshotID(out, sn.ID); newID != sn.ID {
		return s.findSnapshot(ctx, req.ID, newID)
	}
	// An older restic without JSON output for tag: the rewritten snapshot keeps time and kind.
	list, err := s.Snapshots(ctx, req.ID)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	for _, c := range list {
		if c.Time == sn.Time && c.Kind == sn.Kind && c.Pinned == req.Pinned && c.ID != sn.ID {
			return c, nil
		}
	}
	return filesapi.Snapshot{}, fmt.Errorf("restic tag changed no snapshot")
}

// changedSnapshotID reads new_snapshot_id from the "changed" message of `restic tag --json`
// (restic 0.19.1 cmd_tag.go); without one the old ID comes back.
func changedSnapshotID(out []byte, old string) string {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		var m struct {
			MessageType   string `json:"message_type"`
			OldSnapshotID string `json:"old_snapshot_id"`
			NewSnapshotID string `json:"new_snapshot_id"`
		}
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.MessageType == "changed" && m.OldSnapshotID == old && filesapi.ValidSnapshotID(m.NewSnapshotID) {
			return m.NewSnapshotID
		}
	}
	return old
}

// PurgeBackups removes the repository and the key of server id (spec 4 §5.6); nothing there is
// fine. It needs no restic (only folders are removed), so a server can be deleted with its
// backups also on a host without restic (V5). Everything goes through an os.Root on backups/:
// a symlink planted in repos/ or keys/ never leads the removal elsewhere.
func (s *Service) PurgeBackups(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: id must be > 0", filesapi.ErrInvalid)
	}
	release, err := s.jobs.lock(id)
	if err != nil {
		return err
	}
	defer release()
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.backupsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil // backups/ was never created: nothing to purge
	}
	if err != nil {
		return fmt.Errorf("open the backup directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	name := strconv.FormatInt(id, 10)
	if err := root.RemoveAll(path.Join("repos", name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the backup repository: %w", err)
	}
	if err := root.Remove(path.Join("keys", name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the repository key: %w", err)
	}
	return nil
}
