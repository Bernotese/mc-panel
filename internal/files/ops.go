package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

const listBatch = 256

func entryType(m fs.FileMode) filesapi.EntryType {
	switch {
	case m.IsDir():
		return filesapi.TypeDir
	case m.IsRegular():
		return filesapi.TypeFile
	case m&fs.ModeSymlink != 0:
		return filesapi.TypeSymlink
	}
	return filesapi.TypeOther
}

// List lists a folder: hidden names left out, at most max_list_entries entries.
func (s *Service) List(ctx context.Context, id int64, p string) (filesapi.ListResponse, error) {
	if err := ctx.Err(); err != nil {
		return filesapi.ListResponse{}, err
	}
	clean, err := cleanVisible(p)
	if err != nil {
		return filesapi.ListResponse{}, err
	}
	root, err := s.open(id)
	if err != nil {
		return filesapi.ListResponse{}, err
	}
	defer root.Close()
	d, _, err := openDir(root, clean)
	if err != nil {
		return filesapi.ListResponse{}, err
	}
	defer d.Close()

	limit := s.cfg.Limits.MaxListEntries
	res := filesapi.ListResponse{Entries: []filesapi.Entry{}}
	for !res.Truncated {
		batch, err := d.ReadDir(listBatch)
		for _, e := range batch {
			if filesapi.IsHidden(filesapi.Join(clean, e.Name())) {
				continue
			}
			if len(res.Entries) >= limit {
				res.Truncated = true
				break
			}
			info, ierr := root.Lstat(filesapi.Join(clean, e.Name())) // inside the root, never followed
			if ierr != nil {
				continue // vanished meanwhile
			}
			ent := filesapi.Entry{Name: e.Name(), Type: entryType(info.Mode()), MTime: info.ModTime().UnixMilli()}
			if ent.Type == filesapi.TypeFile {
				ent.Size = info.Size()
			}
			res.Entries = append(res.Entries, ent)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return filesapi.ListResponse{}, fsErr(err)
		}
	}
	sort.Slice(res.Entries, func(i, j int) bool {
		a, b := res.Entries[i], res.Entries[j]
		if (a.Type == filesapi.TypeDir) != (b.Type == filesapi.TypeDir) {
			return a.Type == filesapi.TypeDir
		}
		la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if la != lb {
			return la < lb
		}
		return a.Name < b.Name
	})
	return res, nil
}

