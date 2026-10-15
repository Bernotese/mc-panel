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
// e.g. chmod 000 from inside the container, or it is sparse); nil for ordinary skips
// (symlinks, hidden names, entries that vanished).
func skipCause(err error) error {
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, errSparse) {
		return err
	}
	return nil
}

// sparseMinSize: larger regular files that occupy less than 1/8 of their size on disk are
// left out of a backup (SEC-25). Reading such a file yields mostly zeros: a container could
// hold the panel's single backup job (and the server's save-off) for hours with a huge file
// that costs it no disk space (sparseForBackup). A variable so tests can lower it.
var sparseMinSize int64 = 64 << 20

var errSparse = errors.New("sparse file (it occupies less than 1/8 of its size on disk)")

// sparseForBackup reports whether the open file f (Stat st) is one a backup leaves out: larger than
// sparseMinSize, less than 1/8 of it allocated, and with a hole (so a file that a compressing
// file system stores small is still backed up).
func sparseForBackup(f *os.File, st fs.FileInfo) bool {
	alloc, ok := allocatedBytes(st)
	return ok && st.Size() > sparseMinSize && alloc < st.Size()/8 && hasHole(f, st.Size())
}

// maxBackupDepth bounds the folder depth of a backup: the walk holds one open folder per level
// (an *os.Root each). Deeper folders are left out with their content (counted in skipped).
const maxBackupDepth = 256

// writeBackupTar streams the data directory of root as a PAX tar (spec 4 §4.2): a sorted
// depth-first walk that never follows a symlink, only folders and regular files, names relative
// to the root, no owners, and the end entry backupEndName last. The walk keeps an *os.Root on
// every folder on its way down (openSubdir: Lstat, open, os.SameFile), so every entry costs a
// constant number of system calls however deep it lies, and a folder swapped for a symlink
// after it was opened does not change what is read (the open handle stays on the real folder).
// Symlinks, special files, hidden entries, names that filesapi.CleanPath refuses, entries that
// cannot be read, folders deeper than maxBackupDepth and entries that vanish or are swapped
// during the walk are left out (onSkip, with skipCause of the error). Only the data directory
// itself must be readable. Sparse files (sparseForBackup) are left out as well. server.properties at
// the top level is redacted. hook (tests) runs
// before each entry; onEntry reports each written entry with its content size.
func writeBackupTar(ctx context.Context, root *os.Root, w io.Writer, lim config.FilesLimits, hook func(string), onEntry func(int64), onSkip func(p string, cause error)) error {
	tw := tar.NewWriter(w)
	b := &backupBudget{lim: lim}
	wk := &backupWalk{ctx: ctx, tw: tw, b: b, hook: hook, onEntry: onEntry, onSkip: onSkip}
	if err := wk.folder(root, "", 0); err != nil {
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

// backupWalk is the state of one writeBackupTar.
type backupWalk struct {
	ctx     context.Context
	tw      *tar.Writer
	b       *backupBudget
	hook    func(string)
	onEntry func(int64)
	onSkip  func(string, error)
}

// folder writes the entries of folder d (path dir, at the given depth; the data directory is 0).
func (wk *backupWalk) folder(d *os.Root, dir string, depth int) error {
	names, err := readDirSorted(d, dir, wk.b)
	if err != nil {
		if dir != "" && (errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, fs.ErrPermission)) {
			wk.onSkip(dir, skipCause(err)) // the folder cannot be read
			return nil
		}
		return err
	}
	for _, name := range names {
		if err := wk.ctx.Err(); err != nil {
			return err
		}
		p := filesapi.Join(dir, name)
		if wk.hook != nil {
			wk.hook(p)
		}
		if clean, cerr := filesapi.CleanPath(p); cerr != nil || clean != p || filesapi.IsHidden(p) {
			wk.onSkip(p, nil)
			continue
		}
		// One component relative to the open folder: nothing on the way can be a symlink.
		info, err := d.Lstat(name)
		if err != nil {
			wk.onSkip(p, skipCause(err)) // vanished meanwhile
			continue
		}
		switch {
		case info.IsDir():
			if depth+1 > maxBackupDepth {
				wk.onSkip(p, nil)
				continue
			}
			sub, err := openSubdir(d, name, info)
			if err != nil {
				wk.onSkip(p, skipCause(err)) // swapped meanwhile or cannot be read
				continue
			}
			err = wk.dirEntry(sub, p, info, depth+1)
			_ = sub.Close()
			if err != nil {
				return err
			}
		case info.Mode().IsRegular():
			n, err := writeTarFile(wk.tw, d, name, p, info, wk.b)
			if errors.Is(err, errSkipEntry) {
				wk.onSkip(p, skipCause(err))
				continue
			}
			if err != nil {
				return err
			}
			wk.onEntry(n)
		default:
			wk.onSkip(p, nil) // symlink, FIFO, device, socket
		}
	}
	return nil
}

// dirEntry writes the header of folder p and then its content.
func (wk *backupWalk) dirEntry(sub *os.Root, p string, info fs.FileInfo, depth int) error {
	if err := wk.b.add(0); err != nil {
		return err
	}
	if err := wk.tw.WriteHeader(tarHeader(p+"/", tar.TypeDir, info, 0)); err != nil {
		return err
	}
	wk.onEntry(0)
	return wk.folder(sub, p, depth)
}

// openSubdir opens folder name of d as an *os.Root, accepting it only if it is the very folder
// the Lstat fi describes (os.Root.OpenRoot would follow a symlink swapped in meanwhile as long as
// it stays inside the root; os.SameFile refuses it).
func openSubdir(d *os.Root, name string, fi fs.FileInfo) (*os.Root, error) {
	sub, err := d.OpenRoot(name)
	if err != nil {
		return nil, fsErr(err)
	}
	st, err := sub.Stat(".")
	if err != nil || !st.IsDir() || !os.SameFile(fi, st) {
		_ = sub.Close()
		return nil, fmt.Errorf("%w: %s changed while it was opened", filesapi.ErrNotRegular, name)
	}
	return sub, nil
}

// readDirSorted reads all names of the open folder d (path dir) and closes the listing before
// returning. Every name counts against the budget (b.name) as soon as it is read, so a folder
// with too many names ends the walk with ErrTooLarge before the next batch.
func readDirSorted(d *os.Root, dir string, b *backupBudget) ([]string, error) {
	f, err := d.Open(".")
	if err != nil {
		return nil, fsErr(err)
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

// writeTarFile writes regular file name of folder d (path p, Lstat info): exactly the size its
// Stat after the open reported (a file that shrinks is padded with zeros, one that grows is
// cut), server.properties redacted. A file that vanished, was swapped, cannot be read or cannot
// be redacted is errSkipEntry (wrapping the cause when it cannot be read).
func writeTarFile(tw *tar.Writer, d *os.Root, name, p string, info fs.FileInfo, b *backupBudget) (int64, error) {
	if isServerProps(p) {
		data, st, err := readSmall(d, name, b.lim.MaxTextBytes)
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
	f, st, err := openChecked(d, name, info, os.O_RDONLY)
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
	if sparseForBackup(f, st) {
		return 0, fmt.Errorf("%w: %w", errSkipEntry, errSparse)
	}
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
