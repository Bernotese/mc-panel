package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"sync"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// fakeBackups is the backup side of mcpanel-files: a running job ends at the first poll unless
// its plan ("manual", "scheduled", "pre-restore", "restore", "forget") is held.
type fakeBackups struct {
	mu     sync.Mutex
	snaps  map[int64][]filesapi.Snapshot
	jobs   map[int64]*filesapi.Job
	plans  map[int64]string
	kinds  map[int64]filesapi.SnapshotKind
	notes  map[int64]string
	hold   map[string]chan struct{}
	purges []int64
	seq    int
	zip    []byte
}

func newFakeBackups() *fakeBackups {
	return &fakeBackups{
		snaps: map[int64][]filesapi.Snapshot{}, jobs: map[int64]*filesapi.Job{}, plans: map[int64]string{},
		kinds: map[int64]filesapi.SnapshotKind{}, notes: map[int64]string{}, hold: map[string]chan struct{}{},
		zip: []byte("PK\x05\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
	}
}

func (f *fakeBackups) holdPlan(plan string) func() {
	ch := make(chan struct{})
	f.mu.Lock()
	f.hold[plan] = ch
	f.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func (f *fakeBackups) purged(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.purges, id)
}

func (f *fakeBackups) start(id int64, kind filesapi.JobKind, plan string) (filesapi.Job, error) {
	if j := f.jobs[id]; j != nil && j.State == filesapi.JobRunning {
		return filesapi.Job{}, filesapi.ErrBusy
	}
	f.seq++
	j := &filesapi.Job{ID: fmt.Sprintf("job%d", f.seq), Kind: kind, State: filesapi.JobRunning}
	f.jobs[id], f.plans[id] = j, plan
	return *j, nil
}

func (f *fakeBackups) has(id int64, snap string) bool {
	return slices.ContainsFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return s.ID == snap })
}

func (f *fakeBackups) BackupsStatus(context.Context) (filesapi.BackupsStatus, error) {
	return filesapi.BackupsStatus{Available: true, Version: "0.19.1"}, nil
}

func (f *fakeBackups) Snapshots(_ context.Context, id int64) ([]filesapi.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := slices.Clone(f.snaps[id])
	if out == nil {
		out = []filesapi.Snapshot{}
	}
	return out, nil
}

func (f *fakeBackups) CreateBackup(_ context.Context, req filesapi.CreateBackupRequest) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, err := f.start(req.ID, filesapi.JobBackup, string(req.Kind))
	f.kinds[req.ID], f.notes[req.ID] = req.Kind, req.Note
	return j, err
}

func (f *fakeBackups) RestoreBackup(_ context.Context, id int64, snap string) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has(id, snap) {
		return filesapi.Job{}, filesapi.ErrNotFound
	}
	return f.start(id, filesapi.JobRestore, "restore")
}

func (f *fakeBackups) RestoreFrom(_ context.Context, target, source int64, snap string) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has(source, snap) {
		return filesapi.Job{}, filesapi.ErrNotFound
	}
	return f.start(target, filesapi.JobRestore, "restore")
}

func (f *fakeBackups) ForgetBackups(_ context.Context, req filesapi.ForgetRequest) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := req.Validate(); err != nil {
		return filesapi.Job{}, err
	}
	f.snaps[req.ID] = slices.DeleteFunc(f.snaps[req.ID], func(s filesapi.Snapshot) bool { return slices.Contains(req.Snapshots, s.ID) })
	return f.start(req.ID, filesapi.JobForget, "forget")
}

func (f *fakeBackups) PinBackup(_ context.Context, id int64, snap string, pinned bool) (filesapi.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return s.ID == snap })
	if i < 0 {
		return filesapi.Snapshot{}, filesapi.ErrNotFound
	}
	f.seq++
	s := f.snaps[id][i]
	s.ID, s.Pinned = fmt.Sprintf("%064x", f.seq), pinned
	s.ShortID = s.ID[:8]
	f.snaps[id][i] = s
	return s, nil
}

func (f *fakeBackups) DownloadBackup(_ context.Context, id int64, snap string) (filesapi.Download, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.has(id, snap) {
		return filesapi.Download{}, filesapi.ErrNotFound
	}
	return filesapi.Download{Body: io.NopCloser(bytes.NewReader(f.zip)), Size: -1, IsDir: true}, nil
}

func (f *fakeBackups) PurgeBackups(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.purges = append(f.purges, id)
	delete(f.snaps, id)
	return nil
}

func (f *fakeBackups) CancelJob(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j := f.jobs[id]; j != nil && j.State == filesapi.JobRunning {
		j.State, j.Error = filesapi.JobFailed, "canceled"
	}
	return nil
}

func (f *fakeBackups) Job(_ context.Context, id int64) (*filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[id]
	if j == nil {
		return nil, nil
	}
	if j.State == filesapi.JobRunning {
		held := false
		if ch, ok := f.hold[f.plans[id]]; ok {
			select {
			case <-ch:
			default:
				held = true
			}
		}
		if !held {
			j.State = filesapi.JobDone
			if j.Kind == filesapi.JobBackup {
				f.seq++
				sid := fmt.Sprintf("%064x", f.seq)
				f.snaps[id] = append([]filesapi.Snapshot{{ID: sid, ShortID: sid[:8], Time: 1_760_270_400_000, Kind: f.kinds[id], Note: f.notes[id], Bytes: 2048}}, f.snaps[id]...)
				j.Snapshot = sid
			}
		}
	}
	out := *j
	return &out, nil
}

var _ filesapi.BackupClient = (*fakeBackups)(nil)
