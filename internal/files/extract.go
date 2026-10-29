package files

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

type archivePlan struct {
	bytes   int64 // sum of the declared sizes of all files
	entries int64
}

// Extract checks the archive and starts a job that unpacks it into dest (new or empty
// folder). Everything that can be checked up front is answered synchronously.
func (s *Service) Extract(ctx context.Context, req filesapi.ExtractRequest) (filesapi.Job, error) {
	if err := ctx.Err(); err != nil {
		return filesapi.Job{}, err
	}
	src, err := cleanVisible(req.Path)
	if err != nil {
		return filesapi.Job{}, err
	}
	dest, err := cleanVisible(req.Dest)
	if err != nil {
		return filesapi.Job{}, err
	}
	switch {
	case src == "" || dest == "":
		return filesapi.Job{}, fmt.Errorf("%w: archive and destination are required", filesapi.ErrInvalid)
	case !strings.HasSuffix(strings.ToLower(src), ".zip"):
		return filesapi.Job{}, fmt.Errorf("%w: only .zip archives can be extracted", filesapi.ErrInvalid)
	case dest == src || strings.HasPrefix(src, dest+"/"):
		return filesapi.Job{}, fmt.Errorf("%w: the destination must not contain the archive", filesapi.ErrInvalid)
	}
	// The slot is taken before the archive is parsed: pre-checks are throttled like jobs.
	if err := s.jobs.acquire(req.ID, filesapi.JobExtract); err != nil {
		return filesapi.Job{}, err
	}
	ownSlot := true
	defer func() {
		if ownSlot {
			s.jobs.release(req.ID)
		}
	}()
	if err := s.acquireRewrite(ctx, req.ID); err != nil {
		return filesapi.Job{}, err
	}
	root, err := s.open(req.ID)
	if err != nil {
		return filesapi.Job{}, err
	}
	handedOver := false
	defer func() {
		if !handedOver {
			_ = root.Close()
		}
	}()
	if err := requireDir(root, parentOf(dest)); err != nil {
		return filesapi.Job{}, err
	}
	if err := destUsable(root, dest); err != nil {
		return filesapi.Job{}, err
	}
	f, st, err := openRegular(root, src, os.O_RDONLY)
	if err != nil {
		return filesapi.Job{}, err
	}
	defer func() {
		if !handedOver {
			_ = f.Close()
		}
	}()
	if err := checkCentralDirectory(f, st.Size(), s.cfg.Limits.MaxExtractEntries, maxCentralDirBytes); err != nil {
		return filesapi.Job{}, err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return filesapi.Job{}, fmt.Errorf("%w: %s is not a valid zip archive: %w", filesapi.ErrInvalid, src, err)
	}
	plan, err := checkArchive(zr, s.cfg.Limits)
	if err != nil {
		return filesapi.Job{}, err
	}
	res, err := s.reserve(req.ID, plan.bytes)
	if err != nil {
		return filesapi.Job{}, err
	}
	// The pre-check of a large archive takes a while: a caller that gave up meanwhile (the
	// panel reported a failure) must not get a job it does not know about.
	if err := ctx.Err(); err != nil {
		res.release()
		return filesapi.Job{}, err
	}
	ownSlot = false // start owns the slot from here on (it gives it back on error)
	job, err := s.jobs.start(req.ID, jobSpec{kind: filesapi.JobExtract, path: src, dest: dest, totalBytes: plan.bytes, totalEntries: plan.entries},
		s.clock.Now, func(jctx context.Context) error {
			defer root.Close()
			defer f.Close()
			defer res.release()
			err := s.extract(jctx, root, zr, req.ID, dest, res)
			if err != nil {
				s.log.Warn("files: extract failed", "id", req.ID, "archive", src, "dest", dest, "err", err)
			}
			return err
		})
	if err != nil {
		res.release()
		return filesapi.Job{}, err
	}
	handedOver = true
	return job, nil
}

