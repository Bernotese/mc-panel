package files

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
		removed += s.cleanupRoot(ctx, root, id)
		_ = root.Close()
	}
	return removed
}

// cleanupRoot walks one data directory without following symlinks (walkTree) and removes
// every temp entry of an earlier run it finds; a temp-named symlink is removed as a link.
func (s *Service) cleanupRoot(ctx context.Context, root *os.Root, id int64) int {
	var victims []string
	own := tempPrefix + s.tempToken + "-"
	err := walkTree(root, "", func(p string, d fs.DirEntry, err error) error {
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
			victims = append(victims, p)
			return fs.SkipDir
		}
		if d.IsDir() && strings.Count(p, "/") >= cleanupMaxDepth-1 {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return 0 // ctx ended
	}
	n := 0
	for _, v := range victims {
		if ctx.Err() != nil {
			break
		}
		if parentOf(v) == "" && s.keepRestoreLeftover(root, id, v) {
			continue
		}
		if err := root.RemoveAll(v); err != nil {
			s.log.Warn("files: cannot remove a leftover temp entry", "id", id, "path", v, "err", err)
			continue
		}
		n++
	}
	return n
}

// keepRestoreLeftover looks at a top-level temp folder tmp of an earlier run that holds old/: a
// restore that stopped inside its swap (spec 4 §4.3), with the entries of the previous state in
// old/. When old/ is empty, nothing was moved yet and tmp goes like any leftover (false). When
// the top level has no visible entries (the run died after moving the previous state to old/
// and before a restored entry moved up), the previous state is moved back and tmp goes (false).
// Otherwise nothing is guessed: tmp stays where it is, with a log line, for a person to look at
// (true).
func (s *Service) keepRestoreLeftover(root *os.Root, id int64, tmp string) bool {
	old := tmp + "/old"
	if fi, err := lstat(root, old); err != nil || !fi.IsDir() {
		return false // no swap started
	}
	prev, err := visibleNames(root, old)
	if err != nil {
		s.log.Error("files: cannot read the leftover of an interrupted restore; it is kept", "id", id, "folder", tmp, "err", err)
		return true
	}
	if len(prev) == 0 {
		return false
	}
	top, err := visibleNames(root, "")
	if err != nil {
		s.log.Error("files: cannot list a data directory with the leftover of an interrupted restore; it is kept", "id", id, "folder", tmp, "err", err)
		return true
	}
	if len(top) > 0 {
		s.log.Error("files: an interrupted restore left the previous state of the data directory in a hidden folder; it is kept, check it by hand",
			"id", id, "folder", tmp, "entries", len(prev))
		return true
	}
	for _, n := range prev {
		if err := root.Rename(old+"/"+n, n); err != nil {
			s.log.Error("files: cannot put back the previous state after an interrupted restore; the rest is kept", "id", id, "folder", tmp, "entry", n, "err", err)
			return true
		}
	}
	s.log.Warn("files: put back the previous state after an interrupted restore", "id", id, "entries", len(prev))
	return false
}
