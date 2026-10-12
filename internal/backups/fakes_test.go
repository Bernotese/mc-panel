package backups_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// fakeFiles is an in-memory mcpanel-files for the backup endpoints. A service job starts as
// running and ends at the first Job poll, unless hold[plan] is an open channel (then it runs
// until the channel is closed). plan is the snapshot kind for create ("manual", "scheduled",
// "pre-restore"), "restore" or "forget". fail[plan] ends such a job as failed with that text and
// failCode[plan] as its wire code (filesapi.Job.Code, "" = none).
type fakeFiles struct {
	mu        sync.Mutex
	clk       *clock.Fake
	status    filesapi.BackupsStatus
	snaps     map[int64][]filesapi.Snapshot // newest first
	jobs      map[int64]*fakeJob            // last job per instance
	hold      map[string]chan struct{}
	fail      map[string]string
	failCode  map[string]string
	createTZ  map[int64]string   // Timezone of the last CreateBackup per server
	errs      map[string]error   // by operation: status, list, create, restore, forget, pin, download, purge, cancel, job
	errQueue  map[string][]error // used up one per call before errs
	loseJobs  bool               // Job answers nil (a restarted service)
	calls     []string
	seq       int
	zip       []byte
	dlBody    func() io.ReadCloser // overrides the download body
	downloads int                  // DownloadBackup calls that returned a body
}

type fakeJob struct {
	job       filesapi.Job
	plan      string
	snapKind  filesapi.SnapshotKind
	note      string
	forgetIDs []string
	restoreOf string
}

func newFakeFiles(clk *clock.Fake) *fakeFiles {
	return &fakeFiles{
		clk: clk, status: filesapi.BackupsStatus{Available: true, Version: "0.19.1"},
		snaps: map[int64][]filesapi.Snapshot{}, jobs: map[int64]*fakeJob{}, hold: map[string]chan struct{}{},
		fail: map[string]string{}, failCode: map[string]string{}, createTZ: map[int64]string{}, errs: map[string]error{}, errQueue: map[string][]error{},
		zip: []byte("PK\x05\x06" + strings.Repeat("\x00", 18)), // an empty zip archive
	}
}

func short(id string) string { return id[:8] }

// errFor returns the injected error of op (queue first); call with f.mu held.
func (f *fakeFiles) errFor(op string) error {
	if q := f.errQueue[op]; len(q) > 0 {
		f.errQueue[op] = q[1:]
		return q[0]
	}
	return f.errs[op]
}

// add seeds a snapshot and returns its id.
func (f *fakeFiles) add(id int64, kind filesapi.SnapshotKind, pinned bool, note string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addLocked(id, kind, pinned, note)
}

func (f *fakeFiles) addLocked(id int64, kind filesapi.SnapshotKind, pinned bool, note string) string {
	f.seq++
	sid := fmt.Sprintf("%064x", f.seq)
	s := filesapi.Snapshot{ID: sid, ShortID: sid[:8], Time: f.clk.Now().UnixMilli(), Kind: kind, Pinned: pinned, Note: note, Bytes: 4096}
	f.snaps[id] = append([]filesapi.Snapshot{s}, f.snaps[id]...)
	return sid
}

// holdPlan keeps the next jobs of plan running; the returned func releases them.
func (f *fakeFiles) holdPlan(plan string) func() {
	ch := make(chan struct{})
	f.mu.Lock()
	f.hold[plan] = ch
	f.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func (f *fakeFiles) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeFiles) count(prefix string) int {
	n := 0
	for _, c := range f.callLog() {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (f *fakeFiles) list(id int64) []filesapi.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.snaps[id])
}

// start creates a running job; call with f.mu held.
func (f *fakeFiles) start(id int64, kind filesapi.JobKind, plan string) (*fakeJob, error) {
	if j := f.jobs[id]; j != nil && j.job.State == filesapi.JobRunning {
		return nil, fmt.Errorf("%w: a job is running for this server", filesapi.ErrBusy)
	}
	f.seq++
	j := &fakeJob{plan: plan, job: filesapi.Job{
		ID: fmt.Sprintf("job%d", f.seq), Kind: kind, State: filesapi.JobRunning, StartedAt: f.clk.Now().UnixMilli(),
	}}
	f.jobs[id] = j
	return j, nil
}

func (f *fakeFiles) BackupsStatus(context.Context) (filesapi.BackupsStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errFor("status"); err != nil {
		return filesapi.BackupsStatus{}, err
	}
	return f.status, nil
}

