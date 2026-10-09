# Phase 0 – Verträge & Scaffold – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Repo, Toolchain und alle Querschnittsverträge (Schema, OpenAPI, Go-Interfaces, Rechte-Router, HTTP-Helfer, Web-Scaffold) so anlegen, dass sechs Streams danach parallel ohne geteilte Dateien arbeiten können.

**Architecture:** Siehe Übersicht `2026-10-08-foundation-00-overview.md`. Phase 0 liefert lauffähiges `panel serve -dev` mit `/api/health`, migrierter SQLite-DB und eingebettetem (leerem) Frontend.

**Tech Stack:** Go, chi v5, modernc.org/sqlite, goose v3, sqlc, BurntSushi/toml · Vite + React + TS.

**Spec:** `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`

**Global Constraints / Review Focus:** siehe Übersicht (gelten vollständig).

**Abweichung von der Spec (bewusst):** Migrationen liegen in `internal/store/migrations/` statt `migrations/` (für `go:embed`). Das eingebettete Frontend liegt in `internal/webui/dist/` (Vite-`outDir`), nicht in `web/dist/`, damit `web/` keine Go-Dateien enthält. `web/go.mod` (leeres Modul) schließt `web/node_modules` aus `go ./...` aus.

**Formatierung:** Die Go-Snippets in diesem Plan sind inhaltlich verbindlich, aber nicht garantiert gofmt-konform ausgerichtet. Nach dem Schreiben jeder Go-Datei und vor jedem Commit `gofmt -w <geänderte .go-Dateien>` ausführen; `gofmt -l .` muss vor dem Tag `phase0` leer sein.

## Eigentum (für Streams schreibgeschützt)

`go.mod`, `go.sum`, `Makefile`, `sqlc.yaml`, `.gitignore`, `scripts/dev.sh`, `internal/deps/`, `internal/clock/`, `internal/config/`, `internal/httpx/`, `internal/secretbox/`, `internal/principal/`, `internal/store/open.go`, `internal/store/settings.go`, `internal/store/migrations/`, `internal/store/queries/common.sql`, `internal/store/storetest/`, sqlc-Basisdateien (`internal/store/db.go`, `internal/store/models.go`), `internal/rbac/rule.go`, `internal/rbac/router.go`, `internal/audit/types.go`, `internal/audit/memory.go`, `internal/notify/notify.go`, `internal/defense/guard.go`, `internal/defense/fake.go`, `internal/agent/agentapi/`, `internal/password/`, `internal/webui/`, `cmd/panel/main.go` (bis Phase 2), `api/openapi.yaml`, `web/go.mod`, `web/package.json`/`package-lock.json` (nur Stream E darf Frontend-Pakete ergänzen).

## Regeln für Streams (in jeden Stream-Plan übernommen)

1. Routen werden mit **vollem Pfad relativ zu `/api`** auf dem übergebenen `*rbac.Router` registriert (z. B. `api.Post("/auth/login", rbac.Public(), h.login)`); Streams rufen **nie** `Route()` auf.
2. sqlc-Query-Namen tragen das Stream-Präfix: `Auth…` (A), `Defense…` (B), `Rbac…` (D, Users/Servers/Permissions), `Audit…` (D, Audit).
3. Keine neuen Go-Abhängigkeiten außer denen in `internal/deps/deps.go`. Tests nur mit der Standardbibliothek (`testing`, `net/http/httptest`).
4. Jeder Stream liefert am Ende einen Abschnitt „Wiring-Vertrag“: Konstruktor-Signaturen + `Routes(api *rbac.Router)`, die Phase 2 aufruft.

## Dateistruktur Phase 0

```
.gitignore  Makefile  sqlc.yaml  go.mod  go.sum
scripts/dev.sh
api/openapi.yaml
cmd/panel/main.go
internal/deps/deps.go                 Blank-Imports (build tag deps) → alle Abhängigkeiten in go.mod
internal/clock/clock.go (+_test)
internal/config/config.go (+_test)
internal/httpx/errors.go json.go ip.go middleware.go (+_test)
internal/secretbox/secretbox.go (+_test)
internal/principal/principal.go (+_test)
internal/store/open.go settings.go (+_test), migrations/00001_init.sql, queries/common.sql, db.go models.go common.sql.go (generiert)
internal/store/storetest/storetest.go
internal/rbac/rule.go router.go (+_test)
internal/audit/types.go memory.go
internal/notify/notify.go
internal/defense/guard.go fake.go
internal/agent/agentapi/agentapi.go (+_test)
internal/password/password.go common.txt (+_test)
internal/webui/embed.go (+_test), internal/webui/dist/.gitkeep
web/  (Vite-Scaffold), web/go.mod
```

---

### Task 1: Toolchain und Repo-Grundgerüst

**Files:**
- Create: `go.mod`, `.gitignore`, `internal/deps/deps.go`, `web/go.mod`

**Interfaces:** Produces: Modul `mcpanel`.

- [ ] **Step 1: Toolchain installieren**

```bash
brew install go node sqlc goose golangci-lint goreleaser
go version && node --version && sqlc version && golangci-lint --version && goreleaser --version
```
Expected: alle Versionen werden ausgegeben.

- [ ] **Step 2: Modul anlegen**

```bash
cd /Users/jannes/Workspace/mc-panel
go mod init mcpanel
```

- [ ] **Step 3: `.gitignore` schreiben**

```gitignore
/bin/
/dist/
/.dev/
*.db
*.db-wal
*.db-shm
/internal/webui/dist/*
!/internal/webui/dist/.gitkeep
web/node_modules/
web/test-results/
web/playwright-report/
.DS_Store
```

- [ ] **Step 4: `internal/deps/deps.go` schreiben (hält alle Abhängigkeiten in go.mod)**

```go
//go:build deps

// Package deps pins every third-party dependency used by mc-panel so that
// parallel workstreams never need to edit go.mod. It is never compiled.
package deps

import (
	_ "github.com/BurntSushi/toml"
	_ "github.com/go-chi/chi/v5"
	_ "github.com/go-webauthn/webauthn/webauthn"
	_ "github.com/google/nftables"
	_ "github.com/oschwald/maxminddb-golang/v2"
	_ "github.com/pquerna/otp/totp"
	_ "github.com/pressly/goose/v3"
	_ "golang.org/x/crypto/argon2"
	_ "golang.org/x/sys/unix"
	_ "golang.org/x/time/rate"
	_ "modernc.org/sqlite"
)
```

- [ ] **Step 5: `web/go.mod` (schließt node_modules aus `go ./...` aus)**

```
// Separate empty module so that `go ./...` and golangci-lint skip web/node_modules.
module mcpanel/web
```

- [ ] **Step 6: Abhängigkeiten holen**

```bash
go get github.com/BurntSushi/toml github.com/go-chi/chi/v5 github.com/go-webauthn/webauthn/webauthn github.com/google/nftables github.com/oschwald/maxminddb-golang/v2 github.com/pquerna/otp/totp github.com/pressly/goose/v3 golang.org/x/crypto/argon2 golang.org/x/sys/unix golang.org/x/time/rate modernc.org/sqlite
go mod tidy
grep -c require go.mod
```
Expected: `go.mod` listet alle elf Module (direkt oder indirekt); kein Fehler.

- [ ] **Step 7: Commit**

```bash
git add .gitignore go.mod go.sum internal/deps web/go.mod
git commit -m "chore: init Go module and pin dependencies

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `clock`

**Files:** Create `internal/clock/clock.go`, Test `internal/clock/clock_test.go`

**Interfaces:** Produces:
```go
type Clock interface{ Now() time.Time }
type Real struct{}
func NewFake(t time.Time) *Fake
func (f *Fake) Now() time.Time
func (f *Fake) Advance(d time.Duration)
func (f *Fake) Set(t time.Time)
```

- [ ] **Step 1: Failing test**

```go
package clock_test

import (
	"testing"
	"time"

	"mcpanel/internal/clock"
)

func TestFakeAdvance(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	f := clock.NewFake(start)
	var c clock.Clock = f
	if !c.Now().Equal(start) {
		t.Fatalf("Now = %v, want %v", c.Now(), start)
	}
	f.Advance(90 * time.Second)
	if got, want := c.Now(), start.Add(90*time.Second); !got.Equal(want) {
		t.Fatalf("after Advance Now = %v, want %v", got, want)
	}
	f.Set(start)
	if !c.Now().Equal(start) {
		t.Fatalf("after Set Now = %v, want %v", c.Now(), start)
	}
}

func TestRealIsCurrent(t *testing.T) {
	if d := time.Since(clock.Real{}.Now()); d < 0 || d > time.Second {
		t.Fatalf("Real.Now off by %v", d)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/clock/` → FAIL (package missing).

- [ ] **Step 3: Implementation**

```go
// Package clock abstracts time so business logic can be tested deterministically.
package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type Real struct{}

func (Real) Now() time.Time { return time.Now() }

// Fake is a manually controlled clock for tests. Safe for concurrent use.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{t: t} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = t
}
```

- [ ] **Step 4: Run** `go test ./internal/clock/` → PASS.

- [ ] **Step 5: Commit** `git add internal/clock && git commit -m "feat(clock): add Clock interface with real and fake implementations" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 3: `config`

**Files:** Create `internal/config/config.go`, Test `internal/config/config_test.go`

**Interfaces:** Produces:
```go
type Duration struct{ time.Duration }            // TOML: "30m"
type Session struct{ Idle, Absolute, PreAuth, Sudo Duration }
type Panel struct {
	Dev bool; Listen, PublicURL, DataDir, AgentSocket, GeoIPPath string
	TrustedProxies []string; Session Session
}
func DefaultPanel() Panel
func DevPanel(root string) Panel
func LoadPanel(path string, dev bool, root string) (Panel, error)
func (p Panel) Validate() error
func (p Panel) DBPath() string            // <DataDir>/panel.db
func (p Panel) SecretKeyPath() string     // <DataDir>/secret.key
func (p Panel) Origin() string            // scheme://host[:port] of PublicURL
func (p Panel) RPID() string              // hostname of PublicURL
func (p Panel) TrustedPrefixes() []netip.Prefix   // only valid after Validate
type Agent struct{ Dev bool; Socket string; PanelUID int; Firewall string }
func DefaultAgent() Agent
func DevAgent(root string) Agent
func LoadAgent(path string, dev bool, root string) (Agent, error)
func (a Agent) Validate() error
```

- [ ] **Step 1: Failing tests**

```go
package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mcpanel/internal/config"
)

func TestDefaultPanelIsValidAfterSettingPublicURL(t *testing.T) {
	p := config.DefaultPanel()
	p.PublicURL = "https://panel.example.com"
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if p.Session.Idle.Duration != 30*time.Minute || p.Session.Absolute.Duration != 12*time.Hour ||
		p.Session.PreAuth.Duration != 5*time.Minute || p.Session.Sudo.Duration != 10*time.Minute {
		t.Fatalf("unexpected session defaults: %+v", p.Session)
	}
	if p.RPID() != "panel.example.com" || p.Origin() != "https://panel.example.com" {
		t.Fatalf("RPID/Origin = %q/%q", p.RPID(), p.Origin())
	}
	if p.DBPath() != "/var/lib/mc-panel/panel.db" {
		t.Fatalf("DBPath = %q", p.DBPath())
	}
}

func TestProdRequiresHTTPS(t *testing.T) {
	p := config.DefaultPanel()
	p.PublicURL = "http://panel.example.com"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("expected https error, got %v", err)
	}
}

func TestDevPanel(t *testing.T) {
	p := config.DevPanel("/repo")
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !p.Dev || p.PublicURL != "http://localhost:5173" || p.RPID() != "localhost" ||
		p.Origin() != "http://localhost:5173" || p.DataDir != "/repo/.dev/data" ||
		p.AgentSocket != "/repo/.dev/agent.sock" {
		t.Fatalf("unexpected dev config: %+v", p)
	}
}

func TestLoadPanelOverridesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.toml")
	content := `
public_url = "https://mc.example.org"
trusted_proxies = ["127.0.0.1/32", "10.0.0.0/8"]
[session]
idle_timeout = "15m"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := config.LoadPanel(path, false, "")
	if err != nil {
		t.Fatalf("LoadPanel: %v", err)
	}
	if p.Session.Idle.Duration != 15*time.Minute || p.Session.Absolute.Duration != 12*time.Hour {
		t.Fatalf("session = %+v", p.Session)
	}
	if got := len(p.TrustedPrefixes()); got != 2 {
		t.Fatalf("TrustedPrefixes len = %d", got)
	}
}

func TestLoadPanelRejectsUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.toml")
	if err := os.WriteFile(path, []byte("public_url = \"https://x.org\"\nlisten_adress = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadPanel(path, false, ""); err == nil || !strings.Contains(err.Error(), "listen_adress") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestInvalidTrustedProxy(t *testing.T) {
	p := config.DevPanel("/repo")
	p.TrustedProxies = []string{"not-a-cidr"}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestAgentConfig(t *testing.T) {
	a := config.DevAgent("/repo")
	if err := a.Validate(); err != nil {
		t.Fatalf("dev agent invalid: %v", err)
	}
	if a.Firewall != "noop" || a.PanelUID != os.Getuid() {
		t.Fatalf("dev agent = %+v", a)
	}
	d := config.DefaultAgent()
	if err := d.Validate(); err == nil {
		t.Fatal("default agent without panel_uid must be invalid")
	}
	d.PanelUID = 998
	if err := d.Validate(); err != nil || d.Firewall != "nftables" || d.Socket != "/run/mc-panel/agent.sock" {
		t.Fatalf("default agent = %+v err=%v", d, err)
	}
	d.Firewall = "iptables"
	if err := d.Validate(); err == nil {
		t.Fatal("unknown firewall must be invalid")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/config/` → FAIL.

- [ ] **Step 3: Implementation**

```go
// Package config loads and validates panel and agent configuration (TOML).
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

type Session struct {
	Idle     Duration `toml:"idle_timeout"`
	Absolute Duration `toml:"absolute_timeout"`
	PreAuth  Duration `toml:"preauth_timeout"`
	Sudo     Duration `toml:"sudo_ttl"`
}

type Panel struct {
	Dev            bool     `toml:"dev"`
	Listen         string   `toml:"listen"`
	PublicURL      string   `toml:"public_url"`
	DataDir        string   `toml:"data_dir"`
	AgentSocket    string   `toml:"agent_socket"`
	GeoIPPath      string   `toml:"geoip_path"`
	TrustedProxies []string `toml:"trusted_proxies"`
	Session        Session  `toml:"session"`

	trusted []netip.Prefix
}

func defaultSession() Session {
	return Session{
		Idle:     Duration{30 * time.Minute},
		Absolute: Duration{12 * time.Hour},
		PreAuth:  Duration{5 * time.Minute},
		Sudo:     Duration{10 * time.Minute},
	}
}

func DefaultPanel() Panel {
	return Panel{
		Listen:         "127.0.0.1:8080",
		DataDir:        "/var/lib/mc-panel",
		AgentSocket:    "/run/mc-panel/agent.sock",
		GeoIPPath:      "/var/lib/mc-panel/dbip-country-lite.mmdb",
		TrustedProxies: []string{"127.0.0.1/32", "::1/128"},
		Session:        defaultSession(),
	}
}

func DevPanel(root string) Panel {
	p := DefaultPanel()
	p.Dev = true
	p.PublicURL = "http://localhost:5173"
	p.DataDir = filepath.Join(root, ".dev", "data")
	p.AgentSocket = filepath.Join(root, ".dev", "agent.sock")
	p.GeoIPPath = filepath.Join(root, ".dev", "dbip-country-lite.mmdb")
	return p
}

// LoadPanel starts from the default (or dev) profile and overlays the TOML file at path (if non-empty).
func LoadPanel(path string, dev bool, root string) (Panel, error) {
	p := DefaultPanel()
	if dev {
		p = DevPanel(root)
	}
	if path != "" {
		md, err := toml.DecodeFile(path, &p)
		if err != nil {
			return Panel{}, fmt.Errorf("config: %w", err)
		}
		if und := md.Undecoded(); len(und) > 0 {
			keys := make([]string, len(und))
			for i, k := range und {
				keys[i] = k.String()
			}
			return Panel{}, fmt.Errorf("config: unknown keys: %s", strings.Join(keys, ", "))
		}
	}
	if err := p.Validate(); err != nil {
		return Panel{}, err
	}
	return p, nil
}

func (p *Panel) Validate() error {
	var errs []error
	if p.Listen == "" {
		errs = append(errs, errors.New("listen must be set"))
	}
	u, err := url.Parse(p.PublicURL)
	switch {
	case p.PublicURL == "" || err != nil || u.Host == "":
		errs = append(errs, errors.New("public_url must be an absolute URL"))
	case u.Scheme != "https" && !(p.Dev && u.Scheme == "http"):
		errs = append(errs, errors.New("public_url must use https (http only allowed in dev mode)"))
	}
	if p.DataDir == "" || p.AgentSocket == "" {
		errs = append(errs, errors.New("data_dir and agent_socket must be set"))
	}
	p.trusted = p.trusted[:0]
	for _, s := range p.TrustedProxies {
		pf, err := netip.ParsePrefix(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("trusted_proxies: %q: %w", s, err))
			continue
		}
		p.trusted = append(p.trusted, pf.Masked())
	}
	for name, d := range map[string]Duration{
		"idle_timeout": p.Session.Idle, "absolute_timeout": p.Session.Absolute,
		"preauth_timeout": p.Session.PreAuth, "sudo_ttl": p.Session.Sudo,
	} {
		if d.Duration <= 0 {
			errs = append(errs, fmt.Errorf("session.%s must be > 0", name))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}

func (p Panel) DBPath() string        { return filepath.Join(p.DataDir, "panel.db") }
func (p Panel) SecretKeyPath() string { return filepath.Join(p.DataDir, "secret.key") }

func (p Panel) Origin() string {
	u, err := url.Parse(p.PublicURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (p Panel) RPID() string {
	u, err := url.Parse(p.PublicURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func (p Panel) TrustedPrefixes() []netip.Prefix { return append([]netip.Prefix(nil), p.trusted...) }

type Agent struct {
	Dev      bool   `toml:"dev"`
	Socket   string `toml:"socket"`
	PanelUID int    `toml:"panel_uid"`
	Firewall string `toml:"firewall"` // "nftables" | "noop"
}

func DefaultAgent() Agent {
	return Agent{Socket: "/run/mc-panel/agent.sock", PanelUID: -1, Firewall: "nftables"}
}

func DevAgent(root string) Agent {
	return Agent{Dev: true, Socket: filepath.Join(root, ".dev", "agent.sock"), PanelUID: os.Getuid(), Firewall: "noop"}
}

func LoadAgent(path string, dev bool, root string) (Agent, error) {
	a := DefaultAgent()
	if dev {
		a = DevAgent(root)
	}
	if path != "" {
		md, err := toml.DecodeFile(path, &a)
		if err != nil {
			return Agent{}, fmt.Errorf("config: %w", err)
		}
		if und := md.Undecoded(); len(und) > 0 {
			return Agent{}, fmt.Errorf("config: unknown keys: %v", und)
		}
	}
	if err := a.Validate(); err != nil {
		return Agent{}, err
	}
	return a, nil
}

func (a Agent) Validate() error {
	var errs []error
	if a.Socket == "" {
		errs = append(errs, errors.New("socket must be set"))
	}
	if a.PanelUID < 0 {
		errs = append(errs, errors.New("panel_uid must be set to the uid of the panel user"))
	}
	if a.Firewall != "nftables" && a.Firewall != "noop" {
		errs = append(errs, fmt.Errorf("firewall must be \"nftables\" or \"noop\", got %q", a.Firewall))
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}
```

Hinweis: `Validate` hat einen Pointer-Receiver (füllt `trusted`). Im Test `TestDefaultPanelIsValidAfterSettingPublicURL` ist `p` addressierbar, daher funktioniert `p.Validate()`.

- [ ] **Step 4: Run** `go test ./internal/config/` → PASS.

- [ ] **Step 5: Commit** `git add internal/config && git commit -m "feat(config): add panel and agent configuration" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 4: `httpx` – Fehler, JSON, Client-IP, Middleware

**Files:** Create `internal/httpx/errors.go`, `json.go`, `ip.go`, `middleware.go`; Tests `internal/httpx/httpx_test.go`

**Interfaces:** Produces:
```go
// errors.go – the ONLY allowed error codes
const CodeBadRequest, CodeValidation, CodeUnauthorized, CodeInvalidCredentials, CodeInvalidCode,
      CodeForbidden, CodeSudoRequired, CodeSetupRequired, CodeCSRF, CodeNotFound, CodeConflict,
      CodePayloadTooLarge, CodeRateLimited, CodeJailed, CodeIPBlocked, CodeAgentUnavailable, CodeInternal
type ErrorBody struct{ Error, Message, RequestID string }
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, msg string)
func Internal(w http.ResponseWriter, r *http.Request, err error)      // logs + 500
// json.go
func WriteJSON(w http.ResponseWriter, status int, v any)
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool  // writes 400/413 itself on failure
// ip.go
func ClientIP(ctx context.Context) netip.Addr        // unmapped; zero if unknown
func WithClientIP(ctx context.Context, ip netip.Addr) context.Context   // for tests
func IPKey(a netip.Addr) netip.Prefix                // IPv4 → /32, IPv6 → /64 (masked); zero Prefix for invalid
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler
// middleware.go
func RequestID(next http.Handler) http.Handler
func RequestIDFrom(ctx context.Context) string
func SecurityHeaders(next http.Handler) http.Handler
func BodyLimit(n int64) func(http.Handler) http.Handler
```

- [ ] **Step 1: Failing tests**

```go
package httpx_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"mcpanel/internal/httpx"
)

func TestWriteError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	httpx.WriteError(rec, req, http.StatusForbidden, httpx.CodeForbidden, "nope")
	if rec.Code != 403 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("code=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var body httpx.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error != "forbidden" || body.Message != "nope" {
		t.Fatalf("body=%s err=%v", rec.Body, err)
	}
}

func TestInternalHidesDetails(t *testing.T) {
	h := httpx.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpx.Internal(w, r, errors.New("db password is hunter2"))
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body)
	}
	var body httpx.ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.RequestID == "" || body.RequestID != rec.Header().Get("X-Request-ID") {
		t.Fatalf("request id missing/mismatch: %+v / %q", body, rec.Header().Get("X-Request-ID"))
	}
}

func TestDecodeJSON(t *testing.T) {
	type in struct {
		Name string `json:"name"`
	}
	cases := []struct {
		name, body string
		ok         bool
		status     int
	}{
		{"valid", `{"name":"a"}`, true, 0},
		{"unknown field", `{"name":"a","x":1}`, false, 400},
		{"trailing data", `{"name":"a"}{"name":"b"}`, false, 400},
		{"malformed", `{`, false, 400},
		{"too large", `{"name":"` + strings.Repeat("a", 2000) + `"}`, false, 413},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got in
			var ok bool
			h := httpx.BodyLimit(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ok = httpx.DecodeJSON(w, r, &got)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)))
			if ok != tc.ok {
				t.Fatalf("ok=%v want %v (body %s)", ok, tc.ok, rec.Body)
			}
			if !tc.ok && rec.Code != tc.status {
				t.Fatalf("status=%d want %d", rec.Code, tc.status)
			}
		})
	}
}

func TestIPKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":              "203.0.113.7/32",
		"::ffff:203.0.113.7":       "203.0.113.7/32",
		"2001:db8:1:2:3:4:5:6":     "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1":     "2001:db8:1:2::/64",
		"fe80::1%en0":              "fe80::/64",
	}
	for in, want := range cases {
		if got := httpx.IPKey(netip.MustParseAddr(in)).String(); got != want {
			t.Errorf("IPKey(%s) = %s, want %s", in, got, want)
		}
	}
	if httpx.IPKey(netip.Addr{}).IsValid() {
		t.Error("IPKey(invalid) must be invalid")
	}
}

func TestRealIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	cases := []struct {
		name, remote, xff, want string
	}{
		{"direct, no proxy", "198.51.100.1:5000", "", "198.51.100.1"},
		{"untrusted peer ignores XFF", "198.51.100.1:5000", "1.2.3.4", "198.51.100.1"},
		{"trusted proxy uses XFF", "127.0.0.1:5000", "203.0.113.9", "203.0.113.9"},
		{"rightmost untrusted wins (spoofed left entry)", "127.0.0.1:5000", "6.6.6.6, 203.0.113.9", "203.0.113.9"},
		{"mapped v4 unmapped", "[::ffff:198.51.100.1]:5000", "", "198.51.100.1"},
		{"garbage XFF falls back to peer", "127.0.0.1:5000", "not-an-ip", "127.0.0.1"},
		{"v6 trusted proxy", "[::1]:5000", "2001:db8::5", "2001:db8::5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got netip.Addr
			h := httpx.RealIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = httpx.ClientIP(r.Context())
			}))
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			h.ServeHTTP(httptest.NewRecorder(), req)
			if got.String() != tc.want {
				t.Fatalf("ClientIP = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := httpx.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
		"Cache-Control":          "no-store",
		"Cross-Origin-Opener-Policy": "same-origin",
	} {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, part := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(csp, part) {
			t.Errorf("CSP %q missing %q", csp, part)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(strings.Split(csp, "script-src")[1], "unsafe-inline';") {
		t.Errorf("CSP must not allow inline/eval scripts: %q", csp)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if rec.Header().Get("Cache-Control") == "no-store" {
		t.Error("static assets must not get no-store")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/httpx/` → FAIL.

- [ ] **Step 3: Implementation `errors.go`**

```go
// Package httpx contains shared HTTP helpers: error format, JSON I/O, client IP and middleware.
package httpx

import (
	"log/slog"
	"net/http"
)

// Error codes. Handlers must only use these.
const (
	CodeBadRequest         = "bad_request"
	CodeValidation         = "validation_failed"
	CodeUnauthorized       = "unauthorized"
	CodeInvalidCredentials = "invalid_credentials"
	CodeInvalidCode        = "invalid_code"
	CodeForbidden          = "forbidden"
	CodeSudoRequired       = "sudo_required"
	CodeSetupRequired      = "setup_2fa_required"
	CodeCSRF               = "csrf_invalid"
	CodeNotFound           = "not_found"
	CodeConflict           = "conflict"
	CodePayloadTooLarge    = "payload_too_large"
	CodeRateLimited        = "rate_limited"
	CodeJailed             = "jailed"
	CodeIPBlocked          = "ip_blocked"
	CodeAgentUnavailable   = "agent_unavailable"
	CodeInternal           = "internal"
)

type ErrorBody struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	body := ErrorBody{Error: code, Message: msg}
	if status >= 500 {
		body.RequestID = RequestIDFrom(r.Context())
	}
	WriteJSON(w, status, body)
}

// Internal logs err (never sent to the client) and writes a generic 500.
func Internal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "internal error",
		"err", err, "request_id", RequestIDFrom(r.Context()), "method", r.Method, "path", r.URL.Path)
	WriteError(w, r, http.StatusInternalServerError, CodeInternal, "internal server error")
}
```

- [ ] **Step 4: Implementation `json.go`**

```go
package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json", "err", err)
	}
}

// DecodeJSON strictly decodes a single JSON value into dst. On failure it writes
// the error response (400 or 413) and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body too large")
			return false
		}
		WriteError(w, r, http.StatusBadRequest, CodeBadRequest, "invalid JSON body")
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		WriteError(w, r, http.StatusBadRequest, CodeBadRequest, "unexpected data after JSON body")
		return false
	}
	return true
}
```

- [ ] **Step 5: Implementation `ip.go`**

```go
package httpx

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
)

type ctxKey int

const (
	clientIPKey ctxKey = iota
	requestIDKey
)

func ClientIP(ctx context.Context) netip.Addr {
	ip, _ := ctx.Value(clientIPKey).(netip.Addr)
	return ip
}

func WithClientIP(ctx context.Context, ip netip.Addr) context.Context {
	return context.WithValue(ctx, clientIPKey, ip)
}

// IPKey returns the key under which rate limits and jails are tracked:
// IPv4 addresses as /32, IPv6 addresses as their /64 network (clients usually
// control a whole /64 and could otherwise rotate addresses).
func IPKey(a netip.Addr) netip.Prefix {
	if !a.IsValid() {
		return netip.Prefix{}
	}
	a = a.Unmap().WithZone("")
	if a.Is4() {
		return netip.PrefixFrom(a, 32)
	}
	p, _ := a.Prefix(64)
	return p
}

// RealIP stores the client IP in the request context. X-Forwarded-For is only
// honoured when the direct peer is a trusted proxy; the rightmost untrusted
// entry is used so clients cannot spoof by prepending addresses.
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := peerIP(r.RemoteAddr)
			if ip.IsValid() && isTrusted(ip, trusted) {
				if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
					ip = fromXFF(strings.Join(xff, ","), trusted, ip)
				}
			}
			next.ServeHTTP(w, r.WithContext(WithClientIP(r.Context(), ip)))
		})
	}
}

func peerIP(remote string) netip.Addr {
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		return ap.Addr().Unmap()
	}
	if a, err := netip.ParseAddr(remote); err == nil {
		return a.Unmap()
	}
	return netip.Addr{}
}

func fromXFF(header string, trusted []netip.Prefix, fallback netip.Addr) netip.Addr {
	parts := strings.Split(header, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return fallback
		}
		a = a.Unmap()
		if !isTrusted(a, trusted) {
			return a
		}
	}
	return fallback
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
```

Hinweis: `fe80::1%en0` im Test: `IPKey` entfernt die Zone vor `Prefix(64)`.

- [ ] **Step 6: Implementation `middleware.go`**

```go
package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		id := hex.EncodeToString(b)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func BodyLimit(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}
```

(`style-src 'unsafe-inline'` ist bewusst erlaubt, weil Radix/shadcn Inline-`style`-Attribute setzen; Skripte bleiben strikt.)

- [ ] **Step 7: Run** `go test ./internal/httpx/` → PASS. Falls `TestSecurityHeaders` an der `unsafe-inline`-Prüfung scheitert: Die Prüfung betrachtet nur den Teil nach `script-src`; korrigiere den Test, falls die Assertion `style-src`-Teile fälschlich mit erfasst, statt die CSP aufzuweichen.

- [ ] **Step 8: Commit** `git add internal/httpx && git commit -m "feat(httpx): add error format, strict JSON decoding, client IP and security middleware" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 5: `secretbox`

**Files:** Create `internal/secretbox/secretbox.go`, Test `internal/secretbox/secretbox_test.go`

**Interfaces:** Produces:
```go
const KeySize = 32
var ErrInvalid = errors.New("secretbox: invalid ciphertext")
func New(key []byte) (*Box, error)
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error)
func (b *Box) Open(ciphertext, aad []byte) ([]byte, error)
func LoadOrCreateKey(path string) ([]byte, error)    // creates 0600 file; rejects group/other-readable files
```
AAD-Konvention (für Streams verbindlich): TOTP-Secret `[]byte(fmt.Sprintf("totp:%d", userID))`, Notifier-Konfiguration `[]byte("notifier:" + kind)` (kind = `discord`|`ntfy`).

- [ ] **Step 1: Failing test**

```go
package secretbox_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"mcpanel/internal/secretbox"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{7}, secretbox.KeySize)
	b, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := b.Seal([]byte("JBSWY3DPEHPK3PXP"), []byte("totp:1"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := b.Open(ct, []byte("totp:1"))
	if err != nil || string(pt) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("Open = %q, %v", pt, err)
	}
	if _, err := b.Open(ct, []byte("totp:2")); !errors.Is(err, secretbox.ErrInvalid) {
		t.Fatalf("wrong AAD must fail with ErrInvalid, got %v", err)
	}
	ct[len(ct)-1] ^= 1
	if _, err := b.Open(ct, []byte("totp:1")); !errors.Is(err, secretbox.ErrInvalid) {
		t.Fatalf("tampered ciphertext must fail, got %v", err)
	}
	if _, err := b.Open([]byte{1, 2}, nil); !errors.Is(err, secretbox.ErrInvalid) {
		t.Fatalf("short ciphertext must fail, got %v", err)
	}
}

func TestNewRejectsBadKey(t *testing.T) {
	if _, err := secretbox.New(make([]byte, 16)); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	k1, err := secretbox.LoadOrCreateKey(path)
	if err != nil || len(k1) != secretbox.KeySize {
		t.Fatalf("create: len=%d err=%v", len(k1), err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v", info.Mode().Perm())
	}
	k2, err := secretbox.LoadOrCreateKey(path)
	if err != nil || !bytes.Equal(k1, k2) {
		t.Fatalf("reload mismatch err=%v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := secretbox.LoadOrCreateKey(path); err == nil {
		t.Fatal("expected error for world-readable key file")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/secretbox/` → FAIL.

- [ ] **Step 3: Implementation**

```go
// Package secretbox encrypts small secrets at rest (AES-256-GCM).
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
)

const KeySize = 32

var ErrInvalid = errors.New("secretbox: invalid ciphertext")

type Box struct{ aead cipher.AEAD }

func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal returns nonce||ciphertext. aad binds the ciphertext to its purpose (e.g. "totp:42").
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

func (b *Box) Open(ciphertext, aad []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(ciphertext) < ns+b.aead.Overhead() {
		return nil, ErrInvalid
	}
	pt, err := b.aead.Open(nil, ciphertext[:ns], ciphertext[ns:], aad)
	if err != nil {
		return nil, ErrInvalid
	}
	return pt, nil
}

// LoadOrCreateKey reads a 32-byte key from path or creates it (mode 0600).
func LoadOrCreateKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, KeySize)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("secretbox: create key: %w", err)
		}
		if _, err := f.Write(key); err != nil {
			f.Close()
			return nil, err
		}
		return key, f.Close()
	}
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secretbox: key file %s must not be accessible by group/others (mode %v)", path, info.Mode().Perm())
	}
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: key file %s has %d bytes, want %d", path, len(key), KeySize)
	}
	return key, nil
}
```

- [ ] **Step 4: Run** `go test ./internal/secretbox/` → PASS.

- [ ] **Step 5: Commit** `git add internal/secretbox && git commit -m "feat(secretbox): add AES-GCM secret encryption and key file handling" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 6: Datenbank – Schema, Open/Migrate, sqlc, Settings, Test-Helfer

**Files:**
- Create: `internal/store/migrations/00001_init.sql`, `internal/store/open.go`, `internal/store/settings.go`, `internal/store/queries/common.sql`, `sqlc.yaml`, `internal/store/storetest/storetest.go`
- Generated: `internal/store/db.go`, `internal/store/models.go`, `internal/store/common.sql.go`
- Test: `internal/store/store_test.go`

**Interfaces:** Produces:
```go
func Open(path string) (*sql.DB, error)
func Migrate(ctx context.Context, db *sql.DB) error
func New(db DBTX) *Queries                  // sqlc
type SecuritySettings struct{ Enforce2FAForUsers, AdminAllowlistOnly bool; AllowedCountries []string }
func DefaultSecuritySettings() SecuritySettings
func LoadSecuritySettings(ctx context.Context, q *Queries) (SecuritySettings, error)
func SaveSecuritySettings(ctx context.Context, q *Queries, s SecuritySettings, now time.Time) error
// storetest
func Open(t testing.TB) *sql.DB
func CreateUser(t testing.TB, db *sql.DB, username, role string) int64
func CreateServer(t testing.TB, db *sql.DB, name string) int64
```

- [ ] **Step 1: Migration schreiben `internal/store/migrations/00001_init.sql`**

```sql
-- +goose Up
CREATE TABLE users (
    id                  INTEGER PRIMARY KEY,
    username            TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    password_hash       TEXT    NOT NULL,
    role                TEXT    NOT NULL CHECK (role IN ('admin', 'user')),
    locale              TEXT    NOT NULL DEFAULT 'de' CHECK (locale IN ('de', 'en')),
    webauthn_handle     BLOB    NOT NULL UNIQUE,
    totp_secret_enc     BLOB,
    totp_enabled        INTEGER NOT NULL DEFAULT 0,
    totp_last_step      INTEGER NOT NULL DEFAULT 0,
    password_changed_at INTEGER NOT NULL,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

CREATE TABLE webauthn_credentials (
    id              INTEGER PRIMARY KEY,
    user_id         INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    credential_id   BLOB    NOT NULL UNIQUE,
    name            TEXT    NOT NULL,
    credential_json TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    last_used_at    INTEGER
);
CREATE INDEX webauthn_credentials_user ON webauthn_credentials (user_id);

CREATE TABLE recovery_codes (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  BLOB    NOT NULL,
    used_at    INTEGER,
    created_at INTEGER NOT NULL
);
CREATE INDEX recovery_codes_user ON recovery_codes (user_id);

CREATE TABLE sessions (
    id             INTEGER PRIMARY KEY,
    token_hash     BLOB    NOT NULL UNIQUE,
    user_id        INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    stage          TEXT    NOT NULL CHECK (stage IN ('preauth', 'setup', 'full')),
    csrf_token     TEXT    NOT NULL,
    ip             TEXT    NOT NULL,
    user_agent     TEXT    NOT NULL,
    country        TEXT    NOT NULL DEFAULT '',
    webauthn_state TEXT,
    sudo_until     INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    last_seen_at   INTEGER NOT NULL,
    expires_at     INTEGER NOT NULL,
    revoked_at     INTEGER
);
CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE servers (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    created_at INTEGER NOT NULL
);

CREATE TABLE permissions (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    server_id  INTEGER NOT NULL REFERENCES servers (id) ON DELETE CASCADE,
    permission TEXT    NOT NULL CHECK (permission IN ('server.view', 'server.power', 'server.console',
                                                      'server.files', 'server.plugins', 'server.backups')),
    PRIMARY KEY (user_id, server_id, permission)
);

CREATE TABLE audit_log (
    id         INTEGER PRIMARY KEY,
    ts         INTEGER NOT NULL,
    actor_type TEXT    NOT NULL CHECK (actor_type IN ('user', 'system', 'cli')),
    actor_id   INTEGER,
    actor_name TEXT    NOT NULL,
    action     TEXT    NOT NULL,
    target     TEXT    NOT NULL DEFAULT '',
    ip         TEXT    NOT NULL DEFAULT '',
    result     TEXT    NOT NULL CHECK (result IN ('success', 'failure', 'denied')),
    details    TEXT    NOT NULL DEFAULT '{}',
    prev_hash  TEXT    NOT NULL,
    hash       TEXT    NOT NULL UNIQUE
);
CREATE INDEX audit_log_ts ON audit_log (ts);

-- +goose StatementBegin
CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN
    SELECT RAISE(ABORT, 'audit_log is append-only');
END;
-- +goose StatementEnd

CREATE TABLE login_attempts (
    id         INTEGER PRIMARY KEY,
    ts         INTEGER NOT NULL,
    ip         TEXT    NOT NULL,
    ip_key     TEXT    NOT NULL,
    username   TEXT    NOT NULL DEFAULT '',
    user_id    INTEGER,
    stage      TEXT    NOT NULL CHECK (stage IN ('password', 'totp', 'webauthn', 'recovery', 'sudo')),
    success    INTEGER NOT NULL,
    reason     TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT '',
    country    TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX login_attempts_ts ON login_attempts (ts);
CREATE INDEX login_attempts_ipkey_ts ON login_attempts (ip_key, ts);
CREATE INDEX login_attempts_user_ts ON login_attempts (user_id, ts);

CREATE TABLE jails (
    id             INTEGER PRIMARY KEY,
    ip_key         TEXT    NOT NULL,
    reason         TEXT    NOT NULL,
    level          INTEGER NOT NULL,
    manual         INTEGER NOT NULL DEFAULT 0,
    created_at     INTEGER NOT NULL,
    expires_at     INTEGER NOT NULL,
    lifted_at      INTEGER,
    lifted_by      TEXT,
    fw_banned_at   INTEGER,
    fw_unbanned_at INTEGER
);
CREATE INDEX jails_ipkey_expires ON jails (ip_key, expires_at);

CREATE TABLE ip_rules (
    id         INTEGER PRIMARY KEY,
    cidr       TEXT    NOT NULL,
    kind       TEXT    NOT NULL CHECK (kind IN ('allow', 'deny')),
    comment    TEXT    NOT NULL DEFAULT '',
    expires_at INTEGER,
    created_by TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE (cidr, kind)
);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT    NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE notifiers (
    id         INTEGER PRIMARY KEY,
    kind       TEXT    NOT NULL CHECK (kind IN ('discord', 'ntfy')),
    name       TEXT    NOT NULL UNIQUE,
    config_enc BLOB    NOT NULL,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- +goose Down
DROP TABLE notifiers;
DROP TABLE settings;
DROP TABLE ip_rules;
DROP TABLE jails;
DROP TABLE login_attempts;
DROP TRIGGER audit_log_no_delete;
DROP TRIGGER audit_log_no_update;
DROP TABLE audit_log;
DROP TABLE permissions;
DROP TABLE servers;
DROP TABLE sessions;
DROP TABLE recovery_codes;
DROP TABLE webauthn_credentials;
DROP TABLE users;
```

Semantik (für Streams verbindlich): `jails.level` = Eskalationsstufe ab 1, Dauer `15min * 2^(level-1)` gedeckelt auf 7 Tage. `fw_banned_at` = agent hat Ban bestätigt; `fw_unbanned_at` = agent hat Unban nach `lifted_at` bestätigt. `login_attempts.ip` = volle Client-IP, `ip_key` = `httpx.IPKey(ip).String()`. `webauthn_credentials.credential_json` = JSON von `webauthn.Credential`. `recovery_codes.code_hash` = SHA-256 des normalisierten Codes (Kleinbuchstaben, ohne Bindestriche/Leerzeichen). `sessions.token_hash` = SHA-256 des Cookie-Tokens.

- [ ] **Step 2: `sqlc.yaml` und `internal/store/queries/common.sql`**

```yaml
version: "2"
sql:
  - engine: "sqlite"
    schema: "internal/store/migrations"
    queries: "internal/store/queries"
    gen:
      go:
        package: "store"
        out: "internal/store"
        emit_interface: false
        emit_empty_slices: true
```

```sql
-- name: GetSetting :one
SELECT value FROM settings WHERE key = ?;

-- name: UpsertSetting :exec
INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;
```

- [ ] **Step 3: Generieren**

```bash
sqlc generate && ls internal/store
```
Expected: `common.sql.go db.go models.go` (plus `migrations`, `queries`).

- [ ] **Step 4: Failing test `internal/store/store_test.go`**

```go
package store_test

import (
	"context"
	"testing"
	"time"

	"mcpanel/internal/store"
	"mcpanel/internal/store/storetest"
)

func TestMigrateIsIdempotentAndCreatesTables(t *testing.T) {
	db := storetest.Open(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	for _, table := range []string{"users", "webauthn_credentials", "recovery_codes", "sessions", "servers",
		"permissions", "audit_log", "login_attempts", "jails", "ip_rules", "settings", "notifiers"} {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (n=%d err=%v)", table, n, err)
		}
	}
}

func TestForeignKeysAndCaseInsensitiveUsernames(t *testing.T) {
	db := storetest.Open(t)
	uid := storetest.CreateUser(t, db, "Admin", "admin")
	sid := storetest.CreateServer(t, db, "survival")
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "INSERT INTO users (username, password_hash, role, webauthn_handle, password_changed_at, created_at, updated_at) VALUES ('admin','x','user',x'01',0,0,0)"); err == nil {
		t.Fatal("username uniqueness must be case-insensitive")
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO permissions (user_id, server_id, permission) VALUES (?, ?, 'server.power')", uid, sid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", uid); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRowContext(ctx, "SELECT count(*) FROM permissions").Scan(&n)
	if n != 0 {
		t.Fatalf("ON DELETE CASCADE not applied (foreign_keys off?), n=%d", n)
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	db := storetest.Open(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO audit_log (ts, actor_type, actor_name, action, result, prev_hash, hash)
		VALUES (1, 'system', 'system', 'test', 'success', '', 'h1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE audit_log SET action = 'x'"); err == nil {
		t.Fatal("UPDATE on audit_log must fail")
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM audit_log"); err == nil {
		t.Fatal("DELETE on audit_log must fail")
	}
}

func TestSecuritySettingsRoundTrip(t *testing.T) {
	db := storetest.Open(t)
	q := store.New(db)
	ctx := context.Background()
	s, err := store.LoadSecuritySettings(ctx, q)
	if err != nil || s.Enforce2FAForUsers || s.AdminAllowlistOnly || s.AllowedCountries == nil || len(s.AllowedCountries) != 0 {
		t.Fatalf("defaults = %+v, %v", s, err)
	}
	want := store.SecuritySettings{Enforce2FAForUsers: true, AllowedCountries: []string{"DE", "AT"}}
	if err := store.SaveSecuritySettings(ctx, q, want, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadSecuritySettings(ctx, q)
	if err != nil || !got.Enforce2FAForUsers || len(got.AllowedCountries) != 2 || got.AllowedCountries[1] != "AT" {
		t.Fatalf("got %+v, %v", got, err)
	}
}
```

- [ ] **Step 5: Run** `go test ./internal/store/...` → FAIL (Open/Migrate/storetest fehlen).

- [ ] **Step 6: `internal/store/open.go`**

```go
// Package store owns the SQLite database: connection, migrations and sqlc-generated queries.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // registers driver "sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens (or creates) the SQLite database with safe pragmas.
func Open(path string) (*sql.DB, error) {
	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}
	q.Set("_txlock", "immediate")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return db, nil
}

// Migrate applies all pending migrations.
func Migrate(ctx context.Context, db *sql.DB) error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectSQLite3, db, sub)
	if err != nil {
		return fmt.Errorf("store: migrations: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}
```

- [ ] **Step 7: `internal/store/settings.go`**

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const securitySettingsKey = "security"

// SecuritySettings is stored as JSON under settings.key = "security".
// Owned by stream B (API), read by stream A (2FA enforcement).
type SecuritySettings struct {
	Enforce2FAForUsers bool     `json:"enforce_2fa_for_users"`
	AdminAllowlistOnly bool     `json:"admin_allowlist_only"`
	AllowedCountries   []string `json:"allowed_countries"` // ISO 3166-1 alpha-2; empty = no country filter
}

func DefaultSecuritySettings() SecuritySettings {
	return SecuritySettings{AllowedCountries: []string{}}
}

func LoadSecuritySettings(ctx context.Context, q *Queries) (SecuritySettings, error) {
	v, err := q.GetSetting(ctx, securitySettingsKey)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultSecuritySettings(), nil
	}
	if err != nil {
		return SecuritySettings{}, err
	}
	s := DefaultSecuritySettings()
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return SecuritySettings{}, err
	}
	if s.AllowedCountries == nil {
		s.AllowedCountries = []string{}
	}
	return s, nil
}

func SaveSecuritySettings(ctx context.Context, q *Queries, s SecuritySettings, now time.Time) error {
	if s.AllowedCountries == nil {
		s.AllowedCountries = []string{}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return q.UpsertSetting(ctx, UpsertSettingParams{Key: securitySettingsKey, Value: string(b), UpdatedAt: now.UnixMilli()})
}
```

- [ ] **Step 8: `internal/store/storetest/storetest.go`**

```go
// Package storetest provides a migrated temporary database and fixtures for tests.
package storetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"path/filepath"
	"testing"

	"mcpanel/internal/store"
)

func Open(t testing.TB) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

// CreateUser inserts a user with an unusable password hash ("!") and returns its id.
func CreateUser(t testing.TB, db *sql.DB, username, role string) int64 {
	t.Helper()
	handle := make([]byte, 32)
	_, _ = rand.Read(handle)
	res, err := db.ExecContext(context.Background(),
		`INSERT INTO users (username, password_hash, role, webauthn_handle, password_changed_at, created_at, updated_at)
		 VALUES (?, '!', ?, ?, 0, 0, 0)`, username, role, handle)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func CreateServer(t testing.TB, db *sql.DB, name string) int64 {
	t.Helper()
	res, err := db.ExecContext(context.Background(), `INSERT INTO servers (name, created_at) VALUES (?, 0)`, name)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}
```

- [ ] **Step 9: Run** `go test ./internal/store/...` → PASS. Falls `goose.DialectSQLite3` nicht existiert (API-Änderung in goose), `database.DialectSQLite3` aus `github.com/pressly/goose/v3/database` verwenden.

- [ ] **Step 10: Commit** `git add sqlc.yaml internal/store && git commit -m "feat(store): add schema, migrations, sqlc setup and test fixtures" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 7: Querschnitts-Verträge – `principal`, `audit`, `notify`, `defense`, `agentapi`

**Files:**
- Create: `internal/principal/principal.go`, `internal/audit/types.go`, `internal/audit/memory.go`, `internal/notify/notify.go`, `internal/defense/guard.go`, `internal/defense/fake.go`, `internal/agent/agentapi/agentapi.go`
- Test: `internal/principal/principal_test.go`, `internal/agent/agentapi/agentapi_test.go`

**Interfaces:** Produces (alle unten vollständig angegeben).

- [ ] **Step 1: Failing tests**

```go
// internal/principal/principal_test.go
package principal_test

import (
	"context"
	"testing"

	"mcpanel/internal/principal"
)

func TestContextRoundTrip(t *testing.T) {
	if _, ok := principal.From(context.Background()); ok {
		t.Fatal("empty context must have no principal")
	}
	p := principal.Principal{UserID: 7, Username: "jannes", Role: principal.RoleAdmin, Stage: principal.StageFull}
	got, ok := principal.From(principal.With(context.Background(), p))
	if !ok || got.UserID != 7 || !got.IsAdmin() {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}
```

```go
// internal/agent/agentapi/agentapi_test.go
package agentapi_test

import (
	"net/netip"
	"testing"

	"mcpanel/internal/agent/agentapi"
)

func TestValidateBanPrefix(t *testing.T) {
	ok := []string{"203.0.113.7/32", "2001:db8:1:2::/64", "2001:db8::1/128"}
	bad := []string{"0.0.0.0/0", "10.0.0.0/8", "203.0.113.0/24", "2001:db8::/48", "::/0", "127.0.0.1/32", "::1/128"}
	for _, s := range ok {
		if err := agentapi.ValidateBanPrefix(netip.MustParsePrefix(s)); err != nil {
			t.Errorf("%s: unexpected error %v", s, err)
		}
	}
	for _, s := range bad {
		if err := agentapi.ValidateBanPrefix(netip.MustParsePrefix(s)); err == nil {
			t.Errorf("%s: expected error", s)
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/principal/ ./internal/agent/...` → FAIL.

- [ ] **Step 3: `internal/principal/principal.go`**

```go
// Package principal carries the authenticated user of a request through the context.
package principal

import (
	"context"
	"time"
)

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// Stage of a session: preauth = password ok, second factor pending;
// setup = password ok, 2FA must be configured first; full = fully authenticated.
type Stage string

const (
	StagePreAuth Stage = "preauth"
	StageSetup   Stage = "setup"
	StageFull    Stage = "full"
)

type Principal struct {
	UserID    int64
	Username  string
	Role      Role
	Stage     Stage
	SessionID int64
	SudoUntil time.Time
	CSRFToken string
}

func (p Principal) IsAdmin() bool { return p.Role == RoleAdmin }

type ctxKey struct{}

func With(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

func From(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}
```

- [ ] **Step 4: `internal/audit/types.go` und `memory.go`**

```go
// Package audit records security-relevant actions in a tamper-evident log.
// types.go is a Phase-0 contract; the SQLite implementation lives in stream D.
package audit

import "context"

type Result string

const (
	ResultSuccess Result = "success"
	ResultFailure Result = "failure"
	ResultDenied  Result = "denied"
)

type ActorType string

const (
	ActorUser   ActorType = "user"
	ActorSystem ActorType = "system"
	ActorCLI    ActorType = "cli"
)

// Entry describes one action. If ActorType is empty, the recorder fills actor
// fields from principal.From(ctx) (or ActorSystem if none) and IP from httpx.ClientIP(ctx).
type Entry struct {
	Action    string
	Target    string
	Result    Result
	Details   map[string]any
	ActorType ActorType
	ActorID   *int64
	ActorName string
	IP        string
}

type Recorder interface {
	Record(ctx context.Context, e Entry) error
}

// Actions. Streams must use these constants.
const (
	ActionLogin              = "auth.login"
	ActionLogout             = "auth.logout"
	ActionPasswordChange     = "auth.password_change"
	ActionTOTPEnable         = "auth.totp_enable"
	ActionTOTPDisable        = "auth.totp_disable"
	ActionPasskeyAdd         = "auth.passkey_add"
	ActionPasskeyRemove      = "auth.passkey_remove"
	ActionRecoveryRegenerate = "auth.recovery_regenerate"
	ActionRecoveryUsed       = "auth.recovery_used"
	ActionSessionRevoke      = "auth.session_revoke"
	ActionSudo               = "auth.sudo"
	ActionUserCreate         = "user.create"
	ActionUserUpdate         = "user.update"
	ActionUserDelete         = "user.delete"
	ActionUserReset2FA       = "user.reset_2fa"
	ActionPermissionsSet     = "rbac.permissions_set"
	ActionServerCreate       = "server.create"
	ActionServerDelete       = "server.delete"
	ActionServerPower        = "server.power"
	ActionJailAuto           = "security.jail_auto"
	ActionJailCreate         = "security.jail_create"
	ActionJailLift           = "security.jail_lift"
	ActionIPRuleCreate       = "security.ip_rule_create"
	ActionIPRuleDelete       = "security.ip_rule_delete"
	ActionSettingsUpdate     = "security.settings_update"
	ActionNotifierCreate     = "security.notifier_create"
	ActionNotifierUpdate     = "security.notifier_update"
	ActionNotifierDelete     = "security.notifier_delete"
)
```

```go
package audit

import (
	"context"
	"sync"
)

// MemoryRecorder is a test double that keeps entries in memory.
type MemoryRecorder struct {
	mu      sync.Mutex
	Entries []Entry
}

func (m *MemoryRecorder) Record(_ context.Context, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Entries = append(m.Entries, e)
	return nil
}

func (m *MemoryRecorder) Actions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.Entries))
	for i, e := range m.Entries {
		out[i] = e.Action
	}
	return out
}
```

- [ ] **Step 5: `internal/notify/notify.go`**

```go
// Package notify sends security notifications. notify.go is a Phase-0 contract;
// the dispatcher and Discord/ntfy senders live in stream B.
package notify

import (
	"context"
	"sync"
	"time"
)

type EventType string

const (
	EventNewDeviceLogin     EventType = "login.new_device"
	EventJailed             EventType = "security.jailed"
	EventPermissionsChanged EventType = "rbac.changed"
	EventUserCreated        EventType = "user.created"
	EventUserDeleted        EventType = "user.deleted"
	Event2FAChanged         EventType = "auth.2fa_changed"
	EventAuditChainBroken   EventType = "audit.chain_broken"
	EventTest               EventType = "test"
)

type Event struct {
	Type     EventType
	Title    string
	Message  string
	IP       string // may be empty; used for deduplication
	Username string
	Time     time.Time
}

// Notifier must never block the caller and never return errors to it.
type Notifier interface {
	Notify(ctx context.Context, e Event)
}

type Nop struct{}

func (Nop) Notify(context.Context, Event) {}

// Recording is a test double.
type Recording struct {
	mu     sync.Mutex
	Events []Event
}

func (r *Recording) Notify(_ context.Context, e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Events = append(r.Events, e)
}

func (r *Recording) Types() []EventType {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]EventType, len(r.Events))
	for i, e := range r.Events {
		out[i] = e.Type
	}
	return out
}
```

- [ ] **Step 6: `internal/defense/guard.go` und `fake.go`**

```go
// Package defense implements rate limits, jails, IP rules and GeoIP.
// guard.go is a Phase-0 contract used by stream A; the implementation lives in stream B.
package defense

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

type AttemptStage string

const (
	StagePassword AttemptStage = "password"
	StageTOTP     AttemptStage = "totp"
	StageWebAuthn AttemptStage = "webauthn"
	StageRecovery AttemptStage = "recovery"
	StageSudo     AttemptStage = "sudo"
)

type Attempt struct {
	IP        netip.Addr
	Username  string
	UserID    *int64
	Stage     AttemptStage
	Success   bool
	Reason    string // e.g. "bad_password", "unknown_user", "bad_code"
	UserAgent string
}

var (
	ErrJailed       = errors.New("defense: ip is jailed")
	ErrIPBlocked    = errors.New("defense: ip blocked by rule or country")
	ErrAdminNetwork = errors.New("defense: admin login not allowed from this network")
)

type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return fmt.Sprintf("defense: rate limited, retry after %s", e.RetryAfter) }

// LoginGuard is consulted by the auth handlers.
type LoginGuard interface {
	// CheckLogin is called before verifying a password. Returns nil, ErrJailed or *RateLimitError.
	CheckLogin(ctx context.Context, ip netip.Addr, username string) error
	// CheckSecondFactor is called before verifying a 2FA/sudo code. key identifies the session.
	CheckSecondFactor(ctx context.Context, ip netip.Addr, key string) error
	// CheckAdminNetwork returns ErrAdminNetwork if admin logins are restricted and ip is not allowlisted.
	CheckAdminNetwork(ctx context.Context, ip netip.Addr) error
	// RecordAttempt stores the attempt and jails the IP when the failure threshold is reached.
	RecordAttempt(ctx context.Context, a Attempt) error
}

// GeoResolver maps an IP to an ISO 3166-1 alpha-2 country code ("" if unknown).
type GeoResolver interface {
	Country(ip netip.Addr) string
}

type NopGeo struct{}

func (NopGeo) Country(netip.Addr) string { return "" }
```

```go
package defense

import (
	"context"
	"net/netip"
	"sync"
)

// FakeGuard is a test double for LoginGuard. Set the *Err fields to simulate blocks.
type FakeGuard struct {
	mu              sync.Mutex
	Attempts        []Attempt
	LoginErr        error
	SecondFactorErr error
	AdminNetErr     error
}

func (f *FakeGuard) CheckLogin(context.Context, netip.Addr, string) error        { return f.LoginErr }
func (f *FakeGuard) CheckSecondFactor(context.Context, netip.Addr, string) error { return f.SecondFactorErr }
func (f *FakeGuard) CheckAdminNetwork(context.Context, netip.Addr) error         { return f.AdminNetErr }

func (f *FakeGuard) RecordAttempt(_ context.Context, a Attempt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Attempts = append(f.Attempts, a)
	return nil
}

func (f *FakeGuard) Recorded() []Attempt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Attempt(nil), f.Attempts...)
}
```

- [ ] **Step 7: `internal/agent/agentapi/agentapi.go`**

```go
// Package agentapi is the wire contract between panel and agent (JSON over HTTP on a Unix socket).
package agentapi

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

const (
	PathHealth = "/v1/health"
	PathBan    = "/v1/firewall/ban"
	PathUnban  = "/v1/firewall/unban"
	PathBans   = "/v1/firewall/bans"

	MaxBanDuration = 8 * 24 * time.Hour
)

type HealthResponse struct {
	Status   string `json:"status"` // "ok"
	Version  string `json:"version"`
	Firewall string `json:"firewall"` // "nftables" | "noop"
}

type BanRequest struct {
	Prefix         string `json:"prefix"` // e.g. "203.0.113.7/32" or "2001:db8:1:2::/64"
	TimeoutSeconds int64  `json:"timeout_seconds"`
}

type UnbanRequest struct {
	Prefix string `json:"prefix"`
}

type Ban struct {
	Prefix           string `json:"prefix"`
	ExpiresInSeconds int64  `json:"expires_in_seconds"`
}

type BansResponse struct {
	Bans []Ban `json:"bans"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

var ErrUnavailable = errors.New("agent unavailable")

// Client is used by the panel (implemented in stream C).
type Client interface {
	Health(ctx context.Context) (HealthResponse, error)
	Ban(ctx context.Context, prefix netip.Prefix, d time.Duration) error
	Unban(ctx context.Context, prefix netip.Prefix) error
	Bans(ctx context.Context) ([]Ban, error)
}

// Firewall is the agent-side backend (nftables on Linux, noop on macOS).
type Firewall interface {
	Name() string
	Ban(ctx context.Context, prefix netip.Prefix, d time.Duration) error
	Unban(ctx context.Context, prefix netip.Prefix) error
	List(ctx context.Context) ([]Ban, error)
}

// ValidateBanPrefix only allows single IPv4 hosts (/32) and IPv6 /64../128,
// never loopback – so a compromised panel cannot firewall whole networks.
func ValidateBanPrefix(p netip.Prefix) error {
	if !p.IsValid() {
		return errors.New("invalid prefix")
	}
	a := p.Addr()
	if a.IsLoopback() || a.IsUnspecified() {
		return fmt.Errorf("refusing to ban %s", p)
	}
	if a.Is4() && p.Bits() != 32 {
		return fmt.Errorf("IPv4 bans must be /32, got %s", p)
	}
	if a.Is6() && p.Bits() < 64 {
		return fmt.Errorf("IPv6 bans must be /64 or narrower, got %s", p)
	}
	return nil
}
```

- [ ] **Step 8: Run** `go test ./internal/... && go vet ./...` → PASS.

- [ ] **Step 9: Commit** `git add internal/principal internal/audit internal/notify internal/defense internal/agent && git commit -m "feat: add cross-stream contracts (principal, audit, notify, defense, agentapi)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 8: `rbac` – Regeln und erzwingender Router

**Files:** Create `internal/rbac/rule.go`, `internal/rbac/router.go`; Test `internal/rbac/router_test.go`

**Interfaces:**
- Consumes: `principal`, `clock`, `httpx`
- Produces:
```go
type Permission string   // PermServerView "server.view", PermServerPower "server.power", PermServerConsole,
                         // PermServerFiles, PermServerPlugins, PermServerBackups
var AllPermissions []Permission
func (p Permission) Valid() bool
type Rule struct{ /* unexported */ }
func Public() Rule
func Authenticated() Rule                     // stage full
func Admin() Rule                             // stage full + role admin
func Server(p Permission, param string) Rule  // stage full + (admin or permission on server {param})
func (r Rule) WithSudo() Rule
func (r Rule) AllowStages(s ...principal.Stage) Rule
func (r Rule) IsPublic() bool
func (r Rule) RequiresSudo() bool
func (r Rule) String() string
type Checker interface {
	HasServerPermission(ctx context.Context, userID, serverID int64, p Permission) (bool, error)
}
type RouteInfo struct{ Method, Pattern string; Rule Rule }
func NewRouter(mux chi.Router, checker Checker, c clock.Clock) *Router
func (rt *Router) Get/Post/Put/Patch/Delete(pattern string, rule Rule, h http.HandlerFunc)
func (rt *Router) Route(pattern string, fn func(*Router))   // only Phase 2 uses this
func (rt *Router) Routes() []RouteInfo
```
Fehlerverhalten: kein Principal → 401 `unauthorized`; falsche Stage → 403 `setup_2fa_required` (wenn Stage setup) sonst 401 `unauthorized`; kein Admin → 403 `forbidden`; Server-ID unparsbar → 404 `not_found`; kein Recht → 403 `forbidden`; Sudo abgelaufen → 403 `sudo_required`. Eine Route mit `Rule{}` (Nullwert) führt beim Registrieren zu `panic`.

- [ ] **Step 1: Failing test**

```go
package rbac_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/clock"
	"mcpanel/internal/principal"
	"mcpanel/internal/rbac"
)

type fakeChecker map[[2]int64][]rbac.Permission

func (f fakeChecker) HasServerPermission(_ context.Context, uid, sid int64, p rbac.Permission) (bool, error) {
	for _, have := range f[[2]int64{uid, sid}] {
		if have == p {
			return true, nil
		}
	}
	return false, nil
}

var now = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T) (http.Handler, *rbac.Router) {
	t.Helper()
	mux := chi.NewRouter()
	checker := fakeChecker{{2, 10}: {rbac.PermServerView, rbac.PermServerPower}}
	rt := rbac.NewRouter(mux, checker, clock.NewFake(now))
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/health", rbac.Public(), ok)
		api.Get("/me", rbac.Authenticated().AllowStages(principal.StagePreAuth, principal.StageSetup, principal.StageFull), ok)
		api.Get("/servers", rbac.Authenticated(), ok)
		api.Post("/users", rbac.Admin().WithSudo(), ok)
		api.Post("/servers/{id}/power", rbac.Server(rbac.PermServerPower, "id"), ok)
	})
	return mux, rt
}

func do(h http.Handler, method, path string, p *principal.Principal) (int, string) {
	req := httptest.NewRequest(method, path, nil)
	if p != nil {
		req = req.WithContext(principal.With(req.Context(), *p))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body struct{ Error string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Error
}

func TestEnforcement(t *testing.T) {
	h, _ := setup(t)
	admin := &principal.Principal{UserID: 1, Role: principal.RoleAdmin, Stage: principal.StageFull}
	adminSudo := &principal.Principal{UserID: 1, Role: principal.RoleAdmin, Stage: principal.StageFull, SudoUntil: now.Add(time.Minute)}
	adminSudoExpired := &principal.Principal{UserID: 1, Role: principal.RoleAdmin, Stage: principal.StageFull, SudoUntil: now}
	user := &principal.Principal{UserID: 2, Role: principal.RoleUser, Stage: principal.StageFull}
	other := &principal.Principal{UserID: 3, Role: principal.RoleUser, Stage: principal.StageFull}
	pre := &principal.Principal{UserID: 2, Role: principal.RoleUser, Stage: principal.StagePreAuth}
	setupStage := &principal.Principal{UserID: 1, Role: principal.RoleAdmin, Stage: principal.StageSetup}

	cases := []struct {
		name, method, path string
		p                  *principal.Principal
		code               int
		err                string
	}{
		{"public without session", "GET", "/api/health", nil, 204, ""},
		{"auth route without session", "GET", "/api/servers", nil, 401, "unauthorized"},
		{"preauth cannot use full route", "GET", "/api/servers", pre, 401, "unauthorized"},
		{"setup stage gets setup error", "GET", "/api/servers", setupStage, 403, "setup_2fa_required"},
		{"preauth allowed on /me", "GET", "/api/me", pre, 204, ""},
		{"user on admin route", "POST", "/api/users", user, 403, "forbidden"},
		{"admin without sudo", "POST", "/api/users", admin, 403, "sudo_required"},
		{"admin with expired sudo", "POST", "/api/users", adminSudoExpired, 403, "sudo_required"},
		{"admin with sudo", "POST", "/api/users", adminSudo, 204, ""},
		{"user with power on server 10", "POST", "/api/servers/10/power", user, 204, ""},
		{"user without permission on server 11", "POST", "/api/servers/11/power", user, 403, "forbidden"},
		{"other user on server 10", "POST", "/api/servers/10/power", other, 403, "forbidden"},
		{"admin on any server", "POST", "/api/servers/11/power", admin, 204, ""},
		{"non-numeric server id", "POST", "/api/servers/abc/power", user, 404, "not_found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, errCode := do(h, tc.method, tc.path, tc.p)
			if code != tc.code || errCode != tc.err {
				t.Fatalf("got %d %q, want %d %q", code, errCode, tc.code, tc.err)
			}
		})
	}
}

func TestRoutesRegistry(t *testing.T) {
	_, rt := setup(t)
	routes := rt.Routes()
	if len(routes) != 5 {
		t.Fatalf("len(routes) = %d", len(routes))
	}
	found := false
	for _, r := range routes {
		if r.Method == "POST" && r.Pattern == "/api/users" && r.Rule.RequiresSudo() && !r.Rule.IsPublic() {
			found = true
		}
	}
	if !found {
		t.Fatalf("POST /api/users not registered correctly: %+v", routes)
	}
}

func TestZeroRulePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for zero Rule")
		}
	}()
	rt := rbac.NewRouter(chi.NewRouter(), fakeChecker{}, clock.Real{})
	rt.Get("/x", rbac.Rule{}, func(http.ResponseWriter, *http.Request) {})
}
```

- [ ] **Step 2: Run** `go test ./internal/rbac/` → FAIL.

- [ ] **Step 3: `internal/rbac/rule.go`**

```go
// Package rbac declares and enforces access rules for every API route.
package rbac

import (
	"fmt"
	"slices"

	"mcpanel/internal/principal"
)

type Permission string

const (
	PermServerView    Permission = "server.view"
	PermServerPower   Permission = "server.power"
	PermServerConsole Permission = "server.console"
	PermServerFiles   Permission = "server.files"
	PermServerPlugins Permission = "server.plugins"
	PermServerBackups Permission = "server.backups"
)

var AllPermissions = []Permission{
	PermServerView, PermServerPower, PermServerConsole, PermServerFiles, PermServerPlugins, PermServerBackups,
}

func (p Permission) Valid() bool { return slices.Contains(AllPermissions, p) }

type ruleKind int

const (
	kindInvalid ruleKind = iota
	kindPublic
	kindAuthenticated
	kindAdmin
	kindServer
)

// Rule describes who may call a route. The zero value is invalid on purpose.
type Rule struct {
	kind   ruleKind
	perm   Permission
	param  string
	sudo   bool
	stages []principal.Stage
}

func full() []principal.Stage { return []principal.Stage{principal.StageFull} }

func Public() Rule        { return Rule{kind: kindPublic} }
func Authenticated() Rule { return Rule{kind: kindAuthenticated, stages: full()} }
func Admin() Rule         { return Rule{kind: kindAdmin, stages: full()} }

func Server(p Permission, param string) Rule {
	if !p.Valid() || param == "" {
		panic(fmt.Sprintf("rbac: invalid server rule %q/%q", p, param))
	}
	return Rule{kind: kindServer, perm: p, param: param, stages: full()}
}

func (r Rule) WithSudo() Rule {
	if r.kind == kindPublic {
		panic("rbac: public routes cannot require sudo")
	}
	r.sudo = true
	return r
}

func (r Rule) AllowStages(s ...principal.Stage) Rule {
	if r.kind == kindPublic {
		panic("rbac: public routes have no stages")
	}
	r.stages = slices.Clone(s)
	return r
}

func (r Rule) IsPublic() bool     { return r.kind == kindPublic }
func (r Rule) RequiresSudo() bool { return r.sudo }

func (r Rule) String() string {
	var s string
	switch r.kind {
	case kindPublic:
		return "public"
	case kindAuthenticated:
		s = "authenticated"
	case kindAdmin:
		s = "admin"
	case kindServer:
		s = fmt.Sprintf("server(%s,{%s})", r.perm, r.param)
	default:
		return "invalid"
	}
	if r.sudo {
		s += "+sudo"
	}
	return fmt.Sprintf("%s%v", s, r.stages)
}
```

- [ ] **Step 4: `internal/rbac/router.go`**

```go
package rbac

import (
	"context"
	"net/http"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/clock"
	"mcpanel/internal/httpx"
	"mcpanel/internal/principal"
)

// Checker answers per-server permission questions (implemented in stream D).
type Checker interface {
	HasServerPermission(ctx context.Context, userID, serverID int64, p Permission) (bool, error)
}

type RouteInfo struct {
	Method  string
	Pattern string
	Rule    Rule
}

// Router wraps chi so that every route must declare a Rule.
type Router struct {
	mux     chi.Router
	prefix  string
	checker Checker
	clock   clock.Clock
	reg     *[]RouteInfo
}

func NewRouter(mux chi.Router, checker Checker, c clock.Clock) *Router {
	return &Router{mux: mux, checker: checker, clock: c, reg: &[]RouteInfo{}}
}

func (rt *Router) Get(p string, rule Rule, h http.HandlerFunc)    { rt.handle(http.MethodGet, p, rule, h) }
func (rt *Router) Post(p string, rule Rule, h http.HandlerFunc)   { rt.handle(http.MethodPost, p, rule, h) }
func (rt *Router) Put(p string, rule Rule, h http.HandlerFunc)    { rt.handle(http.MethodPut, p, rule, h) }
func (rt *Router) Patch(p string, rule Rule, h http.HandlerFunc)  { rt.handle(http.MethodPatch, p, rule, h) }
func (rt *Router) Delete(p string, rule Rule, h http.HandlerFunc) { rt.handle(http.MethodDelete, p, rule, h) }

// Route mounts a sub-router. Only Phase 2 wiring uses this (once, for "/api").
func (rt *Router) Route(p string, fn func(*Router)) {
	rt.mux.Route(p, func(sub chi.Router) {
		fn(&Router{mux: sub, prefix: rt.prefix + p, checker: rt.checker, clock: rt.clock, reg: rt.reg})
	})
}

func (rt *Router) Routes() []RouteInfo { return slices.Clone(*rt.reg) }

func (rt *Router) handle(method, p string, rule Rule, h http.HandlerFunc) {
	if rule.kind == kindInvalid {
		panic("rbac: route " + method + " " + rt.prefix + p + " registered without a rule")
	}
	*rt.reg = append(*rt.reg, RouteInfo{Method: method, Pattern: rt.prefix + p, Rule: rule})
	rt.mux.Method(method, p, rt.enforce(rule, h))
}

func (rt *Router) enforce(rule Rule, h http.HandlerFunc) http.HandlerFunc {
	if rule.kind == kindPublic {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := principal.From(r.Context())
		if !ok {
			httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
			return
		}
		if !slices.Contains(rule.stages, p.Stage) {
			if p.Stage == principal.StageSetup {
				httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeSetupRequired, "two-factor setup required")
			} else {
				httpx.WriteError(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication incomplete")
			}
			return
		}
		switch rule.kind {
		case kindAdmin:
			if !p.IsAdmin() {
				httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "admin role required")
				return
			}
		case kindServer:
			if !p.IsAdmin() {
				id, err := strconv.ParseInt(chi.URLParam(r, rule.param), 10, 64)
				if err != nil || id <= 0 {
					httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "server not found")
					return
				}
				allowed, err := rt.checker.HasServerPermission(r.Context(), p.UserID, id, rule.perm)
				if err != nil {
					httpx.Internal(w, r, err)
					return
				}
				if !allowed {
					httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeForbidden, "missing permission "+string(rule.perm))
					return
				}
			}
		}
		if rule.sudo && !rt.clock.Now().Before(p.SudoUntil) {
			httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeSudoRequired, "re-authentication required")
			return
		}
		h(w, r)
	}
}
```

- [ ] **Step 5: Run** `go test ./internal/rbac/` → PASS.

- [ ] **Step 6: Commit** `git add internal/rbac && git commit -m "feat(rbac): add declarative route rules and enforcing router" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 9: OpenAPI-Vertrag `api/openapi.yaml`

**Files:** Create `api/openapi.yaml`

**Interfaces:** Produces den API-Vertrag für Streams A, B, D (Backend) und E (Frontend). Die Tabelle unten ist **verbindlich**; die YAML-Datei ist ihre exakte Übersetzung.

**Übersetzungsregeln:**
- `openapi: 3.1.0`, `info.title: mc-panel API`, `info.version: 0.1.0`, `servers: [{url: /api}]` – Pfade in der Datei daher **ohne** `/api`-Präfix.
- `components.securitySchemes.session`: `type: apiKey, in: cookie, name: __Host-sid`. Global `security: [{session: []}]`; Public-Routen setzen `security: []`.
- Jede Nicht-GET-Operation mit Session hat Header-Parameter `X-CSRF-Token` (required) via `$ref: '#/components/parameters/CSRF'`. `POST /auth/login` hat **keinen** CSRF-Header.
- Jede Operation listet die Fehlerantworten aus der Spalte „Fehler“ als `$ref: '#/components/responses/<Name>'`, alle mit Schema `Error`.
- `operationId` = Spalte „operationId“.
- Zeitstempel sind `integer, format: int64` (Unix-ms). Nullable Felder: `type: [integer, "null"]` bzw. `[string, "null"]`.
- Opaque WebAuthn-Objekte: `type: object, additionalProperties: true`.

**Components – Schemas (exakt so):**

```yaml
components:
  securitySchemes:
    session: { type: apiKey, in: cookie, name: __Host-sid }
  parameters:
    CSRF: { name: X-CSRF-Token, in: header, required: true, schema: { type: string } }
    IdPath: { name: id, in: path, required: true, schema: { type: integer, format: int64 } }
    Cursor: { name: cursor, in: query, required: false, schema: { type: string } }
    Limit: { name: limit, in: query, required: false, schema: { type: integer, minimum: 1, maximum: 200, default: 50 } }
  responses:
    BadRequest: { description: Bad request, content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
    Unauthorized: { description: Unauthorized, content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
    Forbidden: { description: Forbidden (forbidden, sudo_required, setup_2fa_required, csrf_invalid, ip_blocked, jailed), content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
    NotFound: { description: Not found, content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
    Conflict: { description: Conflict, content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
    TooManyRequests: { description: Rate limited (Retry-After header set), content: { application/json: { schema: { $ref: '#/components/schemas/Error' } } } }
  schemas:
    Error:
      type: object
      required: [error, message]
      properties:
        error: { type: string, enum: [bad_request, validation_failed, unauthorized, invalid_credentials, invalid_code, forbidden, sudo_required, setup_2fa_required, csrf_invalid, not_found, conflict, payload_too_large, rate_limited, jailed, ip_blocked, agent_unavailable, internal] }
        message: { type: string }
        request_id: { type: string }
    Role: { type: string, enum: [admin, user] }
    Stage: { type: string, enum: [preauth, setup, full] }
    Locale: { type: string, enum: [de, en] }
    Permission: { type: string, enum: [server.view, server.power, server.console, server.files, server.plugins, server.backups] }
    SecondFactor: { type: string, enum: [totp, webauthn, recovery] }
    Me:
      type: object
      required: [id, username, role, stage, locale, totp_enabled, passkey_count, recovery_codes_remaining, second_factors, sudo_until, csrf_token]
      properties:
        id: { type: integer, format: int64 }
        username: { type: string }
        role: { $ref: '#/components/schemas/Role' }
        stage: { $ref: '#/components/schemas/Stage' }
        locale: { $ref: '#/components/schemas/Locale' }
        totp_enabled: { type: boolean }
        passkey_count: { type: integer }
        recovery_codes_remaining: { type: integer }
        second_factors: { type: array, items: { $ref: '#/components/schemas/SecondFactor' }, description: Methods usable in preauth stage }
        sudo_until: { type: integer, format: int64, description: Unix ms; 0 = no sudo }
        csrf_token: { type: string }
    LoginRequest:
      type: object
      required: [username, password]
      properties: { username: { type: string, maxLength: 64 }, password: { type: string, maxLength: 256 } }
    CodeRequest:
      type: object
      required: [code]
      properties: { code: { type: string, maxLength: 32 } }
    WebAuthnOptions: { type: object, additionalProperties: true, description: 'CredentialCreationOptions / CredentialRequestOptions JSON ({publicKey: ...})' }
    WebAuthnAssertion:
      type: object
      required: [credential]
      properties: { credential: { type: object, additionalProperties: true } }
    PasskeyRegistration:
      type: object
      required: [name, credential]
      properties: { name: { type: string, minLength: 1, maxLength: 64 }, credential: { type: object, additionalProperties: true } }
    SudoResponse:
      type: object
      required: [sudo_until]
      properties: { sudo_until: { type: integer, format: int64 } }
    PasswordChangeRequest:
      type: object
      required: [current_password, new_password]
      properties: { current_password: { type: string, maxLength: 256 }, new_password: { type: string, minLength: 12, maxLength: 256 } }
    UpdateMeRequest:
      type: object
      required: [locale]
      properties: { locale: { $ref: '#/components/schemas/Locale' } }
    TotpSetupResponse:
      type: object
      required: [secret, otpauth_url]
      properties: { secret: { type: string }, otpauth_url: { type: string } }
    RecoveryCodesResponse:
      type: object
      required: [codes]
      properties: { codes: { type: array, items: { type: string } }, description: Empty if the user already had recovery codes }
    Passkey:
      type: object
      required: [id, name, created_at, last_used_at]
      properties:
        id: { type: integer, format: int64 }
        name: { type: string }
        created_at: { type: integer, format: int64 }
        last_used_at: { type: [integer, "null"], format: int64 }
    PasskeyCreated:
      type: object
      required: [passkey, recovery_codes]
      properties: { passkey: { $ref: '#/components/schemas/Passkey' }, recovery_codes: { type: array, items: { type: string } } }
    Session:
      type: object
      required: [id, user_id, username, current, ip, country, user_agent, created_at, last_seen_at, expires_at]
      properties:
        id: { type: integer, format: int64 }
        user_id: { type: integer, format: int64 }
        username: { type: string }
        current: { type: boolean }
        ip: { type: string }
        country: { type: string }
        user_agent: { type: string }
        created_at: { type: integer, format: int64 }
        last_seen_at: { type: integer, format: int64 }
        expires_at: { type: integer, format: int64 }
    User:
      type: object
      required: [id, username, role, totp_enabled, passkey_count, created_at, last_login_at]
      properties:
        id: { type: integer, format: int64 }
        username: { type: string }
        role: { $ref: '#/components/schemas/Role' }
        totp_enabled: { type: boolean }
        passkey_count: { type: integer }
        created_at: { type: integer, format: int64 }
        last_login_at: { type: [integer, "null"], format: int64 }
    CreateUserRequest:
      type: object
      required: [username, password, role]
      properties:
        username: { type: string, pattern: '^[a-zA-Z0-9_.-]{3,32}$' }
        password: { type: string, minLength: 12, maxLength: 256 }
        role: { $ref: '#/components/schemas/Role' }
    UpdateUserRequest:
      type: object
      properties:
        role: { $ref: '#/components/schemas/Role' }
        password: { type: string, minLength: 12, maxLength: 256 }
    Server:
      type: object
      required: [id, name, permissions]
      properties:
        id: { type: integer, format: int64 }
        name: { type: string }
        permissions: { type: array, items: { $ref: '#/components/schemas/Permission' }, description: Effective permissions of the caller (admins get all) }
    CreateServerRequest:
      type: object
      required: [name]
      properties: { name: { type: string, pattern: '^[a-z0-9][a-z0-9-]{1,31}$' } }
    PowerRequest:
      type: object
      required: [action]
      properties: { action: { type: string, enum: [start, stop, restart] } }
    PowerResponse:
      type: object
      required: [status]
      properties: { status: { type: string, enum: [accepted] } }
    PermissionGrant:
      type: object
      required: [server_id, permissions]
      properties:
        server_id: { type: integer, format: int64 }
        permissions: { type: array, items: { $ref: '#/components/schemas/Permission' } }
    UserPermissions:
      type: object
      required: [grants]
      properties: { grants: { type: array, items: { $ref: '#/components/schemas/PermissionGrant' } } }
    AuditEntry:
      type: object
      required: [id, ts, actor_type, actor_id, actor_name, action, target, ip, result, details, hash]
      properties:
        id: { type: integer, format: int64 }
        ts: { type: integer, format: int64 }
        actor_type: { type: string, enum: [user, system, cli] }
        actor_id: { type: [integer, "null"], format: int64 }
        actor_name: { type: string }
        action: { type: string }
        target: { type: string }
        ip: { type: string }
        result: { type: string, enum: [success, failure, denied] }
        details: { type: object, additionalProperties: true }
        hash: { type: string }
    AuditPage:
      type: object
      required: [items, next_cursor]
      properties: { items: { type: array, items: { $ref: '#/components/schemas/AuditEntry' } }, next_cursor: { type: [string, "null"] } }
    AuditVerify:
      type: object
      required: [ok, checked, first_bad_id]
      properties: { ok: { type: boolean }, checked: { type: integer }, first_bad_id: { type: [integer, "null"], format: int64 } }
    SeriesPoint:
      type: object
      required: [t, success, failure]
      properties: { t: { type: integer, format: int64 }, success: { type: integer }, failure: { type: integer } }
    IPCount:
      type: object
      required: [ip_key, country, failures]
      properties: { ip_key: { type: string }, country: { type: string }, failures: { type: integer } }
    CountryCount:
      type: object
      required: [country, failures]
      properties: { country: { type: string }, failures: { type: integer } }
    NewDeviceLogin:
      type: object
      required: [ts, username, ip, country, user_agent]
      properties: { ts: { type: integer, format: int64 }, username: { type: string }, ip: { type: string }, country: { type: string }, user_agent: { type: string } }
    SecurityOverview:
      type: object
      required: [range, series, top_ips, top_countries, active_jails, failures_total, new_device_logins]
      properties:
        range: { type: string, enum: [24h, 7d, 30d] }
        series: { type: array, items: { $ref: '#/components/schemas/SeriesPoint' } }
        top_ips: { type: array, items: { $ref: '#/components/schemas/IPCount' } }
        top_countries: { type: array, items: { $ref: '#/components/schemas/CountryCount' } }
        active_jails: { type: integer }
        failures_total: { type: integer }
        new_device_logins: { type: array, items: { $ref: '#/components/schemas/NewDeviceLogin' } }
    LoginAttempt:
      type: object
      required: [id, ts, ip, username, stage, success, reason, country, user_agent]
      properties:
        id: { type: integer, format: int64 }
        ts: { type: integer, format: int64 }
        ip: { type: string }
        username: { type: string }
        stage: { type: string, enum: [password, totp, webauthn, recovery, sudo] }
        success: { type: boolean }
        reason: { type: string }
        country: { type: string }
        user_agent: { type: string }
    LoginAttemptPage:
      type: object
      required: [items, next_cursor]
      properties: { items: { type: array, items: { $ref: '#/components/schemas/LoginAttempt' } }, next_cursor: { type: [string, "null"] } }
    Jail:
      type: object
      required: [id, ip_key, reason, level, manual, created_at, expires_at, lifted_at, lifted_by, firewall_synced]
      properties:
        id: { type: integer, format: int64 }
        ip_key: { type: string }
        reason: { type: string }
        level: { type: integer }
        manual: { type: boolean }
        created_at: { type: integer, format: int64 }
        expires_at: { type: integer, format: int64 }
        lifted_at: { type: [integer, "null"], format: int64 }
        lifted_by: { type: [string, "null"] }
        firewall_synced: { type: boolean }
    CreateJailRequest:
      type: object
      required: [ip, duration_minutes, reason]
      properties:
        ip: { type: string, description: IP address; normalized to /32 or /64 }
        duration_minutes: { type: integer, minimum: 1, maximum: 10080 }
        reason: { type: string, maxLength: 200 }
    IPRule:
      type: object
      required: [id, cidr, kind, comment, expires_at, created_by, created_at]
      properties:
        id: { type: integer, format: int64 }
        cidr: { type: string }
        kind: { type: string, enum: [allow, deny] }
        comment: { type: string }
        expires_at: { type: [integer, "null"], format: int64 }
        created_by: { type: string }
        created_at: { type: integer, format: int64 }
    CreateIPRuleRequest:
      type: object
      required: [cidr, kind, comment]
      properties:
        cidr: { type: string }
        kind: { type: string, enum: [allow, deny] }
        comment: { type: string, maxLength: 200 }
        expires_at: { type: [integer, "null"], format: int64 }
    SecuritySettings:
      type: object
      required: [enforce_2fa_for_users, admin_allowlist_only, allowed_countries]
      properties:
        enforce_2fa_for_users: { type: boolean }
        admin_allowlist_only: { type: boolean }
        allowed_countries: { type: array, items: { type: string, pattern: '^[A-Z]{2}$' } }
    SecuritySettingsResponse:
      allOf:
        - $ref: '#/components/schemas/SecuritySettings'
        - type: object
          required: [geoip_available]
          properties: { geoip_available: { type: boolean } }
    Notifier:
      type: object
      required: [id, kind, name, enabled, target_masked, created_at]
      properties:
        id: { type: integer, format: int64 }
        kind: { type: string, enum: [discord, ntfy] }
        name: { type: string }
        enabled: { type: boolean }
        target_masked: { type: string, description: 'e.g. https://discord.com/api/webhooks/…/****' }
        created_at: { type: integer, format: int64 }
    CreateNotifierRequest:
      type: object
      required: [kind, name, enabled, url]
      properties:
        kind: { type: string, enum: [discord, ntfy] }
        name: { type: string, minLength: 1, maxLength: 64 }
        enabled: { type: boolean }
        url: { type: string, description: 'discord: webhook URL; ntfy: server base URL' }
        topic: { type: string, description: ntfy only }
        token: { type: string, description: ntfy access token (optional) }
    UpdateNotifierRequest:
      type: object
      properties:
        name: { type: string, minLength: 1, maxLength: 64 }
        enabled: { type: boolean }
        url: { type: string }
        topic: { type: string }
        token: { type: string }
    AgentStatus:
      type: object
      required: [reachable, firewall, version, pending_sync]
      properties:
        reachable: { type: boolean }
        firewall: { type: string }
        version: { type: string }
        pending_sync: { type: integer }
    Health:
      type: object
      required: [status]
      properties: { status: { type: string, enum: [ok] } }
```

**Paths (verbindlich; Pfad ohne `/api`; Regel = rbac-Regel, die der Stream verwenden MUSS):**

| Methode Pfad | operationId | Regel | Request | Response | Fehler | Stream |
|---|---|---|---|---|---|---|
| GET /health | getHealth | Public | – | 200 Health | – | 0 |
| POST /auth/login | login | Public | LoginRequest | 200 Me | BadRequest, Unauthorized (invalid_credentials), Forbidden (jailed, ip_blocked), TooManyRequests | A |
| GET /auth/me | getMe | Authenticated, Stages preauth/setup/full | – | 200 Me | Unauthorized | A |
| POST /auth/logout | logout | Authenticated, Stages preauth/setup/full | – | 204 | Unauthorized | A |
| POST /auth/2fa/totp | verifyTotp | Authenticated, Stage preauth | CodeRequest | 200 Me | Unauthorized (invalid_code), Forbidden, TooManyRequests | A |
| POST /auth/2fa/recovery | verifyRecovery | Authenticated, Stage preauth | CodeRequest | 200 Me | Unauthorized (invalid_code), Forbidden, TooManyRequests | A |
| POST /auth/2fa/webauthn/begin | beginWebAuthnLogin | Authenticated, Stage preauth | – | 200 WebAuthnOptions | Unauthorized, Conflict (no passkeys) | A |
| POST /auth/2fa/webauthn/finish | finishWebAuthnLogin | Authenticated, Stage preauth | WebAuthnAssertion | 200 Me | Unauthorized (invalid_code), TooManyRequests | A |
| POST /auth/sudo/totp | sudoTotp | Authenticated | CodeRequest | 200 SudoResponse | Unauthorized (invalid_code), TooManyRequests | A |
| POST /auth/sudo/webauthn/begin | beginSudoWebAuthn | Authenticated | – | 200 WebAuthnOptions | Conflict | A |
| POST /auth/sudo/webauthn/finish | finishSudoWebAuthn | Authenticated | WebAuthnAssertion | 200 SudoResponse | Unauthorized (invalid_code), TooManyRequests | A |
| PATCH /me | updateMe | Authenticated | UpdateMeRequest | 200 Me | BadRequest | A |
| POST /me/password | changePassword | Authenticated | PasswordChangeRequest | 204 | BadRequest (validation_failed), Unauthorized (invalid_credentials) | A |
| POST /me/totp/setup | setupTotp | Authenticated, Stages setup/full (Handler verlangt Sudo, wenn schon 2FA aktiv) | – | 200 TotpSetupResponse | Forbidden (sudo_required), Conflict (TOTP already enabled) | A |
| POST /me/totp/enable | enableTotp | Authenticated, Stages setup/full | CodeRequest | 200 RecoveryCodesResponse | Unauthorized (invalid_code), Conflict (no setup pending) | A |
| DELETE /me/totp | disableTotp | Authenticated + Sudo | – | 204 | Conflict (last 2FA method of admin / enforced) | A |
| POST /me/passkeys/begin | beginPasskeyRegistration | Authenticated, Stages setup/full (Sudo, wenn schon 2FA aktiv) | – | 200 WebAuthnOptions | Forbidden | A |
| POST /me/passkeys/finish | finishPasskeyRegistration | Authenticated, Stages setup/full | PasskeyRegistration | 201 PasskeyCreated | BadRequest | A |
| GET /me/passkeys | listPasskeys | Authenticated | – | 200 Passkey[] | – | A |
| DELETE /me/passkeys/{id} | deletePasskey | Authenticated + Sudo | – | 204 | NotFound, Conflict | A |
| POST /me/recovery-codes | regenerateRecoveryCodes | Authenticated + Sudo | – | 200 RecoveryCodesResponse | – | A |
| GET /me/sessions | listMySessions | Authenticated | – | 200 Session[] | – | A |
| DELETE /me/sessions/{id} | revokeMySession | Authenticated | – | 204 | NotFound | A |
| POST /me/sessions/revoke-others | revokeOtherSessions | Authenticated | – | 204 | – | A |
| GET /admin/sessions | listAllSessions | Admin | – | 200 Session[] | Forbidden | A |
| DELETE /admin/sessions/{id} | revokeAnySession | Admin + Sudo | – | 204 | NotFound | A |
| GET /users | listUsers | Admin | – | 200 User[] | Forbidden | D |
| POST /users | createUser | Admin + Sudo | CreateUserRequest | 201 User | BadRequest, Conflict (username taken) | D |
| GET /users/{id} | getUser | Admin | – | 200 User | NotFound | D |
| PATCH /users/{id} | updateUser | Admin + Sudo | UpdateUserRequest | 200 User | BadRequest, NotFound, Conflict (last admin) | D |
| DELETE /users/{id} | deleteUser | Admin + Sudo | – | 204 | NotFound, Conflict (last admin / self) | D |
| POST /users/{id}/reset-2fa | resetUser2fa | Admin + Sudo | – | 204 | NotFound | D |
| GET /users/{id}/permissions | getUserPermissions | Admin | – | 200 UserPermissions | NotFound | D |
| PUT /users/{id}/permissions | setUserPermissions | Admin + Sudo | UserPermissions | 200 UserPermissions | BadRequest, NotFound | D |
| GET /servers | listServers | Authenticated | – | 200 Server[] | – | D |
| POST /servers | createServer | Admin + Sudo | CreateServerRequest | 201 Server | BadRequest, Conflict | D |
| GET /servers/{id} | getServer | Server(server.view, id) | – | 200 Server | Forbidden, NotFound | D |
| DELETE /servers/{id} | deleteServer | Admin + Sudo | – | 204 | NotFound | D |
| POST /servers/{id}/power | serverPower | Server(server.power, id) | PowerRequest | 202 PowerResponse | BadRequest, Forbidden, NotFound | D |
| GET /audit | listAudit | Admin | Query: actor (string), action (string), result, from (int64), to (int64), Cursor, Limit | 200 AuditPage | – | D |
| GET /audit/verify | verifyAudit | Admin | – | 200 AuditVerify | – | D |
| GET /security/overview | getSecurityOverview | Admin | Query: range (24h\|7d\|30d, default 24h) | 200 SecurityOverview | – | B |
| GET /security/attempts | listLoginAttempts | Admin | Query: success (bool), ip (string), Cursor, Limit | 200 LoginAttemptPage | – | B |
| GET /security/jails | listJails | Admin | Query: active (bool, default true) | 200 Jail[] | – | B |
| POST /security/jails | createJail | Admin + Sudo | CreateJailRequest | 201 Jail | BadRequest, Conflict (allowlisted / own IP) | B |
| DELETE /security/jails/{id} | liftJail | Admin + Sudo | – | 204 | NotFound | B |
| GET /security/ip-rules | listIPRules | Admin | – | 200 IPRule[] | – | B |
| POST /security/ip-rules | createIPRule | Admin + Sudo | CreateIPRuleRequest | 201 IPRule | BadRequest, Conflict (duplicate / would lock out caller) | B |
| DELETE /security/ip-rules/{id} | deleteIPRule | Admin + Sudo | – | 204 | NotFound, Conflict (would lock out caller) | B |
| GET /security/settings | getSecuritySettings | Admin | – | 200 SecuritySettingsResponse | – | B |
| PUT /security/settings | updateSecuritySettings | Admin + Sudo | SecuritySettings | 200 SecuritySettingsResponse | BadRequest, Conflict (would lock out caller) | B |
| GET /security/notifiers | listNotifiers | Admin | – | 200 Notifier[] | – | B |
| POST /security/notifiers | createNotifier | Admin + Sudo | CreateNotifierRequest | 201 Notifier | BadRequest, Conflict | B |
| PATCH /security/notifiers/{id} | updateNotifier | Admin + Sudo | UpdateNotifierRequest | 200 Notifier | BadRequest, NotFound | B |
| DELETE /security/notifiers/{id} | deleteNotifier | Admin + Sudo | – | 204 | NotFound | B |
| POST /security/notifiers/{id}/test | testNotifier | Admin | – | 204 | BadRequest (validation_failed: delivery failed), NotFound | B |
| GET /security/agent | getAgentStatus | Admin | – | 200 AgentStatus | – | B |

Pfadparameter `{id}` immer über `#/components/parameters/IdPath`. Array-Antworten (`Passkey[]` usw.) als `type: array, items: $ref`.

- [ ] **Step 1: Datei schreiben** – Kopf + `components` exakt wie oben, dann alle Pfade aus der Tabelle gemäß Übersetzungsregeln. Beispiel für zwei Operationen (Muster für alle anderen):

```yaml
paths:
  /auth/login:
    post:
      operationId: login
      tags: [auth]
      security: []
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/LoginRequest' } } }
      responses:
        '200': { description: Password accepted; stage tells what comes next, content: { application/json: { schema: { $ref: '#/components/schemas/Me' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '401': { $ref: '#/components/responses/Unauthorized' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '429': { $ref: '#/components/responses/TooManyRequests' }
  /servers/{id}/power:
    post:
      operationId: serverPower
      tags: [servers]
      parameters:
        - $ref: '#/components/parameters/IdPath'
        - $ref: '#/components/parameters/CSRF'
      requestBody:
        required: true
        content: { application/json: { schema: { $ref: '#/components/schemas/PowerRequest' } } }
      responses:
        '202': { description: Accepted, content: { application/json: { schema: { $ref: '#/components/schemas/PowerResponse' } } } }
        '400': { $ref: '#/components/responses/BadRequest' }
        '403': { $ref: '#/components/responses/Forbidden' }
        '404': { $ref: '#/components/responses/NotFound' }
```
Zusätzlich: Jede Operation mit Session erhält `'401': { $ref: '#/components/responses/Unauthorized' }`; jede Sudo-Operation `'403'`. Tags: `auth`, `me`, `admin`, `users`, `servers`, `audit`, `security`, `health`.

- [ ] **Step 2: Validieren**

```bash
npx --yes @redocly/cli@latest lint api/openapi.yaml --extends=minimal
grep -c "operationId:" api/openapi.yaml
```
Expected: keine Fehler; `57` operationIds (Anzahl Tabellenzeilen).

- [ ] **Step 3: Commit** `git add api && git commit -m "feat(api): add OpenAPI contract for foundation API" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`

---

### Task 10: Web-Scaffold, Embed-Handler, `panel serve`, Makefile, Dev-Skript

**Files:**
- Create: `web/` (Vite-Scaffold), `internal/webui/embed.go`, `internal/webui/embed_test.go`, `internal/webui/dist/.gitkeep`, `cmd/panel/main.go`, `Makefile`, `scripts/dev.sh`

**Interfaces:**
- Produces: `webui.Handler() http.Handler`, `webui.NewHandler(fsys fs.FS) http.Handler`; Make-Targets `generate test test-go test-race test-web lint dev web-build build release clean`; npm-Skripte `dev build test lint typecheck gen:api`.

- [ ] **Step 1: Failing test `internal/webui/embed_test.go`**

```go
package webui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"mcpanel/internal/webui"
)

func get(h http.Handler, path string) (int, string, http.Header) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	b, _ := io.ReadAll(rec.Body)
	return rec.Code, string(b), rec.Header()
}

func TestSPAFallbackAndAssets(t *testing.T) {
	h := webui.NewHandler(fstest.MapFS{
		"index.html":     {Data: []byte("<html>app</html>")},
		"assets/app.js":  {Data: []byte("console.log(1)")},
	})
	if code, body, _ := get(h, "/"); code != 200 || !strings.Contains(body, "app") {
		t.Fatalf("/ → %d %q", code, body)
	}
	if code, body, _ := get(h, "/security/jails"); code != 200 || !strings.Contains(body, "app") {
		t.Fatalf("SPA route → %d %q", code, body)
	}
	code, _, hdr := get(h, "/assets/app.js")
	if code != 200 || !strings.Contains(hdr.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset → %d cache=%q", code, hdr.Get("Cache-Control"))
	}
	if code, _, _ := get(h, "/api/unknown"); code != 404 {
		t.Fatalf("/api/unknown → %d, want 404", code)
	}
	if code, _, _ := get(h, "/../../etc/passwd"); code != 200 {
		// path is cleaned to /etc/passwd → not found → SPA fallback (never a file outside the FS)
		t.Fatalf("traversal → %d", code)
	}
}

func TestNotBuilt(t *testing.T) {
	h := webui.NewHandler(fstest.MapFS{".gitkeep": {}})
	if code, _, _ := get(h, "/"); code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", code)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/webui/` → FAIL.

- [ ] **Step 3: `internal/webui/embed.go` und Platzhalter**

```bash
mkdir -p internal/webui/dist && touch internal/webui/dist/.gitkeep
```

```go
// Package webui serves the embedded, pre-built React frontend.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"mcpanel/internal/httpx"
)

//go:embed all:dist
var dist embed.FS

func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return NewHandler(sub)
}

// NewHandler serves files from fsys with SPA fallback to index.html.
func NewHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "no such endpoint")
			return
		}
		if _, err := fs.Stat(fsys, "index.html"); err != nil {
			http.Error(w, "frontend not built - run `make web-build`", http.StatusServiceUnavailable)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "index.html" {
			serveIndex(w, r, files)
			return
		}
		if info, err := fs.Stat(fsys, name); err != nil || info.IsDir() {
			serveIndex(w, r, files)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, files http.Handler) {
	w.Header().Set("Cache-Control", "no-cache")
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/"
	files.ServeHTTP(w, r2)
}
```

- [ ] **Step 4: Run** `go test ./internal/webui/` → PASS.

- [ ] **Step 5: `cmd/panel/main.go` (Phase-0-Skelett; Phase 2 ersetzt `newRouter`)**

```go
// Command panel is the mc-panel web application.
package main

import (
	"context"
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

	"github.com/go-chi/chi/v5"

	"mcpanel/internal/clock"
	"mcpanel/internal/config"
	"mcpanel/internal/httpx"
	"mcpanel/internal/rbac"
	"mcpanel/internal/secretbox"
	"mcpanel/internal/store"
	"mcpanel/internal/webui"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: panel serve [-config path] [-dev]")
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "serve":
		if err := serve(args[1:]); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	default:
		usage(stderr)
		return 2
	}
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to panel.toml")
	dev := fs.Bool("dev", false, "development mode (paths under ./.dev)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.LoadPanel(*cfgPath, *dev, root)
	if err != nil {
		return err
	}
	setupLogging(cfg.Dev)
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := store.Migrate(ctx, db); err != nil {
		return err
	}
	if _, err := secretbox.LoadOrCreateKey(cfg.SecretKeyPath()); err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           newRouter(cfg),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("panel listening", "addr", cfg.Listen, "dev", cfg.Dev)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func setupLogging(dev bool) {
	var h slog.Handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	if dev {
		h = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	}
	slog.SetDefault(slog.New(h))
}

type noChecker struct{}

func (noChecker) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, nil
}

// newRouter is replaced in Phase 2 by the full wiring.
func newRouter(cfg config.Panel) http.Handler {
	mux := chi.NewRouter()
	mux.Use(httpx.RequestID, httpx.RealIP(cfg.TrustedPrefixes()), httpx.SecurityHeaders, httpx.BodyLimit(1<<20))
	rt := rbac.NewRouter(mux, noChecker{}, clock.Real{})
	rt.Route("/api", func(api *rbac.Router) {
		api.Get("/health", rbac.Public(), func(w http.ResponseWriter, r *http.Request) {
			httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
	})
	mux.Handle("/*", webui.Handler())
	return mux
}
```

Hinweis: `config.LoadPanel` gibt `Panel` per Wert zurück, nachdem `Validate` (Pointer-Receiver) auf der lokalen Variable lief – `trusted` ist also befüllt. Falls `TrustedPrefixes()` leer ist, in `LoadPanel` sicherstellen, dass `p.Validate()` auf `&p` aufgerufen wird (ist der Fall, da `p` eine Variable ist).

- [ ] **Step 6: Vite-Scaffold**

```bash
cd /Users/jannes/Workspace/mc-panel
npm create vite@latest web-tmp -- --template react-ts
rsync -a web-tmp/ web/ && rm -rf web-tmp
cd web
npm install
npm install @tanstack/react-router @tanstack/react-query openapi-fetch i18next react-i18next qrcode @simplewebauthn/browser
npm install -D @tanstack/router-plugin openapi-typescript msw vitest jsdom @testing-library/react @testing-library/jest-dom @testing-library/user-event @playwright/test tailwindcss @tailwindcss/vite @types/qrcode @types/node
```

- [ ] **Step 7: `web/vite.config.ts`**

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [tanstackRouter({ target: 'react', autoCodeSplitting: true }), react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(__dirname, './src') } },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false } },
  },
  build: { outDir: '../internal/webui/dist', emptyOutDir: true, sourcemap: false },
  test: { environment: 'jsdom', globals: true, setupFiles: ['./src/test/setup.ts'], exclude: ['e2e/**', 'node_modules/**'] },
})
```
Am Anfang der Datei `/// <reference types="vitest/config" />` ergänzen, damit `test` typisiert ist.

- [ ] **Step 8: Basis-App, Tailwind, Router, Test**

`web/src/index.css`:
```css
@import "tailwindcss";
```

`web/src/routes/__root.tsx`:
```tsx
import { Outlet, createRootRoute } from '@tanstack/react-router'

export const Route = createRootRoute({ component: () => <Outlet /> })
```

`web/src/routes/index.tsx`:
```tsx
import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/')({ component: Home })

export function Home() {
  return <main className="p-6 text-2xl font-semibold">mc-panel</main>
}
```

`web/src/main.tsx`:
```tsx
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import { routeTree } from './routeTree.gen'
import './index.css'

const queryClient = new QueryClient()
const router = createRouter({ routeTree, context: {} })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
```

`web/src/test/setup.ts`:
```ts
import '@testing-library/jest-dom/vitest'
```

`web/src/routes/index.test.tsx`:
```tsx
import { render, screen } from '@testing-library/react'
import { Home } from './index'

test('renders app name', () => {
  render(<Home />)
  expect(screen.getByText('mc-panel')).toBeInTheDocument()
})
```

Löschen: `web/src/App.tsx`, `web/src/App.css`, `web/src/assets/` (Vite-Demo).

- [ ] **Step 9: npm-Skripte in `web/package.json`** (Abschnitt `scripts` ersetzen)

```json
"scripts": {
  "dev": "vite",
  "build": "tsc -b && vite build",
  "test": "vitest run",
  "lint": "eslint .",
  "typecheck": "tsc -b --noEmit",
  "gen:api": "openapi-typescript ../api/openapi.yaml -o src/api/schema.d.ts"
}
```

```bash
cd web && npm run gen:api && npm test && npm run build && ls ../internal/webui/dist && touch ../internal/webui/dist/.gitkeep
```
Expected: Test PASS; `index.html` und `assets/` in `internal/webui/dist`.

- [ ] **Step 10: shadcn initialisieren**

```bash
cd web && npx shadcn@latest init --base-color neutral --yes
npx shadcn@latest add button input label card dialog table tabs badge dropdown-menu sonner form select switch separator --yes
npm run typecheck
```
Expected: `components.json` und `src/components/ui/*` angelegt; typecheck grün. Falls `init` nach dem Import-Alias fragt: `@/` (passt zu `vite.config.ts`; zusätzlich in `tsconfig.app.json` `"baseUrl": "."` und `"paths": {"@/*": ["./src/*"]}` setzen).

- [ ] **Step 11: `Makefile`**

```make
SHELL := /bin/bash
GO ?= go
export CGO_ENABLED ?= 0

.PHONY: generate test test-go test-race test-web lint dev web-build build release clean

generate:
	sqlc generate
	cd web && npm run gen:api

test: test-go test-web

test-go:
	$(GO) test ./...

test-race:
	CGO_ENABLED=1 $(GO) test -race ./...

test-web:
	cd web && npm test

lint:
	golangci-lint run ./...
	cd web && npm run lint && npm run typecheck

dev:
	./scripts/dev.sh

web-build:
	cd web && npm run build
	touch internal/webui/dist/.gitkeep

build: web-build
	mkdir -p bin
	$(GO) build -trimpath -o bin/ ./cmd/...

release:
	goreleaser release --clean --snapshot

clean:
	rm -rf bin dist .dev
	find internal/webui/dist -mindepth 1 ! -name .gitkeep -delete
```

- [ ] **Step 12: `scripts/dev.sh`** (`chmod +x`)

```bash
#!/usr/bin/env bash
# Starts agent (if present), panel and the Vite dev server for local development.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p .dev/bin

go build -o .dev/bin/ ./cmd/...

pids=()
cleanup() { for p in ${pids[@]+"${pids[@]}"}; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT INT TERM

if [ -x .dev/bin/agent ]; then
  .dev/bin/agent -dev & pids+=($!)
fi
.dev/bin/panel serve -dev & pids+=($!)
(cd web && npm run dev) & pids+=($!)

echo "mc-panel dev: http://localhost:5173"
wait
```

- [ ] **Step 13: Ende-zu-Ende prüfen**

```bash
make test
make build && ls bin/
./bin/panel serve -dev & sleep 2
curl -s http://127.0.0.1:8080/api/health
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8080/
curl -sI http://127.0.0.1:8080/api/health | grep -i content-security-policy
kill %1
```
Expected: Tests grün; `bin/panel`; `{"status":"ok"}`; `200` (eingebettetes Frontend); CSP-Header vorhanden.

- [ ] **Step 14: Commit + Tag**

```bash
git add -A
git commit -m "feat: add web scaffold, embedded UI handler, panel serve skeleton and dev tooling" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: `password` – argon2id und Passwort-Policy

Wird von Stream A (Login, Passwortänderung) **und** Stream D (Benutzer anlegen, `admin create`) gebraucht – deshalb in Phase 0.

**Files:** Create `internal/password/password.go`, `internal/password/common.txt`, `scripts/gen-common-passwords.sh`; Test `internal/password/password_test.go`

**Interfaces:** Produces:
```go
var ErrTooShort, ErrTooLong, ErrCommon, ErrContainsUsername, ErrMalformedHash error
func Validate(pw, username string) error                      // 12–256 runes, not common, must not contain username (case-insensitive, if len(username) >= 3)
func Hash(pw string) (string, error)                          // PHC: $argon2id$v=19$m=65536,t=3,p=2$<salt b64>$<key b64>
func Verify(pw, encoded string) (ok, needsRehash bool, err error)  // constant-time; ErrMalformedHash for unparsable (e.g. "!")
func DummyVerify(pw string)                                   // same cost as Verify, for unknown users
```

- [ ] **Step 1: Liste häufiger Passwörter erzeugen** – Quelle: SecLists `Passwords/Common-Credentials/xato-net-10-million-passwords-100000.txt`. **Vor dem Download den Nutzer um Erlaubnis fragen** (Datei ~800 KB von raw.githubusercontent.com). Da die Mindestlänge 12 ist, werden nur Einträge mit ≥ 12 Zeichen behalten.

`scripts/gen-common-passwords.sh`:
```bash
#!/usr/bin/env bash
# Regenerates internal/password/common.txt from SecLists (entries >= 12 chars, lowercased, unique).
set -euo pipefail
cd "$(dirname "$0")/.."
url="https://raw.githubusercontent.com/danielmiessler/SecLists/master/Passwords/Common-Credentials/xato-net-10-million-passwords-100000.txt"
tmp="$(mktemp -d)"
curl -fsSL "$url" -o "$tmp/list.txt"
awk 'length($0) >= 12 { print tolower($0) }' "$tmp/list.txt" | LC_ALL=C sort -u > internal/password/common.txt
rm -rf "$tmp"
wc -l internal/password/common.txt
```
```bash
chmod +x scripts/gen-common-passwords.sh && ./scripts/gen-common-passwords.sh
grep -c . internal/password/common.txt
printf 'passwordpassword\n' >> internal/password/common.txt && LC_ALL=C sort -u -o internal/password/common.txt internal/password/common.txt
```
Expected: einige hundert bis wenige tausend Zeilen; `passwordpassword` ist garantiert enthalten (für den Test).

- [ ] **Step 2: Failing test**

```go
package password_test

import (
	"errors"
	"strings"
	"testing"

	"mcpanel/internal/password"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		pw, user string
		want     error
	}{
		{"correct horse battery", "jannes", nil},
		{"short", "jannes", password.ErrTooShort},
		{strings.Repeat("ä", 11), "jannes", password.ErrTooShort},
		{strings.Repeat("ä", 12), "jannes", nil},
		{strings.Repeat("a", 257), "jannes", password.ErrTooLong},
		{"PasswordPassword", "jannes", password.ErrCommon},
		{"my-JANNES-password", "jannes", password.ErrContainsUsername},
	}
	for _, tc := range cases {
		if err := password.Validate(tc.pw, tc.user); !errors.Is(err, tc.want) {
			t.Errorf("Validate(%q) = %v, want %v", tc.pw, err, tc.want)
		}
	}
}

func TestHashVerify(t *testing.T) {
	h, err := password.Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected encoding %q", h)
	}
	h2, _ := password.Hash("correct horse battery")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
	ok, rehash, err := password.Verify("correct horse battery", h)
	if !ok || rehash || err != nil {
		t.Fatalf("Verify = %v %v %v", ok, rehash, err)
	}
	if ok, _, _ := password.Verify("wrong horse battery", h); ok {
		t.Fatal("wrong password accepted")
	}
}

func TestVerifyOldParamsNeedsRehash(t *testing.T) {
	// m=19456,t=2,p=1 (older params), password "correct horse battery"
	old := password.HashWithParams("correct horse battery", 19456, 2, 1)
	ok, rehash, err := password.Verify("correct horse battery", old)
	if !ok || !rehash || err != nil {
		t.Fatalf("Verify = %v %v %v", ok, rehash, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, h := range []string{"!", "", "$argon2id$v=19$m=x$a$b", "$bcrypt$..."} {
		if ok, _, err := password.Verify("x", h); ok || !errors.Is(err, password.ErrMalformedHash) {
			t.Errorf("Verify(%q) = %v, %v", h, ok, err)
		}
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/password/` → FAIL.

- [ ] **Step 4: Implementation**

```go
// Package password hashes and validates user passwords (argon2id, PHC string format).
package password

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	MinLength = 12
	MaxLength = 256

	memory  uint32 = 64 * 1024
	iterations   uint32 = 3
	threads uint8  = 2
	saltLen        = 16
	keyLen  uint32 = 32
)

var (
	ErrTooShort         = errors.New("password: too short")
	ErrTooLong          = errors.New("password: too long")
	ErrCommon           = errors.New("password: too common")
	ErrContainsUsername = errors.New("password: must not contain the username")
	ErrMalformedHash    = errors.New("password: malformed hash")
)

//go:embed common.txt
var commonRaw []byte

var common = sync.OnceValue(func() map[string]struct{} {
	m := make(map[string]struct{}, 4096)
	sc := bufio.NewScanner(bytes.NewReader(commonRaw))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			m[line] = struct{}{}
		}
	}
	return m
})

func Validate(pw, username string) error {
	n := utf8.RuneCountInString(pw)
	if n < MinLength {
		return ErrTooShort
	}
	if n > MaxLength {
		return ErrTooLong
	}
	lower := strings.ToLower(pw)
	if _, ok := common()[lower]; ok {
		return ErrCommon
	}
	if u := strings.ToLower(username); len(u) >= 3 && strings.Contains(lower, u) {
		return ErrContainsUsername
	}
	return nil
}

func Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return encode(pw, salt, memory, iterations, threads), nil
}

// HashWithParams exists for tests and migrations of old parameters.
func HashWithParams(pw string, m, t uint32, p uint8) string {
	salt := make([]byte, saltLen)
	_, _ = rand.Read(salt)
	return encode(pw, salt, m, t, p)
}

func encode(pw string, salt []byte, m, t uint32, p uint8) string {
	key := argon2.IDKey([]byte(pw), salt, t, m, p, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, m, t, p, b64.EncodeToString(salt), b64.EncodeToString(key))
}

func Verify(pw, encoded string) (ok, needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, false, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, false, ErrMalformedHash
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || m == 0 || t == 0 || p == 0 {
		return false, false, ErrMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false, false, ErrMalformedHash
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	ok = subtle.ConstantTimeCompare(got, want) == 1
	needsRehash = ok && (m != memory || t != iterations || p != threads || uint32(len(want)) != keyLen)
	return ok, needsRehash, nil
}

var dummyHash = sync.OnceValue(func() string {
	h, _ := Hash("dummy password for timing equalisation")
	return h
})

// DummyVerify burns the same CPU/memory as a real Verify (used for unknown usernames).
func DummyVerify(pw string) { _, _, _ = Verify(pw, dummyHash()) }
```

- [ ] **Step 5: Run** `go test ./internal/password/` → PASS.

- [ ] **Step 6: Commit + Tag**

```bash
git add internal/password scripts/gen-common-passwords.sh
git commit -m "feat(password): add argon2id hashing and password policy" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git tag phase0
```

---

## Wiring-Vertrag Phase 0 → Phase 2

Phase 2 ersetzt `newRouter` in `cmd/panel/main.go`. Middleware-Reihenfolge (verbindlich):

1. `httpx.RequestID`
2. `httpx.RealIP(cfg.TrustedPrefixes())`
3. `httpx.SecurityHeaders`
4. `httpx.BodyLimit(1 << 20)`
5. Stream B: IP-Filter (Jail/Deny/Land) – vor allem anderen, auch vor statischen Dateien
6. Stream A: Session-Loader (setzt `principal`), dann CSRF-Prüfung
7. Stream B: API-Rate-Limit pro Session
8. `rbac.Router` mit Stream-D-Checker; `rt.Route("/api", …)` → `authH.Routes(api)`, `defenseH.Routes(api)`, `usersH.Routes(api)`, `auditH.Routes(api)`
9. `mux.Handle("/*", webui.Handler())`
