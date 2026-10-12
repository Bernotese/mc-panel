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
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const (
	tempPrefix       = ".mcpanel-tmp-"
	maxUploads       = 4
	maxZipDownloads  = 4  // folder zips: each holds its central directory in memory (spec §4.1)
	maxFileDownloads = 16 // single files
	maxJobsTotal     = 2
	cleanupMaxDepth  = 64
	maxHashRuns      = 2    // concurrent jar checksum and metadata passes (spec 3c §5.1)
	hashCacheSize    = 4096 // cached jars (spec 3c §5.1)
	fileMode         = 0o644
	dirMode          = 0o755
	serverProps      = "server.properties"
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
	cfg           Config
	clock         clock.Clock
	log           *slog.Logger
	disk          DiskSpace
	uploads       chan struct{} // at most maxUploads concurrent uploads
	zipDownloads  chan struct{} // at most maxZipDownloads concurrent folder zips
	fileDownloads chan struct{} // at most maxFileDownloads concurrent file downloads
	jobs          *jobs
	hashSlots     chan struct{}     // at most maxHashRuns jar passes at a time, for all requests
	jarCache      *hashCache        // checksums and metadata by file identity
	jarHashMax    int64             // larger jars get no checksums (filesapi.MaxJarHashBytes; tests lower it)
	jarHook       func(name string) // tests: runs before every jar that is computed, not cached
	// tempToken is part of every temp name this process creates, so CleanupTemp, which runs
	// while the service already serves, removes only the leftovers of earlier runs.
	tempToken string
	// Host paths at the start of a path in a message (publicMessage): inside a data directory,
	// a data directory itself, and instances_dir.
	dataPathRe, dataDirRe, baseDirRe *regexp.Regexp

	mu      sync.Mutex
	pending int64 // bytes announced by running writes, uploads and extract jobs, not yet on disk
}

func New(cfg Config) *Service {
	s := &Service{
		cfg: cfg, clock: cfg.Clock, log: cfg.Log, disk: cfg.Disk, jobs: newJobs(),
		uploads:       make(chan struct{}, maxUploads),
		zipDownloads:  make(chan struct{}, maxZipDownloads),
		fileDownloads: make(chan struct{}, maxFileDownloads),
		hashSlots:     make(chan struct{}, maxHashRuns),
		jarCache:      newHashCache(hashCacheSize),
		jarHashMax:    filesapi.MaxJarHashBytes,
		tempToken:     randomHex(4),
	}
	instances := regexp.QuoteMeta(filepath.Join(cfg.InstancesDir, "instances") + string(filepath.Separator))
	s.dataPathRe = regexp.MustCompile(pathStart + instances + `[0-9]+/`)
	s.dataDirRe = regexp.MustCompile(pathStart + instances + `[0-9]+\b`)
	if len(cfg.InstancesDir) > 1 {
		s.baseDirRe = regexp.MustCompile(pathStart + regexp.QuoteMeta(cfg.InstancesDir))
	}
	s.jobs.scrub = s.publicMessage
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

// Close cancels running extract jobs (they end as failed and remove their temp folders) and
// waits for them. It may be called more than once.
func (s *Service) Close() { s.jobs.close() }

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

// openRegular opens a regular file for reading without following symlinks: Lstat first
// (lstat refuses symlinked folder components), then openChecked. Only O_RDONLY is allowed:
// os.Root.OpenFile resolves a symlink in the last component itself (inside the root) unless
// O_CREATE|O_EXCL is given, so a writing open could act on a swapped-in link before the
// SameFile check sees it.
func openRegular(root *os.Root, clean string, flag int) (*os.File, fs.FileInfo, error) {
	if flag != os.O_RDONLY {
		return nil, nil, fmt.Errorf("openRegular: flag %#x is not O_RDONLY", flag)
	}
	fi, err := lstat(root, clean)
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, rootName(clean))
	}
	return openChecked(root, clean, fi, os.O_RDONLY)
}

// openDir opens a real folder for reading its entries, like openRegular (with O_DIRECTORY).
func openDir(root *os.Root, clean string) (*os.File, fs.FileInfo, error) {
	fi, err := lstat(root, clean)
	if err != nil {
		return nil, nil, err
	}
	if !fi.IsDir() {
		return nil, nil, fmt.Errorf("%w: %s is not a folder", filesapi.ErrNotRegular, rootName(clean))
	}
	return openChecked(root, clean, fi, os.O_RDONLY|syscall.O_DIRECTORY)
}

