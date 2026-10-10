# Teilprojekt 2 – panel (Aufgaben 11–16) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das panel bekommt das Paket `internal/servers`: Server mit Instanz-Konfiguration anlegen, ändern, löschen, starten/stoppen/neu starten, Laufzeit-Einstellungen (Basisdomain, CurseForge-Key), Versionskataloge und eine Abgleichsschleife, die den Soll-Zustand aus der DB über den agent herstellt.

**Architecture:** `internal/servers.Handler` besitzt alle `/servers*`-Routen (aus `internal/users` herausgelöst), einen Status-Cache mit dem letzten `InstancesResponse` des agents und die Abgleichsschleife (`Run`/`ReconcileOnce`, alle 10 s und nach jedem `Trigger()`). Schreibende Nutzeraktionen ändern nur die DB und stoßen den Abgleich an; nur `DELETE` eines eingerichteten Servers und `restart` sprechen den agent synchron an. `internal/servers/catalog` holt Versionslisten von festen Upstream-URLs (Timeout, Größenlimit, 1-h-Cache). `cmd/panel/app.go` verdrahtet alles mit demselben `*agent.Client`, der schon die Firewall bedient.

**Tech Stack:** Go (Standardbibliothek, chi, sqlc/`store`, `secretbox`), OpenAPI 3.1 + openapi-typescript, MSW 2, Vitest. Keine neuen Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md` (§3.1, §4, §5.3, §6, §8). Verträge V1–V5: `docs/superpowers/plans/2026-10-10-instances-00-overview.md`.

## Global Constraints

Alle Global Constraints und Verträge aus `2026-10-10-instances-00-overview.md` gelten (und damit TP1 und `CLAUDE.md`): Routen nur über `*rbac.Router` mit Regel, Fehler nur über `httpx.WriteError` mit Codes aus `internal/httpx/errors.go`, `clock.Clock` statt `time.Now()` für Zeitlogik, Kontext überall, Go-Tests nur mit Standardbibliothek, generierter Code wird committet, OpenAPI ↔ Routen und Mock pro Operation. Zusätzlich verbindlich für dieses Teilpaket:

| Wert | Festlegung |
|---|---|
| Paket | `github.com/Bernotese/mc-panel/internal/servers`, Konstruktor `servers.NewHandler(servers.Deps) *Handler`, `Routes(api *rbac.Router)`, `Run(ctx)`, `ReconcileOnce(ctx)`, `Trigger()` |
| Abgleich | `servers.ReconcileInterval = 10 * time.Second` (Ticker) + `Trigger()` (Kanal mit Puffer 1, blockiert nie); höchstens ein agent-Aufruf pro Instanz und Durchlauf |
| Backoff | gleiche Aktion bei gleicher Revision und gleichem Soll-Zustand: 10 s, 20 s, 40 s … höchstens 5 min (Fake-Clock) |
| Status-Cache | `Snapshot` aus dem letzten `Instances()`-Aufruf; älter als 1 min (`statusMaxAge`) gilt als „agent nicht erreichbar“ → `state: unknown` |
| `docker` in `ServerSettings` | `ok` = agent antwortet und `docker == "ok"`; `unavailable` = agent antwortet, Docker nicht (`docker != "ok"` oder `agentapi.ErrDockerUnavailable`); `unknown` = agent nicht erreichbar |
| CurseForge-Key | `secretbox.Seal(key, aad = "instance_settings.curseforge_api_key")`; verlässt das panel nie, auch nicht im Audit-Log |
| Audit | `server.create` (Konfiguration ohne Secrets), `server.update` (`{name, revision, changes}`, Diff pro Feld bzw. `properties.<schlüssel>`), `server.delete` (`{name, configured}`), `server.power` (`{action, name}`), `server.settings_update` (Ziel `servers`; `base_domain: {old, new}`, `curseforge_api_key: "set" \| "removed"`) |
| HTTP-Abbildung agent-Fehler | `ErrDockerUnavailable` → 503 `docker_unavailable`; `ErrBusy` → 429 `rate_limited` (`Retry-After: 5`); `instancespec.ErrInvalid` → 400 `validation_failed`; fehlende Basisdomain → 409 `base_domain_missing`; `ErrUnavailable`/Timeout → 503 `agent_unavailable` |
| Katalog | Timeout 10 s pro Anfrage, Antwort ≤ 5 MiB, Cache 1 h pro Art (Fake-Clock), Fehler werden nicht gecacht, User-Agent `mc-panel/<version> (https://github.com/Bernotese/mc-panel)` auf jeder Anfrage |
| NeoForge-Katalog | `versions` = **Minecraft-Versionen** (aus stabilen NeoForge-Releases abgeleitet, dedupliziert, neueste zuerst, `java` per `instancespec.JavaFor`), `loaders = []`; das Formular setzt `version` = gewählte ID, `loader_version` bleibt `null` (itzg nimmt dann die neueste stabile NeoForge-Version zu dieser MC-Version), außer der Admin trägt unter „Erweitert“ eine NeoForge-Version ein. Hilfsfunktion `catalog.NeoForgeMC(neoforgeVersion) (mc string, ok bool)` |
| Frontend-Grenze | Aufgaben 11–16 ändern `web/src/api/errors.ts` (`KNOWN_CODES`), die `errors.*`-Texte und `ERROR_CODES` in `i18n.test.ts` **nicht** (gehört Aufgabe 17). `create-server-dialog.tsx` bleibt bestehen und kompiliert; `web/e2e/smoke.spec.ts` bleibt unverändert |

## Entscheidungen innerhalb dieses Teilplans

1. **Zustandsableitung schon in Aufgabe 13:** `state.go` (Tabelle aus V4) und der Status-Cache entstehen in Aufgabe 13, weil `GET`/`PATCH` sonst kein sinnvolles `status` (inkl. `restart_required`) liefern. Die Austauschbarkeit für Tests gibt es über den Fake-agent und `servers.RefreshStatus` (nur in `export_test.go`). Aufgabe 14 nutzt denselben Code für den Abgleich.
2. **Abgleich – Verfeinerung von §5.3:** Ein Container, der **nicht läuft** und eine ältere Revision trägt, wird immer mit `recreate: true` neu angelegt – auch bei `desired_state = running` (die Tabelle nennt das nur für `stopped`). Danach startet der nächste Durchlauf ihn (`created` → `power start`). So startet nie eine veraltete Konfiguration. Ein laufender Container mit alter Revision bleibt unangetastet (`restart_required`).
3. **„ensure, danach ggf. start“** verteilt sich auf zwei Durchläufe (ein agent-Aufruf pro Durchlauf); nach jedem Durchlauf mit erfolgreicher Aktion ruft der Abgleich selbst `Trigger()` auf, damit der Folgeschritt nicht 10 s wartet.
4. **`restart`** setzt `desired_state = running`. Ist der Container `missing`, ruft der Handler den agent nicht auf (der Abgleich legt ihn an und startet ihn). Ein fehlgeschlagener Neustart ändert nichts und wird nicht auditiert.
5. **`201` von `POST /servers`:** `status` kommt aus dem Status-Cache; ist der agent erreichbar, ist der neue Server dort noch unbekannt → `creating`; ohne agent `unknown`.
6. **`address`** ist `null` für Server ohne Instanz (Spec §8: „`null`-Felder für nicht eingerichtete Server“) und ohne Basisdomain.
7. **Übergangs-Dialog:** `create-server-dialog.tsx` legt bis Aufgabe 19 einen Paper-Server (`LATEST`, 4096 MiB, `eula_accepted: true`) an; der Beschreibungstext des Dialogs sagt, dass „Anlegen“ die Minecraft-EULA akzeptiert (keine neue Checkbox, damit Dashboard-Test und `smoke.spec.ts` unverändert bleiben).

## Review Focus (dieser Teilplan)

- **#3 Name vergeben / keine Basisdomain:** `TestCreateServerWithoutBaseDomain`, `TestCreateServerNameTakenLeavesNoHalfRow` (Aufgabe 13).
- **#4 agent fällt aus:** `TestReconcileAgentOutageAndRecovery`, `TestReconcileBacksOff`, `TestReconcileAbortsWhenAgentFailsMidPass` (Aufgabe 14).
- **#5 Upstream liefert Müll:** `TestCatalogRejectsGarbage` (Aufgabe 15).

## Dateistruktur

```
internal/servers/handler.go          (11–15) Deps, Handler, Routes, Trigger, Helfer, noAgent
internal/servers/perms.go            (11)    effektive Rechte (allPerms, userPerms, serverPerms)
internal/servers/powerlimit.go       (11)    verschoben aus internal/users
internal/servers/servers.go          (11,13) Liste, Details, Anlegen, Ändern, Löschen
internal/servers/power.go            (11,14) Power-Handler, restart, ensureRequest
internal/servers/optional.go         (12)    absent/null/Wert für PUT/PATCH
internal/servers/settings.go         (12,13) GET/PUT /servers/settings, loadSettings
internal/servers/config.go           (13)    gespeicherte Konfiguration, Validierung, Diff
internal/servers/status.go           (13)    Snapshot + Cache, refreshStatus
internal/servers/state.go            (13)    ServerStatus-Ableitung (V4)
internal/servers/view.go             (13)    Server-Schema (InstanceConfig, ServerProperties aufgelöst)
internal/servers/reconcile.go        (14)    Abgleichsschleife, Backoff
internal/servers/catalog_api.go      (15)    GET /servers/catalog/{type}
internal/servers/catalog/            (15)    Katalog-Client, Quellen, testdata/
cmd/panel/app.go, main.go            (11,12,16) Verdrahtung
api/openapi.yaml, web/src/api/schema.d.ts      (12–15) Vertrag
web/src/mocks/{db.ts,handlers/servers.ts,handlers.test.ts}  (12–15) MSW
```

---

### Task 11: Server-Handler nach `internal/servers` verschieben

> **Nachtrag aus dem Review von Phase A (verbindlich):** `instancespec.ValidName` lehnt seit Phase A einen Bindestrich am Ende ab (`^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$`, gültiges DNS-Label). Diese Aufgabe ist deshalb kein reiner Refactor: Sie übernimmt die Regel auch in `api/openapi.yaml` (`CreateServerRequest.name.pattern`), im MSW-Mock (`SERVER_NAME` in `web/src/mocks/handlers/servers.ts`) und in der Liste ungültiger Namen der verschobenen Server-Tests (zusätzlich `"x-"`), dann `make generate`.

Reines Refactoring ohne Verhaltensänderung: Liste, Anlegen, Details, Löschen, Power und der Power-Limiter ziehen aus `internal/users` in das neue Paket `internal/servers`; die Rechtevergabe (`/users/{id}/permissions`) bleibt in `internal/users`. Die lokale Namens-Regex wird durch `instancespec.ValidName` ersetzt.

**Files:**
- Create: `internal/servers/handler.go`, `internal/servers/perms.go`, `internal/servers/servers.go`, `internal/servers/power.go`
- Move: `internal/users/powerlimit.go` → `internal/servers/powerlimit.go`
- Delete: `internal/users/servers.go`, `internal/users/servers_test.go`
- Modify: `internal/users/handler.go`, `internal/users/routes_test.go`, `cmd/panel/app.go`
- Test: `internal/servers/helpers_test.go`, `internal/servers/servers_test.go`, `internal/servers/routes_test.go`

**Interfaces:**
- Consumes: `store.Queries` (`Rbac…`-Queries aus TP1), `rbac.Router`, `audit.Recorder`, `clock.Clock`, `instancespec.ValidName` (V2), `agentapi.PowerAction`/`ValidPowerAction` (V3)
- Produces:
```go
package servers

type Deps struct {
	DB    *sql.DB // from store.Open (BEGIN IMMEDIATE transactions)
	Clock clock.Clock
	Audit audit.Recorder
}

func NewHandler(d Deps) *Handler
func (h *Handler) Routes(api *rbac.Router)
```
`users.NewHandler(db, clock, rec, notifier)` behält seine Signatur, registriert aber keine `/servers`-Routen mehr.

- [ ] **Step 1: Tests ins neue Paket schreiben**

`internal/servers/helpers_test.go`:

```go
package servers_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// env is a real database plus the servers handler mounted on a real rbac.Router
// (with the DB-backed checker), so every request also exercises the route rule.
type env struct {
	t      *testing.T
	db     *sql.DB
	clk    *clock.Fake
	rec    *audit.MemoryRecorder
	h      *servers.Handler
	mux    http.Handler
	rootID int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{}}
	e.h = servers.NewHandler(servers.Deps{DB: db, Clock: e.clk, Audit: e.rec})
	mux := chi.NewRouter()
	e.h.Routes(rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk))
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

// admin returns the principal of the seeded admin "root" with a valid sudo window.
func (e *env) admin() principal.Principal {
	return principal.Principal{
		UserID: e.rootID, Username: "root", Role: principal.RoleAdmin,
		Stage: principal.StageFull, SudoUntil: t0.Add(5 * time.Minute),
	}
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull}
}

// do sends a request with client IP 203.0.113.7 and the given principal (nil = anonymous).
func (e *env) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	ctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if p != nil {
		ctx = principal.With(ctx, *p)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[httpx.ErrorBody](t, rec).Error
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// grant gives a user permissions on a server directly in the database.
func grant(t *testing.T, db *sql.DB, userID, serverID int64, perms ...string) {
	t.Helper()
	for _, p := range perms {
		mustExec(t, db, `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, userID, serverID, p)
	}
}
```

`internal/servers/routes_test.go`:

```go
package servers_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// wantRoutes are exactly the server operations of api/openapi.yaml with their rules.
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers":             rbac.Authenticated(),
	"POST /api/servers":            rbac.Admin().WithSudo(),
	"GET /api/servers/{id}":        rbac.Server(rbac.PermServerView, "id"),
	"DELETE /api/servers/{id}":     rbac.Admin().WithSudo(),
	"POST /api/servers/{id}/power": rbac.Server(rbac.PermServerPower, "id"),
}

func TestRouteContract(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		servers.NewHandler(servers.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
	})
	mux.Handle("/*", http.NotFoundHandler())

	got := rt.Routes()
	if len(got) != len(wantRoutes) {
		t.Errorf("registered %d routes, want %d", len(got), len(wantRoutes))
	}
	seen := map[string]bool{}
	for _, r := range got {
		key := r.Method + " " + r.Pattern
		seen[key] = true
		w, ok := wantRoutes[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if w.String() != r.Rule.String() {
			t.Errorf("%s: rule %s, want %s", key, r.Rule, w)
		}
	}
	for key := range wantRoutes {
		if !seen[key] {
			t.Errorf("missing route %s", key)
		}
	}
	if un := rbac.Uncovered(mux, got, func(_, pattern string) bool { return pattern == "/*" }); len(un) != 0 {
		t.Fatalf("routes without rbac rule: %v", un)
	}
}
```

`internal/servers/servers_test.go` (die bisherigen Tests aus `internal/users/servers_test.go`; Rechte werden direkt in der DB vergeben, weil `/users/{id}/permissions` nicht mehr in diesem Paket liegt):

```go
package servers_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type serverResp struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

var allPermStrings = []string{"server.view", "server.power", "server.console", "server.files", "server.plugins", "server.backups"}

func TestServerCRUDAsAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", map[string]string{"name": "survival"}, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	s := decode[serverResp](t, rec)
	if s.ID == 0 || s.Name != "survival" || !slices.Equal(s.Permissions, allPermStrings) {
		t.Fatalf("created = %+v", s)
	}
	if rec := e.do(http.MethodPost, "/servers", map[string]string{"name": "survival"}, &admin); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d", rec.Code)
	}
	for _, bad := range []string{"-bad", "a", "Upper", "has_underscore", "with space", strings.Repeat("a", 33)} {
		rec := e.do(http.MethodPost, "/servers", map[string]string{"name": bad}, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("name %q: %d %s", bad, rec.Code, rec.Body)
		}
	}
	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &admin))
	if len(list) != 1 || list[0].ID != s.ID || !slices.Equal(list[0].Permissions, allPermStrings) {
		t.Fatalf("list = %+v", list)
	}
	path := fmt.Sprintf("/servers/%d", s.ID)
	if rec := e.do(http.MethodGet, path, nil, &admin); rec.Code != http.StatusOK || decode[serverResp](t, rec).Name != "survival" {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, path, nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: %d", rec.Code)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionServerCreate, audit.ActionServerDelete}) {
		t.Fatalf("audit = %v", got)
	}
	if en := e.rec.Entries[0]; en.Target != fmt.Sprintf("server:%d", s.ID) || en.Details["name"] != "survival" {
		t.Fatalf("audit entry = %+v", en)
	}
}

// Spec §12.4: a restricted user with server.power on server A sees only A; B → 403.
func TestServerVisibilityForRestrictedUser(t *testing.T) {
	e := newEnv(t)
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := storetest.CreateServer(t, e.db, "alpha")
	b := storetest.CreateServer(t, e.db, "beta")
	grant(t, e.db, alice, a, "server.view", "server.power")
	ap := userP(alice, "alice")

	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &ap))
	want := []string{"server.view", "server.power"}
	if len(list) != 1 || list[0].ID != a || list[0].Name != "alpha" || !slices.Equal(list[0].Permissions, want) {
		t.Fatalf("list = %+v", list)
	}
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", a), nil, &ap)
	if rec.Code != http.StatusOK || !slices.Equal(decode[serverResp](t, rec).Permissions, want) {
		t.Fatalf("get A: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, fmt.Sprintf("/servers/%d", b), nil},
		{http.MethodPost, fmt.Sprintf("/servers/%d/power", b), map[string]string{"action": "start"}},
		{http.MethodPost, "/servers", map[string]string{"name": "gamma"}},
		{http.MethodDelete, fmt.Sprintf("/servers/%d", a), nil},
	} {
		if rec := e.do(tc.method, tc.path, tc.body, &ap); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", tc.method, tc.path, rec.Code)
		}
	}

	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "start"}, &ap)
	if rec.Code != http.StatusAccepted || strings.TrimSpace(rec.Body.String()) != `{"status":"accepted"}` {
		t.Fatalf("power A: %d %s", rec.Code, rec.Body)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if last.Action != audit.ActionServerPower || last.Target != fmt.Sprintf("server:%d", a) ||
		last.Details["action"] != "start" || last.Result != audit.ResultSuccess {
		t.Fatalf("power audit = %+v", last)
	}
	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "explode"}, &ap)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("invalid action: %d %s", rec.Code, rec.Body)
	}
}

func TestServerListWithoutPermissionsIsEmptyArray(t *testing.T) {
	e := newEnv(t)
	storetest.CreateServer(t, e.db, "alpha")
	bob := userP(storetest.CreateUser(t, e.db, "bob", "user"), "bob")
	rec := e.do(http.MethodGet, "/servers", nil, &bob)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("list: %d %q", rec.Code, rec.Body)
	}
}

func TestServerPowerUnknownServerAsAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	if rec := e.do(http.MethodPost, "/servers/9999/power", map[string]string{"action": "stop"}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("power: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/servers/9999", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("get: %d", rec.Code)
	}
}

// If the row vanishes between the lookup and the delete (concurrent delete), the handler must
// answer 404 and not audit a deletion that did not happen. A BEFORE DELETE trigger with
// RAISE(IGNORE) makes the DELETE affect 0 rows deterministically.
func TestDeleteServerZeroRowsIs404(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := storetest.CreateServer(t, e.db, "alpha")
	mustExec(t, e.db, `CREATE TRIGGER servers_ignore_delete BEFORE DELETE ON servers BEGIN SELECT RAISE(IGNORE); END`)
	rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil, &admin)
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if got := e.rec.Actions(); slices.Contains(got, audit.ActionServerDelete) {
		t.Fatalf("unexpected audit entries: %v", got)
	}
}

