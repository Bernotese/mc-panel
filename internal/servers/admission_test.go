package servers_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestAdmissionLock(t *testing.T) {
	ctx := context.Background()
	a := servers.NewAdmissionWait(50 * time.Millisecond)
	unlock, err := a.Lock(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.TryLock(1); ok {
		t.Fatal("TryLock got a held lock")
	}
	if _, err := a.Lock(ctx, 1); !errors.Is(err, servers.ErrAdmissionBusy) {
		t.Fatalf("second Lock = %v, want ErrAdmissionBusy after the wait", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.Lock(cctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Lock with a cancelled context = %v", err)
	}
	other, ok := a.TryLock(2)
	if !ok {
		t.Fatal("servers must not share a lock")
	}
	other()
	got := make(chan error, 1)
	go func() {
		u, err := servers.NewAdmission().Lock(ctx, 1) // another Admission: independent
		if err == nil {
			u()
		}
		got <- err
	}()
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() {
		u, err := a.Lock(ctx, 1)
		if err == nil {
			u()
		}
		waited <- err
	}()
	time.Sleep(10 * time.Millisecond)
	unlock()
	unlock() // idempotent
	if err := <-waited; err != nil {
		t.Fatalf("a waiter must get the lock once it is released: %v", err)
	}
	if n := servers.AdmissionEntries(a); n != 0 {
		t.Fatalf("%d lock entries left after every holder released", n)
	}
}

// fakeStateClient is a filesapi.StateClient with one fixed answer; block makes State wait for
// the context. ResolveRecovery answers recovery or resolveErr and counts its calls.
type fakeStateClient struct {
	mu         sync.Mutex
	resp       filesapi.StateResponse
	err        error
	block      bool
	calls      int
	recovery   filesapi.Recovery
	resolveErr error
	resolved   []int64
}

func (f *fakeStateClient) State(ctx context.Context, _ int64) (filesapi.StateResponse, error) {
	f.mu.Lock()
	f.calls++
	resp, err, block := f.resp, f.err, f.block
	f.mu.Unlock()
	if block {
		<-ctx.Done()
		return filesapi.StateResponse{}, ctx.Err()
	}
	return resp, err
}

func (f *fakeStateClient) ResolveRecovery(_ context.Context, id int64) (filesapi.Recovery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, id)
	if f.resolveErr != nil {
		return filesapi.Recovery{}, f.resolveErr
	}
	return f.recovery, nil
}

type fakeHold struct{ kinds map[int64]string }

func (h *fakeHold) Holding(id int64) (string, bool) { k, ok := h.kinds[id]; return k, ok }

// Review Focus 3: only a valid answer of the files service is a state; everything else is
// unknown, never safe.
func TestDataStatesFailClosed(t *testing.T) {
	ctx := context.Background()
	fc := &fakeStateClient{}
	d := servers.NewDataStates(fc)
	servers.SetStateTimeout(d, 20*time.Millisecond)
	check := func(name string, want servers.State, wantJob string, wantErr bool) {
		t.Helper()
		info, err := d.Lookup(ctx, 7, nil)
		if info.State != want || info.Job != wantJob || (err != nil) != wantErr {
			t.Fatalf("%s: %+v, %v; want %s %q (error %t)", name, info, err, want, wantJob, wantErr)
		}
		if st, err2 := d.State(ctx, 7); st != want || (err2 != nil) != wantErr {
			t.Fatalf("%s: State = %s, %v", name, st, err2)
		}
	}
	fc.resp = filesapi.StateResponse{State: filesapi.StateSafe}
	check("safe", servers.StateSafe, "", false)
	fc.resp = filesapi.StateResponse{State: filesapi.StateBusy, Job: &filesapi.Job{Kind: filesapi.JobRestore}}
	check("busy", servers.StateBusy, "restore", false)
	fc.resp = filesapi.StateResponse{State: filesapi.StateBusy, Job: &filesapi.Job{}}
	check("busy, old extract job", servers.StateBusy, "extract", false)
	fc.resp = filesapi.StateResponse{State: filesapi.StateRecoveryRequired, Recovery: &filesapi.Recovery{Phase: "stuck"}}
	check("recovery", servers.StateRecoveryRequired, "", false)
	fc.resp = filesapi.StateResponse{State: "weird"}
	check("invalid answer", servers.StateUnknown, "", true)
	fc.resp, fc.err = filesapi.StateResponse{}, fmt.Errorf("%w: dial unix: connection refused", filesapi.ErrUnavailable)
	check("transport error", servers.StateUnknown, "", true)
	fc.err = &filesapi.StatusError{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "journal unreadable"}
	check("5xx", servers.StateUnknown, "", true)
	fc.err, fc.block = nil, true
	start := time.Now()
	check("timeout", servers.StateUnknown, "", true)
	if time.Since(start) > time.Second {
		t.Fatal("the files call was not bounded")
	}

	// Holds come first (no files call) and the caller's own hold can be left out.
	hold := &fakeHold{kinds: map[int64]string{7: "dev_sync"}}
	fc.block, fc.resp = false, filesapi.StateResponse{State: filesapi.StateSafe}
	d = servers.NewDataStates(fc, nil, hold)
	calls := fc.calls
	if info, err := d.Lookup(ctx, 7, nil); info.State != servers.StateBusy || info.Job != "dev_sync" || err != nil || fc.calls != calls {
		t.Fatalf("held: %+v, %v (files asked %d times)", info, err, fc.calls-calls)
	}
	if info, err := d.Lookup(ctx, 7, hold); info.State != servers.StateSafe || err != nil {
		t.Fatalf("own hold left out: %+v, %v", info, err)
	}
	// Without a files service no data job can exist.
	if st, err := servers.NewDataStates(nil).State(ctx, 7); st != servers.StateSafe || err != nil {
		t.Fatalf("no files service: %s, %v", st, err)
	}
}

// A start or restart is admitted only when the data is safe; a refusal is not audited and costs
// no limiter token. Stop never asks.
func TestStartRefusedUnlessSafe(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "survival")
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, id)
	e.agent.set(container("survival", id, 1, agentapi.StateRunning, ""))
	cases := []struct {
		name   string
		set    func()
		status int
		code   string
	}{
		{"busy", func() { e.data.setBusy(id, "extract") }, http.StatusConflict, "conflict"},
		{"recovery", func() { e.data.set(id, servers.StateRecoveryRequired) }, http.StatusConflict, "recovery_required"},
		{"unknown", func() { e.data.set(id, servers.StateSafe); e.data.setErr(errors.New("files down")) }, http.StatusServiceUnavailable, "data_state_unknown"},
	}
	for _, c := range cases {
		c.set()
		for range 6 { // more than the limiter's burst of 10 over both actions
			for _, action := range []string{"start", "restart"} {
				if code, ec := e.power(id, action, &admin); code != c.status || ec != c.code {
					t.Fatalf("%s: %s = %d %s, want %d %s", c.name, action, code, ec, c.status, c.code)
				}
			}
		}
		e.data.setErr(nil)
	}
	if got := instanceRow(t, e.db, id).DesiredState; got != "stopped" {
		t.Fatalf("desired = %s", got)
	}
	if calls := e.agent.takeCalls(); len(calls) != 0 {
		t.Fatalf("refused actions reached the agent: %v", calls)
	}
	if len(e.rec.Entries) != 0 {
		t.Fatalf("refused actions were audited: %v", e.rec.Actions())
	}
	asked := e.data.askedCount()
	if code, _ := e.power(id, "stop", &admin); code != http.StatusAccepted {
		t.Fatalf("stop while recovery is required: %d", code)
	}
	if e.data.askedCount() != asked {
		t.Fatal("a stop must not ask for the data state")
	}
	e.data.set(id, servers.StateSafe)
	if code, ec := e.power(id, "restart", &admin); code != http.StatusAccepted {
		t.Fatalf("restart when safe: %d %s (the refusals must not have used up the limiter)", code, ec)
	}
	if code, _ := e.power(id, "start", &admin); code != http.StatusAccepted {
		t.Fatalf("start when safe: %d", code)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionServerPower, audit.ActionServerPower, audit.ActionServerPower}) {
		t.Fatalf("audit = %v", got)
	}
}

