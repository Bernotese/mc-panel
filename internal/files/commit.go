package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// commitLocks serializes the moments at which plain mutations change the tree of one data
// directory (#149): an editor save checks its base again and replaces the file under it, an
// upload replaces its target, Mkdir creates and Rename checks and moves under it, and Delete
// moves its target out of the way under it (the removal itself runs outside). The lock covers
// only the service's own requests: a running Minecraft server writes its files itself.
type commitLocks struct {
	mu sync.Mutex
	m  map[int64]*commitLock
}

type commitLock struct {
	ch   chan struct{} // holds one token while the lock is taken
	refs int           // guarded by commitLocks.mu
}

// lock takes the commit lock of instance id; unlock gives it back. It gives up when ctx ends
// while it waits, and never returns the lock to a caller whose ctx has ended: an operation
// whose caller gave up (the panel's deadline) does not commit afterwards.
func (c *commitLocks) lock(ctx context.Context, id int64) (unlock func(), err error) {
	c.mu.Lock()
	if c.m == nil {
		c.m = map[int64]*commitLock{}
	}
	l := c.m[id]
	if l == nil {
		l = &commitLock{ch: make(chan struct{}, 1)}
		c.m[id] = l
	}
	l.refs++
	c.mu.Unlock()
	put := func() {
		c.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(c.m, id)
		}
		c.mu.Unlock()
	}
	select {
	case l.ch <- struct{}{}:
	case <-ctx.Done():
		put()
		return nil, ctx.Err()
	}
	unlock = func() {
		<-l.ch
		put()
	}
	if err := ctx.Err(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

// versionOf is the version token of a file (filesapi.ReadResponse.Version): a hash of device,
// inode, size, mtime and ctime in ns where the platform reports them, else size and mtime. A
// replacing rename changes the inode and the ctime, so every commit changes it.
func versionOf(fi fs.FileInfo) string {
	h := sha256.New()
	if k, ok := fileKeyOf(fi); ok {
		_, _ = fmt.Fprintf(h, "%d/%d/%d/%d/%d", k.dev, k.ino, k.size, k.mtime, k.ctime)
	} else {
		_, _ = fmt.Fprintf(h, "-/%d/%d", fi.Size(), fi.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}

// checkBase checks the current state of clean against the base of a save: with neither an
// expected mtime nor version it must not exist (ErrExists), otherwise it must be a regular
// file matching each one that is given (ErrModified). fi is nil when clean does not exist.
func checkBase(root *os.Root, clean string, req filesapi.WriteRequest) (fs.FileInfo, error) {
	update := req.ExpectedMTime != nil || req.ExpectedVersion != nil
	fi, err := lstat(root, clean)
	switch {
	case errors.Is(err, filesapi.ErrNotFound):
		if update {
			return nil, fmt.Errorf("%w: %s was deleted meanwhile", filesapi.ErrModified, clean)
		}
		return nil, nil
	case err != nil:
		return nil, err
	case !fi.Mode().IsRegular():
		return nil, fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, clean)
	case !update:
		return nil, fmt.Errorf("%w: %s", filesapi.ErrExists, clean)
	case req.ExpectedMTime != nil && *req.ExpectedMTime != fi.ModTime().UnixMilli(),
		req.ExpectedVersion != nil && *req.ExpectedVersion != versionOf(fi):
		return nil, fmt.Errorf("%w: %s was changed meanwhile", filesapi.ErrModified, clean)
	}
	return fi, nil
}

// moveMTimeForward gives the staged file tmp (mtime staged) an mtime at least one millisecond
// after the file cur it replaces, so a save based on cur's millisecond mtime cannot pass
// after this commit even when both writes fall into the same millisecond.
func moveMTimeForward(root *os.Root, tmp string, staged time.Time, cur fs.FileInfo) error {
	old := cur.ModTime().UnixMilli()
	if staged.UnixMilli() > old {
		return nil
	}
	return fsErr(root.Chtimes(tmp, time.Time{}, time.UnixMilli(old+1)))
}

// noReplaceDisabled makes renameNoReplace use its fallback (tests).
var noReplaceDisabled bool

// renameNoReplace moves from to to (cleaned paths inside root) and fails with ErrExists if to
// exists, atomically where the kernel offers it (Linux: renameat2 RENAME_NOREPLACE, macOS:
// renameatx_np RENAME_EXCL) on the parent folders opened like openDir, so no symlink is
// followed. Elsewhere, and on a file system that refuses the flag, it checks and renames: safe
// against the service's own requests because the caller excludes them (Rename and Delete hold
// the commit lock, an extract has drained every lease), not against another process creating
// to in between.
func (s *Service) renameNoReplace(root *os.Root, from, to string) error {
	if !noReplaceDisabled {
		err := renameAtNoReplace(root, from, to)
		if err == nil || !noReplaceUnsupported(err) {
			return fsErr(err)
		}
		s.noReplaceOnce.Do(func() {
			s.log.Warn("files: the file system does not support an atomic no-replace rename; renames check the target first", "err", err)
		})
	}
	if _, err := lstat(root, to); err == nil {
		return fmt.Errorf("%w: %s", filesapi.ErrExists, to)
	} else if !errors.Is(err, filesapi.ErrNotFound) {
		return err
	}
	return fsErr(root.Rename(from, to))
}

func renameAtNoReplace(root *os.Root, from, to string) error {
	fd, _, err := openDir(root, parentOf(from))
	if err != nil {
		return err
	}
	defer fd.Close()
	td, _, err := openDir(root, parentOf(to))
	if err != nil {
		return err
	}
	defer td.Close()
	return renameat(fd, baseOf(from), td, baseOf(to))
}

// noReplaceUnsupported reports whether renameat failed because the platform or the file
// system lacks the no-replace flag. EINVAL also means "a folder into itself": the fallback's
// rename then fails the same way.
func noReplaceUnsupported(err error) bool {
	return errors.Is(err, errors.ErrUnsupported) || errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.ENOTSUP)
}

// baseOf returns the last element of a cleaned path.
func baseOf(clean string) string {
	return clean[strings.LastIndexByte(clean, '/')+1:]
}
