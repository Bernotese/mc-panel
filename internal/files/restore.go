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
	tmp, err := s.tempName("")
	if err != nil {
		return err
	}
	if err := root.Mkdir(tmp, dirMode); err != nil { //nolint:gosec // G301: temp folder, 0755 like extract
		return fsErr(err)
	}
	defer func() { _ = root.RemoveAll(tmp) }()              // after a swap only the old entries are left in it
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
	return swapTop(root, tmp, s.swapHook)
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
		return fmt.Errorf("start restic: %w", err)
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

// visibleNames lists the names of folder dir that are not hidden (spec 4 §4.3: hidden entries
// such as .rcon-cli.env and the service's temp folders stay where they are).
func visibleNames(root *os.Root, dir string) ([]string, error) {
	f, _, err := openDir(root, dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	all, err := f.Readdirnames(-1)
	if err != nil {
		return nil, fsErr(err)
	}
	out := slices.DeleteFunc(all, func(n string) bool { return filesapi.IsHidden(n) })
	slices.Sort(out)
	return out, nil
}

// swapTop replaces the visible top-level entries of the data directory by the entries of
// <tmp>/new (renames only, no copying): the current ones move to <tmp>/old first, then the new
// ones move up. A failing rename undoes the renames done so far, newest first (best effort).
// hook (tests) runs before every rename with its step number.
func swapTop(root *os.Root, tmp string, hook func(step int) error) error {
	old, nw := tmp+"/old", tmp+"/new"
	if err := root.Mkdir(old, dirMode); err != nil { //nolint:gosec // G301: temp folder
		return fsErr(err)
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
	for i, m := range plan {
		var err error
		if hook != nil {
			err = hook(i)
		}
		if err == nil {
			err = root.Rename(m.from, m.to)
		}
		if err != nil {
			for k := i - 1; k >= 0; k-- {
				_ = root.Rename(plan[k].to, plan[k].from)
			}
			return fmt.Errorf("swap the restored files into place: %w", fsErr(err))
		}
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
