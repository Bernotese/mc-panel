package files

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// maxZipNameBytes bounds the entry names a folder zip keeps in memory for its central
// directory (zip.Writer holds one header per entry until Close).
const maxZipNameBytes = 64 << 20

// writeDirZip streams folder dir (root-relative, "" = everything) as a zip without a temp
// file. Entries are named relative to dir's parent, so the folder itself is the top level.
//
// The names come from a file system the Minecraft server controls, and the zip is unpacked on
// an admin's machine: only names that pass filesapi.CleanPath unchanged go in (no backslash,
// no control characters, valid UTF-8, no ".."). Symlinks, FIFOs, devices, sockets and hidden
// names are left out, as are entries that vanish or change type during the walk;
// server.properties at the top level is redacted (left out if it cannot be read as text).
// More than max_extract_entries entries (or very long names in total) end the zip with
// ErrTooLarge.
func writeDirZip(ctx context.Context, root *os.Root, dir string, w io.Writer, lim config.FilesLimits) error {
	zw := zip.NewWriter(w)
	strip := parentOf(dir)
	var entries, nameBytes int64
	add := func(name string) error {
		entries++
		nameBytes += int64(len(name))
		if entries > lim.MaxExtractEntries || nameBytes > maxZipNameBytes {
			return fmt.Errorf("%w: the folder has more than %d entries", filesapi.ErrTooLarge, lim.MaxExtractEntries)
		}
		return nil
	}
	zipName := func(p string) string {
		if strip == "" {
			return p
		}
		return strings.TrimPrefix(p, strip+"/")
	}
	if dir != "" {
		fi, err := lstat(root, dir)
		if err != nil {
			return err
		}
		if err := add(dir); err != nil {
			return err
		}
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: zipName(dir) + "/", Method: zip.Store, Modified: fi.ModTime()}); err != nil {
			return err
		}
	}
	err := walkTree(root, dir, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) {
				return nil // vanished or swapped during the walk: left out
			}
			return err
		}
		if clean, cerr := filesapi.CleanPath(p); cerr != nil || clean != p || filesapi.IsHidden(p) {
			return fs.SkipDir
		}
		info, err := root.Lstat(p)
		if err != nil {
			return fs.SkipDir // vanished meanwhile
		}
		switch {
		case info.IsDir():
			if err := add(p); err != nil {
				return err
			}
			_, err := zw.CreateHeader(&zip.FileHeader{Name: zipName(p) + "/", Method: zip.Store, Modified: info.ModTime()})
			return err
		case info.Mode().IsRegular():
			return addZipFile(zw, root, p, zipName(p), info, lim.MaxTextBytes, add)
		}
		return fs.SkipDir // symlink, FIFO, device, socket - or a folder that is none any more
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

func addZipFile(zw *zip.Writer, root *os.Root, p, name string, info fs.FileInfo, maxSmall int64, add func(string) error) error {
	var src io.Reader
	if isServerProps(p) {
		data, _, err := readSmall(root, p, maxSmall)
		if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) || errors.Is(err, filesapi.ErrTooLarge) {
			return nil // cannot be redacted: left out
		}
		if err != nil {
			return err
		}
		src = strings.NewReader(redactProperties(string(data)))
	} else {
		f, _, err := openRegular(root, p, os.O_RDONLY)
		if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) {
			return nil // removed or swapped during the walk: left out
		}
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}
	if err := add(p); err != nil {
		return err
	}
	zf, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: info.ModTime()})
	if err != nil {
		return err
	}
	_, err = io.Copy(zf, src)
	return err
}
