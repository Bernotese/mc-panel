// Package files implements mcpanel-files, the unprivileged service behind the file browser
// (spec docs/superpowers/specs/2026-10-11-mc-panel-files-design.md). It runs as the user that
// owns the Minecraft data (mcpanel-mc, never root); every request works on exactly one
// instance data directory through an *os.Root and never follows a symlink.
package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	tempPrefix      = ".mcpanel-tmp-"
	cleanupMaxDepth = 64
)

// Config configures a Service.
type Config struct {
	InstancesDir string             // <instances_dir>; data dirs are <InstancesDir>/instances/<id>
	Limits       config.FilesLimits // spec §3.1
	Dev          bool               // make dev: create missing data directories
	Clock        clock.Clock        // nil = clock.Real{}
	Log          *slog.Logger       // nil = slog.Default()
	Disk         DiskSpace          // nil = statfs
}

// Service performs the file operations of all instances.
type Service struct {
	cfg   Config
	clock clock.Clock
	log   *slog.Logger
	disk  DiskSpace
}

func New(cfg Config) *Service {
	s := &Service{cfg: cfg, clock: cfg.Clock, log: cfg.Log, disk: cfg.Disk}
	if s.clock == nil {
		s.clock = clock.Real{}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.disk == nil {
		s.disk = statfsDisk{}
	}
	return s
}

// Close releases background work (extract jobs, Task 7).
func (s *Service) Close() {}

func (s *Service) instanceDir(id int64) string {
	return filepath.Join(s.cfg.InstancesDir, "instances", strconv.FormatInt(id, 10))
}

// open returns an *os.Root on the data directory of server id. The directory itself must be
// a real directory (a symlink is refused even if it points to another instance).
func (s *Service) open(id int64) (*os.Root, error) {
	if id <= 0 {
		return nil, fmt.Errorf("%w: id must be > 0", filesapi.ErrInvalid)
	}
	dir := s.instanceDir(id)
	if s.cfg.Dev {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: server %d has no data directory yet", filesapi.ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() { // Lstat: a symlink is not a directory here
		return nil, fmt.Errorf("data directory %s is not a real directory", dir)
	}
	root, err := os.OpenRoot(dir) //nolint:gosec // G304: <instances_dir>/instances/<id>, id is an int64 > 0
	if err != nil {
		return nil, err
	}
	st, err := root.Stat(".")
	if err != nil || !os.SameFile(fi, st) {
		_ = root.Close()
		return nil, fmt.Errorf("data directory %s changed while it was opened", dir)
	}
	return root, nil
}

// cleanVisible validates p (filesapi.CleanPath) and treats hidden names as missing.
func cleanVisible(p string) (string, error) {
	clean, err := filesapi.CleanPath(p)
	if err != nil {
		return "", err
	}
	if filesapi.IsHidden(clean) {
		return "", fmt.Errorf("%w: %s", filesapi.ErrNotFound, clean)
	}
	return clean, nil
}

// parentOf returns the parent of a cleaned path ("" for top-level names and the root).
func parentOf(clean string) string {
	i := strings.LastIndexByte(clean, '/')
	if i < 0 {
		return ""
	}
	return clean[:i]
}

// rootName turns the cleaned root "" into "." for os.Root calls and messages.
func rootName(clean string) string {
	if clean == "" {
		return "."
	}
	return clean
}

// lstat returns the Lstat info of clean after checking that every folder component on the
// way is a real directory, never a symlink (os.Root itself would follow symlinks that stay
// inside the root). "" is the root folder.
func lstat(root *os.Root, clean string) (fs.FileInfo, error) {
	if clean == "" {
		return root.Stat(".")
	}
	parts := strings.Split(clean, "/")
	for i := 1; i < len(parts); i++ {
		p := strings.Join(parts[:i], "/")
		fi, err := root.Lstat(p)
		if err != nil {
			return nil, fsErr(err)
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("%w: %s is not a folder", filesapi.ErrNotRegular, p)
		}
	}
	fi, err := root.Lstat(clean)
	if err != nil {
		return nil, fsErr(err)
	}
	return fi, nil
}

// requireDir checks that clean exists and is a real folder.
func requireDir(root *os.Root, clean string) error {
	fi, err := lstat(root, clean)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a folder", filesapi.ErrNotRegular, rootName(clean))
	}
	return nil
}

// openRegular opens a regular file without following symlinks: Lstat first, then
// O_NOFOLLOW|O_NONBLOCK (a FIFO swapped in meanwhile cannot block), then the opened file
// must be the very file that Lstat saw.
func openRegular(root *os.Root, clean string, flag int) (*os.File, fs.FileInfo, error) {
	fi, err := lstat(root, clean)
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, rootName(clean))
	}
	f, err := root.OpenFile(clean, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: cleaned path inside the instance's os.Root
	if err != nil {
		return nil, nil, fsErr(err)
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || !os.SameFile(fi, st) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%w: %s changed while it was opened", filesapi.ErrNotRegular, clean)
	}
	return f, st, nil
}

// tempName returns a fresh temp name inside folder dir.
func tempName(dir string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return filesapi.Join(dir, tempPrefix+hex.EncodeToString(b[:])), nil
}

// commit moves the finished temp file tmp to clean. With overwrite it renames over clean;
// without, it hard-links (fails atomically if clean exists meanwhile) and removes tmp.
func commit(root *os.Root, tmp, clean string, overwrite bool) error {
	if overwrite {
		return fsErr(root.Rename(tmp, clean))
	}
	if err := root.Link(tmp, clean); err != nil {
		return fsErr(err)
	}
	_ = root.Remove(tmp) // a leftover is removed at the next start (CleanupTemp)
	return nil
}

// fsErr maps file system errors to the filesapi sentinels; other errors stay internal.
func fsErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, filesapi.ErrNotFound), errors.Is(err, filesapi.ErrExists), errors.Is(err, filesapi.ErrModified),
		errors.Is(err, filesapi.ErrNotRegular), errors.Is(err, filesapi.ErrNotText), errors.Is(err, filesapi.ErrTooLarge),
		errors.Is(err, filesapi.ErrInsufficientStorage), errors.Is(err, filesapi.ErrBusy), errors.Is(err, filesapi.ErrInvalid):
		return err
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return fmt.Errorf("%w: %w", filesapi.ErrNotFound, err)
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%w: %w", filesapi.ErrExists, err)
	case errors.Is(err, syscall.EISDIR), errors.Is(err, syscall.ELOOP):
		return fmt.Errorf("%w: %w", filesapi.ErrNotRegular, err)
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return fmt.Errorf("%w: %w", filesapi.ErrInsufficientStorage, err)
	}
	return err
}

// reserve checks that need more bytes still leave the configured reserve free.
func (s *Service) reserve(id, need int64) error {
	avail, err := s.disk.Available(s.instanceDir(id))
	if err != nil {
		return fmt.Errorf("check free space: %w", err)
	}
	want := uint64(max(need, 0)) + uint64(max(s.cfg.Limits.ReserveBytes, 0))
	if avail < want {
		return fmt.Errorf("%w: %d bytes free, %d bytes needed including the reserve", filesapi.ErrInsufficientStorage, avail, want)
	}
	return nil
}
