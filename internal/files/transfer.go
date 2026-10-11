package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// Upload stores body (exactly size bytes) at path via a temp file next to it; the target
// only changes when every byte arrived. At most maxUploads run at the same time.
func (s *Service) Upload(ctx context.Context, id int64, p string, overwrite bool, size int64, body io.Reader) (filesapi.UploadResponse, error) {
	if size < 0 {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: Content-Length is required", filesapi.ErrInvalid)
	}
	if size > s.cfg.Limits.MaxUploadBytes {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: upload exceeds %d bytes", filesapi.ErrTooLarge, s.cfg.Limits.MaxUploadBytes)
	}
	clean, err := cleanVisible(p)
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	if clean == "" {
		return filesapi.UploadResponse{}, fmt.Errorf("%w: a file name is required", filesapi.ErrInvalid)
	}
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	default:
		return filesapi.UploadResponse{}, fmt.Errorf("%w: too many uploads at the same time", filesapi.ErrBusy)
	}
	root, err := s.open(id)
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	defer root.Close()
	if err := requireDir(root, parentOf(clean)); err != nil {
		return filesapi.UploadResponse{}, err
	}
	switch fi, err := lstat(root, clean); {
	case errors.Is(err, filesapi.ErrNotFound):
	case err != nil:
		return filesapi.UploadResponse{}, err
	case !fi.Mode().IsRegular():
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, clean)
	case !overwrite:
		return filesapi.UploadResponse{}, fmt.Errorf("%w: %s", filesapi.ErrExists, clean)
	}
	if err := s.reserve(id, size); err != nil {
		return filesapi.UploadResponse{}, err
	}

	tmp, err := tempName(parentOf(clean))
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) //nolint:gosec // G302/G304: new temp file in the instance's os.Root, 0644 like the server's own files
	if err != nil {
		return filesapi.UploadResponse{}, fsErr(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = root.Remove(tmp)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(body, size+1))
	if err == nil && n != size {
		err = fmt.Errorf("%w: received %d bytes, Content-Length announced %d", filesapi.ErrInvalid, n, size)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return filesapi.UploadResponse{}, fsErr(err)
	}
	if err := commit(root, tmp, clean, overwrite); err != nil {
		return filesapi.UploadResponse{}, err
	}
	committed = true
	return filesapi.UploadResponse{Size: n}, nil
}

// Download streams a file (exactly its size at open time) or a folder as zip. start is called
// once before the first byte; an error before start can still be answered as JSON.
func (s *Service) Download(ctx context.Context, id int64, p string, start func(isDir bool, size int64), w io.Writer) error {
	clean, err := cleanVisible(p)
	if err != nil {
		return err
	}
	root, err := s.open(id)
	if err != nil {
		return err
	}
	defer root.Close()
	fi, err := lstat(root, clean)
	if err != nil {
		return err
	}
	switch {
	case fi.IsDir():
		start(true, -1)
		return writeDirZip(ctx, root, clean, w, s.cfg.Limits.MaxTextBytes)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, clean)
	case isServerProps(clean):
		data, _, err := readSmall(root, clean, s.cfg.Limits.MaxTextBytes)
		if err != nil {
			return err
		}
		red := redactProperties(string(data))
		start(false, int64(len(red)))
		_, err = io.Copy(w, strings.NewReader(red))
		return err
	}
	f, st, err := openRegular(root, clean, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	start(false, st.Size())
	_, err = io.CopyN(w, f, st.Size()) // Content-Length is fixed: a file that shrank aborts the response
	return err
}
