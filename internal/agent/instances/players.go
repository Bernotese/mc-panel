package instances

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	maxPlayerFileBytes = 1 << 20
	maxPlayerEntries   = 1000
	maxPlayerTextRunes = 256

	opsFile       = "ops.json"
	whitelistFile = "whitelist.json"
	bansFile      = "banned-players.json"
)

type opEntry struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Level int    `json:"level"`
}

type whitelistEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type banEntry struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
	Source  string `json:"source"`
	Created string `json:"created"`
	Expires string `json:"expires"`
}

// Players reads ops.json, whitelist.json and banned-players.json of instance id (spec
// §5.4); it works with a stopped or missing container. The files belong to the container
// user and are untrusted: an unreadable one (symlink, not a regular file, above 1 MiB,
// no JSON list) is named in Errors and its list stays empty; a missing one is an empty
// list. agentapi.ErrNotFound if the data directory does not exist.
func (m *Manager) Players(_ context.Context, id int64) (agentapi.PlayersResponse, error) {
	resp := agentapi.PlayersResponse{
		Ops: []agentapi.PlayerOp{}, Whitelist: []agentapi.PlayerEntry{}, Bans: []agentapi.PlayerBan{}, Errors: []string{},
	}
	if id <= 0 {
		return resp, fmt.Errorf("%w: players: id must be > 0", instancespec.ErrInvalid)
	}
	dir := DataDir(m.host.InstancesDir, id)
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return resp, fmt.Errorf("%w: data directory of instance %d does not exist", agentapi.ErrNotFound, id)
	case err != nil:
		return resp, err
	case !fi.IsDir(): // also a symlink: Lstat does not follow it
		return resp, fmt.Errorf("data directory %s is not a directory", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return resp, err
	}
	defer func() { _ = root.Close() }()
	// instances/ is root-only, so <id> cannot be swapped after the Lstat; check anyway.
	if opened, err := root.Stat("."); err != nil || !os.SameFile(fi, opened) {
		return resp, fmt.Errorf("data directory %s changed while it was opened", dir)
	}

	if ops, ok := readPlayerFile[opEntry](m, root, id, opsFile, &resp); ok {
		for _, o := range ops {
			resp.Ops = append(resp.Ops, agentapi.PlayerOp{UUID: clipText(o.UUID), Name: clipText(o.Name), Level: o.Level})
		}
	}
	if wl, ok := readPlayerFile[whitelistEntry](m, root, id, whitelistFile, &resp); ok {
		for _, w := range wl {
			resp.Whitelist = append(resp.Whitelist, agentapi.PlayerEntry{UUID: clipText(w.UUID), Name: clipText(w.Name)})
		}
	}
	if bans, ok := readPlayerFile[banEntry](m, root, id, bansFile, &resp); ok {
		for _, b := range bans {
			resp.Bans = append(resp.Bans, agentapi.PlayerBan{
				UUID: clipText(b.UUID), Name: clipText(b.Name), Reason: clipText(b.Reason),
				Source: clipText(b.Source), Created: clipText(b.Created), Expires: clipText(b.Expires),
			})
		}
	}
	return resp, nil
}

// readPlayerFile decodes the first maxPlayerEntries entries of the JSON list name. It returns
// false when there is nothing to use; an unreadable file is logged (never its content) and
// added to Errors.
func readPlayerFile[T any](m *Manager, root *os.Root, id int64, name string, resp *agentapi.PlayersResponse) ([]T, bool) {
	data, err := readUntrustedFile(root, name, maxPlayerFileBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	var list []T
	if err == nil {
		list, err = decodeList[T](data, maxPlayerEntries)
	}
	if err != nil {
		m.log.Warn("player list unreadable", "id", id, "file", name, "err", err)
		resp.Errors = append(resp.Errors, name)
		return nil, false
	}
	return list, true
}

// decodeList decodes a JSON list (null is an empty one) entry by entry and stops after limit
// entries: the rest is neither decoded nor checked, so a 1 MiB file of tiny entries never
// becomes hundreds of thousands of values. Up to the limit it is as strict as json.Unmarshal
// (no other top-level value, nothing after the list).
func decodeList[T any](data []byte, limit int) ([]T, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok != nil { // null: no list
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return nil, errors.New("not a JSON list")
		}
		list := []T{}
		for dec.More() {
			if len(list) == limit {
				return list, nil
			}
			var v T
			if err := dec.Decode(&v); err != nil {
				return nil, err
			}
			list = append(list, v)
		}
		if _, err := dec.Token(); err != nil { // the closing ]
			return nil, err
		}
		if _, err := dec.Token(); !errors.Is(err, io.EOF) {
			return nil, errors.New("data after the JSON list")
		}
		return list, nil
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("data after the JSON value")
	}
	return nil, nil
}

// readUntrustedFile reads name below root: only a regular file (no symlink, not even one
// pointing inside root) of at most limit bytes. O_NONBLOCK keeps a FIFO swapped in after
// the Lstat from blocking the open; SameFile catches any swap between Lstat and open.
func readUntrustedFile(root *os.Root, name string, limit int64) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (%v)", name, before.Mode().Type())
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: fixed file name inside the data directory; os.Root prevents leaving it
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("%s changed while it was opened", name)
	}
	if after.Size() > limit {
		return nil, fmt.Errorf("%s has %d bytes, more than %d", name, after.Size(), limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s grew beyond %d bytes", name, limit)
	}
	return data, nil
}

// clipText cuts s to maxPlayerTextRunes runes (the JSON decoder already replaced invalid UTF-8).
func clipText(s string) string { return firstRunes(s, maxPlayerTextRunes) }
