package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// The restore journal (spec 4 §4.3) records where the renames of a restore swap stand, so that a
// service that dies in the middle of them can finish or undo them at its next start. It lives in
// <instances_dir>/backups/restore-journal/<id>: no container sees backups/, so only the service
// itself decides what is moved back or up. An entry exists only while entries are out of place;
// removing <tmp> afterwards is CleanupTemp's job: a temp folder in a data directory without a
// journal entry is an ordinary leftover (removed, never rolled back).
const (
	journalDir      = "restore-journal"
	maxJournalBytes = 4 << 10

	// phaseOldMoving: the current entries move to <tmp>/old; recovery moves them back.
	phaseOldMoving = "old_moving"
	// phaseNewMoving: every current entry is in <tmp>/old, the restored ones move up from
	// <tmp>/new; recovery moves the rest up (the snapshot was checked completely before).
	phaseNewMoving = "new_moving"
	// phaseStuck: a failed swap could not be undone (errRestoreStuck); <tmp> holds the previous
	// state and stays until a person resolves it (ResolveRecovery).
	phaseStuck = "stuck"
	// phaseUnreadable is reported for an entry that cannot be read or is not valid (it is never
	// written): the server needs recovery like a stuck one.
	phaseUnreadable = "unreadable"

	// journalListTTL: how long State reuses a listing of the journal folder (data safety spec
	// §2.1); the service's own writes drop it at once.
	journalListTTL = time.Second
)

// errJournalUnreadable: the journal folder cannot be read at all. The state of every server is
// unknown then (503 without a code, which the panel maps to "unknown"); nothing that rewrites a
// data directory may run.
var errJournalUnreadable = errors.New("the restore journal cannot be read")

// restoreJournal is one journal entry.
type restoreJournal struct {
	Tmp   string `json:"tmp"`   // top-level temp folder in the data directory
	Phase string `json:"phase"` // phaseOldMoving, phaseNewMoving, phaseStuck
}

// journalEntry is an entry as found on disk.
type journalEntry struct {
	restoreJournal
	since int64 // modification time of the entry in Unix ms; 0 = unknown
	// err: the entry exists but cannot be read or is not valid. Such an entry is never dropped
	// automatically; its server needs recovery and keeps all its top-level temp folders.
	err error
}

func (e journalEntry) recovery() filesapi.Recovery {
	if e.err != nil {
		return filesapi.Recovery{Phase: phaseUnreadable, Since: e.since}
	}
	return filesapi.Recovery{Phase: e.Phase, Since: e.since}
}

// journalList is a cached listing of the journal folder (journalEntryOf).
type journalList struct {
	at  time.Time
	ids map[int64]bool
	err error
}

func validJournal(j restoreJournal) bool {
	switch j.Phase {
	case phaseOldMoving, phaseNewMoving, phaseStuck:
	default:
		return false
	}
	clean, err := filesapi.CleanPath(j.Tmp)
	return err == nil && clean == j.Tmp && !strings.Contains(j.Tmp, "/") && strings.HasPrefix(j.Tmp, tempPrefix)
}

// openBackupsRoot opens backups/ (a real folder, never a symlink); ok is false when it does not
// exist.
func (s *Service) openBackupsRoot() (*os.Root, bool, error) {
	if err := realDir(s.backupsDir()); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	r, err := os.OpenRoot(s.backupsDir())
	if err != nil {
		return nil, false, err
	}
	return r, true, nil
}

// syncRootDir fsyncs folder name of r (EINVAL and ENOTSUP are accepted, see syncDir).
func syncRootDir(r *os.Root, name string) error {
	d, err := r.Open(name)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	if errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}