// destUsable: dest must not exist, or be an empty real folder.
func destUsable(root *os.Root, dest string) error {
	fi, err := lstat(root, dest)
	if errors.Is(err, filesapi.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %s exists", filesapi.ErrExists, dest)
	}
	d, _, err := openDir(root, dest)
	if err != nil {
		return err
	}
	defer d.Close()
	if names, err := d.Readdirnames(1); len(names) > 0 || (err != nil && !errors.Is(err, io.EOF)) {
		return fmt.Errorf("%w: %s is not empty", filesapi.ErrExists, dest)
	}
	return nil
}

// checkArchive validates every entry before a byte is written (spec §4.2). The entry count
// is checked first, so a huge archive is refused without validating all names.
func checkArchive(zr *zip.Reader, lim config.FilesLimits) (archivePlan, error) {
	var plan archivePlan
	isDir := make(map[string]bool, len(zr.File))
	for _, zf := range zr.File {
		plan.entries++
		if plan.entries > lim.MaxExtractEntries {
			return plan, fmt.Errorf("%w: the archive has more than %d entries", filesapi.ErrTooLarge, lim.MaxExtractEntries)
		}
		name := strings.TrimSuffix(zf.Name, "/")
		clean, err := filesapi.CleanPath(name)
		switch {
		case err != nil:
			return plan, fmt.Errorf("%w: entry %q: %w", filesapi.ErrInvalid, zf.Name, err)
		case clean == "" || clean != name:
			return plan, fmt.Errorf("%w: entry %q is not a clean relative path", filesapi.ErrInvalid, zf.Name)
		case filesapi.IsHidden(clean):
			return plan, fmt.Errorf("%w: entry %q uses a reserved name", filesapi.ErrInvalid, zf.Name)
		}
		mode := zf.Mode()
		dir := mode.IsDir()
		switch {
		case !unixTypeAllowed(zf.ExternalAttrs) || mode&fs.ModeSymlink != 0 || (!dir && !mode.IsRegular()):
			return plan, fmt.Errorf("%w: entry %q is neither a file nor a folder", filesapi.ErrInvalid, zf.Name)
		case zf.Flags&0x1 != 0:
			return plan, fmt.Errorf("%w: entry %q is encrypted", filesapi.ErrInvalid, zf.Name)
		case zf.Method != zip.Store && zf.Method != zip.Deflate:
			return plan, fmt.Errorf("%w: entry %q uses an unsupported compression method", filesapi.ErrInvalid, zf.Name)
		}
		if _, dup := isDir[clean]; dup {
			return plan, fmt.Errorf("%w: entry %q appears twice", filesapi.ErrInvalid, zf.Name)
		}
		isDir[clean] = dir
		if !dir {
			size := zf.UncompressedSize64
			if size > uint64(lim.MaxExtractBytes) || plan.bytes > lim.MaxExtractBytes-int64(size) {
				return plan, fmt.Errorf("%w: the archive expands to more than %d bytes", filesapi.ErrTooLarge, lim.MaxExtractBytes)
			}
			plan.bytes += int64(size)
		}
	}
	for name := range isDir {
		for p := parentOf(name); p != ""; p = parentOf(p) {
			if d, ok := isDir[p]; ok && !d {
				return plan, fmt.Errorf("%w: %q is a file and the parent of %q", filesapi.ErrInvalid, p, name)
			}
		}
	}
	return plan, nil
}

// unixTypeAllowed checks the Unix file type in the high 16 bits of the external attributes,
// whatever creator system the entry claims (archive/zip only reads them for Unix creators):
// only "none", regular file and folder are allowed - never a symlink, FIFO, device or socket.
func unixTypeAllowed(attrs uint32) bool {
	switch (attrs >> 16) & 0o170000 {
	case 0, 0o100000, 0o040000:
		return true
	}
	return false
}

