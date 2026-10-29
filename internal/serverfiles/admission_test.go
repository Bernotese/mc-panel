package serverfiles_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
	"github.com/Bernotese/mc-panel/internal/servers/admit"
)

// fakeAdmit is servers.Handler.Admit: refuse (nil = admit) answers before fn; inside reports
// whether an fn runs.
type fakeAdmit struct {
	mu     sync.Mutex
	refuse error
	in     bool
	ids    []int64
}

func (f *fakeAdmit) Admit(ctx context.Context, id int64, fn func(context.Context) error) error {
	f.mu.Lock()
	f.ids = append(f.ids, id)
	if f.refuse != nil {
		defer f.mu.Unlock()
		return f.refuse
	}
	f.in = true
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.in = false; f.mu.Unlock() }()
	return fn(ctx)
}

func (f *fakeAdmit) inside() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.in }

// An extract is admitted (data safety spec §3): the state check and the service call run under
// the server's admission, a refused admission answers its code without asking the agent or the
// service, and a server the agent has a start queued for is not stopped.
func TestExtractUsesAdmission(t *testing.T) {
	adm := &fakeAdmit{}
	e := newEnvWith(t, func(d *serverfiles.Deps) { d.Admit = adm.Admit })
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	body := map[string]string{"path": "w.zip", "dest": "w"}
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
		adm.refuse = c.err
		e.agent.onCall = func() { t.Error("the agent was asked although the admission was refused") }
		rec := e.do(http.MethodPost, srvPath(id, "/files/extract"), body, &admin)
		if rec.Code != c.status || errCode(t, rec) != c.code {
			t.Fatalf("%v: %d %s", c.err, rec.Code, rec.Body)
		}
	}
	if len(e.files.extracts) != 0 || len(e.rec.Entries) != 0 {
		t.Fatalf("refused extracts: service %v, audit %v", e.files.extracts, e.rec.Actions())
	}
	adm.refuse = nil

	queued := status(id, agentapi.StateExited, agentapi.OpNone)
	queued.Queued = []agentapi.PowerAction{agentapi.PowerStart}
	e.agent.set(queued)
	e.agent.onCall = nil
	rec := e.do(http.MethodPost, srvPath(id, "/files/extract"), body, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "instance_running" {
		t.Fatalf("extract with a start queued: %d %s", rec.Code, rec.Body)
	}

	e.agent.set(status(id, agentapi.StateExited, agentapi.OpNone))
	var checkedInside, calledInside bool
	e.agent.onCall = func() { checkedInside = adm.inside() }
	e.files.onExtract = func() { calledInside = adm.inside() }
	rec = e.do(http.MethodPost, srvPath(id, "/files/extract"), body, &admin)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("extract: %d %s", rec.Code, rec.Body)
	}
	if !checkedInside || !calledInside {
		t.Fatalf("state check inside the admission: %v, service call inside: %v", checkedInside, calledInside)
	}
	for _, got := range adm.ids {
		if got != id {
			t.Fatalf("admitted ids = %v", adm.ids)
		}
	}
}
