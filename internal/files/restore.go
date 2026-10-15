package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// RestoreBackup checks the snapshot and the free space synchronously and starts a restore job
// (spec 4 §4.3). The panel stops the server before; the service does not check that.
func (s *Service) RestoreBackup(ctx context.Context, req filesapi.SnapshotRequest) (filesapi.Job, error) {
	if !filesapi.ValidSnapshotID(req.Snapshot) {
		return filesapi.Job{}, fmt.Errorf("%w: snapshot id must be 64 hex characters", filesapi.ErrInvalid)
	}
	if err := s.requireBackups(req.ID); err != nil {
		return filesapi.Job{}, err
	}
	if err := s.jobs.acquire(req.ID, filesapi.JobRestore); err != nil {
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
	if j, ok := s.readJournals(false)[req.ID]; ok && j.Phase == phaseStuck {
		return filesapi.Job{}, fmt.Errorf("%w: a previous restore of this server could not be completed; its old state is kept in a hidden folder - see the files service log", filesapi.ErrBusy)
	}
	snap, err := s.findSnapshot(ctx, req.ID, req.Snapshot)
	if err != nil {
		return filesapi.Job{}, err
	}
	res, err := s.reserve(req.ID, snap.Bytes)
	if err != nil {
		return filesapi.Job{}, err
	}
	if err := ctx.Err(); err != nil { // the caller gave up during the pre-check
		res.release()
		return filesapi.Job{}, err
	}
	ownSlot = false
	job, err := s.jobs.start(req.ID, jobSpec{kind: filesapi.JobRestore, snapshot: snap.ID, totalBytes: snap.Bytes}, s.clock.Now,
		func(jctx context.Context) error {
			defer root.Close()
			defer res.release()
			err := s.runRestore(jctx, root, req.ID, snap.ID, res)
			if err != nil {
				s.log.Warn("files: restore failed", "id", req.ID, "snapshot", snap.ID, "err", err)
			}
			return err
		})
	if err != nil {
		res.release()
		return filesapi.Job{}, err
	}
	handedOver = true
	return job, nil
}

func (s *Service) runRestore(ctx context.Context, root *os.Root, id int64, sid string, res *reservation) error {
	if _, err := s.restic(ctx, id, nil, "unlock"); err != nil {
		return s.ctxErr(ctx, err)
	}
	// The container may have made its folders read-only (the service owns them): u+rwx on the
	// data directory, so the temp folder can be created and entries moved.
	if err := unlockDir(root, "."); err != nil {
		return err
	}
	tmp, err := s.tempName("")
	if err != nil {
		return err
	}
	if err := root.Mkdir(tmp, dirMode); err != nil { //nolint:gosec // G301: temp folder, 0755 like extract
		return fsErr(err)
	}
	keep := false      // the swap could neither be completed nor undone: tmp holds the old state
	journaled := false // the restore journal may have an entry for this swap
	defer func() {
		if keep {
			return
		}
		// The journal covers the renames only: once nothing is out of place it goes first, then
		// tmp (a leftover without a journal entry is removed by CleanupTemp, never rolled back).
		if journaled {
			if err := s.removeJournal(id); err != nil {
				s.log.Error("files: cannot remove the restore journal entry", "id", id, "err", err)
			}
		}
		err := unlockTree(root, tmp)
		if err == nil {
			err = root.RemoveAll(tmp)
		}
		if err != nil {
			s.log.Warn("files: cannot remove the temp folder of a restore; CleanupTemp removes it at the next start", "id", id, "folder", tmp, "err", err)
		}
	}()
	if err := root.Mkdir(tmp+"/new", dirMode); err != nil { //nolint:gosec // G301: see above
		return fsErr(err)
	}
	err = s.dump(ctx, id, sid, func(r io.Reader) error {
		return extractBackupTar(ctx, root, tmp+"/new", r, s.cfg.Limits, res, func(n int64) { s.jobs.progress(id, n) })
	})
	if err != nil {
		return s.ctxErr(ctx, err)
	}
	if !s.jobs.beginCommit(ctx, id) {
		return s.jobs.stopErr()
	}
	// The journal (restorejournal.go) lets the next start finish or undo a swap this process
	// does not live to complete.
	journaled = true // set first: removeJournal accepts a missing entry
	if err := s.writeJournal(id, restoreJournal{Tmp: tmp, Phase: phaseOldMoving}); err != nil {
		return err
	}
	err = swapTop(root, tmp, s.swapHook, func(phase string) error {
		return s.writeJournal(id, restoreJournal{Tmp: tmp, Phase: phase})
	})
	if se := (*restoreStuckError)(nil); errors.As(err, &se) {
		keep = true
		s.log.Error("files: RESTORE COULD NOT BE COMPLETED: the old state of the data directory is kept in a hidden folder; the restore journal decides what the next start does",
			"id", id, "snapshot", sid, "folder", tmp, "phase", se.phase, "causes", se.Causes())
		return err
	}
	if err == nil {
		// The renames are on disk before the journal entry goes.
		if serr := syncRootDir(root, "."); serr != nil {
			keep = true // the journal (new_moving, nothing left in new/) resolves it at the next start
			s.log.Warn("files: cannot sync the data directory after a restore; the next start finishes it", "id", id, "err", serr)
		}
	}
	return err
}

// dump streams snapshot sid of server id (`restic dump <id> /data.tar`) into consume. When
// consume fails, restic is killed; when it succeeds, the rest of the stream is drained so restic
// can finish, and restic's own exit decides. A failure of restic itself wins over the damaged
// stream it causes.
func (s *Service) dump(ctx context.Context, id int64, sid string, consume func(io.Reader) error) error {
	pr, pw := io.Pipe()
	p, err := s.runner.Start(ctx, resticCmd{Args: []string{"--no-cache", "dump", sid, "/" + stdinFilename}, Env: s.resticEnv(id), Stdout: pw})
	if err != nil {
		_ = pw.Close()
		return s.startErr(err)
	}
	waited := make(chan error, 1)
	go func() {
		err := p.Wait()
		_ = pw.CloseWithError(err) // nil: EOF for the reader
		waited <- err
	}()
	cerr := consume(pr)
	if cerr == nil {
		_, cerr = io.Copy(io.Discard, pr)
	}
	if cerr != nil {
		p.Kill()
		_ = pr.CloseWithError(cerr)
	}
	werr := <-waited
	var ex *resticExit
	switch {
	case werr != nil && errors.As(werr, &ex) && ex.Code != -1:
		return s.resticErr("dump", werr)
	case cerr != nil:
		return cerr
	case werr != nil:
		return s.resticErr("dump", werr)
	}
	return nil
}

// errRestoreStuck: a swap could neither be completed nor undone (restoreStuckError).
var errRestoreStuck = errors.New("the restore could not be completed or undone; the old state of the data directory is kept in a hidden folder - see the files service log")

// restoreStuckError is errRestoreStuck with the journal phase it was left in and its causes
// (only the fixed text and the phase leave the service; the causes go to the log).
type restoreStuckError struct {
	phase      string // phaseStuck or phaseNewMoving (completed at the next start)
	swap, undo error
}

func (e *restoreStuckError) Error() string {
	return errRestoreStuck.Error() + " (restore journal: " + e.phase + ")"
}

func (e *restoreStuckError) Is(target error) bool { return target == errRestoreStuck }

// Causes describes both failures for the log.
func (e *restoreStuckError) Causes() string {
	return fmt.Sprintf("swap: %v; undo: %v", e.swap, e.undo)
}

// visibleNames lists the names of folder dir that are not hidden (spec 4 §4.3: hidden entries
// such as .rcon-cli.env and the service's temp folders stay where they are).
// The names are read in batches against the walk budget (SEC-23).
func visibleNames(root *os.Root, dir string) ([]string, error) {
	f, _, err := openDir(root, dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var b walkBudget
	var out []string
	for {
		batch, rerr := f.Readdirnames(walkBatch)
		for _, n := range batch {
			if err := b.count(n); err != nil {
				return nil, err
			}
			if !filesapi.IsHidden(n) {
				out = append(out, n)
			}
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && len(batch) == 0) {
			break
		}
		if rerr != nil {
			return nil, fsErr(rerr)
		}
	}
	slices.Sort(out)
	return out, nil
}

// swapTop replaces the visible top-level entries of the data directory by the entries of
// <tmp>/new (renames only, no copying): the current ones move to <tmp>/old first, then the new
// ones move up. Every folder involved gets u+rwx first (unlockDir), so read-only folders of the
// container do not stop it. phase records the progress in the restore journal:
//   - phaseNewMoving before the first new entry moves up, after <tmp>/old and the top level are
//     synced. From then on the journal may say new_moving, also when writing it failed.
//   - A failing rename is undone (newest first). In the new phase the journal is set to
//     phaseStuck before the undo, so a crash during the undo is not guessed at; when that write
//     fails, there is no undo: the error is errRestoreStuck and the journal still says
//     new_moving (or old_moving), which the next start completes (or undoes).
//   - An undo that fails as well is errRestoreStuck (phaseStuck in the journal).
//
// The caller keeps tmp on errRestoreStuck. hook (tests) runs before every rename with its step
// number, and before every undo with -1-<step undone>.
func swapTop(root *os.Root, tmp string, hook func(step int) error, phase func(string) error) error {
	old, nw := tmp+"/old", tmp+"/new"
	if err := root.Mkdir(old, dirMode); err != nil { //nolint:gosec // G301: temp folder
		return fsErr(err)
	}
	for _, d := range []string{".", tmp, old, nw} {
		if err := unlockDir(root, d); err != nil {
			return err
		}
	}
	cur, err := visibleNames(root, "")
	if err != nil {
		return err
	}
	next, err := visibleNames(root, nw)
	if err != nil {
		return err
	}
	type move struct{ from, to string }
	var plan []move
	for _, n := range cur {
		plan = append(plan, move{n, old + "/" + n})
	}
	for _, n := range next {
		plan = append(plan, move{nw + "/" + n, n})
	}
	// A folder that moves to another parent needs write permission itself (its ".." changes).
	for _, m := range plan {
		if err := unlockDir(root, m.from); err != nil {
			return err
		}
	}
	newPhase := false // the journal may say phaseNewMoving
	for i, m := range plan {
		var err error
		if i == len(cur) {
			err = syncRootDir(root, old)
			if err == nil {
				err = syncRootDir(root, ".")
			}
			if err == nil {
				err = phase(phaseNewMoving)
				newPhase = true // even if the write failed: it may have reached the disk
			}
		}
		if err == nil && hook != nil {
			err = hook(i)
		}
		if err == nil {
			err = root.Rename(m.from, m.to)
		}
		if err == nil {
			continue
		}
		if newPhase {
			if perr := phase(phaseStuck); perr != nil {
				return &restoreStuckError{phase: phaseNewMoving, swap: fsErr(err), undo: fmt.Errorf("no undo, the journal could not be updated: %w", perr)}
			}
		}
		var undoErr error
		for k := i - 1; k >= 0; k-- {
			var uerr error
			if hook != nil {
				uerr = hook(-1 - k)
			}
			if uerr == nil {
				uerr = root.Rename(plan[k].to, plan[k].from)
			}
			if uerr != nil && undoErr == nil {
				undoErr = uerr
			}
		}
		if undoErr != nil {
			if !newPhase {
				_ = phase(phaseStuck) // if this fails, old_moving undoes it at the next start
			}
			return &restoreStuckError{phase: phaseStuck, swap: fsErr(err), undo: undoErr}
		}
		if newPhase {
			_ = phase(phaseOldMoving) // everything is back: recovery would find nothing to move
		}
		return fmt.Errorf("swap the restored files into place: %w", fsErr(err))
	}
	return nil
}

// CancelJob cancels the running job of server id (spec 4 §4.5).
func (s *Service) CancelJob(id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: id must be > 0", filesapi.ErrInvalid)
	}
	return s.jobs.cancelJob(id)
}