// Power actions are limited to 10 per minute per user, so a user cannot flood the audit chain.
func TestServerPowerRateLimitedPerUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	a := storetest.CreateServer(t, e.db, "alpha")
	path := fmt.Sprintf("/servers/%d/power", a)
	body := map[string]string{"action": "restart"}
	for i := 0; i < 10; i++ {
		if rec := e.do(http.MethodPost, path, body, &admin); rec.Code != http.StatusAccepted {
			t.Fatalf("request %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := e.do(http.MethodPost, path, body, &admin)
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th request: %d %s retry=%q", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
	if n := len(e.rec.Entries); n != 10 {
		t.Fatalf("audit entries = %d, want 10 (a limited request is not audited)", n)
	}
	// another user has its own budget
	other := principal.Principal{UserID: storetest.CreateUser(t, e.db, "root2", "admin"), Username: "root2",
		Role: principal.RoleAdmin, Stage: principal.StageFull}
	if rec := e.do(http.MethodPost, path, body, &other); rec.Code != http.StatusAccepted {
		t.Fatalf("other user: %d", rec.Code)
	}
	e.clk.Advance(6 * time.Second)
	if rec := e.do(http.MethodPost, path, body, &admin); rec.Code != http.StatusAccepted {
		t.Fatalf("after refill: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./internal/servers/ -count=1`
Expected: FAIL beim Kompilieren (`package github.com/Bernotese/mc-panel/internal/servers` hat keine Go-Dateien außer Tests; `servers.NewHandler` undefiniert).

- [ ] **Step 3: Power-Limiter verschieben**

```bash
mkdir -p internal/servers
git mv internal/users/powerlimit.go internal/servers/powerlimit.go
sed -i.bak 's/^package users$/package servers/' internal/servers/powerlimit.go && rm internal/servers/powerlimit.go.bak
git rm -q internal/users/servers.go internal/users/servers_test.go
```

- [ ] **Step 4: Paket `internal/servers` schreiben**

`internal/servers/handler.go`:

```go
// Package servers implements the server API (list, create, details, delete, power control)
// and, from sub-project 2 on, the Minecraft instances behind it.
package servers

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Deps are the dependencies of the server handlers.
type Deps struct {
	DB    *sql.DB // from store.Open (BEGIN IMMEDIATE transactions)
	Clock clock.Clock
	Audit audit.Recorder
}

type Handler struct {
	db    *sql.DB
	q     *store.Queries
	clock clock.Clock
	rec   audit.Recorder
	power *powerLimiter
}

func NewHandler(d Deps) *Handler {
	return &Handler{db: d.DB, q: store.New(d.DB), clock: d.Clock, rec: d.Audit, power: newPowerLimiter()}
}

// Routes registers all server routes (paths relative to /api).
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/servers", rbac.Authenticated(), h.handleListServers)
	api.Post("/servers", rbac.Admin().WithSudo(), h.handleCreateServer)
	api.Get("/servers/{id}", rbac.Server(rbac.PermServerView, "id"), h.handleGetServer)
	api.Delete("/servers/{id}", rbac.Admin().WithSudo(), h.handleDeleteServer)
	api.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), h.handleServerPower)
}

// pathID parses the {id} URL parameter; false for non-numeric or non-positive values.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

// record writes an audit entry; actor and IP come from the request context. It must only be
// called after any write transaction of the handler has finished (SQLRecorder opens its own).
// The action already happened, so a failing audit write is logged, not returned to the client.
func (h *Handler) record(ctx context.Context, action, target string, result audit.Result, details map[string]any) {
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: target, Result: result, Details: details}); err != nil {
		slog.ErrorContext(ctx, "audit record failed", "action", action, "target", target, "err", err)
	}
}

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
}

func writeConflict(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, msg)
}
```

`internal/servers/perms.go`:

```go
package servers

import (
	"context"
	"slices"

	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

func allPerms() []rbac.Permission { return slices.Clone(rbac.AllPermissions) }

// orderedPerms returns the permissions in set in rbac.AllPermissions order (never nil).
func orderedPerms(set map[rbac.Permission]bool) []rbac.Permission {
	out := make([]rbac.Permission, 0, len(set))
	for _, p := range rbac.AllPermissions {
		if set[p] {
			out = append(out, p)
		}
	}
	return out
}

// userPerms returns the stored permissions of a (non-admin) user per server id.
func userPerms(ctx context.Context, q *store.Queries, userID int64) (map[int64][]rbac.Permission, error) {
	rows, err := q.RbacListUserPermissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	sets := map[int64]map[rbac.Permission]bool{}
	for _, row := range rows {
		if sets[row.ServerID] == nil {
			sets[row.ServerID] = map[rbac.Permission]bool{}
		}
		sets[row.ServerID][rbac.Permission(row.Permission)] = true
	}
	out := make(map[int64][]rbac.Permission, len(sets))
	for id, set := range sets {
		out[id] = orderedPerms(set)
	}
	return out, nil
}

// serverPerms returns the caller's effective permissions on one server (admins get all).
func serverPerms(ctx context.Context, q *store.Queries, p principal.Principal, serverID int64) ([]rbac.Permission, error) {
	if p.IsAdmin() {
		return allPerms(), nil
	}
	rows, err := q.RbacListUserServerPermissions(ctx, store.RbacListUserServerPermissionsParams{UserID: p.UserID, ServerID: serverID})
	if err != nil {
		return nil, err
	}
	set := make(map[rbac.Permission]bool, len(rows))
	for _, row := range rows {
		set[rbac.Permission(row)] = true
	}
	return orderedPerms(set), nil
}
```

`internal/servers/servers.go`:

```go
package servers

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// serverJSON is the OpenAPI schema Server; permissions are the caller's effective permissions.
type serverJSON struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Permissions []rbac.Permission `json:"permissions"`
}

func (h *Handler) handleListServers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := principal.From(ctx)
	out := []serverJSON{}
	if p.IsAdmin() {
		list, err := h.q.RbacListServers(ctx)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		for _, s := range list {
			out = append(out, serverJSON{ID: s.ID, Name: s.Name, Permissions: allPerms()})
		}
		httpx.WriteJSON(w, http.StatusOK, out)
		return
	}
	list, err := h.q.RbacListViewableServers(ctx, p.UserID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	perms, err := userPerms(ctx, h.q, p.UserID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	for _, s := range list {
		out = append(out, serverJSON{ID: s.ID, Name: s.Name, Permissions: perms[s.ID]})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// loadServer resolves {id}; it writes 404 itself and returns false if the server is missing.
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (store.Server, bool) {
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r)
		return store.Server{}, false
	}
	s, err := h.q.RbacGetServer(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r)
		return store.Server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Server{}, false
	}
	return s, true
}

func (h *Handler) handleGetServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	p, _ := principal.From(ctx)
	perms, err := serverPerms(ctx, h.q, p, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, serverJSON{ID: s.ID, Name: s.Name, Permissions: perms})
}

type createServerRequest struct {
	Name string `json:"name"`
}

func (h *Handler) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createServerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if !instancespec.ValidName(req.Name) {
		writeValidation(w, r, "name must match ^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$")
		return
	}
	if _, err := h.q.RbacGetServerByName(ctx, req.Name); err == nil {
		writeConflict(w, r, "server name already taken")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return
	}
	s, err := h.q.RbacCreateServer(ctx, store.RbacCreateServerParams{Name: req.Name, CreatedAt: h.clock.Now().UnixMilli()})
	if store.IsUniqueViolation(err) {
		writeConflict(w, r, "server name already taken")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerCreate, serverTarget(s.ID), audit.ResultSuccess, map[string]any{"name": s.Name})
	httpx.WriteJSON(w, http.StatusCreated, serverJSON{ID: s.ID, Name: s.Name, Permissions: allPerms()})
}

func (h *Handler) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	n, err := h.q.RbacDeleteServer(ctx, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 { // deleted concurrently between lookup and delete
		writeNotFound(w, r)
		return
	}
	h.record(ctx, audit.ActionServerDelete, serverTarget(s.ID), audit.ResultSuccess, map[string]any{"name": s.Name})
	w.WriteHeader(http.StatusNoContent)
}
```

`internal/servers/power.go`:

```go
package servers

import (
	"math"
	"net/http"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

type powerRequest struct {
	Action agentapi.PowerAction `json:"action"`
}

// handleServerPower is still the sub-project 1 stub: it validates, audits and accepts.
func (h *Handler) handleServerPower(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req powerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if !agentapi.ValidPowerAction(req.Action) {
		writeValidation(w, r, "action must be start, stop or restart")
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	if d := h.power.take(p.UserID, h.clock.Now()); d > 0 {
		secs := int(math.Ceil(d.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many power actions, try again later")
		return
	}
	h.record(ctx, audit.ActionServerPower, serverTarget(s.ID), audit.ResultSuccess,
		map[string]any{"action": string(req.Action), "name": s.Name})
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}
```

- [ ] **Step 5: `internal/users` entschlacken**

`internal/users/handler.go`:

- Paketkommentar ersetzen durch:
```go
// Package users implements user management, per-server permissions and the
// `panel admin create` CLI command (stream D). The server API lives in internal/servers.
```
- Im `Handler`-Struct das Feld `power    *powerLimiter` entfernen; `NewHandler` wird zu
```go
	return &Handler{db: db, q: store.New(db), clock: c, rec: rec, notifier: n}
```
- `Routes` wird zu
```go
// Routes registers all user and permission routes (paths relative to /api).
func (h *Handler) Routes(api *rbac.Router) {
	h.userRoutes(api)
	h.manageRoutes(api)
	h.permissionRoutes(api)
}
```

`internal/users/routes_test.go`: in `want` die fünf Einträge `GET /api/servers`, `POST /api/servers`, `GET /api/servers/{id}`, `DELETE /api/servers/{id}` und `POST /api/servers/{id}/power` löschen und den Kommentar darüber ersetzen durch:
```go
	// Exactly the rules from the OpenAPI table in the Phase-0 plan (rows marked "D", without the
	// server routes, which moved to internal/servers).
```
(`orderedPerms`, `loadGrants`, `pathID` und `writeConflict` bleiben in `users`, sie werden dort weiter benutzt.)

- [ ] **Step 6: In `cmd/panel/app.go` verdrahten**

Import `"github.com/Bernotese/mc-panel/internal/servers"` ergänzen (alphabetisch nach `secretbox`), nach `usersH := …`:
```go
	serversH := servers.NewHandler(servers.Deps{DB: db, Clock: d.Clock, Audit: auditRec})
```
und im `/api`-Block nach `usersH.Routes(api)`:
```go
		serversH.Routes(api)
```

- [ ] **Step 7: Tests laufen lassen (grün)**

Run: `gofmt -l internal/ cmd/ && go test ./internal/servers/ ./internal/users/ ./cmd/panel/ -count=1`
Expected: keine Ausgabe von `gofmt -l`; PASS (inkl. `TestRoutesMatchOpenAPI`, `TestRouteCoverage` mit unverändert 57 Routen).

- [ ] **Step 8: Gesamtprüfung**

Run: `make test && make lint`
Expected: PASS, `0 issues`, eslint/tsc ohne Befunde (Frontend unverändert).

- [ ] **Step 9: Commit**

```bash
git add internal/servers internal/users cmd/panel/app.go
git commit -m "refactor(servers): move server handlers into internal/servers

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 12: Server-Einstellungen und neue Fehlercodes

`GET/PUT /servers/settings` (Basisdomain, CurseForge-Key, Docker-Status, unbekannte Container), drei neue Fehlercodes, `ActionServerSettingsUpdate`, der `Trigger()`-Haken (ohne Leser bis Aufgabe 14) und der Fake-agent für alle folgenden Handler-Tests.

**Files:**
- Modify: `internal/httpx/errors.go`, `internal/audit/types.go`, `internal/servers/handler.go` (komplett ersetzen), `internal/servers/routes_test.go`, `internal/servers/helpers_test.go` (komplett ersetzen), `cmd/panel/app.go`, `cmd/panel/integration_test.go`, `api/openapi.yaml`, `web/src/api/types.ts`, `web/src/mocks/db.ts`, `web/src/mocks/handlers/servers.ts`, `web/src/mocks/handlers.test.ts`
- Create: `internal/servers/optional.go`, `internal/servers/settings.go`
- Generated: `web/src/api/schema.d.ts`
- Test: `internal/servers/optional_test.go`, `internal/servers/settings_test.go`

**Interfaces:**
- Consumes: V1 (`ServersGetSettings`, `ServersUpdateSettings`, `ServersBumpAllRevisions`, `ServersListInstances`, `ServersCreateInstance`), V2 (`ValidBaseDomain`, `Spec.Validate`), V3 (`agentapi.InstancesClient`, `ErrUnavailable`, `ErrDockerUnavailable`, `DockerOK`/`DockerUnavailable`), `secretbox.Box`
- Produces:
```go
// internal/httpx/errors.go
CodeDockerUnavailable  = "docker_unavailable"
CodeBaseDomainMissing  = "base_domain_missing"
CodeCatalogUnavailable = "catalog_unavailable"
// internal/audit/types.go
ActionServerSettingsUpdate = "server.settings_update"
// internal/servers
type Deps struct {
	DB        *sql.DB
	Clock     clock.Clock
	Audit     audit.Recorder
	Box       *secretbox.Box
	Instances agentapi.InstancesClient // nil = no agent
}
func (h *Handler) Trigger()
```
OpenAPI: `GET /servers/settings` (`getServerSettings`, Admin), `PUT /servers/settings` (`updateServerSettings`, Admin + Sudo), Schemas `UnknownContainer`, `ServerSettings`, `UpdateServerSettingsRequest` (wörtlich V4), Error-Enum + 3 Codes. MSW: `db.serverSettings` (V5), zwei Operationen (vor `getServer` registriert). Operationen: 59.

- [ ] **Step 1: Failing tests schreiben**

`internal/servers/helpers_test.go` komplett ersetzen (neu: `secretbox`, Fake-agent, `createInstance`, `instanceRow`):

```go
package servers_test

import (
	"bytes"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// env is a real database plus the servers handler mounted on a real rbac.Router
// (with the DB-backed checker), so every request also exercises the route rule.
type env struct {
	t      *testing.T
	db     *sql.DB
	clk    *clock.Fake
	rec    *audit.MemoryRecorder
	box    *secretbox.Box
	agent  *fakeAgent
	h      *servers.Handler
	mux    http.Handler
	rootID int64
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := storetest.Open(t)
	box, err := secretbox.New(bytes.Repeat([]byte{7}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{}, box: box, agent: newFakeAgent()}
	e.h = servers.NewHandler(servers.Deps{DB: db, Clock: e.clk, Audit: e.rec, Box: box, Instances: e.agent})
	mux := chi.NewRouter()
	e.h.Routes(rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk))
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

// admin returns the principal of the seeded admin "root" with a valid sudo window.
func (e *env) admin() principal.Principal {
	return principal.Principal{
		UserID: e.rootID, Username: "root", Role: principal.RoleAdmin,
		Stage: principal.StageFull, SudoUntil: t0.Add(5 * time.Minute),
	}
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull}
}

// do sends a request with client IP 203.0.113.7 and the given principal (nil = anonymous).
func (e *env) do(method, path string, body any, p *principal.Principal) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	ctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if p != nil {
		ctx = principal.With(ctx, *p)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[httpx.ErrorBody](t, rec).Error
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// grant gives a user permissions on a server directly in the database.
func grant(t *testing.T, db *sql.DB, userID, serverID int64, perms ...string) {
	t.Helper()
	for _, p := range perms {
		mustExec(t, db, `INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, ?)`, userID, serverID, p)
	}
}

// createInstance inserts a server with a paper instance row (revision 1, desired running).
func createInstance(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	id := storetest.CreateServer(t, db, name)
	if _, err := store.New(db).ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
		ServerID: id, Type: "paper", Version: "1.21.11", Java: "java21", MemoryMib: 4096,
		Properties: `{}`, CreatedAt: t0.UnixMilli(), UpdatedAt: t0.UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func instanceRow(t *testing.T, db *sql.DB, id int64) store.Instance {
	t.Helper()
	in, err := store.New(db).ServersGetInstance(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// fakeAgent is an in-memory agentapi.InstancesClient. Container states only change when a
// test sets them; calls are recorded in order.
type fakeAgent struct {
	mu        sync.Mutex
	err       error // returned by every call while set
	actionErr error // returned by Ensure, Power and Remove only
	docker    string
	inst      map[int64]agentapi.InstanceStatus
	calls     []string
	ensures   []agentapi.EnsureRequest
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{docker: agentapi.DockerOK, inst: map[int64]agentapi.InstanceStatus{}}
}

func (f *fakeAgent) setErr(err error)   { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeAgent) setDocker(s string) { f.mu.Lock(); f.docker = s; f.mu.Unlock() }

func (f *fakeAgent) set(st agentapi.InstanceStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inst[st.ID] = st
}

func (f *fakeAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agentapi.InstancesResponse{}, f.err
	}
	list := make([]agentapi.InstanceStatus, 0, len(f.inst))
	for _, st := range f.inst {
		list = append(list, st)
	}
	slices.SortFunc(list, func(a, b agentapi.InstanceStatus) int { return cmp.Compare(a.ID, b.ID) })
	return agentapi.InstancesResponse{Docker: f.docker, Instances: list}, nil
}

func (f *fakeAgent) record(call string) error {
	if f.err != nil {
		return f.err
	}
	if f.actionErr != nil {
		return f.actionErr
	}
	f.calls = append(f.calls, call)
	return nil
}

func (f *fakeAgent) Ensure(_ context.Context, req agentapi.EnsureRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := req.Validate(); err != nil {
		return err
	}
	if err := f.record(fmt.Sprintf("ensure %d r%d recreate=%t", req.ID, req.Revision, req.Recreate)); err != nil {
		return err
	}
	f.ensures = append(f.ensures, req)
	return nil
}

func (f *fakeAgent) Power(_ context.Context, id int64, action agentapi.PowerAction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.record(fmt.Sprintf("power %d %s", id, action))
}

func (f *fakeAgent) Remove(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.record(fmt.Sprintf("remove %d", id))
}
```

`internal/servers/optional_test.go` (paketintern, weil `optional` unexportiert ist):

```go
package servers

import (
	"encoding/json"
	"testing"
)

func TestOptionalDistinguishesAbsentNullAndValue(t *testing.T) {
	type body struct {
		A optional[string] `json:"a"`
		B optional[string] `json:"b"`
		C optional[string] `json:"c"`
	}
	var b body
	if err := json.Unmarshal([]byte(`{"b":null,"c":"x"}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.A.Set || b.A.Value != nil {
		t.Errorf("absent: %+v", b.A)
	}
	if !b.B.Set || b.B.Value != nil {
		t.Errorf("null: %+v", b.B)
	}
	if !b.C.Set || b.C.Value == nil || *b.C.Value != "x" {
		t.Errorf("value: %+v", b.C)
	}
}

func TestOptionalRejectsUnknownNestedFields(t *testing.T) {
	type inner struct {
		X int `json:"x"`
	}
	var o optional[inner]
	if err := json.Unmarshal([]byte(`{"x":1,"y":2}`), &o); err == nil {
		t.Fatal("unknown nested field accepted")
	}
	if err := json.Unmarshal([]byte(`{"x":"1"}`), &o); err == nil {
		t.Fatal("wrong type accepted")
	}
}
```

`internal/servers/settings_test.go`:

```go
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
```

`internal/servers/routes_test.go`: in `wantRoutes` nach `"POST /api/servers"` ergänzen (danach `gofmt -w`):
```go
	"GET /api/servers/settings":    rbac.Admin(),
	"PUT /api/servers/settings":    rbac.Admin().WithSudo(),
```

- [ ] **Step 2: Tests laufen lassen (rot)**

Run: `go test ./internal/servers/ -count=1`
Expected: FAIL beim Kompilieren (`unknown field Box in struct literal of type servers.Deps`, `undefined: optional`).

- [ ] **Step 3: Fehlercodes und Audit-Aktion**

`internal/httpx/errors.go`, im `const`-Block nach `CodeAgentUnavailable`:
```go
	CodeDockerUnavailable  = "docker_unavailable"
	CodeBaseDomainMissing  = "base_domain_missing"
	CodeCatalogUnavailable = "catalog_unavailable"
```
`internal/audit/types.go`, nach `ActionServerPower`:
```go
	ActionServerSettingsUpdate = "server.settings_update"
```
Danach `gofmt -w internal/audit/types.go internal/httpx/errors.go` (richtet den ganzen Block neu aus).

- [ ] **Step 4: Implementieren**

`internal/servers/handler.go` komplett ersetzen:

```go
// Package servers implements the server API (list, create, details, delete, power control)
// and, from sub-project 2 on, the Minecraft instances behind it.
package servers

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Deps are the dependencies of the server handlers.
type Deps struct {
	DB    *sql.DB // from store.Open (BEGIN IMMEDIATE transactions)
	Clock clock.Clock
	Audit audit.Recorder
	Box   *secretbox.Box // seals the CurseForge API key at rest
	// Instances is the agent's instance API; nil means "no agent" (every call fails with
	// agentapi.ErrUnavailable).
	Instances agentapi.InstancesClient
}

type Handler struct {
	db    *sql.DB
	q     *store.Queries
	clock clock.Clock
	rec   audit.Recorder
	box   *secretbox.Box
	inst  agentapi.InstancesClient
	power *powerLimiter
	kick  chan struct{} // Trigger -> reconcile loop (buffer 1, never blocks)
}

func NewHandler(d Deps) *Handler {
	inst := d.Instances
	if inst == nil {
		inst = noAgent{}
	}
	return &Handler{
		db: d.DB, q: store.New(d.DB), clock: d.Clock, rec: d.Audit, box: d.Box, inst: inst,
		power: newPowerLimiter(), kick: make(chan struct{}, 1),
	}
}

// Trigger asks the reconcile loop for an immediate pass. It never blocks; several triggers
// before the next pass collapse into one.
func (h *Handler) Trigger() {
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

// Routes registers all server routes (paths relative to /api).
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/servers", rbac.Authenticated(), h.handleListServers)
	api.Post("/servers", rbac.Admin().WithSudo(), h.handleCreateServer)
	api.Get("/servers/settings", rbac.Admin(), h.handleGetSettings)
	api.Put("/servers/settings", rbac.Admin().WithSudo(), h.handleUpdateSettings)
	api.Get("/servers/{id}", rbac.Server(rbac.PermServerView, "id"), h.handleGetServer)
	api.Delete("/servers/{id}", rbac.Admin().WithSudo(), h.handleDeleteServer)
	api.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), h.handleServerPower)
}

// pathID parses the {id} URL parameter; false for non-numeric or non-positive values.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

// record writes an audit entry; actor and IP come from the request context. It must only be
// called after any write transaction of the handler has finished (SQLRecorder opens its own).
// The action already happened, so a failing audit write is logged, not returned to the client.
func (h *Handler) record(ctx context.Context, action, target string, result audit.Result, details map[string]any) {
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: target, Result: result, Details: details}); err != nil {
		slog.ErrorContext(ctx, "audit record failed", "action", action, "target", target, "err", err)
	}
}

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
}

func writeConflict(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, msg)
}

// inTx runs fn in a write transaction (BEGIN IMMEDIATE, see store.Open). Never call h.record
// inside fn: the SQL recorder opens its own transaction.
func (h *Handler) inTx(ctx context.Context, fn func(q *store.Queries) error) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(h.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// noAgent stands in for a missing agent client: every call fails as if the agent were down.
type noAgent struct{}

func (noAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	return agentapi.InstancesResponse{}, agentapi.ErrUnavailable
}
func (noAgent) Ensure(context.Context, agentapi.EnsureRequest) error { return agentapi.ErrUnavailable }
func (noAgent) Power(context.Context, int64, agentapi.PowerAction) error {
	return agentapi.ErrUnavailable
}
func (noAgent) Remove(context.Context, int64) error { return agentapi.ErrUnavailable }
```

`internal/servers/optional.go`:

```go
package servers

import (
	"bytes"
	"encoding/json"
)

// optional is a JSON field that distinguishes "absent" (Set == false), "null" (Set, Value ==
// nil) and a value (Set, Value != nil). Unknown fields inside the value are rejected, like
// httpx.DecodeJSON does for the outer object.
type optional[T any] struct {
	Set   bool
	Value *T
}

func (o *optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	o.Value = nil
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var v T
	if err := dec.Decode(&v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}
```

`internal/servers/settings.go`:

```go
package servers

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

// curseForgeKeyAAD binds the sealed CurseForge API key to its column.
const curseForgeKeyAAD = "instance_settings.curseforge_api_key"

// settingsTarget is the audit target of server settings changes.
const settingsTarget = "servers"

// Docker status values of the ServerSettings schema.
const (
	dockerOK          = "ok"
	dockerUnavailable = "unavailable"
	dockerUnknown     = "unknown" // the agent did not answer
)

type unknownContainerJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// settingsJSON is the OpenAPI schema ServerSettings. The CurseForge key itself never leaves
// the panel; clients only learn whether one is set.
type settingsJSON struct {
	BaseDomain          *string                `json:"base_domain"`
	CurseForgeAPIKeySet bool                   `json:"curseforge_api_key_set"`
	Docker              string                 `json:"docker"`
	UnknownContainers   []unknownContainerJSON `json:"unknown_containers"`
}

// updateSettingsRequest is the OpenAPI schema UpdateServerSettingsRequest: absent fields stay,
// null removes the value.
type updateSettingsRequest struct {
	BaseDomain       optional[string] `json:"base_domain"`
	CurseForgeAPIKey optional[string] `json:"curseforge_api_key"`
}

func nullString(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func toNullString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

// strOrNil turns an optional string into a JSON-friendly audit value.
func strOrNil(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

// validCurseForgeKey checks a key with exactly the rule the agent applies (instancespec), by
// validating a minimal CurseForge spec that carries it.
func validCurseForgeKey(key string) error {
	pack := "x"
	s := instancespec.Spec{
		Type: instancespec.TypeCurseForge, Modpack: &pack, Java: instancespec.Java21,
		MemoryMiB: instancespec.MinMemoryMiB, CurseForgeAPIKey: &key,
	}
	return s.Validate()
}

func (h *Handler) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	body, err := h.settingsBody(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// settingsBody builds the ServerSettings response. Docker status and unknown containers come
// from a live agent call; an unreachable agent yields docker "unknown" and no containers.
func (h *Handler) settingsBody(ctx context.Context) (settingsJSON, error) {
	row, err := h.q.ServersGetSettings(ctx)
	if err != nil {
		return settingsJSON{}, err
	}
	rows, err := h.q.ServersListInstances(ctx)
	if err != nil {
		return settingsJSON{}, err
	}
	known := make(map[int64]bool, len(rows))
	for _, in := range rows {
		known[in.ServerID] = true
	}
	out := settingsJSON{
		BaseDomain:          nullString(row.BaseDomain),
		CurseForgeAPIKeySet: len(row.CurseforgeApiKey) > 0,
		Docker:              dockerUnknown,
		UnknownContainers:   []unknownContainerJSON{},
	}
	resp, err := h.inst.Instances(ctx)
	switch {
	case err == nil && resp.Docker == agentapi.DockerOK:
		out.Docker = dockerOK
	case err == nil || errors.Is(err, agentapi.ErrDockerUnavailable):
		out.Docker = dockerUnavailable
	}
	if err == nil {
		for _, st := range resp.Instances {
			if !known[st.ID] {
				out.UnknownContainers = append(out.UnknownContainers, unknownContainerJSON{ID: st.ID, Name: st.Name})
			}
		}
		slices.SortFunc(out.UnknownContainers, func(a, b unknownContainerJSON) int { return cmp.Compare(a.ID, b.ID) })
	}
	return out, nil
}

func (h *Handler) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req updateSettingsRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	var newBase *string
	if req.BaseDomain.Value != nil {
		d := strings.ToLower(*req.BaseDomain.Value)
		if err := instancespec.ValidBaseDomain(d); err != nil {
			writeValidation(w, r, err.Error())
			return
		}
		newBase = &d
	}
	var sealed []byte // nil removes the key
	if req.CurseForgeAPIKey.Value != nil {
		key := *req.CurseForgeAPIKey.Value
		if err := validCurseForgeKey(key); err != nil {
			writeValidation(w, r, "curseforge_api_key must match ^[A-Za-z0-9$./_-]{1,128}$")
			return
		}
		var err error
		if sealed, err = h.box.Seal([]byte(key), []byte(curseForgeKeyAAD)); err != nil {
			httpx.Internal(w, r, err)
			return
		}
	}
	var (
		oldBase     *string
		baseChanged bool
	)
	now := h.clock.Now().UnixMilli()
	err := h.inTx(ctx, func(q *store.Queries) error {
		cur, err := q.ServersGetSettings(ctx)
		if err != nil {
			return err
		}
		oldBase = nullString(cur.BaseDomain)
		params := store.ServersUpdateSettingsParams{BaseDomain: cur.BaseDomain, CurseforgeApiKey: cur.CurseforgeApiKey, UpdatedAt: now}
		if req.BaseDomain.Set {
			params.BaseDomain = toNullString(newBase)
		}
		if req.CurseForgeAPIKey.Set {
			params.CurseforgeApiKey = sealed
		}
		baseChanged = params.BaseDomain != cur.BaseDomain
		if err := q.ServersUpdateSettings(ctx, params); err != nil {
			return err
		}
		if baseChanged {
			// The FQDN is part of every container (label, route): all instances need a new revision.
			return q.ServersBumpAllRevisions(ctx, now)
		}
		return nil
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	details := map[string]any{}
	if baseChanged {
		details["base_domain"] = map[string]any{"old": strOrNil(oldBase), "new": strOrNil(newBase)}
	}
	if req.CurseForgeAPIKey.Set {
		details["curseforge_api_key"] = "set"
		if req.CurseForgeAPIKey.Value == nil {
			details["curseforge_api_key"] = "removed"
		}
	}
	if len(details) > 0 {
		h.record(ctx, audit.ActionServerSettingsUpdate, settingsTarget, audit.ResultSuccess, details)
	}
	if baseChanged {
		h.Trigger()
	}
	body, err := h.settingsBody(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}
```

`cmd/panel/app.go`: die `serversH`-Zeile wird zu
```go
	serversH := servers.NewHandler(servers.Deps{DB: db, Clock: d.Clock, Audit: auditRec, Box: box})
```
(`Instances` bleibt bis Aufgabe 16 `nil` → `noAgent`.)

- [ ] **Step 5: Go-Tests laufen lassen (grün)**

Run: `gofmt -l internal/ && go test ./internal/servers/ -count=1`
Expected: PASS. `go test ./cmd/panel/` schlägt jetzt erwartungsgemäß fehl (`route not in OpenAPI: GET /api/servers/settings`, Fehlercodes ≠ Enum) – Step 6 behebt das.

- [ ] **Step 6: OpenAPI-Vertrag**

`api/openapi.yaml`:

1. Im Schema `Error` die Enum-Zeile (muss einzeilig bleiben, `TestErrorCodesMatchOpenAPI` liest sie per Regex) zu
```yaml
        error: { type: string, enum: [bad_request, validation_failed, unauthorized, invalid_credentials, invalid_code, forbidden, sudo_required, setup_2fa_required, csrf_invalid, not_found, conflict, payload_too_large, rate_limited, jailed, ip_blocked, agent_unavailable, docker_unavailable, base_domain_missing, catalog_unavailable, internal] }
```
2. Unter `paths`, direkt vor `  /servers/{id}:`:
```yaml
  /servers/settings:
    get:
      operationId: getServerSettings
      tags: [servers]
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/ServerSettings' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
    put:
      operationId: updateServerSettings
      tags: [servers]
      parameters:
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/UpdateServerSettingsRequest' } } }
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/ServerSettings' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
```
3. Unter `components.schemas`, direkt nach `PowerResponse`:
```yaml
    UnknownContainer:
      type: object
      required: [id, name]
      properties:
        id: { type: integer, format: int64 }
        name: { type: string }
    ServerSettings:
      type: object
      required: [base_domain, curseforge_api_key_set, docker, unknown_containers]
      properties:
        base_domain: { type: [string, 'null'] }
        curseforge_api_key_set: { type: boolean }
        docker: { type: string, enum: [ok, unavailable, unknown] }
        unknown_containers: { type: array, items: { $ref: '#/components/schemas/UnknownContainer' } }
    UpdateServerSettingsRequest:
      type: object
      additionalProperties: false
      description: Omitted fields stay unchanged; null removes the value.
      properties:
        base_domain: { type: [string, 'null'] }
        curseforge_api_key: { type: [string, 'null'] }
```

`cmd/panel/integration_test.go`, `TestRouteCoverage`: `57` → `59` (Bedingung und Meldung).

Run: `make generate`
Expected: `web/src/api/schema.d.ts` enthält `"/servers/settings"`, `ServerSettings`, `UpdateServerSettingsRequest`, `UnknownContainer` und die drei neuen Codes im `Error`-Enum; `sqlc` ändert nichts.

- [ ] **Step 7: MSW-Mock und Typen**

`web/src/api/types.ts`, nach `export type PowerAction = …`:
```ts
export type ServerSettings = Schemas['ServerSettings']
export type UpdateServerSettingsRequest = Schemas['UpdateServerSettingsRequest']
export type UnknownContainer = Schemas['UnknownContainer']
```

`web/src/mocks/db.ts`: nach `interface MockServer` einfügen
```ts
/** ServerSettings without unknown_containers (always empty in the mock). */
export interface MockServerSettings {
  base_domain: string | null
  curseforge_api_key_set: boolean
  docker: 'ok' | 'unavailable' | 'unknown'
}
```
in `MockDb` nach `servers: MockServer[]` das Feld `serverSettings: MockServerSettings` und im Seed nach dem `servers`-Array
```ts
    serverSettings: { base_domain: 'mc.example.com', curseforge_api_key_set: false, docker: 'ok' },
```

`web/src/mocks/handlers/servers.ts`: Import-Zeile der Typen zu
```ts
import type { PowerAction, Server, ServerSettings, UpdateServerSettingsRequest } from '@/api/types'
```
nach `const ACTIONS …` einfügen
```ts
const DNS_LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/
const CURSEFORGE_KEY = /^[A-Za-z0-9$./_-]{1,128}$/

/** Same rule as instancespec.ValidBaseDomain (after lowercasing). */
function validBaseDomain(d: string): boolean {
  const labels = d.split('.')
  return d.length <= 200 && labels.length >= 2 && labels.every((l) => DNS_LABEL.test(l))
}

function settingsBody(): ServerSettings {
  return { ...db.serverSettings, unknown_containers: [] }
}
```
und in `serverOperations` **direkt vor** `op('getServer', …)`:
```ts
  // Before getServer: MSW matches in order and /servers/:id would also match /servers/settings.
  op(
    'getServerSettings',
    http.get(`${API}/servers/settings`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<ServerSettings>(settingsBody())
    }),
  ),

  op(
    'updateServerSettings',
    http.put(`${API}/servers/settings`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<UpdateServerSettingsRequest>(request)
      if (!body) return apiError(400, 'bad_request', 'invalid JSON body')
      const details: Record<string, unknown> = {}
      if (body.base_domain !== undefined) {
        const next = body.base_domain === null ? null : body.base_domain.toLowerCase()
        if (next !== null && !validBaseDomain(next)) return apiError(400, 'validation_failed', 'base_domain: invalid DNS name')
        if (next !== db.serverSettings.base_domain) {
          details.base_domain = { old: db.serverSettings.base_domain, new: next }
          db.serverSettings.base_domain = next
        }
      }
      if (body.curseforge_api_key !== undefined) {
        if (body.curseforge_api_key !== null && !CURSEFORGE_KEY.test(body.curseforge_api_key)) {
          return apiError(400, 'validation_failed', 'curseforge_api_key must match ^[A-Za-z0-9$./_-]{1,128}$')
        }
        db.serverSettings.curseforge_api_key_set = body.curseforge_api_key !== null
        details.curseforge_api_key = body.curseforge_api_key === null ? 'removed' : 'set'
      }
      if (Object.keys(details).length > 0) recordAudit(g.user, 'server.settings_update', 'servers', 'success', details)
      return HttpResponse.json<ServerSettings>(settingsBody())
    }),
  ),

```

`web/src/mocks/handlers.test.ts`: `toHaveLength(57)` → `toHaveLength(59)` und vor `it('buckets the overview series by range', …)`:
```ts
  it('keeps server settings: absent stays, null removes, the key is write-only', async () => {
    const me = loginAs('admin', { sudo: true })
    const get = async () => (await callApi('GET', '/servers/settings')).body
    expect(await get()).toEqual({ base_domain: 'mc.example.com', curseforge_api_key_set: false, docker: 'ok', unknown_containers: [] })
    await callApi('PUT', '/servers/settings', { body: { curseforge_api_key: 'abc$123' }, csrf: me.csrf_token })
    expect(field(await get(), 'curseforge_api_key_set')).toBe(true)
    expect(JSON.stringify(await get())).not.toContain('abc$123')
    await callApi('PUT', '/servers/settings', { body: { base_domain: 'Play.Example.org' }, csrf: me.csrf_token })
    expect(await get()).toMatchObject({ base_domain: 'play.example.org', curseforge_api_key_set: true })
    await callApi('PUT', '/servers/settings', { body: { base_domain: null, curseforge_api_key: null }, csrf: me.csrf_token })
    expect(await get()).toMatchObject({ base_domain: null, curseforge_api_key_set: false })
    const bad = await callApi('PUT', '/servers/settings', { body: { base_domain: 'localhost' }, csrf: me.csrf_token })
    expect(field(bad.body, 'error')).toBe('validation_failed')
  })

```

`web/src/api/errors.ts` (`KNOWN_CODES`), die `errors.*`-Texte und `ERROR_CODES` in `web/src/i18n/i18n.test.ts` bleiben unverändert – Aufgabe 17 trägt die drei neuen Codes dort ein. Bis dahin zeigt das Frontend sie als „unerwarteter Fehler“; kein Test hängt davon ab.

- [ ] **Step 8: Gesamtprüfung**

Run: `make test && make lint`
Expected: PASS (Go inkl. `TestRoutesMatchOpenAPI`, `TestErrorCodesMatchOpenAPI`, `TestRouteCoverage` mit 59; Vitest inkl. „mocks every operationId … exactly once“ mit 59), `0 issues`, eslint und `tsc -b` ohne Befunde.

- [ ] **Step 9: Commit**

```bash
git add internal/httpx/errors.go internal/audit/types.go internal/servers cmd/panel/app.go cmd/panel/integration_test.go \
  api/openapi.yaml web/src/api/schema.d.ts web/src/api/types.ts web/src/mocks/db.ts web/src/mocks/handlers/servers.ts \
  web/src/mocks/handlers.test.ts
git commit -m "feat(servers): server settings with base domain and sealed CurseForge key

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 13: CRUD mit Instanz-Konfiguration, Status-Cache und Zustandsableitung

`POST /servers` legt Server **und** Instanz in einer Transaktion an, `GET` liefert das neue `Server`-Schema (V4) mit aufgelösten Eigenschaften, Adresse und abgeleitetem Status, `PATCH /servers/{id}` ändert die Konfiguration (Revision + 1), `DELETE` entfernt bei eingerichteten Servern zuerst den Container beim agent. Status kommt aus einem Cache, den in Aufgabe 14 der Abgleich füllt (Tests füllen ihn über `servers.RefreshStatus` aus `export_test.go`).

**Files:**
- Create: `internal/servers/config.go`, `internal/servers/status.go`, `internal/servers/state.go`, `internal/servers/view.go`
- Modify: `internal/servers/servers.go` (komplett ersetzen), `internal/servers/handler.go`, `internal/servers/settings.go`, `internal/audit/types.go`, `cmd/panel/integration_test.go`, `api/openapi.yaml`, `web/src/api/types.ts`, `web/src/mocks/db.ts`, `web/src/mocks/handlers/servers.ts` (komplett ersetzen), `web/src/mocks/handlers.test.ts`, `web/src/features/dashboard/create-server-dialog.tsx`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`
- Generated: `web/src/api/schema.d.ts`
- Test: `internal/servers/export_test.go`, `internal/servers/state_test.go`, `internal/servers/servers_test.go` (komplett ersetzen), `internal/servers/helpers_test.go`, `internal/servers/routes_test.go`

**Interfaces:**
- Consumes: V1 (`ServersCreateInstance`, `ServersGetInstance`, `ServersListInstances`, `ServersUpdateInstance`), V2 (`Spec`, `Properties`, `Resolve`, `JavaFor`, `ValidName`), V3 (`InstancesClient.Instances/Remove`, `InstanceStatus`, `ContainerState`, `Operation`), Aufgabe 12 (`optional[T]`, `nullString`, `toNullString`, `strOrNil`, `inTx`, `Trigger`)
- Produces:
```go
// internal/audit/types.go
ActionServerUpdate = "server.update"
// internal/servers (paketintern, von Aufgabe 14 genutzt)
type Snapshot struct {
	Reachable bool
	Docker    string
	At        time.Time
	Instances map[int64]agentapi.InstanceStatus
}
func (s Snapshot) actualStatus(id int64) agentapi.InstanceStatus
func (h *Handler) refreshStatus(ctx context.Context) (agentapi.InstancesResponse, error)
func (h *Handler) snapshot() Snapshot
func deriveStatus(in *store.Instance, snap Snapshot) statusJSON
func isUp(s agentapi.ContainerState) bool
type config struct{ … }; func configFromInstance(store.Instance) (config, error); func (c config) spec(*string) instancespec.Spec; func (c config) validate(*string) error
func instanceFromListRow(store.ServersListInstancesRow) store.Instance
type instanceSettings struct{ BaseDomain, CurseForgeKey *string }; func (h *Handler) loadSettings(ctx) (instanceSettings, error)
func writeAgentError(w http.ResponseWriter, r *http.Request, err error)
const desiredRunning, desiredStopped = "running", "stopped"
// nur in Tests (export_test.go)
func RefreshStatus(ctx context.Context, h *Handler)
```
OpenAPI: Schemas `InstanceType`, `JavaVersion`, `ServerState`, `ServerProperties`, `InstanceConfig`, `ServerStatus`, `Server` (neu), `CreateServerRequest` (neu), `UpdateServerRequest` (alle wörtlich V4); Operation `PATCH /servers/{id}` (`updateServer`, Admin + Sudo, 200/400/404/409); `DELETE /servers/{id}` bekommt 429 und 503; neue Antwort-Komponente `ServiceUnavailable`. Operationen: 60. MSW: `MockServer` und Seed nach V5, Verhalten für list/get/create/update/delete nach V5 (Power bleibt bis Aufgabe 14 wie in TP1).

- [ ] **Step 1: Failing tests schreiben**

`internal/servers/export_test.go`:

```go
package servers

import "context"

// RefreshStatus loads the agent's actual state into the status cache, which the reconcile
// loop does in production.
func RefreshStatus(ctx context.Context, h *Handler) { _, _ = h.refreshStatus(ctx) }
```

`internal/servers/state_test.go`:

```go
package servers

import (
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// One case per row of the state table (contract V4), in table order.
func TestDeriveStatusTable(t *testing.T) {
	in := &store.Instance{ServerID: 1, DesiredState: desiredRunning, Revision: 2}
	stopped := &store.Instance{ServerID: 1, DesiredState: desiredStopped, Revision: 2}
	ok := func(st ...agentapi.InstanceStatus) Snapshot {
		s := Snapshot{Reachable: true, Docker: agentapi.DockerOK, Instances: map[int64]agentapi.InstanceStatus{}}
		for _, x := range st {
			s.Instances[x.ID] = x
		}
		return s
	}
	c := func(state agentapi.ContainerState, op agentapi.Operation) agentapi.InstanceStatus {
		return agentapi.InstanceStatus{ID: 1, Revision: 2, State: state, Operation: op}
	}
	exited := func(code int, lastErr string) agentapi.InstanceStatus {
		return agentapi.InstanceStatus{ID: 1, Revision: 2, State: agentapi.StateExited, ExitCode: code, LastError: lastErr}
	}
	cases := []struct {
		name string
		in   *store.Instance
		snap Snapshot
		want string
	}{
		{"no instance row", nil, ok(), stateNotConfigured},
		{"agent unreachable", in, Snapshot{}, stateUnknown},
		{"docker unavailable", in, Snapshot{Reachable: true, Docker: agentapi.DockerUnavailable}, stateUnknown},
		{"pulling", in, ok(c(agentapi.StateMissing, agentapi.OpPulling)), stateCreating},
		{"creating", in, ok(c(agentapi.StateMissing, agentapi.OpCreating)), stateCreating},
		{"container not listed", in, ok(), stateCreating},
		{"missing", in, ok(c(agentapi.StateMissing, agentapi.OpNone)), stateCreating},
		{"starting", in, ok(c(agentapi.StateCreated, agentapi.OpStarting)), stateStarting},
		{"stopping", in, ok(c(agentapi.StateRunning, agentapi.OpStopping)), stateStopping},
		{"removing", in, ok(c(agentapi.StateRunning, agentapi.OpRemoving)), stateStopping},
		{"restarting", in, ok(c(agentapi.StateRestarting, agentapi.OpNone)), stateRestarting},
		{"running", in, ok(c(agentapi.StateRunning, agentapi.OpNone)), stateRunning},
		{"exited with code, wants running", in, ok(exited(1, "")), stateError},
		{"exited with last_error, wants running", in, ok(exited(0, "pull failed")), stateError},
		{"exited cleanly, wants running", in, ok(exited(0, "")), stateStopped},
		{"exited with code, wants stopped", stopped, ok(exited(137, "")), stateStopped},
		{"created", in, ok(c(agentapi.StateCreated, agentapi.OpNone)), stateStopped},
		{"unknown container state", in, ok(c("paused", agentapi.OpNone)), stateUnknown},
	}
	for _, tc := range cases {
		got := deriveStatus(tc.in, tc.snap)
		if got.State != tc.want {
			t.Errorf("%s: state %s, want %s", tc.name, got.State, tc.want)
		}
		if (tc.in == nil) != (got.DesiredState == nil) {
			t.Errorf("%s: desired_state = %v", tc.name, got.DesiredState)
		}
	}
}

func TestRestartRequired(t *testing.T) {
	in := &store.Instance{ServerID: 1, DesiredState: desiredRunning, Revision: 3}
	for _, tc := range []struct {
		state agentapi.ContainerState
		rev   int64
		want  bool
	}{
		{agentapi.StateRunning, 2, true},
		{agentapi.StateRestarting, 2, true},
		{agentapi.StateRunning, 3, false},
		{agentapi.StateExited, 2, false},
		{agentapi.StateCreated, 1, false},
	} {
		snap := Snapshot{Reachable: true, Docker: agentapi.DockerOK, Instances: map[int64]agentapi.InstanceStatus{
			1: {ID: 1, Revision: tc.rev, State: tc.state},
		}}
		if got := deriveStatus(in, snap).RestartRequired; got != tc.want {
			t.Errorf("%s rev %d: restart_required %t, want %t", tc.state, tc.rev, got, tc.want)
		}
	}
}
```

`internal/servers/helpers_test.go` – drei Ergänzungen:

1. Die beiden Einzeiler-Setter ersetzen durch (gofmt richtet sie aus):
```go
func (f *fakeAgent) setErr(err error)       { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeAgent) setActionErr(err error) { f.mu.Lock(); f.actionErr = err; f.mu.Unlock() }
func (f *fakeAgent) setDocker(s string)     { f.mu.Lock(); f.docker = s; f.mu.Unlock() }
```
2. Direkt vor `func (f *fakeAgent) Instances(`:
```go
// takeCalls returns the recorded calls ("ensure 1 r2 recreate=false", "power 1 start",
// "remove 1") and forgets them.
func (f *fakeAgent) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.calls
	f.calls = nil
	return out
}

```
3. Direkt vor `// createInstance inserts`:
```go
// setBaseDomain stores a base domain directly (the API path is tested in settings_test.go).
func setBaseDomain(t *testing.T, db *sql.DB, d string) {
	t.Helper()
	mustExec(t, db, `UPDATE instance_settings SET base_domain = ? WHERE id = 1`, d)
}

```

`internal/servers/routes_test.go`: in `wantRoutes` nach `"GET /api/servers/{id}"` ergänzen (danach `gofmt -w`):
```go
	"PATCH /api/servers/{id}":      rbac.Admin().WithSudo(),
```

`internal/servers/servers_test.go` komplett ersetzen:

```go
package servers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/servers"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type statusResp struct {
	State            string  `json:"state"`
	DesiredState     *string `json:"desired_state"`
	RestartRequired  bool    `json:"restart_required"`
	Operation        string  `json:"operation"`
	LastError        string  `json:"last_error"`
	StartedAt        int64   `json:"started_at"`
	MemoryBytes      int64   `json:"memory_bytes"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	CPUPercent       float64 `json:"cpu_percent"`
}

type instanceResp struct {
	Type          string         `json:"type"`
	Version       string         `json:"version"`
	LoaderVersion *string        `json:"loader_version"`
	Modpack       *string        `json:"modpack"`
	Java          string         `json:"java"`
	MemoryMiB     int            `json:"memory_mib"`
	Properties    map[string]any `json:"properties"`
}

type serverResp struct {
	ID          int64         `json:"id"`
	Name        string        `json:"name"`
	Permissions []string      `json:"permissions"`
	Address     *string       `json:"address"`
	Instance    *instanceResp `json:"instance"`
	Status      statusResp    `json:"status"`
}

var allPermStrings = []string{"server.view", "server.power", "server.console", "server.files", "server.plugins", "server.backups"}

// paper returns a valid CreateServerRequest body.
func paper(name string) map[string]any {
	return map[string]any{"name": name, "type": "paper", "version": "1.21.11", "memory_mib": 4096, "eula_accepted": true}
}

// with returns a copy of body with key set to val (nil deletes the key).
func with(body map[string]any, key string, val any) map[string]any {
	out := maps.Clone(body)
	if val == nil {
		delete(out, key)
	} else {
		out[key] = val
	}
	return out
}

func (e *env) create(t *testing.T, body map[string]any) serverResp {
	t.Helper()
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", body, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %v: %d %s", body, rec.Code, rec.Body)
	}
	return decode[serverResp](t, rec)
}

func (e *env) get(t *testing.T, id int64) serverResp {
	t.Helper()
	admin := e.admin()
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", id), nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("get %d: %d %s", id, rec.Code, rec.Body)
	}
	return decode[serverResp](t, rec)
}

func (e *env) refresh() { servers.RefreshStatus(context.Background(), e.h) }

func (e *env) count(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := e.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestServerCRUDAsAdmin(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, paper("survival"))
	if s.ID == 0 || s.Name != "survival" || !slices.Equal(s.Permissions, allPermStrings) {
		t.Fatalf("created = %+v", s)
	}
	if rec := e.do(http.MethodPost, "/servers", paper("survival"), &admin); rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"-bad", "a", "Upper", "has_underscore", "with space", strings.Repeat("a", 33)} {
		rec := e.do(http.MethodPost, "/servers", paper(bad), &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
			t.Errorf("name %q: %d %s", bad, rec.Code, rec.Body)
		}
	}
	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &admin))
	if len(list) != 1 || list[0].ID != s.ID || !slices.Equal(list[0].Permissions, allPermStrings) {
		t.Fatalf("list = %+v", list)
	}
	path := fmt.Sprintf("/servers/%d", s.ID)
	if got := e.get(t, s.ID); got.Name != "survival" {
		t.Fatalf("get = %+v", got)
	}
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, path, nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted: %d", rec.Code)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionServerCreate, audit.ActionServerDelete}) {
		t.Fatalf("audit = %v", got)
	}
	if en := e.rec.Entries[0]; en.Target != fmt.Sprintf("server:%d", s.ID) || en.Details["name"] != "survival" {
		t.Fatalf("audit entry = %+v", en)
	}
}

func TestCreateServerStoresInstance(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	e.refresh() // agent reachable, no containers yet
	s := e.create(t, with(paper("survival"), "properties", map[string]any{"motd": "Hallo", "max_players": 8}))

	if s.Address == nil || *s.Address != "survival.mc.example.com" {
		t.Fatalf("address = %v", s.Address)
	}
	in := s.Instance
	if in == nil || in.Type != "paper" || in.Version != "1.21.11" || in.Java != "java21" || in.MemoryMiB != 4096 ||
		in.LoaderVersion != nil || in.Modpack != nil {
		t.Fatalf("instance = %+v", in)
	}
	// properties come back fully resolved
	p := in.Properties
	if pvp, _ := p["pvp"].(bool); p["motd"] != "Hallo" || p["max_players"] != float64(8) || p["difficulty"] != "normal" || !pvp ||
		p["view_distance"] != float64(10) || p["level_type"] != "normal" {
		t.Fatalf("properties = %v", p)
	}
	if wl, ok := p["whitelist"].([]any); !ok || len(wl) != 0 {
		t.Fatalf("whitelist = %#v, want []", p["whitelist"])
	}
	if s.Status.State != "creating" || s.Status.DesiredState == nil || *s.Status.DesiredState != "running" || s.Status.RestartRequired {
		t.Fatalf("status = %+v", s.Status)
	}
	// the database keeps the properties as given, not resolved
	row := instanceRow(t, e.db, s.ID)
	if row.Properties != `{"motd":"Hallo","max_players":8}` || row.Revision != 1 || row.DesiredState != "running" {
		t.Fatalf("row = %+v", row)
	}
	en := e.rec.Entries[0]
	if en.Action != audit.ActionServerCreate || en.Details["type"] != "paper" || en.Details["java"] != "java21" ||
		en.Details["memory_mib"] != 4096 || en.Details["name"] != "survival" {
		t.Fatalf("audit = %+v", en)
	}
}

func TestCreateServerDerivesOrKeepsJava(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	if got := e.create(t, with(with(paper("old"), "type", "vanilla"), "version", "1.16.5")).Instance.Java; got != "java8" {
		t.Fatalf("1.16.5 -> %s, want java8", got)
	}
	if got := e.create(t, with(paper("latest"), "version", "LATEST")).Instance.Java; got != "java25" {
		t.Fatalf("LATEST -> %s, want java25", got)
	}
	if got := e.create(t, with(paper("pinned"), "java", "java25")).Instance.Java; got != "java25" {
		t.Fatalf("explicit java ignored: %s", got)
	}
}

// Review focus 3: no base domain -> 409 base_domain_missing, nothing written.
func TestCreateServerWithoutBaseDomain(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", paper("survival"), &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "base_domain_missing" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if n, m := e.count(t, "servers"), e.count(t, "instances"); n != 0 || m != 0 || len(e.rec.Entries) != 0 {
		t.Fatalf("rows servers=%d instances=%d audit=%v", n, m, e.rec.Actions())
	}
}

// Review focus 3: a taken name -> 409 conflict and no half-written instance row.
func TestCreateServerNameTakenLeavesNoHalfRow(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	e.create(t, paper("survival"))
	storetest.CreateServer(t, e.db, "lobby") // TP1 stub without instance
	for _, name := range []string{"survival", "lobby"} {
		rec := e.do(http.MethodPost, "/servers", with(paper(name), "type", "vanilla"), &admin)
		if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if n, m := e.count(t, "servers"), e.count(t, "instances"); n != 2 || m != 1 {
		t.Fatalf("servers=%d instances=%d, want 2 and 1", n, m)
	}
}

func TestCreateServerValidation(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	cases := map[string]struct {
		body map[string]any
		code string
	}{
		"eula false":          {with(paper("a1"), "eula_accepted", false), "validation_failed"},
		"eula missing":        {with(paper("a1"), "eula_accepted", nil), "validation_failed"},
		"unknown type":        {with(paper("a1"), "type", "bukkit"), "validation_failed"},
		"type missing":        {with(paper("a1"), "type", nil), "validation_failed"},
		"memory step":         {with(paper("a1"), "memory_mib", 4000), "validation_failed"},
		"memory missing":      {with(paper("a1"), "memory_mib", nil), "validation_failed"},
		"version with space":  {with(paper("a1"), "version", "1.21 4"), "validation_failed"},
		"version newline":     {with(paper("a1"), "version", "1.21\nEULA=FALSE"), "validation_failed"},
		"loader on paper":     {with(paper("a1"), "loader_version", "0.19.5"), "validation_failed"},
		"modpack on paper":    {with(paper("a1"), "modpack", "atm10"), "validation_failed"},
		"java unknown":        {with(paper("a1"), "java", "java11"), "validation_failed"},
		"motd newline":        {with(paper("a1"), "properties", map[string]any{"motd": "a\nENABLE_RCON=false"}), "validation_failed"},
		"unknown property":    {with(paper("a1"), "properties", map[string]any{"jvm_opts": "-Xmx99G"}), "validation_failed"},
		"property wrong type": {with(paper("a1"), "properties", map[string]any{"max_players": "20"}), "validation_failed"},
		"curseforge no key":   {map[string]any{"name": "a1", "type": "curseforge", "version": "", "modpack": "atm10", "memory_mib": 8192, "eula_accepted": true}, "validation_failed"},
		"unknown field":       {with(paper("a1"), "privileged", true), "bad_request"},
	}
	for name, tc := range cases {
		rec := e.do(http.MethodPost, "/servers", tc.body, &admin)
		if rec.Code != http.StatusBadRequest || errCode(t, rec) != tc.code {
			t.Errorf("%s: %d %s, want 400 %s", name, rec.Code, rec.Body, tc.code)
		}
	}
	if n := e.count(t, "servers"); n != 0 {
		t.Fatalf("invalid requests created %d servers", n)
	}
}

func TestCreateCurseForgeUsesKeyFromSettings(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	const key = "$2a$10$abcdefghijklmnopqrstuv"
	e.putSettings(t, `{"curseforge_api_key":"`+key+`"}`)
	admin := e.admin()
	rec := e.do(http.MethodPost, "/servers", map[string]any{
		"name": "atm", "type": "curseforge", "version": "", "modpack": "all-the-mods-10", "memory_mib": 8192, "eula_accepted": true,
	}, &admin)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), key) {
		t.Fatal("response leaks the CurseForge key")
	}
	s := decode[serverResp](t, rec)
	if s.Instance.Java != "java21" || s.Instance.Modpack == nil || *s.Instance.Modpack != "all-the-mods-10" {
		t.Fatalf("instance = %+v", s.Instance)
	}
	raw, _ := json.Marshal(e.rec.Entries)
	if strings.Contains(string(raw), key) {
		t.Fatalf("audit leaks the key: %s", raw)
	}
}

func TestGetServerShowsActualState(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "survival")
	stub := storetest.CreateServer(t, e.db, "lobby")
	e.agent.set(agentapi.InstanceStatus{
		ID: id, Name: "survival", Revision: 1, State: agentapi.StateRunning, StartedAt: t0.UnixMilli(),
		MemoryBytes: 2 << 30, MemoryLimitBytes: 5 << 30, CPUPercent: 12.5,
	})
	e.refresh()
	got := e.get(t, id).Status
	if got.State != "running" || got.StartedAt != t0.UnixMilli() || got.MemoryBytes != 2<<30 ||
		got.MemoryLimitBytes != 5<<30 || got.CPUPercent != 12.5 || got.RestartRequired {
		t.Fatalf("status = %+v", got)
	}
	ns := e.get(t, stub)
	if ns.Status.State != "not_configured" || ns.Status.DesiredState != nil || ns.Instance != nil || ns.Address != nil {
		t.Fatalf("stub = %+v", ns)
	}

	// agent down: the desired state is still shown, the actual state is unknown
	e.agent.setErr(agentapi.ErrUnavailable)
	e.refresh()
	got = e.get(t, id).Status
	if got.State != "unknown" || got.DesiredState == nil || *got.DesiredState != "running" {
		t.Fatalf("agent down: %+v", got)
	}
	// a snapshot older than a minute counts as unknown as well
	e.agent.setErr(nil)
	e.refresh()
	e.clk.Advance(2 * time.Minute)
	if got := e.get(t, id).Status.State; got != "unknown" {
		t.Fatalf("stale snapshot: %s", got)
	}
}

func TestUpdateServer(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, with(paper("survival"), "properties", map[string]any{"max_players": 8}))
	e.agent.set(agentapi.InstanceStatus{ID: s.ID, Name: "survival", Revision: 1, State: agentapi.StateRunning})
	e.refresh()
	path := fmt.Sprintf("/servers/%d", s.ID)

	rec := e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "properties": map[string]any{"motd": "Neu"}}, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	got := decode[serverResp](t, rec)
	// properties replace the whole object: max_players falls back to its default
	if got.Instance.MemoryMiB != 6144 || got.Instance.Properties["motd"] != "Neu" || got.Instance.Properties["max_players"] != float64(20) {
		t.Fatalf("instance = %+v", got.Instance)
	}
	if !got.Status.RestartRequired || got.Status.State != "running" {
		t.Fatalf("running container with old revision must need a restart: %+v", got.Status)
	}
	if row := instanceRow(t, e.db, s.ID); row.Revision != 2 || row.Properties != `{"motd":"Neu"}` {
		t.Fatalf("row = %+v", row)
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	changes, _ := en.Details["changes"].(map[string]any)
	mem, _ := changes["memory_mib"].(map[string]any)
	motd, _ := changes["properties.motd"].(map[string]any)
	if en.Action != audit.ActionServerUpdate || mem["old"] != 4096 || mem["new"] != 6144 || motd["old"] != nil ||
		string(motd["new"].(json.RawMessage)) != `"Neu"` || changes["properties.max_players"] == nil || len(changes) != 3 {
		t.Fatalf("audit = %+v", en.Details)
	}

	// unchanged values: no new revision, no audit entry
	before := len(e.rec.Entries)
	rec = e.do(http.MethodPatch, path, map[string]any{"memory_mib": 6144, "loader_version": nil}, &admin)
	if rec.Code != http.StatusOK || instanceRow(t, e.db, s.ID).Revision != 2 || len(e.rec.Entries) != before {
		t.Fatalf("no-op patch: %d, revision %d, audit %v", rec.Code, instanceRow(t, e.db, s.ID).Revision, e.rec.Actions())
	}

	// a stopped container takes the new revision on the next reconcile: no restart needed
	e.agent.set(agentapi.InstanceStatus{ID: s.ID, Name: "survival", Revision: 1, State: agentapi.StateExited})
	e.refresh()
	if st := e.get(t, s.ID).Status; st.RestartRequired || st.State != "stopped" {
		t.Fatalf("stopped: %+v", st)
	}
}

