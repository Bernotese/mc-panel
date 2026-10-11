package console_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestCommandSendsAndAudits(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	e.agent.setOutput("list", "There are 0 of a max of 20 players online: ")
	admin := e.admin()

	rec := e.do(http.MethodPost, srvPath(id, "/console"), map[string]string{"command": "/list"}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("command: %d %s", rec.Code, rec.Body)
	}
	if got := decode[map[string]string](t, rec)["output"]; got != "There are 0 of a max of 20 players online: " {
		t.Fatalf("output = %q", got)
	}
	// only ONE leading slash is removed; without a slash the command is sent as is
	e.do(http.MethodPost, srvPath(id, "/console"), map[string]string{"command": "//sethome"}, &admin)
	e.do(http.MethodPost, srvPath(id, "/console"), map[string]string{"command": "say hi"}, &admin)

	want := []string{fmt.Sprintf("%d list", id), fmt.Sprintf("%d /sethome", id), fmt.Sprintf("%d say hi", id)}
	if got := e.agent.takeRcons(); !slices.Equal(got, want) {
		t.Fatalf("rcon calls = %v, want %v", got, want)
	}
	if got := e.rec.Actions(); len(got) != 3 || got[0] != audit.ActionServerConsoleCommand {
		t.Fatalf("audit = %v", got)
	}
	en := e.rec.Entries[0]
	if en.Target != fmt.Sprintf("server:%d", id) || en.Result != audit.ResultSuccess || en.Details["command"] != "list" {
		t.Fatalf("audit entry = %+v", en)
	}
}

func TestCommandPermissions(t *testing.T) {
	e := newEnv(t)
	a := createInstance(t, e.db, "alpha")
	b := createInstance(t, e.db, "beta")
	viewer := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, viewer, a, "server.view", "server.power")
	op := storetest.CreateUser(t, e.db, "op", "user")
	grant(t, e.db, op, a, "server.view", "server.console")
	vp, opP := userP(viewer, "viewer"), userP(op, "op")
	body := map[string]string{"command": "list"}

	if rec := e.do(http.MethodPost, srvPath(a, "/console"), body, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, srvPath(a, "/console"), body, &vp); rec.Code != http.StatusForbidden {
		t.Errorf("view+power only: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, srvPath(b, "/console"), body, &opP); rec.Code != http.StatusForbidden {
		t.Errorf("other server: %d", rec.Code)
	}
	if rec := e.do(http.MethodPost, srvPath(a, "/console"), body, &opP); rec.Code != http.StatusOK {
		t.Errorf("console: %d %s", rec.Code, rec.Body)
	}
	if got := e.agent.takeRcons(); len(got) != 1 {
		t.Fatalf("rcon calls = %v, want exactly the permitted one", got)
	}
}

