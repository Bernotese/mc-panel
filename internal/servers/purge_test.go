package servers_test

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// Backups spec §5.6: DELETE /servers/{id}?purge_backups=1 deletes the restic repository after
// the server is gone; without the parameter the repository stays.
func TestDeletePurgesBackupsOnlyOnRequest(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	keep := storetest.CreateServer(t, e.db, "keep")
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", keep), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete without purge: %d %s", rec.Code, rec.Body)
	}
	if calls := e.purger.takeCalls(); len(calls) != 0 {
		t.Fatalf("purged without the parameter: %v", calls)
	}
	if _, ok := e.rec.Entries[len(e.rec.Entries)-1].Details["backups_purged"]; ok {
		t.Fatal("audit names backups_purged without the parameter")
	}

	for _, v := range []string{"1", "true"} {
		id := storetest.CreateServer(t, e.db, "purge-"+v)
		rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=%s", id, v), nil, &admin)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("purge_backups=%s: %d %s", v, rec.Code, rec.Body)
		}
		if calls := e.purger.takeCalls(); !slices.Equal(calls, []int64{id}) {
			t.Fatalf("purge_backups=%s: purger calls %v", v, calls)
		}
		en := e.rec.Entries[len(e.rec.Entries)-1]
		if purged, _ := en.Details["backups_purged"].(bool); en.Action != audit.ActionServerDelete || !purged {
			t.Fatalf("audit = %+v", en)
		}
	}
	for _, v := range []string{"0", "false"} {
		id := storetest.CreateServer(t, e.db, "nopurge-"+v)
		if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=%s", id, v), nil, &admin); rec.Code != http.StatusNoContent {
			t.Fatalf("purge_backups=%s: %d", v, rec.Code)
		}
		if calls := e.purger.takeCalls(); len(calls) != 0 {
			t.Fatalf("purge_backups=%s purged: %v", v, calls)
		}
	}
}

// A failed purge leaves the server deleted (204) and an orphaned repository; the audit entry
// says so.
func TestDeleteSurvivesAFailedPurge(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := storetest.CreateServer(t, e.db, "lobby")
	e.purger.setErr(errors.New("files service down"))
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=1", id), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete with failing purge: %d %s", rec.Code, rec.Body)
	}
	if n := e.count(t, "servers"); n != 0 {
		t.Fatalf("server rows left: %d", n)
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if purged, ok := en.Details["backups_purged"].(bool); !ok || purged {
		t.Fatalf("audit = %+v, want backups_purged false", en)
	}
	if !strings.Contains(e.logs.String(), "purging the backups failed") {
		t.Fatalf("log = %q", e.logs.String())
	}
}

// An invalid value is refused before anything happens; a delete that fails does not purge.
func TestDeletePurgeValidationAndFailedDelete(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	stub := storetest.CreateServer(t, e.db, "lobby")
	rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=yes", stub), nil, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("purge_backups=yes: %d %s", rec.Code, rec.Body)
	}
	if n := e.count(t, "servers"); n != 1 {
		t.Fatalf("refused delete removed rows: %d", n)
	}
	s := e.create(t, paper("survival"))
	e.agent.setErr(agentapi.ErrUnavailable)
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=1", s.ID), nil, &admin); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("delete with agent down: %d %s", rec.Code, rec.Body)
	}
	if calls := e.purger.takeCalls(); len(calls) != 0 {
		t.Fatalf("a failed delete purged: %v", calls)
	}
}
