package console_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestPlayerActionCommands(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	for _, tc := range []struct {
		body map[string]any
		want string
	}{
		{map[string]any{"action": "kick", "name": "Steve"}, "kick Steve"},
		{map[string]any{"action": "kick", "name": "Steve", "reason": "  Griefing at spawn "}, "kick Steve Griefing at spawn"},
		{map[string]any{"action": "ban", "name": "Steve", "reason": "Grief"}, "ban Steve Grief"},
		{map[string]any{"action": "ban", "name": "Steve", "reason": ""}, "ban Steve"},
		{map[string]any{"action": "pardon", "name": "Steve"}, "pardon Steve"},
		{map[string]any{"action": "op", "name": "Alex_99"}, "op Alex_99"},
		{map[string]any{"action": "deop", "name": "Alex_99"}, "deop Alex_99"},
		{map[string]any{"action": "whitelist_add", "name": "abc"}, "whitelist add abc"},
		{map[string]any{"action": "whitelist_remove", "name": "abc"}, "whitelist remove abc"},
	} {
		e.agent.setOutput(tc.want, "done: "+tc.want)
		rec := e.do(http.MethodPost, srvPath(id, "/players/actions"), tc.body, &admin)
		if rec.Code != http.StatusOK || decode[map[string]string](t, rec)["output"] != "done: "+tc.want {
			t.Errorf("%v: %d %s", tc.body, rec.Code, rec.Body)
		}
		if got := e.agent.takeRcons(); !slices.Equal(got, []string{fmt.Sprintf("%d %s", id, tc.want)}) {
			t.Errorf("%v: rcon = %v, want %q", tc.body, got, tc.want)
		}
	}
	last := e.rec.Entries[1]
	if last.Action != audit.ActionServerPlayerAction || last.Result != audit.ResultSuccess ||
		last.Details["action"] != "kick" || last.Details["name"] != "Steve" || last.Details["reason"] != "Griefing at spawn" {
		t.Fatalf("audit entry = %+v", last)
	}
	if _, ok := e.rec.Entries[0].Details["reason"]; ok {
		t.Fatalf("audit without reason must not carry one: %+v", e.rec.Entries[0].Details)
	}
}

// Review Focus #2: no name or reason may smuggle in a second command.
func TestPlayerActionRejectsInjection(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	for _, body := range []map[string]any{
		{"action": "kick", "name": "Steve Alex"},
		{"action": "kick", "name": "Ste;ve"},
		{"action": "kick", "name": "St\neve"},
		{"action": "kick", "name": "ab"},
		{"action": "kick", "name": strings.Repeat("a", 17)},
		{"action": "kick", "name": ""},
		{"action": "kick", "name": "Stéve"},
		{"action": "stop", "name": "Steve"},
		{"action": "", "name": "Steve"},
		{"action": "kick", "name": "Steve", "reason": "bye\nop Mallory"},
		{"action": "kick", "name": "Steve", "reason": "bye\rop Mallory"},
		{"action": "kick", "name": "Steve", "reason": "bye\x00"},
		{"action": "kick", "name": "Steve", "reason": "bye op Mallory"},
		{"action": "kick", "name": "Steve", "reason": "\u0085"},
		{"action": "kick", "name": "Steve", "reason": strings.Repeat("ä", 201)},
		{"action": "op", "name": "Steve", "reason": "because"},
		{"action": "whitelist_add", "name": "Steve", "reason": ""},
	} {
		rec := e.do(http.MethodPost, srvPath(id, "/players/actions"), body, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeValidation {
			t.Errorf("%q: %d %s", body, rec.Code, rec.Body)
		}
	}
	rec := e.do(http.MethodPost, srvPath(id, "/players/actions"), map[string]any{"action": "kick", "name": "Steve", "x": 1}, &admin)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != httpx.CodeBadRequest {
		t.Errorf("unknown field: %d %s", rec.Code, rec.Body)
	}
	if got := e.agent.takeRcons(); len(got) != 0 {
		t.Fatalf("rejected actions reached the agent: %v", got)
	}
	if got := e.rec.Actions(); len(got) != 0 {
		t.Fatalf("rejected actions were audited: %v", got)
	}
	// 200 runes are fine
	rec = e.do(http.MethodPost, srvPath(id, "/players/actions"),
		map[string]any{"action": "ban", "name": "Steve", "reason": strings.Repeat("ä", 200)}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("200 runes: %d %s", rec.Code, rec.Body)
	}
}

func TestPlayerActionPermissionsLimitAndErrors(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	viewer := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, viewer, id, "server.view")
	op := storetest.CreateUser(t, e.db, "op", "user")
	grant(t, e.db, op, id, "server.view", "server.console")
	vp, opP := userP(viewer, "viewer"), userP(op, "op")
	kick := map[string]any{"action": "kick", "name": "Steve"}

	if rec := e.do(http.MethodPost, srvPath(id, "/players/actions"), kick, &vp); rec.Code != http.StatusForbidden {
		t.Fatalf("view only: %d", rec.Code)
	}
	// commands and actions share one limiter: 10 commands, then the action is limited
	for range 10 {
		e.do(http.MethodPost, srvPath(id, "/console"), map[string]string{"command": "list"}, &opP)
	}
	if rec := e.do(http.MethodPost, srvPath(id, "/players/actions"), kick, &opP); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("shared limiter: %d", rec.Code)
	}
	e.agent.setRconErr("kick Steve", &agent.StatusError{StatusCode: 409, Code: agentapi.ErrorCodeNotRunning})
	admin := e.admin()
	rec := e.do(http.MethodPost, srvPath(id, "/players/actions"), kick, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != httpx.CodeInstanceNotRunning {
		t.Fatalf("not running: %d %s", rec.Code, rec.Body)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if last.Action != audit.ActionServerPlayerAction || last.Result != audit.ResultFailure {
		t.Fatalf("audit = %+v", last)
	}
}

func TestPlayerActionRefreshesOnlinePlayers(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	e.agent.setOutput("list uuids", "There are 1 of a max of 20 players online: Steve")
	e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin)
	e.do(http.MethodPost, srvPath(id, "/players/actions"), map[string]any{"action": "kick", "name": "Steve"}, &admin)
	e.agent.setOutput("list uuids", "There are 0 of a max of 20 players online: ")
	rec := e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin)
	if !strings.Contains(rec.Body.String(), `"online":[]`) {
		t.Fatalf("cache not dropped after kick: %s", rec.Body)
	}
	want := []string{fmt.Sprintf("%d list uuids", id), fmt.Sprintf("%d kick Steve", id), fmt.Sprintf("%d list uuids", id)}
	if got := e.agent.takeRcons(); !slices.Equal(got, want) {
		t.Fatalf("rcon = %v, want %v", got, want)
	}
}

