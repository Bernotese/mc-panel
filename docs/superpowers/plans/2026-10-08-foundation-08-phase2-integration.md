# Phase 2 – Integration – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die sechs Stream-Branches zusammenführen, `cmd/panel` vollständig verdrahten (Server, CLI), das Gesamtsystem per Integrations-, E2E- und VM-Tests gegen die Abnahmekriterien der Spec prüfen und ein Security- und Code-Review durchführen.

**Architecture:** `cmd/panel/app.go` baut aus Config, DB, Clock und Agent-Client die komplette Anwendung (Middleware-Kette, `rbac.Router` mit allen Stream-Routen, Hintergrund-Loops). `main.go` bleibt dünn: Subcommands `serve`, `admin create`, `admin unlock`, `audit verify`, `version`. Integrationstests laufen gegen `buildApp` über einen echten TLS-Testserver.

**Tech Stack:** wie Übersicht; zusätzlich Playwright (Stream E), Lima-VM (Stream F).

**Spec:** `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`

## Global Constraints

Alle Constraints der Übersicht gelten. Zusätzlich:
- Phase 2 darf alle Dateien ändern. Fixes an Stream-Code werden als eigene Commits mit Begründung gemacht (`fix(<stream>): …`), nie als Teil eines Merge-Commits.
- Middleware-Reihenfolge (verbindlich, aus Phase 0 + Wiring-Verträgen): `RequestID → RealIP → SecurityHeaders → BodyLimit(1 MiB) → svc.IPFilter → authH.Sessions().Middleware → auth.CSRF(cfg.Origin()) → svc.APIRateLimit`.
- Genau **ein** `audit.SQLRecorder` pro Prozess; er geht an A, B und D.
- Shutdown-Reihenfolge: HTTP-Server stoppen → Kontext der Hintergrund-Loops abbrechen → auf Loops warten → `db.Close()`.
- Neues-Gerät-Benachrichtigung sendet ausschließlich Stream B (in `RecordAttempt`).

## Review Focus

1. **Middleware-Reihenfolge:** Ein gejailter Client darf auch statische Dateien und `/api/health` nicht erreichen; CSRF muss nach dem Session-Loader laufen, sonst ist jeder POST mit Session ungeprüft. → Integrationstests in Task 3 (`TestJailBlocksEverything`, `TestCSRFRejected`).
2. **Route ohne Regel:** Jede `/api`-Route muss über `rbac.Router` registriert sein. → `TestRouteCoverage` in Task 3.
3. **Dev-Proxy und Origin:** Im Dev-Modus kommt der Browser über Vite (`http://localhost:5173`), das panel muss genau diesen Origin akzeptieren und `__Host-sid` über http://localhost setzen. → manuelle Prüfung + Playwright `e2e:real` in Task 5.
4. **Agent fehlt beim Start:** `serve -dev` ohne laufenden agent muss starten, Login und Jail in der App funktionieren; Security Center zeigt „Nicht erreichbar“. → Task 4 Step 4.
5. **Release ohne Frontend / mit Mock-Worker:** Release-Binary muss das gebaute Frontend enthalten, aber kein `mockServiceWorker.js`. → Task 6.

## Dateistruktur

```
cmd/panel/main.go            Subcommands, Flag-Parsing, Exit-Codes (ersetzt Phase-0-Skelett)
cmd/panel/app.go             buildApp: Konstruktion aller Komponenten, Router, Hintergrund-Loops
cmd/panel/term_linux.go      ioctl-Konstanten für Passwort ohne Echo (Linux)
cmd/panel/term_darwin.go     ioctl-Konstanten (macOS)
cmd/panel/password_prompt.go readPassword: Terminal ohne Echo, sonst eine Zeile aus stdin
cmd/panel/app_test.go        Route-Coverage + Ende-zu-Ende-Integrationstests
cmd/panel/fakeagent_test.go  In-Memory agentapi.Client für Tests
.github/workflows/ci.yml     + Playwright-Job (Eigentum geht nach Phase 1 an den Lead)
```

---

### Task 1: Streams zusammenführen

**Files:** keine neuen; Merges der Branches `stream/f`, `stream/c`, `stream/d`, `stream/b`, `stream/a`, `stream/e`.

- [ ] **Step 1: Vorbedingungen prüfen** – jeder Stream-Branch hat im eigenen Worktree `make test` und `make lint` grün gemeldet.

```bash
git branch --list 'stream/*'
git log --oneline phase0..stream/f | head
```
Expected: sechs Branches, jeder mit Commits nach `phase0`.

- [ ] **Step 2: In fester Reihenfolge mergen, nach jedem Merge testen**

```bash
for b in f c d b a e; do
  git merge --no-ff "stream/$b" -m "merge: stream $b" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>" || break
  sqlc generate && git diff --exit-code internal/store || { echo "sqlc drift after stream $b"; break; }
  make test-go || break
done
```
Expected: alle sechs Merges ohne Konflikt, `make test-go` nach jedem grün. Konflikte sind laut Planung nur möglich in `go.sum` (nicht erwartet, Abhängigkeiten sind gepinnt) und bei Stream E in `web/package.json` und `web/src/routeTree.gen.ts`. Lösung: `go mod tidy` bzw. `cd web && npx vite build` (generiert `routeTree.gen.ts` neu), dann `git add` und `git merge --continue`.

