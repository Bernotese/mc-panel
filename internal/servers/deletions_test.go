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
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/servers/admit"
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

// adminNow is the admin with a sudo window from the fake clock's now (tests that advance it).
func (e *env) adminNow() principal.Principal {
	p := e.admin()
	p.SudoUntil = e.clk.Now().Add(5 * time.Minute)
	return p
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

// Review #181: later completions, retries and abandons are audited, and a deletion that is still
// stuck 10 minutes after the request is reported once.
func TestDeletionAuditAndNotify(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := e.create(t, paper("survival")).ID
	e.agent.setErr(agentapi.ErrUnavailable)
	e.deleteServer(t, id, "")
	if en := e.rec.Entries[len(e.rec.Entries)-1]; en.Action != audit.ActionServerDelete {
		t.Fatalf("audit = %+v", en)
	} else if completed, ok := en.Details["completed"].(bool); !ok || completed {
		t.Fatalf("audit = %+v", en)
	}
	e.clk.Advance(5 * time.Minute)
	e.deletionPass()
	if n := len(e.notes.Events); n != 0 {
		t.Fatalf("notified before the alert delay: %v", e.notes.Types())
	}
	e.clk.Advance(6 * time.Minute)
	e.deletionPass()
	e.clk.Advance(time.Hour)
	e.deletionPass()
	if got := e.notes.Types(); !slices.Equal(got, []notify.EventType{notify.EventServerDeleteFailed}) {
		t.Fatalf("events = %v", got)
	}
	if ev := e.notes.Events[0]; !strings.Contains(ev.Title, "survival") || !strings.Contains(ev.Message, "agent is not reachable") {
		t.Fatalf("event = %+v", ev)
	}

	// a retry by an admin is audited with the actor; the completion by the panel as system
	e.agent.setErr(nil)
	admin = e.adminNow()
	rec := e.do(http.MethodPost, fmt.Sprintf("/servers/deletions/%d/retry", id), nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry: %d %s", rec.Code, rec.Body)
	}
	if en := e.rec.Entries[len(e.rec.Entries)-1]; en.Action != audit.ActionServerDeleteRetry || en.ActorType == audit.ActorSystem {
		t.Fatalf("audit = %+v", en)
	}
	e.agent.confirmRemoved(id)
	e.deletionPass()
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if en.Action != audit.ActionServerDeleteCompleted || en.ActorType != audit.ActorSystem || en.Details["container"] != "removed" {
		t.Fatalf("audit = %+v", en)
	}
	if n := len(e.notes.Events); n != 1 {
		t.Fatalf("events = %v", e.notes.Types())
	}
}

// Review #181: an agent error of an earlier job of the instance is not shown as the error of the
// deletion; only the remove's own result is.
func TestDeletionIgnoresEarlierAgentError(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := e.create(t, paper("survival")).ID
	old := container("survival", id, 1, agentapi.StateExited, "")
	old.LastError = "start failed: port in use"
	e.agent.set(old)
	if d := e.deleteServer(t, id, ""); d.ContainerError != nil || d.State != "pending" {
		t.Fatalf("deletion = %+v", d)
	}
	// the agent has the remove queued: the old error is still in its status
	queued := old
	queued.Operation = agentapi.OpRemoving
	e.agent.set(queued)
	e.deletionPass()
	if d := e.deletion(t, id); d.ContainerError != nil {
		t.Fatalf("deletion shows the earlier error: %+v", d)
	}
	// the remove ran and failed: now its error is shown
	failed := old
	failed.LastError = "remove: archive the data: permission denied"
	e.agent.set(failed)
	e.deletionPass()
	if d := e.deletion(t, id); d.ContainerError == nil || *d.ContainerError != failed.LastError {
		t.Fatalf("deletion after the failed remove = %+v", d)
	}
}

// Review #181: a deletion that cannot finish can be abandoned (admin, sudo, audited): the name is
// free again and nothing more is asked of the agent. An agent without containers has nothing to
// remove.
func TestDeletionAbandonAndDisabledAgent(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := e.create(t, paper("survival")).ID
	unmanaged := container("survival", id, 1, agentapi.StateRunning, "")
	e.agent.set(unmanaged)
	e.agent.setActionErr(errors.New("container mcpanel-mc-1 exists but is not managed by mc-panel"))
	e.deleteServer(t, id, "")
	e.agent.setActionErr(nil)
	noSudo := admin
	noSudo.SudoUntil = time.Time{}
	path := fmt.Sprintf("/servers/deletions/%d/abandon", id)
	if rec := e.do(http.MethodPost, path, nil, &noSudo); rec.Code != http.StatusForbidden {
		t.Fatalf("abandon without sudo: %d", rec.Code)
	}
	rec := e.do(http.MethodPost, path, nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("abandon: %d %s", rec.Code, rec.Body)
	}
	if d := decode[deletionResp](t, rec); d.State != "abandoned" || d.CompletedAt == nil || d.Container != "pending" {
		t.Fatalf("abandoned = %+v", d)
	}
	if en := e.rec.Entries[len(e.rec.Entries)-1]; en.Action != audit.ActionServerDeleteAbandon || en.Details["name"] != "survival" {
		t.Fatalf("audit = %+v", en)
	}
	if rec := e.do(http.MethodPost, path, nil, &admin); rec.Code != http.StatusConflict {
		t.Fatalf("second abandon: %d", rec.Code)
	}
	e.agent.takeCalls()
	if calls := e.deletionPass(); len(calls) != 0 {
		t.Fatalf("an abandoned deletion still asks the agent: %v", calls)
	}
	if rec := e.do(http.MethodPost, "/servers", paper("survival"), &admin); rec.Code != http.StatusCreated {
		t.Fatalf("name after abandon: %d %s", rec.Code, rec.Body)
	}

	e.agent.disabled = true
	other := e.create(t, paper("creative")).ID
	if d := e.deleteServer(t, other, ""); d.State != "done" || d.Container != "none" {
		t.Fatalf("deletion with an agent without containers = %+v", d)
	}
}

// Review #181: one agent status per pass for all open deletions.
func TestDeletionPassAsksAgentOnce(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e.deleteServer(t, e.create(t, paper(name)).ID, "")
	}
	before := e.agent.statusCalls()
	e.clk.Advance(time.Minute)
	e.deletionPass()
	if n := e.agent.statusCalls() - before; n != 1 {
		t.Fatalf("status calls in one pass = %d", n)
	}
}

// Review #181: a deleted server gets no admitted job (a queued dev sync of a deleted dev would
// send a power job and clear the agent's confirmation).
func TestAdmitRefusesDeletedServer(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := e.create(t, paper("survival")).ID
	e.deleteServer(t, id, "")
	ran := false
	err := e.h.Admit(context.Background(), id, nil, func(context.Context) error { ran = true; return nil })
	if !errors.Is(err, admit.ErrDeleted) || ran {
		t.Fatalf("Admit = %v (ran %v)", err, ran)
	}
}