// Review Focus 3: without an answer of the files service the reconcile loop starts nothing,
// counts no backoff and logs it once; stopping and creating containers go on.
func TestReconcileUnknownDoesNotStart(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	stopped := createInstance(t, e.db, "survival") // desired running
	running := createInstance(t, e.db, "lobby")
	missing := createInstance(t, e.db, "creative")
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, running)
	e.agent.set(container("survival", stopped, 1, agentapi.StateExited, ""))
	e.agent.set(container("lobby", running, 1, agentapi.StateRunning, ""))
	e.data.setErr(errors.New("files down"))
	// ServersListInstances sorts by name: creative, lobby, survival.
	want := []string{fmt.Sprintf("ensure %d r1 recreate=false", missing), fmt.Sprintf("power %d stop", running)}
	if got := e.pass(); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	e.agent.set(container("lobby", running, 1, agentapi.StateExited, ""))
	e.agent.set(container("creative", missing, 1, agentapi.StateCreated, ""))
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, missing)
	if got := e.pass(); len(got) != 0 {
		t.Fatalf("calls while the data state is unknown = %v", got)
	}
	if n := servers.Attempts(e.h); n != 0 {
		t.Fatalf("a held start must not count for the backoff: %d attempts", n)
	}
	if n := strings.Count(e.logs.String(), "start held, the files service does not report"); n != 1 {
		t.Fatalf("warning logged %d times, want once:\n%s", n, e.logs)
	}
	e.data.setErr(nil)
	if got := e.pass(); !slices.Equal(got, []string{fmt.Sprintf("power %d start", stopped)}) {
		t.Fatalf("calls once the state is known = %v", got)
	}
	if !strings.Contains(e.logs.String(), "data state known again") {
		t.Fatalf("recovery not logged:\n%s", e.logs)
	}
	for _, st := range []servers.State{servers.StateBusy, servers.StateRecoveryRequired} {
		e.agent.set(container("survival", stopped, 1, agentapi.StateExited, ""))
		e.data.set(stopped, st)
		if got := e.pass(); len(got) != 0 {
			t.Fatalf("calls while %s = %v", st, got)
		}
	}
}