// extract unpacks into a temp folder next to dest, counting the bytes actually written, and
// renames it to dest when everything succeeded. On any error the temp folder is removed.
func (s *Service) extract(ctx context.Context, root *os.Root, zr *zip.Reader, id int64, dest string, res *reservation) error {
	tmp, err := s.tempName(parentOf(dest))
	if err != nil {
		return err
	}
	if err := root.Mkdir(tmp, dirMode); err != nil { //nolint:gosec // G301: folders are 0755 like the server's own (umask 0027 applies)
		return fsErr(err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = root.RemoveAll(tmp)
		}
	}()
	budget := s.cfg.Limits.MaxExtractBytes
	type dirTime struct {
		path string
		t    time.Time
	}
	var dirTimes []dirTime
	for _, zf := range zr.File {
		name := strings.TrimSuffix(zf.Name, "/")
		if h := s.jobs.hook; h != nil {
			h(ctx, id, name)
		}
		if ctx.Err() != nil {
			return errShuttingDown
		}
		target := tmp + "/" + name
		if zf.Mode().IsDir() {
			if err := root.MkdirAll(target, dirMode); err != nil { //nolint:gosec // G301: see above
				return fsErr(err)
			}
			dirTimes = append(dirTimes, dirTime{target, zf.Modified})
			s.jobs.progress(id, 0)
			continue
		}
		if err := root.MkdirAll(parentOf(target), dirMode); err != nil { //nolint:gosec // G301: see above
			return fsErr(err)
		}
		n, err := extractFile(ctx, root, zf, target, budget)
		budget -= n
		res.landed(n)
		if err != nil {
			return err
		}
		_ = root.Chtimes(target, zf.Modified, zf.Modified)
		s.jobs.progress(id, n)
	}
	// Folder times last (writing into a folder changes its mtime), deepest first.
	sort.SliceStable(dirTimes, func(a, b int) bool {
		return strings.Count(dirTimes[a].path, "/") > strings.Count(dirTimes[b].path, "/")
	})
	for _, d := range dirTimes {
		_ = root.Chtimes(d.path, d.t, d.t)
	}
	if err := destUsable(root, dest); err != nil { // something appeared meanwhile
		return err
	}
	// The rename never replaces anything (renameNoReplace), so an empty dest is removed first -
	// with rmdir, which fails for a folder that got content meanwhile; content that appears at
	// the last moment is never lost, the job fails with exists instead.
	exists := func(err error) bool {
		return errors.Is(err, fs.ErrExist) || errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.ENOTDIR)
	}
	if fi, err := root.Lstat(dest); err == nil && fi.IsDir() {
		if err := root.Remove(dest); err != nil {
			if exists(err) {
				return fmt.Errorf("%w: %s is not empty any more: %w", filesapi.ErrExists, dest, err)
			}
			return fsErr(err)
		}
	}
	if err := s.renameNoReplace(root, tmp, dest); err != nil {
		if exists(err) {
			return fmt.Errorf("%w: %s exists now: %w", filesapi.ErrExists, dest, err)
		}
		return fsErr(err)
	}
	ok = true
	return nil
}

// ctxReader ends a read as soon as ctx is done, so Close interrupts a large entry.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if c.ctx.Err() != nil {
		return 0, errShuttingDown
	}
	return c.r.Read(p)
}

// extractFile writes one entry, refusing more bytes than declared or than the budget left.
func extractFile(ctx context.Context, root *os.Root, zf *zip.File, target string, budget int64) (int64, error) {
	rc, err := zf.Open()
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %w", filesapi.ErrInvalid, zf.Name, err)
	}
	defer rc.Close()
	out, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) //nolint:gosec // G302/G304: new file in the job's temp folder inside the instance's os.Root
	if err != nil {
		return 0, fsErr(err)
	}
	declared := int64(zf.UncompressedSize64) // ≤ MaxExtractBytes, checked by checkArchive
	limit := min(declared, budget)
	n, err := io.Copy(out, io.LimitReader(ctxReader{ctx: ctx, r: rc}, limit+1))
	cerr := out.Close()
	switch {
	case errors.Is(err, errShuttingDown):
		return n, err
	case err != nil:
		if fe := fsErr(err); errors.Is(fe, filesapi.ErrInsufficientStorage) {
			return n, fe // the disk filled up while writing
		}
		return n, fmt.Errorf("%w: %s: %w", filesapi.ErrInvalid, zf.Name, err)
	case n > declared:
		return n, fmt.Errorf("%w: %s is larger than its header says", filesapi.ErrInvalid, zf.Name)
	case n > limit:
		return n, fmt.Errorf("%w: the archive expands to more than allowed", filesapi.ErrTooLarge)
	case cerr != nil:
		return n, fsErr(cerr)
	}
	return n, nil
}
