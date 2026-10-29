package files

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// CleanupTemp removes leftover temp files and folders (".mcpanel-tmp-*", up to depth 64) of
// every instance; an aborted upload, write or extract never survives a restart. It runs once at
// startup, in the background while the service already serves (a large instances_dir must not
// delay the answers the panel waits for), so it leaves the temp entries of this process alone
// (tempToken). It stops early when ctx ends and returns the number of removed entries.
// Problems of single instances are logged and skipped.
func (s *Service) CleanupTemp(ctx context.Context) int {
	entries, err := os.ReadDir(filepath.Join(s.cfg.InstancesDir, "instances"))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("files: cannot list instances for temp cleanup", "err", err)
		}
		return 0
	}
	// RecoverRestores ran before: only restores that need recovery are left in the journal.
	keep := s.journaledTemps()
	removed := 0
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		id, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil || id <= 0 || strconv.FormatInt(id, 10) != e.Name() || !e.IsDir() {
			continue // only canonical names are data directories (instanceDir)
		}
		root, err := s.open(id)
		if err != nil {
			s.log.Warn("files: temp cleanup skipped an instance", "id", id, "err", err)
			continue
		}
		removed += s.cleanupRoot(ctx, root, id, func(p string) bool { return keep.keeps(id, p) })
		_ = root.Close()
	}
	return removed
}

// cleanupRoot walks one data directory without following symlinks (walkTree) and removes
// every temp entry of an earlier run it finds except those keep reports (the top-level temp
// folders a restore journal entry may refer to: tempKeep); a temp-named symlink is removed as a
// link. A temp folder without a journal entry is removed whatever it holds, never rolled back: a
// container can fake one.
// Leftovers are removed per batch while the walk goes on; a data directory with more names
// than the walk budget is left with a warning (SEC-23).
func (s *Service) cleanupRoot(ctx context.Context, root *os.Root, id int64, keep func(p string) bool) int {
	var victims []string
	n := 0
	flush := func() {
		for _, v := range victims {
			if ctx.Err() != nil {
				break
			}
			if keep(v) {
				continue // a restore that needs recovery: the journal entry protects its folder
			}
			// The container may have made the leftover or its folder read-only (unlockTree).
			err := unlockDir(root, rootName(parentOf(v)))
			if err == nil {
				err = unlockTree(root, v, s.cfg.Limits)
			}
			if err == nil {
				err = root.RemoveAll(v)
			}
			if err != nil {
				s.log.Warn("files: cannot remove a leftover temp entry", "id", id, "path", v, "err", err)
				continue
			}
			n++
		}
		victims = victims[:0]
	}
	own := tempPrefix + s.tempToken + "-"
	err := walkTree(root, "", newWalkBudget(s.cfg.Limits), func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			s.log.Debug("files: temp cleanup skipped a folder", "id", id, "path", p, "err", err)
			return nil
		}
		if strings.HasPrefix(d.Name(), own) {
			return fs.SkipDir // a running upload, write or extract of this process
		}
		if strings.HasPrefix(d.Name(), tempPrefix) {
			if victims = append(victims, p); len(victims) >= walkBatch {
				flush()
			}
			return fs.SkipDir
		}
		if d.IsDir() && strings.Count(p, "/") >= cleanupMaxDepth-1 {
			return fs.SkipDir
		}
		return nil
	})
	switch {
	case ctx.Err() != nil:
		return n
	case err != nil:
		s.log.Warn("files: temp cleanup skipped the rest of an instance", "id", id, "err", err)
		return n
	}
	flush()
	return n
}

// maxUnlockDepth bounds how deep unlockTree descends (one open folder per level).
const maxUnlockDepth = 512

// unlockDir gives the service's own user rwx on folder p of root (the service owns every folder
// in a data directory; a container running as the same user may have made one read-only, which
// would stop a rename or removal). Anything but a folder is left alone.
func unlockDir(root *os.Root, p string) error {
	fi, err := lstat(root, cleanRoot(p))
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode().Perm()&0o700 == 0o700 {
		return nil
	}
	if err := root.Chmod(rootName(cleanRoot(p)), fi.Mode().Perm()|0o700); err != nil {
		return fsErr(err)
	}
	return nil
}

// cleanRoot maps "." to "" (the root for lstat).
func cleanRoot(p string) string {
	if p == "." {
		return ""
	}
	return p
}

// unlockTree is unlockDir for folder p and every folder below it, so the whole tree can be
// removed. It never follows a symlink (one *os.Root per level, openSubdir); a missing p is fine.
// Only folders count against the walk budget of lim: files cost no memory here, and a temp
// folder full of files must stay removable (RemoveAll reads in batches itself).
func unlockTree(root *os.Root, p string, lim config.FilesLimits) error {
	fi, err := lstat(root, p)
	if errors.Is(err, filesapi.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return nil
	}
	if err := unlockDir(root, p); err != nil {
		return err
	}
	sub, err := openSubdir(root, p, fi)
	if err != nil {
		return err
	}
	defer func() { _ = sub.Close() }()
	return unlockBelow(sub, 1, newWalkBudget(lim))
}

// unlockBelow unlocks the folders below d. Names are read in batches; the folders among them
// count against the walk budget b and only their names are kept until the listing is closed.
func unlockBelow(d *os.Root, depth int, b *walkBudget) error {
	if depth > maxUnlockDepth {
		return nil // RemoveAll fails below; the error is logged by the caller
	}
	f, err := d.Open(".")
	if err != nil {
		return fsErr(err)
	}
	var subdirs []string
	for {
		batch, rerr := f.Readdirnames(walkBatch)
		for _, n := range batch {
			fi, err := d.Lstat(n)
			if err != nil || !fi.IsDir() {
				continue
			}
			if err := b.count(n); err != nil {
				_ = f.Close()
				return err
			}
			if fi.Mode().Perm()&0o700 != 0o700 {
				if err := d.Chmod(n, fi.Mode().Perm()|0o700); err != nil {
					_ = f.Close()
					return fsErr(err)
				}
			}
			subdirs = append(subdirs, n)
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && len(batch) == 0) {
			break
		}
		if rerr != nil {
			_ = f.Close()
			return fsErr(rerr)
		}
	}
	_ = f.Close()
	for _, n := range subdirs {
		fi, err := d.Lstat(n)
		if err != nil || !fi.IsDir() {
			continue // gone or swapped meanwhile
		}
		sub, err := openSubdir(d, n, fi)
		if err != nil {
			continue // swapped meanwhile: RemoveAll removes a symlink as a link
		}
		err = unlockBelow(sub, depth+1, b)
		_ = sub.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
