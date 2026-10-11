package files

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CleanupTemp removes leftover temp files and folders (".mcpanel-tmp-*", up to depth 64) of
// every instance; an aborted upload, write or extract never survives a restart. It runs once
// before the socket accepts requests and returns the number of removed entries. Problems of
// single instances are logged and skipped.
func (s *Service) CleanupTemp() int {
	entries, err := os.ReadDir(filepath.Join(s.cfg.InstancesDir, "instances"))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("files: cannot list instances for temp cleanup", "err", err)
		}
		return 0
	}
	removed := 0
	for _, e := range entries {
		id, err := strconv.ParseInt(e.Name(), 10, 64)
		if err != nil || id <= 0 || !e.IsDir() {
			continue
		}
		root, err := s.open(id)
		if err != nil {
			s.log.Warn("files: temp cleanup skipped an instance", "id", id, "err", err)
			continue
		}
		removed += cleanupRoot(root, s.log, id)
		_ = root.Close()
	}
	return removed
}

// cleanupRoot walks one data directory without following symlinks (fs.WalkDir only descends
// into real folders) and removes every temp entry it finds.
func cleanupRoot(root *os.Root, log *slog.Logger, id int64) int {
	var victims []string
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // unreadable parts are skipped, the rest is still cleaned up
		}
		if p != "." && strings.HasPrefix(d.Name(), tempPrefix) {
			victims = append(victims, p)
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() && p != "." && strings.Count(p, "/") >= cleanupMaxDepth-1 {
			return fs.SkipDir
		}
		return nil
	})
	n := 0
	for _, v := range victims {
		if err := root.RemoveAll(v); err != nil {
			log.Warn("files: cannot remove a leftover temp entry", "id", id, "path", v, "err", err)
			continue
		}
		n++
	}
	return n
}