func TestUpdateServerRejects(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, paper("survival"))
	stub := storetest.CreateServer(t, e.db, "lobby")
	path := fmt.Sprintf("/servers/%d", s.ID)
	for name, tc := range map[string]struct {
		body   any
		status int
		code   string
	}{
		"version null":     {map[string]any{"version": nil}, 400, "validation_failed"},
		"memory null":      {map[string]any{"memory_mib": nil}, 400, "validation_failed"},
		"properties null":  {map[string]any{"properties": nil}, 400, "validation_failed"},
		"memory invalid":   {map[string]any{"memory_mib": 512}, 400, "validation_failed"},
		"loader on paper":  {map[string]any{"loader_version": "1"}, 400, "validation_failed"},
		"unknown property": {map[string]any{"properties": map[string]any{"jvm_opts": "x"}}, 400, "validation_failed"},
		"type not allowed": {map[string]any{"type": "vanilla"}, 400, "bad_request"},
		"name not allowed": {map[string]any{"name": "other"}, 400, "bad_request"},
	} {
		rec := e.do(http.MethodPatch, path, tc.body, &admin)
		if rec.Code != tc.status || errCode(t, rec) != tc.code {
			t.Errorf("%s: %d %s, want %d %s", name, rec.Code, rec.Body, tc.status, tc.code)
		}
	}
	if rev := instanceRow(t, e.db, s.ID).Revision; rev != 1 {
		t.Fatalf("rejected patches changed the revision to %d", rev)
	}
	rec := e.do(http.MethodPatch, fmt.Sprintf("/servers/%d", stub), map[string]any{"memory_mib": 2048}, &admin)
	if rec.Code != http.StatusConflict || errCode(t, rec) != "conflict" {
		t.Fatalf("patch stub: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodPatch, "/servers/9999", map[string]any{"memory_mib": 2048}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("patch unknown: %d", rec.Code)
	}
}

func TestDeleteServerRemovesContainerFirst(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	s := e.create(t, paper("survival"))
	path := fmt.Sprintf("/servers/%d", s.ID)

	e.agent.setErr(agentapi.ErrUnavailable)
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "agent_unavailable" {
		t.Fatalf("agent down: %d %s", rec.Code, rec.Body)
	}
	e.agent.setErr(nil)
	e.agent.setActionErr(agentapi.ErrDockerUnavailable)
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "docker_unavailable" {
		t.Fatalf("docker down: %d %s", rec.Code, rec.Body)
	}
	if n := e.count(t, "instances"); n != 1 {
		t.Fatalf("failed deletes removed rows (instances=%d)", n)
	}
	e.agent.setActionErr(nil)
	if rec := e.do(http.MethodDelete, path, nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if calls := e.agent.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("remove %d", s.ID)}) {
		t.Fatalf("agent calls = %v", calls)
	}
	if n, m := e.count(t, "servers"), e.count(t, "instances"); n != 0 || m != 0 {
		t.Fatalf("rows left: servers=%d instances=%d", n, m)
	}
	en := e.rec.Entries[len(e.rec.Entries)-1]
	if configured, _ := en.Details["configured"].(bool); en.Action != audit.ActionServerDelete || !configured {
		t.Fatalf("audit = %+v", en)
	}

	// a server without instance needs no agent
	stub := storetest.CreateServer(t, e.db, "lobby")
	e.agent.setErr(agentapi.ErrUnavailable)
	if rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", stub), nil, &admin); rec.Code != http.StatusNoContent {
		t.Fatalf("delete stub without agent: %d %s", rec.Code, rec.Body)
	}
	if calls := e.agent.takeCalls(); len(calls) != 0 {
		t.Fatalf("stub delete called the agent: %v", calls)
	}
}

// Spec §12.4: a restricted user with server.power on server A sees only A; B → 403.
func TestServerVisibilityForRestrictedUser(t *testing.T) {
	e := newEnv(t)
	alice := storetest.CreateUser(t, e.db, "alice", "user")
	a := storetest.CreateServer(t, e.db, "alpha")
	b := storetest.CreateServer(t, e.db, "beta")
	grant(t, e.db, alice, a, "server.view", "server.power")
	ap := userP(alice, "alice")

	list := decode[[]serverResp](t, e.do(http.MethodGet, "/servers", nil, &ap))
	want := []string{"server.view", "server.power"}
	if len(list) != 1 || list[0].ID != a || list[0].Name != "alpha" || !slices.Equal(list[0].Permissions, want) {
		t.Fatalf("list = %+v", list)
	}
	rec := e.do(http.MethodGet, fmt.Sprintf("/servers/%d", a), nil, &ap)
	if rec.Code != http.StatusOK || !slices.Equal(decode[serverResp](t, rec).Permissions, want) {
		t.Fatalf("get A: %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, fmt.Sprintf("/servers/%d", b), nil},
		{http.MethodPost, fmt.Sprintf("/servers/%d/power", b), map[string]string{"action": "start"}},
		{http.MethodPost, "/servers", paper("gamma")},
		{http.MethodPatch, fmt.Sprintf("/servers/%d", a), map[string]any{"memory_mib": 2048}},
		{http.MethodDelete, fmt.Sprintf("/servers/%d", a), nil},
		{http.MethodGet, "/servers/settings", nil},
	} {
		if rec := e.do(tc.method, tc.path, tc.body, &ap); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d, want 403", tc.method, tc.path, rec.Code)
		}
	}

	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "start"}, &ap)
	if rec.Code != http.StatusAccepted || strings.TrimSpace(rec.Body.String()) != `{"status":"accepted"}` {
		t.Fatalf("power A: %d %s", rec.Code, rec.Body)
	}
	last := e.rec.Entries[len(e.rec.Entries)-1]
	if last.Action != audit.ActionServerPower || last.Target != fmt.Sprintf("server:%d", a) ||
		last.Details["action"] != "start" || last.Result != audit.ResultSuccess {
		t.Fatalf("power audit = %+v", last)
	}
	rec = e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "explode"}, &ap)
	if rec.Code != http.StatusBadRequest || errCode(t, rec) != "validation_failed" {
		t.Fatalf("invalid action: %d %s", rec.Code, rec.Body)
	}
}

func TestServerListWithoutPermissionsIsEmptyArray(t *testing.T) {
	e := newEnv(t)
	storetest.CreateServer(t, e.db, "alpha")
	bob := userP(storetest.CreateUser(t, e.db, "bob", "user"), "bob")
	rec := e.do(http.MethodGet, "/servers", nil, &bob)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("list: %d %q", rec.Code, rec.Body)
	}
}

func TestServerPowerUnknownServerAsAdmin(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	if rec := e.do(http.MethodPost, "/servers/9999/power", map[string]string{"action": "stop"}, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("power: %d", rec.Code)
	}
	if rec := e.do(http.MethodGet, "/servers/9999", nil, &admin); rec.Code != http.StatusNotFound {
		t.Fatalf("get: %d", rec.Code)
	}
}

// If the row vanishes between the lookup and the delete (concurrent delete), the handler must
// answer 404 and not audit a deletion that did not happen. A BEFORE DELETE trigger with
// RAISE(IGNORE) makes the DELETE affect 0 rows deterministically.
func TestDeleteServerZeroRowsIs404(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := storetest.CreateServer(t, e.db, "alpha")
	mustExec(t, e.db, `CREATE TRIGGER servers_ignore_delete BEFORE DELETE ON servers BEGIN SELECT RAISE(IGNORE); END`)
	rec := e.do(http.MethodDelete, fmt.Sprintf("/servers/%d", id), nil, &admin)
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if got := e.rec.Actions(); slices.Contains(got, audit.ActionServerDelete) {
		t.Fatalf("unexpected audit entries: %v", got)
	}
}

