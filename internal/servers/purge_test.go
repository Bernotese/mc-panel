package servers_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// Backups spec §5.6: DELETE /servers/{id}?purge_backups=1 deletes the restic repository after
// the server is gone; without the parameter the repository stays.
func TestDeletePurgesBackupsOnlyOnRequest(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	keep := storetest.CreateServer(t, e.db, "keep")
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", keep), nil, &admin); rec.Code != http.StatusAccepted {
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
		if rec.Code != http.StatusAccepted {
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
		if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=%s", id, v), nil, &admin); rec.Code != http.StatusAccepted {
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
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=1", id), nil, &admin); rec.Code != http.StatusAccepted {
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

// An invalid value is refused before anything happens.
func TestDeletePurgeValidationAndAgentDown(t *testing.T) {
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
	// issue #148: the agent being down does not hold the purge back; the container is removed
	// later from the tombstone
	s := e.create(t, paper("survival"))
	e.agent.setErr(agentapi.ErrUnavailable)
	rec = e.do(http.MethodDelete, fmt.Sprintf("/servers/%d?purge_backups=1", s.ID), nil, &admin)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete with agent down: %d %s", rec.Code, rec.Body)
	}
	if d := decode[deletionResp](t, rec); d.Container != "pending" || d.Backups != "purged" {
		t.Fatalf("deletion = %+v", d)
	}
	if calls := e.purger.takeCalls(); !slices.Equal(calls, []int64{s.ID}) {
		t.Fatalf("purges = %v", calls)
	}
}

// serve runs the handler on a real HTTP server (WriteTimeout, connection-bound request
// contexts) as the admin; seen receives the context of every request.
func (e *env) serve(t *testing.T, writeTimeout time.Duration, seen chan<- context.Context) *httptest.Server {
	t.Helper()
	admin := e.admin()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			seen <- r.Context()
		}
		ctx := httpx.WithClientIP(r.Context(), netip.MustParseAddr("203.0.113.7"))
		e.mux.ServeHTTP(w, r.WithContext(principal.With(ctx, admin)))
	}))
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// A purge that takes longer than the server's WriteTimeout still answers 204: the answer gets a
// write deadline past the purge, and the purge its own deadline (filesapi.DeleteTimeout plus a
// minute for ending a running backup job).
func TestDeletePurgeOutlastsWriteTimeout(t *testing.T) {
	e := newEnv(t)
	id := storetest.CreateServer(t, e.db, "big")
	type seen struct {
		err      error
		deadline time.Time
	}
	got := make(chan seen, 1)
	e.purger.setHook(func(ctx context.Context) {
		time.Sleep(400 * time.Millisecond) // well past the WriteTimeout below
		d, _ := ctx.Deadline()
		got <- seen{ctx.Err(), d}
	})
	srv := e.serve(t, 100*time.Millisecond, nil)
	start := time.Now()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, srv.URL+fmt.Sprintf("/servers/%d?purge_backups=1", id), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("delete with a slow purge: %v (the answer was lost to the WriteTimeout)", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("delete with a slow purge: %d", resp.StatusCode)
	}
	s := <-got
	if s.err != nil {
		t.Fatalf("purge context ended early: %v", s.err)
	}
	want := filesapi.DeleteTimeout + time.Minute
	if s.deadline.IsZero() || s.deadline.Before(start.Add(want-30*time.Second)) || s.deadline.After(start.Add(want+30*time.Second)) {
		t.Fatalf("purge deadline %v, want about %s from the start", s.deadline, want)
	}
}

// A client that goes away during the purge neither cancels it nor loses the audit entry.
func TestDeletePurgeSurvivesAClientThatGoesAway(t *testing.T) {
	e := newEnv(t)
	id := storetest.CreateServer(t, e.db, "big")
	started, release := make(chan struct{}), make(chan struct{})
	purgeErr := make(chan error, 1)
	e.purger.setHook(func(ctx context.Context) {
		close(started)
		<-release
		purgeErr <- ctx.Err()
	})
	seen := make(chan context.Context, 1)
	srv := e.serve(t, time.Minute, seen)
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, srv.URL+fmt.Sprintf("/servers/%d?purge_backups=1", id), nil)
	if err != nil {
		t.Fatal(err)
	}
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		if resp, err := srv.Client().Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	reqCtx := <-seen
	<-started
	cancel()
	<-clientDone
	select {
	case <-reqCtx.Done(): // the server noticed the client is gone
	case <-time.After(5 * time.Second):
		t.Fatal("the request context was not canceled after the client went away")
	}
	close(release)
	if err := <-purgeErr; err != nil {
		t.Fatalf("the purge was canceled with the client: %v", err)
	}
	srv.Close() // waits for the handler
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if purged, _ := en.Details["backups_purged"].(bool); en.Action != audit.ActionServerDelete || !purged {
		t.Fatalf("audit = %+v", en)
	}
}