- [ ] **Step 3: Gesamtprüfung nach dem letzten Merge**

```bash
make generate && git diff --exit-code
make test && make lint
```
Expected: keine Diffs durch Generatoren, alle Tests und Linter grün. Rote Tests hier sind Integrationsfehler zwischen Streams → mit `superpowers:systematic-debugging` untersuchen, Fix als eigener Commit.

---

### Task 2: `buildApp` und vollständiges `cmd/panel`

**Files:**
- Create: `cmd/panel/app.go`, `cmd/panel/password_prompt.go`, `cmd/panel/term_linux.go`, `cmd/panel/term_darwin.go`
- Modify: `cmd/panel/main.go` (komplett ersetzen)

**Interfaces:**
- Consumes (aus den Wiring-Verträgen):
  - A: `auth.New(auth.Deps{DB, Config, Clock, Box, Guard, Geo, Audit, Notifier}) (*auth.Handler, error)`, `(*auth.Handler).Sessions().Middleware`, `auth.CSRF(origin string) func(http.Handler) http.Handler`, `(*auth.Handler).Routes(api *rbac.Router)`
  - B: `defense.LoadGeo(path string, log *slog.Logger) (defense.GeoResolver, bool, func() error)`, `notify.NewDispatcher(db, box, clk, notify.DispatcherOptions{Dev: cfg.Dev}) *notify.Dispatcher`, `defense.NewService(ctx, defense.Deps{DB, Clock, Geo, GeoAvailable, Audit, Notifier, Agent}) (*defense.Service, error)`, `svc.IPFilter`, `svc.APIRateLimit`, `svc.Run(ctx)`, `disp.Run(ctx)`, `defense.NewHandler(svc).Routes(api)`, `notify.NewHandler(notify.HandlerDeps{DB, Box, Dispatcher, Audit, Clock, Dev}).Routes(api)`, `defense.Unlock(ctx, db, rec, clk, stdout) (defense.UnlockResult, error)`
  - C: `agent.NewClient(socketPath string) *agent.Client`
  - D: `rbac.NewDBChecker(db)`, `audit.NewSQLRecorder(db, clk)`, `auditapi.NewHandler(db, clk, n)`, `users.NewHandler(db, clk, rec, n)`, `users.RunAdminCreate(ctx, db, clk, rec, args, stdin, stdout) error`, `audit.RunAuditVerify(ctx, db, stdout) error`, `audit.ErrChainBroken`
- Produces:
```go
type appDeps struct {
	Clock clock.Clock
	Agent agentapi.Client
}
type app struct {
	Handler http.Handler
	Mux     *chi.Mux
	Routes  []rbac.RouteInfo
	Run     func(ctx context.Context) // background loops; blocks until ctx is done
	Close   func()                    // releases GeoIP reader
}
func buildApp(ctx context.Context, cfg config.Panel, db *sql.DB, d appDeps) (*app, error)
var version = "dev"
```

Hinweis: Stimmen einzelne Signaturen nach den Merges nicht exakt (z. B. ein Konstruktor gibt zusätzlich `error` zurück), gilt der tatsächlich gemergte Code; `app.go` wird angepasst, die Struktur bleibt.

- [ ] **Step 1: `cmd/panel/app.go`**

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/audit"
	"mcpanel/internal/auth"
	"mcpanel/internal/clock"
	"mcpanel/internal/config"
	"mcpanel/internal/defense"
	"mcpanel/internal/httpx"
	"mcpanel/internal/notify"
	"mcpanel/internal/rbac"
	"mcpanel/internal/secretbox"
	"mcpanel/internal/users"
	"mcpanel/internal/webui"
)

type appDeps struct {
	Clock clock.Clock
	Agent agentapi.Client
}

type app struct {
	Handler http.Handler
	Mux     *chi.Mux
	Routes  []rbac.RouteInfo
	Run     func(ctx context.Context)
	Close   func()
}

