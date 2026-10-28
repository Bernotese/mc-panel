package files

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
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
	return s.UploadChecked(ctx, id, p, overwrite, size, filesapi.Checksum{}, body)
}

// UploadChecked is Upload with an expected digest (spec 3c §5.2). With a non-zero sum it
// computes sha256 and sha512 while it writes the temp file and compares them before fsync and
// rename: a mismatch is ErrHashMismatch, the temp file is removed and the target stays as it
// was. Only a checked upload answers with both digests; the zero Checksum means no check.
func (s *Service) UploadChecked(ctx context.Context, id int64, p string, overwrite bool, size int64, sum filesapi.Checksum, body io.Reader) (filesapi.UploadResponse, error) {
	checked := sum != (filesapi.Checksum{})
	if checked {
		if err := sum.Validate(); err != nil {
			return filesapi.UploadResponse{}, err
		}
	}
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
	sem := s.uploads
	if checked {
		sem = s.checkedUploads // plugin installs (SEC-24)
	}
	release, err := slot(sem, "uploads")
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	defer release()
	// The lease is held while the body streams: a restore or extract waits for it (or gives up).
	unlease, err := s.lease(id)
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	defer unlease()
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
	res, err := s.reserve(id, size)
	if err != nil {
		return filesapi.UploadResponse{}, err
	}
	defer res.release()

	tmp, err := s.tempName(parentOf(clean))
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
	src := &bodyReader{r: body}
	h256, h512 := sha256.New(), sha512.New()
	var dst io.Writer = landingWriter{w: f, res: res}
	if checked {
		dst = io.MultiWriter(dst, h256, h512) // one pass: both digests while the bytes land
	}
	n, err := io.Copy(dst, io.LimitReader(src, size+1))
	if src.err != nil {
		// The caller's body broke off or stalled: its problem, not an internal error (the panel
		// would otherwise report the service as unavailable).
		err = src.problem()
	}
	if err == nil && n != size {
		err = fmt.Errorf("%w: received %d bytes, Content-Length announced %d", filesapi.ErrInvalid, n, size)
	}
	if err == nil {
		err = ctx.Err()
	}
	out := filesapi.UploadResponse{Size: n}
	if err == nil && checked {
		out.SHA256, out.SHA512 = hex.EncodeToString(h256.Sum(nil)), hex.EncodeToString(h512.Sum(nil))
		if !sum.Matches(out.SHA256, out.SHA512) {
			err = fmt.Errorf("%w: the upload does not match the expected %s checksum", filesapi.ErrHashMismatch, sum.Algo)
		}
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
	return out, nil
}

// bodyReader records the error of the upload body (as opposed to a write error on disk).
type bodyReader struct {
	r   io.Reader
	err error
}

func (b *bodyReader) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		b.err = err
	}
	return n, err
}

// problem describes the body error for the caller (without the error text, which may name the
// connection).
func (b *bodyReader) problem() error {
	switch {
	case errors.Is(b.err, os.ErrDeadlineExceeded):
		return fmt.Errorf("%w: the upload stalled (no data for %s)", filesapi.ErrInvalid, ioTimeout)
	case errors.Is(b.err, errClientTooSlow), errors.Is(b.err, errStreamTooLong):
		return fmt.Errorf("%w: the upload was too slow: %w", filesapi.ErrInvalid, b.err)
	}
	return fmt.Errorf("%w: the upload was interrupted", filesapi.ErrInvalid)
}

// slot takes a place in sem without waiting; release gives it back.
func slot(sem chan struct{}, what string) (release func(), err error) {
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	default:
		return nil, fmt.Errorf("%w: too many %s at the same time", filesapi.ErrBusy, what)
	}
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
		// Refused up front rather than cut off mid-stream (the browser would keep a broken zip).
		// The counting walk holds no zip in memory, so the zip slot is taken only afterwards.
		if err := checkDirZip(ctx, root, clean, s.cfg.Limits); err != nil {
			return err
		}
		release, err := slot(s.zipDownloads, "folder downloads")
		if err != nil {
			return err
		}
		defer release()
		start(true, -1)
		return writeDirZip(ctx, root, clean, w, s.cfg.Limits)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, clean)
	}
	release, err := slot(s.fileDownloads, "downloads")
	if err != nil {
		return err
	}
	defer release()
	if isServerProps(clean) {
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
