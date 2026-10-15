package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const walkBatch = 256

// The budget of one walk over data a Minecraft server controls (walkTree, unlockTree,
// visibleNames): every name read counts, also the ones the caller skips, so millions of
// entries (SEC-23) end the walk with ErrTooLarge instead of exhausting the service's memory.
// Variables so tests can lower them.
var (
	walkMaxNames     int64 = 1_000_000
	walkMaxNameBytes int64 = 256 << 20
)

// walkBudget counts the names one walk has read.
type walkBudget struct{ names, bytes int64 }

func (b *walkBudget) count(name string) error {
	b.names++
	b.bytes += int64(len(name))
	if b.names > walkMaxNames || b.bytes > walkMaxNameBytes {
		return fmt.Errorf("%w: more than %d names or %d bytes of names in one folder tree",
			filesapi.ErrTooLarge, walkMaxNames, walkMaxNameBytes)
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
// More names than the walk budget end it with ErrTooLarge.
func walkTree(root *os.Root, dir string, fn func(p string, d fs.DirEntry, err error) error) error {
	return walkDir(root, dir, fn, &walkBudget{})
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
