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
const maxZipNameBytes = 16 << 20

// zipLimit counts the entries of a folder zip against max_extract_entries and maxZipNameBytes.
type zipLimit struct {
	max              int64
	entries, nameLen int64
}

func (l *zipLimit) add(name string) error {
	l.entries++
	l.nameLen += int64(len(name))
	if l.entries > l.max || l.nameLen > maxZipNameBytes {
		return fmt.Errorf("%w: the folder has more than %d entries (or too long names) for a zip download", filesapi.ErrTooLarge, l.max)
	}
	return nil
}

// zipSafeName reports whether one name component can go into a folder zip: on top of
// filesapi.CleanPath (checked by the walk), names that Windows cannot store or treats
// specially are left out - a ':' (NTFS alternate data streams), a trailing '.' or ' ' (Windows
// strips it, so two entries could collide) and the reserved device names CON, PRN, AUX, NUL,
// COM1-9 and LPT1-9, also with an extension ("nul.txt").
func zipSafeName(name string) bool {
	if strings.ContainsRune(name, ':') || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRight(stem, " "))
	switch stem {
	case "CON", "PRN", "AUX", "NUL":
		return false
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		return false
	}
	return true
}

// walkZip visits what a folder zip of dir contains below dir (not dir itself), in walk order:
// only names that pass filesapi.CleanPath unchanged and zipSafeName, never hidden names,
// symlinks, FIFOs, devices or sockets; entries that vanish or change type during the walk are
// left out. visit gets the cleaned path and its Lstat info (a folder or a regular file).
func walkZip(ctx context.Context, root *os.Root, dir string, visit func(p string, info fs.FileInfo) error) error {
	return walkTree(root, dir, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) {
				return nil // vanished or swapped during the walk: left out
			}
			return err
		}
		if clean, cerr := filesapi.CleanPath(p); cerr != nil || clean != p || filesapi.IsHidden(p) || !zipSafeName(d.Name()) {
			return fs.SkipDir
		}
		info, err := root.Lstat(p)
		if err != nil {
			return fs.SkipDir // vanished meanwhile
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fs.SkipDir // symlink, FIFO, device, socket - or a folder that is none any more
		}
		return visit(p, info)
	})
}

// checkDirZip runs before the headers of a folder download: the folder's own name must be
// storable, and the zip must stay within the entry and name limits (counted by a first walk),
// so the panel can answer 400/413 instead of cutting the stream off. writeDirZip still counts,
// for a folder that grows meanwhile.
func checkDirZip(ctx context.Context, root *os.Root, dir string, lim config.FilesLimits) error {
	l := &zipLimit{max: lim.MaxExtractEntries}
	if dir != "" {
		name := dir[strings.LastIndexByte(dir, '/')+1:]
		if !zipSafeName(name) {
			return fmt.Errorf("%w: a folder named %q cannot be downloaded as a zip", filesapi.ErrInvalid, name)
		}
		if err := l.add(dir); err != nil {
			return err
		}
	}
	return walkZip(ctx, root, dir, func(p string, _ fs.FileInfo) error { return l.add(p) })
}

// writeDirZip streams folder dir (root-relative, "" = everything) as a zip without a temp
// file. Entries are named relative to dir's parent, so the folder itself is the top level.
//
// The names come from a file system the Minecraft server controls, and the zip is unpacked on
// an admin's machine: only what walkZip lets through goes in. server.properties at the top
// level is redacted (left out if it cannot be read as text). More than max_extract_entries
// entries (or very long names in total) end the zip with ErrTooLarge.
func writeDirZip(ctx context.Context, root *os.Root, dir string, w io.Writer, lim config.FilesLimits) error {
	zw := zip.NewWriter(w)
	strip := parentOf(dir)
	l := &zipLimit{max: lim.MaxExtractEntries}
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
		if err := l.add(dir); err != nil {
			return err
		}
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: zipName(dir) + "/", Method: zip.Store, Modified: fi.ModTime()}); err != nil {
			return err
		}
	}
	err := walkZip(ctx, root, dir, func(p string, info fs.FileInfo) error {
		if info.IsDir() {
			if err := l.add(p); err != nil {
				return err
			}
			_, err := zw.CreateHeader(&zip.FileHeader{Name: zipName(p) + "/", Method: zip.Store, Modified: info.ModTime()})
			return err
		}
		return addZipFile(zw, root, p, zipName(p), info, lim.MaxTextBytes, l.add)
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
