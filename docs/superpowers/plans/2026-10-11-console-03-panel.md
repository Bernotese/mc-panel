# Teilprojekt 3a – panel (Aufgaben 9–13) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das panel bekommt das Paket `internal/console`: Konsolenbefehle über RCON, Online-Spieler, Spielerlisten und Spieler-Aktionen sowie den Live-Log-Stream per Server-Sent Events mit Neuverbindung zum agent und erneuter Prüfung von Session und Recht alle 30 s.

**Architecture:** `console.Handler` besitzt die fünf Routen aus V3/V4 der Übersicht. Befehle und Spieler-Aktionen laufen durch dieselbe Kette (Server laden → validieren → Limiter → `agentapi.ConsoleClient.Rcon` → Audit → Antwort). Online-Spieler parst das panel aus `list uuids` und hält sie 10 s pro Server vor. Der Stream öffnet den agent-Log-Stream, schreibt jede Zeile als SSE-Ereignis, verbindet sich nach dessen Ende alle 3 s neu (mit `since`), sendet `state`-Ereignisse und Heartbeats und prüft alle 30 s über `console.Access` (Implementierung `console.NewAccess` auf `auth.Sessions.Recheck` + `rbac.DBChecker`) erneut. `cmd/panel` verdrahtet den Handler mit demselben `*agent.Client` und beendet offene Streams beim Herunterfahren über `http.Server.RegisterOnShutdown`.

