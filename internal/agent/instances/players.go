package instances

import (
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

	var ops []opEntry
	if m.readPlayerFile(root, id, opsFile, &ops, &resp) {
		for _, o := range ops[:min(len(ops), maxPlayerEntries)] {
			resp.Ops = append(resp.Ops, agentapi.PlayerOp{UUID: clipText(o.UUID), Name: clipText(o.Name), Level: o.Level})
		}
	}
	var wl []whitelistEntry
	if m.readPlayerFile(root, id, whitelistFile, &wl, &resp) {
		for _, w := range wl[:min(len(wl), maxPlayerEntries)] {
			resp.Whitelist = append(resp.Whitelist, agentapi.PlayerEntry{UUID: clipText(w.UUID), Name: clipText(w.Name)})
		}
	}
	var bans []banEntry
	if m.readPlayerFile(root, id, bansFile, &bans, &resp) {
		for _, b := range bans[:min(len(bans), maxPlayerEntries)] {
			resp.Bans = append(resp.Bans, agentapi.PlayerBan{
				UUID: clipText(b.UUID), Name: clipText(b.Name), Reason: clipText(b.Reason),
				Source: clipText(b.Source), Created: clipText(b.Created), Expires: clipText(b.Expires),
			})
		}
	}
	return resp, nil
}

// readPlayerFile decodes the JSON list name into dst. It returns false when there is
// nothing to use; an unreadable file is logged (never its content) and added to Errors.
func (m *Manager) readPlayerFile(root *os.Root, id int64, name string, dst any, resp *agentapi.PlayersResponse) bool {
	data, err := readUntrustedFile(root, name, maxPlayerFileBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err == nil {
		err = json.Unmarshal(data, dst) // a JSON object instead of a list fails here
	}
	if err != nil {
		m.log.Warn("player list unreadable", "id", id, "file", name, "err", err)
		resp.Errors = append(resp.Errors, name)
		return false
	}
	return true
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

// clipText cuts s to maxPlayerTextRunes runes (json.Unmarshal already replaced invalid UTF-8).
func clipText(s string) string { return firstRunes(s, maxPlayerTextRunes) }