// Power actions are limited to 10 per minute per user, so a user cannot flood the audit chain.
func TestServerPowerRateLimitedPerUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	a := storetest.CreateServer(t, e.db, "alpha")
	path := fmt.Sprintf("/servers/%d/power", a)
	body := map[string]string{"action": "restart"}
	for i := 0; i < 10; i++ {
		if rec := e.do(http.MethodPost, path, body, &admin); rec.Code != http.StatusAccepted {
			t.Fatalf("request %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := e.do(http.MethodPost, path, body, &admin)
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th request: %d %s retry=%q", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
	if n := len(e.rec.Entries); n != 10 {
		t.Fatalf("audit entries = %d, want 10 (a limited request is not audited)", n)
	}
	// another user has its own budget
	other := principal.Principal{UserID: storetest.CreateUser(t, e.db, "root2", "admin"), Username: "root2",
		Role: principal.RoleAdmin, Stage: principal.StageFull}
	if rec := e.do(http.MethodPost, path, body, &other); rec.Code != http.StatusAccepted {
		t.Fatalf("other user: %d", rec.Code)
	}
	e.clk.Advance(6 * time.Second)
	if rec := e.do(http.MethodPost, path, body, &admin); rec.Code != http.StatusAccepted {
		t.Fatalf("after refill: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Tests laufen lassen (rot)**

Run: `go test ./internal/servers/ -count=1`
Expected: FAIL beim Kompilieren (`undefined: Snapshot`, `undefined: deriveStatus`, `h.refreshStatus undefined`).

- [ ] **Step 3: Audit-Aktion**

`internal/audit/types.go`, nach `ActionServerPower`:
```go
	ActionServerUpdate         = "server.update"
```

- [ ] **Step 4: Konfiguration, Status-Cache, Zustandsableitung, Antwortschema**

`internal/servers/config.go`:

```go
package servers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Desired states of an instance (instances.desired_state).
const (
	desiredRunning = "running"
	desiredStopped = "stopped"
)

// config is the stored configuration of one instance as the admin gave it. Properties stay
// sparse (unset = default), so the resolved values are computed on every read.
type config struct {
	Type          instancespec.Type
	Version       string
	LoaderVersion *string
	Modpack       *string
	Java          instancespec.Java
	MemoryMiB     int
	Properties    instancespec.Properties
}

func instanceFromListRow(r store.ServersListInstancesRow) store.Instance {
	return store.Instance{
		ServerID: r.ServerID, Type: r.Type, Version: r.Version, LoaderVersion: r.LoaderVersion,
		Modpack: r.Modpack, Java: r.Java, MemoryMib: r.MemoryMib, Properties: r.Properties,
		DesiredState: r.DesiredState, Revision: r.Revision, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func configFromInstance(in store.Instance) (config, error) {
	var props instancespec.Properties
	if err := json.Unmarshal([]byte(in.Properties), &props); err != nil {
		return config{}, fmt.Errorf("servers: properties of server %d: %w", in.ServerID, err)
	}
	return config{
		Type: instancespec.Type(in.Type), Version: in.Version, LoaderVersion: nullString(in.LoaderVersion),
		Modpack: nullString(in.Modpack), Java: instancespec.Java(in.Java), MemoryMiB: int(in.MemoryMib),
		Properties: props,
	}, nil
}

// spec is the agent's view of the config; the CurseForge key is only attached for curseforge.
func (c config) spec(curseForgeKey *string) instancespec.Spec {
	s := instancespec.Spec{
		Type: c.Type, Version: c.Version, LoaderVersion: c.LoaderVersion, Modpack: c.Modpack,
		Java: c.Java, MemoryMiB: c.MemoryMiB, Properties: c.Properties,
	}
	if c.Type == instancespec.TypeCurseForge {
		s.CurseForgeAPIKey = curseForgeKey
	}
	return s
}

// validate checks the config with the agent's rules (instancespec), including the
// CurseForge key from the settings.
func (c config) validate(curseForgeKey *string) error {
	if c.Type == instancespec.TypeCurseForge && curseForgeKey == nil {
		return fmt.Errorf("%w: curseforge_api_key: set a CurseForge API key in the server settings first", instancespec.ErrInvalid)
	}
	return c.spec(curseForgeKey).Validate()
}

func (c config) propertiesJSON() (string, error) {
	b, err := json.Marshal(c.Properties)
	return string(b), err
}

// auditDetails describes the config for server.create. It contains no secrets: the
// CurseForge key lives in the settings, not in the config.
func (c config) auditDetails(name string) map[string]any {
	return map[string]any{
		"name": name, "type": string(c.Type), "version": c.Version,
		"loader_version": strOrNil(c.LoaderVersion), "modpack": strOrNil(c.Modpack),
		"java": string(c.Java), "memory_mib": c.MemoryMiB, "properties": c.Properties,
	}
}

func eqPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// diff returns the changed fields as {"old": …, "new": …}; properties are compared per key
// ("properties.motd").
func diff(old, cur config) (map[string]any, error) {
	d := map[string]any{}
	change := func(field string, o, n any) { d[field] = map[string]any{"old": o, "new": n} }
	if old.Version != cur.Version {
		change("version", old.Version, cur.Version)
	}
	if !eqPtr(old.LoaderVersion, cur.LoaderVersion) {
		change("loader_version", strOrNil(old.LoaderVersion), strOrNil(cur.LoaderVersion))
	}
	if !eqPtr(old.Modpack, cur.Modpack) {
		change("modpack", strOrNil(old.Modpack), strOrNil(cur.Modpack))
	}
	if old.Java != cur.Java {
		change("java", string(old.Java), string(cur.Java))
	}
	if old.MemoryMiB != cur.MemoryMiB {
		change("memory_mib", old.MemoryMiB, cur.MemoryMiB)
	}
	op, err := propertyMap(old.Properties)
	if err != nil {
		return nil, err
	}
	np, err := propertyMap(cur.Properties)
	if err != nil {
		return nil, err
	}
	keys := slices.Sorted(maps.Keys(op))
	for k := range np {
		if _, ok := op[k]; !ok {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		if !bytes.Equal(op[k], np[k]) {
			change("properties."+k, rawOrNil(op[k]), rawOrNil(np[k]))
		}
	}
	return d, nil
}

func propertyMap(p instancespec.Properties) (map[string]json.RawMessage, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	return m, json.Unmarshal(b, &m)
}

// rawOrNil keeps a JSON value as is (audit details embed it) and maps "unset" to null.
func rawOrNil(v json.RawMessage) any {
	if v == nil {
		return nil
	}
	return v
}

// decodeProperties strictly decodes a ServerProperties object; unknown keys and wrong types
// are validation errors. Absent or null means "all defaults".
func decodeProperties(raw json.RawMessage) (instancespec.Properties, error) {
	var p instancespec.Properties
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return p, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return instancespec.Properties{}, fmt.Errorf("%w: properties: %w", instancespec.ErrInvalid, err)
	}
	return p, nil
}
```

`internal/servers/status.go`:

```go
package servers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// statusMaxAge: an older snapshot means the reconcile loop is not getting through, so the
// actual state is reported as unknown.
const statusMaxAge = time.Minute

// Snapshot is the last actual state reported by the agent.
type Snapshot struct {
	Reachable bool   // the agent answered
	Docker    string // agentapi.DockerOK or agentapi.DockerUnavailable (only if Reachable)
	At        time.Time
	Instances map[int64]agentapi.InstanceStatus // by server id
}

type statusCache struct {
	mu   sync.Mutex
	snap Snapshot
}

// snapshot returns the cached state, or the zero Snapshot (unreachable) if it is too old.
func (h *Handler) snapshot() Snapshot {
	h.status.mu.Lock()
	s := h.status.snap
	h.status.mu.Unlock()
	if s.At.IsZero() || h.clock.Now().Sub(s.At) > statusMaxAge {
		return Snapshot{}
	}
	return s
}

// refreshStatus asks the agent for the actual state and caches the answer (or the failure).
func (h *Handler) refreshStatus(ctx context.Context) (agentapi.InstancesResponse, error) {
	resp, err := h.inst.Instances(ctx)
	snap := Snapshot{At: h.clock.Now()}
	switch {
	case err == nil:
		snap.Reachable, snap.Docker = true, resp.Docker
		snap.Instances = make(map[int64]agentapi.InstanceStatus, len(resp.Instances))
		for _, st := range resp.Instances {
			snap.Instances[st.ID] = st
		}
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		snap.Reachable, snap.Docker = true, agentapi.DockerUnavailable
	}
	h.status.mu.Lock()
	h.status.snap = snap
	h.status.mu.Unlock()
	return resp, err
}
```

`internal/servers/state.go`:

```go
package servers

import (
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Values of the ServerState schema.
const (
	stateNotConfigured = "not_configured"
	stateUnknown       = "unknown"
	stateCreating      = "creating"
	stateStarting      = "starting"
	stateRunning       = "running"
	stateStopping      = "stopping"
	stateStopped       = "stopped"
	stateRestarting    = "restarting"
	stateError         = "error"
)

// statusJSON is the OpenAPI schema ServerStatus.
type statusJSON struct {
	State            string  `json:"state"`
	DesiredState     *string `json:"desired_state"`
	RestartRequired  bool    `json:"restart_required"`
	Operation        string  `json:"operation"`
	LastError        string  `json:"last_error"`
	StartedAt        int64   `json:"started_at"`
	MemoryBytes      int64   `json:"memory_bytes"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	CPUPercent       float64 `json:"cpu_percent"`
}

// isUp reports a container that runs (or that Docker is restarting).
func isUp(s agentapi.ContainerState) bool {
	return s == agentapi.StateRunning || s == agentapi.StateRestarting
}

// actualStatus returns the agent's status of one instance; a container the agent does not
// list is missing.
func (s Snapshot) actualStatus(id int64) agentapi.InstanceStatus {
	if st, ok := s.Instances[id]; ok {
		return st
	}
	return agentapi.InstanceStatus{ID: id, State: agentapi.StateMissing}
}

// deriveStatus implements the state table of the plan (contract V4). in == nil means the
// server has no instance yet (TP1 stub).
func deriveStatus(in *store.Instance, snap Snapshot) statusJSON {
	if in == nil {
		return statusJSON{State: stateNotConfigured}
	}
	desired := in.DesiredState
	out := statusJSON{State: stateUnknown, DesiredState: &desired}
	if !snap.Reachable || snap.Docker != agentapi.DockerOK {
		return out
	}
	st := snap.actualStatus(in.ServerID)
	out.State = containerState(st, desired)
	out.RestartRequired = isUp(st.State) && st.Revision < in.Revision
	out.Operation = string(st.Operation)
	out.LastError = st.LastError
	out.StartedAt = st.StartedAt
	out.MemoryBytes = st.MemoryBytes
	out.MemoryLimitBytes = st.MemoryLimitBytes
	out.CPUPercent = st.CPUPercent
	return out
}

func containerState(st agentapi.InstanceStatus, desired string) string {
	switch {
	case st.Operation == agentapi.OpPulling || st.Operation == agentapi.OpCreating || st.State == agentapi.StateMissing:
		return stateCreating
	case st.Operation == agentapi.OpStarting:
		return stateStarting
	case st.Operation == agentapi.OpStopping || st.Operation == agentapi.OpRemoving:
		return stateStopping
	case st.State == agentapi.StateRestarting:
		return stateRestarting
	case st.State == agentapi.StateRunning:
		return stateRunning
	case st.State == agentapi.StateExited || st.State == agentapi.StateCreated:
		if desired == desiredRunning && (st.ExitCode != 0 || st.LastError != "") {
			return stateError
		}
		return stateStopped
	default:
		return stateUnknown
	}
}
```

`internal/servers/view.go`:

```go
package servers

import (
	"context"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// propertiesJSON is ServerProperties with every field set (defaults resolved, spec §7).
type propertiesJSON struct {
	MOTD               string   `json:"motd"`
	Difficulty         string   `json:"difficulty"`
	Gamemode           string   `json:"gamemode"`
	MaxPlayers         int      `json:"max_players"`
	PVP                bool     `json:"pvp"`
	Hardcore           bool     `json:"hardcore"`
	AllowFlight        bool     `json:"allow_flight"`
	EnableCommandBlock bool     `json:"enable_command_block"`
	OnlineMode         bool     `json:"online_mode"`
	WhitelistEnabled   bool     `json:"whitelist_enabled"`
	Whitelist          []string `json:"whitelist"`
	Ops                []string `json:"ops"`
	ViewDistance       int      `json:"view_distance"`
	SimulationDistance int      `json:"simulation_distance"`
	SpawnProtection    int      `json:"spawn_protection"`
	Seed               string   `json:"seed"`
	LevelType          string   `json:"level_type"`
}

func resolvedProperties(name string, p instancespec.Properties) propertiesJSON {
	r := instancespec.Resolve(name, p)
	return propertiesJSON{
		MOTD: *r.MOTD, Difficulty: *r.Difficulty, Gamemode: *r.Gamemode, MaxPlayers: *r.MaxPlayers,
		PVP: *r.PVP, Hardcore: *r.Hardcore, AllowFlight: *r.AllowFlight, EnableCommandBlock: *r.EnableCommandBlock,
		OnlineMode: *r.OnlineMode, WhitelistEnabled: *r.WhitelistEnabled, Whitelist: r.Whitelist, Ops: r.Ops,
		ViewDistance: *r.ViewDistance, SimulationDistance: *r.SimulationDistance, SpawnProtection: *r.SpawnProtection,
		Seed: *r.Seed, LevelType: *r.LevelType,
	}
}

// instanceJSON is the OpenAPI schema InstanceConfig.
type instanceJSON struct {
	Type          instancespec.Type `json:"type"`
	Version       string            `json:"version"`
	LoaderVersion *string           `json:"loader_version"`
	Modpack       *string           `json:"modpack"`
	Java          instancespec.Java `json:"java"`
	MemoryMiB     int               `json:"memory_mib"`
	Properties    propertiesJSON    `json:"properties"`
}

// serverJSON is the OpenAPI schema Server; permissions are the caller's effective permissions.
type serverJSON struct {
	ID          int64             `json:"id"`
	Name        string            `json:"name"`
	Permissions []rbac.Permission `json:"permissions"`
	Address     *string           `json:"address"`
	Instance    *instanceJSON     `json:"instance"`
	Status      statusJSON        `json:"status"`
}

// viewData is everything a Server response needs besides the server row.
type viewData struct {
	baseDomain *string
	instances  map[int64]store.Instance
	snap       Snapshot
}

func (h *Handler) loadViewData(ctx context.Context) (viewData, error) {
	set, err := h.q.ServersGetSettings(ctx)
	if err != nil {
		return viewData{}, err
	}
	rows, err := h.q.ServersListInstances(ctx)
	if err != nil {
		return viewData{}, err
	}
	d := viewData{baseDomain: nullString(set.BaseDomain), instances: make(map[int64]store.Instance, len(rows)), snap: h.snapshot()}
	for _, r := range rows {
		d.instances[r.ServerID] = instanceFromListRow(r)
	}
	return d, nil
}

func (d viewData) server(s store.Server, perms []rbac.Permission) (serverJSON, error) {
	var in *store.Instance
	if row, ok := d.instances[s.ID]; ok {
		in = &row
	}
	return view(s, perms, in, d.baseDomain, d.snap)
}

// view builds the Server schema; in == nil is a server without instance ("not configured",
// all instance fields null).
func view(s store.Server, perms []rbac.Permission, in *store.Instance, baseDomain *string, snap Snapshot) (serverJSON, error) {
	out := serverJSON{ID: s.ID, Name: s.Name, Permissions: perms, Status: deriveStatus(in, snap)}
	if in == nil {
		return out, nil
	}
	c, err := configFromInstance(*in)
	if err != nil {
		return serverJSON{}, err
	}
	if baseDomain != nil {
		addr := s.Name + "." + *baseDomain
		out.Address = &addr
	}
	out.Instance = &instanceJSON{
		Type: c.Type, Version: c.Version, LoaderVersion: c.LoaderVersion, Modpack: c.Modpack,
		Java: c.Java, MemoryMiB: c.MemoryMiB, Properties: resolvedProperties(s.Name, c.Properties),
	}
	return out, nil
}
```

- [ ] **Step 5: Handler – `servers.go` komplett ersetzen**

```go
package servers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

func (h *Handler) handleListServers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, _ := principal.From(ctx)
	d, err := h.loadViewData(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var list []store.Server
	var perms map[int64][]rbac.Permission
	if p.IsAdmin() {
		list, err = h.q.RbacListServers(ctx)
	} else {
		list, err = h.q.RbacListViewableServers(ctx, p.UserID)
		if err == nil {
			perms, err = userPerms(ctx, h.q, p.UserID)
		}
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]serverJSON, 0, len(list))
	for _, s := range list {
		ps := allPerms()
		if !p.IsAdmin() {
			ps = perms[s.ID]
		}
		v, err := d.server(s, ps)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		out = append(out, v)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// loadServer resolves {id}; it writes 404 itself and returns false if the server is missing.
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (store.Server, bool) {
	id, ok := pathID(r)
	if !ok {
		writeNotFound(w, r)
		return store.Server{}, false
	}
	s, err := h.q.RbacGetServer(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r)
		return store.Server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Server{}, false
	}
	return s, true
}

// loadInstance returns the instance of s; it writes 409 itself if the server has none.
func (h *Handler) loadInstance(w http.ResponseWriter, r *http.Request, s store.Server) (store.Instance, bool) {
	in, err := h.q.ServersGetInstance(r.Context(), s.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeConflict(w, r, "server is not configured (no instance)")
		return store.Instance{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Instance{}, false
	}
	return in, true
}

// writeAgentError maps a failed synchronous agent call to the API error.
func writeAgentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeDockerUnavailable, "docker is not available on the host")
	case errors.Is(err, agentapi.ErrBusy):
		w.Header().Set("Retry-After", "5")
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "the agent is busy with this server, try again shortly")
	case errors.Is(err, instancespec.ErrInvalid):
		writeValidation(w, r, err.Error())
	case errors.Is(err, agentapi.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "the agent is not reachable")
	default:
		httpx.Internal(w, r, err)
	}
}

func (h *Handler) handleGetServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	p, _ := principal.From(ctx)
	perms, err := serverPerms(ctx, h.q, p, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	d, err := h.loadViewData(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out, err := d.server(s, perms)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// createServerRequest is the OpenAPI schema CreateServerRequest. Properties are decoded
// separately so that unknown keys become validation errors.
type createServerRequest struct {
	Name          string             `json:"name"`
	Type          instancespec.Type  `json:"type"`
	Version       string             `json:"version"`
	LoaderVersion *string            `json:"loader_version"`
	Modpack       *string            `json:"modpack"`
	Java          *instancespec.Java `json:"java"`
	MemoryMiB     int                `json:"memory_mib"`
	Properties    json.RawMessage    `json:"properties"`
	EulaAccepted  bool               `json:"eula_accepted"`
}

func (h *Handler) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req createServerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	set, err := h.loadSettings(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if set.BaseDomain == nil {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeBaseDomainMissing, "set a base domain in the server settings first")
		return
	}
	if !instancespec.ValidName(req.Name) {
		writeValidation(w, r, "name must match ^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$")
		return
	}
	if !req.EulaAccepted {
		writeValidation(w, r, "eula_accepted must be true (https://aka.ms/MinecraftEULA)")
		return
	}
	props, err := decodeProperties(req.Properties)
	if err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	c := config{
		Type: req.Type, Version: req.Version, LoaderVersion: req.LoaderVersion, Modpack: req.Modpack,
		Java: instancespec.JavaFor(req.Type, req.Version), MemoryMiB: req.MemoryMiB, Properties: props,
	}
	if req.Java != nil {
		c.Java = *req.Java
	}
	if err := c.validate(set.CurseForgeKey); err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	propsJSON, err := c.propertiesJSON()
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now().UnixMilli()
	var (
		s  store.Server
		in store.Instance
	)
	// Server and instance row are written together: a taken name leaves no half instance.
	err = h.inTx(ctx, func(q *store.Queries) error {
		var err error
		if s, err = q.RbacCreateServer(ctx, store.RbacCreateServerParams{Name: req.Name, CreatedAt: now}); err != nil {
			return err
		}
		in, err = q.ServersCreateInstance(ctx, store.ServersCreateInstanceParams{
			ServerID: s.ID, Type: string(c.Type), Version: c.Version, LoaderVersion: toNullString(c.LoaderVersion),
			Modpack: toNullString(c.Modpack), Java: string(c.Java), MemoryMib: int64(c.MemoryMiB),
			Properties: propsJSON, CreatedAt: now, UpdatedAt: now,
		})
		return err
	})
	if store.IsUniqueViolation(err) {
		writeConflict(w, r, "server name already taken")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionServerCreate, serverTarget(s.ID), audit.ResultSuccess, c.auditDetails(s.Name))
	h.Trigger()
	out, err := view(s, allPerms(), &in, set.BaseDomain, h.snapshot())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

// updateServerRequest is the OpenAPI schema UpdateServerRequest: absent fields stay,
// properties replaces the whole object. Only loader_version and modpack may be null.
type updateServerRequest struct {
	Version       optional[string]            `json:"version"`
	LoaderVersion optional[string]            `json:"loader_version"`
	Modpack       optional[string]            `json:"modpack"`
	Java          optional[instancespec.Java] `json:"java"`
	MemoryMiB     optional[int]               `json:"memory_mib"`
	Properties    optional[json.RawMessage]   `json:"properties"`
}

var errNotNullable = errors.New("must not be null")

// apply merges the request into c.
func (req updateServerRequest) apply(c config) (config, error) {
	if req.Version.Set {
		if req.Version.Value == nil {
			return c, fmt.Errorf("version: %w", errNotNullable)
		}
		c.Version = *req.Version.Value
	}
	if req.LoaderVersion.Set {
		c.LoaderVersion = req.LoaderVersion.Value
	}
	if req.Modpack.Set {
		c.Modpack = req.Modpack.Value
	}
	if req.Java.Set {
		if req.Java.Value == nil {
			return c, fmt.Errorf("java: %w", errNotNullable)
		}
		c.Java = *req.Java.Value
	}
	if req.MemoryMiB.Set {
		if req.MemoryMiB.Value == nil {
			return c, fmt.Errorf("memory_mib: %w", errNotNullable)
		}
		c.MemoryMiB = *req.MemoryMiB.Value
	}
	if req.Properties.Set {
		if req.Properties.Value == nil {
			return c, fmt.Errorf("properties: %w", errNotNullable)
		}
		p, err := decodeProperties(*req.Properties.Value)
		if err != nil {
			return c, err
		}
		c.Properties = p
	}
	return c, nil
}

func (h *Handler) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req updateServerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	in, ok := h.loadInstance(w, r, s)
	if !ok {
		return
	}
	cur, err := configFromInstance(in)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	next, err := req.apply(cur)
	if err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	set, err := h.loadSettings(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := next.validate(set.CurseForgeKey); err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	changes, err := diff(cur, next)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if len(changes) > 0 {
		propsJSON, err := next.propertiesJSON()
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		in, err = h.q.ServersUpdateInstance(ctx, store.ServersUpdateInstanceParams{
			Version: next.Version, LoaderVersion: toNullString(next.LoaderVersion), Modpack: toNullString(next.Modpack),
			Java: string(next.Java), MemoryMib: int64(next.MemoryMiB), Properties: propsJSON,
			UpdatedAt: h.clock.Now().UnixMilli(), ServerID: s.ID,
		})
		if errors.Is(err, sql.ErrNoRows) { // deleted concurrently
			writeNotFound(w, r)
			return
		}
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		h.record(ctx, audit.ActionServerUpdate, serverTarget(s.ID), audit.ResultSuccess,
			map[string]any{"name": s.Name, "revision": in.Revision, "changes": changes})
		h.Trigger()
	}
	out, err := view(s, allPerms(), &in, set.BaseDomain, h.snapshot())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// handleDeleteServer removes the container first (data goes to deleted/ on the host), then
// the rows. Without the agent a configured server cannot be deleted, so no container is
// orphaned; a server without instance needs no agent.
func (h *Handler) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	_, err := h.q.ServersGetInstance(ctx, s.ID)
	configured := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		httpx.Internal(w, r, err)
		return
	}
	if configured {
		if err := h.inst.Remove(ctx, s.ID); err != nil {
			writeAgentError(w, r, err)
			return
		}
	}
	n, err := h.q.RbacDeleteServer(ctx, s.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 { // deleted concurrently between lookup and delete
		writeNotFound(w, r)
		return
	}
	h.record(ctx, audit.ActionServerDelete, serverTarget(s.ID), audit.ResultSuccess,
		map[string]any{"name": s.Name, "configured": configured})
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 6: `handler.go` und `settings.go` anpassen**

`internal/servers/handler.go`:
- Das `Handler`-Struct wird zu
```go
type Handler struct {
	db     *sql.DB
	q      *store.Queries
	clock  clock.Clock
	rec    audit.Recorder
	box    *secretbox.Box
	inst   agentapi.InstancesClient
	power  *powerLimiter
	kick   chan struct{} // Trigger -> reconcile loop (buffer 1, never blocks)
	status statusCache   // last actual state reported by the agent
}
```
- In `Routes` nach der `GET /servers/{id}`-Zeile:
```go
	api.Patch("/servers/{id}", rbac.Admin().WithSudo(), h.handleUpdateServer)
```

`internal/servers/settings.go`:
- Import `"fmt"` ergänzen (nach `"errors"`).
- In `settingsBody` die Zeile `resp, err := h.inst.Instances(ctx)` ersetzen durch `resp, err := h.refreshStatus(ctx)` (die Einstellungsseite aktualisiert damit auch den Status-Cache).
- Vor `func (h *Handler) handleGetSettings(` einfügen:
```go
// instanceSettings are the runtime settings with the CurseForge key decrypted.
type instanceSettings struct {
	BaseDomain    *string
	CurseForgeKey *string
}

func (h *Handler) loadSettings(ctx context.Context) (instanceSettings, error) {
	row, err := h.q.ServersGetSettings(ctx)
	if err != nil {
		return instanceSettings{}, err
	}
	out := instanceSettings{BaseDomain: nullString(row.BaseDomain)}
	if len(row.CurseforgeApiKey) > 0 {
		pt, err := h.box.Open(row.CurseforgeApiKey, []byte(curseForgeKeyAAD))
		if err != nil {
			return instanceSettings{}, fmt.Errorf("servers: open curseforge key: %w", err)
		}
		key := string(pt)
		out.CurseForgeKey = &key
	}
	return out, nil
}

```

- [ ] **Step 7: Go-Tests laufen lassen (grün)**

Run: `gofmt -l internal/ && go test ./internal/servers/ -count=1 && golangci-lint run ./internal/servers/...`
Expected: keine gofmt-Ausgabe, PASS, `0 issues`.

- [ ] **Step 8: OpenAPI-Vertrag**

`api/openapi.yaml`:

1. Unter `components.responses` nach `TooManyRequests`:
```yaml
    ServiceUnavailable: { description: 'Agent or Docker not available (agent_unavailable, docker_unavailable)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```
2. In `/servers` → `post` (`createServer`) die `409`-Zeile ersetzen durch
```yaml
        '409': { description: 'conflict (name taken) or base_domain_missing', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```
3. In `/servers/{id}` zwischen `get` und `delete` einfügen und `delete` um 429/503 ergänzen (der ganze `delete`-Block danach):
```yaml
    patch:
      operationId: updateServer
      tags: [servers]
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/UpdateServerRequest' } } }
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/Server' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '409': { $ref: '#/components/responses/Conflict' }
    delete:
      operationId: deleteServer
      tags: [servers]
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      responses:
        '204': { description: No content }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
        '429': { $ref: '#/components/responses/TooManyRequests' }
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
```
4. Die Schemas `Server` und `CreateServerRequest` ersetzen durch (wörtlich V4, inkl. der neuen Hilfsschemas davor):
```yaml
    InstanceType: { type: string, enum: [vanilla, paper, purpur, fabric, neoforge, modrinth, curseforge] }
    JavaVersion: { type: string, enum: [java8, java17, java21, java25] }
    ServerState: { type: string, enum: [not_configured, unknown, creating, starting, running, stopping, stopped, restarting, error] }
    ServerProperties:
      type: object
      additionalProperties: false
      properties:
        motd: { type: string, maxLength: 150 }
        difficulty: { type: string, enum: [peaceful, easy, normal, hard] }
        gamemode: { type: string, enum: [survival, creative, adventure, spectator] }
        max_players: { type: integer, minimum: 1, maximum: 500 }
        pvp: { type: boolean }
        hardcore: { type: boolean }
        allow_flight: { type: boolean }
        enable_command_block: { type: boolean }
        online_mode: { type: boolean }
        whitelist_enabled: { type: boolean }
        whitelist: { type: array, maxItems: 200, items: { type: string, pattern: '^[A-Za-z0-9_]{3,16}$' } }
        ops: { type: array, maxItems: 50, items: { type: string, pattern: '^[A-Za-z0-9_]{3,16}$' } }
        view_distance: { type: integer, minimum: 3, maximum: 32 }
        simulation_distance: { type: integer, minimum: 3, maximum: 32 }
        spawn_protection: { type: integer, minimum: 0, maximum: 256 }
        seed: { type: string, maxLength: 64 }
        level_type: { type: string, enum: [normal, flat, large_biomes, amplified] }
    InstanceConfig:
      type: object
      required: [type, version, loader_version, modpack, java, memory_mib, properties]
      properties:
        type: { $ref: '#/components/schemas/InstanceType' }
        version: { type: string, description: "'LATEST', a version, or '' for modpacks" }
        loader_version: { type: [string, 'null'] }
        modpack: { type: [string, 'null'] }
        java: { $ref: '#/components/schemas/JavaVersion' }
        memory_mib: { type: integer, minimum: 1024, maximum: 32768, multipleOf: 256 }
        properties: { $ref: '#/components/schemas/ServerProperties', description: All fields set (defaults resolved) }
    ServerStatus:
      type: object
      required: [state, desired_state, restart_required, operation, last_error, started_at, memory_bytes, memory_limit_bytes, cpu_percent]
      properties:
        state: { $ref: '#/components/schemas/ServerState' }
        desired_state: { type: [string, 'null'], enum: [running, stopped, null] }
        restart_required: { type: boolean }
        operation: { type: string, enum: ['', pulling, creating, starting, stopping, removing] }
        last_error: { type: string }
        started_at: { type: integer, format: int64, description: Unix ms; 0 = not running }
        memory_bytes: { type: integer, format: int64 }
        memory_limit_bytes: { type: integer, format: int64 }
        cpu_percent: { type: number }
    Server:
      type: object
      required: [id, name, permissions, address, instance, status]
      properties:
        id: { type: integer, format: int64 }
        name: { type: string }
        permissions: { type: array, items: { $ref: '#/components/schemas/Permission' }, description: Effective permissions of the caller (admins get all) }
        address: { type: [string, 'null'], description: '<name>.<base_domain>; null without base domain' }
        instance: { anyOf: [{ $ref: '#/components/schemas/InstanceConfig' }, { type: 'null' }] }
        status: { $ref: '#/components/schemas/ServerStatus' }
    CreateServerRequest:
      type: object
      additionalProperties: false
      required: [name, type, version, memory_mib, eula_accepted]
      properties:
        name: { type: string, pattern: '^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$' }
        type: { $ref: '#/components/schemas/InstanceType' }
        version: { type: string }
        loader_version: { type: [string, 'null'] }
        modpack: { type: [string, 'null'] }
        java: { $ref: '#/components/schemas/JavaVersion', description: Derived from type/version if omitted }
        memory_mib: { type: integer, minimum: 1024, maximum: 32768, multipleOf: 256 }
        properties: { $ref: '#/components/schemas/ServerProperties' }
        eula_accepted: { type: boolean, const: true }
    UpdateServerRequest:
      type: object
      additionalProperties: false
      description: Omitted fields stay unchanged; properties replaces the whole object.
      properties:
        version: { type: string }
        loader_version: { type: [string, 'null'] }
        modpack: { type: [string, 'null'] }
        java: { $ref: '#/components/schemas/JavaVersion' }
        memory_mib: { type: integer, minimum: 1024, maximum: 32768, multipleOf: 256 }
        properties: { $ref: '#/components/schemas/ServerProperties' }
```

Run: `make generate`
Expected: `schema.d.ts` enthält `updateServer`, `Server.instance: components["schemas"]["InstanceConfig"] | null`, `ServerStatus.desired_state: "running" | "stopped" | null` und `CreateServerRequest.eula_accepted: true`.

- [ ] **Step 9: Integrationstest in `cmd/panel` anpassen**

`cmd/panel/integration_test.go`:
- `TestRouteCoverage`: `59` → `60`.
- In `TestFullFlowPermissionsAndAudit` den Block ab `// sudo required for admin mutations` bis einschließlich `b := admin.must(…)` ersetzen durch
```go
	// sudo required for admin mutations
	if c, out := admin.do("POST", "/servers", paperServer("survival")); c != 403 || out["error"] != "sudo_required" {
		t.Fatalf("POST /servers without sudo = %d %v", c, out)
	}
	sudo(e, admin, secret)
	if c, out := admin.do("POST", "/servers", paperServer("survival")); c != 409 || out["error"] != "base_domain_missing" {
		t.Fatalf("POST /servers without base domain = %d %v", c, out)
	}
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	a := admin.must(201, "POST", "/servers", paperServer("survival"))
	b := admin.must(201, "POST", "/servers", paperServer("creative"))
```
- In derselben Funktion die erwarteten Audit-Aktionen:
```go
	for _, want := range []string{audit.ActionTOTPEnable, audit.ActionSudo, audit.ActionServerSettingsUpdate,
		audit.ActionServerCreate, audit.ActionUserCreate, audit.ActionPermissionsSet, audit.ActionServerPower} {
```
- Vor `func (c *client) doList(`:
```go
// paperServer is a valid CreateServerRequest.
func paperServer(name string) map[string]any {
	return map[string]any{"name": name, "type": "paper", "version": "1.21.11", "memory_mib": 4096, "eula_accepted": true}
}

```

Run: `go test ./cmd/panel/ -count=1`
Expected: PASS (60 Routen, OpenAPI ↔ Routen identisch).

- [ ] **Step 10: Typen, MSW-Mock (V5) und Übergangs-Dialog**

`web/src/api/types.ts`, vor `export type ServerSettings = …`:
```ts
export type InstanceType = Schemas['InstanceType']
export type JavaVersion = Schemas['JavaVersion']
export type ServerState = Schemas['ServerState']
export type ServerProperties = Schemas['ServerProperties']
export type InstanceConfig = Schemas['InstanceConfig']
export type ServerStatus = Schemas['ServerStatus']
export type CreateServerRequest = Schemas['CreateServerRequest']
export type UpdateServerRequest = Schemas['UpdateServerRequest']
```

`web/src/mocks/db.ts`:
- In der Typ-Importliste `InstanceConfig` (nach `AuditResult`), `ServerProperties` und `ServerStatus` (nach `SecuritySettings`) ergänzen.
- `interface MockServer` ersetzen durch (inkl. Hilfsfunktionen; `MockServerSettings` aus Aufgabe 12 bleibt dahinter stehen):
```ts
export interface MockServer {
  id: number
  name: string
  instance: InstanceConfig | null // properties always fully resolved (defaults from spec §7)
  status: ServerStatus
  revision: number // desired revision
  runningRevision: number // revision of the "running container"
}

const MIB = 1024 * 1024
const GIB = 1024 * MIB

/** Every property set: spec §7 defaults (motd = server name) overridden by p. */
export function resolveProperties(name: string, p: ServerProperties = {}): Required<ServerProperties> {
  return {
    motd: name,
    difficulty: 'normal',
    gamemode: 'survival',
    max_players: 20,
    pvp: true,
    hardcore: false,
    allow_flight: false,
    enable_command_block: false,
    online_mode: true,
    whitelist_enabled: false,
    whitelist: [],
    ops: [],
    view_distance: 10,
    simulation_distance: 10,
    spawn_protection: 16,
    seed: '',
    level_type: 'normal',
    ...p,
  }
}

/** Container limit like the agent sets it: memory_mib × 1.25 MiB. */
export function memoryLimitBytes(memoryMib: number): number {
  return memoryMib * 1.25 * MIB
}

export function runningStatus(memoryMib: number, startedAt: number, memoryBytes = Math.round(memoryLimitBytes(memoryMib) * 0.4), cpu = 3): ServerStatus {
  return {
    state: 'running',
    desired_state: 'running',
    restart_required: false,
    operation: '',
    last_error: '',
    started_at: startedAt,
    memory_bytes: memoryBytes,
    memory_limit_bytes: memoryLimitBytes(memoryMib),
    cpu_percent: cpu,
  }
}

export function stoppedStatus(): ServerStatus {
  return {
    state: 'stopped',
    desired_state: 'stopped',
    restart_required: false,
    operation: '',
    last_error: '',
    started_at: 0,
    memory_bytes: 0,
    memory_limit_bytes: 0,
    cpu_percent: 0,
  }
}

export function notConfiguredStatus(): ServerStatus {
  return { ...stoppedStatus(), state: 'not_configured', desired_state: null }
}
```
- Im Seed das `servers`-Array ersetzen durch (V5):
```ts
    servers: [
      {
        id: 1,
        name: 'survival',
        instance: { type: 'paper', version: '1.21.11', loader_version: null, modpack: null, java: 'java21', memory_mib: 4096, properties: resolveProperties('survival') },
        status: runningStatus(4096, now - 2 * HOUR, Math.round(2.1 * GIB), 12.5),
        revision: 1,
        runningRevision: 1,
      },
      {
        id: 2,
        name: 'creative',
        instance: { type: 'vanilla', version: '26.3', loader_version: null, modpack: null, java: 'java25', memory_mib: 2048, properties: resolveProperties('creative', { gamemode: 'creative' }) },
        status: stoppedStatus(),
        revision: 1,
        runningRevision: 1,
      },
      { id: 3, name: 'lobby', instance: null, status: notConfiguredStatus(), revision: 0, runningRevision: 0 },
    ],
```

`web/src/mocks/handlers/servers.ts` komplett ersetzen (Power bleibt hier noch wie in TP1; Aufgabe 14 ergänzt die Übergänge):

```ts
import { http, HttpResponse } from 'msw'
import type {
  CreateServerRequest,
  InstanceType,
  JavaVersion,
  Permission,
  PowerAction,
  Server,
  ServerSettings,
  UpdateServerRequest,
  UpdateServerSettingsRequest,
} from '@/api/types'
import { ALL_PERMISSIONS } from '@/api/types'
import { db, nextId, recordAudit, resolveProperties, runningStatus, type MockServer } from '../db'
import { API, apiError, guard, idParam, noContent, op, readJson, serverPermissions, type MockOperation } from './util'

const SERVER_NAME = /^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$/
const ACTIONS: PowerAction[] = ['start', 'stop', 'restart']
const TYPES: InstanceType[] = ['vanilla', 'paper', 'purpur', 'fabric', 'neoforge', 'modrinth', 'curseforge']
const DNS_LABEL = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/
const CURSEFORGE_KEY = /^[A-Za-z0-9$./_-]{1,128}$/

/** Same rule as instancespec.ValidBaseDomain (after lowercasing). */
function validBaseDomain(d: string): boolean {
  const labels = d.split('.')
  return d.length <= 200 && labels.length >= 2 && labels.every((l) => DNS_LABEL.test(l))
}

function validMemory(m: unknown): m is number {
  return typeof m === 'number' && Number.isInteger(m) && m >= 1024 && m <= 32768 && m % 256 === 0
}

/** Same table as instancespec.JavaFor (spec §6.1). Task 17 moves it to web/src/features/servers/java.ts. */
function mockJavaFor(type: InstanceType, version: string): JavaVersion {
  if (type === 'modrinth' || type === 'curseforge') return 'java21'
  const [major, minor, patch] = version.split('.').map((p) => Number.parseInt(p, 10))
  if (major !== 1 || Number.isNaN(minor)) return 'java25'
  const pt = Number.isNaN(patch) || patch === undefined ? 0 : patch
  if (minor <= 16) return 'java8'
  if (minor <= 19 || (minor === 20 && pt <= 4)) return 'java17'
  if (minor <= 21) return 'java21'
  return 'java25'
}

function settingsBody(): ServerSettings {
  return { ...db.serverSettings, unknown_containers: [] }
}

/** A new container comes up between two reads: "creating" becomes "running" on the next GET. */
function settle(s: MockServer): void {
  if (s.status.state === 'creating' && s.instance) {
    s.status = runningStatus(s.instance.memory_mib, Date.now())
    s.runningRevision = s.revision
  }
}

/** New desired revision: a running server needs a restart, a stopped one is recreated at once. */
function bumpRevision(s: MockServer): void {
  s.revision += 1
  if (s.status.state === 'running') s.status.restart_required = true
  else s.runningRevision = s.revision
}

function toServer(s: MockServer, permissions: Permission[]): Server {
  const base = db.serverSettings.base_domain
  return {
    id: s.id,
    name: s.name,
    permissions,
    address: s.instance && base ? `${s.name}.${base}` : null,
    instance: s.instance,
    status: s.status,
  }
}

export const serverOperations: MockOperation[] = [
  op(
    'listServers',
    http.get(`${API}/servers`, ({ request }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const visible = db.servers
        .map((s) => {
          settle(s)
          return toServer(s, serverPermissions(g.user, s.id))
        })
        .filter((s) => s.permissions.length > 0)
      return HttpResponse.json<Server[]>(visible)
    }),
  ),

  op(
    'createServer',
    http.post(`${API}/servers`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<Partial<CreateServerRequest>>(request)
      if (!body) return apiError(400, 'bad_request', 'invalid JSON body')
      if (!db.serverSettings.base_domain) return apiError(409, 'base_domain_missing', 'set a base domain in the server settings first')
      const name = body.name ?? ''
      if (!SERVER_NAME.test(name)) return apiError(400, 'validation_failed', 'name must match ^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$')
      if (body.eula_accepted !== true) return apiError(400, 'validation_failed', 'eula_accepted must be true')
      if (!body.type || !TYPES.includes(body.type)) return apiError(400, 'validation_failed', 'type: unknown type')
      if (!validMemory(body.memory_mib)) return apiError(400, 'validation_failed', 'memory_mib: must be 1024..32768 in steps of 256')
      if (db.servers.some((s) => s.name === name)) return apiError(409, 'conflict', 'server name already taken')
      const version = body.version ?? ''
      const server: MockServer = {
        id: nextId(),
        name,
        instance: {
          type: body.type,
          version,
          loader_version: body.loader_version ?? null,
          modpack: body.modpack ?? null,
          java: body.java ?? mockJavaFor(body.type, version),
          memory_mib: body.memory_mib,
          properties: resolveProperties(name, body.properties),
        },
        status: { ...runningStatus(body.memory_mib, 0), state: 'creating', operation: 'pulling', memory_bytes: 0, memory_limit_bytes: 0, cpu_percent: 0 },
        revision: 1,
        runningRevision: 1,
      }
      db.servers.push(server)
      recordAudit(g.user, 'server.create', `server:${server.id}`, 'success', { name, type: body.type, version })
      return HttpResponse.json<Server>(toServer(server, [...ALL_PERMISSIONS]), { status: 201 })
    }),
  ),

  // Before getServer: MSW matches in order and /servers/:id would also match /servers/settings.
  op(
    'getServerSettings',
    http.get(`${API}/servers/settings`, ({ request }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      return HttpResponse.json<ServerSettings>(settingsBody())
    }),
  ),

  op(
    'updateServerSettings',
    http.put(`${API}/servers/settings`, async ({ request }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const body = await readJson<UpdateServerSettingsRequest>(request)
      if (!body) return apiError(400, 'bad_request', 'invalid JSON body')
      const details: Record<string, unknown> = {}
      if (body.base_domain !== undefined) {
        const next = body.base_domain === null ? null : body.base_domain.toLowerCase()
        if (next !== null && !validBaseDomain(next)) return apiError(400, 'validation_failed', 'base_domain: invalid DNS name')
        if (next !== db.serverSettings.base_domain) {
          details.base_domain = { old: db.serverSettings.base_domain, new: next }
          db.serverSettings.base_domain = next
          for (const s of db.servers) if (s.instance) bumpRevision(s)
        }
      }
      if (body.curseforge_api_key !== undefined) {
        if (body.curseforge_api_key !== null && !CURSEFORGE_KEY.test(body.curseforge_api_key)) {
          return apiError(400, 'validation_failed', 'curseforge_api_key must match ^[A-Za-z0-9$./_-]{1,128}$')
        }
        db.serverSettings.curseforge_api_key_set = body.curseforge_api_key !== null
        details.curseforge_api_key = body.curseforge_api_key === null ? 'removed' : 'set'
      }
      if (Object.keys(details).length > 0) recordAudit(g.user, 'server.settings_update', 'servers', 'success', details)
      return HttpResponse.json<ServerSettings>(settingsBody())
    }),
  ),

  op(
    'getServer',
    http.get(`${API}/servers/:id`, ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      const permissions = serverPermissions(g.user, id)
      if (!permissions.includes('server.view')) return apiError(403, 'forbidden', 'missing permission server.view')
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      settle(server)
      return HttpResponse.json<Server>(toServer(server, permissions))
    }),
  ),

  op(
    'updateServer',
    http.patch(`${API}/servers/:id`, async ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const server = db.servers.find((s) => s.id === idParam(params))
      if (!server) return apiError(404, 'not_found', 'server not found')
      if (!server.instance) return apiError(409, 'conflict', 'server is not configured (no instance)')
      const body = await readJson<UpdateServerRequest>(request)
      if (!body) return apiError(400, 'bad_request', 'invalid JSON body')
      if (body.memory_mib !== undefined && !validMemory(body.memory_mib)) {
        return apiError(400, 'validation_failed', 'memory_mib: must be 1024..32768 in steps of 256')
      }
      const before = JSON.stringify(server.instance)
      const next = { ...server.instance }
      if (body.version !== undefined) next.version = body.version
      if (body.loader_version !== undefined) next.loader_version = body.loader_version
      if (body.modpack !== undefined) next.modpack = body.modpack
      if (body.java !== undefined) next.java = body.java
      if (body.memory_mib !== undefined) next.memory_mib = body.memory_mib
      if (body.properties !== undefined) next.properties = resolveProperties(server.name, body.properties)
      if (JSON.stringify(next) !== before) {
        server.instance = next
        bumpRevision(server)
        recordAudit(g.user, 'server.update', `server:${server.id}`, 'success', { name: server.name, revision: server.revision })
      }
      return HttpResponse.json<Server>(toServer(server, [...ALL_PERMISSIONS]))
    }),
  ),

  op(
    'deleteServer',
    http.delete(`${API}/servers/:id`, ({ request, params }) => {
      const g = guard(request, { admin: true, sudo: true })
      if (g instanceof Response) return g
      const id = idParam(params)
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      db.servers = db.servers.filter((s) => s.id !== id)
      for (const [userId, grants] of db.grants) db.grants.set(userId, grants.filter((gr) => gr.server_id !== id))
      recordAudit(g.user, 'server.delete', `server:${server.id}`, 'success', { name: server.name })
      return noContent()
    }),
  ),

  op(
    'serverPower',
    http.post(`${API}/servers/:id/power`, async ({ request, params }) => {
      const g = guard(request)
      if (g instanceof Response) return g
      const id = idParam(params)
      if (!serverPermissions(g.user, id).includes('server.power')) return apiError(403, 'forbidden', 'missing permission server.power')
      const server = db.servers.find((s) => s.id === id)
      if (!server) return apiError(404, 'not_found', 'server not found')
      const body = await readJson<{ action?: PowerAction }>(request)
      if (!body?.action || !ACTIONS.includes(body.action)) return apiError(400, 'validation_failed', 'action must be start, stop or restart')
      recordAudit(g.user, 'server.power', `server:${server.id}`, 'success', { action: body.action, name: server.name })
      return HttpResponse.json({ status: 'accepted' as const }, { status: 202 })
    }),
  ),
]
```

`web/src/mocks/handlers.test.ts`: `toHaveLength(59)` → `toHaveLength(60)` und vor `it('buckets the overview series by range', …)`:
```ts
  it('creates a server that is running on the next read', async () => {
    const me = loginAs('admin', { sudo: true })
    const body = { name: 'skyblock', type: 'fabric', version: '1.20.4', memory_mib: 2048, eula_accepted: true }
    const created = await callApi('POST', '/servers', { body, csrf: me.csrf_token })
    expect(created.status).toBe(201)
    expect(created.body).toMatchObject({
      address: 'skyblock.mc.example.com',
      instance: { type: 'fabric', java: 'java17', properties: { motd: 'skyblock', max_players: 20 } },
      status: { state: 'creating', operation: 'pulling', desired_state: 'running' },
    })
    const id = field(created.body, 'id') as number
    expect(field((await callApi('GET', `/servers/${id}`)).body, 'status')).toMatchObject({ state: 'running', operation: '' })
    expect((await callApi('POST', '/servers', { body, csrf: me.csrf_token })).status).toBe(409)
    const noEula = await callApi('POST', '/servers', { body: { ...body, name: 'other', eula_accepted: false }, csrf: me.csrf_token })
    expect(field(noEula.body, 'error')).toBe('validation_failed')
    db.serverSettings.base_domain = null
    const noDomain = await callApi('POST', '/servers', { body: { ...body, name: 'other' }, csrf: me.csrf_token })
    expect(noDomain.status).toBe(409)
    expect(field(noDomain.body, 'error')).toBe('base_domain_missing')
  })

  it('marks a running server as restart_required after an update, a stopped one not', async () => {
    const me = loginAs('admin', { sudo: true })
    const running = await callApi('PATCH', '/servers/1', { body: { memory_mib: 6144 }, csrf: me.csrf_token })
    expect(running.body).toMatchObject({ instance: { memory_mib: 6144 }, status: { state: 'running', restart_required: true } })
    const stopped = await callApi('PATCH', '/servers/2', { body: { properties: { motd: 'Hi' } }, csrf: me.csrf_token })
    expect(stopped.body).toMatchObject({ instance: { properties: { motd: 'Hi', gamemode: 'survival' } }, status: { restart_required: false } })
    expect((await callApi('PATCH', '/servers/3', { body: { memory_mib: 2048 }, csrf: me.csrf_token })).status).toBe(409)
  })

```

`web/src/features/dashboard/create-server-dialog.tsx` (Übergang bis Aufgabe 19; Signatur und Import aus `./servers` bleiben): die `useMutation`-Definition ersetzen durch
```tsx
  // Interim until the full form (/servers/new, task 19): a Paper server, latest version, 4 GiB.
  // The dialog description tells the admin that creating the server accepts the Minecraft EULA.
  const create = useMutation({
    mutationFn: () =>
      unwrap(api.POST('/servers', { body: { name: trimmed, type: 'paper', version: 'LATEST', memory_mib: 4096, eula_accepted: true } })),
    onSuccess: (server) => {
      toast.success(t('dashboard.created', { name: server.name }))
      setName('')
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: serversKey })
    },
  })
```
`web/src/i18n/de.ts`, Block `dashboard`:
```ts
    create_description: 'Legt einen Paper-Server an (neueste Version, 4 GiB RAM). Mit „Anlegen“ akzeptierst du die Minecraft-EULA (aka.ms/MinecraftEULA).',
```
`web/src/i18n/en.ts`, Block `dashboard`:
```ts
    create_description: 'Creates a Paper server (latest version, 4 GiB RAM). By clicking “Create” you accept the Minecraft EULA (aka.ms/MinecraftEULA).',
```
`dashboard-page.test.tsx` und `web/e2e/smoke.spec.ts` bleiben unverändert (der Dialog hat weiterhin nur das Namensfeld; der Mock-Seed hat eine Basisdomain).

- [ ] **Step 11: Gesamtprüfung**

Run: `make test && make lint && (cd web && npm run e2e)`
Expected: Go und Vitest PASS (60 Operationen), `0 issues`, eslint/`tsc -b` ohne Befunde, Playwright gegen den Mock grün (Sudo-Test legt `skyblock` weiter über den Dialog an).

- [ ] **Step 12: Commit**

```bash
git add internal/audit/types.go internal/servers cmd/panel/integration_test.go api/openapi.yaml web/src/api/schema.d.ts \
  web/src/api/types.ts web/src/mocks/db.ts web/src/mocks/handlers/servers.ts web/src/mocks/handlers.test.ts \
  web/src/features/dashboard/create-server-dialog.tsx web/src/i18n/de.ts web/src/i18n/en.ts
git commit -m "feat(servers): create, update and delete servers with instance configuration

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 14: Power-Aktionen und Abgleichsschleife

`start`/`stop` setzen nur `desired_state` (funktioniert ohne agent), `restart` spricht den agent direkt an (`power restart` bei aktueller Revision, sonst `ensure` mit `recreate: true`). `ReconcileOnce` setzt die Tabelle aus §5.3 um (inkl. Verfeinerung, siehe „Entscheidungen“ 2–3), höchstens ein agent-Aufruf pro Instanz und Durchlauf, Backoff bei wiederholter Aktion, Log statt Panik. `Run` treibt die Schleife (Verdrahtung in Aufgabe 16).

**Files:**
- Create: `internal/servers/reconcile.go`
- Modify: `internal/servers/power.go` (komplett ersetzen), `internal/servers/handler.go`, `internal/servers/servers.go`, `cmd/panel/integration_test.go`, `api/openapi.yaml`, `web/src/mocks/handlers/servers.ts`, `web/src/mocks/handlers.test.ts`
- Generated: `web/src/api/schema.d.ts`
- Test: `internal/servers/power_test.go`, `internal/servers/reconcile_test.go`, `internal/servers/helpers_test.go`, `internal/servers/servers_test.go`

**Interfaces:**
- Consumes: Aufgabe 13 (`refreshStatus`, `snapshot`, `actualStatus`, `isUp`, `configFromInstance`, `loadSettings`, `instanceFromListRow`, `loadInstance`, `writeAgentError`), V1 (`ServersSetDesiredState`, `ServersListInstances`), V2 (`FQDN`), V3 (`EnsureRequest`, `PowerAction`, `ErrBusy`)
- Produces:
```go
const ReconcileInterval = 10 * time.Second
type Deps struct { …; Log *slog.Logger } // nil = slog.Default()
func (h *Handler) Run(ctx context.Context)           // blocks until ctx is done
func (h *Handler) ReconcileOnce(ctx context.Context) // one pass, never panics, never returns errors
func ensureRequest(name string, in store.Instance, set instanceSettings, recreate bool) (agentapi.EnsureRequest, error)
```
OpenAPI: `serverPower` bekommt `409` (nicht eingerichtet / `base_domain_missing` bei restart) und `503`; `PowerRequest` eine Beschreibung. Keine neue Operation (weiter 60). MSW: Power-Übergänge nach V5.

- [ ] **Step 1: Failing tests schreiben**

`internal/servers/helpers_test.go`:
- Import `"log/slog"` ergänzen (nach `"io"`).
- `env` bekommt nach `agent  *fakeAgent` das Feld
```go
	logs   *bytes.Buffer // reconcile log output
```
- In `newEnv` die Zeilen `e := &env{…}` und `e.h = servers.NewHandler(…)` ersetzen durch
```go
	e := &env{t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{}, box: box, agent: newFakeAgent(), logs: &bytes.Buffer{}}
	e.h = servers.NewHandler(servers.Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Box: box, Instances: e.agent,
		Log: slog.New(slog.NewTextHandler(e.logs, nil)),
	})
```
- Im `fakeAgent`-Struct die Zeile `calls     []string` ersetzen durch
```go
	calls     []string // accepted calls
	failed    []string // calls answered with an error
```
- `record` ersetzen und zwei Helfer ergänzen:
```go
func (f *fakeAgent) record(call string) error {
	err := f.err
	if err == nil {
		err = f.actionErr
	}
	if err != nil {
		f.failed = append(f.failed, call)
		return err
	}
	f.calls = append(f.calls, call)
	return nil
}

func (f *fakeAgent) failedCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.failed)
}

func (f *fakeAgent) lastEnsure() agentapi.EnsureRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ensures) == 0 {
		return agentapi.EnsureRequest{}
	}
	return f.ensures[len(f.ensures)-1]
}
```

`internal/servers/servers_test.go`:
- `TestServerPowerRateLimitedPerUser` und den Kommentar darüber **löschen** (zieht nach `power_test.go`, dort mit eingerichtetem Server).
- Import `"github.com/Bernotese/mc-panel/internal/principal"` entfernen.
- In `TestServerVisibilityForRestrictedUser` die beiden Stub-Server durch eingerichtete ersetzen (Power braucht jetzt eine Instanz):
```go
	a := createInstance(t, e.db, "alpha")
	b := createInstance(t, e.db, "beta")