**Tech Stack:** Go (Standardbibliothek, chi, sqlc/`store`, `golang.org/x/time/rate`), `http.ResponseController`. Keine neuen Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md` (§6). Verträge V1, V3, V4 und Dateieigentum B2: `docs/superpowers/plans/2026-10-11-console-00-overview.md`.

## Global Constraints

Alle Global Constraints und Verträge aus `2026-10-11-console-00-overview.md` gelten (und damit TP1, TP2 und `CLAUDE.md`): Routen nur über `*rbac.Router` mit Regel, Fehler nur über `httpx.WriteError` mit Codes aus `internal/httpx/errors.go`, `clock.Clock` statt `time.Now()`, Kontext überall, Go-Tests nur mit Standardbibliothek. Zusätzlich verbindlich für dieses Teilpaket:

| Wert | Festlegung |
|---|---|
| Paket | `github.com/Bernotese/mc-panel/internal/console`; `NewHandler(Deps) *Handler`, `Routes(api *rbac.Router)`, `Shutdown()`, `NewAccess(SessionChecker, rbac.Checker) Access`, `ErrAccessRevoked` |
| Dateien | nur `internal/console/**`, `internal/auth/recheck.go` + `internal/auth/recheck_test.go` (additiv), `cmd/panel/**` |
| Nicht eingerichteter Server | `404 not_found` auf allen fünf Routen (ungültige ID, unbekannter Server, Server ohne `instances`-Zeile) |
| Befehl | genau ein führendes `/` wird entfernt, dann `agentapi.ValidateCommand`; Verstoß → `400 validation_failed` |
| Limiter | 30/min pro Benutzer, Burst 10 (`rate.Every(2 * time.Second)`, Burst 10), gemeinsam für Befehle und Spieler-Aktionen, Fake-Clock; `429 rate_limited` mit `Retry-After` (aufgerundete Sekunden, mindestens 1) |
| agent-Fehler → HTTP | `ErrNotRunning` und `ErrNotFound` → `409 instance_not_running`; `ErrRconUnavailable` → `503 rcon_unavailable`; `ErrDockerUnavailable` → `503 docker_unavailable`; `ErrBusy` → `429 rate_limited` (`Retry-After: 5`); `instancespec.ErrInvalid` → `400 validation_failed`; `ErrUnavailable`/`context.DeadlineExceeded` → `503 agent_unavailable`; sonst `500 internal`. Reihenfolge der Prüfung genau so (Plan 01 Aufgabe 2: `404 not_found` erfüllt auch `instancespec.ErrInvalid`, `409 not_running` auch `banrules.ErrConflict`, `503 rcon_unavailable`/`docker_unavailable` auch `ErrUnavailable` – die spezifischen Fehler kommen immer zuerst, `ErrUnavailable` zuletzt) |
| Audit | `server.console_command` `{command}` bzw. `server.player_action` `{action, name, reason?}`, Ziel `server:<id>`, Ergebnis `success`/`failure` (jeder agent-Fehler = `failure`); geschrieben mit `context.WithoutCancel`, damit ein abgebrochener Client die Aufzeichnung nicht verhindert. Validierung, Rechte und Limiter werden nicht auditiert |
| Online-Spieler | RCON `list uuids` mit eigenem Timeout 5 s; Cache 10 s pro Server (Erfolg und `available: false`); nicht gecacht, wenn der Anfragekontext abgebrochen ist; jeder Fehler → `{"available":false,"online":[],"max":null}`; erfolgreiche Spieler-Aktion leert den Cache des Servers |
| Spielerlisten | `agentapi.ErrNotFound` (Datenverzeichnis fehlt, Server noch nie angelegt) → `200` mit leeren Listen; `nil`-Slices → `[]` |
| Stream | `Content-Type: text/event-stream`, `Cache-Control: no-store`, `X-Accel-Buffering: no`; `tail` 0–1000 (Default 500, sonst `400`); `Last-Event-ID` (Ganzzahl > 0) → `since` = Wert + 1, sonst ignoriert; Ereignisse `line` (`id` = ts in ns, `data` = `{"ts": <ms>, "stream", "line"}`), `state` (`{"running": bool}`, nur bei Änderung), Kommentar `: ping` alle 15 s; Neuverbindung nach 3 s; erneute Prüfung alle 30 s; höchstens 4 Streams pro Benutzer (`429 rate_limited`); nach `Shutdown` `503 agent_unavailable` |
| `running` | Direkt nach erfolgreichem Öffnen des agent-Streams `state {running: stream.Running()}` (V1: aus dem agent-Header `X-Container-Running`), bei Ende oder Fehler des agent-Streams `state {running: false}`; gesendet wird nur bei Änderung gegenüber dem zuletzt gesendeten Wert (siehe Entscheidung 3) |
| Doppelte Zeilen | Zeilen mit `ts < since` werden verworfen; jede gesendete Zeile setzt `since = ts + 1` |
| Fristen | `ReadDeadline` des Stream-Requests wird aufgehoben (sonst bricht `ReadTimeout` 30 s den Kontext ab); vor jedem Schreiben `WriteDeadline` = jetzt + 30 s über `clock.Real{}` (I/O-Fristen sind Wanduhrzeit; der Fake-Clock der Tests läge in der Vergangenheit); `http.ErrNotSupported` wird ignoriert |
| Zeitgeber | Stream-Zeitgeber über die interne Schnittstelle `timers` (`Every`, `After`), Default `time.NewTicker`/`time.NewTimer`; Tests ersetzen sie über `console.SetTimers` (`export_test.go`) |

## Entscheidungen innerhalb dieses Teilplans

1. **Fehlender Container bei RCON = „läuft nicht“:** `agentapi.ErrNotFound` (Container `mcpanel-mc-<id>` fehlt, z. B. während der Neuanlage) wird wie `ErrNotRunning` zu `409 instance_not_running`. Aus Sicht des Nutzers gibt es dann keinen laufenden Server, dem man Befehle schicken könnte; ein `404` würde mit „Server existiert nicht“ verwechselt.
2. **`GET /players` antwortet nie mit 503:** Laut V3 hat die Operation nur `200/401/403/404`. Auch ohne agent (`agent_unavailable`) oder ohne Docker antwortet sie `available: false`, damit die Übersicht nicht rot wird.
3. **`running` kommt vom agent:** Docker liefert für einen gestoppten Container die Historie und beendet `follow` sofort – „Stream geöffnet“ heißt also nicht „läuft“. Der agent meldet deshalb beim Öffnen per Header `X-Container-Running`, ob der Container läuft (`agentapi.LogStream.Running()`, V1). Weil `state` nur bei Änderung gesendet wird, erzeugt der 3-s-Neuversuch bei einem gestoppten Server kein Flackern (`false` → offen mit `Running() == false` → Ende → `false`: keine Ereignisse).
4. **Stream ohne agent bleibt offen:** Kann der agent-Stream nicht geöffnet werden (agent weg, Docker weg, Container fehlt), antwortet der Stream trotzdem `200`, sendet `state {running:false}` und versucht es alle 3 s erneut. So übersteht die Konsole Neustarts von agent und Container.
5. **`Access` darf `nil` sein:** `NewHandler` ersetzt `nil` durch `denyAll` (jede erneute Prüfung schlägt fehl, Streams enden nach 30 s). Damit bleibt `cmd/panel` vor Aufgabe 13 lauffähig; Aufgabe 13 verdrahtet `console.NewAccess`.
6. **Recheck lädt die Rolle neu:** `auth.Sessions.Recheck` liefert die aktuelle `users`-Zeile; ein inzwischen herabgestufter Admin braucht dann eine echte `server.console`-Berechtigung. Die Stufe wird wie in der Session-Middleware über `effectiveStage` bestimmt (Admin ohne 2FA → `setup` → Stream endet).

## Review Focus (dieser Teilplan)

- **#2 Befehls-Injektion über Spieler-Aktionen:** `TestPlayerActionRejectsInjection` (Aufgabe 11).
- **#3 Hängende oder endlose Streams:** `TestStreamRecheckClosesStream`, `TestStreamClosesOnDeletedServer`, `TestStreamClientGoneClosesAgentStream`, `TestStreamLimitPerUser`, `TestShutdownClosesStreams`, `TestStreamSurvivesServerTimeouts` (Aufgabe 12).
- **#4 Doppelte oder verlorene Zeilen:** `TestStreamRunningStateAndReconnect`, `TestStreamLastEventID` (Aufgabe 12).
- **#5 Unerwartete RCON-Ausgaben:** `TestParseList` mit `testdata/list/*.txt`, `TestOnlinePlayersUnavailable` (Aufgabe 10).

## Dateistruktur

```
internal/console/handler.go        (9, 10, 12) Deps, Access, Handler, Routes, Shutdown, Helfer, noAgent, denyAll
internal/console/limit.go          (9)         userLimiter (30/min, Burst 10)
internal/console/command.go        (9)         POST /servers/{id}/console, run (gemeinsam mit Aktionen)
internal/console/stubs.go          (9–12)      Übergangs-Handler (503), wird in 12 gelöscht
internal/console/listparse.go      (10)        Parser für "list uuids"
internal/console/players.go        (10)        GET /servers/{id}/players, Cache
internal/console/actions.go        (11)        GET /players/lists, POST /players/actions
internal/console/timers.go         (12)        timers, realTimers
internal/console/stream.go         (12)        GET /console/stream, streamSet, sseWriter, logSession
internal/console/access.go         (12)        SessionChecker, NewAccess
internal/auth/recheck.go           (12)        Sessions.Recheck
cmd/panel/app.go, main.go          (13)        Verdrahtung, Shutdown
Tests: internal/console/{helpers,routes,command,listparse,players,actions,faketimers,stream,access}_test.go,
       internal/console/export_test.go, internal/console/testdata/list/*.txt,
       internal/auth/recheck_test.go, cmd/panel/{fakeconsole,console}_test.go
```

---

### Task 9: Grundgerüst, Limiter und `POST /servers/{id}/console`

Ersetzt den Stub aus Phase A durch das echte Grundgerüst (gemeinsame Helfer, Fehlerabbildung, Limiter) und implementiert Konsolenbefehle. Die übrigen vier Handler bleiben vorerst Stubs in `stubs.go`.

**Files:**
- Modify (vollständig ersetzen): `internal/console/handler.go`
- Create: `internal/console/limit.go`, `internal/console/command.go`, `internal/console/stubs.go`
- Delete: `internal/console/handler_test.go` (Phase A; `TestRouteContract` zieht nach `routes_test.go`, `TestStubsAnswerAgentUnavailable` gilt ab dieser Aufgabe nicht mehr, `t0` steht künftig in `helpers_test.go`)
- Test: `internal/console/helpers_test.go`, `internal/console/routes_test.go`, `internal/console/command_test.go`

**Interfaces:**
- Consumes: `agentapi.ConsoleClient`, `agentapi.ValidateCommand`, `agentapi.ErrNotFound/ErrNotRunning/ErrRconUnavailable/ErrDockerUnavailable/ErrBusy/ErrUnavailable`, `agentapi.ErrorCode*` (V1, Phase A); `*agent.StatusError` (nur Tests); `httpx.CodeInstanceNotRunning`, `httpx.CodeRconUnavailable` (Phase A, Aufgabe 3); `audit.ActionServerConsoleCommand` (Phase A); `store.Queries.RbacGetServer`, `ServersGetInstance`
- Produces:
```go
package console

type Deps struct {
	DB     *sql.DB
	Clock  clock.Clock
	Audit  audit.Recorder
	Agent  agentapi.ConsoleClient // nil = no agent
	Access Access                 // nil = every recheck fails
	Log    *slog.Logger           // nil = slog.Default()
}
type Access interface {
	Recheck(ctx context.Context, p principal.Principal, serverID int64, perm rbac.Permission) error
}
var ErrAccessRevoked error
func NewHandler(d Deps) *Handler
func (h *Handler) Routes(api *rbac.Router)
func (h *Handler) Shutdown()
```

- [ ] **Step 0: Worktree und Draft-PR**

```bash
git fetch origin
git worktree add ../mc-panel-wt/feat/console-panel -b feat/console-panel origin/main
cd ../mc-panel-wt/feat/console-panel
ls internal/console/
```

Erwartet (Phase A, Plan 01 Aufgabe 3): `handler.go` (Stub nach V4; die fünf Handler-Methoden und `noAgent` stehen in dieser Datei) und `handler_test.go` (`t0`, `wantRoutes`, `newRouter`, `TestRouteContract`, `TestStubsAnswerAgentUnavailable`, `TestShutdownWithoutStreams`). `handler_test.go` wird gelöscht, weil die neuen Testdateien dieselben Namen definieren und die Stubs ab dieser Aufgabe verschwinden:

```bash
git rm internal/console/handler_test.go
```

- [ ] **Step 1: Testhelfer und Routen-Vertrag schreiben**

`internal/console/helpers_test.go`:

```go
package console_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// env is a real database plus the console handler mounted on a real rbac.Router (with the
// DB-backed checker), so every request also exercises the route rule.
type env struct {
	t          *testing.T
	db         *sql.DB
	clk        *clock.Fake
	rec        *audit.MemoryRecorder
	agent      *fakeAgent
	access     *fakeAccess
	h          *console.Handler
	mux        http.Handler
	rootID     int64
	principals sync.Map // user id -> principal.Principal (stream tests, see stream_test.go)
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

// newEnvWith lets a test change the dependencies (e.g. Agent = nil) before the handler is built.
func newEnvWith(t *testing.T, opt func(*console.Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		t: t, db: db, clk: clock.NewFake(t0), rec: &audit.MemoryRecorder{},
		agent: newFakeAgent(), access: &fakeAccess{},
	}
	d := console.Deps{
		DB: db, Clock: e.clk, Audit: e.rec, Agent: e.agent, Access: e.access,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if opt != nil {
		opt(&d)
	}
	e.h = console.NewHandler(d)
	mux := chi.NewRouter()
	e.h.Routes(rbac.NewRouter(mux, rbac.NewDBChecker(db), e.clk))
	e.mux = mux
	e.rootID = storetest.CreateUser(t, db, "root", "admin")
	return e
}

// admin returns the principal of the seeded admin "root".
func (e *env) admin() principal.Principal {
	return principal.Principal{
		UserID: e.rootID, Username: "root", Role: principal.RoleAdmin, Stage: principal.StageFull, SessionID: 1,
	}
}

func userP(id int64, name string) principal.Principal {
	return principal.Principal{UserID: id, Username: name, Role: principal.RoleUser, Stage: principal.StageFull, SessionID: id + 100}
}

func srvPath(id int64, suffix string) string { return fmt.Sprintf("/servers/%d%s", id, suffix) }

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
	rctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("203.0.113.7"))
	if p != nil {
		rctx = principal.With(rctx, *p)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(rctx))
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

// createInstance inserts a server with a paper instance row.
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

// waitFor polls cond every 10 ms for up to 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// fakeAccess is a console.Access with a settable answer.
type fakeAccess struct {
	mu    sync.Mutex
	err   error
	calls int
	perms []rbac.Permission
}

func (f *fakeAccess) set(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func (f *fakeAccess) Recheck(_ context.Context, _ principal.Principal, _ int64, perm rbac.Permission) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.perms = append(f.perms, perm)
	return f.err
}

// fakeAgent is an in-memory agentapi.ConsoleClient.
type fakeAgent struct {
	mu         sync.Mutex
	err        error             // returned by every call while set
	outputs    map[string]string // Rcon output per command ("" if missing)
	rconErrs   map[string]error  // Rcon error per command
	rcons      []string          // "<id> <command>" of every Rcon call
	players    agentapi.PlayersResponse
	playersErr error
	logsErr    error         // returned by Logs while set
	streams    []*fakeStream // handed out by Logs in order; none left = ErrUnavailable
	logReqs    []agentapi.LogsRequest
}

var _ agentapi.ConsoleClient = (*fakeAgent)(nil)

func newFakeAgent() *fakeAgent {
	return &fakeAgent{
		outputs: map[string]string{}, rconErrs: map[string]error{},
		players: agentapi.PlayersResponse{
			Ops: []agentapi.PlayerOp{}, Whitelist: []agentapi.PlayerEntry{},
			Bans: []agentapi.PlayerBan{}, Errors: []string{},
		},
	}
}

func (f *fakeAgent) setErr(err error)     { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeAgent) setLogsErr(err error) { f.mu.Lock(); f.logsErr = err; f.mu.Unlock() }

func (f *fakeAgent) setOutput(cmd, out string) { f.mu.Lock(); f.outputs[cmd] = out; f.mu.Unlock() }

func (f *fakeAgent) setRconErr(cmd string, err error) { f.mu.Lock(); f.rconErrs[cmd] = err; f.mu.Unlock() }

func (f *fakeAgent) setPlayers(p agentapi.PlayersResponse, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.players, f.playersErr = p, err
}

// takeRcons returns the recorded Rcon calls and forgets them.
func (f *fakeAgent) takeRcons() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.rcons
	f.rcons = nil
	return out
}

func (f *fakeAgent) Rcon(ctx context.Context, id int64, command string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rcons = append(f.rcons, fmt.Sprintf("%d %s", id, command))
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if f.err != nil {
		return "", f.err
	}
	if err := f.rconErrs[command]; err != nil {
		return "", err
	}
	return f.outputs[command], nil
}

func (f *fakeAgent) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agentapi.PlayersResponse{}, f.err
	}
	return f.players, f.playersErr
}

func (f *fakeAgent) Logs(_ context.Context, req agentapi.LogsRequest) (agentapi.LogStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logReqs = append(f.logReqs, req)
	if f.err != nil {
		return nil, f.err
	}
	if f.logsErr != nil {
		return nil, f.logsErr
	}
	if len(f.streams) == 0 {
		return nil, fmt.Errorf("%w: no stream prepared", agentapi.ErrUnavailable)
	}
	s := f.streams[0]
	f.streams = f.streams[1:]
	return s, nil
}

// prepare queues a stream of a running container for the next Logs call.
func (f *fakeAgent) prepare() *fakeStream { return f.prepareStream(true) }

// prepareStopped queues a stream of a stopped container (Running() == false).
func (f *fakeAgent) prepareStopped() *fakeStream { return f.prepareStream(false) }

func (f *fakeAgent) prepareStream(running bool) *fakeStream {
	s := newFakeStream(running)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.streams = append(f.streams, s)
	return s
}

func (f *fakeAgent) requests() []agentapi.LogsRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.logReqs)
}

var errStreamClosed = errors.New("fake stream closed")

// fakeStream is an agentapi.LogStream fed by the test.
type fakeStream struct {
	running bool
	lines   chan agentapi.LogLine
	end     chan error
	closed  chan struct{}
	once    sync.Once
}

func newFakeStream(running bool) *fakeStream {
	return &fakeStream{running: running, lines: make(chan agentapi.LogLine), end: make(chan error, 1), closed: make(chan struct{})}
}

func (s *fakeStream) Running() bool { return s.running }

func (s *fakeStream) Next() (agentapi.LogLine, error) {
	select {
	case l := <-s.lines:
		return l, nil
	case err := <-s.end:
		return agentapi.LogLine{}, err
	case <-s.closed:
		return agentapi.LogLine{}, errStreamClosed
	}
}

func (s *fakeStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

// send hands one line to the panel's reader (blocks until it is taken).
func (s *fakeStream) send(t *testing.T, l agentapi.LogLine) {
	t.Helper()
	select {
	case s.lines <- l:
	case <-time.After(5 * time.Second):
		t.Fatalf("nobody reads line %+v", l)
	}
}

// finish ends the stream like the agent does when the container stops.
func (s *fakeStream) finish() { s.end <- io.EOF }

func (s *fakeStream) waitClosed(t *testing.T) {
	t.Helper()
	select {
	case <-s.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("agent stream was not closed")
	}
}
```

`internal/console/routes_test.go`:

```go
package console_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// wantRoutes are exactly the console operations of api/openapi.yaml with their rules (V3/V4).
var wantRoutes = map[string]rbac.Rule{
	"GET /api/servers/{id}/console/stream":   rbac.Server(rbac.PermServerConsole, "id"),
	"POST /api/servers/{id}/console":         rbac.Server(rbac.PermServerConsole, "id"),
	"GET /api/servers/{id}/players":          rbac.Server(rbac.PermServerView, "id"),
	"GET /api/servers/{id}/players/lists":    rbac.Server(rbac.PermServerConsole, "id"),
	"POST /api/servers/{id}/players/actions": rbac.Server(rbac.PermServerConsole, "id"),
}

func TestRouteContract(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(t0)
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), clk)
	rt.Route("/api", func(api *rbac.Router) {
		console.NewHandler(console.Deps{DB: db, Clock: clk, Audit: &audit.MemoryRecorder{}}).Routes(api)
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

- [ ] **Step 2: Befehls-Tests schreiben**

`internal/console/command_test.go`:

```go
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
```

- [ ] **Step 3: Implementierung schreiben**

`internal/console/handler.go` vollständig ersetzen (der Phase-A-Stub mit `deps Deps`, `notImplemented` und den fünf Stub-Methoden verschwindet; vier davon ziehen vorübergehend nach `stubs.go`):

```go
// Package console implements the live console of a Minecraft server (sub-project 3a): the log
// stream (SSE), console commands and player management. Everything goes through the agent; the
// panel never touches Docker or a server's data directory.
package console

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Deps are the dependencies of the console handlers.
type Deps struct {
	DB    *sql.DB // from store.Open
	Clock clock.Clock
	Audit audit.Recorder
	// Agent is the agent's console API; nil means "no agent" (every call fails with
	// agentapi.ErrUnavailable).
	Agent agentapi.ConsoleClient
	// Access re-checks open streams every 30 s; nil makes every recheck fail (streams end
	// after 30 s). Production wires NewAccess.
	Access Access
	Log    *slog.Logger // nil = slog.Default()
}

// Access re-checks the caller of an open stream (spec §6.2).
type Access interface {
	// Recheck returns nil while the session p.SessionID is valid (not expired, not revoked,
	// stage full), the user exists and still holds perm on serverID (admins always do).
	Recheck(ctx context.Context, p principal.Principal, serverID int64, perm rbac.Permission) error
}

// ErrAccessRevoked: the caller of a stream lost the permission (or Access is not configured).
var ErrAccessRevoked = errors.New("console: access revoked")

type Handler struct {
	q      *store.Queries
	clock  clock.Clock
	rec    audit.Recorder
	agent  agentapi.ConsoleClient
	access Access
	log    *slog.Logger
	limit  *userLimiter
}

func NewHandler(d Deps) *Handler {
	ag := d.Agent
	if ag == nil {
		ag = noAgent{}
	}
	acc := d.Access
	if acc == nil {
		acc = denyAll{}
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, agent: ag, access: acc, log: log,
		limit: newUserLimiter(),
	}
}

// Routes registers the console routes (paths relative to /api, rules from contract V4).
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/servers/{id}/console/stream", rbac.Server(rbac.PermServerConsole, "id"), h.handleStream)
	api.Post("/servers/{id}/console", rbac.Server(rbac.PermServerConsole, "id"), h.handleCommand)
	api.Get("/servers/{id}/players", rbac.Server(rbac.PermServerView, "id"), h.handleOnlinePlayers)
	api.Get("/servers/{id}/players/lists", rbac.Server(rbac.PermServerConsole, "id"), h.handlePlayerLists)
	api.Post("/servers/{id}/players/actions", rbac.Server(rbac.PermServerConsole, "id"), h.handlePlayerAction)
}

// Shutdown closes all open streams (there are none before the stream handler exists).
func (h *Handler) Shutdown() {}

func serverTarget(id int64) string { return "server:" + strconv.FormatInt(id, 10) }

// record writes an audit entry; actor and IP come from the request context. The action already
// happened, so the entry is written even if the client went away (WithoutCancel), and a failing
// audit write is logged, not returned to the client.
func (h *Handler) record(ctx context.Context, action, target string, result audit.Result, details map[string]any) {
	ctx = context.WithoutCancel(ctx)
	if err := h.rec.Record(ctx, audit.Entry{Action: action, Target: target, Result: result, Details: details}); err != nil {
		h.log.ErrorContext(ctx, "audit record failed", "action", action, "target", target, "err", err)
	}
}

func writeValidation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func writeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
}

// loadServer returns the server of the {id} path parameter if it has an instance; it writes
// 404 itself for malformed ids, unknown servers and servers without instance (spec §6.1).
func (h *Handler) loadServer(w http.ResponseWriter, r *http.Request) (store.Server, bool) {
	ctx := r.Context()
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeNotFound(w, r)
		return store.Server{}, false
	}
	s, err := h.q.RbacGetServer(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeNotFound(w, r)
		return store.Server{}, false
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return store.Server{}, false
	}
	_, err = h.q.ServersGetInstance(ctx, id)
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

// allow takes a command token of userID; without one it writes 429 with Retry-After.
func (h *Handler) allow(w http.ResponseWriter, r *http.Request, userID int64) bool {
	d := h.limit.take(userID, h.clock.Now())
	if d <= 0 {
		return true
	}
	secs := int(math.Ceil(d.Seconds()))
	w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many commands, try again later")
	return false
}

// writeAgentError maps a failed agent call to the API error. The order matters: a 404/409
// *agent.StatusError also matches instancespec.ErrInvalid, a 503 also agentapi.ErrUnavailable.
func writeAgentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, agentapi.ErrNotRunning), errors.Is(err, agentapi.ErrNotFound):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeInstanceNotRunning, "the server is not running")
	case errors.Is(err, agentapi.ErrRconUnavailable):
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeRconUnavailable,
			"the server does not accept commands yet (RCON not ready)")
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

var errNoAgent = fmt.Errorf("%w: no agent configured", agentapi.ErrUnavailable)

// noAgent stands in for a missing agent client: every call fails as if the agent were down.
type noAgent struct{}

func (noAgent) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, errNoAgent
}

func (noAgent) Rcon(context.Context, int64, string) (string, error) { return "", errNoAgent }

func (noAgent) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, errNoAgent
}

// denyAll stands in for a missing Access: every recheck fails.
type denyAll struct{}

func (denyAll) Recheck(context.Context, principal.Principal, int64, rbac.Permission) error {
	return ErrAccessRevoked
}
```

`internal/console/limit.go`:

```go
package console

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Console commands and player actions per user: 30 per minute, burst 10 (spec §6.4). Every
// accepted request writes an audit entry, so this also bounds how fast one account grows the
// audit chain.
const (
	commandEvery = 2 * time.Second
	commandBurst = 10
)

// userLimiter keeps one token bucket per user id (bounded by the number of users).
type userLimiter struct {
	mu    sync.Mutex
	users map[int64]*rate.Limiter
}

func newUserLimiter() *userLimiter { return &userLimiter{users: make(map[int64]*rate.Limiter)} }

// take consumes a token for userID at now; it returns 0 if allowed, otherwise the wait time
// (no token is consumed then).
func (l *userLimiter) take(userID int64, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim, ok := l.users[userID]
	if !ok {
		lim = rate.NewLimiter(rate.Every(commandEvery), commandBurst)
		l.users[userID] = lim
	}
	r := lim.ReserveN(now, 1)
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return d
	}
	return 0
}
```

`internal/console/command.go`:

```go
package console

import (
	"net/http"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

type commandRequest struct {
	Command string `json:"command"`
}

// commandResponse is the ConsoleCommandResponse schema (also the answer of player actions).
type commandResponse struct {
	Output string `json:"output"`
}

// handleCommand sends one console command over RCON (spec §6.4).
func (h *Handler) handleCommand(w http.ResponseWriter, r *http.Request) {
	var req commandRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	cmd := strings.TrimPrefix(req.Command, "/") // exactly one leading slash
	if err := agentapi.ValidateCommand(cmd); err != nil {
		writeValidation(w, r, err.Error())
		return
	}
	h.run(w, r, s.ID, cmd, audit.ActionServerConsoleCommand, map[string]any{"command": cmd})
}

// run sends an accepted command: rate limit, agent call, audit entry, answer. Console commands
// and player actions share it (and the limiter, spec §6.5). It reports whether the agent
// accepted the command.
func (h *Handler) run(w http.ResponseWriter, r *http.Request, serverID int64, cmd, action string, details map[string]any) bool {
	ctx := r.Context()
	p, _ := principal.From(ctx) // guaranteed by the rbac rule
	if !h.allow(w, r, p.UserID) {
		return false
	}
	out, err := h.agent.Rcon(ctx, serverID, cmd)
	result := audit.ResultSuccess
	if err != nil {
		result = audit.ResultFailure
	}
	h.record(ctx, action, serverTarget(serverID), result, details)
	if err != nil {
		writeAgentError(w, r, err)
		return false
	}
	httpx.WriteJSON(w, http.StatusOK, commandResponse{Output: out})
	return true
}
```

`internal/console/stubs.go`:

```go
package console

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

// Transitional handlers: tasks 10–12 of plan 03 replace them one by one and task 12 deletes
// this file.
func notYet(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "console not implemented yet")
}

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request)        { notYet(w, r) }
func (h *Handler) handleOnlinePlayers(w http.ResponseWriter, r *http.Request) { notYet(w, r) }
func (h *Handler) handlePlayerLists(w http.ResponseWriter, r *http.Request)   { notYet(w, r) }
func (h *Handler) handlePlayerAction(w http.ResponseWriter, r *http.Request)  { notYet(w, r) }
```

Falls Phase A die Konstanten anders benannt hat als `httpx.CodeInstanceNotRunning`, `httpx.CodeRconUnavailable` bzw. `audit.ActionServerConsoleCommand`, die Namen aus Phase A übernehmen (in Code und Tests); die Werte (`instance_not_running`, `rcon_unavailable`, `server.console_command`) sind verbindlich.

- [ ] **Step 4: Lokal prüfen**

Run: `make check`
Expected: OK (gofmt, vet, Build macOS/Linux, tsc).

- [ ] **Step 5: Commit, Push, Draft-PR, CI**

```bash
git add internal/console
git commit -m "feat(console): console commands over RCON with rate limit and audit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin feat/console-panel
gh pr create --draft --title "feat(console): panel side of the console (TP3a, tasks 9-13)" --body "Plan: docs/superpowers/plans/2026-10-11-console-03-panel.md"
gh pr checks --watch
```

Expected: CI grün, insbesondere `TestRouteContract`, `TestCommand*` und `cmd/panel` `TestRoutesMatchOpenAPI`.

---

### Task 10: Online-Spieler (`GET /servers/{id}/players`) und Parser für `list uuids`

**Files:**
- Create: `internal/console/listparse.go`, `internal/console/players.go`, `internal/console/testdata/list/*.txt`
- Modify: `internal/console/handler.go` (Feld `online`), `internal/console/stubs.go` (Stub `handleOnlinePlayers` entfernen)
- Test: `internal/console/listparse_test.go`, `internal/console/players_test.go`, `internal/console/players_internal_test.go`

**Interfaces:**
- Consumes: `Handler.loadServer`, `agentapi.ConsoleClient.Rcon` (Aufgabe 9)
- Produces (paketintern, Aufgabe 11 nutzt `forget`):
```go
type onlinePlayer struct { Name string `json:"name"`; UUID *string `json:"uuid"` }
func parseList(out string) (players []onlinePlayer, maxPlayers int, ok bool)
func (c *onlineCache) forget(id int64)
```

- [ ] **Step 1: Beispielausgaben anlegen**

Die Formate stammen aus der Vanilla-Sprachdatei (`commands.list.players` = `There are %s of a max of %s players online: %s`, `commands.list.nameAndId` = `%s (%s)`), die auch Paper benutzt (beobachtet auf der Test-VM, Paper 26.2, 2026-10-11: `There are 0 of a max of 20 players online: ` mit Leerzeichen am Ende), sowie aus Bukkit/Spigot (`There are N/M players online:` mit Namen in der nächsten Zeile). `rcon-cli` hängt einen Zeilenumbruch an.

```bash
mkdir -p internal/console/testdata/list
cd internal/console/testdata/list
printf '%s\n' 'There are 2 of a max of 20 players online: Alex (8667ba71-b85a-4004-af54-457a9734eed7), Steve (069a79f4-44e9-4726-a5be-fca90e38aaf5)' > vanilla_uuids.txt
printf '%s\n' 'There are 0 of a max of 20 players online: ' > paper_empty.txt
printf '%s\n' 'There are 2 of a max of 20 players online: Alex, Steve' > no_uuids.txt
printf '%s\n' '§6There are §c1§6 of a max of §c20§6 players online: §fAlex (8667BA71-B85A-4004-AF54-457A9734EED7)§r' > formatted.txt
printf '%s\n%s\n' 'There are 2/20 players online:' 'Alex, Steve' > bukkit_slash.txt
printf '%s\n%s\n' 'There are 1 out of maximum 50 players online.' 'Alex' > bukkit_outof.txt
printf '%s\n' 'Online (2): Alex, Steve' > foreign.txt
printf '%s\n' 'Unknown or incomplete command, see below for error' > unknown_command.txt
cd -
```

- [ ] **Step 2: Parser-Test schreiben**

`internal/console/listparse_test.go` (internes Paket, weil `parseList` unexportiert ist):

```go
package console

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseList(t *testing.T) {
	type want struct {
		ok      bool
		max     int
		players string // "Name=uuid|Name=" (empty uuid = null)
	}
	cases := map[string]want{
		"vanilla_uuids.txt":   {true, 20, "Alex=8667ba71-b85a-4004-af54-457a9734eed7|Steve=069a79f4-44e9-4726-a5be-fca90e38aaf5"},
		"paper_empty.txt":     {true, 20, ""},
		"no_uuids.txt":        {true, 20, "Alex=|Steve="},
		"formatted.txt":       {true, 20, "Alex=8667ba71-b85a-4004-af54-457a9734eed7"},
		"bukkit_slash.txt":    {true, 20, "Alex=|Steve="},
		"bukkit_outof.txt":    {true, 50, "Alex="},
		"foreign.txt":         {false, 0, ""},
		"unknown_command.txt": {false, 0, ""},
	}
	files, err := filepath.Glob(filepath.Join("testdata", "list", "*.txt"))
	if err != nil || len(files) != len(cases) {
		t.Fatalf("testdata files = %v (%v), want %d", files, err, len(cases))
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		w, ok := cases[filepath.Base(f)]
		if !ok {
			t.Fatalf("no expectation for %s", f)
		}
		players, maxP, gotOK := parseList(string(raw))
		if gotOK != w.ok || maxP != w.max || render(players) != w.players {
			t.Errorf("%s: ok=%v max=%d players=%q, want ok=%v max=%d players=%q",
				filepath.Base(f), gotOK, maxP, render(players), w.ok, w.max, w.players)
		}
	}
}

func TestParseListEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"", false},
		{"There are 99999999999999999999 of a max of 20 players online: ", false}, // overflow
		{"There are 1 of a max of 20 players online: Alex (not-a-uuid)", true},     // name kept as is
		{"  There are 0 of a max of 5 players online:  \r\n", true},
	} {
		if _, _, ok := parseList(tc.in); ok != tc.ok {
			t.Errorf("parseList(%q) ok = %v, want %v", tc.in, ok, tc.ok)
		}
	}
	// at most 500 entries, names cut to 64 runes
	long := "There are 600 of a max of 1000 players online: " + strings.Repeat(strings.Repeat("x", 80)+", ", 600)
	players, _, ok := parseList(long)
	if !ok || len(players) != maxListedPlayers || len([]rune(players[0].Name)) != maxPlayerName {
		t.Fatalf("long list: ok=%v len=%d name=%d", ok, len(players), len([]rune(players[0].Name)))
	}
	if p, _, _ := parseList("There are 1 of a max of 20 players online: Alex (not-a-uuid)"); p[0].Name != "Alex (not-a-uuid)" || p[0].UUID != nil {
		t.Fatalf("invalid uuid: %+v", p[0])
	}
}

func render(ps []onlinePlayer) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		u := ""
		if p.UUID != nil {
			u = *p.UUID
		}
		parts[i] = p.Name + "=" + u
	}
	return strings.Join(parts, "|")
}
```

- [ ] **Step 3: Handler-Tests schreiben**

`internal/console/players_test.go`:

```go
package console_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const unavailableJSON = `{"available":false,"online":[],"max":null}`

func TestOnlinePlayers(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	e.agent.setOutput("list uuids", "There are 1 of a max of 20 players online: Alex (8667ba71-b85a-4004-af54-457a9734eed7)\n")
	viewer := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, viewer, id, "server.view")
	vp := userP(viewer, "viewer")

	rec := e.do(http.MethodGet, srvPath(id, "/players"), nil, &vp)
	want := `{"available":true,"online":[{"name":"Alex","uuid":"8667ba71-b85a-4004-af54-457a9734eed7"}],"max":20}`
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("players: %d %s", rec.Code, rec.Body)
	}
	if got := e.agent.takeRcons(); len(got) != 1 || got[0] != fmt.Sprintf("%d list uuids", id) {
		t.Fatalf("rcon calls = %v", got)
	}
	if got := e.rec.Actions(); len(got) != 0 {
		t.Fatalf("reading online players must not be audited: %v", got)
	}

	other := storetest.CreateUser(t, e.db, "other", "user")
	op := userP(other, "other")
	if rec := e.do(http.MethodGet, srvPath(id, "/players"), nil, &op); rec.Code != http.StatusForbidden {
		t.Fatalf("without view: %d", rec.Code)
	}
	stub := storetest.CreateServer(t, e.db, "stub")
	admin := e.admin()
	if rec := e.do(http.MethodGet, srvPath(stub, "/players"), nil, &admin); rec.Code != http.StatusNotFound || errCode(t, rec) != httpx.CodeNotFound {
		t.Fatalf("not configured: %d %s", rec.Code, rec.Body)
	}
}

func TestOnlinePlayersCache(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	e.agent.setOutput("list uuids", "There are 0 of a max of 20 players online: ")
	admin := e.admin()
	for range 3 {
		e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin)
	}
	if got := e.agent.takeRcons(); len(got) != 1 {
		t.Fatalf("within 10 s: %d rcon calls, want 1", len(got))
	}
	e.clk.Advance(9 * time.Second)
	e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin)
	if got := e.agent.takeRcons(); len(got) != 0 {
		t.Fatalf("after 9 s: %v", got)
	}
	e.clk.Advance(time.Second)
	e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin)
	if got := e.agent.takeRcons(); len(got) != 1 {
		t.Fatalf("after 10 s: %d rcon calls, want 1", len(got))
	}
}

// Review Focus #5: nothing about the answer may produce a 500.
func TestOnlinePlayersUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
	}{
		{"not running", "", &agent.StatusError{StatusCode: 409, Code: agentapi.ErrorCodeNotRunning}},
		{"rcon not ready", "", &agent.StatusError{StatusCode: 503, Code: agentapi.ErrorCodeRconUnavailable}},
		{"docker down", "", &agent.StatusError{StatusCode: 503, Code: agentapi.ErrorCodeDockerUnavailable}},
		{"agent down", "", fmt.Errorf("%w: dial", agentapi.ErrUnavailable)},
		{"unknown format", "Online (2): Alex, Steve", nil},
		{"plugin error", "An internal error occurred while attempting to perform this command", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			id := createInstance(t, e.db, "survival")
			e.agent.setOutput("list uuids", tc.out)
			e.agent.setRconErr("list uuids", tc.err)
			admin := e.admin()
			rec := e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin)
			if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != unavailableJSON {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		})
	}
	e := newEnvWith(t, func(d *console.Deps) { d.Agent = nil })
	id := createInstance(t, e.db, "survival")
	admin := e.admin()
	if rec := e.do(http.MethodGet, srvPath(id, "/players"), nil, &admin); strings.TrimSpace(rec.Body.String()) != unavailableJSON {
		t.Fatalf("no agent: %d %s", rec.Code, rec.Body)
	}
}
```

`internal/console/players_internal_test.go` (internes Paket: ein Aufrufer, dessen Kontext schon abgebrochen ist, käme über HTTP gar nicht bis zum agent, weil vorher die DB-Abfrage scheitert):

```go
package console

import (
	"context"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
)

// countingAgent answers "list" and fails for a cancelled context, like the real client.
type countingAgent struct {
	noAgent
	calls int
}

func (a *countingAgent) Rcon(ctx context.Context, _ int64, _ string) (string, error) {
	a.calls++
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "There are 0 of a max of 20 players online: ", nil
}

// An answer computed for a caller that went away is not cached.
func TestOnlineNotCachedForCancelledCaller(t *testing.T) {
	ag := &countingAgent{}
	h := NewHandler(Deps{Clock: clock.NewFake(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)), Agent: ag})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := h.onlinePlayers(ctx, 1); got.Available {
		t.Fatalf("cancelled caller got %+v", got)
	}
	if got := h.onlinePlayers(context.Background(), 1); !got.Available || ag.calls != 2 {
		t.Fatalf("second caller: %+v after %d calls, want a fresh answer (2 calls)", got, ag.calls)
	}
	_ = h.onlinePlayers(context.Background(), 1)
	if ag.calls != 2 {
		t.Fatalf("third caller: %d calls, want the cached answer", ag.calls)
	}
}
```

- [ ] **Step 4: Implementierung**

`internal/console/listparse.go`:

```go
package console

import (
	"regexp"
	"strconv"
	"strings"
)

// onlinePlayer is the OnlinePlayer schema.
type onlinePlayer struct {
	Name string  `json:"name"`
	UUID *string `json:"uuid"`
}

const (
	maxListedPlayers = 500
	maxPlayerName    = 64 // runes; Minecraft names have at most 16, the rest is defensive
)

var (
	// listHeaders match the first line of "list" answers: vanilla (also Paper), Bukkit, Spigot.
	listHeaders = []*regexp.Regexp{
		regexp.MustCompile(`^There are (\d+) of a max(?:imum)? of (\d+) players online:?`),
		regexp.MustCompile(`^There are (\d+)/(\d+) players online:?`),
		regexp.MustCompile(`^There are (\d+) out of maximum (\d+) players online[.:]?`),
	}
	nameAndUUID = regexp.MustCompile(`^(.+) \(([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\)$`)
)

// parseList parses the answer of "list uuids" (spec §6.3). Formatting codes (§x) are removed
// first. ok is false for any other text, including a count that does not fit into an int.
func parseList(out string) (players []onlinePlayer, maxPlayers int, ok bool) {
	s := strings.TrimSpace(stripFormatting(out))
	for _, re := range listHeaders {
		m := re.FindStringSubmatchIndex(s)
		if m == nil {
			continue
		}
		if _, err := strconv.Atoi(s[m[2]:m[3]]); err != nil {
			return nil, 0, false
		}
		maxP, err := strconv.Atoi(s[m[4]:m[5]])
		if err != nil {
			return nil, 0, false
		}
		return splitNames(s[m[1]:]), maxP, true
	}
	return nil, 0, false
}

// splitNames splits "A (uuid), B" (comma or line separated) into players.
func splitNames(rest string) []onlinePlayer {
	players := []onlinePlayer{}
	for _, f := range strings.FieldsFunc(rest, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		p := onlinePlayer{Name: f}
		if m := nameAndUUID.FindStringSubmatch(f); m != nil {
			id := strings.ToLower(m[2])
			p.Name, p.UUID = m[1], &id
		}
		if r := []rune(p.Name); len(r) > maxPlayerName {
			p.Name = string(r[:maxPlayerName])
		}
		players = append(players, p)
		if len(players) == maxListedPlayers {
			break
		}
	}
	return players
}

// stripFormatting removes Minecraft formatting codes: '§' and the rune after it.
func stripFormatting(s string) string {
	if !strings.ContainsRune(s, '§') {
		return s
	}
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch {
		case skip:
			skip = false
		case r == '§':
			skip = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
```

`internal/console/players.go`:

```go
package console

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

const (
	onlineTTL     = 10 * time.Second
	onlineTimeout = 5 * time.Second
	listCommand   = "list uuids"
)

// onlinePlayers is the OnlinePlayers schema.
type onlinePlayers struct {
	Available bool           `json:"available"`
	Online    []onlinePlayer `json:"online"`
	Max       *int           `json:"max"`
}

func unavailablePlayers() onlinePlayers { return onlinePlayers{Online: []onlinePlayer{}} }

// onlineCache keeps the last answer per server for onlineTTL (spec §6.3).
type onlineCache struct {
	mu      sync.Mutex
	entries map[int64]*onlineEntry
}

type onlineEntry struct {
	mu  sync.Mutex // held while one request asks the agent; concurrent ones wait and reuse it
	at  time.Time
	val onlinePlayers
	set bool
}

func newOnlineCache() *onlineCache { return &onlineCache{entries: map[int64]*onlineEntry{}} }

func (c *onlineCache) entry(id int64) *onlineEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok {
		e = &onlineEntry{}
		c.entries[id] = e
	}
	return e
}

// forget drops the cached answer of a server (after a player action changed it).
func (c *onlineCache) forget(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}

// handleOnlinePlayers answers who is online; it never fails because of the agent (decision 2).
func (h *Handler) handleOnlinePlayers(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.onlinePlayers(r.Context(), s.ID))
}

func (h *Handler) onlinePlayers(ctx context.Context, id int64) onlinePlayers {
	e := h.online.entry(id)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := h.clock.Now()
	if e.set && now.Sub(e.at) < onlineTTL {
		return e.val
	}
	val, cacheable := h.askOnline(ctx, id)
	if cacheable {
		e.val, e.at, e.set = val, now, true
	}
	return val
}

// askOnline runs "list uuids"; cacheable is false if the caller's context ended meanwhile.
func (h *Handler) askOnline(ctx context.Context, id int64) (onlinePlayers, bool) {
	cctx, cancel := context.WithTimeout(ctx, onlineTimeout)
	defer cancel()
	out, err := h.agent.Rcon(cctx, id, listCommand)
	if err != nil {
		h.log.DebugContext(ctx, "online players unavailable", "server", id, "err", err)
		return unavailablePlayers(), ctx.Err() == nil
	}
	players, maxP, ok := parseList(out)
	if !ok {
		h.log.WarnContext(ctx, "unrecognised answer to list", "server", id)
		return unavailablePlayers(), true
	}
	return onlinePlayers{Available: true, Online: players, Max: &maxP}, true
}
```

In `internal/console/handler.go` das Feld `online *onlineCache` an `Handler` anhängen und in `NewHandler` mit `online: newOnlineCache(),` initialisieren:

```go
type Handler struct {
	q      *store.Queries
	clock  clock.Clock
	rec    audit.Recorder
	agent  agentapi.ConsoleClient
	access Access
	log    *slog.Logger
	limit  *userLimiter
	online *onlineCache
}
```

```go
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, rec: d.Audit, agent: ag, access: acc, log: log,
		limit: newUserLimiter(), online: newOnlineCache(),
	}
```

In `internal/console/stubs.go` die Zeile `func (h *Handler) handleOnlinePlayers…` löschen.

- [ ] **Step 5: Lokal prüfen**

Run: `make check`
Expected: OK.

- [ ] **Step 6: Commit, Push, CI**

```bash
git add internal/console
git commit -m "feat(console): online players from list uuids with a 10 s cache

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI grün (`TestParseList*`, `TestOnlinePlayers*`).

---

### Task 11: Spielerlisten und Spieler-Aktionen

**Files:**
- Create: `internal/console/actions.go`
- Modify: `internal/console/stubs.go` (Stubs `handlePlayerLists`, `handlePlayerAction` entfernen)
- Test: `internal/console/actions_test.go`

**Interfaces:**
- Consumes: `Handler.loadServer`, `Handler.run`, `writeAgentError`, `onlineCache.forget`, `agentapi.ConsoleClient.Players`, `agentapi.ValidateCommand`, `audit.ActionServerPlayerAction` (Phase A)
- Produces (paketintern):
```go
type actionRequest struct { Action string; Name string; Reason *string }
func actionCommand(req actionRequest) (cmd, problem string)
```

- [ ] **Step 1: Tests schreiben**

`internal/console/actions_test.go`:

```go
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
```

`TestPlayerActionCommands` sendet neun Aktionen hintereinander; der Limiter erlaubt 10 im Burst. Tests mit mehr als zehn Befehlen/Aktionen pro Benutzer müssen `e.clk.Advance` nutzen.

- [ ] **Step 2: Implementierung**

`internal/console/actions.go`:

```go
package console

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// handlePlayerLists returns ops, whitelist and bans as the server stores them (spec §6.1).
func (h *Handler) handlePlayerLists(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	res, err := h.agent.Players(r.Context(), s.ID)
	switch {
	case errors.Is(err, agentapi.ErrNotFound):
		res = agentapi.PlayersResponse{} // data directory not created yet: nothing stored
	case err != nil:
		writeAgentError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, normalizeLists(res))
}

// normalizeLists turns nil slices into empty ones (JSON [] instead of null).
func normalizeLists(p agentapi.PlayersResponse) agentapi.PlayersResponse {
	if p.Ops == nil {
		p.Ops = []agentapi.PlayerOp{}
	}
	if p.Whitelist == nil {
		p.Whitelist = []agentapi.PlayerEntry{}
	}
	if p.Bans == nil {
		p.Bans = []agentapi.PlayerBan{}
	}
	if p.Errors == nil {
		p.Errors = []string{}
	}
	return p
}

type actionRequest struct {
	Action string  `json:"action"`
	Name   string  `json:"name"`
	Reason *string `json:"reason"`
}

const maxReasonRunes = 200

var playerName = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

// actionVerbs maps the PlayerAction enum to the Minecraft command (spec §6.5). User text never
// becomes a verb.
var actionVerbs = map[string]string{
	"kick":             "kick",
	"ban":              "ban",
	"pardon":           "pardon",
	"op":               "op",
	"deop":             "deop",
	"whitelist_add":    "whitelist add",
	"whitelist_remove": "whitelist remove",
}

func takesReason(action string) bool { return action == "kick" || action == "ban" }

// validReason: at most 200 runes, valid UTF-8, only printable runes (no control characters,
// no line or paragraph separators).
func validReason(s string) bool {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxReasonRunes {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// actionCommand builds the RCON command of req, or returns why req is invalid.
func actionCommand(req actionRequest) (cmd, problem string) {
	verb, ok := actionVerbs[req.Action]
	if !ok {
		return "", "action must be one of kick, ban, pardon, op, deop, whitelist_add, whitelist_remove"
	}
	if !playerName.MatchString(req.Name) {
		return "", "name must be 3-16 letters, digits or underscores"
	}
	cmd = verb + " " + req.Name
	if req.Reason != nil {
		if !takesReason(req.Action) {
			return "", "reason is only allowed for kick and ban"
		}
		if !validReason(*req.Reason) {
			return "", "reason must be at most 200 printable characters"
		}
		if reason := strings.TrimSpace(*req.Reason); reason != "" {
			cmd += " " + reason
		}
	}
	// Defence in depth: the result must be a valid single command like any console input.
	if err := agentapi.ValidateCommand(cmd); err != nil {
		return "", err.Error()
	}
	return cmd, ""
}

// handlePlayerAction runs one player action over RCON (spec §6.5).
func (h *Handler) handlePlayerAction(w http.ResponseWriter, r *http.Request) {
	var req actionRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	cmd, problem := actionCommand(req)
	if problem != "" {
		writeValidation(w, r, problem)
		return
	}
	details := map[string]any{"action": req.Action, "name": req.Name}
	if req.Reason != nil {
		if reason := strings.TrimSpace(*req.Reason); reason != "" {
			details["reason"] = reason
		}
	}
	if h.run(w, r, s.ID, cmd, audit.ActionServerPlayerAction, details) {
		h.online.forget(s.ID)
	}
}
```

In `internal/console/stubs.go` die Zeilen `handlePlayerLists` und `handlePlayerAction` löschen.

- [ ] **Step 3: Lokal prüfen**

Run: `make check`
Expected: OK.

- [ ] **Step 4: Commit, Push, CI**

```bash
git add internal/console
git commit -m "feat(console): player lists and player actions with fixed command table

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI grün (`TestPlayer*`).

---

### Task 12: Log-Stream (SSE), erneute Prüfung und `auth.Sessions.Recheck`

**Files:**
- Create: `internal/console/timers.go`, `internal/console/stream.go`, `internal/console/access.go`, `internal/auth/recheck.go`, `internal/console/export_test.go`
- Modify: `internal/console/handler.go` (Felder `streams`, `timers`, `ioClock`; `Shutdown`), `internal/console/helpers_test.go` (Fake-Zeitgeber im `env`)
- Delete: `internal/console/stubs.go`
- Test: `internal/console/faketimers_test.go`, `internal/console/stream_test.go`, `internal/console/access_test.go`, `internal/auth/recheck_test.go`

**Interfaces:**
- Consumes: `agentapi.ConsoleClient.Logs`, `agentapi.LogStream` (inkl. `Running() bool` aus dem agent-Header `X-Container-Running`), `agentapi.LogsRequest`, `agentapi.MaxLogTail` (V1); `store.Queries.AuthGetSessionByID`, `AuthGetUserByID`, `ServersGetInstance` (vorhanden); `rbac.Checker`
- Produces:
```go
// internal/auth
func (s *Sessions) Recheck(ctx context.Context, sessionID, userID int64) (store.User, error) // ErrNoSession

// internal/console
type SessionChecker interface {
	Recheck(ctx context.Context, sessionID, userID int64) (store.User, error)
}
func NewAccess(s SessionChecker, c rbac.Checker) Access
func (h *Handler) Shutdown() // closes all open streams; new streams get 503 afterwards
// export_test.go
type Timers = timers
func SetTimers(h *Handler, t Timers)
```

- [ ] **Step 1: `auth.Sessions.Recheck` testen**

`internal/auth/recheck_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestRecheck(t *testing.T) {
	db := storetest.Open(t)
	clk := clock.NewFake(sessionStart)
	q := store.New(db)
	s := NewSessions(q, clk, config.DefaultPanel().Session)
	ctx := context.Background()
	alice := storetest.CreateUser(t, db, "alice", "user")
	bob := storetest.CreateUser(t, db, "bob", "user")
	root := storetest.CreateUser(t, db, "root", "admin") // admin without 2FA → effective stage setup

	_, full := mustCreate(t, s, alice, principal.StageFull)
	u, err := s.Recheck(ctx, full.ID, alice)
	if err != nil || u.ID != alice || u.Username != "alice" {
		t.Fatalf("valid session: %+v, %v", u, err)
	}
	_, pre := mustCreate(t, s, alice, principal.StagePreAuth)
	_, adminSess := mustCreate(t, s, root, principal.StageFull)
	_, revoked := mustCreate(t, s, alice, principal.StageFull)
	if err := s.Revoke(ctx, revoked.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		sessID, user  int64
	}{
		{"unknown session", 99999, alice},
		{"other user's session", full.ID, bob},
		{"pre-auth stage", pre.ID, alice},
		{"admin without 2FA (setup)", adminSess.ID, root},
		{"revoked", revoked.ID, alice},
	} {
		if _, err := s.Recheck(ctx, tc.sessID, tc.user); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s: err = %v, want ErrNoSession", tc.name, err)
		}
	}

	// idle timeout (30 min without a request)
	clk.Advance(31 * time.Minute)
	if _, err := s.Recheck(ctx, full.ID, alice); !errors.Is(err, ErrNoSession) {
		t.Fatalf("idle session: err = %v", err)
	}

	// deleted user
	clk.Set(sessionStart)
	_, bobSess := mustCreate(t, s, bob, principal.StageFull)
	if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recheck(ctx, bobSess.ID, bob); !errors.Is(err, ErrNoSession) {
		t.Fatalf("deleted user: err = %v", err)
	}
}
```

- [ ] **Step 2: `Recheck` implementieren**

`internal/auth/recheck.go`:

```go
package auth

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Recheck reports whether session sessionID still fully authenticates userID: it exists,
// belongs to userID, is neither revoked nor expired nor idle, and its effective stage is full
// (including the downgrade of effectiveStage). It returns the current user row, so callers see
// role changes. Long-lived requests (console streams) call it periodically; it never touches
// the session. Any failure of the session itself is ErrNoSession.
func (s *Sessions) Recheck(ctx context.Context, sessionID, userID int64) (store.User, error) {
	sess, err := s.q.AuthGetSessionByID(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.User{}, ErrNoSession
	}
	if err != nil {
		return store.User{}, err
	}
	if sess.UserID != userID || !s.valid(sess) {
		return store.User{}, ErrNoSession
	}
	user, err := s.q.AuthGetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return store.User{}, ErrNoSession
	}
	if err != nil {
		return store.User{}, err
	}
	stage, err := s.effectiveStage(ctx, user, sess)
	if err != nil {
		return store.User{}, err
	}
	if stage != principal.StageFull {
		return store.User{}, ErrNoSession
	}
	return user, nil
}
```

- [ ] **Step 3: `Access` testen**

`internal/console/access_test.go`:

```go
package console_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type fakeSessions struct {
	user store.User
	err  error
}

func (f fakeSessions) Recheck(context.Context, int64, int64) (store.User, error) { return f.user, f.err }

type failingChecker struct{}

func (failingChecker) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, errors.New("db down")
}

func TestAccess(t *testing.T) {
	db := storetest.Open(t)
	ctx := context.Background()
	srv := storetest.CreateServer(t, db, "survival")
	alice := storetest.CreateUser(t, db, "alice", "user")
	grant(t, db, alice, srv, "server.view", "server.console")
	bob := storetest.CreateUser(t, db, "bob", "user")
	grant(t, db, bob, srv, "server.view")
	checker := rbac.NewDBChecker(db)
	p := principal.Principal{UserID: alice, SessionID: 7, Role: principal.RoleUser, Stage: principal.StageFull}
	sessionGone := errors.New("auth: no valid session")

	for _, tc := range []struct {
		name    string
		s       console.SessionChecker
		c       rbac.Checker
		wantErr error // nil = allowed
		anyErr  bool
	}{
		{"user with permission", fakeSessions{user: store.User{ID: alice, Role: "user"}}, checker, nil, false},
		{"session gone", fakeSessions{err: sessionGone}, checker, sessionGone, false},
		{"permission revoked", fakeSessions{user: store.User{ID: bob, Role: "user"}}, checker, console.ErrAccessRevoked, false},
		{"admin needs no grant", fakeSessions{user: store.User{ID: bob, Role: "admin"}}, checker, nil, false},
		{"checker fails", fakeSessions{user: store.User{ID: alice, Role: "user"}}, failingChecker{}, nil, true},
	} {
		err := console.NewAccess(tc.s, tc.c).Recheck(ctx, p, srv, rbac.PermServerConsole)
		switch {
		case tc.anyErr:
			if err == nil {
				t.Errorf("%s: err = nil, want an error", tc.name)
			}
		case tc.wantErr == nil && err != nil:
			t.Errorf("%s: err = %v, want nil", tc.name, err)
		case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
		}
	}
}
```

- [ ] **Step 4: `Access` implementieren**

`internal/console/access.go`:

```go
package console

import (
	"context"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

// SessionChecker is the part of *auth.Sessions the stream recheck needs.
type SessionChecker interface {
	// Recheck returns the current user row while sessionID still fully authenticates userID.
	Recheck(ctx context.Context, sessionID, userID int64) (store.User, error)
}

// NewAccess re-checks session and permission from the database (spec §6.2): the role comes from
// the current user row, so a demoted admin needs a real grant.
func NewAccess(s SessionChecker, c rbac.Checker) Access { return access{sessions: s, perms: c} }

type access struct {
	sessions SessionChecker
	perms    rbac.Checker
}

func (a access) Recheck(ctx context.Context, p principal.Principal, serverID int64, perm rbac.Permission) error {
	u, err := a.sessions.Recheck(ctx, p.SessionID, p.UserID)
	if err != nil {
		return err
	}
	if principal.Role(u.Role) == principal.RoleAdmin {
		return nil
	}
	ok, err := a.perms.HasServerPermission(ctx, u.ID, serverID, perm)
	if err != nil {
		return fmt.Errorf("console: recheck permission: %w", err)
	}
	if !ok {
		return ErrAccessRevoked
	}
	return nil
}
```

- [ ] **Step 5: Fake-Zeitgeber und Export für Tests**

`internal/console/export_test.go`:

```go
package console

// Timers lets the tests replace the stream timers (heartbeat, recheck, reconnect).
type Timers = timers

func SetTimers(h *Handler, t Timers) { h.timers = t }
```

`internal/console/faketimers_test.go`:

```go
package console_test

import (
	"sync"
	"testing"
	"time"
)

// fakeTimers hands out one unbuffered channel per duration (3 s reconnect, 15 s heartbeat,
// 30 s recheck); fire delivers exactly one tick to whichever stream waits on it.
type fakeTimers struct {
	mu sync.Mutex
	ch map[time.Duration]chan time.Time
}

func newFakeTimers() *fakeTimers { return &fakeTimers{ch: map[time.Duration]chan time.Time{}} }

func (f *fakeTimers) get(d time.Duration) chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.ch[d]
	if !ok {
		c = make(chan time.Time)
		f.ch[d] = c
	}
	return c
}

func (f *fakeTimers) Every(d time.Duration) (<-chan time.Time, func()) { return f.get(d), func() {} }
func (f *fakeTimers) After(d time.Duration) (<-chan time.Time, func()) { return f.get(d), func() {} }

// fire blocks until a stream takes the tick for d (at most 5 s).
func (f *fakeTimers) fire(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case f.get(d) <- t0:
	case <-time.After(5 * time.Second):
		t.Fatalf("no stream waits for the %v timer", d)
	}
}
```

In `internal/console/helpers_test.go`:
- das Feld `timers *fakeTimers` an `env` anhängen,
- in `newEnvWith` `timers: newFakeTimers()` in das `&env{…}`-Literal aufnehmen und direkt nach `e.h = console.NewHandler(d)` die Zeile `console.SetTimers(e.h, e.timers)` einfügen.

- [ ] **Step 6: Stream-Tests schreiben**

`internal/console/stream_test.go`:

```go
package console_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/console"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// streamServer serves e.mux over real HTTP (streams need flushing); the principal comes from
// the X-Test-User header and e.principals.
func (e *env) streamServer(configure func(*http.Server)) *httptest.Server {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		id, _ := strconv.ParseInt(r.Header.Get("X-Test-User"), 10, 64)
		if p, ok := e.principals.Load(id); ok {
			ctx = principal.With(ctx, p.(principal.Principal))
		}
		e.mux.ServeHTTP(w, r.WithContext(ctx))
	}))
	if configure != nil {
		configure(srv.Config)
	}
	srv.Start()
	e.t.Cleanup(srv.Close)
	return srv
}

type sseEvent struct{ Event, ID, Data, Comment string }

type sseConn struct {
	t      *testing.T
	resp   *http.Response
	body   []byte // non-200 answers
	cancel context.CancelFunc
	events chan sseEvent
}

func (e *env) openStream(srv *httptest.Server, p principal.Principal, id int64, query, lastEventID string) *sseConn {
	e.t.Helper()
	e.principals.Store(p.UserID, p)
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+srvPath(id, "/console/stream")+query, nil)
	if err != nil {
		cancel()
		e.t.Fatal(err)
	}
	req.Header.Set("X-Test-User", strconv.FormatInt(p.UserID, 10))
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		e.t.Fatal(err)
	}
	c := &sseConn{t: e.t, resp: resp, cancel: cancel, events: make(chan sseEvent, 256)}
	e.t.Cleanup(c.close)
	if resp.StatusCode == http.StatusOK {
		go c.read()
	} else {
		c.body, _ = io.ReadAll(resp.Body)
		close(c.events)
	}
	return c
}

func (c *sseConn) read() {
	defer close(c.events)
	sc := bufio.NewScanner(c.resp.Body)
	var ev sseEvent
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if ev != (sseEvent{}) {
				c.events <- ev
			}
			ev = sseEvent{}
		case strings.HasPrefix(line, ":"):
			ev.Comment = strings.TrimSpace(line[1:])
		case strings.HasPrefix(line, "event: "):
			ev.Event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "id: "):
			ev.ID = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "data: "):
			ev.Data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func (c *sseConn) close() {
	c.cancel()
	_ = c.resp.Body.Close()
}

func (c *sseConn) next() sseEvent {
	c.t.Helper()
	select {
	case ev, ok := <-c.events:
		if !ok {
			c.t.Fatal("stream ended unexpectedly")
		}
		return ev
	case <-time.After(5 * time.Second):
		c.t.Fatal("no event within 5 s")
	}
	return sseEvent{}
}

// expectClosed waits until the panel ends the stream (events before that are ignored).
func (c *sseConn) expectClosed() {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-c.events:
			if !ok {
				return
			}
		case <-deadline:
			c.t.Fatal("stream still open after 5 s")
		}
	}
}

func (c *sseConn) errCode() string {
	var b httpx.ErrorBody
	_ = json.Unmarshal(c.body, &b)
	return b.Error
}

func expectState(t *testing.T, ev sseEvent, running bool) {
	t.Helper()
	if want := fmt.Sprintf(`{"running":%t}`, running); ev.Event != "state" || ev.Data != want {
		t.Fatalf("event = %+v, want state %s", ev, want)
	}
}

func TestStreamHeadersAndLines(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s := e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")
	if c.resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d %s", c.resp.StatusCode, c.body)
	}
	h := c.resp.Header
	if h.Get("Content-Type") != "text/event-stream" || h.Get("Cache-Control") != "no-store" || h.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers = %v", h)
	}
	expectState(t, c.next(), true) // right after opening, from Running()
	s.send(t, agentapi.LogLine{TS: 1_760_184_000_123_456_789, Stream: agentapi.StreamOut, Line: `[12:00:00 INFO]: Done (3.1s)! "quoted"`})
	s.send(t, agentapi.LogLine{TS: 1_760_184_000_223_456_789, Stream: agentapi.StreamErr, Line: "warn"})
	ev := c.next()
	if ev.Event != "line" || ev.ID != "1760184000123456789" ||
		ev.Data != `{"ts":1760184000123,"stream":"out","line":"[12:00:00 INFO]: Done (3.1s)! \"quoted\""}` {
		t.Fatalf("line event = %+v", ev)
	}
	if ev := c.next(); ev.ID != "1760184000223456789" || !strings.Contains(ev.Data, `"stream":"err"`) {
		t.Fatalf("second line = %+v", ev)
	}
	if reqs := e.agent.requests(); len(reqs) != 1 || reqs[0] != (agentapi.LogsRequest{ID: id, Tail: 500}) {
		t.Fatalf("agent requests = %+v", reqs)
	}
}

func TestStreamLastEventID(t *testing.T) {
	for _, tc := range []struct {
		query, lastID string
		want          agentapi.LogsRequest
	}{
		{"", "42", agentapi.LogsRequest{Tail: 500, Since: 43}},
		{"?tail=20", "", agentapi.LogsRequest{Tail: 20}},
		{"?tail=0", "", agentapi.LogsRequest{Tail: 0}},
		{"", "abc", agentapi.LogsRequest{Tail: 500}},
		{"", "-5", agentapi.LogsRequest{Tail: 500}},
		{"", "0", agentapi.LogsRequest{Tail: 500}},
	} {
		e := newEnv(t)
		id := createInstance(t, e.db, "survival")
		srv := e.streamServer(nil)
		e.agent.prepare()
		c := e.openStream(srv, e.admin(), id, tc.query, tc.lastID)
		waitFor(t, "agent request", func() bool { return len(e.agent.requests()) == 1 })
		tc.want.ID = id
		if got := e.agent.requests()[0]; got != tc.want {
			t.Errorf("query %q Last-Event-ID %q: request %+v, want %+v", tc.query, tc.lastID, got, tc.want)
		}
		c.close()
	}
}

func TestStreamRejectsBadRequests(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	stub := storetest.CreateServer(t, e.db, "stub")
	viewer := storetest.CreateUser(t, e.db, "viewer", "user")
	grant(t, e.db, viewer, id, "server.view")
	admin, vp := e.admin(), userP(viewer, "viewer")
	for _, tc := range []struct {
		path   string
		p      *principal.Principal
		status int
		code   string
	}{
		{srvPath(id, "/console/stream?tail=1001"), &admin, 400, httpx.CodeValidation},
		{srvPath(id, "/console/stream?tail=-1"), &admin, 400, httpx.CodeValidation},
		{srvPath(id, "/console/stream?tail=x"), &admin, 400, httpx.CodeValidation},
		{srvPath(stub, "/console/stream"), &admin, 404, httpx.CodeNotFound},
		{srvPath(id, "/console/stream"), &vp, 403, httpx.CodeForbidden},
		{srvPath(id, "/console/stream"), nil, 401, httpx.CodeUnauthorized},
	} {
		rec := e.do(http.MethodGet, tc.path, nil, tc.p)
		if rec.Code != tc.status || errCode(t, rec) != tc.code {
			t.Errorf("%s: %d %s", tc.path, rec.Code, rec.Body)
		}
	}
	if reqs := e.agent.requests(); len(reqs) != 0 {
		t.Fatalf("agent asked: %+v", reqs)
	}
}

// Review Focus #4: state events, reconnect with since, no duplicate lines.
func TestStreamRunningStateAndReconnect(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s1 := e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")

	expectState(t, c.next(), true)
	s1.send(t, agentapi.LogLine{TS: 100, Stream: agentapi.StreamOut, Line: "a"})
	if ev := c.next(); ev.Event != "line" || ev.ID != "100" {
		t.Fatalf("first line = %+v", ev)
	}
	s1.finish() // container stopped
	expectState(t, c.next(), false)
	s1.waitClosed(t)

	s2 := e.agent.prepare()
	e.timers.fire(t, 3*time.Second)
	expectState(t, c.next(), true) // running again
	s2.send(t, agentapi.LogLine{TS: 100, Stream: agentapi.StreamOut, Line: "a"}) // repeated by the agent: dropped
	s2.send(t, agentapi.LogLine{TS: 101, Stream: agentapi.StreamOut, Line: "b"})
	if ev := c.next(); ev.Event != "line" || ev.ID != "101" {
		t.Fatalf("after reconnect = %+v", ev)
	}
	if reqs := e.agent.requests(); len(reqs) != 2 || reqs[1] != (agentapi.LogsRequest{ID: id, Tail: 500, Since: 101}) {
		t.Fatalf("requests = %+v", reqs)
	}
}

// A stopped container: the agent delivers the history and ends at once. No "running" flapping.
func TestStreamStoppedServerDoesNotFlap(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s1 := e.agent.prepareStopped()
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), false)
	s1.send(t, agentapi.LogLine{TS: 5, Stream: agentapi.StreamOut, Line: "old"})
	if ev := c.next(); ev.Event != "line" || ev.ID != "5" {
		t.Fatalf("history line = %+v", ev)
	}
	s1.finish() // still false: no event
	for range 3 {
		s := e.agent.prepareStopped()
		e.timers.fire(t, 3*time.Second)
		s.finish()
		s.waitClosed(t)
	}
	e.timers.fire(t, 15*time.Second)
	if ev := c.next(); ev.Comment != "ping" {
		t.Fatalf("expected only a ping after the reconnects, got %+v", ev)
	}
}

func TestStreamAgentDownRetries(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	e.agent.setLogsErr(fmt.Errorf("%w: dial", agentapi.ErrUnavailable))
	c := e.openStream(srv, e.admin(), id, "", "")
	if c.resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", c.resp.StatusCode)
	}
	expectState(t, c.next(), false)
	e.timers.fire(t, 3*time.Second)
	waitFor(t, "second attempt", func() bool { return len(e.agent.requests()) == 2 })
	e.agent.setLogsErr(nil)
	s := e.agent.prepare()
	e.timers.fire(t, 3*time.Second)
	expectState(t, c.next(), true) // no second state:false before it
	s.send(t, agentapi.LogLine{TS: 7, Stream: agentapi.StreamOut, Line: "back"})
	if ev := c.next(); ev.Event != "line" || ev.ID != "7" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestStreamHeartbeat(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), true)
	e.timers.fire(t, 15*time.Second)
	if ev := c.next(); ev.Comment != "ping" {
		t.Fatalf("event = %+v, want ping", ev)
	}
}

// Review Focus #3: permission revoked, session ended.
func TestStreamRecheckClosesStream(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s := e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), true)
	e.timers.fire(t, 30*time.Second) // allowed: stays open
	e.timers.fire(t, 15*time.Second)
	if ev := c.next(); ev.Comment != "ping" {
		t.Fatalf("event = %+v", ev)
	}
	e.access.set(console.ErrAccessRevoked)
	e.timers.fire(t, 30*time.Second)
	c.expectClosed()
	s.waitClosed(t)
	e.access.mu.Lock()
	defer e.access.mu.Unlock()
	if len(e.access.perms) != 2 || e.access.perms[0] != "server.console" {
		t.Fatalf("recheck perms = %v", e.access.perms)
	}
}

func TestStreamClosesOnDeletedServer(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	e.agent.setLogsErr(fmt.Errorf("%w: dial", agentapi.ErrUnavailable))
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), false)
	mustExec(t, e.db, `DELETE FROM servers WHERE id = ?`, id)
	e.timers.fire(t, 30*time.Second) // while waiting to reconnect
	c.expectClosed()
}

func TestStreamClientGoneClosesAgentStream(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s := e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), true)
	s.send(t, agentapi.LogLine{TS: 1, Stream: agentapi.StreamOut, Line: "x"})
	if ev := c.next(); ev.Event != "line" {
		t.Fatalf("event = %+v", ev)
	}
	c.close()
	s.waitClosed(t)
}

func TestStreamLimitPerUser(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	op := storetest.CreateUser(t, e.db, "op", "user")
	grant(t, e.db, op, id, "server.view", "server.console")
	opP := userP(op, "op")
	var open []*sseConn
	for i := range 4 {
		c := e.openStream(srv, opP, id, "", "")
		if c.resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: %d", i+1, c.resp.StatusCode)
		}
		open = append(open, c)
	}
	c5 := e.openStream(srv, opP, id, "", "")
	if c5.resp.StatusCode != http.StatusTooManyRequests || c5.errCode() != httpx.CodeRateLimited {
		t.Fatalf("5th stream: %d %s", c5.resp.StatusCode, c5.body)
	}
	if c := e.openStream(srv, e.admin(), id, "", ""); c.resp.StatusCode != http.StatusOK {
		t.Fatalf("other user: %d", c.resp.StatusCode)
	}
	open[0].close()
	waitFor(t, "a free slot after closing a stream", func() bool {
		c := e.openStream(srv, opP, id, "", "")
		defer c.close()
		return c.resp.StatusCode == http.StatusOK
	})
}

func TestShutdownClosesStreams(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(nil)
	s := e.agent.prepare()
	c1 := e.openStream(srv, e.admin(), id, "", "")
	c2 := e.openStream(srv, e.admin(), id, "", "")
	waitFor(t, "both streams asked the agent", func() bool { return len(e.agent.requests()) == 2 })
	e.h.Shutdown()
	c1.expectClosed()
	c2.expectClosed()
	s.waitClosed(t)
	c3 := e.openStream(srv, e.admin(), id, "", "")
	if c3.resp.StatusCode != http.StatusServiceUnavailable || c3.errCode() != httpx.CodeAgentUnavailable {
		t.Fatalf("after shutdown: %d %s", c3.resp.StatusCode, c3.body)
	}
}

// The panel's http.Server has ReadTimeout 30 s and WriteTimeout 60 s; a stream must outlive both.
func TestStreamSurvivesServerTimeouts(t *testing.T) {
	e := newEnv(t)
	id := createInstance(t, e.db, "survival")
	srv := e.streamServer(func(s *http.Server) {
		s.ReadTimeout = 100 * time.Millisecond
		s.WriteTimeout = 100 * time.Millisecond
	})
	s := e.agent.prepare()
	c := e.openStream(srv, e.admin(), id, "", "")
	expectState(t, c.next(), true)
	time.Sleep(300 * time.Millisecond)
	s.send(t, agentapi.LogLine{TS: 9, Stream: agentapi.StreamOut, Line: "late"})
	if ev := c.next(); ev.Event != "line" || ev.ID != "9" {
		t.Fatalf("event = %+v", ev)
	}
}
```

- [ ] **Step 7: Implementierung**

`internal/console/timers.go`:

```go
package console

import "time"

// timers creates the stream's timers; tests replace them (export_test.go).
type timers interface {
	// Every returns a channel that ticks every d and a stop function.
	Every(d time.Duration) (<-chan time.Time, func())
	// After returns a channel that fires once after d and a stop function.
	After(d time.Duration) (<-chan time.Time, func())
}

type realTimers struct{}

func (realTimers) Every(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

func (realTimers) After(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}
```

`internal/console/stream.go`:

```go
package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

// Stream parameters (spec §6.2).
const (
	defaultTail       = 500
	heartbeatInterval = 15 * time.Second
	recheckInterval   = 30 * time.Second
	reconnectDelay    = 3 * time.Second
	writeTimeout      = 30 * time.Second
	maxStreamsPerUser = 4
	lineBuffer        = 64
)

var (
	errTooManyStreams = errors.New("console: too many open streams")
	errShuttingDown   = errors.New("console: shutting down")
	errStop           = errors.New("console: stop stream")
)

// lineEvent and stateEvent are the data of the SSE events "line" and "state" (V3).
type lineEvent struct {
	TS     int64  `json:"ts"` // Unix ms
	Stream string `json:"stream"`
	Line   string `json:"line"`
}

type stateEvent struct {
	Running bool `json:"running"`
}

// streamSet counts open streams per user and cancels all of them on Shutdown.
type streamSet struct {
	mu      sync.Mutex
	closed  bool
	next    int
	perUser map[int64]int
	cancels map[int]context.CancelFunc
}

func newStreamSet() *streamSet {
	return &streamSet{perUser: map[int64]int{}, cancels: map[int]context.CancelFunc{}}
}

// acquire registers a stream of userID; release must be called when it ends.
func (s *streamSet) acquire(userID int64, cancel context.CancelFunc) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errShuttingDown
	}
	if s.perUser[userID] >= maxStreamsPerUser {
		return nil, errTooManyStreams
	}
	s.perUser[userID]++
	s.next++
	id := s.next
	s.cancels[id] = cancel
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.cancels, id)
		s.perUser[userID]--
		if s.perUser[userID] <= 0 {
			delete(s.perUser, userID)
		}
	}, nil
}

func (s *streamSet) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, cancel := range s.cancels {
		cancel()
	}
}

// handleStream serves the live log as Server-Sent Events (spec §6.2).
func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	s, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	req, problem := logsRequest(s.ID, r)
	if problem != "" {
		writeValidation(w, r, problem)
		return
	}
	p, _ := principal.From(r.Context()) // guaranteed by the rbac rule
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	release, err := h.streams.acquire(p.UserID, cancel)
	switch {
	case errors.Is(err, errTooManyStreams):
		httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many open consoles, close one first")
		return
	case err != nil:
		httpx.WriteError(w, r, http.StatusServiceUnavailable, httpx.CodeAgentUnavailable, "the panel is shutting down")
		return
	}
	defer release()

	rc := http.NewResponseController(w)
	// The server's ReadTimeout would cancel the request context of an idle connection after
	// 30 s; a stream is bounded by the recheck instead.
	if err := rc.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		h.log.DebugContext(ctx, "clear read deadline", "err", err)
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("X-Accel-Buffering", "no")
	sw := &sseWriter{w: w, rc: rc, clock: h.ioClock}
	if err := sw.deadline(); err != nil {
		return
	}
	w.WriteHeader(http.StatusOK)
	if err := sw.flush(); err != nil {
		return
	}
	ls := &logSession{h: h, ctx: ctx, sw: sw, p: p, serverID: s.ID, req: req}
	ls.run()
}

// logsRequest reads ?tail and Last-Event-ID; problem is non-empty for an invalid tail.
func logsRequest(id int64, r *http.Request) (agentapi.LogsRequest, string) {
	req := agentapi.LogsRequest{ID: id, Tail: defaultTail}
	if v := r.URL.Query().Get("tail"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > agentapi.MaxLogTail {
			return req, "tail must be an integer from 0 to " + strconv.Itoa(agentapi.MaxLogTail)
		}
		req.Tail = n
	}
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil && ts > 0 && ts < math.MaxInt64 {
			req.Since = ts + 1
		}
	}
	return req, ""
}

// sseWriter writes events with a fresh write deadline each time. Deadlines are wall-clock time
// (clock.Real), whatever clock the handler uses for its logic.
type sseWriter struct {
	w     io.Writer
	rc    *http.ResponseController
	clock clock.Clock
}

func (s *sseWriter) deadline() error {
	err := s.rc.SetWriteDeadline(s.clock.Now().Add(writeTimeout))
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

func (s *sseWriter) flush() error {
	if err := s.deadline(); err != nil {
		return err
	}
	err := s.rc.Flush()
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}

// event writes one event (without flushing); id may be empty. JSON never contains a newline,
// so data fits on one line.
func (s *sseWriter) event(name, id string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("event: " + name + "\n")
	if id != "" {
		buf.WriteString("id: " + id + "\n")
	}
	buf.WriteString("data: ")
	buf.Write(b)
	buf.WriteString("\n\n")
	if err := s.deadline(); err != nil {
		return err
	}
	_, err = s.w.Write(buf.Bytes())
	return err
}

// ping writes and flushes the heartbeat comment.
func (s *sseWriter) ping() error {
	if err := s.deadline(); err != nil {
		return err
	}
	if _, err := io.WriteString(s.w, ": ping\n\n"); err != nil {
		return err
	}
	return s.flush()
}

// logSession is one open stream: it (re)opens the agent stream and forwards its lines.
type logSession struct {
	h        *Handler
	ctx      context.Context
	sw       *sseWriter
	p        principal.Principal
	serverID int64
	req      agentapi.LogsRequest // next request to the agent
	running  *bool                // last state sent; nil = none yet
}

type lineOrErr struct {
	line agentapi.LogLine
	err  error
}

func (s *logSession) run() {
	heartbeat, stopHeartbeat := s.h.timers.Every(heartbeatInterval)
	defer stopHeartbeat()
	recheck, stopRecheck := s.h.timers.Every(recheckInterval)
	defer stopRecheck()
	for {
		stream, err := s.h.agent.Logs(s.ctx, s.req)
		switch {
		case err == nil:
			// Docker also streams the history of a stopped container (and ends at once), so the
			// state comes from the agent (X-Container-Running), not from "the stream opened".
			if s.setRunning(stream.Running()) != nil {
				_ = stream.Close()
				return
			}
			err = s.follow(stream, heartbeat, recheck)
			_ = stream.Close()
			if errors.Is(err, errStop) {
				return
			}
		case s.ctx.Err() != nil:
			return
		default:
			s.h.log.DebugContext(s.ctx, "console log stream unavailable", "server", s.serverID, "err", err)
		}
		if s.setRunning(false) != nil {
			return
		}
		if !s.wait(heartbeat, recheck) {
			return
		}
	}
}

// follow forwards the lines of one agent stream. It returns nil when the agent ended the stream
// (reconnect) and errStop when the whole SSE stream must end.
func (s *logSession) follow(stream agentapi.LogStream, heartbeat, recheck <-chan time.Time) error {
	lines := make(chan lineOrErr, lineBuffer)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			l, err := stream.Next()
			select {
			case lines <- lineOrErr{line: l, err: err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-s.ctx.Done():
			return errStop
		case <-heartbeat:
			if s.sw.ping() != nil {
				return errStop
			}
		case <-recheck:
			if !s.allowed() {
				return errStop
			}
		case le := <-lines:
			if le.err != nil {
				if !errors.Is(le.err, io.EOF) {
					s.h.log.DebugContext(s.ctx, "console log stream broke", "server", s.serverID, "err", le.err)
				}
				return nil
			}
			if s.sendLine(le.line) != nil {
				return errStop
			}
			if len(lines) == 0 && s.sw.flush() != nil {
				return errStop
			}
		}
	}
}

// sendLine forwards one line unless it repeats one already sent (spec §6.2: since = ts + 1).
func (s *logSession) sendLine(l agentapi.LogLine) error {
	if s.req.Since > 0 && l.TS < s.req.Since {
		return nil
	}
	id := ""
	if l.TS > 0 {
		s.req.Since = l.TS + 1
		id = strconv.FormatInt(l.TS, 10)
	}
	return s.sw.event("line", id, lineEvent{TS: l.TS / int64(time.Millisecond), Stream: l.Stream, Line: l.Line})
}

// setRunning sends a state event if v differs from the last state sent (no flapping while a
// stopped server is retried every 3 s).
func (s *logSession) setRunning(v bool) error {
	if s.running != nil && *s.running == v {
		return nil
	}
	s.running = &v
	if err := s.sw.event("state", "", stateEvent{Running: v}); err != nil {
		return err
	}
	return s.sw.flush()
}

// wait sleeps until the next reconnect attempt, still serving heartbeat and recheck.
func (s *logSession) wait(heartbeat, recheck <-chan time.Time) bool {
	retry, stop := s.h.timers.After(reconnectDelay)
	defer stop()
	for {
		select {
		case <-s.ctx.Done():
			return false
		case <-retry:
			return true
		case <-heartbeat:
			if s.sw.ping() != nil {
				return false
			}
		case <-recheck:
			if !s.allowed() {
				return false
			}
		}
	}
}

// allowed re-checks session, permission and the server itself (spec §6.2).
func (s *logSession) allowed() bool {
	if err := s.h.access.Recheck(s.ctx, s.p, s.serverID, rbac.PermServerConsole); err != nil {
		s.h.log.InfoContext(s.ctx, "console stream closed", "server", s.serverID, "user", s.p.UserID, "reason", err)
		return false
	}
	if _, err := s.h.q.ServersGetInstance(s.ctx, s.serverID); err != nil {
		s.h.log.InfoContext(s.ctx, "console stream closed", "server", s.serverID, "reason", "server gone", "err", err)
		return false
	}
	return true
}
```

In `internal/console/handler.go`:

```go
type Handler struct {
	q       *store.Queries
	clock   clock.Clock
	ioClock clock.Clock // wall clock for I/O deadlines (always clock.Real)
	rec     audit.Recorder
	agent   agentapi.ConsoleClient
	access  Access
	log     *slog.Logger
	limit   *userLimiter
	online  *onlineCache
	streams *streamSet
	timers  timers
}
```

```go
	return &Handler{
		q: store.New(d.DB), clock: d.Clock, ioClock: clock.Real{}, rec: d.Audit, agent: ag, access: acc, log: log,
		limit: newUserLimiter(), online: newOnlineCache(), streams: newStreamSet(), timers: realTimers{},
	}
```

```go
// Shutdown closes all open streams; new streams are refused with 503 afterwards. cmd/panel
// registers it with http.Server.RegisterOnShutdown, so Shutdown does not wait for streams.
func (h *Handler) Shutdown() { h.streams.closeAll() }
```

`internal/console/stubs.go` löschen (`git rm internal/console/stubs.go`).

- [ ] **Step 8: Lokal prüfen**

Run: `make check`
Expected: OK.

- [ ] **Step 9: Commit, Push, CI**

```bash
git add internal/console internal/auth/recheck.go internal/auth/recheck_test.go
git commit -m "feat(console): live log stream over SSE with reconnect and periodic recheck

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI grün, auch `make test-race` (Stream-Tests mit Goroutinen). Bei Flakes in `TestStreamSurvivesServerTimeouts` die Ursache klären, nicht die Zeiten verlängern: der Test prüft genau, dass `ReadTimeout`/`WriteTimeout` den Stream nicht beenden.

---

### Task 13: Verdrahtung in `cmd/panel`

**Files:**
- Modify: `cmd/panel/app.go`, `cmd/panel/main.go` (nur `RegisterOnShutdown`; `Console: ac` hat Phase A gesetzt), `cmd/panel/integration_test.go` (Fake im `env`)
- Create: `cmd/panel/fakeconsole_test.go`
- Test: `cmd/panel/console_test.go`

**Interfaces:**
- Consumes: `console.NewHandler`, `console.Deps`, `console.NewAccess`, `(*console.Handler).Routes/Shutdown`, `(*auth.Sessions).Recheck` (über `authH.Sessions()`), `*agent.Client` als `agentapi.ConsoleClient` (Phase A)
- Produces (`appDeps.Console` existiert seit Phase A):
```go
type app struct {
	// … unverändert …
	Shutdown func() // ends open console streams; registered with http.Server.RegisterOnShutdown
}
```

- [ ] **Step 1: Fake und Integrationstest schreiben**

`cmd/panel/fakeconsole_test.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// fakeConsole simulates the agent's console API: fixed RCON answers and one log stream with
// the lines in logLines (then it stays open until closed).
type fakeConsole struct {
	mu       sync.Mutex
	commands []string
	logLines []agentapi.LogLine
	streams  []*fakeLogStream
}

func newFakeConsole() *fakeConsole { return &fakeConsole{} }

func (f *fakeConsole) Rcon(_ context.Context, id int64, command string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, fmt.Sprintf("%d %s", id, command))
	if command == "list uuids" {
		return "There are 0 of a max of 20 players online: ", nil
	}
	return "ok: " + command, nil
}

func (f *fakeConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{
		Ops: []agentapi.PlayerOp{{UUID: "u1", Name: "Alex", Level: 4}}, Whitelist: []agentapi.PlayerEntry{},
		Bans: []agentapi.PlayerBan{}, Errors: []string{},
	}, nil
}

func (f *fakeConsole) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &fakeLogStream{lines: append([]agentapi.LogLine(nil), f.logLines...), closed: make(chan struct{})}
	f.streams = append(f.streams, s)
	return s, nil
}

func (f *fakeConsole) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

type fakeLogStream struct {
	mu     sync.Mutex
	lines  []agentapi.LogLine
	closed chan struct{}
	once   sync.Once
}

func (s *fakeLogStream) Next() (agentapi.LogLine, error) {
	s.mu.Lock()
	if len(s.lines) > 0 {
		l := s.lines[0]
		s.lines = s.lines[1:]
		s.mu.Unlock()
		return l, nil
	}
	s.mu.Unlock()
	<-s.closed
	return agentapi.LogLine{}, io.EOF
}

func (s *fakeLogStream) Running() bool { return true }

func (s *fakeLogStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}
```

In `cmd/panel/integration_test.go`:
- `env` bekommt das Feld `console *fakeConsole`,
- `newEnv` legt `fc := newFakeConsole()` an, setzt `fc.logLines = []agentapi.LogLine{{TS: 1_760_184_000_000_000_001, Stream: agentapi.StreamOut, Line: "Done (3.0s)!"}}`, übergibt `Console: fc` in `appDeps{…}` und speichert `console: fc` im `env`-Literal (Import `github.com/Bernotese/mc-panel/internal/agent/agentapi` ergänzen).

`cmd/panel/console_test.go`:

```go
package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// openConsole opens the SSE stream with c's cookies and returns the response.
func openConsole(t *testing.T, c *client, id int64) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/servers/%d/console/stream", c.e.srv.URL, id), nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = resp.Body.Close() })
	return resp, cancel
}

// The console routes are wired with the real rbac rules, the agent client and the recheck.
func TestConsoleEndToEnd(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	admin.must(200, "PUT", "/servers/settings", map[string]any{"base_domain": "mc.example.com"})
	srv := admin.must(201, "POST", "/servers", paperServer("survival"))
	id := int64(srv["id"].(float64))

	out := admin.must(200, "POST", fmt.Sprintf("/servers/%d/console", id), map[string]string{"command": "/say hi"})
	if out["output"] != "ok: say hi" {
		t.Fatalf("command output = %v", out)
	}
	players := admin.must(200, "GET", fmt.Sprintf("/servers/%d/players", id), nil)
	if players["available"] != true {
		t.Fatalf("players = %v", players)
	}
	lists := admin.must(200, "GET", fmt.Sprintf("/servers/%d/players/lists", id), nil)
	if ops, _ := lists["ops"].([]any); len(ops) != 1 {
		t.Fatalf("lists = %v", lists)
	}
	admin.must(200, "POST", fmt.Sprintf("/servers/%d/players/actions", id), map[string]string{"action": "op", "name": "Steve"})
	if got := e.console.sent(); !slices.Contains(got, fmt.Sprintf("%d say hi", id)) || !slices.Contains(got, fmt.Sprintf("%d op Steve", id)) {
		t.Fatalf("agent commands = %v", got)
	}

	resp, _ := openConsole(t, admin, id)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %v", resp.StatusCode, resp.Header)
	}
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	if !waitLine(lines, "id: 1760184000000000001") {
		t.Fatal("first log line not streamed")
	}
	e.app.Shutdown() // what http.Server.Shutdown triggers via RegisterOnShutdown
	if !waitClosed(lines) {
		t.Fatal("stream still open after Shutdown")
	}

	// a friend with server.view + server.power only
	u := admin.must(201, "POST", "/users", map[string]string{"username": "friend", "password": "another long secret 42", "role": "user"})
	admin.must(200, "PUT", fmt.Sprintf("/users/%d/permissions", int64(u["id"].(float64))), map[string]any{
		"grants": []map[string]any{{"server_id": id, "permissions": []string{"server.power"}}},
	})
	friend := e.client("")
	friend.must(200, "POST", "/auth/login", map[string]string{"username": "friend", "password": "another long secret 42"})
	friend.must(200, "GET", fmt.Sprintf("/servers/%d/players", id), nil)
	for _, tc := range []struct{ method, path string }{
		{"POST", fmt.Sprintf("/servers/%d/console", id)},
		{"GET", fmt.Sprintf("/servers/%d/players/lists", id)},
		{"POST", fmt.Sprintf("/servers/%d/players/actions", id)},
		{"GET", fmt.Sprintf("/servers/%d/console/stream", id)},
	} {
		if c, body := friend.do(tc.method, tc.path, map[string]string{"command": "list", "action": "op", "name": "Steve"}); c != 403 {
			t.Errorf("%s %s as friend = %d %v, want 403", tc.method, tc.path, c, body)
		}
	}
}

func waitLine(lines <-chan string, want string) bool {
	deadline := time.After(5 * time.Second)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return false
			}
			if strings.TrimSpace(l) == want {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func waitClosed(lines <-chan string) bool {
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
```

Hinweis: `friend.do` sendet für GET-Routen einen Body mit; das stört die 403-Prüfung nicht (die Regel greift vor dem Handler). `server.power` impliziert `server.view` (TP1: Vergabe eines Rechts speichert `server.view` mit).

- [ ] **Step 2: Verdrahtung implementieren**

Stand nach Phase A (Plan 01 Aufgabe 3, Step 4): `appDeps.Console` existiert, `consoleH := console.NewHandler(console.Deps{DB: db, Clock: d.Clock, Audit: auditRec, Agent: d.Console})` steht nach `serversH := …`, `consoleH.Routes(api)` nach `serversH.Routes(api)`, die Rückgabe enthält `Close: func() { consoleH.Shutdown(); _ = closeGeo() }`, und `main.go` übergibt `Console: ac`.

`cmd/panel/app.go` – `app` um `Shutdown` erweitern:

```go
type app struct {
	Handler  http.Handler
	Mux      *chi.Mux
	Routes   []rbac.RouteInfo
	Run      func(ctx context.Context) // background loops; blocks until ctx is done
	Close    func()                    // ends console streams, releases the GeoIP reader
	Shutdown func()                    // ends open console streams (http.Server.RegisterOnShutdown)
}
```

Die Konstruktion von `consoleH` ersetzen (jetzt mit `Access`):

```go
	checker := rbac.NewDBChecker(db)
	consoleH := console.NewHandler(console.Deps{
		DB: db, Clock: d.Clock, Audit: auditRec, Agent: d.Console,
		Access: console.NewAccess(authH.Sessions(), checker),
	})
```

`rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), d.Clock)` wird zu `rt := rbac.NewRouter(mux, checker, d.Clock)`. Im `return &app{…}` kommt `Shutdown: consoleH.Shutdown,` hinzu; `Close` bleibt wie in Phase A (`Shutdown` ist idempotent).

`cmd/panel/main.go` – direkt nach `srv := newHTTPServer(a.Handler)`:

```go
	// Console streams never become idle: without this, srv.Shutdown would wait for them until
	// its 10 s timeout and then close the connections hard.
	srv.RegisterOnShutdown(a.Shutdown)
```

- [ ] **Step 3: Lokal prüfen**

Run: `make check`
Expected: OK.

- [ ] **Step 4: Commit, Push, CI**

```bash
git add cmd/panel
git commit -m "feat(panel): wire the console handler, recheck and shutdown of streams

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI grün: `TestConsoleEndToEnd`, `TestRoutesMatchOpenAPI`, `TestRouteCoverage` und alle übrigen Jobs. Danach PR als „ready for review“ markieren (`gh pr ready`) und dem Lead melden; gemergt wird nur durch den Nutzer.
