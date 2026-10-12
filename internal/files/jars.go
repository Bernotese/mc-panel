package files

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// jarFile is a jar found on the top level of the folder.
type jarFile struct {
	name string
	fi   fs.FileInfo // Lstat when it was found
}

// Jars lists the plugin or mod jars on the top level of dir (spec 3c §5.1): regular files
// named *.jar or *.jar.disabled (any case), never symlinks, folders or special files, never
// hidden names; at most filesapi.MaxJars (Truncated), sorted by name. Each comes with sha256
// and sha512 (not above jarHashMax) and best-effort metadata, cached by file identity. A
// missing data directory or dir, or a dir that is not a real folder, is an empty list.
func (s *Service) Jars(ctx context.Context, id int64, dir string) (filesapi.JarsResponse, error) {
	if err := ctx.Err(); err != nil {
		return filesapi.JarsResponse{}, err
	}
	if !filesapi.ValidJarDir(dir) {
		return filesapi.JarsResponse{}, fmt.Errorf("%w: dir must be %q or %q", filesapi.ErrInvalid, filesapi.DirPlugins, filesapi.DirMods)
	}
	res := filesapi.JarsResponse{Jars: []filesapi.Jar{}}
	root, err := s.open(id)
	if errors.Is(err, filesapi.ErrNotFound) {
		return res, nil // the server never ran: no data directory, no jars
	}
	if err != nil {
		return filesapi.JarsResponse{}, err
	}
	defer root.Close()
	found, truncated, err := findJars(root, dir)
	if err != nil {
		return filesapi.JarsResponse{}, err
	}
	res.Truncated = truncated
	for _, jf := range found {
		jar, ok, err := s.describeJar(ctx, root, dir, jf)
		if err != nil {
			return filesapi.JarsResponse{}, err
		}
		if ok {
			res.Jars = append(res.Jars, jar)
		}
	}
	return res, nil
}

// findJars returns at most filesapi.MaxJars regular jar files on the top level of dir, sorted
// by name (byte order); truncated reports that there were more. Like List it stops reading
// at the limit, so which jars are left out follows the directory order.
func findJars(root *os.Root, dir string) ([]jarFile, bool, error) {
	d, _, err := openDir(root, dir)
	if errors.Is(err, filesapi.ErrNotFound) || errors.Is(err, filesapi.ErrNotRegular) {
		return []jarFile{}, false, nil // no folder, a file or a symlink: no jars
	}
	if err != nil {
		return nil, false, err
	}
	defer d.Close()
	out := []jarFile{}
	truncated := false
	for !truncated {
		batch, rerr := d.ReadDir(listBatch)
		for _, e := range batch {
			p := filesapi.Join(dir, e.Name())
			if _, ok := filesapi.IsJarName(e.Name()); !ok || filesapi.IsHidden(p) {
				continue
			}
			fi, lerr := lstat(root, p)
			if lerr != nil || !fi.Mode().IsRegular() {
				continue // vanished meanwhile, a symlink, a folder, a FIFO or a device
			}
			if len(out) == filesapi.MaxJars {
				truncated = true
				break
			}
			out = append(out, jarFile{name: e.Name(), fi: fi})
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && len(batch) == 0) {
			break
		}
		if rerr != nil {
			return nil, false, fsErr(rerr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, truncated, nil
}

// describeJar returns the entry of one jar; ok is false when it is no longer a regular file
// (removed, or swapped for a symlink or FIFO after findJars saw it). A jar the service cannot
// open for another reason (permissions) is listed without checksums and metadata.
func (s *Service) describeJar(ctx context.Context, root *os.Root, dir string, jf jarFile) (filesapi.Jar, bool, error) {
	enabled, _ := filesapi.IsJarName(jf.name)
	jar := filesapi.Jar{Name: jf.name, Enabled: enabled, Size: jf.fi.Size(), MTime: jf.fi.ModTime().UnixMilli()}
	p := filesapi.Join(dir, jf.name)
	f, st, err := openRegular(root, p, os.O_RDONLY)
	switch {
	case errors.Is(err, filesapi.ErrNotFound), errors.Is(err, filesapi.ErrNotRegular):
		return filesapi.Jar{}, false, nil
	case err != nil:
		s.log.Warn("files: cannot open jar", "path", p, "err", err)
		return jar, true, nil
	}
	defer f.Close()
	jar.Size, jar.MTime = st.Size(), st.ModTime().UnixMilli()
	info, err := s.jarInfo(ctx, jf.name, f, st)
	if err != nil {
		return filesapi.Jar{}, false, err
	}
	jar.SHA256, jar.SHA512 = info.sha256, info.sha512
	if info.meta != nil {
		m := *info.meta // the cached value stays untouched
		jar.Meta = &m
	}
	return jar, true, nil
}

// jarInfo returns checksums and metadata of the open jar f, from the cache when its identity
// is known. A computation takes one of maxHashRuns slots; a request waits for a slot as long
// as its context lives. A jar that changed while it was read gets nothing and is not cached.
func (s *Service) jarInfo(ctx context.Context, name string, f *os.File, st fs.FileInfo) (jarInfo, error) {
	key, keyed := fileKeyOf(st)
	if keyed {
		if info, ok := s.jarCache.get(key); ok {
			return info, nil
		}
	}
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	case <-ctx.Done():
		return jarInfo{}, ctx.Err()
	}
	if keyed { // computed by another request while this one waited
		if info, ok := s.jarCache.get(key); ok {
			return info, nil
		}
	}
	if s.jarHook != nil {
		s.jarHook(name)
	}
	var info jarInfo
	cacheable := keyed
	if st.Size() <= s.jarHashMax {
		h256, h512, err := hashJar(ctx, f, st.Size())
		switch {
		case ctx.Err() != nil:
			return jarInfo{}, ctx.Err()
		case err != nil:
			s.log.Warn("files: cannot hash jar", "name", name, "err", err)
			cacheable = false
		default:
			info.sha256, info.sha512 = h256, h512
		}
	}
	// A jar that is written to while it is read has no reliable checksum.
	if now, err := f.Stat(); err != nil || now.Size() != st.Size() || !now.ModTime().Equal(st.ModTime()) {
		return jarInfo{}, nil
	}
	if cacheable {
		s.jarCache.put(key, info)
	}
	return info, nil
}

// hashJar computes sha256 and sha512 of the first size bytes of r in one pass; it stops as
// soon as ctx ends (ctxReader from extract.go; the caller checks ctx.Err()).
func hashJar(ctx context.Context, r io.ReaderAt, size int64) (string, string, error) {
	h256, h512 := sha256.New(), sha512.New()
	src := ctxReader{ctx: ctx, r: io.NewSectionReader(r, 0, size)}
	if _, err := io.CopyN(io.MultiWriter(h256, h512), src, size); err != nil {
		return "", "", err
	}
	return hex.EncodeToString(h256.Sum(nil)), hex.EncodeToString(h512.Sum(nil)), nil
}