// admitBlocked holds the admission of server id until the returned release is called; fn runs
// under the lock right before it is released.
func admitBlocked(t *testing.T, h *servers.Handler, id int64, fn func()) (release func(), done <-chan error) {
	t.Helper()
	held, rel, out := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		out <- h.Admit(context.Background(), id, nil, func(context.Context) error {
			close(held)
			<-rel
			if fn != nil {
				fn()
			}
			return nil
		})
	}()
	<-held
	return func() { close(rel) }, out
}

// The reconcile pass skips a server whose admission another operation holds (TryLock), without
// a backoff; the other servers are started.
func TestReconcileSkipsLockedServer(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	a := createInstance(t, e.db, "alpha")
	b := createInstance(t, e.db, "beta")
	e.agent.set(container("alpha", a, 1, agentapi.StateExited, ""))
	e.agent.set(container("beta", b, 1, agentapi.StateExited, ""))
	release, done := admitBlocked(t, e.h, a, nil)
	passed := make(chan []string)
	go func() { passed <- e.pass() }()
	select {
	case got := <-passed:
		if !slices.Equal(got, []string{fmt.Sprintf("power %d start", b)}) {
			t.Fatalf("calls with alpha's admission held = %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the reconcile pass waited for a held admission")
	}
	if n := servers.Attempts(e.h); n != 1 {
		t.Fatalf("attempts = %d, want only beta's", n)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := e.pass(); !slices.Equal(got, []string{fmt.Sprintf("power %d start", a)}) {
		t.Fatalf("calls after the admission = %v", got)
	}
}

// Barrier: a restart that arrives while a restore is being admitted waits for the lock and then
// sees the restore's hold; it never reaches the agent in between.
func TestRestartAdmittedUnderLock(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "survival")
	e.agent.set(container("survival", id, 1, agentapi.StateRunning, ""))
	release, done := admitBlocked(t, e.h, id, func() { e.data.setBusy(id, "restore") })
	res := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		res <- e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "restart"}, &admin)
	}()
	select {
	case rec := <-res:
		t.Fatalf("restart answered while the admission was held: %d %s", rec.Code, rec.Body)
	case <-time.After(100 * time.Millisecond):
	}
	if calls := e.agent.takeCalls(); len(calls) != 0 {
		t.Fatalf("restart reached the agent during the admission: %v", calls)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	rec := <-res
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("restart after the restore was admitted: %d %s", rec.Code, rec.Body)
	}
	if calls := e.agent.takeCalls(); len(calls) != 0 {
		t.Fatalf("restart reached the agent: %v", calls)
	}

	// A handler waits at most the admission wait, then answers 409 conflict.
	servers.SetAdmissionWait(e.h, 20*time.Millisecond)
	e.data.set(id, servers.StateSafe)
	release, done = admitBlocked(t, e.h, id, nil)
	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": "start"}, &admin)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "being admitted") {
		t.Fatalf("start while the admission stays held: %d %s", rec.Code, rec.Body)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if code, ec := e.power(id, "restart", &admin); code != http.StatusAccepted {
		t.Fatalf("restart after the admission: %d %s", code, ec)
	}
	if calls := e.agent.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("power %d restart", id)}) {
		t.Fatalf("calls = %v", calls)
	}
}

