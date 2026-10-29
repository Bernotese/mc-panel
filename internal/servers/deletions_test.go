package servers_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

type deletionResp struct {
	ServerID       int64   `json:"server_id"`
	Name           string  `json:"name"`
	State          string  `json:"state"`
	Container      string  `json:"container"`
	ContainerError *string `json:"container_error"`
	Backups        string  `json:"backups"`
	BackupsError   *string `json:"backups_error"`
	CompletedAt    *int64  `json:"completed_at"`
}

// deleteServer deletes id (202) and returns the deletion.
func (e *env) deleteServer(t *testing.T, id int64, query string) deletionResp {
	t.Helper()
	admin := e.admin()
	rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d%s", id, query), nil, &admin)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete %d: %d %s", id, rec.Code, rec.Body)
	}
	return decode[deletionResp](t, rec)
}

// deletion returns the listed deletion of id (fails if it is not listed).
func (e *env) deletion(t *testing.T, id int64) deletionResp {
	t.Helper()
	admin := e.admin()
	for _, d := range decode[[]deletionResp](t, e.do(http.MethodGet, "/servers/deletions", nil, &admin)) {
		if d.ServerID == id {
			return d
		}
	}
	t.Fatalf("deletion %d not listed", id)
	return deletionResp{}
}

func (e *env) deletionPass() []string {
	e.h.DeletionPass(context.Background())
	return e.agent.takeCalls()
}

// Issue #148: the server is gone at once, but the deletion stays open until the agent
// confirms the removal. A remove the agent lost (restart) is asked for again; only the
// confirmation completes the deletion, and the name stays taken until then.
func TestDeletionCompletesOnlyOnConfirmation(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := e.create(t, paper("survival")).ID
	e.agent.set(container("survival", id, 1, agentapi.StateRunning, ""))
	d := e.deleteServer(t, id, "")
	if d.State != "pending" || d.Container != "pending" || d.Backups != "none" || d.CompletedAt != nil || d.Name != "survival" {
		t.Fatalf("deletion = %+v", d)
	}
	if calls := e.agent.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("remove %d", id)}) {
		t.Fatalf("calls = %v", calls)
	}
	if rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("the deleted server is still served: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, "/servers", paper("survival"), &admin); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "still being deleted") {
		t.Fatalf("name reused during the deletion: %d %s", rec.Code, rec.Body)
	}

	// the agent works on it: nothing is asked again
	e.agent.set(container("survival", id, 1, agentapi.StateRunning, agentapi.OpStopping))
	e.clk.Advance(time.Minute)
	if calls := e.deletionPass(); len(calls) != 0 {
		t.Fatalf("calls while the agent removes = %v", calls)
	}
	// the agent restarted and lost the job: the container still runs, no confirmation
	e.agent.set(container("survival", id, 1, agentapi.StateRunning, ""))
	if calls := e.deletionPass(); !slices.Equal(calls, []string{fmt.Sprintf("remove %d", id)}) {
		t.Fatalf("lost remove not asked again: %v", calls)
	}
	if calls := e.deletionPass(); len(calls) != 0 {
		t.Fatalf("asked again before the retry delay: %v", calls)
	}
	if d := e.deletion(t, id); d.State != "pending" {
		t.Fatalf("deletion = %+v", d)
	}
	// the reconcile loop leaves the container of a deleted server alone and does not warn
	if calls := e.pass(); len(calls) != 0 || strings.Contains(e.logs.String(), "container without database entry") {
		t.Fatalf("reconcile touched a deleting server: %v\n%s", calls, e.logs)
	}

	e.agent.confirmRemoved(id)
	e.deletionPass()
	d = e.deletion(t, id)
	if d.State != "done" || d.Container != "removed" || d.CompletedAt == nil {
		t.Fatalf("after the confirmation = %+v", d)
	}
	if rec := e.do(http.MethodPost, "/servers", paper("survival"), &admin); rec.Code != http.StatusCreated {
		t.Fatalf("name after the deletion: %d %s", rec.Code, rec.Body)
	}
	// completed deletions are listed for a week
	e.clk.Advance(8 * 24 * time.Hour)
	e.deletionPass()
	for _, l := range decode[[]deletionResp](t, e.do(http.MethodGet, "/servers/deletions", nil, &admin)) {
		if l.ServerID == id {
			t.Fatalf("completed deletion still listed after a week: %+v", l)
		}
	}
}

