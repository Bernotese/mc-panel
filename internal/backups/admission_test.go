package backups_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/backups"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/servers/admit"
	"github.com/Bernotese/mc-panel/internal/store"
)

// newAdmitEnv is newEnv with a fake admission (data safety spec §3).
func newAdmitEnv(t *testing.T) (*env, *fakeAdmit) {
	t.Helper()
	adm := newFakeAdmit()
	e := newEnvNoRun(t, func(d *backups.Deps) { d.Admit = adm.Admit })
	e.run()
	return e, adm
}

// Review Focus 1: a start the agent has queued before the restore was admitted runs before the
// restore's settled wait ends, never after it; the restore then stops the server again.
func TestRestoreWaitsForQueuedStart(t *testing.T) {
	cases := []struct {
		name  string
		state agentapi.ContainerState
		op    agentapi.Operation
		queue []agentapi.PowerAction
	}{
		{"start queued behind a stop", agentapi.StateRunning, agentapi.OpStopping, []agentapi.PowerAction{agentapi.PowerStart}},
		{"stopped with a start queued", agentapi.StateExited, agentapi.OpNone, []agentapi.PowerAction{agentapi.PowerStart}},
		{"starting", agentapi.StateExited, agentapi.OpStarting, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, adm := newAdmitEnv(t)
			backups.SetTimings(e.h, backups.Timings{
				Poll: 5 * time.Millisecond, RCON: 2 * time.Second, Stop: 5 * time.Second,
				Shutdown: 5 * time.Second, IO: 2 * time.Second,
			})
			id := e.instance("survival")
			e.agent.setStatus(id, c.state, c.op, c.queue...)
			snap := e.files.add(id, filesapi.SnapshotManual, false, "")
			e.restore(id, snap)
			e.waitStep(id, "stopping")
			time.Sleep(50 * time.Millisecond) // several polls while the agent is not settled
			if got := e.agent.powerLog(); len(got) != 0 {
				t.Fatalf("stop sent while the agent was not settled: %v", got)
			}
			if got := e.files.callLog(); len(got) != 0 {
				t.Fatalf("the service was called before the agent settled: %v", got)
			}
			// the agent drains its queue: the start runs
			e.agent.setStatus(id, agentapi.StateRunning, agentapi.OpNone)
			if done := e.waitJob(id); done.State != "done" {
				t.Fatalf("job = %+v", done)
			}
			if got := e.agent.powerLog(); !slices.Equal(got, []string{fmt.Sprintf("stop %d", id)}) {
				t.Fatalf("powers = %v", got)
			}
			want := []string{fmt.Sprintf("create %d pre-restore", id), fmt.Sprintf("restore %d %s", id, snap[:8])}
			if got := e.files.callLog(); !slices.Equal(got, want) {
				t.Fatalf("calls = %v, want %v", got, want)
			}
			if n := adm.calls(id); n != 3 { // the request's check, before the pre-restore snapshot, before the restore
				t.Fatalf("admissions = %d", n)
			}
		})
	}
}

