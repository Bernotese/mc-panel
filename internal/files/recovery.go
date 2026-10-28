package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// recoveryDir (below backups/) receives the folders of restores that were resolved by hand. The
// service creates it and never deletes anything in it; the operator does (docs/install.md).
const recoveryDir = "recovery"

// ResolveRecovery resolves an unfinished restore of server id (data safety spec §2.1): it moves
// the restore's temp folder (old and new halves) out of the data directory to
// backups/recovery/<id>-<unix>/ and removes the journal entry. For an entry that cannot be read
// every top-level temp folder of the data directory moves (which one it meant is unknown). The
// visible data directory is not touched. ErrNotFound when the server does not need recovery,
// ErrBusy while a job holds it.
func (s *Service) ResolveRecovery(ctx context.Context, id int64) (filesapi.Recovery, error) {
	if id <= 0 {
		return filesapi.Recovery{}, fmt.Errorf("%w: id must be > 0", filesapi.ErrInvalid)
	}
	if err := s.jobs.acquire(id, jobResolve); err != nil {
		return filesapi.Recovery{}, err
	}
	defer s.jobs.release(id)
	if err := s.jobs.drain(ctx, id); err != nil {
		return filesapi.Recovery{}, err
	}
	s.dropJournalList() // decide on the entry as it is on disk now
	e, journaled, err := s.journalEntryOf(id)
	if err != nil {
		return filesapi.Recovery{}, err
	}
	if !journaled {
		return filesapi.Recovery{}, fmt.Errorf("%w: there is no unfinished restore of this server", filesapi.ErrNotFound)
	}
	rec := e.recovery()
	root, err := s.open(id)
	switch {
	case errors.Is(err, filesapi.ErrNotFound):
		// no data directory: nothing to move
	case err != nil:
		return filesapi.Recovery{}, err
	default:
		defer func() { _ = root.Close() }()
		var temps []string
		if e.err != nil {
			temps, err = topTempDirs(root, s.cfg.Limits)
		} else {
			temps, err = existingDirs(root, e.Tmp)
		}
		if err != nil {
			return filesapi.Recovery{}, err
		}
		if len(temps) > 0 {
			if rec.Folder, err = s.moveToRecovery(root, id, temps); err != nil {
				return filesapi.Recovery{}, err
			}
		}
	}
	if err := s.removeJournal(id); err != nil {
		return filesapi.Recovery{}, fmt.Errorf("remove the restore journal entry: %w", err)
	}
	s.log.Warn("files: resolved an unfinished restore", "id", id, "phase", rec.Phase, "moved_to", rec.Folder)
	return rec, nil
}

// dropJournalList forgets the cached listing of the journal.
func (s *Service) dropJournalList() {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	s.journalList = nil
}

// existingDirs returns name if it is a real top-level folder of root, else nothing.
func existingDirs(root *os.Root, name string) ([]string, error) {
	fi, err := lstat(root, name)
	switch {
	case errors.Is(err, filesapi.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	case !fi.IsDir():
		return nil, nil
	}
	return []string{name}, nil
}

// topTempDirs lists the real folders at the top of root whose names carry the temp prefix.
func topTempDirs(root *os.Root, lim config.FilesLimits) ([]string, error) {
	f, _, err := openDir(root, "")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := newWalkBudget(lim)
	var out []string
	for {
		batch, rerr := f.Readdirnames(walkBatch)
		for _, n := range batch {
			if err := b.count(n); err != nil {
				return nil, err
			}
			if !strings.HasPrefix(n, tempPrefix) {
				continue
			}
			if fi, err := root.Lstat(n); err == nil && fi.IsDir() {
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
	return out, nil
}

// moveToRecovery moves the top-level folders temps of the data directory of server id (root)
// into a new folder backups/recovery/<id>-<unix>[-<n>] (0700) and returns its name. Each folder
// keeps its name without the leading dot, so it is not hidden there. Both trees are on the file
// system of instances_dir, so these are renames; the moved folders get u+rwx first (a folder
// that changes its parent needs it).
func (s *Service) moveToRecovery(root *os.Root, id int64, temps []string) (string, error) {
	br, ok, err := s.openBackupsRoot()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("%w: the backup directory does not exist", filesapi.ErrBackupsUnavailable)
	}
	defer func() { _ = br.Close() }()
	if err := br.Mkdir(recoveryDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	if fi, err := br.Lstat(recoveryDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s is not a real folder", recoveryDir)
	}
	base := strconv.FormatInt(id, 10) + "-" + strconv.FormatInt(s.clock.Now().Unix(), 10)
	name := base
	for n := 2; ; n++ {
		err := br.Mkdir(recoveryDir+"/"+name, 0o700)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || n > 100 {
			return "", err
		}
		name = base + "-" + strconv.Itoa(n)
	}
	if err := unlockDir(root, "."); err != nil {
		return "", err
	}
	dest := filepath.Join(s.backupsDir(), recoveryDir, name)
	for _, t := range temps {
		if err := unlockDir(root, t); err != nil {
			return "", err
		}
		// root.Rename cannot leave the root: plain paths. The data directory itself was checked
		// by open; the last component is renamed as it is (a symlink swapped in would move as a
		// link and is never followed there).
		from := filepath.Join(s.instanceDir(id), t)
		to := filepath.Join(dest, strings.TrimPrefix(t, "."))
		if err := os.Rename(from, to); err != nil { //nolint:gosec // G703: below <instances_dir>, built from an int64 id and checked top-level temp names
			return "", fmt.Errorf("move %s to the recovery folder: %w", t, err)
		}
	}
	for _, d := range []string{recoveryDir + "/" + name, recoveryDir} {
		if err := syncRootDir(br, d); err != nil {
			return "", err
		}
	}
	if err := syncRootDir(root, "."); err != nil {
		return "", err
	}
	return name, nil
}
