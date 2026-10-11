package serverfiles_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
)

func status(id int64, s agentapi.ContainerState, op agentapi.Operation) agentapi.InstanceStatus {
	return agentapi.InstanceStatus{ID: id, Name: "survival", Revision: 1, State: s, Operation: op}
}

// Review Focus #5: only a stopped server may be extracted into.
func TestExtractChecksState(t *testing.T) {
	cases := []struct {
		name     string
		state    agentapi.ContainerState // "" = not listed by the agent
		op       agentapi.Operation
		docker   string
		agentErr error
		code     int
		ec       string
	}{
		{"not listed", "", "", agentapi.DockerOK, nil, http.StatusAccepted, ""},
		{"exited", agentapi.StateExited, "", agentapi.DockerOK, nil, http.StatusAccepted, ""},
		{"created", agentapi.StateCreated, "", agentapi.DockerOK, nil, http.StatusAccepted, ""},
		{"missing", agentapi.StateMissing, "", agentapi.DockerOK, nil, http.StatusAccepted, ""},
		{"running", agentapi.StateRunning, "", agentapi.DockerOK, nil, http.StatusConflict, httpx.CodeInstanceRunning},
		{"restarting", agentapi.StateRestarting, "", agentapi.DockerOK, nil, http.StatusConflict, httpx.CodeInstanceRunning},
		{"exited but starting", agentapi.StateExited, agentapi.OpStarting, agentapi.DockerOK, nil, http.StatusConflict, httpx.CodeInstanceRunning},
		{"agent down", "", "", agentapi.DockerOK, agentapi.ErrUnavailable, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable},
		{"docker down", "", "", agentapi.DockerUnavailable, nil, http.StatusServiceUnavailable, httpx.CodeDockerUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			id := createInstance(t, e.db, "survival")
			admin := e.admin()
			if tc.state != "" {
				e.agent.set(status(id, tc.state, tc.op))
			}
			e.agent.docker = tc.docker
			e.agent.err = tc.agentErr
			rec := e.do(http.MethodPost, srvPath(id, "/files/extract"), map[string]string{"path": "world.zip", "dest": "world-new"}, &admin)
			if rec.Code != tc.code {
				t.Fatalf("extract: %d %s, want %d", rec.Code, rec.Body, tc.code)
			}
			calls := e.files.takeCalls()
			if tc.code != http.StatusAccepted {
				if errCode(t, rec) != tc.ec {
					t.Fatalf("code = %s, want %s", errCode(t, rec), tc.ec)
				}
				if len(calls) != 0 || len(e.rec.Entries) != 0 {
					t.Fatalf("refused extract reached the service or the audit: %v %v", calls, e.rec.Actions())
				}
				return
			}
			job := decode[filesapi.Job](t, rec)
			if job.State != "running" || job.Path != "world.zip" || job.Dest != "world-new" {
				t.Fatalf("job = %+v", job)
			}
			if !slices.Equal(calls, []string{fmt.Sprintf("extract %d world.zip world-new", id)}) {
				t.Fatalf("calls = %v", calls)
			}
			last := e.lastEntry()
			if last.Action != audit.ActionFileExtract || last.Result != audit.ResultSuccess ||
				last.Details["path"] != "world.zip" || last.Details["dest"] != "world-new" {
				t.Fatalf("audit = %+v", last)
			}
		})
	}
}

func TestExtractValidation(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	for _, tc := range []struct {
		body any
		code int
		ec   string
	}{
		{map[string]string{"path": "", "dest": "w"}, http.StatusBadRequest, httpx.CodeValidation},
		{map[string]string{"path": "w.zip", "dest": ""}, http.StatusBadRequest, httpx.CodeValidation},
		{map[string]string{"path": "w.zip", "dest": "../w"}, http.StatusBadRequest, httpx.CodeValidation},
		{map[string]string{"path": "w.zip", "dest": ".mcpanel-tmp-w"}, http.StatusNotFound, httpx.CodeNotFound},
		{map[string]any{"path": "w.zip", "dest": "w", "x": 1}, http.StatusBadRequest, httpx.CodeBadRequest},
	} {
		rec := e.do(http.MethodPost, srvPath(id, "/files/extract"), tc.body, &admin)
		if rec.Code != tc.code || errCode(t, rec) != tc.ec {
			t.Errorf("%v: %d %s", tc.body, rec.Code, rec.Body)
		}
	}
	if calls := e.files.takeCalls(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
}

// The service checks the archive synchronously (spec §4.2); its refusals come back as errors
// and are audited as failed attempts, like every other file action that reached the service
// (decision 7).
func TestExtractServiceErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		ec   string
	}{
		{filesapi.ErrInvalid, http.StatusBadRequest, httpx.CodeValidation},    // not a valid zip, zip slip …
		{filesapi.ErrNotRegular, http.StatusBadRequest, httpx.CodeNotRegular}, // the archive is a symlink
		{filesapi.ErrExists, http.StatusConflict, httpx.CodeFileExists},       // destination not empty
		{filesapi.ErrTooLarge, http.StatusRequestEntityTooLarge, httpx.CodePayloadTooLarge},
		{filesapi.ErrInsufficientStorage, http.StatusInsufficientStorage, httpx.CodeInsufficientStorage},
		{filesapi.ErrBusy, http.StatusTooManyRequests, httpx.CodeRateLimited}, // job limit
		{filesapi.ErrNotFound, http.StatusNotFound, httpx.CodeNotFound},
	} {
		e := newEnv(t)
		id := createInstance(t, e.db, "survival")
		admin := e.admin()
		e.files.setErr(fmt.Errorf("service: %w", tc.err))
		rec := e.do(http.MethodPost, srvPath(id, "/files/extract"), map[string]string{"path": "w.zip", "dest": "w"}, &admin)
		if rec.Code != tc.code || errCode(t, rec) != tc.ec {
			t.Errorf("%v: %d %s, want %d %s", tc.err, rec.Code, rec.Body, tc.code, tc.ec)
			continue
		}
		if errors.Is(tc.err, filesapi.ErrBusy) { // a rejection, not audited (review B4)
			if len(e.rec.Entries) != 0 {
				t.Errorf("busy: audit = %v", e.rec.Actions())
			}
			continue
		}
		last := e.lastEntry()
		if last.Action != audit.ActionFileExtract || last.Result != audit.ResultFailure || last.Details["dest"] != "w" {
			t.Errorf("%v: audit = %+v", tc.err, last)
		}
	}
}

