package files

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// errSkipEntry: the entry vanished, was swapped or cannot be redacted; it is left out.
var errSkipEntry = errors.New("entry left out")

// backupBudget counts a backup stream against max_backup_entries and max_backup_bytes.
type backupBudget struct {
	lim            config.FilesLimits
	entries, bytes int64
}

func (b *backupBudget) add(size int64) error {
	b.entries++
	if b.entries > b.lim.MaxBackupEntries {
		return fmt.Errorf("%w: the data directory has more than %d entries", filesapi.ErrTooLarge, b.lim.MaxBackupEntries)
	}
	if size > b.lim.MaxBackupBytes-b.bytes {
		return fmt.Errorf("%w: the data directory holds more than %d bytes", filesapi.ErrTooLarge, b.lim.MaxBackupBytes)
	}
	b.bytes += size
	return nil
}

// writeBackupTar streams the data directory of root as a PAX tar (spec 4 §4.2): a sorted
// depth-first walk that never follows a symlink (readDirSorted opens folders with openDir,
// files go through openRegular), only folders and regular files, names relative to the root,
// no owners. Symlinks, special files, hidden entries, names that filesapi.CleanPath refuses and
// entries that vanish or are swapped during the walk are left out (onSkip). server.properties at
// the top level is redacted. hook (tests) runs before each entry; onEntry reports each written
// entry with its content size.
func writeBackupTar(ctx context.Context, root *os.Root, w io.Writer, lim config.FilesLimits, hook func(string), onEntry func(int64), onSkip func()) error {
	tw := tar.NewWriter(w)
	b := &backupBudget{lim: lim}
	if err := walkBackup(ctx, root, "", tw, b, hook, onEntry, onSkip); err != nil {
		return err
	}
	return tw.Close()
}

func walkBackup(ctx context.Context, root *os.Root, dir string, tw *tar.Writer, b *backupBudget, hook func(string), onEntry func(int64), onSkip func()) error {
	entries, err := readDirSorted(root, dir, b.lim.MaxBackupEntries-b.entries)
	if err != nil {
		if dir != "" && (errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular)) {
			onSkip() // the folder vanished or was swapped after it was listed
			return nil
		}
		return err
	}
	for _, name := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := filesapi.Join(dir, name)
		if hook != nil {
			hook(p)
		}
		if clean, cerr := filesapi.CleanPath(p); cerr != nil || clean != p || filesapi.IsHidden(p) {
			onSkip()
			continue
		}
		// lstat, not root.Lstat: a parent folder swapped for a symlink (even one that stays
		// inside the root) is refused, never followed.
		info, err := lstat(root, p)
		if err != nil {
			onSkip() // vanished or swapped meanwhile
			continue
		}
		switch {
		case info.IsDir():
			if err := b.add(0); err != nil {
				return err
			}
			if err := tw.WriteHeader(tarHeader(p+"/", tar.TypeDir, info, 0)); err != nil {
				return err
			}
			onEntry(0)
			if err := walkBackup(ctx, root, p, tw, b, hook, onEntry, onSkip); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			n, err := writeTarFile(tw, root, p, b)
			if errors.Is(err, errSkipEntry) {
				onSkip()
				continue
			}
			if err != nil {
				return err
			}
			onEntry(n)
		default:
			onSkip() // symlink, FIFO, device, socket
		}
	}
	return nil
}

// readDirSorted reads all names of a real folder (openDir: no symlink, no FIFO) and closes it
// before returning, so a deep walk keeps one folder open at a time. More than limit names end
// the walk with ErrTooLarge before anything else is read.
func readDirSorted(root *os.Root, dir string, limit int64) ([]string, error) {
	f, _, err := openDir(root, dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	for {
		batch, err := f.Readdirnames(walkBatch)
		names = append(names, batch...)
		if int64(len(names)) > limit {
			return nil, fmt.Errorf("%w: the data directory has too many entries for a backup", filesapi.ErrTooLarge)
		}
		if errors.Is(err, io.EOF) || (err == nil && len(batch) == 0) {
			break
		}
		if err != nil {
			return nil, fsErr(err)
		}
	}
	slices.Sort(names)
	return names, nil
}

// writeTarFile writes one regular file: exactly the size its Stat after the open reported
// (a file that shrinks is padded with zeros, one that grows is cut), server.properties
// redacted. A file that vanished, was swapped or cannot be redacted is errSkipEntry.
func writeTarFile(tw *tar.Writer, root *os.Root, p string, b *backupBudget) (int64, error) {
	if isServerProps(p) {
		data, st, err := readSmall(root, p, b.lim.MaxTextBytes)
		if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) || errors.Is(err, filesapi.ErrTooLarge) {
			return 0, errSkipEntry
		}
		if err != nil {
			return 0, err
		}
		red := redactProperties(string(data))
		if err := b.add(int64(len(red))); err != nil {
			return 0, err
		}
		if err := tw.WriteHeader(tarHeader(p, tar.TypeReg, st, int64(len(red)))); err != nil {
			return 0, err
		}
		_, err = io.WriteString(tw, red)
		return int64(len(red)), err
	}
	f, st, err := openRegular(root, p, os.O_RDONLY)
	if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) {
		return 0, errSkipEntry
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	size := st.Size()
	if err := b.add(size); err != nil {
		return 0, err
	}
	if err := tw.WriteHeader(tarHeader(p, tar.TypeReg, st, size)); err != nil {
		return 0, err
	}
	if err := copyPadded(tw, f, size); err != nil {
		return 0, fsErr(err)
	}
	return size, nil
}

// copyPadded copies exactly size bytes: what r has, then zeros (the file shrank meanwhile).
func copyPadded(w io.Writer, r io.Reader, size int64) error {
	n, err := io.CopyN(w, r, size)
	if errors.Is(err, io.EOF) {
		_, err = io.CopyN(w, zeroFill{}, size-n)
	}
	return err
}

// zeroFill yields zero bytes forever.
type zeroFill struct{}

func (zeroFill) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// tarHeader: PAX, no owner names or ids, permission bits and mtime of info.
func tarHeader(name string, typ byte, info fs.FileInfo, size int64) *tar.Header {
	return &tar.Header{
		Typeflag: typ, Name: name, Size: size, Mode: int64(info.Mode().Perm()),
		ModTime: info.ModTime(), Format: tar.FormatPAX,
	}
}