// writeJournal stores the journal entry of server id atomically: a new file, fsync, rename over
// the entry, fsync of the folder. All journal operations hold journalMu.
func (s *Service) writeJournal(id int64, j restoreJournal) error {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	s.journalList = nil
	r, ok, err := s.openBackupsRoot()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: the backup directory does not exist", filesapi.ErrBackupsUnavailable)
	}
	defer func() { _ = r.Close() }()
	if err := r.Mkdir(journalDir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if fi, err := r.Lstat(journalDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a real folder", journalDir)
	}
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}
	name := journalDir + "/" + strconv.FormatInt(id, 10)
	part := name + ".new"
	_ = r.Remove(part)
	f, err := r.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = r.Rename(part, name)
	}
	if err == nil {
		err = syncRootDir(r, journalDir)
	}
	if err != nil {
		_ = r.Remove(part)
		return fmt.Errorf("write the restore journal: %w", err)
	}
	return nil
}

// removeJournal deletes the journal entry of server id (and fsyncs the folder); a missing entry
// is fine.
func (s *Service) removeJournal(id int64) error {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	s.journalList = nil
	r, ok, err := s.openBackupsRoot()
	if err != nil || !ok {
		return err
	}
	defer func() { _ = r.Close() }()
	if err := r.Remove(journalDir + "/" + strconv.FormatInt(id, 10)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return syncRootDir(r, journalDir)
}

// errBadJournal: an entry that cannot be decoded or is not valid.
var errBadJournal = errors.New("broken restore journal entry")

// journalNames lists the canonical entry names (server ids) of the journal folder of the open
// backups/ root r; other names (".new" leftovers of unfinished writes, junk) are returned in
// other. A missing folder has no entries; a folder that cannot be listed (or is not a real
// folder) is errJournalUnreadable.
func journalNames(r *os.Root) (ids []int64, other []string, err error) {
	fi, err := r.Lstat(journalDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, nil
	case err != nil:
		return nil, nil, fmt.Errorf("%w: %w", errJournalUnreadable, err)
	case !fi.IsDir():
		return nil, nil, fmt.Errorf("%w: %s is not a real folder", errJournalUnreadable, journalDir)
	}
	d, err := r.Open(journalDir)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errJournalUnreadable, err)
	}
	names, err := d.Readdirnames(-1)
	_ = d.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errJournalUnreadable, err)
	}
	for _, n := range names {
		id, perr := strconv.ParseInt(n, 10, 64)
		if perr != nil || id <= 0 || strconv.FormatInt(id, 10) != n {
			other = append(other, n)
			continue
		}
		ids = append(ids, id)
	}
	return ids, other, nil
}

// readJournalEntry reads the journal entry of server id from the open backups/ root r; ok is false
// when it does not exist (any more).
func readJournalEntry(r *os.Root, id int64) (journalEntry, bool) {
	p := journalDir + "/" + strconv.FormatInt(id, 10)
	var e journalEntry
	if fi, err := r.Lstat(p); err == nil {
		e.since = fi.ModTime().UnixMilli()
	} else if errors.Is(err, fs.ErrNotExist) {
		return journalEntry{}, false
	}
	e.restoreJournal, e.err = readJournalFile(r, p)
	switch {
	case errors.Is(e.err, fs.ErrNotExist):
		return journalEntry{}, false
	case e.err == nil && !validJournal(e.restoreJournal):
		e.err = errBadJournal
	}
	if e.err != nil {
		e.restoreJournal = restoreJournal{}
	}
	return e, true
}

// readJournals returns the journal entries by server id, readable or not. With atStart
// (RecoverRestores only) leftovers of unfinished writes (".new") and other junk names are
// removed; entries themselves are never removed here. errJournalUnreadable when the folder
// cannot be listed.
func (s *Service) readJournals(atStart bool) (map[int64]journalEntry, error) {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	out := map[int64]journalEntry{}
	r, ok, err := s.openBackupsRoot()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errJournalUnreadable, err)
	}
	if !ok {
		return out, nil
	}
	defer func() { _ = r.Close() }()
	ids, other, err := journalNames(r)
	if err != nil {
		return nil, err
	}
	if atStart {
		for _, n := range other {
			_ = r.RemoveAll(journalDir + "/" + n) // a ".new" of a write that did not finish, or junk
		}
	}
	for _, id := range ids {
		if e, ok := readJournalEntry(r, id); ok {
			out[id] = e
		}
	}
	return out, nil
}

