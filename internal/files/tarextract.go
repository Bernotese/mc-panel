package files

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// extractBackupTar unpacks a backup stream into folder dir of root (a fresh temp folder of the
// restore, spec 4 §4.3) with the rules of the zip extract (spec 3b §4.2): clean relative names
// only, no hidden names, only folders and regular files, no duplicate files (O_EXCL; a folder
// that appears twice is harmless), sizes re-counted against max_backup_bytes /
// max_backup_entries and the names against maxBackupNameBytes, files 0644 (0755 with an execute
// bit), folders 0755, times from the stream. The stream must end with the end entry
// (backupEndName) and its counts must match, otherwise the snapshot is incomplete. Every
// violation is ErrInvalid or ErrTooLarge; the caller removes the temp folder.
func extractBackupTar(ctx context.Context, root *os.Root, dir string, r io.Reader, lim config.FilesLimits, res *reservation, onEntry func(int64)) error {
	tr := tar.NewReader(r)
	var entries, total, nameBytes int64
	type dirTime struct {
		path string
		t    time.Time
	}
	var dirTimes []dirTime
	invalid := func(name string, err error) error {
		if fe := fsErr(err); errors.Is(fe, filesapi.ErrInsufficientStorage) {
			return fe
		}
		return fmt.Errorf("%w: entry %q: %w", filesapi.ErrInvalid, name, err)
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errIncomplete // no end entry: the stream was cut
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return errIncomplete
		}
		if err != nil {
			return fmt.Errorf("%w: the backup stream is damaged: %w", filesapi.ErrInvalid, err)
		}
		if hdr.Name == backupEndName {
			if err := checkBackupEnd(tr, hdr, backupEnd{Entries: entries, Bytes: total}); err != nil {
				return err
			}
			break
		}
		entries++
		nameBytes += int64(len(strings.TrimSuffix(hdr.Name, "/"))) // as the writer counts (backupBudget.name)
		switch {
		case entries > lim.MaxBackupEntries:
			return fmt.Errorf("%w: the snapshot has more than %d entries", filesapi.ErrTooLarge, lim.MaxBackupEntries)
		case nameBytes > maxBackupNameBytes:
			return fmt.Errorf("%w: the names in the snapshot take more than %d bytes", filesapi.ErrTooLarge, maxBackupNameBytes)
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		clean, cerr := filesapi.CleanPath(name)
		switch {
		case cerr != nil:
			return fmt.Errorf("%w: entry %q: %w", filesapi.ErrInvalid, hdr.Name, cerr)
		case clean == "" || clean != name:
			return fmt.Errorf("%w: entry %q is not a clean relative path", filesapi.ErrInvalid, hdr.Name)
		case filesapi.IsHidden(clean):
			return fmt.Errorf("%w: entry %q uses a reserved name", filesapi.ErrInvalid, hdr.Name)
		}
		target := dir + "/" + clean
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(target, dirMode); err != nil { //nolint:gosec // G301: folders are 0755 like the server's own (umask 0027 applies)
				return invalid(hdr.Name, err)
			}
			dirTimes = append(dirTimes, dirTime{target, hdr.ModTime})
			onEntry(0)
		case tar.TypeReg:
			if hdr.Size < 0 || hdr.Size > lim.MaxBackupBytes-total {
				return fmt.Errorf("%w: the snapshot expands to more than %d bytes", filesapi.ErrTooLarge, lim.MaxBackupBytes)
			}
			total += hdr.Size
			if err := root.MkdirAll(parentOf(target), dirMode); err != nil { //nolint:gosec // G301: see above
				return invalid(hdr.Name, err)
			}
			mode := os.FileMode(fileMode)
			if hdr.Mode&0o111 != 0 {
				mode = dirMode // 0755: keep an execute bit (start scripts)
			}
			out, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode) //nolint:gosec // G302/G304: new file in the restore's temp folder inside the instance's os.Root
			if err != nil {
				return invalid(hdr.Name, err)
			}
			n, err := io.Copy(landingWriter{w: out, res: res}, io.LimitReader(ctxReader{ctx: ctx, r: tr}, hdr.Size))
			cerr := out.Close()
			switch {
			case errors.Is(err, errShuttingDown):
				return ctx.Err()
			case errors.Is(err, io.ErrUnexpectedEOF), err == nil && n != hdr.Size:
				return errIncomplete // the stream ends inside this entry
			case err != nil:
				return invalid(hdr.Name, err)
			case cerr != nil:
				return fsErr(cerr)
			}
			_ = root.Chtimes(target, hdr.ModTime, hdr.ModTime)
			onEntry(n)
		default:
			return fmt.Errorf("%w: entry %q is neither a file nor a folder", filesapi.ErrInvalid, hdr.Name)
		}
	}
	sort.SliceStable(dirTimes, func(a, b int) bool {
		return strings.Count(dirTimes[a].path, "/") > strings.Count(dirTimes[b].path, "/")
	})
	for _, d := range dirTimes {
		_ = root.Chtimes(d.path, d.t, d.t)
	}
	return nil
}