func (f *fakeFiles) Snapshots(_ context.Context, id int64) ([]filesapi.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errFor("list"); err != nil {
		return nil, err
	}
	out := slices.Clone(f.snaps[id])
	if out == nil {
		out = []filesapi.Snapshot{}
	}
	return out, nil
}

func (f *fakeFiles) CreateBackup(_ context.Context, req filesapi.CreateBackupRequest) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("create %d %s", req.ID, req.Kind))
	f.createTZ[req.ID] = req.Timezone
	if err := f.errFor("create"); err != nil {
		return filesapi.Job{}, err
	}
	j, err := f.start(req.ID, filesapi.JobBackup, string(req.Kind))
	if err != nil {
		return filesapi.Job{}, err
	}
	j.snapKind, j.note = req.Kind, req.Note
	return j.job, nil
}

func (f *fakeFiles) RestoreBackup(_ context.Context, id int64, snapshot string) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("restore %d %s", id, short(snapshot)))
	if err := f.errFor("restore"); err != nil {
		return filesapi.Job{}, err
	}
	if !slices.ContainsFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return s.ID == snapshot }) {
		return filesapi.Job{}, filesapi.ErrNotFound
	}
	j, err := f.start(id, filesapi.JobRestore, "restore")
	if err != nil {
		return filesapi.Job{}, err
	}
	j.restoreOf = snapshot
	j.job.Snapshot = snapshot
	return j.job, nil
}

func (f *fakeFiles) ForgetBackups(_ context.Context, req filesapi.ForgetRequest) (filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Policy != nil {
		p := req.Policy
		f.calls = append(f.calls, fmt.Sprintf("forget %d policy %d/%d/%d/%d %s", req.ID, p.KeepLast, p.KeepDaily, p.KeepWeekly, p.KeepMonthly, req.Timezone))
	} else {
		ids := make([]string, len(req.Snapshots))
		for i, s := range req.Snapshots {
			ids[i] = short(s)
		}
		f.calls = append(f.calls, fmt.Sprintf("forget %d ids %s", req.ID, strings.Join(ids, ",")))
	}
	if err := req.Validate(); err != nil {
		return filesapi.Job{}, err
	}
	if err := f.errFor("forget"); err != nil {
		return filesapi.Job{}, err
	}
	for _, s := range req.Snapshots {
		if !slices.ContainsFunc(f.snaps[req.ID], func(x filesapi.Snapshot) bool { return x.ID == s }) {
			return filesapi.Job{}, filesapi.ErrNotFound
		}
	}
	j, err := f.start(req.ID, filesapi.JobForget, "forget")
	if err != nil {
		return filesapi.Job{}, err
	}
	j.forgetIDs = slices.Clone(req.Snapshots)
	return j.job, nil
}

func (f *fakeFiles) PinBackup(_ context.Context, id int64, snapshot string, pinned bool) (filesapi.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("pin %d %s %v", id, short(snapshot), pinned))
	if err := f.errFor("pin"); err != nil {
		return filesapi.Snapshot{}, err
	}
	if j := f.jobs[id]; j != nil && j.job.State == filesapi.JobRunning {
		return filesapi.Snapshot{}, filesapi.ErrBusy
	}
	i := slices.IndexFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return s.ID == snapshot })
	if i < 0 {
		return filesapi.Snapshot{}, filesapi.ErrNotFound
	}
	s := f.snaps[id][i]
	if s.Kind != filesapi.SnapshotScheduled {
		return filesapi.Snapshot{}, fmt.Errorf("%w: only scheduled snapshots can be pinned", filesapi.ErrInvalid)
	}
	f.seq++
	s.ID = fmt.Sprintf("%064x", f.seq) // restic rewrites the snapshot: a new id
	s.ShortID, s.Pinned = s.ID[:8], pinned
	f.snaps[id][i] = s
	return s, nil
}

func (f *fakeFiles) DownloadBackup(_ context.Context, id int64, snapshot string) (filesapi.Download, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("download %d %s", id, short(snapshot)))
	if err := f.errFor("download"); err != nil {
		return filesapi.Download{}, err
	}
	if !slices.ContainsFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return s.ID == snapshot }) {
		return filesapi.Download{}, filesapi.ErrNotFound
	}
	f.downloads++
	body := io.NopCloser(bytes.NewReader(f.zip))
	if f.dlBody != nil {
		body = f.dlBody()
	}
	return filesapi.Download{Body: body, Size: -1, IsDir: true}, nil
}

func (f *fakeFiles) PurgeBackups(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("purge %d", id))
	if err := f.errFor("purge"); err != nil {
		return err
	}
	if j := f.jobs[id]; j != nil && j.job.State == filesapi.JobRunning {
		return filesapi.ErrBusy
	}
	delete(f.snaps, id)
	return nil
}