```

`internal/servers/power_test.go`:

```go
package servers_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func (e *env) power(id int64, action string, p *principal.Principal) (int, string) {
	e.t.Helper()
	rec := e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", id), map[string]string{"action": action}, p)
	if rec.Code >= 400 {
		return rec.Code, errCode(e.t, rec)
	}
	return rec.Code, ""
}

// Start and stop only store the wish; the reconcile loop talks to the agent. They therefore
// work while the agent is down.
func TestPowerStartStopSetDesiredState(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	id := createInstance(t, e.db, "survival")
	e.agent.setErr(agentapi.ErrUnavailable)
	if code, _ := e.power(id, "stop", &admin); code != http.StatusAccepted {
		t.Fatalf("stop: %d", code)
	}
	if got := instanceRow(t, e.db, id).DesiredState; got != "stopped" {
		t.Fatalf("desired = %s", got)
	}
	if code, _ := e.power(id, "start", &admin); code != http.StatusAccepted {
		t.Fatalf("start: %d", code)
	}
	if got := instanceRow(t, e.db, id).DesiredState; got != "running" {
		t.Fatalf("desired = %s", got)
	}
	if calls := e.agent.failedCalls(); len(calls) != 0 {
		t.Fatalf("start/stop called the agent: %v", calls)
	}
	if got := e.rec.Actions(); !slices.Equal(got, []string{audit.ActionServerPower, audit.ActionServerPower}) {
		t.Fatalf("audit = %v", got)
	}
	stub := storetest.CreateServer(t, e.db, "lobby")
	if code, ec := e.power(stub, "start", &admin); code != http.StatusConflict || ec != "conflict" {
		t.Fatalf("stub: %d %s", code, ec)
	}
}

func TestPowerRestart(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "survival")

	// current revision: plain restart
	e.agent.set(agentapi.InstanceStatus{ID: id, Name: "survival", Revision: 1, State: agentapi.StateRunning})
	if code, ec := e.power(id, "restart", &admin); code != http.StatusAccepted {
		t.Fatalf("restart: %d %s", code, ec)
	}
	if calls := e.agent.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("power %d restart", id)}) {
		t.Fatalf("calls = %v", calls)
	}

	// outdated revision: recreate with the new config
	mustExec(t, e.db, `UPDATE instances SET revision = 2, memory_mib = 6144, desired_state = 'stopped' WHERE server_id = ?`, id)
	if code, ec := e.power(id, "restart", &admin); code != http.StatusAccepted {
		t.Fatalf("restart outdated: %d %s", code, ec)
	}
	if calls := e.agent.takeCalls(); !slices.Equal(calls, []string{fmt.Sprintf("ensure %d r2 recreate=true", id)}) {
		t.Fatalf("calls = %v", calls)
	}
	req := e.agent.lastEnsure()
	if req.FQDN != "survival.mc.example.com" || req.Spec.MemoryMiB != 6144 || !req.Recreate {
		t.Fatalf("ensure = %+v", req)
	}
	if got := instanceRow(t, e.db, id).DesiredState; got != "running" {
		t.Fatalf("restart must set desired running, got %s", got)
	}

	// missing container: the reconcile loop creates and starts it
	e.agent.set(agentapi.InstanceStatus{ID: id, State: agentapi.StateMissing})
	if code, _ := e.power(id, "restart", &admin); code != http.StatusAccepted {
		t.Fatalf("restart missing: %d", code)
	}
	if calls := e.agent.takeCalls(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestPowerRestartNeedsAgent(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "survival")
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, id)
	e.agent.set(agentapi.InstanceStatus{ID: id, Name: "survival", Revision: 1, State: agentapi.StateExited})
	for _, tc := range []struct {
		setup  func()
		status int
		code   string
	}{
		{func() { e.agent.setErr(agentapi.ErrUnavailable) }, http.StatusServiceUnavailable, "agent_unavailable"},
		{func() { e.agent.setErr(nil); e.agent.setDocker(agentapi.DockerUnavailable) }, http.StatusServiceUnavailable, "docker_unavailable"},
		{func() { e.agent.setDocker(agentapi.DockerOK); e.agent.setActionErr(agentapi.ErrBusy) }, http.StatusTooManyRequests, "rate_limited"},
	} {
		tc.setup()
		if code, ec := e.power(id, "restart", &admin); code != tc.status || ec != tc.code {
			t.Errorf("got %d %s, want %d %s", code, ec, tc.status, tc.code)
		}
	}
	if got := instanceRow(t, e.db, id).DesiredState; got != "stopped" {
		t.Fatalf("a failed restart changed the desired state to %s", got)
	}
	if n := len(e.rec.Entries); n != 0 {
		t.Fatalf("failed restarts were audited: %v", e.rec.Actions())
	}
}

