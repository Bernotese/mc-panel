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

// errSkipEntry: the entry vanished, was swapped, cannot be read or cannot be redacted; it is
// left out.
var errSkipEntry = errors.New("entry left out")

// backupBudget counts a backup stream: every name read from the data directory (also the ones
// left out, so millions of symlinks cannot exhaust the memory) against max_backup_entries and
// maxBackupNameBytes, the content against max_backup_bytes. entries and bytes are what the
// stream holds (the end entry, backupEnd).
type backupBudget struct {
	lim              config.FilesLimits
	names, nameBytes int64
	entries, bytes   int64
}

// name counts one name read from folder dir.
func (b *backupBudget) name(p string) error {
	b.names++
	b.nameBytes += int64(len(p))
	switch {
	case b.names > b.lim.MaxBackupEntries:
		return fmt.Errorf("%w: the data directory has more than %d entries", filesapi.ErrTooLarge, b.lim.MaxBackupEntries)
	case b.nameBytes > maxBackupNameBytes:
		return fmt.Errorf("%w: the names in the data directory take more than %d bytes", filesapi.ErrTooLarge, maxBackupNameBytes)
	}
	return nil
}

// add counts one written entry with size content bytes.
func (b *backupBudget) add(size int64) error {
	if size > b.lim.MaxBackupBytes-b.bytes {
		return fmt.Errorf("%w: the data directory holds more than %d bytes", filesapi.ErrTooLarge, b.lim.MaxBackupBytes)
	}
	b.entries++
	b.bytes += size
	return nil
}

// skipCause is the reason an entry is left out that deserves a log line (it cannot be read,
// e.g. chmod 000 from inside the container); nil for ordinary skips (symlinks, hidden names,
// entries that vanished).
func skipCause(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return err
	}
	return nil
}

// writeBackupTar streams the data directory of root as a PAX tar (spec 4 §4.2): a sorted
// depth-first walk that never follows a symlink (readDirSorted opens folders with openDir,
// files go through openRegular), only folders and regular files, names relative to the root,
// no owners, and the end entry backupEndName last. Symlinks, special files, hidden entries,
// names that filesapi.CleanPath refuses, entries that cannot be read and entries that vanish
// or are swapped during the walk are left out (onSkip, with skipCause of the error). Only the
// data directory itself must be readable. server.properties at the top level is redacted.
// hook (tests) runs before each entry; onEntry reports each written entry with its content
// size.
func writeBackupTar(ctx context.Context, root *os.Root, w io.Writer, lim config.FilesLimits, hook func(string), onEntry func(int64), onSkip func(p string, cause error)) error {
	tw := tar.NewWriter(w)
	b := &backupBudget{lim: lim}
	if err := walkBackup(ctx, root, "", tw, b, hook, onEntry, onSkip); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeBackupEnd(tw, backupEnd{Entries: b.entries, Bytes: b.bytes}); err != nil {
		return err
	}
	return tw.Close()
}

func walkBackup(ctx context.Context, root *os.Root, dir string, tw *tar.Writer, b *backupBudget, hook func(string), onEntry func(int64), onSkip func(string, error)) error {
	entries, err := readDirSorted(root, dir, b)
	if err != nil {
		if dir != "" && (errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) || errors.Is(err, fs.ErrPermission)) {
			onSkip(dir, skipCause(err)) // the folder vanished, was swapped or cannot be read
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
			onSkip(p, nil)
			continue
		}
		// lstat, not root.Lstat: a parent folder swapped for a symlink (even one that stays
		// inside the root) is refused, never followed.
		info, err := lstat(root, p)
		if err != nil {
			onSkip(p, skipCause(err)) // vanished or swapped meanwhile
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
				onSkip(p, skipCause(err))
				continue
			}
			if err != nil {
				return err
			}
			onEntry(n)
		default:
			onSkip(p, nil) // symlink, FIFO, device, socket
		}
	}
	return nil
}

// readDirSorted reads all names of a real folder (openDir: no symlink, no FIFO) and closes it
// before returning, so a deep walk keeps one folder open at a time. Every name counts against
// the budget (b.name) as soon as it is read, so a folder with too many names ends the walk with
// ErrTooLarge before the next batch.
func readDirSorted(root *os.Root, dir string, b *backupBudget) ([]string, error) {
	f, _, err := openDir(root, dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	for {
		batch, err := f.Readdirnames(walkBatch)
		for _, n := range batch {
			if berr := b.name(filesapi.Join(dir, n)); berr != nil {
				return nil, berr
			}
		}
		names = append(names, batch...)
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
// redacted. A file that vanished, was swapped, cannot be read or cannot be redacted is
// errSkipEntry (wrapping the cause when it cannot be read).
func writeTarFile(tw *tar.Writer, root *os.Root, p string, b *backupBudget) (int64, error) {
	if isServerProps(p) {
		data, st, err := readSmall(root, p, b.lim.MaxTextBytes)
		if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) || errors.Is(err, filesapi.ErrTooLarge) {
			return 0, errSkipEntry
		}
		if errors.Is(err, fs.ErrPermission) {
			return 0, fmt.Errorf("%w: %w", errSkipEntry, err)
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
	if errors.Is(err, fs.ErrPermission) {
		return 0, fmt.Errorf("%w: %w", errSkipEntry, err)
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
