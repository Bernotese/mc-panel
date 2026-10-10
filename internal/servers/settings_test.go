package servers_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type unknownResp struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type settingsResp struct {
	BaseDomain          *string       `json:"base_domain"`
	CurseForgeAPIKeySet bool          `json:"curseforge_api_key_set"`
	Docker              string        `json:"docker"`
	UnknownContainers   []unknownResp `json:"unknown_containers"`
}

func (e *env) settings(t *testing.T) settingsResp {
	t.Helper()
	admin := e.admin()
	rec := e.do(http.MethodGet, "/servers/settings", nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /servers/settings: %d %s", rec.Code, rec.Body)
	}
	return decode[settingsResp](t, rec)
}

func (e *env) putSettings(t *testing.T, body string) settingsResp {
	t.Helper()
	admin := e.admin()
	rec := e.do(http.MethodPut, "/servers/settings", json.RawMessage(body), &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /servers/settings %s: %d %s", body, rec.Code, rec.Body)
	}
	return decode[settingsResp](t, rec)
}

func TestSettingsDefaultsAndDockerStatus(t *testing.T) {
	e := newEnv(t)
	s := e.settings(t)
	if s.BaseDomain != nil || s.CurseForgeAPIKeySet || s.Docker != "ok" || s.UnknownContainers == nil || len(s.UnknownContainers) != 0 {
		t.Fatalf("defaults = %+v", s)
	}
	e.agent.setDocker(agentapi.DockerUnavailable)
	if got := e.settings(t).Docker; got != "unavailable" {
		t.Fatalf("docker unavailable in response: %q", got)
	}
	e.agent.setDocker(agentapi.DockerOK)
	e.agent.setErr(agentapi.ErrDockerUnavailable)
	if got := e.settings(t).Docker; got != "unavailable" {
		t.Fatalf("docker_unavailable error: %q", got)
	}
	e.agent.setErr(agentapi.ErrUnavailable)
	if got := e.settings(t).Docker; got != "unknown" {
		t.Fatalf("agent down: %q", got)
	}
}

// Containers with the mcpanel.instance label that the database does not know are listed,
// never touched (spec §5.3).
func TestSettingsListsUnknownContainers(t *testing.T) {
	e := newEnv(t)
	known := createInstance(t, e.db, "survival")
	e.agent.set(agentapi.InstanceStatus{ID: known, Name: "survival", Revision: 1, State: agentapi.StateRunning})
	e.agent.set(agentapi.InstanceStatus{ID: 99, Name: "old", Revision: 3, State: agentapi.StateExited})
	e.agent.set(agentapi.InstanceStatus{ID: 42, Name: "older", Revision: 1, State: agentapi.StateRunning})
	got := e.settings(t).UnknownContainers
	want := []unknownResp{{ID: 42, Name: "older"}, {ID: 99, Name: "old"}}
	if !slices.Equal(got, want) {
		t.Fatalf("unknown containers = %+v, want %+v", got, want)
	}
}

func TestUpdateBaseDomainBumpsRevisions(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	s := e.putSettings(t, `{"base_domain":"MC.Example.com"}`)
	if s.BaseDomain == nil || *s.BaseDomain != "mc.example.com" {
		t.Fatalf("base_domain = %v, want lowercase mc.example.com", s.BaseDomain)
	}
	if rev := instanceRow(t, e.db, id).Revision; rev != 2 {
		t.Fatalf("revision after base domain change = %d, want 2", rev)
	}
	if len(e.rec.Entries) != 1 {
		t.Fatalf("audit = %v", e.rec.Actions())
	}
	en := e.rec.Entries[0]
	bd, _ := en.Details["base_domain"].(map[string]any)
	if en.Action != audit.ActionServerSettingsUpdate || en.Target != "servers" || bd["old"] != nil || bd["new"] != "mc.example.com" {
		t.Fatalf("audit entry = %+v", en)
	}

	// same value again: nothing changes, nothing is audited
	e.putSettings(t, `{"base_domain":"mc.example.com"}`)
	if rev := instanceRow(t, e.db, id).Revision; rev != 2 || len(e.rec.Entries) != 1 {
		t.Fatalf("unchanged base domain: revision %d, audit %v", rev, e.rec.Actions())
	}
	// absent field keeps the value
	if s := e.putSettings(t, `{}`); s.BaseDomain == nil || *s.BaseDomain != "mc.example.com" {
		t.Fatalf("empty PUT changed base_domain: %v", s.BaseDomain)
	}
	// null removes it (and is a change of the FQDN)
	if s := e.putSettings(t, `{"base_domain":null}`); s.BaseDomain != nil {
		t.Fatalf("null did not remove base_domain: %v", *s.BaseDomain)
	}
	if rev := instanceRow(t, e.db, id).Revision; rev != 3 {
		t.Fatalf("revision after removing base domain = %d, want 3", rev)
	}
}