// Power actions are limited to 10 per minute per user, so a user cannot flood the audit chain.
func TestServerPowerRateLimitedPerUser(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	a := createInstance(t, e.db, "alpha")
	for i := 0; i < 10; i++ {
		if code, ec := e.power(a, "start", &admin); code != http.StatusAccepted {
			t.Fatalf("request %d: %d %s", i, code, ec)
		}
	}
	rec := e.do(http.MethodPost, fmt.Sprintf("/servers/%d/power", a), map[string]string{"action": "start"}, &admin)
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th request: %d %s retry=%q", rec.Code, rec.Body, rec.Header().Get("Retry-After"))
	}
	if n := len(e.rec.Entries); n != 10 {
		t.Fatalf("audit entries = %d, want 10 (a limited request is not audited)", n)
	}
	// another user has its own budget
	other := principal.Principal{UserID: storetest.CreateUser(t, e.db, "root2", "admin"), Username: "root2",
		Role: principal.RoleAdmin, Stage: principal.StageFull}
	if code, _ := e.power(a, "stop", &other); code != http.StatusAccepted {
		t.Fatalf("other user: %d", code)
	}
	e.clk.Advance(6 * time.Second)
	if code, _ := e.power(a, "start", &admin); code != http.StatusAccepted {
		t.Fatalf("after refill: %d", code)
	}
}
```

`internal/servers/reconcile_test.go`:

```go
package servers_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func (e *env) pass() []string {
	e.h.ReconcileOnce(context.Background())
	return e.agent.takeCalls()
}

// container returns the agent status of instance id in state s with revision rev.
func container(id, rev int64, s agentapi.ContainerState, op agentapi.Operation) agentapi.InstanceStatus {
	return agentapi.InstanceStatus{ID: id, Name: "survival", Revision: rev, State: s, Operation: op}
}

// Every row of the table in spec §5.3 (plus the plan's refinement: a container that is not
// up is recreated before it is started, never started with an old revision).
func TestReconcileTable(t *testing.T) {
	const missing = agentapi.ContainerState("") // not listed by the agent at all
	cases := []struct {
		name     string
		desired  string
		revision int64
		state    agentapi.ContainerState
		crev     int64
		op       agentapi.Operation
		want     string // "" = no call
	}{
		{"running, not listed", "running", 1, missing, 0, "", "ensure 1 r1 recreate=false"},
		{"stopped, not listed", "stopped", 1, missing, 0, "", "ensure 1 r1 recreate=false"},
		{"running, missing", "running", 1, agentapi.StateMissing, 0, "", "ensure 1 r1 recreate=false"},
		{"stopped, outdated exited", "stopped", 2, agentapi.StateExited, 1, "", "ensure 1 r2 recreate=true"},
		{"stopped, outdated created", "stopped", 2, agentapi.StateCreated, 1, "", "ensure 1 r2 recreate=true"},
		{"running, outdated running", "running", 2, agentapi.StateRunning, 1, "", ""},
		{"running, outdated restarting", "running", 2, agentapi.StateRestarting, 1, "", ""},
		{"running, outdated exited", "running", 2, agentapi.StateExited, 1, "", "ensure 1 r2 recreate=true"},
		{"running, exited", "running", 1, agentapi.StateExited, 1, "", "power 1 start"},
		{"running, created", "running", 1, agentapi.StateCreated, 1, "", "power 1 start"},
		{"stopped, running", "stopped", 1, agentapi.StateRunning, 1, "", "power 1 stop"},
		{"stopped, restarting", "stopped", 1, agentapi.StateRestarting, 1, "", "power 1 stop"},
		{"operation pulling", "running", 1, agentapi.StateMissing, 0, agentapi.OpPulling, ""},
		{"operation starting", "running", 1, agentapi.StateCreated, 1, agentapi.OpStarting, ""},
		{"operation stopping", "running", 1, agentapi.StateRunning, 1, agentapi.OpStopping, ""},
		{"running, running", "running", 1, agentapi.StateRunning, 1, "", ""},
		{"stopped, exited", "stopped", 1, agentapi.StateExited, 1, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			setBaseDomain(t, e.db, "mc.example.com")
			id := createInstance(t, e.db, "survival")
			if id != 1 {
				t.Fatalf("id = %d", id)
			}
			mustExec(t, e.db, `UPDATE instances SET desired_state = ?, revision = ? WHERE server_id = ?`, tc.desired, tc.revision, id)
			if tc.state != missing {
				e.agent.set(container(id, tc.crev, tc.state, tc.op))
			}
			var want []string
			if tc.want != "" {
				want = []string{tc.want}
			}
			if got := e.pass(); !slices.Equal(got, want) {
				t.Fatalf("calls = %v, want %v", got, want)
			}
		})
	}
}

func TestReconcileEnsureRequest(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	id := createInstance(t, e.db, "survival")
	mustExec(t, e.db, `UPDATE instances SET properties = '{"motd":"Hallo"}' WHERE server_id = ?`, id)
	e.pass()
	req := e.agent.lastEnsure()
	if req.ID != id || req.Name != "survival" || req.FQDN != "survival.mc.example.com" || req.Revision != 1 || req.Recreate ||
		req.Spec.Type != "paper" || req.Spec.Version != "1.21.11" || req.Spec.MemoryMiB != 4096 ||
		req.Spec.Properties.MOTD == nil || *req.Spec.Properties.MOTD != "Hallo" || req.Spec.CurseForgeAPIKey != nil {
		t.Fatalf("ensure = %+v", req)
	}
}

// Review focus 4: while the agent is down nothing is sent, the state is unknown and power
// wishes are stored; once the agent answers again they are applied.
func TestReconcileAgentOutageAndRecovery(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	admin := e.admin()
	id := createInstance(t, e.db, "survival")
	e.agent.set(container(id, 1, agentapi.StateRunning, ""))
	e.agent.setErr(agentapi.ErrUnavailable)
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("calls while down = %v", calls)
	}
	if st := e.get(t, id).Status.State; st != "unknown" {
		t.Fatalf("state while down = %s", st)
	}
	if code, _ := e.power(id, "stop", &admin); code != http.StatusAccepted {
		t.Fatalf("stop while down: %d", code)
	}
	e.pass()
	if !strings.Contains(e.logs.String(), "reconcile paused") || strings.Count(e.logs.String(), "reconcile paused") != 1 {
		t.Fatalf("outage must be logged once:\n%s", e.logs)
	}
	e.agent.setErr(nil)
	if calls := e.pass(); !slices.Equal(calls, []string{"power 1 stop"}) {
		t.Fatalf("calls after recovery = %v", calls)
	}
	if !strings.Contains(e.logs.String(), "reconcile resumed") {
		t.Fatalf("recovery not logged:\n%s", e.logs)
	}
	e.agent.set(container(id, 1, agentapi.StateExited, ""))
	e.h.ReconcileOnce(context.Background())
	if st := e.get(t, id).Status.State; st != "stopped" {
		t.Fatalf("state after recovery = %s", st)
	}

	// docker unavailable behaves like an outage
	e.agent.setDocker(agentapi.DockerUnavailable)
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'running' WHERE server_id = ?`, id)
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("calls without docker = %v", calls)
	}
}

// Review focus 4: one agent call per instance and pass, and a permanent error leads to an
// exponential backoff instead of a tight loop.
func TestReconcileBacksOff(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	a := createInstance(t, e.db, "alpha")
	b := createInstance(t, e.db, "beta")
	if calls := e.pass(); !slices.Equal(calls, []string{fmt.Sprintf("ensure %d r1 recreate=false", a), fmt.Sprintf("ensure %d r1 recreate=false", b)}) {
		t.Fatalf("first pass = %v", calls)
	}
	// immediate second pass (e.g. triggered): the same action is not repeated yet
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("second pass = %v", calls)
	}
	// the agent keeps failing to create alpha: 10 s, 20 s, 40 s … between attempts, 5 min max
	e.agent.set(agentapi.InstanceStatus{ID: a, Name: "alpha", State: agentapi.StateMissing, LastError: "pull failed"})
	e.agent.set(container(b, 1, agentapi.StateRunning, ""))
	var at []time.Duration
	for elapsed := 10 * time.Second; elapsed <= 10*time.Minute; elapsed += 10 * time.Second {
		e.clk.Advance(10 * time.Second)
		if calls := e.pass(); len(calls) > 0 {
			if len(calls) != 1 || calls[0] != fmt.Sprintf("ensure %d r1 recreate=false", a) {
				t.Fatalf("calls at %s = %v", elapsed, calls)
			}
			at = append(at, elapsed)
		}
	}
	want := []time.Duration{10 * time.Second, 30 * time.Second, 70 * time.Second, 150 * time.Second, 310 * time.Second}
	if !slices.Equal(at, want) {
		t.Fatalf("ensure attempts at %v, want %v", at, want)
	}
	// a new wish starts over: the stop is applied at once
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, a)
	if calls := e.pass(); !slices.Equal(calls, []string{fmt.Sprintf("ensure %d r1 recreate=false", a)}) {
		t.Fatalf("after new desired state = %v", calls)
	}
}

func TestReconcileAbortsWhenAgentFailsMidPass(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	createInstance(t, e.db, "alpha")
	createInstance(t, e.db, "beta")
	e.agent.setActionErr(agentapi.ErrUnavailable)
	e.pass()
	if failed := e.agent.failedCalls(); len(failed) != 1 {
		t.Fatalf("unavailable agent: attempts = %v, want only the first", failed)
	}
	// a busy instance queue only affects that instance
	e.clk.Advance(time.Hour)
	e.agent.setActionErr(agentapi.ErrBusy)
	e.pass()
	if failed := e.agent.failedCalls(); len(failed) != 3 {
		t.Fatalf("busy agent: attempts = %v, want both instances", failed)
	}
}

// A base domain change bumps every revision: stopped servers are recreated with the new
// FQDN, running ones show restart_required and keep running.
func TestReconcileAfterBaseDomainChange(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	up := createInstance(t, e.db, "alpha")
	down := createInstance(t, e.db, "beta")
	mustExec(t, e.db, `UPDATE instances SET desired_state = 'stopped' WHERE server_id = ?`, down)
	e.agent.set(container(up, 1, agentapi.StateRunning, ""))
	e.agent.set(container(down, 1, agentapi.StateExited, ""))
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("steady state = %v", calls)
	}
	e.putSettings(t, `{"base_domain":"play.example.org"}`)
	if calls := e.pass(); !slices.Equal(calls, []string{fmt.Sprintf("ensure %d r2 recreate=true", down)}) {
		t.Fatalf("after base domain change = %v", calls)
	}
	if fqdn := e.agent.lastEnsure().FQDN; fqdn != "beta.play.example.org" {
		t.Fatalf("fqdn = %s", fqdn)
	}
	if st := e.get(t, up).Status; !st.RestartRequired || st.State != "running" {
		t.Fatalf("running server = %+v", st)
	}
}

func TestReconcileLeavesUnknownContainersAlone(t *testing.T) {
	e := newEnv(t)
	setBaseDomain(t, e.db, "mc.example.com")
	e.agent.set(agentapi.InstanceStatus{ID: 99, Name: "old", Revision: 1, State: agentapi.StateRunning})
	e.pass()
	if calls := e.pass(); len(calls) != 0 {
		t.Fatalf("calls = %v", calls)
	}
	if n := strings.Count(e.logs.String(), "container without database entry"); n != 1 {
		t.Fatalf("unknown container logged %d times:\n%s", n, e.logs)
	}
}

func TestReconcileWithoutBaseDomainDoesNotEnsure(t *testing.T) {
	e := newEnv(t)
	a := createInstance(t, e.db, "alpha")
	b := createInstance(t, e.db, "beta")
	e.agent.set(container(b, 1, agentapi.StateExited, ""))
	if calls := e.pass(); !slices.Equal(calls, []string{fmt.Sprintf("power %d start", b)}) {
		t.Fatalf("calls = %v (alpha %d cannot be created without base domain)", calls, a)
	}
	if !strings.Contains(e.logs.String(), "no base domain") {
		t.Fatalf("missing base domain not logged:\n%s", e.logs)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.h.Run(ctx); close(done) }()
	e.h.Trigger()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after cancel")
	}
}
```

- [ ] **Step 2: Tests laufen lassen (rot)**

Run: `go test ./internal/servers/ -count=1`
Expected: FAIL beim Kompilieren (`unknown field Log in struct literal of type servers.Deps`, `e.h.ReconcileOnce undefined`).

- [ ] **Step 3: Implementieren**

`internal/servers/power.go` komplett ersetzen:

```go
package servers

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

type powerRequest struct {
	Action agentapi.PowerAction `json:"action"`
}

var errBaseDomainMissing = errors.New("servers: no base domain configured")

// handleServerPower stores the user's wish (desired_state) and lets the reconcile loop apply
// it, so start and stop work while the agent is down. Restart needs the agent right away.
func (h *Handler) handleServerPower(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req powerRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if !agentapi.ValidPowerAction(req.Action) {
		writeValidation(w, r, "action must be start, stop or restart")
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	in, ok := h.loadInstance(w, r, s)
	if !ok {
		return
	}
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	if d := h.power.take(p.UserID, h.clock.Now()); d > 0 {
		secs := int(math.Ceil(d.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many power actions, try again later")
		return
	}
	if req.Action == agentapi.PowerRestart {
		if err := h.restart(ctx, s, in); err != nil {
			writeAgentError(w, r, err)
			return
		}
	}
	desired := desiredRunning
	if req.Action == agentapi.PowerStop {
		desired = desiredStopped
	}
	n, err := h.q.ServersSetDesiredState(ctx, store.ServersSetDesiredStateParams{
		DesiredState: desired, UpdatedAt: h.clock.Now().UnixMilli(), ServerID: s.ID,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 { // deleted concurrently
		writeNotFound(w, r)
		return
	}
	h.record(ctx, audit.ActionServerPower, serverTarget(s.ID), audit.ResultSuccess,
		map[string]any{"action": string(req.Action), "name": s.Name})
	h.Trigger()
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// restart applies a user's restart (spec §5.3): a container in the current revision is
// restarted, an outdated one is recreated with the new configuration (it starts again
// because it ran before). A missing container is left to the reconcile loop, which creates
// and starts it because the desired state becomes "running".
func (h *Handler) restart(ctx context.Context, s store.Server, in store.Instance) error {
	resp, err := h.refreshStatus(ctx)
	if err != nil {
		return err
	}
	if resp.Docker != agentapi.DockerOK {
		return agentapi.ErrDockerUnavailable
	}
	st := h.snapshot().actualStatus(in.ServerID)
	switch {
	case st.State == agentapi.StateMissing:
		return nil
	case st.Revision >= in.Revision:
		return h.inst.Power(ctx, in.ServerID, agentapi.PowerRestart)
	}
	set, err := h.loadSettings(ctx)
	if err != nil {
		return err
	}
	req, err := ensureRequest(s.Name, in, set, true)
	if err != nil {
		return err
	}
	return h.inst.Ensure(ctx, req)
}

// ensureRequest builds the agent request for the stored instance.
func ensureRequest(name string, in store.Instance, set instanceSettings, recreate bool) (agentapi.EnsureRequest, error) {
	if set.BaseDomain == nil {
		return agentapi.EnsureRequest{}, errBaseDomainMissing
	}
	fqdn, err := instancespec.FQDN(name, *set.BaseDomain)
	if err != nil {
		return agentapi.EnsureRequest{}, err
	}
	c, err := configFromInstance(in)
	if err != nil {
		return agentapi.EnsureRequest{}, err
	}
	if err := c.validate(set.CurseForgeKey); err != nil {
		return agentapi.EnsureRequest{}, err
	}
	return agentapi.EnsureRequest{
		ID: in.ServerID, Name: name, FQDN: fqdn, Revision: in.Revision,
		Spec: c.spec(set.CurseForgeKey), Recreate: recreate,
	}, nil
}
```

`internal/servers/reconcile.go`:

```go
package servers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// ReconcileInterval is the period of the reconcile loop (spec §5.3). Every user action
// triggers an additional pass right away.
const ReconcileInterval = 10 * time.Second

// maxBackoff caps the delay between repeats of an action that does not take effect.
const maxBackoff = 5 * time.Minute

type action int

const (
	actNone     action = iota
	actEnsure          // container missing: create it in the current revision
	actRecreate        // outdated container that does not run: recreate it
	actStart           // power start
	actStop            // power stop
)

func (a action) String() string {
	return [...]string{"none", "ensure", "recreate", "start", "stop"}[a]
}

// decide implements the reconcile table of spec §5.3 for one instance. A container that is
// up with an older revision is left alone (restart_required); one that is not up is
// recreated before it is started, so it never starts with an old configuration.
func decide(desired string, revision int64, st agentapi.InstanceStatus) action {
	switch {
	case st.Operation != agentapi.OpNone:
		return actNone // the agent is working on it
	case st.State == agentapi.StateMissing:
		return actEnsure
	case !isUp(st.State) && st.Revision < revision:
		return actRecreate
	case desired == desiredStopped && isUp(st.State):
		return actStop
	case desired == desiredRunning && (st.State == agentapi.StateExited || st.State == agentapi.StateCreated):
		return actStart
	default:
		return actNone
	}
}

// attempt remembers the last action per instance for the backoff.
type attempt struct {
	act      action
	revision int64
	desired  string
	count    int
	next     time.Time
}

// reconcileState is only touched by ReconcileOnce (under mu).
type reconcileState struct {
	mu        sync.Mutex
	attempts  map[int64]attempt
	warned    map[int64]bool // unknown containers already logged
	agentDown bool
}

func isUnavailable(err error) bool {
	return errors.Is(err, agentapi.ErrUnavailable) || errors.Is(err, agentapi.ErrDockerUnavailable) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// Run runs the reconcile loop until ctx is cancelled: one pass right away, then every
// ReconcileInterval and after every Trigger.
func (h *Handler) Run(ctx context.Context) {
	t := time.NewTicker(ReconcileInterval)
	defer t.Stop()
	for {
		h.ReconcileOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-h.kick:
		}
	}
}

// ReconcileOnce runs one pass: it reads the actual state from the agent and makes at most one
// agent call per instance. Failures are logged, never returned; an unreachable agent or
// Docker ends the pass and the next pass retries.
func (h *Handler) ReconcileOnce(ctx context.Context) {
	rs := &h.reconcile
	rs.mu.Lock()
	defer rs.mu.Unlock()
	resp, err := h.refreshStatus(ctx)
	if err == nil && resp.Docker != agentapi.DockerOK {
		err = agentapi.ErrDockerUnavailable
	}
	if err != nil {
		if !rs.agentDown && ctx.Err() == nil {
			h.log.Warn("servers: reconcile paused, agent or docker unavailable", "err", err)
		}
		rs.agentDown = true
		return
	}
	if rs.agentDown {
		h.log.Info("servers: reconcile resumed, agent reachable again")
		rs.agentDown = false
	}
	rows, err := h.q.ServersListInstances(ctx)
	if err != nil {
		h.log.Error("servers: reconcile: list instances", "err", err)
		return
	}
	set, err := h.loadSettings(ctx)
	if err != nil {
		h.log.Error("servers: reconcile: load settings", "err", err)
		return
	}
	snap := h.snapshot()
	h.warnUnknown(resp, rows)
	now := h.clock.Now()
	seen := make(map[int64]bool, len(rows))
	acted := false
	for _, row := range rows {
		in := instanceFromListRow(row)
		seen[in.ServerID] = true
		act := decide(in.DesiredState, in.Revision, snap.actualStatus(in.ServerID))
		if act == actNone {
			delete(rs.attempts, in.ServerID)
			continue
		}
		if !rs.allow(in, act, now) {
			continue
		}
		err := h.apply(ctx, row.Name, in, act, set)
		switch {
		case err == nil:
			acted = true
		case isUnavailable(err):
			h.log.Warn("servers: reconcile aborted", "server_id", in.ServerID, "action", act.String(), "err", err)
			return
		default:
			h.log.Error("servers: reconcile action failed", "server_id", in.ServerID, "action", act.String(), "err", err)
		}
	}
	for id := range rs.attempts {
		if !seen[id] {
			delete(rs.attempts, id)
		}
	}
	if acted {
		h.Trigger() // look again soon: the agent may already be done
	}
}

// allow applies an exponential backoff to an action that repeats without effect (10 s, 20 s,
// 40 s … at most 5 min), so a permanent last_error never becomes a tight loop. A new
// revision or desired state starts over.
func (rs *reconcileState) allow(in store.Instance, act action, now time.Time) bool {
	a, ok := rs.attempts[in.ServerID]
	same := ok && a.act == act && a.revision == in.Revision && a.desired == in.DesiredState
	if same && now.Before(a.next) {
		return false
	}
	count := 1
	if same {
		count = a.count + 1
	}
	delay := ReconcileInterval << min(count-1, 10)
	if delay > maxBackoff {
		delay = maxBackoff
	}
	rs.attempts[in.ServerID] = attempt{act: act, revision: in.Revision, desired: in.DesiredState, count: count, next: now.Add(delay)}
	return true
}

func (h *Handler) apply(ctx context.Context, name string, in store.Instance, act action, set instanceSettings) error {
	switch act {
	case actEnsure, actRecreate:
		req, err := ensureRequest(name, in, set, act == actRecreate)
		if err != nil {
			return err
		}
		return h.inst.Ensure(ctx, req)
	case actStart:
		return h.inst.Power(ctx, in.ServerID, agentapi.PowerStart)
	case actStop:
		return h.inst.Power(ctx, in.ServerID, agentapi.PowerStop)
	}
	return nil
}

// warnUnknown logs every container with the mcpanel.instance label that the database does
// not know, once per container. Such containers are never touched (spec §5.3).
func (h *Handler) warnUnknown(resp agentapi.InstancesResponse, rows []store.ServersListInstancesRow) {
	known := make(map[int64]bool, len(rows))
	for _, r := range rows {
		known[r.ServerID] = true
	}
	for _, st := range resp.Instances {
		if !known[st.ID] && !h.reconcile.warned[st.ID] {
			h.reconcile.warned[st.ID] = true
			h.log.Warn("servers: container without database entry, leaving it alone", "server_id", st.ID, "name", st.Name)
		}
	}
}
```

`internal/servers/handler.go`: den Abschnitt von `// Deps are the dependencies` bis zum Ende von `func NewHandler` ersetzen durch

```go
// Deps are the dependencies of the server handlers.
type Deps struct {
	DB    *sql.DB // from store.Open (BEGIN IMMEDIATE transactions)
	Clock clock.Clock
	Audit audit.Recorder
	Box   *secretbox.Box // seals the CurseForge API key at rest
	// Instances is the agent's instance API; nil means "no agent" (every call fails with
	// agentapi.ErrUnavailable).
	Instances agentapi.InstancesClient
	Log       *slog.Logger // nil = slog.Default()
}

type Handler struct {
	db     *sql.DB
	q      *store.Queries
	clock  clock.Clock
	rec    audit.Recorder
	box    *secretbox.Box
	inst   agentapi.InstancesClient
	log    *slog.Logger
	power  *powerLimiter
	kick   chan struct{} // Trigger -> reconcile loop (buffer 1, never blocks)
	status statusCache   // last actual state reported by the agent

	reconcile reconcileState
}

func NewHandler(d Deps) *Handler {
	inst := d.Instances
	if inst == nil {
		inst = noAgent{}
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Handler{
		db: d.DB, q: store.New(d.DB), clock: d.Clock, rec: d.Audit, box: d.Box, inst: inst, log: log,
		power: newPowerLimiter(), kick: make(chan struct{}, 1),
		reconcile: reconcileState{attempts: map[int64]attempt{}, warned: map[int64]bool{}},
	}
}
```

`internal/servers/servers.go`, in `writeAgentError` als ersten `case`:
```go
	case errors.Is(err, errBaseDomainMissing):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeBaseDomainMissing, "set a base domain in the server settings first")
```

- [ ] **Step 4: Tests laufen lassen (grün, auch mit Race-Detector)**

Run: `gofmt -l internal/ && go test ./internal/servers/ -count=1 && CGO_ENABLED=1 go test -race -count=1 ./internal/servers/ && golangci-lint run ./internal/servers/...`
Expected: PASS (inkl. aller 17 Zeilen von `TestReconcileTable`), keine Races, `0 issues`.

- [ ] **Step 5: Integrationstest, OpenAPI, MSW**

`cmd/panel/integration_test.go`, `TestFullFlowPermissionsAndAudit`: `restart` braucht jetzt den agent (in `buildApp` erst ab Aufgabe 16 verdrahtet) – die beiden Power-Aufrufe von `maxUser` nutzen `stop`:
```go
	maxUser.must(202, "POST", fmt.Sprintf("/servers/%d/power", srvA), map[string]string{"action": "stop"})
	if c, out := maxUser.do("POST", fmt.Sprintf("/servers/%d/power", srvB), map[string]string{"action": "stop"}); c != 403 || out["error"] != "forbidden" {
```

`api/openapi.yaml`:
- In `/servers/{id}/power` → `post` die Antworten ergänzen (nach `'404'` bzw. `'429'`):
```yaml
        '409': { description: 'conflict (server not configured) or base_domain_missing (restart)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```
```yaml
        '503': { $ref: '#/components/responses/ServiceUnavailable' }
```
- Schema `PowerRequest` bekommt nach `required: [action]`:
```yaml
      description: 'start/stop set the desired state (applied by the reconcile loop, works without agent); restart needs the agent'
```

Run: `make generate`
Expected: nur `schema.d.ts` ändert sich (`serverPower` mit 409/503).

`web/src/mocks/handlers/servers.ts`:
- Import aus `'../db'` um `stoppedStatus` erweitern:
```ts
import { db, nextId, recordAudit, resolveProperties, runningStatus, stoppedStatus, type MockServer } from '../db'
```
- In `serverPower` zwischen der Aktions-Prüfung und `recordAudit(…)` einfügen (V5-Übergänge):
```ts
      if (!server.instance) return apiError(409, 'conflict', 'server is not configured (no instance)')
      settle(server)
      if (body.action === 'stop') {
        server.status = stoppedStatus()
      } else if (body.action === 'start' && server.status.state === 'running') {
        // already running: nothing to do
      } else {
        // start of a stopped server and every restart: up in the current revision
        server.status = runningStatus(server.instance.memory_mib, Date.now())
        server.runningRevision = server.revision
      }
```

`web/src/mocks/handlers.test.ts`, vor `it('buckets the overview series by range', …)`:
```ts
  it('applies power actions to the mock state', async () => {
    const me = loginAs('admin', { sudo: true })
    const power = (id: number, action: string) => callApi('POST', `/servers/${id}/power`, { body: { action }, csrf: me.csrf_token })
    const status = async (id: number) => field((await callApi('GET', `/servers/${id}`)).body, 'status')
    await callApi('PATCH', '/servers/1', { body: { memory_mib: 6144 }, csrf: me.csrf_token })
    expect((await power(1, 'restart')).status).toBe(202)
    expect(await status(1)).toMatchObject({ state: 'running', restart_required: false, memory_limit_bytes: 6144 * 1.25 * 1024 * 1024 })
    await power(1, 'stop')
    expect(await status(1)).toMatchObject({ state: 'stopped', desired_state: 'stopped', started_at: 0 })
    await power(2, 'start')
    expect(await status(2)).toMatchObject({ state: 'running', desired_state: 'running' })
    const stub = await power(3, 'start')
    expect(stub.status).toBe(409)
    expect(field(stub.body, 'error')).toBe('conflict')
  })

```

- [ ] **Step 6: Gesamtprüfung**

Run: `make test && make lint`
Expected: PASS (die Dashboard-Tests „Neu starten“/„Stoppen“ auf `survival` laufen über die neuen Übergänge), `0 issues`.

- [ ] **Step 7: Commit**

