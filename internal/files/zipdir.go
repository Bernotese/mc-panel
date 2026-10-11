package files

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// writeDirZip streams folder dir (root-relative, "" = everything) as a zip without a temp
// file. Entries are named relative to dir's parent, so the folder itself is the top level.
// Symlinks, FIFOs, devices, sockets and hidden names are left out; server.properties at the
// top level is redacted.
func writeDirZip(ctx context.Context, root *os.Root, dir string, w io.Writer, maxSmall int64) error {
	zw := zip.NewWriter(w)
	strip := parentOf(dir)
	err := fs.WalkDir(root.FS(), rootName(dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if p == "." {
			return nil
		}
		if filesapi.IsHidden(p) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		name := p
		if strip != "" {
			name = strings.TrimPrefix(p, strip+"/")
		}
		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil // vanished during the walk
		}
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			_, err := zw.CreateHeader(&zip.FileHeader{Name: name + "/", Method: zip.Store, Modified: info.ModTime()})
			return err
		case d.Type().IsRegular():
			return addZipFile(zw, root, p, name, info, maxSmall)
		}
		return nil // symlink, FIFO, device, socket
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

func addZipFile(zw *zip.Writer, root *os.Root, p, name string, info fs.FileInfo, maxSmall int64) error {
	var src io.Reader
	if isServerProps(p) {
		data, _, err := readSmall(root, p, maxSmall)
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
	zf, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: info.ModTime()})
	if err != nil {
		return err
	}
	_, err = io.Copy(zf, src)
	return err
}