func buildApp(ctx context.Context, cfg config.Panel, db *sql.DB, d appDeps) (*app, error) {
	key, err := secretbox.LoadOrCreateKey(cfg.SecretKeyPath())
	if err != nil {
		return nil, err
	}
	box, err := secretbox.New(key)
	if err != nil {
		return nil, err
	}
	geo, geoOK, closeGeo := defense.LoadGeo(cfg.GeoIPPath, slog.Default())

	auditRec := audit.NewSQLRecorder(db, d.Clock)
	disp := notify.NewDispatcher(db, box, d.Clock, notify.DispatcherOptions{Dev: cfg.Dev})
	svc, err := defense.NewService(ctx, defense.Deps{
		DB: db, Clock: d.Clock, Geo: geo, GeoAvailable: geoOK,
		Audit: auditRec, Notifier: disp, Agent: d.Agent,
	})
	if err != nil {
		_ = closeGeo()
		return nil, fmt.Errorf("defense: %w", err)
	}
	authH, err := auth.New(auth.Deps{
		DB: db, Config: cfg, Clock: d.Clock, Box: box,
		Guard: svc, Geo: geo, Audit: auditRec, Notifier: disp,
	})
	if err != nil {
		_ = closeGeo()
		return nil, fmt.Errorf("auth: %w", err)
	}
	usersH := users.NewHandler(db, d.Clock, auditRec, disp)
	auditH := auditapi.NewHandler(db, d.Clock, disp)
	defenseH := defense.NewHandler(svc)
	notifyH := notify.NewHandler(notify.HandlerDeps{
		DB: db, Box: box, Dispatcher: disp, Audit: auditRec, Clock: d.Clock, Dev: cfg.Dev,
	})

	mux := chi.NewRouter()
	mux.Use(
		httpx.RequestID,
		httpx.RealIP(cfg.TrustedPrefixes()),
		httpx.SecurityHeaders,
		httpx.BodyLimit(1<<20),
		svc.IPFilter,
		authH.Sessions().Middleware,
		auth.CSRF(cfg.Origin()),
		svc.APIRateLimit,
	)
	rt := rbac.NewRouter(mux, rbac.NewDBChecker(db), d.Clock)
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/health", rbac.Public(), func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
		authH.Routes(api)
		defenseH.Routes(api)
		notifyH.Routes(api)
		usersH.Routes(api)
		auditH.Routes(api)
	})
	mux.Handle("/*", webui.Handler())

	run := func(ctx context.Context) {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); disp.Run(ctx) }()
		go func() { defer wg.Done(); svc.Run(ctx) }()
		wg.Wait()
	}
	return &app{
		Handler: mux,
		Mux:     mux,
		Routes:  rt.Routes(),
		Run:     run,
		Close:   func() { _ = closeGeo() },
	}, nil
}
```

- [ ] **Step 2: Passwort-Eingabe ohne Echo**

`cmd/panel/term_linux.go`:
```go
//go:build linux

package main

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)
```

`cmd/panel/term_darwin.go`:
```go
//go:build darwin

package main

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
```

`cmd/panel/password_prompt.go`:
```go
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// readPassword returns a reader yielding one password line. On a terminal the
// prompt goes to stderr and echo is disabled; otherwise stdin is read as-is.
func readPassword(stdin *os.File, stderr io.Writer) (io.Reader, error) {
	fd := int(stdin.Fd())
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil { // not a terminal (pipe, file)
		return stdin, nil
	}
	fmt.Fprint(stderr, "Password: ")
	noEcho := *t
	noEcho.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &noEcho); err != nil {
		return nil, err
	}
	line, readErr := bufio.NewReader(stdin).ReadString('\n')
	_ = unix.IoctlSetTermios(fd, ioctlSetTermios, t)
	fmt.Fprintln(stderr)
	if readErr != nil && readErr != io.EOF {
		return nil, readErr
	}
	return strings.NewReader(strings.TrimRight(line, "\r\n") + "\n"), nil
}
```

- [ ] **Step 3: `cmd/panel/main.go` komplett ersetzen**

```go
// Command panel is the mc-panel web application and its admin CLI.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mcpanel/internal/agent"
	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/config"
	"mcpanel/internal/defense"
	"mcpanel/internal/store"
	"mcpanel/internal/users"
)

var version = "dev"

const usageText = `usage:
  panel serve        [-config path] [-dev]
  panel admin create [-config path] [-dev] -username NAME   (password from stdin)
  panel admin unlock [-config path] [-dev]
  panel audit verify [-config path] [-dev]
  panel version`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	cmd := ""
	switch {
	case len(args) >= 1 && (args[0] == "serve" || args[0] == "version"):
		cmd, args = args[0], args[1:]
	case len(args) >= 2 && (args[0] == "admin" || args[0] == "audit"):
		cmd, args = args[0]+" "+args[1], args[2:]
	}
	var err error
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, version)
		return 0
	case "serve":
		err = serve(args)
	case "admin create", "admin unlock", "audit verify":
		err = runCLI(cmd, args, stdin, stdout, stderr)
	default:
		fmt.Fprintln(stderr, usageText)
		return 2
	}
	if err != nil {
		if !errors.Is(err, audit.ErrChainBroken) { // verify already printed its report
			fmt.Fprintln(stderr, "error:", err)
		}
		return 1
	}
	return 0
}

// commonFlags parses -config/-dev and returns the remaining args for the subcommand.
func commonFlags(name string, args []string) (config.Panel, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to panel.toml")
	dev := fs.Bool("dev", false, "development mode (paths under ./.dev)")
	// Unknown flags (e.g. -username) belong to the subcommand: stop at the first one.
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-config" || a == "--config" {
			if i+1 >= len(args) {
				return config.Panel{}, nil, errors.New("-config needs a value")
			}
			if err := fs.Set("config", args[i+1]); err != nil {
				return config.Panel{}, nil, err
			}
			i++
			continue
		}
		if a == "-dev" || a == "--dev" {
			_ = fs.Set("dev", "true")
			continue
		}
		rest = append(rest, a)
	}
	root, err := os.Getwd()
	if err != nil {
		return config.Panel{}, nil, err
	}
	cfg, err := config.LoadPanel(*cfgPath, *dev, root)
	return cfg, rest, err
}

