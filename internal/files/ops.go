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
	"time"
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
	return filesapi.ReadResponse{Content: content, MTime: st.ModTime().UnixMilli(), Size: st.Size(), Version: versionOf(st)}, nil
}

// Write saves editor content atomically (temp file, fsync, rename/link). Its base (expected
// mtime and/or version, none = a new file) is checked before staging and again under the
// commit lock right before the rename, so of two saves with the same base only one succeeds.
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
	release, err := s.lease(req.ID)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	defer release()
	root, err := s.open(req.ID)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	defer root.Close()
	if err := requireDir(root, parentOf(clean)); err != nil {
		return filesapi.WriteResponse{}, err
	}

	// An early check, so a conflict costs no temp copy; the base is checked again at commit.
	fi, err := checkBase(root, clean, req)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	content := req.Content
	if isServerProps(clean) {
		cur := []byte(nil) // a new file never gets the placeholder as its password
		if fi != nil {
			cur, _, err = readSmall(root, clean, s.cfg.Limits.MaxTextBytes)
			if err != nil && !errors.Is(err, filesapi.ErrTooLarge) {
				return filesapi.WriteResponse{}, err
			}
		}
		content = restorePassword(content, string(cur))
	}
	// The temp copy exists next to the old file until the rename, so the whole new content
	// needs the reserve (SEC-04, #153), even when the file shrinks: a save refused here leaves
	// the old file as it was. An empty file stages no data.
	var res *reservation
	if n := int64(len(content)); n > 0 {
		res, err = s.reserve(req.ID, n)
		if err != nil {
			return filesapi.WriteResponse{}, err
		}
		defer res.release()
	}
	tmp, staged, err := s.stage(root, clean, []byte(content), res)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	committed := false
	defer func() {
		if !committed {
			// By path: if the parent folder was renamed meanwhile, the temp file stays (hidden)
			// until CleanupTemp removes it at the next start.
			_ = root.Remove(tmp)
		}
	}()
	if s.commitHook != nil {
		s.commitHook(clean)
	}
	unlock, err := s.commits.lock(ctx, req.ID)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	defer unlock()
	// Under the commit lock, nothing the service does changes clean between this check and
	// the rename (#149); the server's own writes are not covered.
	cur, err := checkBase(root, clean, req)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	if cur != nil {
		if err := moveMTimeForward(root, tmp, staged, cur); err != nil {
			return filesapi.WriteResponse{}, err
		}
	}
	if err := commit(root, tmp, clean, cur != nil); err != nil {
		return filesapi.WriteResponse{}, err
	}
	committed = true
	st, err := lstat(root, clean)
	if err != nil {
		return filesapi.WriteResponse{}, err
	}
	return filesapi.WriteResponse{MTime: st.ModTime().UnixMilli(), Size: st.Size(), Version: versionOf(st)}, nil
}

// stage writes data to a fresh temp file next to clean and syncs it; it returns the temp name
// and its mtime. The bytes land on res (nil: none reserved). On error nothing is left behind.
func (s *Service) stage(root *os.Root, clean string, data []byte, res *reservation) (string, time.Time, error) {
	tmp, err := s.tempName(parentOf(clean))
	if err != nil {
		return "", time.Time{}, err
	}
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode) //nolint:gosec // G302/G304: new temp file in the instance's os.Root, 0644 like the server's own files
	if err != nil {
		return "", time.Time{}, fsErr(err)
	}
	n, err := f.Write(data)
	if res != nil {
		res.landed(int64(n))
	}
	if err == nil {
		err = f.Sync()
	}
	var st fs.FileInfo
	if err == nil {
		st, err = f.Stat()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = root.Remove(tmp)
		return "", time.Time{}, fsErr(err)
	}
	return tmp, st.ModTime(), nil
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
	release, err := s.lease(id)
	if err != nil {
		return err
	}
	defer release()
	root, err := s.open(id)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := requireDir(root, parentOf(clean)); err != nil {
		return err
	}
	if s.commitHook != nil {
		s.commitHook(clean)
	}
	// Under the commit lock, so a rename's fallback check cannot miss a folder made meanwhile.
	unlock, err := s.commits.lock(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()
	return fsErr(root.Mkdir(clean, dirMode)) //nolint:gosec // G301: folders are 0755 like the server's own (umask 0027 applies)
}

// Rename moves a file, folder or symlink (the link itself); the target must not exist, and a
// target that appears meanwhile is never replaced (renameNoReplace).
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
	release, err := s.lease(req.ID)
	if err != nil {
		return err
	}
	defer release()
	root, err := s.open(req.ID)
	if err != nil {
		return err
	}
	defer root.Close()
	if s.commitHook != nil {
		s.commitHook(from)
	}
	unlock, err := s.commits.lock(ctx, req.ID)
	if err != nil {
		return err
	}
	defer unlock()
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
	if s.renameHook != nil {
		s.renameHook()
	}
	// The check above gives the clear answer; only this rename guarantees that a target
	// created meanwhile (by the server) is never replaced (#149).
	return s.renameNoReplace(root, from, to)
}

// Delete removes a file, symlink (the link itself) or folder with its content. Under the commit
// lock it only moves the target to a hidden temp name next to it, so a save of a file in it
// cannot bring the file back afterwards; the removal itself, which can take minutes for a
// world, runs outside the lock. A removal that fails leaves the hidden temp entry, which
// CleanupTemp removes at the next start.
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
	release, err := s.lease(id)
	if err != nil {
		return err
	}
	defer release()
	root, err := s.open(id)
	if err != nil {
		return err
	}
	defer root.Close()
	if s.commitHook != nil {
		s.commitHook(clean)
	}
	tmp, err := s.moveAside(ctx, root, id, clean)
	if err != nil {
		return err
	}
	if s.deleteHook != nil {
		s.deleteHook()
	}
	return fsErr(root.RemoveAll(tmp))
}

// moveAside renames clean to a fresh hidden temp name in its folder under the commit lock.
func (s *Service) moveAside(ctx context.Context, root *os.Root, id int64, clean string) (string, error) {
	unlock, err := s.commits.lock(ctx, id)
	if err != nil {
		return "", err
	}
	defer unlock()
	if _, err := lstat(root, clean); err != nil {
		return "", err
	}
	tmp, err := s.tempName(parentOf(clean))
	if err != nil {
		return "", err
	}
	if err := s.renameNoReplace(root, clean, tmp); err != nil {
		return "", err
	}
	return tmp, nil
}