// journalEntryOf returns the journal entry of server id (ok = it exists, readable or not). The
// listing of the folder is reused for journalListTTL (every journal write of this process drops
// it); the entry itself is read on every call. errJournalUnreadable when the folder cannot be
// listed.
func (s *Service) journalEntryOf(id int64) (journalEntry, bool, error) {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	now := s.clock.Now()
	if l := s.journalList; l == nil || now.Sub(l.at) >= journalListTTL || now.Before(l.at) {
		s.journalList = s.listJournalLocked(now)
	}
	if s.journalList.err != nil {
		return journalEntry{}, false, s.journalList.err
	}
	if !s.journalList.ids[id] {
		return journalEntry{}, false, nil
	}
	r, ok, err := s.openBackupsRoot()
	if err != nil {
		return journalEntry{}, false, fmt.Errorf("%w: %w", errJournalUnreadable, err)
	}
	if !ok {
		return journalEntry{}, false, nil
	}
	defer func() { _ = r.Close() }()
	e, ok := readJournalEntry(r, id)
	return e, ok, nil
}

func (s *Service) listJournalLocked(now time.Time) *journalList {
	l := &journalList{at: now, ids: map[int64]bool{}}
	r, ok, err := s.openBackupsRoot()
	if err != nil {
		l.err = fmt.Errorf("%w: %w", errJournalUnreadable, err)
		return l
	}
	if !ok {
		return l
	}
	defer func() { _ = r.Close() }()
	ids, _, err := journalNames(r)
	l.err = err
	for _, id := range ids {
		l.ids[id] = true
	}
	return l
}

func readJournalFile(r *os.Root, p string) (restoreJournal, error) {
	fi, err := r.Lstat(p)
	if err != nil {
		return restoreJournal{}, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxJournalBytes {
		return restoreJournal{}, fmt.Errorf("%w: not a small regular file", errBadJournal)
	}
	f, err := r.Open(p)
	if err != nil {
		return restoreJournal{}, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxJournalBytes+1))
	if err != nil {
		return restoreJournal{}, err
	}
	var j restoreJournal
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return restoreJournal{}, fmt.Errorf("%w: %w", errBadJournal, err)
	}
	return j, nil
}

// RecoverRestores finishes or undoes the restore swaps that an earlier run of the service left
// in the journal (spec 4 §4.3). It runs once at start, before the service serves and before
// CleanupTemp; it only renames top-level entries (the temp folders are removed by CleanupTemp
// afterwards). It returns the number of entries it resolved; a stuck swap stays, one that
// cannot be resolved now is set to phaseStuck, and an entry that cannot be read stays as it is
// (its server needs recovery: State).
func (s *Service) RecoverRestores() int {
	entries, err := s.readJournals(true)
	if err != nil {
		s.log.Error("files: cannot read the restore journal; no server can be started or restored until it can be read", "err", err)
		return 0
	}
	n := 0
	for id, e := range entries {
		if e.err != nil {
			s.log.Error("files: cannot read a restore journal entry; it stays and the server needs recovery", "id", id, "err", e.err)
			continue
		}
		if s.recoverRestore(id, e.restoreJournal) {
			n++
		}
	}
	return n
}