func openDB(ctx context.Context, cfg config.Panel) (*sql.DB, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func runCLI(cmd string, args []string, stdin *os.File, stdout, stderr io.Writer) error {
	cfg, rest, err := commonFlags(cmd, args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	clk := clock.Real{}
	rec := audit.NewSQLRecorder(db, clk)
	switch cmd {
	case "admin create":
		pw, err := readPassword(stdin, stderr)
		if err != nil {
			return err
		}
		return users.RunAdminCreate(ctx, db, clk, rec, rest, pw, stdout)
	case "admin unlock":
		_, err := defense.Unlock(ctx, db, rec, clk, stdout)
		return err
	case "audit verify":
		return audit.RunAuditVerify(ctx, db, stdout)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func serve(args []string) error {
	cfg, rest, err := commonFlags("serve", args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unexpected arguments: %v", rest)
	}
	setupLogging(cfg.Dev)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := openDB(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}, Agent: agent.NewClient(cfg.AgentSocket)})
	if err != nil {
		return err
	}
	defer a.Close()

	bgCtx, cancelBg := context.WithCancel(context.Background())
	bgDone := make(chan struct{})
	go func() { a.Run(bgCtx); close(bgDone) }()

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           a.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("panel listening", "addr", cfg.Listen, "dev", cfg.Dev, "version", version)
		errCh <- srv.ListenAndServe()
	}()
	var serveErr error
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = err
	}
	cancelBg()
	<-bgDone // background loops must finish before db.Close (deferred)
	return serveErr
}

func setupLogging(dev bool) {
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	if dev {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(h))
}
```

- [ ] **Step 4: Bauen und CLI prüfen**

```bash
gofmt -w cmd/panel && go vet ./cmd/panel && go build -o bin/ ./cmd/...
./bin/panel version
rm -rf .dev/data
printf 'mc-panel integration pw 1\n' | ./bin/panel admin create -dev -username admin
./bin/panel audit verify -dev; echo "exit=$?"
./bin/panel admin unlock -dev; echo "exit=$?"
./bin/panel foo; echo "exit=$?"
```
Expected: `dev`; Admin angelegt (Ausgabe von `RunAdminCreate`); Audit-Kette OK mit `exit=0`; Unlock meldet „nichts zu tun“ o. ä. mit `exit=0`; Usage mit `exit=2`.

- [ ] **Step 5: Commit** `git add cmd/panel && git commit -m "feat(panel): wire all streams into serve and add admin/audit CLI" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 3: Route-Coverage und Ende-zu-Ende-Integrationstests

**Files:** Create `cmd/panel/fakeagent_test.go`, `cmd/panel/app_test.go`

**Interfaces:** Consumes `buildApp`, `users.RunAdminCreate`, `audit.Verify`, `rbac.Uncovered`, `github.com/pquerna/otp/totp.GenerateCode`, `clock.NewFake`.

Die Tests sprechen ausschließlich den OpenAPI-Vertrag. Weicht ein Detail vom tatsächlich gemergten Verhalten ab (z. B. Stage-Wechsel erst nach `GET /auth/me` sichtbar), wird zuerst geprüft, ob der Code den Vertrag verletzt (dann Code fixen); nur wenn der Vertrag mehrdeutig ist, wird der Test angepasst und der Vertrag präzisiert.

- [ ] **Step 1: `cmd/panel/fakeagent_test.go`**

```go
package main

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"mcpanel/internal/agent/agentapi"
)

type fakeAgent struct {
	mu   sync.Mutex
	bans map[string]time.Duration
}

func newFakeAgent() *fakeAgent { return &fakeAgent{bans: map[string]time.Duration{}} }

func (f *fakeAgent) Health(context.Context) (agentapi.HealthResponse, error) {
	return agentapi.HealthResponse{Status: "ok", Version: "test", Firewall: "noop"}, nil
}

func (f *fakeAgent) Ban(_ context.Context, p netip.Prefix, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bans[p.String()] = d
	return nil
}

func (f *fakeAgent) Unban(_ context.Context, p netip.Prefix) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.bans, p.String())
	return nil
}

func (f *fakeAgent) Bans(context.Context) ([]agentapi.Ban, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]agentapi.Ban, 0, len(f.bans))
	for p, d := range f.bans {
		out = append(out, agentapi.Ban{Prefix: p, ExpiresInSeconds: int64(d / time.Second)})
	}
	return out, nil
}

func (f *fakeAgent) banned(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.bans[prefix]
	return ok
}
```

- [ ] **Step 2: `cmd/panel/app_test.go`**

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/config"
	"mcpanel/internal/rbac"
	"mcpanel/internal/store/storetest"
	"mcpanel/internal/users"
)

const adminPW = "mc-panel integration pw 1"

type env struct {
	t     *testing.T
	srv   *httptest.Server
	app   *app
	clk   *clock.Fake
	agent *fakeAgent
	cfg   config.Panel
}

func newEnv(t *testing.T) *env {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	srv.StartTLS()
	t.Cleanup(srv.Close)

	cfg := config.DevPanel(t.TempDir())
	cfg.Dev = false
	cfg.PublicURL = srv.URL // https://127.0.0.1:port
	cfg.DataDir = t.TempDir()
	cfg.GeoIPPath = ""
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	db := storetest.Open(t)
	clk := clock.NewFake(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
	fa := newFakeAgent()
	ctx, cancel := context.WithCancel(context.Background())
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clk, Agent: fa})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; a.Close() })
	srv.Config.Handler = a.Handler

	if err := users.RunAdminCreate(context.Background(), db, clk, audit.NewSQLRecorder(db, clk),
		[]string{"-username", "admin"}, strings.NewReader(adminPW+"\n"), io.Discard); err != nil {
		t.Fatalf("admin create: %v", err)
	}
	return &env{t: t, srv: srv, app: a, clk: clk, agent: fa, cfg: cfg}
}