// Review Focus 5: a server whose restore could not be completed may be deleted: its
// half-swapped folders are moved to backups/recovery/ first and the audit entry names the
// folder; busy and unknown refuse the delete before anything changes.
func TestDeleteDataStates(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, paper("survival"))
	e.agent.set(container("survival", s.ID, 1, agentapi.StateExited, ""))
	refuse := func(name string, status int, code string) {
		t.Helper()
		rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=1", s.ID), nil, &admin)
		if rec.Code != status || errCode(t, rec) != code {
			t.Fatalf("delete while %s: %d %s", name, rec.Code, rec.Body)
		}
		if n := e.count(t, "servers"); n != 1 {
			t.Fatalf("refused delete removed rows: %d", n)
		}
		if calls := e.purger.takeCalls(); len(calls) != 0 {
			t.Fatalf("refused delete purged: %v", calls)
		}
		if calls := e.agent.takeCalls(); len(calls) != 0 {
			t.Fatalf("refused delete reached the agent: %v", calls)
		}
	}
	audits := len(e.rec.Entries)
	e.data.setBusy(s.ID, "restore")
	refuse("busy", http.StatusConflict, "conflict")
	e.data.set(s.ID, servers.StateSafe)
	e.data.setErr(errors.New("files down"))
	refuse("unknown", http.StatusServiceUnavailable, "data_state_unknown")
	if len(e.rec.Entries) != audits {
		t.Fatalf("refused deletes were audited: %v", e.rec.Actions())
	}
	e.data.setErr(nil)
	e.data.set(s.ID, servers.StateRecoveryRequired)
	// the half-swapped folders are moved out before the data directory goes: a failing move
	// refuses the delete
	e.files.resolveErr = fmt.Errorf("%w: dial unix", filesapi.ErrUnavailable)
	refuse("recovery, files failing", http.StatusServiceUnavailable, "files_unavailable")
	e.files.resolveErr = &filesapi.StatusError{Status: http.StatusTooManyRequests, Code: filesapi.CodeBusy, Message: "busy"}
	refuse("recovery, files busy", http.StatusConflict, "conflict")
	e.files.resolveErr = nil
	e.files.recovery = filesapi.Recovery{Phase: "stuck", Since: 1, Folder: fmt.Sprintf("%d-1793000000", s.ID)}
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", s.ID), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete in recovery_required: %d %s", rec.Code, rec.Body)
	}
	if !slices.Equal(e.files.resolved, []int64{s.ID, s.ID, s.ID}) {
		t.Fatalf("resolved = %v", e.files.resolved)
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if rr, _ := en.Details["recovery_required"].(bool); en.Action != audit.ActionServerDelete || !rr ||
		en.Details["recovery_folder"] != e.files.recovery.Folder {
		t.Fatalf("audit = %+v", en)
	}
	other := e.create(t, paper("creative"))
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", other.ID), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete when safe: %d %s", rec.Code, rec.Body)
	}
	if _, ok := e.rec.Entries[len(e.rec.Entries)-1].Details["recovery_required"]; ok || len(e.files.resolved) != 3 {
		t.Fatal("a safe delete must neither resolve nor report a recovery")
	}
}

type dataStateResp struct {
	ID        int64   `json:"id"`
	DataState string  `json:"data_state"`
	DataJob   *string `json:"data_job"`
}