```bash
git add internal/servers cmd/panel/integration_test.go api/openapi.yaml web/src/api/schema.d.ts \
  web/src/mocks/handlers/servers.ts web/src/mocks/handlers.test.ts
git commit -m "feat(servers): power actions and reconcile loop with backoff

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 15: Versionskataloge

Paket `internal/servers/catalog` mit festen Upstream-URLs pro Art (Mojang, PaperMC Fill v3, Purpur, Fabric, NeoForged-Maven), 10 s Timeout pro Anfrage, 5-MiB-Limit, 1-h-Cache über `clock.Clock`, User-Agent mit Kontakt; Handler `GET /servers/catalog/{type}`. Upstream-Formate und Beispielantworten sind gegen die Live-Dienste geprüft (2026-10-10) und gekürzt in `testdata/`.

**NeoForge (verbindlich, siehe Global Constraints):** `versions[].id` ist die **Minecraft-Version** (z. B. `26.2`, `1.21.11`), abgeleitet aus allen NeoForge-Releases ohne `-beta`/`-alpha`/`+snapshot` über `catalog.NeoForgeMC` (alt `A.B.C` → `1.A.B`, `B = 0` → `1.A`; neu `Y.D.H.B` mit `Y ≥ 26` → `Y.D.H`, `H = 0` → `Y.D`), dedupliziert, numerisch absteigend; `java` = `instancespec.JavaFor(neoforge, mc)`; `loaders = []`. Das Frontend behandelt NeoForge damit wie jede andere Art: `version = id`, `loader_version = null` (itzg wählt die neueste stabile NeoForge-Version zur MC-Version), außer der Admin trägt unter „Erweitert“ eine NeoForge-Version ein.

**Files:**
- Create: `internal/servers/catalog/catalog.go`, `internal/servers/catalog/sources.go`, `internal/servers/catalog/testdata/{mojang_manifest,paper_versions,paper_builds_stable,paper_builds_none,purpur,fabric_game,fabric_loader,neoforge_versions}.json`, `internal/servers/catalog_api.go`
- Modify: `internal/servers/handler.go`, `internal/servers/helpers_test.go`, `internal/servers/routes_test.go`, `cmd/panel/integration_test.go`, `api/openapi.yaml`, `web/src/api/types.ts`, `web/src/mocks/db.ts`, `web/src/mocks/handlers/servers.ts`, `web/src/mocks/handlers.test.ts`
- Generated: `web/src/api/schema.d.ts`
- Test: `internal/servers/catalog/catalog_test.go`, `internal/servers/catalog_api_test.go`

**Interfaces:**
- Consumes: V2 (`Type`, `Types`, `IsModpack`, `JavaFor`, `Spec.Validate`), `clock.Clock`
- Produces:
```go
package catalog // github.com/Bernotese/mc-panel/internal/servers/catalog
const Timeout = 10 * time.Second; const MaxBytes = 5 << 20; const CacheTTL = time.Hour
type Upstreams struct{ Mojang, Paper, Purpur, Fabric, NeoForge string }
var DefaultUpstreams Upstreams
var ErrUnsupported, ErrUnavailable error
type Version struct { ID string `json:"id"`; Java instancespec.Java `json:"java"` }
type Catalog struct { Versions []Version `json:"versions"`; Loaders []string `json:"loaders"` }
type Options struct { Clock clock.Clock; Version string; Upstreams Upstreams; HTTPClient *http.Client; Timeout time.Duration }
func New(o Options) *Client
func (c *Client) Get(ctx context.Context, t instancespec.Type) (Catalog, error)
func UserAgent(version string) string
func NeoForgeMC(v string) (string, bool)