// A restore runs only when its admissions succeed: a server that is not safe is refused at the
// request, and a refusal while the job runs fails it before the server is stopped or restored.
func TestRestoreAdmittedOnlyWhenSafe(t *testing.T) {
	e, adm := newAdmitEnv(t)
	id := e.instance("survival")
	e.agent.set(id, agentapi.StateRunning)
	snap := e.files.add(id, filesapi.SnapshotManual, false, "")
	admin := e.sudoAdmin()
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{admit.ErrRecoveryRequired, http.StatusConflict, "recovery_required"},
		{admit.ErrDataStateUnknown, http.StatusServiceUnavailable, "data_state_unknown"},
		{admit.ErrDataBusy, http.StatusConflict, "conflict"},
		{admit.ErrAdmissionBusy, http.StatusConflict, "conflict"},
	} {
		adm.setRefuse(func(int64, int) error { return c.err })
		rec := e.do(http.MethodPost, bPath(id, "/"+snap+"/restore"), nil, &admin)
		if rec.Code != c.status || errCode(t, rec) != c.code {
			t.Fatalf("%v: %d %s", c.err, rec.Code, rec.Body)
		}
		if e.job(id) != nil || holding(e.h, id) {
			t.Fatalf("%v: a refused restore was queued", c.err)
		}
	}
	for _, c := range []struct {
		err  error
		code string
	}{
		{admit.ErrRecoveryRequired, "files_busy"},
		{admit.ErrDataStateUnknown, "files_unavailable"},
	} {
		first := adm.calls(id) + 1
		adm.setRefuse(func(_ int64, n int) error {
			if n > first { // the request's check passes, the job's admission is refused
				return c.err
			}
			return nil
		})
		e.restore(id, snap)
		j := e.waitJob(id)
		if j.State != "failed" || j.ErrorCode == nil || *j.ErrorCode != c.code {
			t.Fatalf("%v: job = %+v", c.err, j)
		}
	}
	if got := e.agent.powerLog(); len(got) != 0 {
		t.Fatalf("a refused restore stopped the server: %v", got)
	}
	if got := e.files.callLog(); len(got) != 0 {
		t.Fatalf("a refused restore reached the service: %v", got)
	}
	// refused right before the restore (after the pre-restore snapshot): nothing is restored
	first := adm.calls(id) + 1
	adm.setRefuse(func(_ int64, n int) error {
		if n > first+1 {
			return admit.ErrDataBusy
		}
		return nil
	})
	e.restore(id, snap)
	if j := e.waitJob(id); j.State != "failed" || *j.ErrorCode != "files_busy" {
		t.Fatalf("job = %+v", j)
	}
	if n := e.files.count("restore"); n != 0 {
		t.Fatalf("restored although refused: %v", e.files.callLog())
	}
}

// A dev sync stops the dev and hands the restore to the service under the dev's admission; the
// prod is never admitted (it is only backed up).
func TestDevSyncUsesAdmission(t *testing.T) {
	log := &orderLog{}
	adm := newFakeAdmit()
	adm.log = log
	e := newEnvNoRun(t, func(d *backups.Deps) {
		d.Files = orderedFiles{d.Files.(*fakeFiles), log}
		d.Instances = orderedAgent{d.Instances.(*fakeAgent), log}
		d.Admit = adm.Admit
	})
	e.run()
	de := &devEnv{env: e, log: log, prod: e.instance("survival"), dev: e.instance("dev-survival")}
	if _, err := store.New(e.db).DevCreate(context.Background(), store.DevCreateParams{
		ServerID: de.dev, SourceID: de.prod, SyncTime: 300, IdleMinutes: 15, PropertyOverrides: `{}`,
		CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	e.agent.set(de.dev, agentapi.StateRunning)
	r := de.wait(de.queueAs("manual", nil))
	if r.err != nil {
		t.Fatal(r.err)
	}
	got := log.all()
	at := func(line string) int {
		t.Helper()
		i := slices.Index(got, line)
		if i < 0 {
			t.Fatalf("%q missing in %v", line, got)
		}
		return i
	}
	in, stop, restore, out := at(fmt.Sprintf("admit %d", de.dev)), at(fmt.Sprintf("stop %d", de.dev)),
		at(fmt.Sprintf("restore-from %d %d %s", de.dev, de.prod, short(r.snap))), at(fmt.Sprintf("admitted %d", de.dev))
	if !(in < stop && stop < restore && restore < out) {
		t.Fatalf("stop and restore must run inside the dev's admission: %v", got)
	}
	if adm.calls(de.prod) != 0 {
		t.Fatalf("the prod was admitted: %v", got)
	}

	// refused: the sync fails before the dev is stopped or restored
	adm.setRefuse(func(int64, int) error { return admit.ErrRecoveryRequired })
	e.agent.set(de.dev, agentapi.StateRunning)
	before := len(log.all())
	r = de.wait(de.queueAs("manual", nil))
	if r.err == nil {
		t.Fatal("a refused dev sync succeeded")
	}
	for _, l := range log.all()[before:] {
		if l == fmt.Sprintf("stop %d", de.dev) || len(l) > 12 && l[:12] == "restore-from" {
			t.Fatalf("a refused dev sync wrote the dev: %v", log.all()[before:])
		}
	}
}
