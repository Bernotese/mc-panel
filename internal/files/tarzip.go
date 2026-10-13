package files

import (
	"archive/tar"
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// DownloadBackup streams snapshot sid of server id as a zip (spec 4 §4.4). Snapshot, download
// lock and zip slot are checked before the first byte; start is called right before the first
// zip entry, so a dump that fails at once is still answered as an error.
func (s *Service) DownloadBackup(ctx context.Context, id int64, sid string, start func(), w io.Writer) error {
	if !filesapi.ValidSnapshotID(sid) {
		return fmt.Errorf("%w: snapshot id must be 64 hex characters", filesapi.ErrInvalid)
	}
	if err := s.requireBackups(id); err != nil {
		return err
	}
	releaseReader, err := s.jobs.addReader(id)
	if err != nil {
		return err
	}
	defer releaseReader()
	sn, err := s.findSnapshot(ctx, id, sid)
	if err != nil {
		return err
	}
	releaseZip, err := slot(s.zipDownloads, "folder downloads")
	if err != nil {
		return err
	}
	defer releaseZip()
	return s.dump(ctx, id, sn.ID, func(r io.Reader) error {
		return tarToZip(ctx, r, w, s.cfg.Limits, start)
	})
}

// zipSafePath: every component passes zipSafeName (names Windows cannot store are left out,
// like in the folder download).
func zipSafePath(clean string) bool {
	for _, c := range strings.Split(clean, "/") {
		if !zipSafeName(c) {
			return false
		}
	}
	return true
}

// underSkipped reports whether clean lies below one of the skipped folders: one map lookup per
// ancestor, O(depth) whatever the number of skipped folders.
func underSkipped(skipped map[string]bool, clean string) bool {
	for i := 0; i < len(clean); i++ {
		if clean[i] == '/' && skipped[clean[:i]] {
			return true
		}
	}
	return false
}

// tarToZip converts a backup stream to a zip on the fly. Only folders and regular files with
// clean, visible, zip-safe names go in; a folder that is left out takes its content along.
// Every entry of the stream counts against max_backup_entries and maxBackupNameBytes (also the
// ones left out). begin runs once before the first byte of the zip (also for an empty stream).
// The stream must end with a matching end entry (backupEndName, never part of the zip); without
// it the zip is not finished (no central directory), so a cut snapshot never downloads as a
// valid zip.
func tarToZip(ctx context.Context, r io.Reader, w io.Writer, lim config.FilesLimits, begin func()) error {
	tr := tar.NewReader(r)
	var zw *zip.Writer
	open := func() {
		if zw == nil {
			begin()
			zw = zip.NewWriter(w)
		}
	}
	l := &zipLimit{max: lim.MaxBackupEntries}
	skipped := map[string]bool{}
	var total, seen, nameBytes int64
	var counted backupEnd // what the writer counted: folders and regular files, their bytes
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return errIncomplete
		case err != nil:
			return fmt.Errorf("%w: the backup stream is damaged: %w", filesapi.ErrInvalid, err)
		}
		if hdr.Name == backupEndName {
			if err := checkBackupEnd(tr, hdr, counted); err != nil {
				return err
			}
			break
		}
		seen++
		nameBytes += int64(len(hdr.Name))
		switch {
		case seen > lim.MaxBackupEntries:
			return fmt.Errorf("%w: the snapshot has more than %d entries", filesapi.ErrTooLarge, lim.MaxBackupEntries)
		case nameBytes > maxBackupNameBytes:
			return fmt.Errorf("%w: the names in the snapshot take more than %d bytes", filesapi.ErrTooLarge, maxBackupNameBytes)
		}
		isDir, isReg := hdr.Typeflag == tar.TypeDir, hdr.Typeflag == tar.TypeReg
		if !isDir && !isReg {
			continue // the service never writes other types
		}
		if hdr.Size < 0 || (isReg && hdr.Size > lim.MaxBackupBytes-total) {
			return fmt.Errorf("%w: the snapshot expands to more than %d bytes", filesapi.ErrTooLarge, lim.MaxBackupBytes)
		}
		counted.Entries++
		if isReg {
			counted.Bytes += hdr.Size
			total += hdr.Size
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		clean, cerr := filesapi.CleanPath(name)
		if cerr != nil || clean == "" || clean != name || filesapi.IsHidden(clean) || !zipSafePath(clean) || underSkipped(skipped, clean) {
			if isDir && cerr == nil && clean == name {
				skipped[clean] = true // its content follows below it: left out as well
			}
			continue
		}
		if err := l.add(clean); err != nil {
			return err
		}
		open()
		if isDir {
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: clean + "/", Method: zip.Store, Modified: hdr.ModTime}); err != nil {
				return err
			}
			continue
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: clean, Method: zip.Deflate, Modified: hdr.ModTime})
		if err != nil {
			return err
		}
		n, err := io.Copy(fw, io.LimitReader(tr, hdr.Size))
		switch {
		case errors.Is(err, io.ErrUnexpectedEOF), err == nil && n != hdr.Size:
			return errIncomplete
		case err != nil:
			return err
		}
	}
	open()
	return zw.Close()
}