// recoverRestore resolves one journal entry; false = it stays.
func (s *Service) recoverRestore(id int64, j restoreJournal) bool {
	if j.Phase == phaseStuck { // never resolved automatically
		s.log.Error("files: a previous restore could not be completed; the old state of the data directory is kept in a hidden folder. Check the data directory, then resolve the restore in the panel (it moves the folder to backups/recovery/)",
			"id", id, "folder", j.Tmp, "journal", journalDir+"/"+strconv.FormatInt(id, 10))
		return false
	}
	root, err := s.open(id)
	if errors.Is(err, filesapi.ErrNotFound) {
		s.log.Warn("files: dropped the restore journal of a server without a data directory", "id", id)
		return s.dropJournal(id)
	}
	if err != nil {
		s.log.Error("files: cannot open a data directory to recover its restore; the journal entry stays", "id", id, "err", err)
		return false
	}
	defer func() { _ = root.Close() }()
	fi, err := lstat(root, j.Tmp)
	switch {
	case errors.Is(err, filesapi.ErrNotFound):
		return s.dropJournal(id) // the temp folder is gone: nothing left to do
	case err != nil:
		s.log.Error("files: cannot look at the temp folder of an interrupted restore; the journal entry stays", "id", id, "folder", j.Tmp, "err", err)
		return false
	case !fi.IsDir():
		return s.dropJournal(id)
	}
	from, result := j.Tmp+"/old", "rolled back" // old_moving: put the previous state back
	if j.Phase == phaseNewMoving {
		from, result = j.Tmp+"/new", "completed" // the snapshot was checked completely before
	}
	moved, err := moveUp(root, from, s.cfg.Limits)
	if err == nil {
		err = syncRootDir(root, ".")
	}
	if err != nil {
		s.log.Error("files: cannot recover an interrupted restore; the old state is kept in a hidden folder", "id", id, "folder", j.Tmp, "phase", j.Phase, "err", err)
		if werr := s.writeJournal(id, restoreJournal{Tmp: j.Tmp, Phase: phaseStuck}); werr != nil {
			s.log.Error("files: cannot mark the restore journal as stuck", "id", id, "err", werr)
		}
		return false
	}
	s.log.Warn("files: recovered an interrupted restore", "id", id, "result", result, "entries", moved)
	return s.dropJournal(id)
}

func (s *Service) dropJournal(id int64) bool {
	if err := s.removeJournal(id); err != nil {
		s.log.Error("files: cannot remove a restore journal entry", "id", id, "err", err)
		return false
	}
	return true
}

// moveUp moves every visible entry of folder from to the top level of root. An entry of that
// name at the top is replaced only if it is not a folder (rename semantics); a folder there
// fails the move.
func moveUp(root *os.Root, from string, lim config.FilesLimits) (int, error) {
	fi, err := lstat(root, from)
	if errors.Is(err, filesapi.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !fi.IsDir() {
		return 0, fmt.Errorf("%s is not a folder", from)
	}
	for _, d := range []string{".", from} {
		if err := unlockDir(root, d); err != nil {
			return 0, err
		}
	}
	names, err := visibleNames(root, from, lim)
	if err != nil {
		return 0, err
	}
	for i, n := range names {
		if err := unlockDir(root, from+"/"+n); err != nil { // a folder's ".." changes
			return i, err
		}
		if err := root.Rename(from+"/"+n, n); err != nil {
			return i, fsErr(err)
		}
	}
	return len(names), nil
}

// tempKeep says which top-level temp folders CleanupTemp must leave alone because the journal
// still refers to them after RecoverRestores (stuck restores, entries that could not be read or
// resolved).
type tempKeep struct {
	all     bool             // the journal cannot be read: every top-level temp entry stays
	tmp     map[int64]string // the temp folder of a readable entry
	unknown map[int64]bool   // an entry that cannot be read: every top-level temp entry stays
}

// keeps reports whether the temp entry p (a path in the data directory of server id) stays.
func (k tempKeep) keeps(id int64, p string) bool {
	if strings.Contains(p, "/") {
		return false // restores only use top-level temp folders
	}
	return k.all || k.unknown[id] || k.tmp[id] == p
}

func (s *Service) journaledTemps() tempKeep {
	entries, err := s.readJournals(false)
	if err != nil {
		s.log.Error("files: cannot read the restore journal; temp cleanup keeps every top-level temp folder", "err", err)
		return tempKeep{all: true}
	}
	k := tempKeep{tmp: map[int64]string{}, unknown: map[int64]bool{}}
	for id, e := range entries {
		if e.err != nil {
			k.unknown[id] = true
		} else {
			k.tmp[id] = e.Tmp
		}
	}
	return k
}

// purgeJournal removes the journal entry of server id with its backups (PurgeBackups), also a
// stuck one: the server is gone. r is the open backups/ root.
func (s *Service) purgeJournal(r *os.Root, id int64) error {
	s.journalMu.Lock()
	defer s.journalMu.Unlock()
	s.journalList = nil
	if err := r.Remove(journalDir + "/" + strconv.FormatInt(id, 10)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
