package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const walkBatch = 256

// The budget of one walk over data a Minecraft server controls (walkTree, unlockTree,
// visibleNames): the names counted (unlockTree counts only folders, the only names it keeps)
// end the walk with ErrTooLarge above it instead of exhausting the service's memory (SEC-23).
// newWalkBudget raises the count with the configured limits. Variables so tests can lower
// them.
var (
	walkMaxNames     int64 = 1_000_000
	walkMaxNameBytes int64 = 256 << 20
)

// walkMargin is added to twice the configured entry limits (newWalkBudget; a variable so
// tests can lower it).
var walkMargin int64 = 10_000

// walkBudget counts the names one walk has read.
type walkBudget struct{ names, bytes, maxNames, maxBytes int64 }

// newWalkBudget returns the budget of one walk: walkMaxNames names, but at least twice the
// larger of max_backup_entries and max_extract_entries plus walkMargin, so a data directory
// that a backup accepts, and the temp folder of a restore (old/ and new/, each within
// max_backup_entries) or of an extract, always fit and are never left behind for good. Name
// bytes likewise cover twice the name bytes of a backup stream.
func newWalkBudget(lim config.FilesLimits) *walkBudget {
	return &walkBudget{
		maxNames: max(walkMaxNames, 2*max(lim.MaxBackupEntries, lim.MaxExtractEntries)+walkMargin),
		maxBytes: max(walkMaxNameBytes, 2*maxBackupNameBytes),
	}
}

func (b *walkBudget) count(name string) error {
	b.names++
	b.bytes += int64(len(name))
	if b.names > b.maxNames || b.bytes > b.maxBytes {
		return fmt.Errorf("%w: more than %d names or %d bytes of names in one folder tree",
			filesapi.ErrTooLarge, b.maxNames, b.maxBytes)
	}
	return nil
}

// walkTree visits every entry below folder dir ("" = the root), like fs.WalkDir but safe on
// data a Minecraft server controls: folders are read through openDir, so a symlink is never
// followed and a FIFO swapped in for a folder cannot block, and at most one folder is open at
// a time (subfolders are visited after their parent was read and closed).
//
// fn gets the cleaned path and the entry. For a folder, fs.SkipDir skips its content (for
// other entries it is ignored). A folder that cannot be read (also dir itself) is reported as
// fn(path, nil, err); returning nil or fs.SkipDir skips it, any other error ends the walk.
// More names than the walk budget b end it with ErrTooLarge.
func walkTree(root *os.Root, dir string, b *walkBudget, fn func(p string, d fs.DirEntry, err error) error) error {
	return walkDir(root, dir, fn, b)
}

func walkDir(root *os.Root, dir string, fn func(p string, d fs.DirEntry, err error) error, b *walkBudget) error {
	f, _, err := openDir(root, dir)
	if err != nil {
		return skipped(fn(dir, nil, err))
	}
	var subdirs []string
	for {
		batch, rerr := f.ReadDir(walkBatch)
		for _, e := range batch {
			if err := b.count(e.Name()); err != nil {
				_ = f.Close()
				return err
			}
			p := filesapi.Join(dir, e.Name())
			err := fn(p, e, nil)
			switch {
			case errors.Is(err, fs.SkipDir):
			case err != nil:
				_ = f.Close()
				return err
			case e.IsDir():
				subdirs = append(subdirs, p)
			}
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && len(batch) == 0) {
			break
		}
		if rerr != nil {
			if err := skipped(fn(dir, nil, rerr)); err != nil {
				_ = f.Close()
				return err
			}
			break
		}
	}
	_ = f.Close()
	for _, p := range subdirs {
		if err := walkDir(root, p, fn, b); err != nil {
			return err
		}
	}
	return nil
}

// skipped turns fs.SkipDir into nil.
func skipped(err error) error {
	if errors.Is(err, fs.SkipDir) {
		return nil
	}
	return err
}
