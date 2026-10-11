package files

import (
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const walkBatch = 256

// walkTree visits every entry below folder dir ("" = the root), like fs.WalkDir but safe on
// data a Minecraft server controls: folders are read through openDir, so a symlink is never
// followed and a FIFO swapped in for a folder cannot block, and at most one folder is open at
// a time (subfolders are visited after their parent was read and closed).
//
// fn gets the cleaned path and the entry. For a folder, fs.SkipDir skips its content (for
// other entries it is ignored). A folder that cannot be read (also dir itself) is reported as
// fn(path, nil, err); returning nil or fs.SkipDir skips it, any other error ends the walk.
func walkTree(root *os.Root, dir string, fn func(p string, d fs.DirEntry, err error) error) error {
	f, _, err := openDir(root, dir)
	if err != nil {
		return skipped(fn(dir, nil, err))
	}
	var subdirs []string
	for {
		batch, rerr := f.ReadDir(walkBatch)
		for _, e := range batch {
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
		if err := walkTree(root, p, fn); err != nil {
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