func TestCurseForgeKeyIsSealedAndNeverReturned(t *testing.T) {
	e := newEnv(t)
	const key = "$2a$10$abcdefghijklmnopqrstuv"
	s := e.putSettings(t, `{"curseforge_api_key":"`+key+`"}`)
	if !s.CurseForgeAPIKeySet {
		t.Fatal("curseforge_api_key_set = false after setting a key")
	}
	var blob []byte
	if err := e.db.QueryRow(`SELECT curseforge_api_key FROM instance_settings WHERE id = 1`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), key) {
		t.Fatal("key stored in plaintext")
	}
	pt, err := e.box.Open(blob, []byte("instance_settings.curseforge_api_key"))
	if err != nil || string(pt) != key {
		t.Fatalf("sealed key does not open with the column AAD: %q, %v", pt, err)
	}
	admin := e.admin()
	if body := e.do(http.MethodGet, "/servers/settings", nil, &admin).Body.String(); strings.Contains(body, key) {
		t.Fatalf("GET leaks the key: %s", body)
	}
	raw, _ := json.Marshal(e.rec.Entries)
	if strings.Contains(string(raw), key) || e.rec.Entries[0].Details["curseforge_api_key"] != "set" {
		t.Fatalf("audit = %s", raw)
	}

	if s := e.putSettings(t, `{"curseforge_api_key":null}`); s.CurseForgeAPIKeySet {
		t.Fatal("null did not remove the key")
	}
	var isNull bool
	if err := e.db.QueryRow(`SELECT curseforge_api_key IS NULL FROM instance_settings WHERE id = 1`).Scan(&isNull); err != nil || !isNull {
		t.Fatalf("key column after removal: null=%t, %v", isNull, err)
	}
	if got := e.rec.Entries[1].Details["curseforge_api_key"]; got != "removed" {
		t.Fatalf("audit after removal = %v", got)
	}
}

func TestUpdateSettingsValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	for _, tc := range []struct {
		body, code string
	}{
		{`{"base_domain":"localhost"}`, "validation_failed"},
		{`{"base_domain":"mc.example.com:25565"}`, "validation_failed"},
		{`{"base_domain":"bad_label.example"}`, "validation_failed"},
		{`{"curseforge_api_key":"has space"}`, "validation_failed"},
		{`{"curseforge_api_key":"k\nX=1"}`, "validation_failed"},
		{`{"base_domain":42}`, "bad_request"},
		{`{"docker":"ok"}`, "bad_request"},
	} {
		rec := e.do(http.MethodPut, "/servers/settings", json.RawMessage(tc.body), &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != tc.code {
			t.Errorf("%s: %d %s, want 400 %s", tc.body, rec.Code, rec.Body, tc.code)
		}
	}
	if len(e.rec.Entries) != 0 {
		t.Fatalf("rejected updates were audited: %v", e.rec.Actions())
	}
}

func TestSettingsNeedAdminAndSudo(t *testing.T) {
	e := newEnv(t)
	bob := userP(storetest.CreateUser(t, e.db, "bob", "user"), "bob")
	if rec := e.do(http.MethodGet, "/servers/settings", nil, &bob); rec.Code != http.StatusForbidden {
		t.Fatalf("user GET: %d", rec.Code)
	}
	noSudo := e.admin()
	noSudo.SudoUntil = t0.Add(-time.Second)
	rec := e.do(http.MethodPut, "/servers/settings", json.RawMessage(`{"base_domain":"mc.example.com"}`), &noSudo)
	if rec.Code != http.StatusForbidden || errCode(t, rec) != "sudo_required" {
		t.Fatalf("PUT without sudo: %d %s", rec.Code, rec.Body)
	}
}