type client struct {
	e    *env
	hc   *http.Client
	csrf string
	xff  string
}

func (e *env) client(xff string) *client {
	jar, _ := cookiejar.New(nil)
	hc := e.srv.Client()
	hc.Jar = jar
	return &client{e: e, hc: hc, xff: xff}
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.e.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.srv.URL+"/api"+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.e.srv.URL)
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.xff != "" {
		req.Header.Set("X-Forwarded-For", c.xff)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if tok, ok := out["csrf_token"].(string); ok && tok != "" {
		c.csrf = tok
	}
	return resp.StatusCode, out
}

// refresh reloads Me (CSRF token may rotate on stage changes / sudo).
func (c *client) refresh() map[string]any {
	c.e.t.Helper()
	code, me := c.do("GET", "/auth/me", nil)
	if code != 200 {
		c.e.t.Fatalf("GET /auth/me = %d %v", code, me)
	}
	return me
}

func (c *client) must(want int, method, path string, body any) map[string]any {
	c.e.t.Helper()
	code, out := c.do(method, path, body)
	if code != want {
		c.e.t.Fatalf("%s %s = %d (%v), want %d", method, path, code, out, want)
	}
	return out
}

func code(e *env, secret string) string {
	e.t.Helper()
	c, err := totp.GenerateCode(secret, e.clk.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// loginAdminWithTOTP performs the forced 2FA setup and returns the client and TOTP secret.
func loginAdminWithTOTP(e *env) (*client, string) {
	c := e.client("")
	me := c.must(200, "POST", "/auth/login", map[string]string{"username": "admin", "password": adminPW})
	if me["stage"] != "setup" {
		e.t.Fatalf("admin without 2FA must land in setup stage, got %v", me["stage"])
	}
	c.must(403, "GET", "/servers", nil) // setup_2fa_required
	setup := c.must(200, "POST", "/me/totp/setup", nil)
	secret, _ := setup["secret"].(string)
	rc := c.must(200, "POST", "/me/totp/enable", map[string]string{"code": code(e, secret)})
	if codes, _ := rc["codes"].([]any); len(codes) != 10 {
		e.t.Fatalf("expected 10 recovery codes, got %v", rc["codes"])
	}
	if me := c.refresh(); me["stage"] != "full" || me["totp_enabled"] != true {
		e.t.Fatalf("after enable: %v", me)
	}
	return c, secret
}

func sudo(e *env, c *client, secret string) {
	e.clk.Advance(30 * time.Second) // next TOTP step (replay protection)
	c.must(200, "POST", "/auth/sudo/totp", map[string]string{"code": code(e, secret)})
	c.refresh()
}

func TestRouteCoverage(t *testing.T) {
	e := newEnv(t)
	if un := rbac.Uncovered(e.app.Mux, e.app.Routes, func(_, p string) bool { return p == "/*" }); len(un) != 0 {
		t.Fatalf("routes registered without rbac rule: %v", un)
	}
	if n := len(e.app.Routes); n != 57 {
		t.Fatalf("registered %d API routes, OpenAPI defines 57", n)
	}
}

func TestFullFlowPermissionsAndAudit(t *testing.T) {
	e := newEnv(t)
	admin, secret := loginAdminWithTOTP(e)

	// sudo required for admin mutations
	if c, out := admin.do("POST", "/servers", map[string]string{"name": "survival"}); c != 403 || out["error"] != "sudo_required" {
		t.Fatalf("POST /servers without sudo = %d %v", c, out)
	}
	sudo(e, admin, secret)
	a := admin.must(201, "POST", "/servers", map[string]string{"name": "survival"})
	b := admin.must(201, "POST", "/servers", map[string]string{"name": "creative"})
	srvA, srvB := int64(a["id"].(float64)), int64(b["id"].(float64))

	u := admin.must(201, "POST", "/users", map[string]string{"username": "max", "password": "another long secret 42", "role": "user"})
	uid := int64(u["id"].(float64))
	admin.must(200, "PUT", fmt.Sprintf("/users/%d/permissions", uid), map[string]any{
		"grants": []map[string]any{{"server_id": srvA, "permissions": []string{"server.power"}}},
	})

	// acceptance criterion 4: user sees only server A, 403 on B
	max := e.client("")
	if me := max.must(200, "POST", "/auth/login", map[string]string{"username": "max", "password": "another long secret 42"}); me["stage"] != "full" {
		t.Fatalf("user without enforced 2FA should be full, got %v", me["stage"])
	}
	code, list := max.doList("GET", "/servers")
	if code != 200 || len(list) != 1 || int64(list[0]["id"].(float64)) != srvA {
		t.Fatalf("GET /servers as max = %d %v", code, list)
	}
	max.must(202, "POST", fmt.Sprintf("/servers/%d/power", srvA), map[string]string{"action": "restart"})
	if c, out := max.do("POST", fmt.Sprintf("/servers/%d/power", srvB), map[string]string{"action": "restart"}); c != 403 {
		t.Fatalf("power on B = %d %v", c, out)
	}
	if c, _ := max.do("GET", "/users", nil); c != 403 {
		t.Fatalf("user on admin route = %d", c)
	}

	// acceptance criterion 6: chain valid and actions recorded
	v := admin.must(200, "GET", "/audit/verify", nil)
	if v["ok"] != true {
		t.Fatalf("audit verify: %v", v)
	}
	page := admin.must(200, "GET", "/audit?limit=200", nil)
	seen := map[string]bool{}
	for _, it := range page["items"].([]any) {
		seen[it.(map[string]any)["action"].(string)] = true
	}
	for _, want := range []string{audit.ActionTOTPEnable, audit.ActionSudo, audit.ActionServerCreate,
		audit.ActionUserCreate, audit.ActionPermissionsSet, audit.ActionServerPower} {
		if !seen[want] {
			t.Errorf("audit log misses %s (have %v)", want, seen)
		}
	}
}

func (c *client) doList(method, path string) (int, []map[string]any) {
	c.e.t.Helper()
	req, _ := http.NewRequest(method, c.e.srv.URL+"/api"+path, nil)
	req.Header.Set("Origin", c.e.srv.URL)
	resp, err := c.hc.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestCSRFRejected(t *testing.T) {
	e := newEnv(t)
	admin, _ := loginAdminWithTOTP(e)
	token := admin.csrf
	admin.csrf = ""
	if c, out := admin.do("POST", "/me/sessions/revoke-others", nil); c != 403 || out["error"] != "csrf_invalid" {
		t.Fatalf("POST without CSRF token = %d %v", c, out)
	}
	admin.csrf = token
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/me/sessions/revoke-others", nil)
	req.Header.Set("X-CSRF-Token", token)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := admin.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("foreign Origin = %d, want 403", resp.StatusCode)
	}
	admin.must(204, "POST", "/me/sessions/revoke-others", nil)
}

func TestJailBlocksEverything(t *testing.T) {
	e := newEnv(t)
	attacker := e.client("203.0.113.50")
	for i := 0; i < 5; i++ {
		if c, out := attacker.do("POST", "/auth/login", map[string]string{"username": "admin", "password": "wrong password!!"}); c != 401 {
			t.Fatalf("attempt %d = %d %v", i+1, c, out)
		}
	}
	if c, out := attacker.do("POST", "/auth/login", map[string]string{"username": "admin", "password": adminPW}); c != 403 || out["error"] != "jailed" {
		t.Fatalf("6th attempt (even correct password) = %d %v", c, out)
	}
	if c, _ := attacker.do("GET", "/health", nil); c != 403 {
		t.Fatalf("jailed IP reached /api/health: %d", c)
	}
	other := e.client("198.51.100.9")
	other.must(200, "GET", "/health", nil)

	deadline := time.Now().Add(3 * time.Second)
	for !e.agent.banned("203.0.113.50/32") {
		if time.Now().After(deadline) {
			t.Fatal("jail was not synced to the agent firewall")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// lift via API (admin, sudo) → unbanned
	admin, secret := loginAdminWithTOTP(e)
	sudo(e, admin, secret)
	_, jails := admin.doList("GET", "/security/jails")
	if len(jails) != 1 {
		t.Fatalf("jails = %v", jails)
	}
	admin.must(204, "DELETE", fmt.Sprintf("/security/jails/%d", int64(jails[0]["id"].(float64))), nil)
	attacker.must(200, "GET", "/health", nil)
}

func TestAgentDownStillWorks(t *testing.T) {
	cfg := config.DevPanel(t.TempDir())
	cfg.DataDir = t.TempDir()
	cfg.GeoIPPath = ""
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	db := storetest.Open(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := buildApp(ctx, cfg, db, appDeps{Clock: clock.Real{}, Agent: agentClientForMissingSocket(t)})
	if err != nil {
		t.Fatalf("buildApp must succeed without agent: %v", err)
	}
	defer a.Close()
	done := make(chan struct{})
	go func() { a.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/health", nil))
	if rec.Code != 200 {
		t.Fatalf("/api/health without agent = %d", rec.Code)
	}
}
```

Ergänze am Dateiende (nutzt den echten Client aus Stream C gegen einen nicht existierenden Socket):

```go
func agentClientForMissingSocket(t *testing.T) *agent.Client {
	return agent.NewClient(t.TempDir() + "/missing.sock")
}
```
und den Import `"mcpanel/internal/agent"`.

- [ ] **Step 3: Run** `go test ./cmd/panel/ -run . -count=1 -v`
Expected: alle fünf Tests PASS. Typische Integrationsfehler und ihr Ort: `TestRouteCoverage` ≠ 57 → ein Stream registriert eine Route zu viel/zu wenig (mit Phase-0-Tabelle abgleichen); `csrf_invalid` bei legitimen Requests → Origin-Vergleich in `auth.CSRF` vs. `cfg.Origin()`; Jail nicht synchronisiert → `svc.Run`-Kick nach `addJail` (Stream B Task 7).

- [ ] **Step 4: Race-Detector** `CGO_ENABLED=1 go test -race ./cmd/panel/ ./internal/...` → PASS.

- [ ] **Step 5: Commit** `git add cmd/panel && git commit -m "test(panel): add route coverage and end-to-end integration tests" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 4: Lokaler Gesamtlauf auf dem Mac

**Files:** keine (nur Verifikation; gefundene Fehler → eigene Fix-Commits)

- [ ] **Step 1: Dev-Stack starten** – `make dev` in einem eigenen Terminal-Tab.
Expected: agent (noop-Firewall), panel auf `127.0.0.1:8080`, Vite auf `http://localhost:5173`.

- [ ] **Step 2: Admin anlegen**
```bash
printf '%s\n' 'local dev admin password' | ./.dev/bin/panel admin create -dev -username jannes
```

- [ ] **Step 3: Im Browser durchklicken (Built-in-Browser, `http://localhost:5173`)** – Login → erzwungenes TOTP-Setup (QR scannen mit Authenticator-App) → Recovery-Codes → Dashboard; Passkey im Profil hinzufügen (Touch ID); abmelden; Login per Passkey; Server-Stub anlegen (Sudo-Dialog erscheint); Benutzer `max` mit `server.power` anlegen; in privatem Fenster als `max` einloggen. Expected: alles ohne Konsolenfehler, keine CSP-Verletzungen in der Browser-Konsole.

- [ ] **Step 4: Agent-Ausfall** – agent-Prozess beenden (`pkill -f '.dev/bin/agent'`), Security Center → Agent-Karte zeigt „Nicht erreichbar“, Login funktioniert weiter; agent neu starten (`.dev/bin/agent -dev &`), Karte wird nach Reload grün.

- [ ] **Step 5: Jail lokal** – 5 Fehlversuche aus einem zweiten Browser-Profil sind lokal *nicht* jailbar (Loopback wird nie gejailt, Stream-B-Entscheidung). Daher per curl mit simulierter Client-IP über den Proxy-Header:
```bash
for i in 1 2 3 4 5 6; do curl -s -o /dev/null -w "%{http_code}\n" -H 'Content-Type: application/json' -H 'Origin: http://localhost:5173' -H 'X-Forwarded-For: 203.0.113.77' -d '{"username":"jannes","password":"wrong wrong wrong"}' http://127.0.0.1:8080/api/auth/login; done
```
Expected: fünfmal `401`, dann `403`; Jail im Security Center sichtbar; Aufheben per Button (Sudo) funktioniert.

- [ ] **Step 6: Benachrichtigung** – ntfy-Notifier mit `https://ntfy.sh` und einem zufälligen Topic anlegen (Nutzer fragen, ob ein öffentlicher ntfy.sh-Test ok ist), „Test senden“ → Nachricht kommt in der ntfy-App/Weboberfläche an; Schritt 5 wiederholen → Jail-Benachrichtigung kommt an (Abnahmekriterium 5, App-Teil).

---

### Task 5: Playwright gegen das echte Backend + CI-Job

**Files:** Modify `.github/workflows/ci.yml`

- [ ] **Step 1: Session-Rotation abgleichen** – `ROTATING_PATHS`/`ME_PATHS` in `web/src/api/client.ts` mit den Stellen vergleichen, an denen Stream A die Session rotiert:
```bash
grep -n "rotate\|Rotate" internal/auth/*.go | grep -v _test
grep -n "ROTATING_PATHS\|ME_PATHS" -A8 web/src/api/client.ts
```
Expected: Jeder Endpunkt, der in `internal/auth` eine Rotation auslöst und kein `Me` liefert, steht in `ROTATING_PATHS`. Fehlende ergänzen, `cd web && npm test`, Commit `fix(web): align session rotation paths with backend`.

- [ ] **Step 2: E2E gegen echtes Backend**
```bash
make dev &
sleep 8
printf '%s\n' 'e2e admin password 123' | ./.dev/bin/panel admin create -dev -username e2e-admin
cd web && npx playwright install --with-deps chromium
E2E_BASE_URL=http://localhost:5173 E2E_ADMIN_USER=e2e-admin E2E_ADMIN_PASSWORD='e2e admin password 123' npm run e2e:real
npm run e2e
```
Expected: beide Playwright-Projekte grün (real: Login + TOTP-Setup + zweiter Login; mock: Smoke inkl. 360 px).

- [ ] **Step 3: Playwright-Job in `.github/workflows/ci.yml`** – unter `jobs:` ergänzen (Action-Versionen exakt wie in den übrigen Jobs der Datei übernehmen):

```yaml
  e2e:
    name: e2e (playwright, real backend)
    runs-on: ubuntu-latest
    needs: [go, web]
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
      - uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0
        with:
          node-version: lts/*
          cache: npm
          cache-dependency-path: web/package-lock.json
      - name: Install web deps
        run: cd web && npm ci
      - name: Build binaries
        run: mkdir -p .dev/bin && go build -o .dev/bin/ ./cmd/...
      - name: Start stack
        run: |
          .dev/bin/agent -dev > agent.log 2>&1 &
          .dev/bin/panel serve -dev > panel.log 2>&1 &
          (cd web && npx vite --port 5173 --strictPort > ../vite.log 2>&1 &)
          for i in $(seq 1 30); do curl -sf http://127.0.0.1:8080/api/health && break; sleep 1; done
          printf '%s\n' 'ci e2e admin password 1' | .dev/bin/panel admin create -dev -username e2e-admin
      - name: Playwright
        env:
          E2E_BASE_URL: http://localhost:5173
          E2E_ADMIN_USER: e2e-admin
          E2E_ADMIN_PASSWORD: ci e2e admin password 1
        run: cd web && npx playwright install --with-deps chromium && npm run e2e:real && npm run e2e
      - name: Logs on failure
        if: failure()
        run: tail -n 200 agent.log panel.log vite.log
```
Die Pins entsprechen denen aus dem Stream-F-Plan; hat Stream F beim Ausführen andere SHAs aufgelöst, die aus dem `go`-Job derselben Datei übernehmen.

- [ ] **Step 4: Validieren** `go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/ci.yml` → keine Meldungen.

- [ ] **Step 5: Commit** `git add .github web && git commit -m "ci: add playwright e2e job against real backend" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 6: Release-Build und Linux-VM-Abnahme

**Files:** keine (Verifikation)

- [ ] **Step 1: Release-Snapshot**
```bash
make clean && make release
ls dist/*.tar.gz dist/checksums.txt
tar -tzf dist/mc-panel_*_linux_arm64.tar.gz
for a in dist/mc-panel_*_linux_*.tar.gz; do tar -xzOf "$a" panel | strings | grep -c '/assets/index-'; done
tar -xzOf dist/mc-panel_*_linux_amd64.tar.gz panel | strings | grep -c mockServiceWorker || true
```
Expected: zwei Archive + `checksums.txt`; Inhalt `panel`, `agent`; Frontend eingebettet (Zähler ≥ 1 je Archiv); `mockServiceWorker` 0 Treffer.

- [ ] **Step 2: VM-Abnahme** (Nutzer vorher fragen, falls das Ubuntu-Image noch nicht geladen ist, ca. 600 MB)
```bash
scripts/vm-setup.sh --no-build
scripts/vm-test.sh
```
Expected (aus Stream-F-Vertrag): `vm-setup.sh` endet mit `{"status":"ok"}`, Socket `660 root:mcpanel`, beide Dienste `active`, `nftables: table inet mcpanel present`; `vm-test.sh` zeigt fünfmal `HTTP 401`, dann das Set `mcpanel_jail4` mit `192.0.2.2` und endet mit `PASS: acceptance check 5 (nftables part)`.

---

### Task 7: Security- und Code-Review, Abnahme

**Files:** nach Befund

- [ ] **Step 1: Security-Review** – Skill `security-review` auf dem gesamten Branch seit `phase0` ausführen. Jeder Befund: bewerten (`superpowers:receiving-code-review`), berechtigte mit Test + Fix als eigenen Commit beheben.

- [ ] **Step 2: Code-Review** – Skill `superpowers:requesting-code-review` für den Gesamtstand; Befunde wie oben.

- [ ] **Step 3: Abnahmekriterien der Spec (§12) abhaken**

| # | Kriterium | Nachweis |
|---|---|---|
| 1 | `make test` grün; Linter ohne Befunde | `make test && make lint` |
| 2 | `make dev` → UI auf `http://localhost:5173` | Task 4 Step 1 |
| 3 | `admin create` → 2FA-Setup → Login mit TOTP und Passkey | Task 4 Step 3, Playwright real |
| 4 | User mit `server.power` auf A sieht nur A, 403 auf B | `TestFullFlowPermissionsAndAudit` |
| 5 | 5 Fehlversuche → Jail, Benachrichtigung, nftables | `TestJailBlocksEverything`, Task 4 Step 5–6, Task 6 Step 2 |
| 6 | Audit-Log vollständig, verify OK, Manipulation erkannt | `TestFullFlowPermissionsAndAudit`, Stream-D-Tamper-Tests, `panel audit verify` |
| 7 | `make release` → Linux-Binaries mit Frontend, laufen in VM | Task 6 |

- [ ] **Step 4: Abschluss** – Skill `superpowers:finishing-a-development-branch`; danach mit dem Nutzer klären, ob ein GitHub-Remote angelegt und `v0.1.0` getaggt werden soll (Tag löst den Release-Workflow aus → nur nach ausdrücklicher Zustimmung).

## Self-Review

- Spec §3 (Architektur, Middleware, Dev/Prod) → Task 2, 4, 6. §4–§7 (Auth, Rechte, Audit, Abwehr) → Streams A–D, Gesamtprüfung Task 3–4. §8 Frontend → Stream E, Task 4–5. §10 Tests/CI → Task 3, 5. §11 Parallelisierung → Task 1. §12 Abnahme → Task 7 Step 3.
- Bekannte Kopplungsrisiken und wo sie auffallen: Signaturabweichungen → Task 2 Build; Routenanzahl → `TestRouteCoverage`; Session-Rotation Frontend/Backend → Task 5 Step 1; Neues-Gerät-Benachrichtigung doppelt → durch Vertrag ausgeschlossen (nur Stream B), `TestFullFlow…` würde doppelte Events nicht sehen, daher in Task 4 Step 3 manuell beobachten (ein Login von neuer IP → genau eine Benachrichtigung).