func TestCommandValidation(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	for _, cmd := range []string{
		"", "/", "   ", "/   ", "say\nhi", "say\rhi", "say\thi", "a\x00b", "a\x7fb", "\xff\xfe",
		strings.Repeat("a", 1001), "/" + strings.Repeat("a", 1001),
	} {
		rec := e.do(http.MethodPost, srvPath(id, "/console"), map[string]string{"command": cmd}, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("command %q: %d %s", cmd, rec.Code, rec.Body)
		}
	}
	rec := e.do(http.MethodPost, srvPath(id, "/console"), map[string]any{"command": "list", "extra": 1}, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeBadRequest {
		t.Errorf("unknown field: %d %s", rec.Code, rec.Body)
	}
	if got := e.agent.takeRcons(); len(got) != 0 {
		t.Fatalf("rejected commands reached the agent: %v", got)
	}
	if got := e.rec.Actions(); len(got) != 0 {
		t.Fatalf("rejected commands were audited: %v", got)
	}
	// 1000 bytes after removing the slash are fine
	rec = e.do(http.MethodPost, srvPath(id, "/console"), map[string]string{"command": "/" + strings.Repeat("a", 1000)}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("1000 bytes: %d %s", rec.Code, rec.Body)
	}
}

func TestCommandUnknownOrNotConfiguredServer(t *testing.T) {
	e := newEnv(t)
	stub := storetest.CreateServer(t, e.db, "stub") // TP1 stub without instance row
	admin := e.admin()
	for _, p := range []string{srvPath(stub, "/console"), "/servers/999/console", "/servers/abc/console"} {
		rec := e.do(http.MethodPost, p, map[string]string{"command": "list"}, &admin)
		if rec.Code != http.StatusNotFound || errCode(t, rec) != httpx.CodeNotFound {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
	if got := e.agent.takeRcons(); len(got) != 0 {
		t.Fatalf("agent called: %v", got)
	}
}

func TestCommandAgentErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"not running", &agent.StatusError{StatusCode: 409, Code: agentapi.ErrorCodeNotRunning, Message: "not running"}, 409, httpx.CodeInstanceNotRunning},
		{"container missing", &agent.StatusError{StatusCode: 404, Code: agentapi.ErrorCodeNotFound, Message: "no container"}, 409, httpx.CodeInstanceNotRunning},
		{"rcon not ready", &agent.StatusError{StatusCode: 503, Code: agentapi.ErrorCodeRconUnavailable, Message: "connection refused"}, 503, httpx.CodeRconUnavailable},
		{"docker down", &agent.StatusError{StatusCode: 503, Code: agentapi.ErrorCodeDockerUnavailable, Message: "docker"}, 503, httpx.CodeDockerUnavailable},
		{"busy", &agent.StatusError{StatusCode: 429, Code: agentapi.ErrorCodeBusy, Message: "busy"}, 429, httpx.CodeRateLimited},
		{"agent rejects", &agent.StatusError{StatusCode: 400, Message: "bad command"}, 400, httpx.CodeValidation},
		{"agent down", fmt.Errorf("%w: dial unix", agentapi.ErrUnavailable), 503, httpx.CodeAgentUnavailable},
		{"timeout", context.DeadlineExceeded, 503, httpx.CodeAgentUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			id := createInstance(t, e.db, "survival")
			e.agent.setRconErr("list", tc.err)
			admin := e.admin()
			rec := e.do(http.MethodPost, srvPath(id, "/console"), map[string]string{"command": "list"}, &admin)
			if rec.Code != tc.status || errCode(t, rec) != tc.code {
				t.Fatalf("%d %s, want %d %s", rec.Code, rec.Body, tc.status, tc.code)
			}
			if len(e.rec.Entries) != 1 || e.rec.Entries[0].Result != audit.ResultFailure ||
				e.rec.Entries[0].Action != audit.ActionServerConsoleCommand {
				t.Fatalf("audit = %+v", e.rec.Entries)
			}
		})
	}
}

func TestCommandWithoutAgent(t *testing.T) {
	e := newEnvWith(t, func(d *console.Deps) { d.Agent = nil })
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	rec := e.do(http.MethodPost, srvPath(id, "/console"), map[string]string{"command": "list"}, &admin)
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != httpx.CodeAgentUnavailable {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestCommandRateLimit(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	op := storetest.CreateUser(t, e.db, "op", "user")
	grant(t, e.db, op, id, "server.view", "server.console")
	opP := userP(op, "op")
	send := func(p *principal.Principal, cmd string) int {
		return e.do(http.MethodPost, srvPath(id, "/console"), map[string]string{"command": cmd}, p).Code
	}
	// rejected (invalid) commands do not use up tokens
	for range 5 {
		if c := send(&opP, "bad\ncmd"); c != http.StatusBadRequest {
			t.Fatalf("invalid: %d", c)
		}
	}
	for i := range 10 {
		if c := send(&opP, "list"); c != http.StatusOK {
			t.Fatalf("command %d: %d", i+1, c)
		}
	}
	rec := e.do(http.MethodPost, srvPath(id, "/console"), map[string]string{"command": "list"}, &opP)
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != httpx.CodeRateLimited || rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("11th: %d %s Retry-After=%q", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
	admin := e.admin()
	if c := send(&admin, "list"); c != http.StatusOK {
		t.Fatalf("other user: %d", c)
	}
	e.clk.Advance(2 * time.Second)
	if c := send(&opP, "list"); c != http.StatusOK {
		t.Fatalf("after 2 s: %d", c)
	}
	if got := len(e.rec.Entries); got != 12 {
		t.Fatalf("audit entries = %d, want 12 (rate-limited request not audited)", got)
	}
}