func TestFileJob(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	rec := e.do(http.MethodGet, srvPath(id, "/files/jobs"), nil, &admin)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"job":null}` {
		t.Fatalf("no job: %d %s", rec.Code, rec.Body)
	}
	e.files.setJob(&filesapi.Job{ID: "j1", State: "done", Path: "w.zip", Dest: "w", Bytes: 10, Entries: 2, TotalBytes: 10, TotalEntries: 2})
	got := decode[struct {
		Job *filesapi.Job `json:"job"`
	}](t, e.do(http.MethodGet, srvPath(id, "/files/jobs"), nil, &admin))
	if got.Job == nil || got.Job.State != "done" || got.Job.Entries != 2 {
		t.Fatalf("job = %+v", got.Job)
	}
	if len(e.rec.Entries) != 0 {
		t.Fatalf("job queries must not be audited: %v", e.rec.Actions())
	}
}

func TestExtractRunning(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	ctx := context.Background()
	if e.h.ExtractRunning(ctx, id) {
		t.Fatal("no job: running")
	}
	e.files.setJob(&filesapi.Job{ID: "j1", State: "running"})
	if !e.h.ExtractRunning(ctx, id) {
		t.Fatal("running job: not running")
	}
	e.files.setJob(&filesapi.Job{ID: "j1", State: "failed"})
	if e.h.ExtractRunning(ctx, id) {
		t.Fatal("failed job: running")
	}
	e.files.setJob(&filesapi.Job{ID: "j1", State: "running"})
	e.files.setErr(filesapi.ErrUnavailable)
	if e.h.ExtractRunning(ctx, id) {
		t.Fatal("service down must count as no job")
	}
}

// Review Focus #5: from the state check until the service answers, the gate already holds
// starts (the service knows no job yet); afterwards only the service's job counts.
func TestExtractPendingHoldsStarts(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	other := createInstance(t, e.db, "creative")
	admin := e.admin()
	var during, otherDuring bool
	e.agent.onCall = func() {
		ctx := context.Background()
		during, otherDuring = e.h.ExtractRunning(ctx, id), e.h.ExtractRunning(ctx, other)
	}
	e.agent.set(status(id, agentapi.StateRunning, ""))
	rec := e.do(http.MethodPost, srvPath(id, "/files/extract"), map[string]string{"path": "w.zip", "dest": "w"}, &admin)
	if rec.Code != http.StatusConflict {
		t.Fatalf("extract: %d %s", rec.Code, rec.Body)
	}
	if !during || otherDuring {
		t.Fatalf("during the state check: running=%v, other server=%v", during, otherDuring)
	}
	if e.h.ExtractRunning(context.Background(), id) {
		t.Fatal("a refused extract keeps holding starts")
	}

	// during the service call the service knows no job yet, the mark still holds; a refusal
	// of the service (job limit) clears it
	e.agent.set(status(id, agentapi.StateExited, ""))
	e.agent.onCall = nil
	var inCall bool
	e.files.onExtract = func() { inCall = e.h.ExtractRunning(context.Background(), id) }
	e.files.setErr(fmt.Errorf("service: %w", filesapi.ErrBusy))
	rec = e.do(http.MethodPost, srvPath(id, "/files/extract"), map[string]string{"path": "w.zip", "dest": "w"}, &admin)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("extract with busy service: %d %s", rec.Code, rec.Body)
	}
	if !inCall {
		t.Fatal("the gate was open during the service call")
	}
	e.files.setErr(nil) // the service answers again and knows no job
	if e.h.ExtractRunning(context.Background(), id) {
		t.Fatal("a failed extract keeps holding starts")
	}
}

// An open gate because the service is unreachable is logged.
func TestExtractRunningLogsUnreachableService(t *testing.T) {
	var logs bytes.Buffer
	e := newEnvWith(t, func(d *serverfiles.Deps) { d.Log = slog.New(slog.NewTextHandler(&logs, nil)) })
	id := createInstance(t, e.db, "survival")
	e.files.setErr(filesapi.ErrUnavailable)
	if e.h.ExtractRunning(context.Background(), id) {
		t.Fatal("service down must count as no job")
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), fmt.Sprintf("server_id=%d", id)) {
		t.Fatalf("log = %q", logs.String())
	}
}