// openChecked opens clean with O_NOFOLLOW|O_NONBLOCK (a FIFO swapped in after the Lstat cannot
// block) and accepts the result only if it is the very object fi describes (same type, same
// inode): a symlink or anything else swapped in meanwhile ends as ErrNotRegular.
func openChecked(root *os.Root, clean string, fi fs.FileInfo, flag int) (*os.File, fs.FileInfo, error) {
	f, err := root.OpenFile(rootName(clean), flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: cleaned path inside the instance's os.Root
	if errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) {
		return nil, nil, fmt.Errorf("%w: %s changed while it was opened", filesapi.ErrNotRegular, rootName(clean))
	}
	if err != nil {
		return nil, nil, fsErr(err)
	}
	st, err := f.Stat()
	if err != nil || st.Mode().Type() != fi.Mode().Type() || !os.SameFile(fi, st) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%w: %s changed while it was opened", filesapi.ErrNotRegular, rootName(clean))
	}
	return f, st, nil
}

// randomHex returns n random bytes as hex.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // never fails (crypto/rand panics instead)
	return hex.EncodeToString(b)
}

// tempName returns a fresh temp name inside folder dir: .mcpanel-tmp-<process token>-<random>.
func (s *Service) tempName(dir string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return filesapi.Join(dir, tempPrefix+s.tempToken+"-"+hex.EncodeToString(b[:])), nil
}

// pathStart matches where a path can start in a message: its beginning, after white space,
// a quote, a parenthesis or '='. A user's name that merely contains the host path is left alone.
const pathStart = `(^|[\s"'(=])`

// publicMessage removes host paths from a message that leaves the service (error answers and
// job errors; spec §1.2: the panel and its users never learn the host layout). Files opened
// through an os.Root carry the absolute path of the data directory in their errors: the part
// up to the instance id is cut, so "write <instances_dir>/instances/1/world/x: …" becomes
// "write world/x: …" and the data directory itself becomes ".". Any other path starting with
// instances_dir gets "<instances_dir>" instead. Only paths at a path start (pathStart) are
// replaced. The log keeps the full message.
func (s *Service) publicMessage(msg string) string {
	msg = s.dataPathRe.ReplaceAllString(msg, "$1")
	msg = s.dataDirRe.ReplaceAllString(msg, "$1.")
	if s.baseDirRe != nil {
		msg = s.baseDirRe.ReplaceAllString(msg, "$1<instances_dir>")
	}
	return msg
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
		errors.Is(err, filesapi.ErrInsufficientStorage), errors.Is(err, filesapi.ErrBusy), errors.Is(err, filesapi.ErrInvalid),
		errors.Is(err, filesapi.ErrHashMismatch):
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

// reserve checks that need more bytes still leave the configured reserve free, counting the
// bytes that running operations announced but have not written yet (all data directories
// share one file system). The caller reports written bytes with landed and must release the
// reservation when it ends.
func (s *Service) reserve(id, need int64) (*reservation, error) {
	need = max(need, 0)
	avail, err := s.disk.Available(s.instanceDir(id))
	if err != nil {
		return nil, fmt.Errorf("check free space: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	want := uint64(need) + uint64(max(s.cfg.Limits.ReserveBytes, 0)) + uint64(s.pending)
	if avail < want {
		return nil, fmt.Errorf("%w: %d bytes free, %d bytes needed including the reserve and running operations",
			filesapi.ErrInsufficientStorage, avail, want)
	}
	s.pending += need
	return &reservation{s: s, left: need}, nil
}

// reservation is disk space announced by one running operation.
type reservation struct {
	s    *Service
	left int64 // guarded by s.mu
}

// landed reports n bytes written to disk: statfs shows them now, so they leave the reservation.
func (r *reservation) landed(n int64) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	d := min(max(n, 0), r.left)
	r.left -= d
	r.s.pending -= d
}

// release returns what is left of the reservation; it may be called more than once.
func (r *reservation) release() {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	r.s.pending -= r.left
	r.left = 0
}

// landingWriter reports every write to a reservation.
type landingWriter struct {
	w   io.Writer
	res *reservation
}

func (l landingWriter) Write(p []byte) (int, error) {
	n, err := l.w.Write(p)
	l.res.landed(int64(n))
	return n, err
}