func (f *fakeFiles) CancelJob(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("cancel %d", id))
	if err := f.errFor("cancel"); err != nil {
		return err
	}
	if j := f.jobs[id]; j != nil && j.job.State == filesapi.JobRunning {
		j.job.State, j.job.Error, j.job.FinishedAt = filesapi.JobFailed, "canceled", f.clk.Now().UnixMilli()
	}
	return nil
}

func (f *fakeFiles) Job(_ context.Context, id int64) (*filesapi.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errFor("job"); err != nil {
		return nil, err
	}
	j := f.jobs[id]
	if j == nil || f.loseJobs {
		return nil, nil
	}
	if j.job.State == filesapi.JobRunning {
		j.job.Bytes += 1024
		held := false
		if ch, ok := f.hold[j.plan]; ok {
			select {
			case <-ch:
			default:
				held = true
			}
		}
		if !held {
			f.complete(id, j)
		}
	}
	out := j.job
	return &out, nil
}

// complete ends a running job as planned; call with f.mu held.
func (f *fakeFiles) complete(id int64, j *fakeJob) {
	j.job.FinishedAt = f.clk.Now().UnixMilli()
	if msg := f.fail[j.plan]; msg != "" {
		j.job.State, j.job.Error, j.job.Code = filesapi.JobFailed, msg, f.failCode[j.plan]
		return
	}
	j.job.State = filesapi.JobDone
	switch j.job.Kind {
	case filesapi.JobBackup:
		j.job.Snapshot = f.addLocked(id, j.snapKind, false, j.note)
	case filesapi.JobForget:
		f.snaps[id] = slices.DeleteFunc(f.snaps[id], func(s filesapi.Snapshot) bool { return slices.Contains(j.forgetIDs, s.ID) })
	}
}

// runningJob seeds a running service job of kind (recovery tests); it stays running while
// hold[plan] is open.
func (f *fakeFiles) runningJob(id int64, kind filesapi.JobKind, plan string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.start(id, kind, plan); err != nil {
		panic(err)
	}
}

var _ filesapi.BackupClient = (*fakeFiles)(nil)

// fakeAgent is the agent's instance API: states per server, stop takes effect at once unless
// stuck.
type fakeAgent struct {
	mu     sync.Mutex
	state  map[int64]agentapi.ContainerState
	powers []string
	stuck  bool
	err    error
}

func newFakeAgent() *fakeAgent { return &fakeAgent{state: map[int64]agentapi.ContainerState{}} }

func (a *fakeAgent) set(id int64, s agentapi.ContainerState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state[id] = s
}

func (a *fakeAgent) powerLog() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.powers)
}

func (a *fakeAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return agentapi.InstancesResponse{}, a.err
	}
	out := agentapi.InstancesResponse{Docker: agentapi.DockerOK}
	for id, s := range a.state {
		out.Instances = append(out.Instances, agentapi.InstanceStatus{ID: id, State: s})
	}
	return out, nil
}

func (a *fakeAgent) Ensure(context.Context, agentapi.EnsureRequest) error { return nil }

func (a *fakeAgent) Power(_ context.Context, id int64, action agentapi.PowerAction) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.powers = append(a.powers, fmt.Sprintf("%s %d", action, id))
	if a.err != nil {
		return a.err
	}
	if action == agentapi.PowerStop && !a.stuck {
		a.state[id] = agentapi.StateExited
	}
	return nil
}

func (a *fakeAgent) Remove(context.Context, int64) error { return nil }

// fakeConsole records RCON commands as "<id> <command>"; errs[command] is used up one per call.
type fakeConsole struct {
	mu    sync.Mutex
	cmds  []string
	errs  map[string][]error
	onCmd func(cmd string) // called before the answer, without mu (set before the job starts)
}

func newFakeConsole() *fakeConsole { return &fakeConsole{errs: map[string][]error{}} }

func (c *fakeConsole) failNext(cmd string, errs ...error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errs[cmd] = append(c.errs[cmd], errs...)
}

func (c *fakeConsole) sent() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.cmds)
}

func (c *fakeConsole) Rcon(_ context.Context, id int64, cmd string) (string, error) {
	if c.onCmd != nil {
		c.onCmd(cmd)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cmds = append(c.cmds, fmt.Sprintf("%d %s", id, cmd))
	if q := c.errs[cmd]; len(q) > 0 {
		c.errs[cmd] = q[1:]
		return "", q[0]
	}
	return "ok", nil
}

func (c *fakeConsole) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, agentapi.ErrUnavailable
}

func (c *fakeConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, agentapi.ErrUnavailable
}