// Everyone who can view a server sees its data state and, while busy, the job kind.
func TestServerViewDataState(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	safe := e.create(t, paper("alpha")).ID
	busy := e.create(t, paper("beta")).ID
	rec := e.create(t, paper("gamma")).ID
	e.data.setBusy(busy, "restore")
	e.data.set(rec, servers.StateRecoveryRequired)
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	grant(t, e.db, alice, busy, "server.view")
	p := userP(alice, "alice")

	list := decode[[]dataStateResp](t, e.do(http.MethodGet, "/servers", nil, &admin))
	got := map[int64]string{}
	for _, s := range list {
		job := "null"
		if s.DataJob != nil {
			job = *s.DataJob
		}
		got[s.ID] = s.DataState + "/" + job
	}
	want := map[int64]string{safe: "safe/null", busy: "busy/restore", rec: "recovery_required/null"}
	for id, w := range want {
		if got[id] != w {
			t.Fatalf("list: %d = %s, want %s (all %v)", id, got[id], w, got)
		}
	}
	one := decode[dataStateResp](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d", busy), nil, &p))
	if one.DataState != "busy" || one.DataJob == nil || *one.DataJob != "restore" {
		t.Fatalf("viewer sees %+v", one)
	}
	ulist := decode[[]dataStateResp](t, e.do(http.MethodGet, "/servers", nil, &p))
	if len(ulist) != 1 || ulist[0].DataState != "busy" {
		t.Fatalf("viewer list = %+v", ulist)
	}
	e.data.setErr(errors.New("files down"))
	one = decode[dataStateResp](t, e.do(http.MethodGet, fmt.Sprintf("/servers/%d", safe), nil, &admin))
	if one.DataState != "unknown" || one.DataJob != nil {
		t.Fatalf("files down: %+v", one)
	}
	// The raw JSON always carries both fields.
	body := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", safe), nil, &admin).Body.String()
	if !strings.Contains(body, `"data_job":null`) {
		t.Fatalf("data_job missing: %s", body)
	}
}

// POST /servers/{id}/recovery/resolve: admin with sudo only; only a server in
// recovery_required is resolved through the files service (audited), every other state is
// refused without asking it.
func TestResolveEndpoint(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := e.create(t, paper("survival")).ID
	path := fmt.Sprintf("/servers/%d/recovery/resolve", id)
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	grant(t, e.db, alice, id, allPermStrings...)
	p := userP(alice, "alice")
	if rec := e.do(http.MethodPost, path, nil, &p); rec.Code != http.StatusForbidden {
		t.Fatalf("user: %d %s", rec.Code, rec.Body)
	}
	noSudo := admin
	noSudo.SudoUntil = time.Time{}
	if rec := e.do(http.MethodPost, path, nil, &noSudo); rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("without sudo: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodPost, "/servers/999/recovery/resolve", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown server: %d", rec.Code)
	}
	for _, c := range []struct {
		name   string
		set    func()
		status int
		code   string
	}{
		{"safe", func() { e.data.set(id, servers.StateSafe) }, http.StatusConflict, "recovery_required"},
		{"busy", func() { e.data.setBusy(id, "restore") }, http.StatusConflict, "recovery_required"},
		{"unknown", func() { e.data.setErr(errors.New("files down")) }, http.StatusServiceUnavailable, "data_state_unknown"},
	} {
		c.set()
		rec := e.do(http.MethodPost, path, nil, &admin)
		if rec.Code != c.status || errCode(t, rec) != c.code {
			t.Fatalf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
		e.data.setErr(nil)
	}
	if len(e.files.resolved) != 0 || len(e.rec.Entries) != 1 { // server.create
		t.Fatalf("refused resolves: service %v, audit %v", e.files.resolved, e.rec.Actions())
	}

	e.data.set(id, servers.StateRecoveryRequired)
	for _, se := range []*filesapi.StatusError{
		{Status: http.StatusNotFound, Code: filesapi.CodeNotFound, Message: "no unfinished restore"},
		{Status: http.StatusTooManyRequests, Code: filesapi.CodeBusy, Message: "a job holds the server"},
	} {
		e.files.resolveErr = se
		if rec := e.do(http.MethodPost, path, nil, &admin); rec.Code != http.StatusConflict || errCode(t, rec) != "recovery_required" {
			t.Fatalf("refused by the service (%s): %d %s", se.Code, rec.Code, rec.Body)
		}
	}
	e.files.resolveErr = fmt.Errorf("%w: dial unix /run/mcpanel/files.sock", filesapi.ErrUnavailable)
	rec := e.do(http.MethodPost, path, nil, &admin)
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "files_unavailable" || strings.Contains(rec.Body.String(), "/run/") {
		t.Fatalf("service down: %d %s", rec.Code, rec.Body)
	}
	if last := e.rec.Entries[len(e.rec.Entries)-1]; last.Action != audit.ActionServerRecoveryResolve || last.Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", last)
	}
	e.files.resolveErr = nil
	e.files.recovery = filesapi.Recovery{Phase: "stuck", Since: 1_700_000_000_000, Folder: fmt.Sprintf("backups/recovery/%d-1793000000", id)}
	rec = e.do(http.MethodPost, path, nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body)
	}
	got := decode[filesapi.Recovery](t, rec)
	if got != e.files.recovery {
		t.Fatalf("answer = %+v", got)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if last.Action != audit.ActionServerRecoveryResolve || last.Result != audit.ResultSuccess ||
		last.Details["folder"] != e.files.recovery.Folder || last.Details["name"] != "survival" {
		t.Fatalf("audit = %+v", last)
	}
	if !slices.Equal(e.files.resolved, []int64{id, id, id, id}) {
		t.Fatalf("resolved = %v", e.files.resolved)
	}
}