// readSmall reads a regular file of at most limit bytes (ErrTooLarge otherwise).
func readSmall(root *os.Root, clean string, limit int64) ([]byte, fs.FileInfo, error) {
	f, st, err := openRegular(root, clean, os.O_RDONLY)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if st.Size() > limit {
		return nil, st, fmt.Errorf("%w: %s is larger than %d bytes", filesapi.ErrTooLarge, clean, limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, st, fsErr(err)
	}
	if int64(len(data)) > limit {
		return nil, st, fmt.Errorf("%w: %s is larger than %d bytes", filesapi.ErrTooLarge, clean, limit)
	}
	return data, st, nil
}

// Read returns a text file for the editor (spec §4: ≤ max_text_bytes, UTF-8, no NUL).
func (s *Service) Read(ctx context.Context, id int64, p string) (filesapi.ReadResponse, error) {
	if err := ctx.Err(); err != nil {
		return filesapi.ReadResponse{}, err
	}
	clean, err := cleanVisible(p)
	if err != nil {
		return filesapi.ReadResponse{}, err
	}
	if clean == "" {
		return filesapi.ReadResponse{}, fmt.Errorf("%w: the root is a folder", filesapi.ErrNotRegular)
	}
	root, err := s.open(id)
	if err != nil {
		return filesapi.ReadResponse{}, err
	}
	defer root.Close()
	data, st, err := readSmall(root, clean, s.cfg.Limits.MaxTextBytes)
	if errors.Is(err, filesapi.ErrTooLarge) {
		return filesapi.ReadResponse{}, fmt.Errorf("%w: %s is too large for the editor", filesapi.ErrNotText, clean)
	}
	if err != nil {
		return filesapi.ReadResponse{}, err
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return filesapi.ReadResponse{}, fmt.Errorf("%w: %s is not a text file", filesapi.ErrNotText, clean)
	}
	content := string(data)
	if isServerProps(clean) {
		content = redactProperties(content)
	}
	return filesapi.ReadResponse{Content: content, MTime: st.ModTime().UnixMilli(), Size: st.Size()}, nil
}

// Write saves editor content atomically (temp file, fsync, rename/link) with an mtime check.
func (s *Service) Write(ctx context.Context, req filesapi.WriteRequest) (filesapi.WriteResponse, error) {
	if err := ctx.Err(); err != nil {
		return filesapi.WriteResponse{}, err
	}
	clean, err := cleanVisible(req.Path)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	if clean == "" {
		return filesapi.WriteResponse{}, fmt.Errorf("%w: the root is a folder", filesapi.ErrNotRegular)
	}
	if int64(len(req.Content)) > s.cfg.Limits.MaxTextBytes {
		return filesapi.WriteResponse{}, fmt.Errorf("%w: content exceeds %d bytes", filesapi.ErrTooLarge, s.cfg.Limits.MaxTextBytes)
	}
	root, err := s.open(req.ID)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	defer root.Close()
	if err := requireDir(root, parentOf(clean)); err != nil {
		return filesapi.WriteResponse{}, err
	}

	content := req.Content
	fi, err := lstat(root, clean)
	switch {
	case errors.Is(err, filesapi.ErrNotFound):
		if req.ExpectedMTime != nil {
			return filesapi.WriteResponse{}, fmt.Errorf("%w: %s was deleted meanwhile", filesapi.ErrModified, clean)
		}
		if isServerProps(clean) { // a new file never gets the placeholder as its password
			content = restorePassword(content, "")
		}
	case err != nil:
		return filesapi.WriteResponse{}, err
	case !fi.Mode().IsRegular():
		return filesapi.WriteResponse{}, fmt.Errorf("%w: %s is not a regular file", filesapi.ErrNotRegular, clean)
	case req.ExpectedMTime == nil:
		return filesapi.WriteResponse{}, fmt.Errorf("%w: %s", filesapi.ErrExists, clean)
	case *req.ExpectedMTime != fi.ModTime().UnixMilli():
		return filesapi.WriteResponse{}, fmt.Errorf("%w: %s was changed meanwhile", filesapi.ErrModified, clean)
	case isServerProps(clean):
		cur, _, rerr := readSmall(root, clean, s.cfg.Limits.MaxTextBytes)
		if rerr != nil && !errors.Is(rerr, filesapi.ErrTooLarge) {
			return filesapi.WriteResponse{}, rerr
		}
		content = restorePassword(content, string(cur))
	}
	if err := s.reserve(req.ID, int64(len(content))); err != nil {
		return filesapi.WriteResponse{}, err
	}
	if err := writeAtomic(root, clean, []byte(content), req.ExpectedMTime != nil); err != nil {
		return filesapi.WriteResponse{}, err
	}
	st, err := lstat(root, clean)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	return filesapi.WriteResponse{MTime: st.ModTime().UnixMilli(), Size: st.Size()}, nil
}

// writeAtomic writes data to a temp file next to clean, syncs it and commits it.
func writeAtomic(root *os.Root, clean string, data []byte, overwrite bool) error {
	tmp, err := tempName(parentOf(clean))
	if err != nil {
		return err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) //nolint:gosec // G302/G304: new temp file in the instance's os.Root, 0644 like the server's own files
	if err != nil {
		return fsErr(err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = commit(root, tmp, clean, overwrite)
	}
	if err != nil {
		_ = root.Remove(tmp)
		return fsErr(err)
	}
	return nil
}

// Mkdir creates one folder (the parent must exist).
func (s *Service) Mkdir(ctx context.Context, id int64, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clean, err := cleanVisible(p)
	if err != nil {
		return err
	}
	if clean == "" {
		return fmt.Errorf("%w: the root exists", filesapi.ErrInvalid)
	}
	root, err := s.open(id)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := requireDir(root, parentOf(clean)); err != nil {
		return err
	}
	return fsErr(root.Mkdir(clean, dirMode)) //nolint:gosec // G301: folders are 0755 like the server's own (umask 0027 applies)
}

// Rename moves a file, folder or symlink (the link itself); the target must not exist.
func (s *Service) Rename(ctx context.Context, req filesapi.RenameRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	from, err := cleanVisible(req.From)
	if err != nil {
		return err
	}
	to, err := cleanVisible(req.To)
	if err != nil {
		return err
	}
	if from == "" || to == "" {
		return fmt.Errorf("%w: the root cannot be renamed", filesapi.ErrInvalid)
	}
	if isServerProps(from) {
		// Under another name or in a subfolder its RCON password would no longer be redacted.
		return fmt.Errorf("%w: %s cannot be renamed or moved", filesapi.ErrInvalid, from)
	}
	if to == from || strings.HasPrefix(to, from+"/") {
		return fmt.Errorf("%w: %s cannot be moved into itself", filesapi.ErrInvalid, from)
	}
	root, err := s.open(req.ID)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := lstat(root, from); err != nil {
		return err
	}
	if err := requireDir(root, parentOf(to)); err != nil {
		return err
	}
	if _, err := lstat(root, to); err == nil {
		return fmt.Errorf("%w: %s", filesapi.ErrExists, to)
	} else if !errors.Is(err, filesapi.ErrNotFound) {
		return err
	}
	return fsErr(root.Rename(from, to))
}

// Delete removes a file, symlink (the link itself) or folder with its content.
func (s *Service) Delete(ctx context.Context, id int64, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clean, err := cleanVisible(p)
	if err != nil {
		return err
	}
	if clean == "" {
		return fmt.Errorf("%w: the root cannot be deleted", filesapi.ErrInvalid)
	}
	root, err := s.open(id)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := lstat(root, clean); err != nil {
		return err
	}
	return fsErr(root.RemoveAll(clean))
}