package servers
type CatalogSource interface { Get(ctx context.Context, t instancespec.Type) (catalog.Catalog, error) }
type Deps struct { …; Catalog CatalogSource } // nil = catalog.New(catalog.Options{Clock: d.Clock})
```
OpenAPI: `GET /servers/catalog/{type}` (`getServerCatalog`, Admin; 200 `Catalog`, 404, 502), Schemas `CatalogVersion`, `Catalog` (V4). Operationen: 61. MSW: `db.catalogs` nach V5.

- [ ] **Step 1: Beispielantworten (gekürzt, echte Formen)**

`internal/servers/catalog/testdata/mojang_manifest.json`:
```json
{
  "latest": { "release": "26.3", "snapshot": "26.4-snapshot-3" },
  "versions": [
    { "id": "26.4-snapshot-3", "type": "snapshot", "url": "https://piston-meta.mojang.com/v1/packages/0d9a2b6f4c1e8a7b3c5d2e9f1a4b6c8d0e2f4a6b/26.4-snapshot-3.json", "time": "2026-10-07T12:31:04+00:00", "releaseTime": "2026-10-07T12:20:13+00:00", "sha1": "0d9a2b6f4c1e8a7b3c5d2e9f1a4b6c8d0e2f4a6b", "complianceLevel": 1 },
    { "id": "26.3", "type": "release", "url": "https://piston-meta.mojang.com/v1/packages/7f3e1c9a5b2d4e6f8a0c1b3d5e7f9a2c4e6b8d0f/26.3.json", "time": "2026-09-16T09:12:44+00:00", "releaseTime": "2026-09-16T09:00:00+00:00", "sha1": "7f3e1c9a5b2d4e6f8a0c1b3d5e7f9a2c4e6b8d0f", "complianceLevel": 1 },
    { "id": "26.3-rc1", "type": "snapshot", "url": "https://piston-meta.mojang.com/v1/packages/1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b/26.3-rc1.json", "time": "2026-09-10T10:01:00+00:00", "releaseTime": "2026-09-10T10:00:00+00:00", "sha1": "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", "complianceLevel": 1 },
    { "id": "26.2", "type": "release", "url": "https://piston-meta.mojang.com/v1/packages/2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c/26.2.json", "time": "2026-06-17T08:45:10+00:00", "releaseTime": "2026-06-17T08:30:00+00:00", "sha1": "2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c", "complianceLevel": 1 },
    { "id": "26.1.2", "type": "release", "url": "https://piston-meta.mojang.com/v1/packages/3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d/26.1.2.json", "time": "2026-04-21T11:02:33+00:00", "releaseTime": "2026-04-21T11:00:00+00:00", "sha1": "3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d", "complianceLevel": 1 },
    { "id": "26.1", "type": "release", "url": "https://piston-meta.mojang.com/v1/packages/4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e/26.1.json", "time": "2026-03-24T13:14:15+00:00", "releaseTime": "2026-03-24T13:00:00+00:00", "sha1": "4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e", "complianceLevel": 1 },
    { "id": "1.21.11", "type": "release", "url": "https://piston-meta.mojang.com/v1/packages/5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f/1.21.11.json", "time": "2025-12-09T12:40:02+00:00", "releaseTime": "2025-12-09T12:30:00+00:00", "sha1": "5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f", "complianceLevel": 1 },
    { "id": "1.20.4", "type": "release", "url": "https://piston-meta.mojang.com/v1/packages/6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a/1.20.4.json", "time": "2023-12-07T12:56:20+00:00", "releaseTime": "2023-12-07T12:56:20+00:00", "sha1": "6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a", "complianceLevel": 1 },
    { "id": "1.16.5", "type": "release", "url": "https://piston-meta.mojang.com/v1/packages/7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b/1.16.5.json", "time": "2021-01-14T16:05:32+00:00", "releaseTime": "2021-01-14T16:05:32+00:00", "sha1": "7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b", "complianceLevel": 0 },
    { "id": "1.8.9", "type": "release", "url": "https://piston-meta.mojang.com/v1/packages/8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c/1.8.9.json", "time": "2015-12-03T09:24:39+00:00", "releaseTime": "2015-12-03T09:24:39+00:00", "sha1": "8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c", "complianceLevel": 0 },
    { "id": "b1.7.3", "type": "old_beta", "url": "https://piston-meta.mojang.com/v1/packages/9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d/b1.7.3.json", "time": "2011-07-08T00:00:00+00:00", "releaseTime": "2011-07-08T00:00:00+00:00", "sha1": "9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d", "complianceLevel": 0 }
  ]
}
```

`internal/servers/catalog/testdata/paper_versions.json` (14 Versionen + ein Pre-Release, damit „nur die neuesten 10 prüfen“ greift):
```json
{
  "versions": [
    {"version": {"id": "26.3", "support": {"status": "SUPPORTED"}, "java": {"version": {"minimum": 25}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [30, 29, 28]},
    {"version": {"id": "26.2", "support": {"status": "SUPPORTED"}, "java": {"version": {"minimum": 25}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [29, 28, 27]},
    {"version": {"id": "26.1.2", "support": {"status": "SUPPORTED"}, "java": {"version": {"minimum": 25}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [28, 27, 26]},
    {"version": {"id": "26.1", "support": {"status": "DEPRECATED"}, "java": {"version": {"minimum": 25}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [27, 26, 25]},
    {"version": {"id": "1.21.11", "support": {"status": "DEPRECATED"}, "java": {"version": {"minimum": 21}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [26, 25, 24]},
    {"version": {"id": "1.21.11-rc3", "support": {"status": "UNSUPPORTED"}, "java": {"version": {"minimum": 21}, "flags": {"recommended": []}}}, "builds": [2, 1]},
    {"version": {"id": "1.21.10", "support": {"status": "DEPRECATED"}, "java": {"version": {"minimum": 21}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [25, 24, 23]},
    {"version": {"id": "1.21.8", "support": {"status": "DEPRECATED"}, "java": {"version": {"minimum": 21}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [24, 23, 22]},
    {"version": {"id": "1.21.4", "support": {"status": "DEPRECATED"}, "java": {"version": {"minimum": 21}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [23, 22, 21]},
    {"version": {"id": "1.21.1", "support": {"status": "DEPRECATED"}, "java": {"version": {"minimum": 21}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [22, 21, 20]},
    {"version": {"id": "1.20.6", "support": {"status": "DEPRECATED"}, "java": {"version": {"minimum": 21}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [21, 20, 19]},
    {"version": {"id": "1.20.4", "support": {"status": "UNSUPPORTED"}, "java": {"version": {"minimum": 17}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [20, 19, 18]},
    {"version": {"id": "1.19.4", "support": {"status": "UNSUPPORTED"}, "java": {"version": {"minimum": 17}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [19, 18, 17]},
    {"version": {"id": "1.16.5", "support": {"status": "UNSUPPORTED"}, "java": {"version": {"minimum": 8}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [18, 17, 16]},
    {"version": {"id": "1.8.8", "support": {"status": "UNSUPPORTED"}, "java": {"version": {"minimum": 8}, "flags": {"recommended": ["-XX:+AlwaysPreTouch", "-XX:+UseG1GC"]}}}, "builds": [17, 16, 15]}
  ]
}
```

`internal/servers/catalog/testdata/paper_builds_stable.json`:
```json
[
  {
    "id": 128,
    "time": "2026-09-30T18:04:11.512Z",
    "channel": "STABLE",
    "commits": [{ "sha": "4f1e2d3c4b5a69788796a5b4c3d2e1f0a9b8c7d6", "time": "2026-09-30T17:55:02Z", "message": "Update upstream" }],
    "downloads": {
      "server:default": {
        "name": "paper-26.2-128.jar",
        "checksums": { "sha256": "9b8f2c7e1d4a6b3c5e8f0a2d4c6e8b0a1c3e5f7a9b1d3f5a7c9e1b3d5f7a9c1e" },
        "size": 51874213,
        "url": "https://fill-data.papermc.io/v1/objects/9b8f2c7e1d4a6b3c5e8f0a2d4c6e8b0a1c3e5f7a9b1d3f5a7c9e1b3d5f7a9c1e/paper-26.2-128.jar"
      }
    }
  }
]
```

`internal/servers/catalog/testdata/paper_builds_none.json` (26.3 hat derzeit keinen STABLE-Build):
```json
[]
```

`internal/servers/catalog/testdata/purpur.json`:
```json
{
  "project": "purpur",
  "metadata": { "current": "26.2" },
  "versions": ["1.14.1", "1.16.5", "1.20.4", "1.21.11", "26.1", "26.1.2", "26.2", "26.3"]
}
```

`internal/servers/catalog/testdata/fabric_game.json`:
```json
[
  { "version": "26.4-snapshot-3", "stable": false },
  { "version": "26.3", "stable": true },
  { "version": "26.3-rc1", "stable": false },
  { "version": "26.2", "stable": true },
  { "version": "1.21.11", "stable": true },
  { "version": "1.20.4", "stable": true },
  { "version": "1.14 Pre-Release 1", "stable": false }
]
```

`internal/servers/catalog/testdata/fabric_loader.json`:
```json
[
  { "separator": ".", "build": 6, "maven": "net.fabricmc:fabric-loader:0.20.0-beta.1", "version": "0.20.0-beta.1", "stable": false },
  { "separator": ".", "build": 5, "maven": "net.fabricmc:fabric-loader:0.19.5", "version": "0.19.5", "stable": true },
  { "separator": ".", "build": 4, "maven": "net.fabricmc:fabric-loader:0.19.4", "version": "0.19.4", "stable": true }
]
```

`internal/servers/catalog/testdata/neoforge_versions.json` (unsortiert wie die echte API):
```json
{
  "isSnapshot": false,
  "versions": [
    "20.2.3-beta",
    "20.4.251",
    "21.0.167",
    "21.1.219",
    "21.4.140",
    "21.11.38",
    "21.11.39-beta",
    "26.1.2.15",
    "26.1.2.16-beta",
    "26.2.0.89",
    "26.2.0.90-beta",
    "26.3.0.64-beta",
    "20.4.250"
  ]
}
```

- [ ] **Step 2: Failing tests schreiben**

`internal/servers/catalog/catalog_test.go`:

```go
package catalog_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// upstream serves the testdata samples under the real paths and records every request.
type upstream struct {
	mu   sync.Mutex
	reqs []*http.Request
	srv  *httptest.Server
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{}
	files := map[string]string{
		"/mc/game/version_manifest_v2.json":                   "mojang_manifest.json",
		"/v3/projects/paper/versions":                         "paper_versions.json",
		"/v2/purpur":                                          "purpur.json",
		"/v2/versions/game":                                   "fabric_game.json",
		"/v2/versions/loader":                                 "fabric_loader.json",
		"/api/maven/versions/releases/net/neoforged/neoforge": "neoforge_versions.json",
	}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.reqs = append(u.reqs, r)
		u.mu.Unlock()
		name, ok := files[r.URL.Path]
		if v, isBuilds := strings.CutPrefix(r.URL.Path, "/v3/projects/paper/versions/"); isBuilds && strings.HasSuffix(v, "/builds") {
			if r.URL.Query().Get("channel") != "STABLE" {
				http.Error(w, "channel missing", http.StatusBadRequest)
				return
			}
			name, ok = "paper_builds_stable.json", true
			if v == "26.3/builds" {
				name = "paper_builds_none.json"
			}
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *upstream) count(prefix string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	n := 0
	for _, r := range u.reqs {
		if strings.HasPrefix(r.URL.Path, prefix) {
			n++
		}
	}
	return n
}

func (u *upstream) client(clk clock.Clock) *catalog.Client {
	url := u.srv.URL
	return catalog.New(catalog.Options{
		Clock: clk, Version: "1.2.3",
		Upstreams: catalog.Upstreams{Mojang: url, Paper: url, Purpur: url, Fabric: url, NeoForge: url},
	})
}

func ids(c catalog.Catalog) []string {
	out := make([]string, len(c.Versions))
	for i, v := range c.Versions {
		out[i] = v.ID + "/" + string(v.Java)
	}
	return out
}

func TestCatalogsFromRealShapedSamples(t *testing.T) {
	u := newUpstream(t)
	c := u.client(clock.NewFake(t0))
	ctx := context.Background()
	cases := []struct {
		typ     instancespec.Type
		want    []string
		loaders []string
	}{
		{instancespec.TypeVanilla, []string{"26.3/java25", "26.2/java25", "26.1.2/java25", "26.1/java25", "1.21.11/java21", "1.20.4/java17", "1.16.5/java8", "1.8.9/java8"}, []string{}},
		// 26.3 has no STABLE build yet; the pre-release 1.21.11-rc3 is skipped.
		{instancespec.TypePaper, []string{"26.2/java25", "26.1.2/java25", "26.1/java25", "1.21.11/java21", "1.21.10/java21", "1.21.8/java21", "1.21.4/java21", "1.21.1/java21", "1.20.6/java21", "1.20.4/java17", "1.19.4/java17", "1.16.5/java8", "1.8.8/java8"}, []string{}},
		// up to metadata.current (26.2), newest first
		{instancespec.TypePurpur, []string{"26.2/java25", "26.1.2/java25", "26.1/java25", "1.21.11/java21", "1.20.4/java17", "1.16.5/java8", "1.14.1/java8"}, []string{}},
		{instancespec.TypeFabric, []string{"26.3/java25", "26.2/java25", "1.21.11/java21", "1.20.4/java17"}, []string{"0.19.5", "0.19.4"}},
		// Minecraft versions with at least one NeoForge release; 26.3 only has betas
		{instancespec.TypeNeoForge, []string{"26.2/java25", "26.1.2/java25", "1.21.11/java21", "1.21.4/java21", "1.21.1/java21", "1.21/java21", "1.20.4/java17"}, []string{}},
	}
	for _, tc := range cases {
		got, err := c.Get(ctx, tc.typ)
		if err != nil {
			t.Fatalf("%s: %v", tc.typ, err)
		}
		if !slices.Equal(ids(got), tc.want) {
			t.Errorf("%s versions =\n%v\nwant\n%v", tc.typ, ids(got), tc.want)
		}
		if !slices.Equal(got.Loaders, tc.loaders) {
			t.Errorf("%s loaders = %v, want %v", tc.typ, got.Loaders, tc.loaders)
		}
	}
	// Paper: only the newest 10 versions are checked for a STABLE build.
	if n := u.count("/v3/projects/paper/versions/"); n != 10 {
		t.Errorf("paper build checks = %d, want 10", n)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, r := range u.reqs {
		if ua := r.Header.Get("User-Agent"); ua != "mc-panel/1.2.3 (https://github.com/Bernotese/mc-panel)" {
			t.Fatalf("%s: User-Agent %q", r.URL.Path, ua)
		}
	}
}

func TestCatalogCacheAndUnsupportedTypes(t *testing.T) {
	u := newUpstream(t)
	clk := clock.NewFake(t0)
	c := u.client(clk)
	ctx := context.Background()
	for range 3 {
		if _, err := c.Get(ctx, instancespec.TypeVanilla); err != nil {
			t.Fatal(err)
		}
	}
	if n := u.count("/mc/"); n != 1 {
		t.Fatalf("cached catalog fetched %d times", n)
	}
	clk.Advance(time.Hour)
	if _, err := c.Get(ctx, instancespec.TypeVanilla); err != nil || u.count("/mc/") != 2 {
		t.Fatalf("catalog not refreshed after 1 h: %v, fetches %d", err, u.count("/mc/"))
	}
	for _, typ := range []instancespec.Type{instancespec.TypeModrinth, instancespec.TypeCurseForge, "bukkit"} {
		if _, err := c.Get(ctx, typ); !errors.Is(err, catalog.ErrUnsupported) {
			t.Errorf("%s: %v, want ErrUnsupported", typ, err)
		}
	}
}

// Review focus 5: garbage from upstream never reaches the form.
func TestCatalogRejectsGarbage(t *testing.T) {
	type answer struct {
		body   string
		status int
		delay  time.Duration
	}
	var (
		mu  sync.Mutex
		cur answer
	)
	set := func(a answer) { mu.Lock(); cur = a; mu.Unlock() }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		a := cur
		mu.Unlock()
		time.Sleep(a.delay)
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(srv.Close)
	c := catalog.New(catalog.Options{
		Clock: clock.NewFake(t0), Timeout: 200 * time.Millisecond,
		Upstreams: catalog.Upstreams{Mojang: srv.URL, Paper: srv.URL, Purpur: srv.URL, Fabric: srv.URL, NeoForge: srv.URL},
	})
	ctx := context.Background()
	unavailable := []struct {
		name, body string
		typ        instancespec.Type
		status     int
		delay      time.Duration
	}{
		{"not json", `<html>maintenance</html>`, instancespec.TypeVanilla, 200, 0},
		{"wrong shape", `{"versions":"26.3"}`, instancespec.TypeVanilla, 200, 0},
		{"http 500", `{}`, instancespec.TypeVanilla, 500, 0},
		{"too large", `{"versions":[` + strings.Repeat(`{"id":"1.21.11","type":"release"},`, catalog.MaxBytes/30) + `{}]}`, instancespec.TypeVanilla, 200, 0},
		{"timeout", `{"versions":[{"id":"26.3","type":"release"}]}`, instancespec.TypeVanilla, 200, time.Second},
		{"no usable version", `{"versions":[{"id":"26.3 beta","type":"release"},{"id":"1.21\nX=1","type":"release"}]}`, instancespec.TypeVanilla, 200, 0},
		{"purpur without current", `{"metadata":{"current":"27.1"},"versions":["26.2"]}`, instancespec.TypePurpur, 200, 0},
	}
	for _, tc := range unavailable {
		set(answer{tc.body, tc.status, tc.delay})
		if _, err := c.Get(ctx, tc.typ); !errors.Is(err, catalog.ErrUnavailable) {
			t.Errorf("%s: %v, want ErrUnavailable", tc.name, err)
		}
	}
	// invalid entries are dropped, the rest is served; failures above were not cached
	set(answer{`{"versions":[{"id":"26.3","type":"release"},{"id":"26 .2","type":"release"},{"id":"` + strings.Repeat("9", 70) + `","type":"release"}]}`, http.StatusOK, 0})
	got, err := c.Get(ctx, instancespec.TypeVanilla)
	if err != nil || !slices.Equal(ids(got), []string{"26.3/java25"}) {
		t.Fatalf("got %v, %v", ids(got), err)
	}
}

func TestNeoForgeMC(t *testing.T) {
	for in, want := range map[string]string{
		"20.4.251": "1.20.4", "21.0.167": "1.21", "21.1.219": "1.21.1", "21.11.38": "1.21.11",
		"26.1.2.15": "26.1.2", "26.2.0.89": "26.2",
	} {
		if got, ok := catalog.NeoForgeMC(in); !ok || got != want {
			t.Errorf("NeoForgeMC(%q) = %q, %t, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"21.11.39-beta", "26.3.0.64-beta", "20.2.3-beta", "26.1.0.1+snapshot", "47.1.79", "19.4.1", "26.1.2", "21.4", "", "a.b.c", "21.+4.1", "21.4.99999999999"} {
		if got, ok := catalog.NeoForgeMC(in); ok {
			t.Errorf("NeoForgeMC(%q) = %q, want rejected", in, got)
		}
	}
}
```

`internal/servers/catalog_api_test.go`:

```go
package servers_test

import (
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestGetCatalog(t *testing.T) {
	e := newEnv(t)
	admin := e.admin()
	e.catalog.cat = catalog.Catalog{
		Versions: []catalog.Version{{ID: "26.3", Java: instancespec.Java25}, {ID: "1.21.11", Java: instancespec.Java21}},
		Loaders:  []string{"0.19.5"},
	}
	rec := e.do(http.MethodGet, "/servers/catalog/fabric", nil, &admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("fabric: %d %s", rec.Code, rec.Body)
	}
	got := decode[catalog.Catalog](t, rec)
	if len(got.Versions) != 2 || got.Versions[0].ID != "26.3" || got.Versions[0].Java != "java25" || !slices.Equal(got.Loaders, []string{"0.19.5"}) {
		t.Fatalf("catalog = %+v", got)
	}
	for _, typ := range []string{"modrinth", "curseforge", "bukkit", "..%2F..%2Fetc"} {
		rec := e.do(http.MethodGet, "/servers/catalog/"+typ, nil, &admin)
		if rec.Code != http.StatusNotFound || errCode(t, rec) != "not_found" {
			t.Errorf("%s: %d %s", typ, rec.Code, rec.Body)
		}
	}
	if !slices.Equal(e.catalog.asked, []instancespec.Type{instancespec.TypeFabric}) {
		t.Fatalf("catalog asked for %v", e.catalog.asked)
	}
	e.catalog.err = fmt.Errorf("%w: vanilla: GET …: HTTP 503", catalog.ErrUnavailable)
	rec = e.do(http.MethodGet, "/servers/catalog/vanilla", nil, &admin)
	if rec.Code != http.StatusBadGateway || errCode(t, rec) != "catalog_unavailable" {
		t.Fatalf("upstream down: %d %s", rec.Code, rec.Body)
	}
	bob := userP(storetest.CreateUser(t, e.db, "bob", "user"), "bob")
	if rec := e.do(http.MethodGet, "/servers/catalog/paper", nil, &bob); rec.Code != http.StatusForbidden {
		t.Fatalf("user: %d", rec.Code)
	}
}
```

`internal/servers/helpers_test.go`:
- Imports ergänzen: `"github.com/Bernotese/mc-panel/internal/agent/instancespec"` (nach `agentapi`) und `"github.com/Bernotese/mc-panel/internal/servers/catalog"` (nach `servers`).
- `env` bekommt nach `agent` das Feld `catalog *fakeCatalog`; in `newEnv` werden `e := &env{…}` und `e.h = …` zu
```go
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{}, box: box,
		agent: newFakeAgent(), catalog: &fakeCatalog{}, logs: &bytes.Buffer{},
	}
	e.h = servers.NewHandler(servers.Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Box: box, Instances: e.agent,
		Log: slog.New(slog.NewTextHandler(e.logs, nil)), Catalog: e.catalog,
	})
```
- Am Dateiende:
```go
// fakeCatalog is a servers.CatalogSource with a fixed answer.
type fakeCatalog struct {
	mu    sync.Mutex
	cat   catalog.Catalog
	err   error
	asked []instancespec.Type
}

func (f *fakeCatalog) Get(_ context.Context, t instancespec.Type) (catalog.Catalog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, t)
	return f.cat, f.err
}
```

`internal/servers/routes_test.go`: `wantRoutes` ist jetzt vollständig:
```go
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers":                rbac.Authenticated(),
	"POST /api/servers":               rbac.Admin().WithSudo(),
	"GET /api/servers/catalog/{type}": rbac.Admin(),
	"GET /api/servers/settings":       rbac.Admin(),
	"PUT /api/servers/settings":       rbac.Admin().WithSudo(),
	"GET /api/servers/{id}":           rbac.Server(rbac.PermServerView, "id"),
	"PATCH /api/servers/{id}":         rbac.Admin().WithSudo(),
	"DELETE /api/servers/{id}":        rbac.Admin().WithSudo(),
	"POST /api/servers/{id}/power":    rbac.Server(rbac.PermServerPower, "id"),
}
```

- [ ] **Step 3: Tests laufen lassen (rot)**

Run: `go test ./internal/servers/... -count=1`
Expected: FAIL beim Kompilieren (Paket `internal/servers/catalog` ohne Quelldateien, `unknown field Catalog in struct literal of type servers.Deps`).

- [ ] **Step 4: Katalog-Paket implementieren**

`internal/servers/catalog/catalog.go`:

```go
// Package catalog fetches the Minecraft version lists for the create form (spec §6). Only
// fixed upstream URLs are queried; the instance type selects the source, never the client.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
)

const (
	// Timeout per upstream request.
	Timeout = 10 * time.Second
	// MaxBytes limits every upstream response.
	MaxBytes = 5 << 20
	// CacheTTL is how long a fetched catalog is served from memory.
	CacheTTL = time.Hour
	// paperStableChecks: only the newest Paper versions are asked for a STABLE build; the
	// older ones all have one (verified 2026-10-10).
	paperStableChecks = 10
)

// Upstreams are the base URLs (scheme and host, no trailing slash) of the catalog sources.
type Upstreams struct {
	Mojang, Paper, Purpur, Fabric, NeoForge string
}

// DefaultUpstreams are the production sources, checked against the live services 2026-10-10.
var DefaultUpstreams = Upstreams{
	Mojang:   "https://piston-meta.mojang.com",
	Paper:    "https://fill.papermc.io",
	Purpur:   "https://api.purpurmc.org",
	Fabric:   "https://meta.fabricmc.net",
	NeoForge: "https://maven.neoforged.net",
}

var (
	// ErrUnsupported: the type has no version catalog (modrinth, curseforge: the pack decides).
	ErrUnsupported = errors.New("catalog: no version catalog for this type")
	// ErrUnavailable: the upstream failed (network, status, size, JSON, no usable versions).
	ErrUnavailable = errors.New("catalog: upstream unavailable")
)

// Version is the OpenAPI schema CatalogVersion: a Minecraft version and its derived image.
type Version struct {
	ID   string            `json:"id"`
	Java instancespec.Java `json:"java"`
}

// Catalog is the OpenAPI schema Catalog; versions newest first.
type Catalog struct {
	Versions []Version `json:"versions"`
	Loaders  []string  `json:"loaders"`
}

type Options struct {
	Clock      clock.Clock
	Version    string        // panel version for the User-Agent; "" = "dev"
	Upstreams  Upstreams     // zero value = DefaultUpstreams
	HTTPClient *http.Client  // nil = a client with Timeout
	Timeout    time.Duration // per request; 0 = Timeout
}

type entry struct {
	at  time.Time
	cat Catalog
}

// Client fetches and caches catalogs. It is safe for concurrent use.
type Client struct {
	clock   clock.Clock
	hc      *http.Client
	ua      string
	up      Upstreams
	timeout time.Duration

	mu    sync.Mutex
	cache map[instancespec.Type]entry
}

func New(o Options) *Client {
	c := &Client{clock: o.Clock, hc: o.HTTPClient, ua: UserAgent(o.Version), up: o.Upstreams, timeout: o.Timeout,
		cache: map[instancespec.Type]entry{}}
	if c.up == (Upstreams{}) {
		c.up = DefaultUpstreams
	}
	if c.timeout <= 0 {
		c.timeout = Timeout
	}
	if c.hc == nil {
		c.hc = &http.Client{Timeout: c.timeout}
	}
	return c
}

// UserAgent is sent on every catalog request; the PaperMC Fill API requires a contact.
func UserAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return "mc-panel/" + version + " (https://github.com/Bernotese/mc-panel)"
}

// Get returns the catalog of t, from the cache if it is younger than CacheTTL. Failures are
// not cached.
func (c *Client) Get(ctx context.Context, t instancespec.Type) (Catalog, error) {
	fetch := map[instancespec.Type]func(context.Context) (Catalog, error){
		instancespec.TypeVanilla:  c.vanilla,
		instancespec.TypePaper:    c.paper,
		instancespec.TypePurpur:   c.purpur,
		instancespec.TypeFabric:   c.fabric,
		instancespec.TypeNeoForge: c.neoforge,
	}[t]
	if fetch == nil {
		return Catalog{}, ErrUnsupported
	}
	c.mu.Lock()
	e, ok := c.cache[t]
	c.mu.Unlock()
	if ok && c.clock.Now().Sub(e.at) < CacheTTL {
		return e.cat, nil
	}
	cat, err := fetch(ctx)
	if err != nil {
		return Catalog{}, fmt.Errorf("%w: %s: %w", ErrUnavailable, t, err)
	}
	if len(cat.Versions) == 0 {
		return Catalog{}, fmt.Errorf("%w: %s: no usable versions", ErrUnavailable, t)
	}
	if cat.Loaders == nil {
		cat.Loaders = []string{}
	}
	c.mu.Lock()
	c.cache[t] = entry{at: c.clock.Now(), cat: cat}
	c.mu.Unlock()
	return cat, nil
}

// getJSON fetches url (one of the fixed upstreams) and decodes at most MaxBytes of JSON.
func (c *Client) getJSON(ctx context.Context, url string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	if len(body) > MaxBytes {
		return fmt.Errorf("GET %s: response larger than %d bytes", url, MaxBytes)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	return nil
}
```

`internal/servers/catalog/sources.go`:

```go
package catalog

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// validVersion reports whether instancespec accepts v as a version, so the form never offers
// a value the create request would reject (spaces, control characters, overlong values).
func validVersion(v string) bool {
	if v == "LATEST" {
		return false
	}
	s := instancespec.Spec{Type: instancespec.TypeVanilla, Version: v, Java: instancespec.Java21, MemoryMiB: instancespec.MinMemoryMiB}
	return s.Validate() == nil
}

// versions keeps the valid ids in the given order (first occurrence wins) and derives Java.
func versions(t instancespec.Type, ids []string) []Version {
	out := make([]Version, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if validVersion(id) && !seen[id] {
			seen[id] = true
			out = append(out, Version{ID: id, Java: instancespec.JavaFor(t, id)})
		}
	}
	return out
}

// leadingInt parses the leading digits of s ("4-rc1" -> 4); no digits -> -1.
func leadingInt(s string) int {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, err := strconv.Atoi(s[:end])
	if err != nil {
		return -1
	}
	return n
}

// compareVersions orders dotted versions numerically ("1.21.11" > "1.21.4" > "1.21").
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(pa), len(pb)) {
		if c := cmp.Compare(leadingInt(pa[i]), leadingInt(pb[i])); c != 0 {
			return c
		}
	}
	if c := cmp.Compare(len(pa), len(pb)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func newestFirst(ids []string) []string {
	ids = slices.Clone(ids)
	slices.SortFunc(ids, func(a, b string) int { return compareVersions(b, a) })
	return slices.Compact(ids)
}

// vanilla: releases from the Mojang manifest, in manifest order (newest first).
func (c *Client) vanilla(ctx context.Context) (Catalog, error) {
	var m struct {
		Versions []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"versions"`
	}
	if err := c.getJSON(ctx, c.up.Mojang+"/mc/game/version_manifest_v2.json", &m); err != nil {
		return Catalog{}, err
	}
	var ids []string
	for _, v := range m.Versions {
		if v.Type == "release" {
			ids = append(ids, v.ID)
		}
	}
	return Catalog{Versions: versions(instancespec.TypeVanilla, ids)}, nil
}

// paper: versions with at least one STABLE build (itzg only installs STABLE builds by
// default). Pre-releases ("-rc", "-pre") are skipped; only the newest paperStableChecks
// versions are checked.
func (c *Client) paper(ctx context.Context) (Catalog, error) {
	var resp struct {
		Versions []struct {
			Version struct {
				ID string `json:"id"`
			} `json:"version"`
		} `json:"versions"`
	}
	if err := c.getJSON(ctx, c.up.Paper+"/v3/projects/paper/versions", &resp); err != nil {
		return Catalog{}, err
	}
	var ids []string
	for _, v := range resp.Versions {
		if validVersion(v.Version.ID) && !strings.Contains(v.Version.ID, "-") {
			ids = append(ids, v.Version.ID)
		}
	}
	var keep []string
	for i, id := range newestFirst(ids) {
		if i < paperStableChecks {
			var builds []json.RawMessage
			u := c.up.Paper + "/v3/projects/paper/versions/" + url.PathEscape(id) + "/builds?channel=STABLE"
			if err := c.getJSON(ctx, u, &builds); err != nil {
				return Catalog{}, err
			}
			if len(builds) == 0 {
				continue
			}
		}
		keep = append(keep, id)
	}
	return Catalog{Versions: versions(instancespec.TypePaper, keep)}, nil
}

// purpur: versions up to and including metadata.current (newer ones are experimental).
func (c *Client) purpur(ctx context.Context) (Catalog, error) {
	var resp struct {
		Metadata struct {
			Current string `json:"current"`
		} `json:"metadata"`
		Versions []string `json:"versions"`
	}
	if err := c.getJSON(ctx, c.up.Purpur+"/v2/purpur", &resp); err != nil {
		return Catalog{}, err
	}
	i := slices.Index(resp.Versions, resp.Metadata.Current)
	if i < 0 {
		return Catalog{}, fmt.Errorf("purpur: current version %q missing from the version list", resp.Metadata.Current)
	}
	ids := slices.Clone(resp.Versions[:i+1])
	slices.Reverse(ids) // the API lists oldest first
	return Catalog{Versions: versions(instancespec.TypePurpur, ids)}, nil
}

// fabric: stable game versions and stable loader versions, both in upstream order (newest first).
func (c *Client) fabric(ctx context.Context) (Catalog, error) {
	type item struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	var game, loader []item
	if err := c.getJSON(ctx, c.up.Fabric+"/v2/versions/game", &game); err != nil {
		return Catalog{}, err
	}
	if err := c.getJSON(ctx, c.up.Fabric+"/v2/versions/loader", &loader); err != nil {
		return Catalog{}, err
	}
	var ids []string
	for _, g := range game {
		if g.Stable {
			ids = append(ids, g.Version)
		}
	}
	loaders := []string{}
	for _, l := range loader {
		if l.Stable && validVersion(l.Version) && !slices.Contains(loaders, l.Version) {
			loaders = append(loaders, l.Version)
		}
	}
	return Catalog{Versions: versions(instancespec.TypeFabric, ids), Loaders: loaders}, nil
}

// neoforge: the Minecraft versions that have at least one NeoForge release (no -beta,
// -alpha, +snapshot), newest first. The id is the Minecraft version; itzg picks the newest
// NeoForge build for it when loader_version is null.
func (c *Client) neoforge(ctx context.Context) (Catalog, error) {
	var resp struct {
		Versions []string `json:"versions"`
	}
	if err := c.getJSON(ctx, c.up.NeoForge+"/api/maven/versions/releases/net/neoforged/neoforge", &resp); err != nil {
		return Catalog{}, err
	}
	var ids []string
	for _, v := range resp.Versions {
		if mc, ok := NeoForgeMC(v); ok {
			ids = append(ids, mc)
		}
	}
	return Catalog{Versions: versions(instancespec.TypeNeoForge, newestFirst(ids))}, nil
}

// NeoForgeMC maps a NeoForge release to its Minecraft version: the old scheme A.B.C
// (20 ≤ A ≤ 25) belongs to 1.A.B (1.A if B is 0), the yearly scheme Y.D.H.B (Y ≥ 26) to
// Y.D.H (Y.D if H is 0). Pre-releases (-beta, -alpha, +snapshot) and anything else are
// rejected.
func NeoForgeMC(v string) (string, bool) {
	parts := strings.Split(v, ".")
	n := make([]int, len(parts))
	for i, p := range parts {
		if p == "" || len(p) > 9 || strings.Trim(p, "0123456789") != "" {
			return "", false
		}
		n[i], _ = strconv.Atoi(p)
	}
	switch {
	case len(n) == 3 && n[0] >= 20 && n[0] <= 25:
		if n[1] == 0 {
			return fmt.Sprintf("1.%d", n[0]), true
		}
		return fmt.Sprintf("1.%d.%d", n[0], n[1]), true
	case len(n) == 4 && n[0] >= 26:
		if n[2] == 0 {
			return fmt.Sprintf("%d.%d", n[0], n[1]), true
		}
		return fmt.Sprintf("%d.%d.%d", n[0], n[1], n[2]), true
	}
	return "", false
}
```

- [ ] **Step 5: Handler und Verdrahtung im Paket**

`internal/servers/catalog_api.go`:

```go
package servers

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/servers/catalog"
)

// CatalogSource provides the version catalogs (*catalog.Client in production).
type CatalogSource interface {
	Get(ctx context.Context, t instancespec.Type) (catalog.Catalog, error)
}

func (h *Handler) handleGetCatalog(w http.ResponseWriter, r *http.Request) {
	t := instancespec.Type(chi.URLParam(r, "type"))
	if !slices.Contains(instancespec.Types, t) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "unknown server type")
		return
	}
	if t.IsModpack() {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "no version catalog: the modpack decides the version")
		return
	}
	cat, err := h.catalog.Get(r.Context(), t)
	switch {
	case errors.Is(err, catalog.ErrUnsupported):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "no version catalog for this type")
	case err != nil:
		h.log.WarnContext(r.Context(), "servers: version catalog unavailable", "type", string(t), "err", err)
		httpx.WriteError(w, r, http.StatusBadGateway, httpx.CodeCatalogUnavailable, "version catalog unavailable, enter the version manually")
	default:
		httpx.WriteJSON(w, http.StatusOK, cat)
	}
}
```

`internal/servers/handler.go`:
- Import `"github.com/Bernotese/mc-panel/internal/servers/catalog"` ergänzen (nach `secretbox`).
- Den Abschnitt von `// Deps are the dependencies` bis zum Ende von `func NewHandler` ersetzen durch
```go
// Deps are the dependencies of the server handlers.
type Deps struct {
	DB    *sql.DB // from store.Open (BEGIN IMMEDIATE transactions)
	Clock clock.Clock
	Audit audit.Recorder
	Box   *secretbox.Box // seals the CurseForge API key at rest
	// Instances is the agent's instance API; nil means "no agent" (every call fails with
	// agentapi.ErrUnavailable).
	Instances agentapi.InstancesClient
	Log       *slog.Logger  // nil = slog.Default()
	Catalog   CatalogSource // nil = catalog.New with the default upstreams
}

type Handler struct {
	db      *sql.DB
	q       *store.Queries
	clock   clock.Clock
	rec     audit.Recorder
	box     *secretbox.Box
	inst    agentapi.InstancesClient
	catalog CatalogSource
	log     *slog.Logger
	power   *powerLimiter
	kick    chan struct{} // Trigger -> reconcile loop (buffer 1, never blocks)
	status  statusCache   // last actual state reported by the agent

	reconcile reconcileState
}

func NewHandler(d Deps) *Handler {
	inst := d.Instances
	if inst == nil {
		inst = noAgent{}
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	cat := d.Catalog
	if cat == nil {
		cat = catalog.New(catalog.Options{Clock: d.Clock})
	}
	return &Handler{
		db: d.DB, q: store.New(d.DB), clock: d.Clock, rec: d.Audit, box: d.Box, inst: inst, catalog: cat, log: log,
		power: newPowerLimiter(), kick: make(chan struct{}, 1),
		reconcile: reconcileState{attempts: map[int64]attempt{}, warned: map[int64]bool{}},
	}
}
```
- In `Routes` vor der `GET /servers/settings`-Zeile:
```go
	api.Get("/servers/catalog/{type}", rbac.Admin(), h.handleGetCatalog)
```

(`cmd/panel/app.go` bleibt unverändert: `Catalog: nil` erzeugt den Standard-Client mit User-Agent `mc-panel/dev …`; Aufgabe 16 übergibt die echte Version.)

- [ ] **Step 6: Go-Tests laufen lassen (grün)**

Run: `gofmt -l internal/ && go test ./internal/servers/... -count=1 && CGO_ENABLED=1 go test -race -count=1 ./internal/servers/catalog/ && golangci-lint run ./internal/servers/...`
Expected: PASS (der Timeout-Fall dauert ≈ 1 s), keine Races, `0 issues`.

- [ ] **Step 7: OpenAPI, Zähler, MSW**

`api/openapi.yaml`:
- Unter `paths`, direkt vor `  /servers/settings:`:
```yaml
  /servers/catalog/{type}:
    get:
      operationId: getServerCatalog
      tags: [servers]
      parameters:
        - { name: type, in: path, required: true, schema: { $ref: '#/components/schemas/InstanceType' } }
      responses:
        '200': { description: OK, content: { application/json: { schema: { $ref: '#/components/schemas/Catalog' } } } }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { description: 'No catalog for modrinth/curseforge (the pack decides the version)', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
        '502': { description: 'catalog_unavailable: upstream not reachable or invalid; the form falls back to free text', content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
```
- Unter `components.schemas`, direkt vor `UnknownContainer`:
```yaml
    CatalogVersion:
      type: object
      required: [id, java]
      properties:
        id: { type: string, description: 'Minecraft version for every type, also neoforge (itzg picks the newest NeoForge build for it)' }
        java: { $ref: '#/components/schemas/JavaVersion' }
    Catalog:
      type: object
      required: [versions, loaders]
      properties:
        versions: { type: array, items: { $ref: '#/components/schemas/CatalogVersion' }, description: Newest first }
        loaders: { type: array, items: { type: string }, description: 'fabric: stable loader versions, newest first; all other types: []' }
```

`cmd/panel/integration_test.go`, `TestRouteCoverage`: `60` → `61`.

Run: `make generate`
Expected: `schema.d.ts` enthält `"/servers/catalog/{type}"`, `Catalog`, `CatalogVersion`.

`web/src/api/types.ts`, vor `export type ServerSettings = …`:
```ts
export type Catalog = Schemas['Catalog']
export type CatalogVersion = Schemas['CatalogVersion']
```

`web/src/mocks/db.ts`:
- Typ-Importe `Catalog` (nach `AuditResult`) und `InstanceType` (nach `InstanceConfig`) ergänzen.
- `MockDb` nach `serverSettings: MockServerSettings`: `catalogs: Partial<Record<InstanceType, Catalog>>`.
- Im Seed nach `serverSettings: …` (V5; NeoForge mit Minecraft-Versionen):
```ts
    catalogs: {
      vanilla: {
        versions: [
          { id: '26.3', java: 'java25' },
          { id: '26.2', java: 'java25' },
          { id: '1.21.11', java: 'java21' },
          { id: '1.20.4', java: 'java17' },
          { id: '1.16.5', java: 'java8' },
        ],
        loaders: [],
      },
      paper: {
        versions: [
          { id: '26.2', java: 'java25' },
          { id: '1.21.11', java: 'java21' },
          { id: '1.20.4', java: 'java17' },
        ],
        loaders: [],
      },
      purpur: {
        versions: [
          { id: '26.2', java: 'java25' },
          { id: '1.21.11', java: 'java21' },
        ],
        loaders: [],
      },
      fabric: {
        versions: [
          { id: '26.3', java: 'java25' },
          { id: '1.21.11', java: 'java21' },
        ],
        loaders: ['0.19.5', '0.19.4'],
      },
      // Minecraft versions like every other type (plan 02, task 15); the NeoForge build is optional (loader_version).
      neoforge: {
        versions: [
          { id: '26.2', java: 'java25' },
          { id: '1.21.11', java: 'java21' },
        ],
        loaders: [],
      },
    },
```

`web/src/mocks/handlers/servers.ts`:
- Typ-Import `Catalog` ergänzen (erste Zeile der Liste).
- In `serverOperations` direkt vor dem Kommentar `// Before getServer: …`:
```ts
  op(
    'getServerCatalog',
    http.get(`${API}/servers/catalog/:type`, ({ request, params }) => {
      const g = guard(request, { admin: true })
      if (g instanceof Response) return g
      const type = params.type as InstanceType
      if (!TYPES.includes(type) || type === 'modrinth' || type === 'curseforge') {
        return apiError(404, 'not_found', 'no version catalog for this type')
      }
      const catalog = db.catalogs[type]
      if (!catalog) return apiError(502, 'catalog_unavailable', 'version catalog unavailable, enter the version manually')
      return HttpResponse.json<Catalog>(catalog)
    }),
  ),

```

`web/src/mocks/handlers.test.ts`: `toHaveLength(60)` → `toHaveLength(61)` und vor `it('buckets the overview series by range', …)`:
```ts
  it('serves version catalogs: 404 for modpacks, 502 when missing', async () => {
    loginAs('admin')
    const fabric = await callApi('GET', '/servers/catalog/fabric')
    expect(fabric.body).toMatchObject({ versions: [{ id: '26.3', java: 'java25' }, { id: '1.21.11', java: 'java21' }], loaders: ['0.19.5', '0.19.4'] })
    expect((await callApi('GET', '/servers/catalog/modrinth')).status).toBe(404)
    delete db.catalogs.paper
    const paper = await callApi('GET', '/servers/catalog/paper')
    expect(paper.status).toBe(502)
    expect(field(paper.body, 'error')).toBe('catalog_unavailable')
    loginAs('alice')
    expect((await callApi('GET', '/servers/catalog/vanilla')).status).toBe(403)
  })

```

- [ ] **Step 8: Gesamtprüfung**

Run: `make test && make lint`
Expected: PASS (61 Operationen in Go und Vitest), `0 issues`.

- [ ] **Step 9: Commit**

```bash
git add internal/servers cmd/panel/integration_test.go api/openapi.yaml web/src/api/schema.d.ts web/src/api/types.ts \
  web/src/mocks/db.ts web/src/mocks/handlers/servers.ts web/src/mocks/handlers.test.ts
git commit -m "feat(servers): version catalogs for vanilla, paper, purpur, fabric and neoforge

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 16: Verdrahtung in `cmd/panel` und Ende-zu-Ende-Test

Die Abgleichsschleife läuft in `app.Run`, `appDeps` bekommt den Instanz-Client und die Panel-Version (für den Katalog-User-Agent), `serve` reicht denselben `*agent.Client` für Firewall und Instanzen durch. Ein Integrationstest mit simulierendem Fake-agent prüft den ganzen Weg über HTTP inkl. Abnahmekriterium 4.

**Files:**
- Modify: `cmd/panel/app.go`, `cmd/panel/main.go`, `cmd/panel/integration_test.go`
- Create (Test): `cmd/panel/fakeinstances_test.go`, `cmd/panel/instances_test.go`

**Interfaces:**
- Consumes: `servers.NewHandler`, `(*servers.Handler).Run`, `catalog.New`, `agentapi.InstancesClient`, `*agent.Client` (implementiert `agentapi.Client` und `agentapi.InstancesClient`, V3)
- Produces:
```go
type appDeps struct {
	Clock     clock.Clock
	Agent     agentapi.Client
	Instances agentapi.InstancesClient // nil = no agent (states "unknown")
	Version   string                   // User-Agent of catalog requests
}
```

- [ ] **Step 1: Failing test schreiben**

`cmd/panel/fakeinstances_test.go`:

```go
package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// fakeInstances simulates the agent's instance API: every accepted call takes effect at once
// (no queue, no Docker).
type fakeInstances struct {
	mu      sync.Mutex
	inst    map[int64]agentapi.InstanceStatus
	ensures []agentapi.EnsureRequest
	powers  []string
}

func newFakeInstances() *fakeInstances {
	return &fakeInstances{inst: map[int64]agentapi.InstanceStatus{}}
}

func (f *fakeInstances) Instances(context.Context) (agentapi.InstancesResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := make([]agentapi.InstanceStatus, 0, len(f.inst))
	for _, st := range f.inst {
		list = append(list, st)
	}
	slices.SortFunc(list, func(a, b agentapi.InstanceStatus) int { return cmp.Compare(a.ID, b.ID) })
	return agentapi.InstancesResponse{Docker: agentapi.DockerOK, Instances: list,
		Router: agentapi.RouterStatus{State: agentapi.StateRunning}}, nil
}

func (f *fakeInstances) Ensure(_ context.Context, req agentapi.EnsureRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensures = append(f.ensures, req)
	cur, ok := f.inst[req.ID]
	switch {
	case !ok:
		f.inst[req.ID] = agentapi.InstanceStatus{ID: req.ID, Name: req.Name, Revision: req.Revision, State: agentapi.StateCreated}
	case cur.Revision != req.Revision && req.Recreate:
		// recreated; it runs again if it ran before
		cur.Revision = req.Revision
		f.inst[req.ID] = cur
	}
	return nil
}

func (f *fakeInstances) Power(_ context.Context, id int64, action agentapi.PowerAction) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.inst[id]
	if !ok {
		return fmt.Errorf("%w: no container", agentapi.ErrUnavailable)
	}
	f.powers = append(f.powers, fmt.Sprintf("%d %s", id, action))
	st.State = agentapi.StateRunning
	if action == agentapi.PowerStop {
		st.State = agentapi.StateExited
	}
	f.inst[id] = st
	return nil
}

func (f *fakeInstances) Remove(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.inst, id)
	return nil
}

func (f *fakeInstances) firstEnsure() agentapi.EnsureRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ensures) == 0 {
		return agentapi.EnsureRequest{}
	}
	return f.ensures[0]
}

func (f *fakeInstances) lastEnsure() agentapi.EnsureRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.ensures) == 0 {
		return agentapi.EnsureRequest{}
	}
	return f.ensures[len(f.ensures)-1]
}
```

`cmd/panel/instances_test.go`:

```go
package main

import (
	"fmt"
	"testing"
	"time"
)

// waitServer polls GET /servers/{id} until ok accepts the status (the reconcile loop runs in
// the background of the app).
func waitServer(t *testing.T, c *client, id int64, what string, ok func(status map[string]any) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out := c.must(200, "GET", fmt.Sprintf("/servers/%d", id), nil)
		st, _ := out["status"].(map[string]any)
		if ok(st) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server %d never became %s: %v", id, what, st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func stateIs(want string) func(map[string]any) bool {
	return func(st map[string]any) bool { return st["state"] == want }
}

// Create → reconcile (ensure, start) → running → change → restart_required → restart
// (ensure with recreate) → running in the new revision; then acceptance criterion 4.
func TestInstanceLifecycleEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	a := admin.must(201, "POST", "/servers", paperServer("survival"))
	b := admin.must(201, "POST", "/servers", paperServer("creative"))
	srvA, srvB := int64(a["id"].(float64)), int64(b["id"].(float64))
	if a["address"] != "survival.mc.example.com" {
		t.Fatalf("address = %v", a["address"])
	}

	waitServer(t, admin, srvA, "running", stateIs("running"))
	waitServer(t, admin, srvB, "running", stateIs("running"))
	if req := e.instances.firstEnsure(); req.FQDN != "survival.mc.example.com" || req.Revision != 1 || req.Recreate {
		t.Fatalf("first ensure = %+v", req)
	}

	out := admin.must(200, "PATCH", fmt.Sprintf("/servers/%d", srvA), map[string]any{"memory_mib": 6144})
	if st, _ := out["status"].(map[string]any); !isTrue(st["restart_required"]) {
		t.Fatalf("after PATCH: %v", st)
	}
	admin.must(202, "POST", fmt.Sprintf("/servers/%d/power", srvA), map[string]string{"action": "restart"})
	if req := e.instances.lastEnsure(); req.ID != srvA || !req.Recreate || req.Revision != 2 || req.Spec.MemoryMiB != 6144 {
		t.Fatalf("restart ensure = %+v", req)
	}
	waitServer(t, admin, srvA, "running in revision 2", func(st map[string]any) bool {
		return st["state"] == "running" && !isTrue(st["restart_required"])
	})

	// acceptance criterion 4: server.power on A allows start/stop of A, nothing else
	u := admin.must(201, "POST", "/users", map[string]string{"username": "friend", "password": "another long secret 42", "role": "user"})
	admin.must(200, "PUT", fmt.Sprintf("/users/%d/permissions", int64(u["id"].(float64))), map[string]any{
		"grants": []map[string]any{{"server_id": srvA, "permissions": []string{"server.power"}}},
	})
	friend := e.client("")
	friend.must(200, "POST", "/auth/login", map[string]string{"username": "friend", "password": "another long secret 42"})
	friend.must(202, "POST", fmt.Sprintf("/servers/%d/power", srvA), map[string]string{"action": "stop"})
	waitServer(t, friend, srvA, "stopped", stateIs("stopped"))
	friend.must(202, "POST", fmt.Sprintf("/servers/%d/power", srvA), map[string]string{"action": "start"})
	waitServer(t, friend, srvA, "running", stateIs("running"))
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", fmt.Sprintf("/servers/%d", srvA), map[string]any{"memory_mib": 2048}},
		{"DELETE", fmt.Sprintf("/servers/%d", srvA), nil},
		{"GET", fmt.Sprintf("/servers/%d", srvB), nil},
		{"POST", fmt.Sprintf("/servers/%d/power", srvB), map[string]string{"action": "stop"}},
		{"GET", "/servers/settings", nil},
	} {
		if c, out := friend.do(tc.method, tc.path, tc.body); c != 403 {
			t.Errorf("%s %s as friend = %d %v, want 403", tc.method, tc.path, c, out)
		}
	}
}
```

`cmd/panel/integration_test.go`:
- `env` bekommt das Feld `instances *fakeInstances` (nach `agent`; `gofmt` richtet das Struct aus).
- In `newEnv`:
```go
	fa := newFakeAgent()
	fi := newFakeInstances()
	ctx, cancel := context.WithCancel(context.Background())
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clk, Agent: fa, Instances: fi, Version: "test"})
```
und am Ende `return &env{t: t, srv: srv, app: a, clk: clk, agent: fa, instances: fi, cfg: cfg}`.
- In `TestAgentDownStillWorks` denselben (nicht erreichbaren) Client für beide Felder übergeben:
```go
	ac := agentClientForMissingSocket(t)
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}, Agent: ac, Instances: ac})
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./cmd/panel/ -run 'TestInstanceLifecycleEndToEnd|TestAgentDownStillWorks' -count=1`
Expected: FAIL beim Kompilieren (`unknown field Instances in struct literal of type appDeps`).

- [ ] **Step 3: Verdrahten**

`cmd/panel/app.go`:
- Import `"github.com/Bernotese/mc-panel/internal/servers/catalog"` ergänzen (nach `internal/servers`).
- `appDeps`:
```go
type appDeps struct {
	Clock clock.Clock
	Agent agentapi.Client
	// Instances is the agent's instance API (in production the same *agent.Client as Agent);
	// nil means no agent: the panel works, instance states are "unknown".
	Instances agentapi.InstancesClient
	Version   string // panel version, sent in the User-Agent of catalog requests
}
```
- `serversH`:
```go
	serversH := servers.NewHandler(servers.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Box: box, Instances: d.Instances,
		Catalog: catalog.New(catalog.Options{Clock: d.Clock, Version: d.Version}),
	})
```
- `run`:
```go
	run := func(ctx context.Context) {
		var wg sync.WaitGroup
		wg.Add(4)
		go func() { defer wg.Done(); disp.Run(ctx) }()
		go func() { defer wg.Done(); svc.Run(ctx) }()
		go func() { defer wg.Done(); serversH.Run(ctx) }()
		go func() { defer wg.Done(); pruneSessionsLoop(ctx, authH.PruneSessions, sessionPruneInterval) }()
		wg.Wait()
	}
```

`cmd/panel/main.go`, in `serve` die `buildApp`-Zeile ersetzen durch
```go
	ac := agent.NewClient(cfg.AgentSocket) // firewall and instance API share one client
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}, Agent: ac, Instances: ac, Version: version})
```
(`newTestApp` in `app_test.go` bleibt bei `appDeps{Clock: clock.Real{}}`: `Instances: nil` → `noAgent`; `TestRunStopsOnCancel` prüft, dass auch die Abgleichsschleife beim Abbruch endet.)

- [ ] **Step 4: Tests laufen lassen (grün, auch mit Race-Detector)**

Run: `gofmt -l cmd/ && go test ./cmd/panel/ -count=1 && CGO_ENABLED=1 go test -race -count=1 ./cmd/panel/ ./internal/servers/...`
Expected: PASS (`TestInstanceLifecycleEndToEnd` braucht < 2 s: jede erfolgreiche Abgleichsaktion stößt sofort den nächsten Durchlauf an), keine Races.

- [ ] **Step 5: Gesamtprüfung und manuelle Kontrolle**

Run: `make test && make lint && make build`
Expected: alles grün, `bin/panel` und `bin/agent` gebaut.

Manuell (optional, macOS): `make dev`, als Admin anmelden, `PUT /api/servers/settings` über die Browser-Konsole bzw. ab Aufgabe 21 über die Einstellungsseite; ohne Docker zeigt `GET /api/servers/settings` `docker: "unavailable"` (agent läuft) bzw. `"unknown"` (agent gestoppt), angelegte Server stehen auf `unknown`, die panel-Logs enthalten genau eine Zeile `servers: reconcile paused …`.

- [ ] **Step 6: Commit**

```bash
git add cmd/panel/app.go cmd/panel/main.go cmd/panel/integration_test.go cmd/panel/fakeinstances_test.go cmd/panel/instances_test.go
git commit -m "feat(panel): run the instance reconcile loop and wire the agent instance API

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Selbstprüfung des Teilplans

- **Spec-Abdeckung:** §3.1 (Paket, Soll-Zustand, agent-Ausfall) → 11, 13, 14; §4 (Basisdomain-Regeln, Revision) → 12, 13; §5.3 (Tabelle, Trigger, Fehler, unbekannte Container) → 14; §6 (Kataloge, Cache, Timeout, Größe, UA, Java) → 15; §8 (alle neun Routen, `address`, `unknown`, DELETE ohne agent, Basisdomain-Wechsel) → 12–14; §8.1 Fehlercodes → 12; §8.2 Audit → 12–14; §11 „panel servers“, „panel Abgleich“, „panel Katalog“, „Vertrag“ → 12–16; §12.4 → 16.
- **Vertrag:** Nach Aufgabe 15 deckt `api/openapi.yaml` alle Operationen aus V4 ab (61 inkl. `GET /health`); Abweichungen von V4: keine an Schemas/Operationen; zusätzlich dokumentierte Antworten (`409`/`429`/`503`, Komponente `ServiceUnavailable`) und Beschreibungen (`PowerRequest`, `CatalogVersion.id`).
- **Typkonsistenz:** `Snapshot`, `statusJSON`, `config`, `instanceSettings`, `ensureRequest` werden in 13 eingeführt und in 14 unverändert benutzt; `fakeAgent` wächst in 12 → 13 (`setActionErr`, `takeCalls`) → 14 (`failed`, `failedCalls`, `lastEnsure`) → 15 (nur `env.catalog`).