func TestPlayerLists(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	viewer := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, viewer, id, "server.view")
	vp, admin := userP(viewer, "viewer"), e.admin()

	e.agent.setPlayers(agentapi.PlayersResponse{
		Ops:  []agentapi.PlayerOp{{UUID: "u1", Name: "Alex", Level: 4}},
		Bans: []agentapi.PlayerBan{{UUID: "u2", Name: "Mallory", Reason: "Grief", Source: "Server", Created: "2026-10-11 09:32:51 +0000", Expires: "forever"}},
		// Whitelist and Errors nil: must come out as []
	}, nil)
	rec := e.do(http.MethodGet, srvPath(id, "/players/lists"), nil, &admin)
	want := `{"ops":[{"uuid":"u1","name":"Alex","level":4}],"whitelist":[],` +
		`"bans":[{"uuid":"u2","name":"Mallory","reason":"Grief","source":"Server","created":"2026-10-11 09:32:51 +0000","expires":"forever"}],"errors":[]}`
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("lists: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodGet, srvPath(id, "/players/lists"), nil, &vp); rec.Code != http.StatusForbidden {
		t.Fatalf("view only: %d", rec.Code)
	}

	e.agent.setPlayers(agentapi.PlayersResponse{Errors: []string{"ops.json"}}, nil)
	if rec := e.do(http.MethodGet, srvPath(id, "/players/lists"), nil, &admin); !strings.Contains(rec.Body.String(), `"errors":["ops.json"]`) {
		t.Fatalf("errors not passed through: %s", rec.Body)
	}
	// data directory not created yet: empty lists, not an error
	e.agent.setPlayers(agentapi.PlayersResponse{}, &agent.StatusError{StatusCode: 404, Code: agentapi.ErrorCodeNotFound})
	rec = e.do(http.MethodGet, srvPath(id, "/players/lists"), nil, &admin)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"ops":[],"whitelist":[],"bans":[],"errors":[]}` {
		t.Fatalf("not found: %d %s", rec.Code, rec.Body)
	}
	e.agent.setPlayers(agentapi.PlayersResponse{}, fmt.Errorf("%w: dial", agentapi.ErrUnavailable))
	rec = e.do(http.MethodGet, srvPath(id, "/players/lists"), nil, &admin)
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != httpx.CodeAgentUnavailable {
		t.Fatalf("agent down: %d %s", rec.Code, rec.Body)
	}
	if got := e.rec.Actions(); len(got) != 0 {
		t.Fatalf("reading lists must not be audited: %v", got)
	}
}