// Agent down or failing: the delete still goes through, the failure is visible on the deletion
// and retried until the agent confirms.
func TestDeletionRetriesFailures(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := e.create(t, paper("survival")).ID
	e.agent.set(container("survival", id, 1, agentapi.StateExited, ""))
	e.agent.setErr(agentapi.ErrUnavailable)
	d := e.deleteServer(t, id, "")
	if d.State != "failed" || d.ContainerError == nil || !strings.Contains(*d.ContainerError, "agent is not reachable") {
		t.Fatalf("deletion with the agent down = %+v", d)
	}
	e.agent.setErr(nil)
	e.clk.Advance(time.Minute)
	if calls := e.deletionPass(); !slices.Equal(calls, []string{fmt.Sprintf("remove %d", id)}) {
		t.Fatalf("calls once the agent is back = %v", calls)
	}
	// the remove fails in the agent: its error is shown, the remove is asked again later
	failed := container("survival", id, 1, agentapi.StateExited, "")
	failed.LastError = "docker: removal of container failed"
	e.agent.set(failed)
	e.deletionPass()
	if d := e.deletion(t, id); d.State != "failed" || d.ContainerError == nil || *d.ContainerError != failed.LastError {
		t.Fatalf("deletion after a failed remove = %+v", d)
	}
	e.clk.Advance(time.Hour)
	if calls := e.deletionPass(); !slices.Equal(calls, []string{fmt.Sprintf("remove %d", id)}) {
		t.Fatalf("calls after the retry delay = %v", calls)
	}
	e.agent.confirmRemoved(id)
	e.deletionPass()
	if d := e.deletion(t, id); d.State != "done" || d.ContainerError != nil {
		t.Fatalf("deletion = %+v", d)
	}
}

// A container of another server under the same ID is never removed; the deletion completes
// without it. A server without instance has nothing to remove.
func TestDeletionLeavesForeignContainer(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := e.create(t, paper("survival")).ID
	e.agent.set(container("other", id, 1, agentapi.StateRunning, ""))
	d := e.deleteServer(t, id, "")
	if d.State != "done" || d.Container != "foreign" {
		t.Fatalf("deletion = %+v", d)
	}
	if calls := e.agent.takeCalls(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
}

// Issue #148: a failed purge reaches the caller (backups failed, state partial once the
// container is gone) and is retried, also on request.
func TestDeletionPurgeFailureVisibleAndRetried(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := e.create(t, paper("survival")).ID
	e.purger.setErr(errors.New("files: repository busy at /var/lib/x"))
	d := e.deleteServer(t, id, "?purge_backups=1")
	if d.Backups != "failed" || d.BackupsError == nil || strings.Contains(*d.BackupsError, "/var/") {
		t.Fatalf("deletion = %+v", d)
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if purged, ok := en.Details["backups_purged"].(bool); !ok || purged {
		t.Fatalf("audit = %+v", en)
	}
	e.agent.confirmRemoved(id)
	e.deletionPass() // within the purge retry delay: the purge waits
	if d := e.deletion(t, id); d.State != "partial" || d.Container != "removed" {
		t.Fatalf("deletion = %+v", d)
	}
	if n := len(e.purger.takeCalls()); n != 1 {
		t.Fatalf("purges = %d", n)
	}
	e.purger.setErr(nil)
	rec := e.do(http.MethodPost, fmt.Sprintf("/servers/deletions/%d/retry", id), nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	if d := decode[deletionResp](t, rec); d.State != "done" || d.Backups != "purged" || d.BackupsError != nil {
		t.Fatalf("after the retry = %+v", d)
	}
	if rec := e.do(http.MethodPost, fmt.Sprintf("/servers/deletions/%d/retry", id), nil, &admin); rec.Code != http.StatusConflict {
		t.Fatalf("retry of a complete deletion: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, "/servers/deletions/999/retry", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("retry of an unknown deletion: %d", rec.Code)
	}
}
