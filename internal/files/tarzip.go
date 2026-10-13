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

// tarToZip converts a backup stream to a zip on the fly. Only folders and regular files with
// clean, visible, zip-safe names go in; a folder that is left out takes its content along.
// begin runs once before the first byte of the zip (also for an empty stream).
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
	var skipped []string
	var total int64
	under := func(p string) bool {
		for _, d := range skipped {
			if strings.HasPrefix(p, d+"/") {
				return true
			}
		}
		return false
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("%w: the backup stream is damaged: %w", filesapi.ErrInvalid, err)
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		clean, cerr := filesapi.CleanPath(name)
		ok := cerr == nil && clean != "" && clean == name && !filesapi.IsHidden(clean) && zipSafePath(clean) && !under(clean)
		switch {
		case !ok || (hdr.Typeflag != tar.TypeDir && hdr.Typeflag != tar.TypeReg):
			if hdr.Typeflag == tar.TypeDir {
				skipped = append(skipped, name)
			}
			continue
		}
		if err := l.add(clean); err != nil {
			return err
		}
		open()
		if hdr.Typeflag == tar.TypeDir {
			if _, err := zw.CreateHeader(&zip.FileHeader{Name: clean + "/", Method: zip.Store, Modified: hdr.ModTime}); err != nil {
				return err
			}
			continue
		}
		if hdr.Size < 0 || hdr.Size > lim.MaxBackupBytes-total {
			return fmt.Errorf("%w: the snapshot expands to more than %d bytes", filesapi.ErrTooLarge, lim.MaxBackupBytes)
		}
		total += hdr.Size
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: clean, Method: zip.Deflate, Modified: hdr.ModTime})
		if err != nil {
			return err
		}
		n, err := io.Copy(fw, io.LimitReader(tr, hdr.Size))
		if err != nil {
			return err
		}
		if n != hdr.Size {
			return fmt.Errorf("%w: entry %q is cut off", filesapi.ErrInvalid, hdr.Name)
		}
	}
	open()
	return zw.Close()
}
