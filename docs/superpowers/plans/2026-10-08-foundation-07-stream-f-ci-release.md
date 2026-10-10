# Stream F – CI, Lint & Release – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lint-Konfiguration (golangci-lint v2), GitHub-Actions-CI, reproduzierbares Release mit GoReleaser (linux/amd64 + linux/arm64, eingebettetes Frontend, SHA256-Checksums) sowie eine Lima-Linux-VM mit Skripten für produktionsnahe Tests (nftables, systemd, Abnahmekriterium 5) bereitstellen – ohne Anwendungscode zu schreiben.

**Architecture:** `.golangci.yml` ist die einzige Lint-Wahrheit für lokal (`make lint`) und CI. `ci.yml` spiegelt die Make-Targets aus Phase 0 in getrennten Jobs (`go`, `web`, `openapi`, `nftables`, `build`); `release.yml` baut auf Tags `v*` mit GoReleaser, wobei nur dieser eine Job `contents: write` erhält und Node-Code nie den Token sieht. GoReleaser prüft per Build-Hook jedes Binary (kein CGO, Frontend eingebettet). `scripts/vm-setup.sh` installiert genau die Snapshot-Archive aus `dist/` in einer Lima-VM und startet agent (root) + panel (User `mcpanel`) als transiente systemd-Units; `scripts/vm-test.sh` erzeugt Fehl-Logins aus einem eigenen Netz-Namespace (`192.0.2.2`) und prüft das nftables-Set.

**Tech Stack:** golangci-lint v2.14.0 · GitHub Actions (`actions/checkout` v7.0.1, `actions/setup-go` v7.0.0, `actions/setup-node` v7.0.0, `golangci/golangci-lint-action` v9.3.0, `goreleaser/goreleaser-action` v7.2.3 – alle per Commit-SHA gepinnt) · GoReleaser v2.18.2 · govulncheck v1.8.0 · actionlint v1.7.12 · Redocly CLI 2.54.2 · sqlc (Version aus dem generierten Header) · Lima (`template:ubuntu-lts`), optional OrbStack · Bash, nftables, systemd-run.

**Spec:** `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md` (insb. §3.5, §10, §12 Kriterien 1, 5, 7)

## Global Constraints

Gelten zusätzlich zu allen Global Constraints aus `2026-10-08-foundation-00-overview.md`:

- **Eigene Pfade (nur diese anlegen/ändern):** `.github/**`, `.golangci.yml`, `.goreleaser.yaml`, `docs/dev/**`, `scripts/vm-setup.sh`, `scripts/vm-test.sh`. Kein Anwendungscode, nichts unter `web/`, `cmd/`, `internal/`.
- **Phase-0-Dateien sind schreibgeschützt** (`Makefile`, `go.mod`, `.gitignore`, `scripts/dev.sh`, …). Einzige Ausnahme: reine Whitespace-Formatierung durch `gofmt` in Task 1 Step 5 (nachweisbar per `git diff -w --exit-code`).
- Arbeitsort: eigener Worktree auf Branch `stream/f`, abgezweigt vom Tag `phase0`.
- Build-Invarianten: Modul `github.com/Bernotese/mc-panel`, `CGO_ENABLED=0` für alle Release-Builds, Ziele nur `linux/amd64` und `linux/arm64`, Frontend liegt nach `make web-build` in `internal/webui/dist/` (mit `.gitkeep`), `web/go.mod` hält `web/` aus `./...` heraus.
- Version wird ausschließlich per `-ldflags "-X main.version={{ .Version }}"` gesetzt (Tag `v1.2.3` → `1.2.3`).
- GitHub Actions: jede `uses:`-Referenz ist auf einen vollständigen Commit-SHA gepinnt, mit Versionskommentar (`# v7.0.1`). Die SHAs unten wurden am 2026-10-08 per `git ls-remote` aufgelöst.
- Werkzeugversionen sind gepinnt (siehe Tech Stack). Lokal werden versionssensible Werkzeuge (golangci-lint, actionlint, govulncheck) mit derselben Version wie in CI aufgerufen.
- Workflow-Rechte: Top-Level `permissions: contents: read` (CI) bzw. `permissions: {}` (Release); `contents: write` nur im Release-Job. Kein `pull_request_target`, keine `${{ github.event.* }}`-Interpolation in `run:`-Skripten, `persist-credentials: false` bei jedem Checkout.
- Integrationstests für nftables: Build-Tag `nftintegration`, nur Linux, laufen als root (`go test -exec sudo`).
- Prosa auf Deutsch, Konfiguration/Code auf Englisch. Commits: Conventional Commits + `-m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`.
- Downloads außerhalb der normalen Toolchain (Lima-Ubuntu-Image, ca. 600 MB) erst nach Rückfrage beim Nutzer.

## Review Focus

1. **Release ohne eingebettetes Frontend.** Wird `make web-build` übersprungen, kompiliert `cmd/panel` trotzdem (nur `.gitkeep` in `dist/`) und liefert im Betrieb 503. Ein naiver Check `strings panel | grep index.html` ist wertlos, weil `net/http` das Literal `index.html` selbst enthält. → Task 3: GoReleaser-Post-Hook bricht ab, wenn `/assets/index-` (Vite-Entry, existiert nur im echten Build) fehlt; Negativtest beweist, dass der Check zwischen „mit“ und „ohne“ UI unterscheidet. Task 4: `build`-Job prüft `bin/panel` und alle Snapshot-Binaries. Task 5: Release baut das Web vor GoReleaser.
2. **Lint-Konfiguration lehnt idiomatischen Code anderer Streams ab.** Streams mergen nach F und müssen `make lint` bestehen; zu strenge oder falsch gezielte Regeln (z. B. `revive:exported`, `unused-parameter`, `misspell` mit US-Locale gegen „honoured“, gosec G302 gegen das spezifizierte Socket-Mode `0660`, noctx gegen `net.Listen` im agent) blockieren Merges. → Task 1: Lauf gegen den gesamten Phase-0-Code, Task 2: Probe-Pakete mit typischen Mustern aus Streams A–D (in deren Paketpfaden) müssen `0 issues.` ergeben.
3. **Secrets-/Token-Exposition in CI.** PR-Workflows dürfen keinen schreibenden Token und keine Secrets sehen; im Release darf Drittcode (npm-Build-Skripte, Vite-Plugins) den `GITHUB_TOKEN` mit `contents: write` nicht erreichen; Release-Builds dürfen keine (potenziell vergifteten) Caches wiederherstellen. → Task 4/5: `contents: read` global, Release-Job als einziger mit `contents: write`, `goreleaser release --skip=before` nach separatem tokenlosen `make web-build`, `cache: false`; Verifikation mit actionlint plus `grep`-Prüfungen in Task 5 Step 4.
4. **Falsche Architektur oder versehentliches CGO.** Ein `amd64`-Binary im `arm64`-Archiv oder ein dynamisch gelinktes Binary startet auf dem VPS nicht. → Task 3: `file dist/*/panel dist/*/agent` muss `x86-64` bzw. `ARM aarch64` und `statically linked` zeigen, `go version -m` muss `CGO_ENABLED=0` und `-X main.version=` enthalten, Archive enthalten exakt `agent panel`, `shasum -a 256 -c checksums.txt` grün. Task 4 wiederholt das in CI auf Basis von `dist/artifacts.json`.
5. **Drift zwischen VM-Test und Realität.** Testet die VM andere Binaries als das Release, andere Set-Namen als der agent oder einen Zustand aus einem vorherigen Lauf (bestehender Jail), ist das Ergebnis wertlos; ebenso driftet sqlc-generierter Code, wenn CI eine andere sqlc-Version nutzt als der Entwickler. → Task 6: `vm-setup.sh` installiert genau ein Archiv `dist/mc-panel_*_linux_<arch>.tar.gz` (Abbruch bei 0 oder >1), setzt DB und Jail-Sets bei jedem Lauf zurück und verwendet die Spec-Namen `inet mcpanel`/`mcpanel_jail4`; Phase-2-Schritt im Wiring-Vertrag prüft per `grep`, dass `internal/agent` dieselben Namen nutzt. Task 4: CI installiert sqlc in exakt der Version aus dem Header von `internal/store/db.go`.

## Dateistruktur

```
.golangci.yml                    golangci-lint v2: Linter, Formatter, gezielte Ausnahmen
.goreleaser.yaml                 GoReleaser v2: panel + agent, linux/amd64+arm64, tar.gz, checksums.txt
.github/workflows/ci.yml         Jobs go, web, openapi, nftables, build (Push/PR/wöchentlich)
.github/workflows/release.yml    Tag v* → Tests → Web-Build → GoReleaser (einziger Job mit contents: write)
.github/dependabot.yml           Wöchentliche Updates der gepinnten Action-SHAs
scripts/vm-setup.sh              Lima/OrbStack-VM anlegen, provisionieren, Snapshot installieren, agent+panel starten
scripts/vm-test.sh               Abnahmekriterium 5 in der VM (5 Fehl-Logins → IP im nft-Set)
docs/dev/README.md               Toolchain, Make-Targets, CI-Abbildung lokal, Lint-Konventionen, Release-Ablauf
docs/dev/linux-vm.md             VM-Anleitung, Topologie, Fehlersuche
```

Temporär (nie committen): `internal/lintprobe/`, `internal/agent/lintprobe/`, `internal/notify/lintprobe/` (Task 2), `cmd/agent/main.go`-Stub (Task 3, nur falls Stream C noch nicht gemergt ist), `.dev/tools/` (gitignored über `/.dev/`).

**Interfaces (Consumes):** Make-Targets `test-go test-race web-build build release clean` (Phase 0), npm-Skripte `lint typecheck test build gen:api` (Phase 0/E), `internal/webui/dist/` (Phase 0), `cmd/panel` mit `serve -config <path>` (Phase 0), `cmd/agent` mit `-config <path>` (Stream C), Config-Schlüssel aus `internal/config` (Phase 0), Build-Tag `nftintegration` (Stream C), nftables-Namen `inet mcpanel`, `mcpanel_jail4`, `mcpanel_jail6` (Spec §3.3).

**Interfaces (Produces):** siehe Abschnitt „Wiring-Vertrag“.

---

### Task 1: `.golangci.yml` und Lauf gegen den Phase-0-Code

**Files:**
- Create: `.golangci.yml`

- [ ] **Step 1: Ausgangszustand prüfen und Werkzeuge installieren**

```bash
cd "$(git rev-parse --show-toplevel)"
git describe --tags --always          # erwartet: phase0 (oder phase0-<n>-g<sha>)
git branch --show-current             # erwartet: stream/f
brew install jq shellcheck
mkdir -p .dev/tools
GOBIN="$PWD/.dev/tools" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
.dev/tools/golangci-lint version
make test
```
Expected: `golangci-lint has version 2.14.0 …`; `make test` grün. (`.dev/` ist per Phase-0-`.gitignore` ausgeschlossen.)

- [ ] **Step 2: `.golangci.yml` schreiben**

```yaml
# golangci-lint v2 configuration for mc-panel (spec §10).
# CI pins golangci-lint v2.14.0 (.github/workflows/ci.yml); run the same version locally:
#   GOBIN="$PWD/.dev/tools" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
# Validate this file with: golangci-lint config verify
version: "2"

run:
  timeout: 5m
  tests: true
  # Lint the Linux-only nftables integration tests too (they only compile with this tag).
  build-tags:
    - nftintegration
  modules-download-mode: readonly

linters:
  default: none
  enable:
    - bodyclose
    - errcheck
    - errorlint
    - gosec
    - govet
    - ineffassign
    - misspell
    - nilerr
    - noctx
    - revive
    - rowserrcheck
    - sqlclosecheck
    - staticcheck
    - unused

  settings:
    errorlint:
      errorf: true
      asserts: true
      comparison: true

    gosec:
      excludes:
        # Unchecked errors are errcheck's job (with the std-error-handling preset below).
        - G104
        # Integer-conversion overflow: fires on every uint32(len(x)) / int(uid) conversion;
        # bounds are checked explicitly where inputs are untrusted and in code review.
        - G115
        # "Secret" field names in API DTOs (password, csrf_token, TOTP secret, recovery codes)
        # are part of the OpenAPI contract and intentionally (un)marshalled.
        - G117

    # No locale on purpose: Phase-0 comments use British spellings ("honoured", "equalisation").
    misspell: {}

    revive:
      # Explicit rule list = golint defaults minus doc-comment and unused-parameter noise,
      # plus a few bug-finding rules. Doc comments are a review concern, not a merge blocker.
      rules:
        - name: blank-imports
        - name: context-as-argument
        - name: context-keys-type
        - name: dot-imports
        - name: error-naming
        - name: error-return
        - name: error-strings
        - name: errorf
        - name: increment-decrement
        - name: indent-error-flow
        - name: range
        - name: receiver-naming
        - name: redefines-builtin-id
        - name: superfluous-else
        - name: time-naming
        - name: unexported-return
        - name: unreachable-code
        - name: var-declaration
        - name: var-naming
        - name: atomic
        - name: bool-literal-in-expr
        - name: constant-logical-expr
        - name: datarace
        - name: identical-branches
        - name: unconditional-recursion
        - name: waitgroup-by-value

    staticcheck:
      # golangci-lint defaults plus: QF1001 (De Morgan rewrite) and QF1008 (embedded field
      # in selector) are style suggestions that conflict with readable Phase-0 code.
      checks:
        - all
        - -ST1000
        - -ST1003
        - -ST1016
        - -ST1020
        - -ST1021
        - -ST1022
        - -QF1001
        - -QF1008

  exclusions:
    # sqlc writes "// Code generated by sqlc. DO NOT EDIT." – matched by strict mode.
    generated: strict
    warn-unused: false
    presets:
      - std-error-handling
      - common-false-positives
    paths:
      - internal/store/db\.go$
      - internal/store/models\.go$
      - internal/store/[^/]+\.sql\.go$
    rules:
      # Tests may use fixed credentials, math/rand, loose file modes and paths from t.TempDir().
      - path: _test\.go
        linters:
          - gosec
        text: "G101|G301|G302|G304|G306|G404"
      # Tests talk to httptest servers; contexts and body closing are not production concerns there.
      - path: _test\.go
        linters:
          - noctx
          - bodyclose
      # Phase 0: store.Open pings once at startup without a request context.
      - path: internal/store/open\.go
        linters:
          - noctx
        text: Ping
      # Spec §3: the agent socket is 0660 with group mcpanel so the panel user can connect.
      - linters:
          - gosec
        text: G302
        source: "0o?660"
      # The agent daemon listens on its Unix socket for the whole process lifetime.
      - path: (cmd|internal)/agent/
        linters:
          - noctx
        text: net\.Listen

formatters:
  enable:
    - gofmt
    - goimports
  settings:
    gofmt:
      simplify: true
    goimports:
      local-prefixes:
        - mcpanel
  exclusions:
    generated: strict
    paths:
      - internal/store/db\.go$
      - internal/store/models\.go$
      - internal/store/[^/]+\.sql\.go$

issues:
  max-issues-per-linter: 0
  max-same-issues: 0
```

- [ ] **Step 3: Konfiguration validieren**

```bash
.dev/tools/golangci-lint config verify && echo CONFIG-OK
.dev/tools/golangci-lint linters | sed -n '/^Enabled by your configuration linters:/,/^$/p'
```
Expected: `CONFIG-OK` (keine Schemafehler); die Liste „Enabled by your configuration linters“ enthält genau die 14 Linter aus `linters.enable`.

- [ ] **Step 4: Gegen Phase-0-Code laufen lassen (macOS und Linux-Dateien)**

```bash
.dev/tools/golangci-lint run ./...
GOOS=linux .dev/tools/golangci-lint run ./...
.dev/tools/golangci-lint fmt --diff ./...
```
Expected: beide `run`-Aufrufe enden mit `0 issues.`; `fmt --diff` gibt nichts aus.

Wenn Befunde auftreten, gilt folgende verbindliche Reaktion (Code von Phase 0 wird **nicht** umgeschrieben):

| Befund | Ursache | Reaktion |
|---|---|---|
| `gofmt`/`goimports` in Phase-0-Dateien (z. B. Konstantenblock in `internal/password/password.go`, Map-Literale in `internal/httpx/httpx_test.go`) | Plan-Snippets nicht ausgerichtet | Step 5 (Whitespace-Commit) |
| `gosec` `G407` in `internal/secretbox/secretbox.go` | Nonce wird per `crypto/rand` gefüllt, Fehlalarm | Regel ergänzen: `- path: internal/secretbox/` / `linters: [gosec]` / `text: G407` mit Kommentar `# nonce comes from crypto/rand` |
| `gosec` `G118` in `cmd/panel/main.go` | Shutdown nutzt bewusst `context.Background()`, weil `ctx` bereits beendet ist | Regel: `- path: cmd/panel/main\.go` / `linters: [gosec]` / `text: G118` |
| `gosec` `G706` (Log-Injection) auf `slog`-Aufrufen | `slog`-Handler quoten/escapen Attributwerte | `G706` in `linters.settings.gosec.excludes` aufnehmen, Kommentar `# slog handlers escape attribute values` |
| `staticcheck` `SA1019` auf `goose.DialectSQLite3` | Deprecation in goose | Regel: `- path: internal/store/open\.go` / `linters: [staticcheck]` / `text: SA1019` |
| `revive` `blank-imports` auf `_ "embed"` | ältere revive-Logik | Regel: `- linters: [revive]` / `text: blank-imports` / `source: '"embed"'` |
| jeder andere Befund in idiomatischem Code | – | gezielte Regel (immer `path` **und** `linters` **und** `text`), mit Begründungskommentar; nie einen Linter komplett abschalten |
| echter Fehler im Phase-0-Code | – | nicht selbst beheben; Datei:Zeile + Linter-Meldung an den Lead melden und auf Entscheidung warten |

Nach jeder Config-Änderung Step 3 und Step 4 wiederholen, bis alle drei Befehle sauber sind.

- [ ] **Step 5: Nur falls `fmt --diff` Ausgaben hatte – Whitespace-Formatierung anwenden**

```bash
.dev/tools/golangci-lint fmt ./...
git diff --stat
git diff -w --exit-code && echo WHITESPACE-ONLY
.dev/tools/golangci-lint fmt --diff ./...
git add -u
git commit -m "style: apply gofmt to phase-0 files" -m "Whitespace-only (verified with git diff -w); required for the formatter checks in .golangci.yml." -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
Expected: `WHITESPACE-ONLY`; zweiter `fmt --diff` ohne Ausgabe. Meldet `git diff -w` Unterschiede (z. B. Import-Umsortierung), **nicht** committen: `git checkout -- .` und den Befund an den Lead melden.

- [ ] **Step 6: Commit**

```bash
git add .golangci.yml
git commit -m "ci: add golangci-lint v2 configuration" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Kompatibilitäts-Probe für Code der anderen Streams

Ziel: Nachweisen, dass typische, idiomatische Muster aus Streams A–D die Konfiguration bestehen, bevor diese Streams nach F mergen. Jede Probe-Datei liegt in dem Paketpfad, in dem der echte Code später entsteht, weil die Ausnahmen in `.golangci.yml` pfadbasiert sind. Die Probe-Pakete werden **nie** committet.

**Files:**
- Temporär: `internal/agent/lintprobe/probe.go` (Stream C), `internal/notify/lintprobe/probe.go` (Stream B), `internal/lintprobe/probe.go` und `internal/lintprobe/probe_test.go` (Streams A/B/D)
- Modify (nur bei Befunden): `.golangci.yml`

- [ ] **Step 1: Probe-Pakete anlegen**

`internal/agent/lintprobe/probe.go`:

```go
// Package lintprobe holds idiomatic stream-C patterns. Verification only, never commit.
package lintprobe

import (
	"errors"
	"fmt"
	"net"
	"os"
)

// Listen mirrors the agent: a Unix socket the panel group may connect to (spec §3: mode 0660).
func Listen(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// PeerAllowed compares the kernel peer uid (uint32) with the configured panel_uid (int).
func PeerAllowed(peerUID uint32, panelUID int) bool { return int(peerUID) == panelUID }
```

`internal/notify/lintprobe/probe.go`:

```go
// Package lintprobe holds idiomatic stream-B notifier patterns. Verification only, never commit.
package lintprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Post sends a notification to an admin-configured webhook URL.
func Post(ctx context.Context, c *http.Client, url string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("notifier: unexpected status %d", resp.StatusCode)
	}
	return nil
}
```

`internal/lintprobe/probe.go`:

```go
// Package lintprobe holds idiomatic stream-A/B/D patterns. Verification only, never commit.
package lintprobe

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"
)

// LoginRequest mirrors API DTOs with secret-looking fields (stream A).
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// TotpSetupResponse is returned to the user once, on purpose (stream A).
type TotpSetupResponse struct {
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauth_url"`
}

// FailuresByIP mirrors streams B/D: a hand-written query with rows iteration.
func FailuresByIP(ctx context.Context, db *sql.DB, since time.Time) (map[string]int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT ip_key, count(*) FROM login_attempts WHERE success = 0 AND ts >= ? GROUP BY ip_key`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, rows.Err()
}

// LogFailedLogin mirrors request logging with user-controlled values.
func LogFailedLogin(r *http.Request, username string) {
	slog.InfoContext(r.Context(), "login failed", "username", username, "path", r.URL.Path, "user_agent", r.UserAgent())
}

// SetSessionCookie mirrors stream A's session cookie (spec §4.4).
func SetSessionCookie(w http.ResponseWriter, token string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     "__Host-sid",
		Value:    token,
		Path:     "/",
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}
```

`internal/lintprobe/probe_test.go`:

```go
package lintprobe_test

import (
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestProbe(t *testing.T) {
	const adminPassword = "correct horse battery staple"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	path := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(path, []byte(adminPassword), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := rand.IntN(10); n < 0 {
		t.Fatal("impossible")
	}
}
```

- [ ] **Step 2: Probes linten**

```bash
probes="./internal/agent/lintprobe/... ./internal/notify/lintprobe/... ./internal/lintprobe/..."
.dev/tools/golangci-lint run $probes
GOOS=linux .dev/tools/golangci-lint run $probes
```
Expected: beide Male `0 issues.`

Verbindliche Reaktion auf Befunde (gleiche Regeln wie Task 1 Step 4: gezielt, begründet, nie ganze Linter abschalten):

| Befund | Reaktion |
|---|---|
| `gosec` `G704` (SSRF) in `internal/notify/lintprobe/probe.go` | Regel: `- path: internal/notify/` / `linters: [gosec]` / `text: G704`, Kommentar `# webhook URLs are admin-configured (sudo + audit), spec §7.5` |
| `gosec` `G706` in `internal/lintprobe/probe.go` (`slog.InfoContext`) | `G706` in `linters.settings.gosec.excludes`, Kommentar `# slog handlers escape attribute values` |
| `gosec` `G124` auf `SetSessionCookie` | kein Config-Fix, weil das Muster korrekt ist. Meldung samt gosec-Version an den Lead (Fehlalarm im Linter) |
| `gosec` `G302` oder `noctx` in `internal/agent/lintprobe/probe.go` | die Regeln `source: "0o?660"` bzw. `path: (cmd\|internal)/agent/` in `.golangci.yml` prüfen und korrigieren |
| irgendein Befund in `probe_test.go` | Test-Ausnahmeregeln (`path: _test\.go`) in `.golangci.yml` prüfen und korrigieren |
| sonstiger Befund in idiomatischem Code | gezielte Regel mit `path`, `linters` und `text` plus Begründungskommentar |

Nach jeder Config-Änderung `config verify` (Task 1 Step 3) und Step 2 wiederholen.

- [ ] **Step 3: Gegenprobe – die agent-Ausnahme ist wirklich auf den agent beschränkt**

```bash
mkdir -p internal/lintprobe2
sed 's/^package lintprobe$/package lintprobe2/' internal/agent/lintprobe/probe.go > internal/lintprobe2/probe.go
.dev/tools/golangci-lint run ./internal/lintprobe2/...; echo "exit=$?"
rm -rf internal/lintprobe2
```
Expected: genau ein Befund `net.Listen must not be called … (noctx)` in `internal/lintprobe2/probe.go` und `exit=1`. Außerhalb des agent bleibt `noctx` für `net.Listen` also aktiv. (Der `G302`-Befund bleibt aus, weil die `0660`-Regel pfadunabhängig gilt.)

- [ ] **Step 4: Probes entfernen und Arbeitsbaum prüfen**

```bash
rm -rf internal/agent/lintprobe internal/notify/lintprobe internal/lintprobe internal/lintprobe2
git status --porcelain -- internal/
.dev/tools/golangci-lint run ./...
```
Expected: `git status` ohne Ausgabe; `0 issues.`

- [ ] **Step 5: Commit (nur wenn `.golangci.yml` geändert wurde)**

```bash
git diff --quiet -- .golangci.yml || {
  git add .golangci.yml
  git commit -m "ci: tune lint exclusions for stream code patterns" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
}
```

---

### Task 3: `.goreleaser.yaml` und Snapshot-Verifikation

**Files:**
- Create: `.goreleaser.yaml`
- Temporär (nur falls `cmd/agent` noch fehlt): `cmd/agent/main.go`

- [ ] **Step 1: `.goreleaser.yaml` schreiben**

```yaml
# yaml-language-server: $schema=https://goreleaser.com/static/schema.json
# GoReleaser v2 configuration for mc-panel (spec §3.5).
# Local snapshot: `make release`. Tagged release: .github/workflows/release.yml (tags v*).
# Requires GoReleaser >= v2.8 (archives.ids); CI pins v2.18.2.
version: 2

project_name: mc-panel

before:
  hooks:
    # Builds the React app into internal/webui/dist, which cmd/panel embeds via go:embed.
    # The release workflow runs this step itself (without GITHUB_TOKEN) and passes --skip=before.
    - make web-build

builds:
  - id: panel
    main: ./cmd/panel
    binary: panel
    env:
      - CGO_ENABLED=0
    goos:
      - linux
    goarch:
      - amd64
      - arm64
    flags:
      - -trimpath
    ldflags:
      - -s -w -X main.version={{ .Version }}
    mod_timestamp: "{{ .CommitTimestamp }}"
    hooks:
      post:
        - cmd: >-
            sh -c 'go version -m "{{ .Path }}" | grep -q "CGO_ENABLED=0" || { echo "cgo detected in {{ .Path }}" >&2; exit 1; }'
          output: true
        # "index.html" alone proves nothing (net/http contains that literal); the hashed Vite
        # entry "/assets/index-" only exists when the real frontend build was embedded.
        - cmd: >-
            sh -c 'grep -aq "/assets/index-" "{{ .Path }}" || { echo "no embedded frontend in {{ .Path }} - run make web-build" >&2; exit 1; }'
          output: true

  - id: agent
    main: ./cmd/agent
    binary: agent
    env:
      - CGO_ENABLED=0
    goos:
      - linux
    goarch:
      - amd64
      - arm64
    flags:
      - -trimpath
    ldflags:
      - -s -w -X main.version={{ .Version }}
    mod_timestamp: "{{ .CommitTimestamp }}"
    hooks:
      post:
        - cmd: >-
            sh -c 'go version -m "{{ .Path }}" | grep -q "CGO_ENABLED=0" || { echo "cgo detected in {{ .Path }}" >&2; exit 1; }'
          output: true

archives:
  - id: default
    ids:
      - panel
      - agent
    formats:
      - tar.gz
    name_template: "mc-panel_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    # Binaries only: "none*" matches nothing and disables the default README/LICENSE globs.
    files:
      - none*

checksum:
  name_template: checksums.txt
  algorithm: sha256

snapshot:
  version_template: "{{ incpatch .Version }}-snapshot+{{ .ShortCommit }}"

changelog:
  use: git
  sort: asc
  filters:
    exclude:
      - '^(docs|test|ci|chore|style)(\(.+\))?!?:'
      - "^Merge "
  groups:
    - title: Features
      regexp: '^feat(\(.+\))?!?:'
      order: 0
    - title: Bug fixes
      regexp: '^fix(\(.+\))?!?:'
      order: 1
    - title: Other changes
      order: 999

release:
  name_template: "mc-panel {{ .Version }}"
  prerelease: auto
  draft: false
```

- [ ] **Step 2: Konfiguration prüfen**

```bash
goreleaser --version | head -n 3
goreleaser check 2>&1 | tee .dev/goreleaser-check.log
grep -qi deprecat .dev/goreleaser-check.log && echo "DEPRECATIONS FOUND" || echo NO-DEPRECATIONS
```
Expected: Version ≥ 2.8 (Homebrew liefert aktuell 2.18.x); `goreleaser check` mit Exit-Code 0 und Meldung `1 configuration file(s) validated`; `NO-DEPRECATIONS`.

- [ ] **Step 3: Temporären agent-Stub anlegen (nur falls Stream C noch nicht gemergt ist)**

Merge-Reihenfolge ist F vor C, daher fehlt `cmd/agent` im Stream-F-Worktree in der Regel. Der Stub dient nur dieser Verifikation:

```bash
test -f cmd/agent/main.go && echo "agent present - skip stub" || {
mkdir -p cmd/agent
cat > cmd/agent/main.go <<'EOF'
// Temporary stub to verify .goreleaser.yaml before stream C lands. Never commit.
package main

import "fmt"

var version = "dev"

func main() { fmt.Println("agent", version) }
EOF
echo STUB-CREATED; }
```

- [ ] **Step 4: Snapshot bauen**

```bash
(cd web && npm ci)
make release
ls -1 dist
```
Expected: GoReleaser endet mit `release succeeded`; in `dist/` liegen u. a. `checksums.txt`, `mc-panel_<v>-snapshot+<sha>_linux_amd64.tar.gz`, `mc-panel_<v>-snapshot+<sha>_linux_arm64.tar.gz`, `artifacts.json` sowie die Build-Verzeichnisse `panel_linux_amd64_v1/`, `panel_linux_arm64_v8.0/`, `agent_linux_amd64_v1/`, `agent_linux_arm64_v8.0/`.

- [ ] **Step 5: Architektur, Static Linking, CGO, Version und eingebettetes Frontend prüfen**

```bash
file dist/*/panel dist/*/agent
for b in dist/*/panel dist/*/agent; do
  go version -m "$b" | grep -E 'CGO_ENABLED|-ldflags'
done
for b in dist/*/panel; do printf '%s embedded-ui-markers=%s\n' "$b" "$(grep -ac '/assets/index-' "$b")"; done
```
Expected:
- `file`: Für `*_amd64_v1/*` jeweils `ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked, … stripped`, für `*_arm64_v8.0/*` jeweils `ELF 64-bit LSB executable, ARM aarch64, version 1 (SYSV), statically linked, … stripped`.
- `go version -m`: je Binary `build	CGO_ENABLED=0` und `build	-ldflags="-s -w -X main.version=<v>-snapshot+<sha>"`.
- Beide `panel`-Binaries: `embedded-ui-markers=` ≥ 1.

- [ ] **Step 6: Archive und Checksummen prüfen**

```bash
for a in dist/mc-panel_*_linux_amd64.tar.gz dist/mc-panel_*_linux_arm64.tar.gz; do
  printf '%s: %s\n' "$a" "$(tar -tzf "$a" | sort | paste -sd' ' -)"
done
(cd dist && shasum -a 256 -c checksums.txt)
```
Expected: beide Archive `agent panel` (keine weiteren Dateien, kein Unterordner); `shasum`: zwei Zeilen `…tar.gz: OK`.

- [ ] **Step 7: Negativtest – der Embed-Check unterscheidet wirklich**

```bash
tmp="$(mktemp -d)"
make clean
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$tmp/panel-noui" ./cmd/panel
printf 'vite-marker=%s naive-index.html=%s\n' \
  "$(grep -ac '/assets/index-' "$tmp/panel-noui")" \
  "$(strings "$tmp/panel-noui" | grep -c 'index.html')"
sh -c 'grep -aq "/assets/index-" "'"$tmp/panel-noui"'" || { echo "HOOK WOULD FAIL (correct)"; exit 1; }'; echo "exit=$?"
rm -rf "$tmp"
```
Expected: `vite-marker=0 naive-index.html=` ≥ 1 (der naive Check wäre fälschlich grün), danach `HOOK WOULD FAIL (correct)` und `exit=1`.

- [ ] **Step 8: Stub entfernen, Arbeitsbaum prüfen, Commit**

```bash
if grep -q 'Temporary stub to verify .goreleaser.yaml' cmd/agent/main.go 2>/dev/null; then rm -rf cmd/agent; fi
make clean
git status --porcelain
git add .goreleaser.yaml
git commit -m "build: add GoReleaser config for linux amd64/arm64 releases" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
Expected: `git status --porcelain` zeigt vor dem `git add` ausschließlich `?? .goreleaser.yaml`.

---

### Task 4: CI-Workflow `.github/workflows/ci.yml`

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Workflow schreiben**

```yaml
name: CI

on:
  push:
    branches:
      - main
      - "stream/**"
  pull_request:
  schedule:
    # Weekly run so new advisories (govulncheck, npm audit) surface without code changes.
    - cron: "17 5 * * 1"
  workflow_dispatch:

permissions:
  contents: read

concurrency:
  group: ci-${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: true

env:
  CGO_ENABLED: "0"

defaults:
  run:
    shell: bash

jobs:
  go:
    name: Go (tests, race, lint, vulns)
    runs-on: ubuntu-24.04
    timeout-minutes: 30
    steps:
      - name: Checkout
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false

      - name: Set up Go
        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache: true

      - name: Verify modules
        run: |
          go mod verify
          go mod tidy -diff

      - name: Check sqlc output is up to date
        run: |
          set -euo pipefail
          version="$(grep -m1 -oE 'sqlc v[0-9]+\.[0-9]+\.[0-9]+' internal/store/db.go | cut -d' ' -f2 || true)"
          if [ -z "${version}" ]; then
            echo "::error file=internal/store/db.go::no 'sqlc vX.Y.Z' header found"
            exit 1
          fi
          echo "Using sqlc ${version} (taken from the generated header)"
          CGO_ENABLED=1 go install "github.com/sqlc-dev/sqlc/cmd/sqlc@${version}"
          "$(go env GOPATH)/bin/sqlc" generate
          if [ -n "$(git status --porcelain -- internal/store)" ]; then
            git status --porcelain -- internal/store
            git --no-pager diff -- internal/store
            echo "::error::generated sqlc code is stale - run 'sqlc generate' and commit the result"
            exit 1
          fi

      - name: Unit tests
        run: make test-go

      - name: Race tests
        run: make test-race

      - name: golangci-lint (incl. gosec, gofmt, goimports)
        uses: golangci/golangci-lint-action@ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a # v9.3.0
        with:
          version: v2.14.0

      - name: govulncheck
        run: go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

  web:
    name: Web (lint, typecheck, test, build, audit)
    runs-on: ubuntu-24.04
    timeout-minutes: 20
    defaults:
      run:
        working-directory: web
    steps:
      - name: Checkout
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false

      - name: Set up Node
        uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0
        with:
          node-version: lts/*
          cache: npm
          cache-dependency-path: web/package-lock.json

      - name: Install dependencies
        run: npm ci

      - name: Lint
        run: npm run lint

      - name: Typecheck
        run: npm run typecheck

      - name: Unit tests
        run: npm test

      - name: Build
        run: npm run build

      - name: Generated files are committed
        run: |
          set -euo pipefail
          npm run gen:api
          if [ -n "$(git status --porcelain -- .)" ]; then
            git status --porcelain -- .
            git --no-pager diff -- .
            echo "::error::web/ has uncommitted generated changes - run 'npm run gen:api' and 'npm run build', then commit"
            exit 1
          fi

      - name: npm audit
        run: npm audit --audit-level=high

  openapi:
    name: OpenAPI lint
    runs-on: ubuntu-24.04
    timeout-minutes: 10
    steps:
      - name: Checkout
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false

      - name: Set up Node
        uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0
        with:
          node-version: lts/*

      - name: Redocly lint
        run: npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal

  nftables:
    name: nftables integration (root)
    runs-on: ubuntu-24.04
    timeout-minutes: 15
    steps:
      - name: Checkout
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false

      - name: Set up Go
        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache: true

      - name: Install nftables
        run: |
          sudo apt-get update -qq
          sudo apt-get install -y -qq --no-install-recommends nftables
          sudo nft --version

      - name: Look for integration tests
        run: |
          if ! grep -rlE '^//go:build .*nftintegration' --include='*_test.go' internal/agent; then
            echo "::warning::no nftintegration tests under internal/agent yet (expected once stream C is merged)"
          fi

      # Test binaries are compiled as the runner user and only executed as root,
      # so no root-owned files end up in the Go caches.
      - name: Integration tests as root
        run: go test -tags nftintegration -exec sudo -count=1 -v ./internal/agent/...

  build:
    name: Build & release snapshot
    needs:
      - go
      - web
    runs-on: ubuntu-24.04
    timeout-minutes: 30
    steps:
      - name: Checkout
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
          persist-credentials: false

      - name: Set up Go
        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache: true

      - name: Set up Node
        uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0
        with:
          node-version: lts/*
          cache: npm
          cache-dependency-path: web/package-lock.json

      - name: Install web dependencies
        working-directory: web
        run: npm ci

      - name: make build
        run: make build

      - name: Check host binary (static, no cgo, embedded UI)
        run: |
          set -euo pipefail
          file bin/panel
          file bin/panel | grep -q 'ELF 64-bit LSB executable, x86-64'
          file bin/panel | grep -q 'statically linked'
          go version -m bin/panel | grep -q 'CGO_ENABLED=0'
          grep -aq '/assets/index-' bin/panel

      - name: Cross-compile linux/arm64
        run: |
          set -euo pipefail
          out="${RUNNER_TEMP}/linux-arm64"
          mkdir -p "${out}"
          GOOS=linux GOARCH=arm64 go build -trimpath -o "${out}/" ./cmd/...
          file "${out}/panel"
          file "${out}/panel" | grep -q 'ARM aarch64'

      - name: GoReleaser config check
        uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94 # v7.2.3
        with:
          distribution: goreleaser
          version: v2.18.2
          args: check

      - name: Snapshot skipped notice
        if: hashFiles('cmd/agent/main.go') == ''
        run: echo "::warning::cmd/agent is missing (stream C not merged yet) - release snapshot skipped"

      - name: GoReleaser snapshot
        if: hashFiles('cmd/agent/main.go') != ''
        uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94 # v7.2.3
        with:
          distribution: goreleaser
          version: v2.18.2
          args: release --snapshot --clean

      - name: Verify snapshot artifacts
        if: hashFiles('cmd/agent/main.go') != ''
        run: |
          set -euo pipefail
          ls -l dist
          count="$(jq '[.[] | select(.type == "Binary")] | length' dist/artifacts.json)"
          if [ "${count}" -ne 4 ]; then
            echo "::error::expected 4 binaries (panel+agent x amd64+arm64), got ${count}"
            exit 1
          fi
          jq -r '.[] | select(.type == "Binary") | "\(.goarch) \(.name) \(.path)"' dist/artifacts.json |
            while read -r arch name path; do
              case "${arch}" in
                amd64) want='x86-64' ;;
                arm64) want='ARM aarch64' ;;
                *) echo "::error::unexpected arch ${arch}"; exit 1 ;;
              esac
              info="$(file "${path}")"
              echo "${info}"
              grep -q "${want}" <<<"${info}"
              grep -q 'statically linked' <<<"${info}"
              go version -m "${path}" | grep -q 'CGO_ENABLED=0'
              go version -m "${path}" | grep -q -- '-X main.version='
              if [ "${name}" = panel ]; then
                grep -aq '/assets/index-' "${path}"
              fi
            done
          for arch in amd64 arm64; do
            set -- dist/mc-panel_*_linux_"${arch}".tar.gz
            if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
              echo "::error::expected exactly one archive for ${arch}"
              exit 1
            fi
            contents="$(tar -tzf "$1" | sort | paste -sd' ' -)"
            if [ "${contents}" != "agent panel" ]; then
              echo "::error::$1 contains '${contents}', expected 'agent panel'"
              exit 1
            fi
          done
          (cd dist && sha256sum -c checksums.txt)
```

- [ ] **Step 2: Mit actionlint (inkl. shellcheck) validieren**

```bash
command -v shellcheck
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml; echo "exit=$?"
```
Expected: `shellcheck`-Pfad wird ausgegeben (actionlint prüft damit die `run:`-Skripte); actionlint gibt keine Befundzeilen aus, danach `exit=0`.

- [ ] **Step 3: Jobs lokal nachstellen (soweit macOS es erlaubt)**

```bash
go mod verify && go mod tidy -diff && echo MODULES-OK
make test-go && make test-race
.dev/tools/golangci-lint run ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
(cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build && npm run gen:api && git status --porcelain -- . && npm audit --audit-level=high)
npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal
make build && file bin/panel && grep -ac '/assets/index-' bin/panel
make clean
```
Expected: `all modules verified`, `MODULES-OK`; Tests grün; `0 issues.`; govulncheck `No vulnerabilities found.`; Web-Schritte grün, `git status --porcelain -- .` ohne Ausgabe, `found 0 vulnerabilities` (oder nur Befunde unterhalb `high`); Redocly `Woohoo! Your API description is valid.`; `bin/panel: Mach-O 64-bit executable arm64` (Host-Build) und Marker-Zahl ≥ 1. Der nftables-Job und die Linux-`file`-Prüfungen laufen nur in CI (Push des Branches `stream/f` triggert den Workflow, sobald ein Remote existiert).

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add GitHub Actions workflow for Go, web, OpenAPI, nftables and build" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Release-Workflow und Dependabot

**Files:**
- Create: `.github/workflows/release.yml`, `.github/dependabot.yml`

- [ ] **Step 1: `.github/workflows/release.yml` schreiben**

```yaml
name: Release

on:
  push:
    tags:
      - "v*"

permissions: {}

concurrency:
  group: release-${{ github.ref }}
  cancel-in-progress: false

defaults:
  run:
    shell: bash

jobs:
  release:
    name: GoReleaser
    runs-on: ubuntu-24.04
    timeout-minutes: 30
    permissions:
      contents: write
    env:
      CGO_ENABLED: "0"
    steps:
      - name: Checkout
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
          persist-credentials: false

      # No caches in release builds: a cache written by a PR run must never feed a release.
      - name: Set up Go
        uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache: false

      - name: Set up Node
        uses: actions/setup-node@820762786026740c76f36085b0efc47a31fe5020 # v7.0.0
        with:
          node-version: lts/*

      - name: Install web dependencies
        working-directory: web
        run: npm ci

      - name: Tests
        run: make test

      # Built here, without GITHUB_TOKEN in the environment, so npm/Vite code never sees it.
      # GoReleaser then runs with --skip=before; its post-build hook still fails the release
      # if the frontend is not embedded.
      - name: Build frontend
        run: make web-build

      - name: GoReleaser
        uses: goreleaser/goreleaser-action@f06c13b6b1a9625abc9e6e439d9c05a8f2190e94 # v7.2.3
        with:
          distribution: goreleaser
          version: v2.18.2
          args: release --clean --skip=before
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 2: `.github/dependabot.yml` schreiben**

```yaml
version: 2
updates:
  # Keeps the SHA-pinned actions (and their version comments) current.
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
    groups:
      github-actions:
        patterns:
          - "*"
    commit-message:
      prefix: ci
```

- [ ] **Step 3: actionlint**

```bash
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12; echo "exit=$?"
```
Expected: keine Befundzeilen für die Workflows in `.github/workflows/`, danach `exit=0`.

- [ ] **Step 4: Sicherheitsprüfungen der Workflows**

```bash
echo "--- pull_request_target (expect none)"; grep -rn 'pull_request_target' .github/workflows || true
echo "--- secrets usage (expect only GITHUB_TOKEN in release.yml)"; grep -rn 'secrets\.' .github/workflows
echo "--- write permissions (expect only release.yml job)"; grep -rn ': write' .github/workflows
echo "--- event interpolation in scripts (expect none)"; grep -rn 'github\.event\.' .github/workflows | grep -v 'concurrency\|group:' || true
echo "--- unpinned uses (expect none)"; grep -rnE 'uses: [^ ]+@' .github/workflows | grep -vE '@[0-9a-f]{40} # v[0-9]' || true
echo "--- checkouts without persist-credentials: false (expect none)"
for f in .github/workflows/*.yml; do
  printf '%s checkouts=%s persist=%s\n' "$f" "$(grep -c 'actions/checkout@' "$f")" "$(grep -c 'persist-credentials: false' "$f")"
done
```
Expected:
- `pull_request_target`: keine Zeile.
- `secrets.`: genau eine Zeile `release.yml: … GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}`.
- `write`: genau eine Zeile `release.yml: … contents: write`.
- `github.event.`: keine Zeile (die einzige Verwendung steht in `concurrency.group` von `ci.yml` und wird herausgefiltert).
- unpinned uses: keine Zeile.
- Pro Datei `checkouts` = `persist` (ci.yml 5 = 5, release.yml 1 = 1).

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/release.yml .github/dependabot.yml
git commit -m "ci: add tag-triggered GoReleaser workflow and Dependabot for actions" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Linux-VM-Skripte

**Entscheidung panel-Modus in der VM:** Das panel läuft **ohne** `-dev`, mit expliziter `/etc/mc-panel/panel.toml` und `public_url = "https://mcpanel.test"`. Begründung: Das panel terminiert selbst kein TLS (in Produktion macht das Caddy, Spec §3); `Validate` prüft nur das Schema der `public_url`. Damit läuft exakt der Produktionspfad (JSON-Logs, Prod-Defaults, keine Dev-Sonderwege in Streams A/B), während `-dev` die VM entwerten würde. Der agent läuft ebenfalls ohne `-dev` (sonst `firewall = "noop"`). Testclient ist ein Netz-Namespace `mcpanel-client` mit `192.0.2.2` (TEST-NET-1), das panel lauscht auf `192.0.2.1:8080` am zugehörigen veth – Loopback-Adressen lehnt `agentapi.ValidateBanPrefix` bewusst ab.

**Files:**
- Create: `scripts/vm-setup.sh`, `scripts/vm-test.sh`

- [ ] **Step 1: `scripts/vm-setup.sh` schreiben**

```bash
#!/usr/bin/env bash
# Creates/starts a Linux VM (Lima by default, OrbStack with VM_TOOL=orb), installs the
# GoReleaser snapshot archive from dist/ and starts agent (root) and panel (user mcpanel)
# as transient systemd units. Panel state (DB, jail sets) is reset on every run.
# See docs/dev/linux-vm.md.
set -euo pipefail
cd "$(dirname "$0")/.."

VM_NAME="${VM_NAME:-mcpanel}"
VM_TOOL="${VM_TOOL:-lima}"
BUILD=1
PROVISION_ONLY=0

usage() {
  cat <<'EOF'
usage: scripts/vm-setup.sh [--no-build] [--provision-only]

  --no-build        reuse the snapshot archive already in dist/ (skip `make release`)
  --provision-only  create/start the VM and install packages, user and test network only

environment:
  VM_NAME   VM / machine name (default: mcpanel)
  VM_TOOL   lima (default) or orb (OrbStack)
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --no-build) BUILD=0 ;;
    --provision-only) PROVISION_ONLY=1 ;;
    -h | --help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
  shift
done

die() { echo "vm-setup: $*" >&2; exit 1; }

# vm_root runs the bash script on stdin as root inside the VM.
vm_root() {
  case "$VM_TOOL" in
    lima) limactl shell --workdir / "$VM_NAME" sudo bash -s ;;
    orb) orb -m "$VM_NAME" -u root bash -s ;;
    *) die "unknown VM_TOOL=$VM_TOOL (lima|orb)" ;;
  esac
}

# vm_copy copies a local file to a path inside the VM (as the default VM user).
vm_copy() {
  case "$VM_TOOL" in
    lima) limactl copy "$1" "$VM_NAME:$2" ;;
    orb) orb push -m "$VM_NAME" "$1" "$2" ;;
    *) die "unknown VM_TOOL=$VM_TOOL (lima|orb)" ;;
  esac
}

ensure_vm() {
  case "$VM_TOOL" in
    lima)
      command -v limactl >/dev/null || die "limactl not found - brew install lima"
      if ! limactl list --quiet | grep -qx "$VM_NAME"; then
        echo "== creating Lima VM $VM_NAME (Ubuntu LTS)"
        limactl start --name="$VM_NAME" --tty=false --cpus=2 --memory=4 template:ubuntu-lts
      elif [ "$(limactl list --format '{{.Status}}' "$VM_NAME")" != "Running" ]; then
        echo "== starting Lima VM $VM_NAME"
        limactl start --tty=false "$VM_NAME"
      fi
      ;;
    orb)
      command -v orb >/dev/null || die "orb not found - install OrbStack"
      if ! orb -m "$VM_NAME" true 2>/dev/null; then
        echo "== creating OrbStack machine $VM_NAME (Ubuntu)"
        orb create ubuntu "$VM_NAME"
      fi
      ;;
    *) die "unknown VM_TOOL=$VM_TOOL (lima|orb)" ;;
  esac
}

provision() {
  echo "== provisioning (nftables, curl, user mcpanel, test network)"
  vm_root <<'GUEST'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
if ! command -v nft >/dev/null || ! command -v curl >/dev/null; then
  apt-get update -qq
  apt-get install -y -qq nftables curl
fi
if ! id mcpanel >/dev/null 2>&1; then
  useradd --system --user-group --home-dir /var/lib/mc-panel --no-create-home --shell /usr/sbin/nologin mcpanel
fi
install -d -m 0750 -o root -g mcpanel /etc/mc-panel
# Test client: namespace mcpanel-client (192.0.2.2) <-> veth mcp-host in the root namespace (192.0.2.1).
if [ ! -e /run/netns/mcpanel-client ]; then
  ip netns add mcpanel-client
fi
if ! ip link show mcp-host >/dev/null 2>&1; then
  ip link add mcp-host type veth peer name mcp-client
  ip link set mcp-client netns mcpanel-client
  ip addr add 192.0.2.1/24 dev mcp-host
  ip link set mcp-host up
  ip netns exec mcpanel-client ip addr add 192.0.2.2/24 dev mcp-client
  ip netns exec mcpanel-client ip link set mcp-client up
  ip netns exec mcpanel-client ip link set lo up
fi
ip netns exec mcpanel-client ping -c 1 -W 2 192.0.2.1 >/dev/null
echo "provisioned: $(nft --version), user mcpanel uid=$(id -u mcpanel), client 192.0.2.2 -> 192.0.2.1 ok"
GUEST
}

install_and_start() {
  local arch goarch
  arch="$(echo 'uname -m' | vm_root)"
  case "$arch" in
    aarch64 | arm64) goarch=arm64 ;;
    x86_64 | amd64) goarch=amd64 ;;
    *) die "unsupported VM architecture: $arch" ;;
  esac

  if [ "$BUILD" -eq 1 ]; then
    echo "== building snapshot (make release)"
    make release
  fi
  shopt -s nullglob
  local archives=(dist/mc-panel_*_linux_"$goarch".tar.gz)
  shopt -u nullglob
  [ "${#archives[@]}" -eq 1 ] ||
    die "expected exactly one dist/mc-panel_*_linux_${goarch}.tar.gz, found ${#archives[@]} - run make release"
  echo "== installing ${archives[0]}"
  vm_copy "${archives[0]}" /tmp/mc-panel.tar.gz

  vm_root <<'GUEST'
set -euo pipefail
for unit in mcpanel-panel mcpanel-agent; do
  systemctl stop "$unit.service" 2>/dev/null || true
  systemctl reset-failed "$unit.service" 2>/dev/null || true
done

tmp="$(mktemp -d)"
tar -xzf /tmp/mc-panel.tar.gz -C "$tmp"
install -m 0755 -o root -g root "$tmp/panel" "$tmp/agent" /usr/local/bin/
rm -rf "$tmp" /tmp/mc-panel.tar.gz

cat >/etc/mc-panel/agent.toml <<EOF
socket = "/run/mc-panel/agent.sock"
panel_uid = $(id -u mcpanel)
firewall = "nftables"
EOF
chown root:root /etc/mc-panel/agent.toml
chmod 0600 /etc/mc-panel/agent.toml

cat >/etc/mc-panel/panel.toml <<'EOF'
# VM test profile: production mode (no -dev). In production Caddy terminates TLS;
# the panel itself only requires public_url to use https.
listen = "192.0.2.1:8080"
public_url = "https://mcpanel.test"
data_dir = "/var/lib/mc-panel"
agent_socket = "/run/mc-panel/agent.sock"
trusted_proxies = ["127.0.0.1/32", "::1/128"]
EOF
chown root:mcpanel /etc/mc-panel/panel.toml
chmod 0640 /etc/mc-panel/panel.toml

# Fresh state on every run: no DB, no jail entries left over from earlier tests.
rm -f /var/lib/mc-panel/panel.db /var/lib/mc-panel/panel.db-wal /var/lib/mc-panel/panel.db-shm
for set in mcpanel_jail4 mcpanel_jail6; do
  nft flush set inet mcpanel "$set" 2>/dev/null || true
done

systemd-run --unit=mcpanel-agent --description="mc-panel agent (VM test)" \
  -p User=root -p Group=mcpanel \
  -p RuntimeDirectory=mc-panel -p RuntimeDirectoryMode=0750 \
  /usr/local/bin/agent -config /etc/mc-panel/agent.toml
for _ in $(seq 1 50); do [ -S /run/mc-panel/agent.sock ] && break; sleep 0.2; done
if [ ! -S /run/mc-panel/agent.sock ]; then
  journalctl -u mcpanel-agent --no-pager -n 50
  echo "agent socket /run/mc-panel/agent.sock missing" >&2
  exit 1
fi

systemd-run --unit=mcpanel-panel --description="mc-panel panel (VM test)" \
  -p User=mcpanel -p Group=mcpanel \
  -p StateDirectory=mc-panel -p StateDirectoryMode=0700 \
  /usr/local/bin/panel serve -config /etc/mc-panel/panel.toml
for _ in $(seq 1 50); do curl -fsS http://192.0.2.1:8080/api/health >/dev/null 2>&1 && break; sleep 0.2; done
if ! curl -fsS http://192.0.2.1:8080/api/health; then
  journalctl -u mcpanel-panel --no-pager -n 50
  echo "panel health check failed" >&2
  exit 1
fi
echo
stat -c 'socket: %a %U:%G %n' /run/mc-panel/agent.sock
systemctl is-active mcpanel-agent.service mcpanel-panel.service
if nft list table inet mcpanel >/dev/null 2>&1; then
  echo "nftables: table inet mcpanel present"
else
  echo "WARNING: table inet mcpanel not present yet (agent creates it on startup once stream C is merged)"
fi
GUEST
}

ensure_vm
provision
if [ "$PROVISION_ONLY" -eq 1 ]; then
  echo "== done (provision only)"
  exit 0
fi
install_and_start
echo "== done: agent + panel running in VM $VM_NAME (panel at http://192.0.2.1:8080 inside the VM)"
```

- [ ] **Step 2: `scripts/vm-test.sh` schreiben**

```bash
#!/usr/bin/env bash
# Acceptance check 5 (spec §12), nftables part: five failed logins from the test client
# namespace (192.0.2.2) must put that IP into `nft list set inet mcpanel mcpanel_jail4`.
# Needs the Phase-2-integrated panel (jail -> agent wiring). Redeploys and resets the VM
# state via scripts/vm-setup.sh --no-build first, so every run starts without jails.
set -euo pipefail
cd "$(dirname "$0")/.."

VM_NAME="${VM_NAME:-mcpanel}"
VM_TOOL="${VM_TOOL:-lima}"

case "${1:-}" in
  "") ;;
  -h | --help) echo "usage: scripts/vm-test.sh   (env: VM_NAME, VM_TOOL as in vm-setup.sh)"; exit 0 ;;
  *) echo "usage: scripts/vm-test.sh" >&2; exit 2 ;;
esac

vm_root() {
  case "$VM_TOOL" in
    lima) limactl shell --workdir / "$VM_NAME" sudo bash -s ;;
    orb) orb -m "$VM_NAME" -u root bash -s ;;
    *) echo "vm-test: unknown VM_TOOL=$VM_TOOL (lima|orb)" >&2; exit 2 ;;
  esac
}

VM_NAME="$VM_NAME" VM_TOOL="$VM_TOOL" scripts/vm-setup.sh --no-build

vm_root <<'GUEST'
set -euo pipefail
ns=mcpanel-client
client_ip=192.0.2.2
base=http://192.0.2.1:8080
origin=https://mcpanel.test
body=/tmp/vm-test-body

login() {
  ip netns exec "$ns" curl -sS -o "$body" -w '%{http_code}' --max-time 5 \
    -X POST "$base/api/auth/login" \
    -H 'Content-Type: application/json' \
    -H "Origin: $origin" \
    -H 'Sec-Fetch-Site: same-origin' \
    --data '{"username":"vm-test-nobody","password":"definitely-not-the-password"}'
}

echo "== 5 failed logins from $client_ip"
for i in 1 2 3 4 5; do
  code="$(login)" || code="curl-error"
  echo "attempt $i: HTTP $code $(cat "$body" 2>/dev/null || true)"
  if [ "$code" != 401 ]; then
    echo "FAIL: expected HTTP 401 on attempt $i" >&2
    journalctl -u mcpanel-panel --no-pager -n 40
    exit 1
  fi
done

echo "== waiting for $client_ip in set inet mcpanel mcpanel_jail4"
found=0
for _ in $(seq 1 30); do
  if nft list set inet mcpanel mcpanel_jail4 2>/dev/null | grep -qE "(^|[^0-9.])192\.0\.2\.2([^0-9]|$)"; then
    found=1
    break
  fi
  sleep 1
done
if [ "$found" -ne 1 ]; then
  nft list ruleset || true
  journalctl -u mcpanel-panel -u mcpanel-agent --no-pager -n 80
  echo "FAIL: $client_ip not in inet mcpanel mcpanel_jail4 after 30s" >&2
  exit 1
fi
nft list set inet mcpanel mcpanel_jail4

echo "== informational: request from $client_ip after the ban"
if ip netns exec "$ns" curl -sS -o /dev/null --max-time 3 "$base/api/health"; then
  echo "NOTE: request still reached the panel (app-level jail answers 403; packet drop depends on the agent's chain)"
else
  echo "OK: request from $client_ip was dropped by nftables"
fi
echo "PASS: acceptance check 5 (nftables part)"
GUEST
```

- [ ] **Step 3: Statische Prüfung**

```bash
chmod +x scripts/vm-setup.sh scripts/vm-test.sh
bash -n scripts/vm-setup.sh && bash -n scripts/vm-test.sh && echo SYNTAX-OK
shellcheck -x scripts/vm-setup.sh scripts/vm-test.sh && echo SHELLCHECK-OK
scripts/vm-setup.sh --help
scripts/vm-setup.sh --bogus; echo "exit=$?"
```
Expected: `SYNTAX-OK`, `SHELLCHECK-OK`, Usage-Text; bei `--bogus` Usage auf stderr und `exit=2`.

- [ ] **Step 4: VM provisionieren (Stream-F-Verifikation)**

**Vor diesem Schritt den Nutzer fragen:** `brew install lima` und der erste `limactl start` laden Lima bzw. das Ubuntu-LTS-Cloud-Image (ca. 600 MB, Quelle cloud-images.ubuntu.com über Lima-Template `ubuntu-lts`).

```bash
brew install lima
limactl --version
scripts/vm-setup.sh --provision-only
limactl shell --workdir / mcpanel sudo ip netns list
limactl shell --workdir / mcpanel id mcpanel
```
Expected: letzte Zeile des Provisionings `provisioned: nftables v1.…, user mcpanel uid=<n>, client 192.0.2.2 -> 192.0.2.1 ok`, danach `== done (provision only)`; `ip netns list` zeigt `mcpanel-client`; `id mcpanel` zeigt `uid=<n>(mcpanel) gid=<m>(mcpanel)`. Ein zweiter Aufruf `scripts/vm-setup.sh --provision-only` läuft ohne Fehler durch (idempotent).

Der vollständige Lauf (`scripts/vm-setup.sh` ohne Flags und `scripts/vm-test.sh`) braucht `cmd/agent` aus Stream C bzw. die Phase-2-Verdrahtung und ist als Phase-2-Verifikation im Wiring-Vertrag festgelegt.

- [ ] **Step 5: Commit**

```bash
git add scripts/vm-setup.sh scripts/vm-test.sh
git commit -m "test: add Lima VM setup and nftables acceptance scripts" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Entwickler-Dokumentation

**Files:**
- Create: `docs/dev/README.md`, `docs/dev/linux-vm.md`

- [ ] **Step 1: `docs/dev/README.md` schreiben**

````markdown
# Entwicklung

Lokale Entwicklung auf macOS (Apple Silicon), Release-Ziele `linux/amd64` und `linux/arm64`.
Architektur und Anforderungen: `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`.

## Toolchain

```bash
brew install go node sqlc goose golangci-lint goreleaser jq shellcheck lima
```

| Werkzeug | Version | Hinweis |
|---|---|---|
| Go | laut `go.mod` | CI liest dieselbe Datei (`go-version-file`) |
| Node.js | aktuelles LTS | CI nutzt `lts/*` |
| golangci-lint | v2.14.0 | CI-Version; exakt dieselbe lokal: `GOBIN="$PWD/.dev/tools" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0` |
| GoReleaser | v2.18.2 (CI), lokal ≥ v2.8 | `make release` nutzt das lokal installierte `goreleaser` |
| sqlc | Version im Header von `internal/store/db.go` | CI installiert genau diese Version; wer mit einer neueren Version generiert, committet den geänderten Header mit |
| govulncheck | v1.8.0 | `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |
| Redocly CLI | 2.54.2 | `npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal` |
| actionlint | v1.7.12 | `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` (nutzt `shellcheck`, falls installiert) |
| Lima | aktuell | nur für die Linux-VM, siehe `docs/dev/linux-vm.md` |

## Schnellstart

```bash
(cd web && npm ci)
make dev        # agent + panel (-dev) + Vite; UI unter http://localhost:5173
```

## Make-Targets

| Target | Wirkung |
|---|---|
| `make dev` | agent (falls gebaut) + `panel serve -dev` + Vite-Dev-Server, alles unter `./.dev/` |
| `make test` | Go-Tests (`test-go`) und Frontend-Tests (`test-web`) |
| `make test-race` | Go-Tests mit Race-Detector (`CGO_ENABLED=1`) |
| `make lint` | `golangci-lint run ./...` plus ESLint und TypeScript-Check im Frontend |
| `make generate` | `sqlc generate` und `npm run gen:api` |
| `make build` | Frontend bauen, dann `bin/panel` (und `bin/agent`) für das Host-System |
| `make release` | GoReleaser-Snapshot nach `dist/`: Archive für linux/amd64 und linux/arm64 plus `checksums.txt` |
| `make clean` | entfernt `bin/`, `dist/`, `.dev/` und das gebaute Frontend |

## CI lokal nachstellen

| CI-Job | Lokal |
|---|---|
| `go` | `go mod verify && go mod tidy -diff`, `sqlc generate && git status --porcelain internal/store`, `make test-go`, `make test-race`, `.dev/tools/golangci-lint run ./...`, `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` |
| `web` | `cd web && npm ci && npm run lint && npm run typecheck && npm test && npm run build && npm run gen:api && git status --porcelain . && npm audit --audit-level=high` |
| `openapi` | `npx --yes @redocly/cli@2.54.2 lint api/openapi.yaml --extends=minimal` |
| `nftables` | nur unter Linux als root: in der VM oder per CI (`go test -tags nftintegration -exec sudo ./internal/agent/...`) |
| `build` | `make build`, `make release`, dann `file dist/*/panel dist/*/agent` |

Linux-spezifische Dateien lokal linten: `GOOS=linux .dev/tools/golangci-lint run ./...`.

## Lint-Konventionen

- Die Konfiguration steht in `.golangci.yml`; gosec läuft als Teil von golangci-lint.
- Ausnahmen gehören gezielt in `.golangci.yml` (Pfad + Linter + Text, mit Begründung) oder als `//nolint:<linter> // <Begründung>` direkt an die Zeile. Nie einen Linter global abschalten.
- Requests und DB-Zugriffe immer mit Kontext: `http.NewRequestWithContext`, `QueryContext`/`ExecContext`/`PingContext`. Ausnahme ist `net.Listen` im agent (`cmd/agent/`, `internal/agent/`).
- Dateimodus `0660` (agent-Socket, Spec §3) ist für gosec G302 freigegeben, sofern er als Literal (`0o660`) in der Zeile steht; andere Modi über `0600` brauchen eine Begründung.
- In Tests sind feste Test-Passwörter, `math/rand` und lockere Dateimodi erlaubt.
- Formatierung: `gofmt` + `goimports` mit lokalem Präfix `github.com/Bernotese/mc-panel` (`golangci-lint fmt ./...`).

## Release

```bash
git switch main && git pull --ff-only
make test && make lint
git tag -a v0.1.0 -m "mc-panel v0.1.0"
git push origin v0.1.0
```

Der Workflow `.github/workflows/release.yml` testet, baut das Frontend und veröffentlicht mit GoReleaser:

- `mc-panel_<version>_linux_amd64.tar.gz`, `mc-panel_<version>_linux_arm64.tar.gz` (je `panel` und `agent`, ohne Unterordner)
- `checksums.txt` (SHA-256, Format `<hash>  <datei>`)

`<version>` ist der Tag ohne führendes `v`. Tags mit Suffix (z. B. `v0.2.0-rc.1`) werden als Pre-Release markiert.
Vor dem Taggen lokal prüfen: `make release && (cd dist && shasum -a 256 -c checksums.txt)`.

## Linux-VM

Produktionsnahe Tests (nftables, systemd, Benutzer `mcpanel`): siehe `docs/dev/linux-vm.md`.
````

- [ ] **Step 2: `docs/dev/linux-vm.md` schreiben**

````markdown
# Linux-VM für produktionsnahe Tests

macOS hat weder nftables noch systemd. Für Spec §3.5 und die Abnahmekriterien 5 und 7 läuft mc-panel deshalb in einer Ubuntu-LTS-VM unter Lima (Standard) oder OrbStack.

## Was die Skripte aufbauen

```
macOS                         VM (Ubuntu LTS, Architektur = Mac: arm64 bzw. amd64)
─────                         ───────────────────────────────────────────────────
make release → dist/*.tar.gz  /usr/local/bin/{panel,agent}  (aus genau einem Snapshot-Archiv)
scripts/vm-setup.sh ───────▶  mcpanel-agent.service  root, Gruppe mcpanel, /etc/mc-panel/agent.toml
                              mcpanel-panel.service  User mcpanel, /etc/mc-panel/panel.toml (Prod-Modus)
                              /run/mc-panel/agent.sock (0660 root:mcpanel), /var/lib/mc-panel (0700)
                              nftables: table inet mcpanel, sets mcpanel_jail4 / mcpanel_jail6
scripts/vm-test.sh ────────▶  netns mcpanel-client 192.0.2.2 ──veth──▶ 192.0.2.1:8080 (panel)
```

- Das panel läuft **ohne** `-dev`. `public_url = "https://mcpanel.test"` erfüllt die Prod-Validierung; TLS terminiert in Produktion Caddy, nicht das panel. Der Test sendet deshalb `Origin: https://mcpanel.test`.
- Der agent läuft ohne `-dev`, also mit `firewall = "nftables"` und `panel_uid` = UID von `mcpanel`.
- Testclient ist ein eigener Netz-Namespace mit `192.0.2.2`: Loopback-Adressen dürfen nicht gebannt werden, und über Lima-Portweiterleitungen käme jede Anfrage als `127.0.0.1` an.
- Jeder Lauf von `vm-setup.sh` (ohne `--provision-only`) setzt den Zustand zurück: DB gelöscht, Jail-Sets geleert, Units neu gestartet.
- Die Units sind transient (`systemd-run`) und verschwinden beim VM-Neustart; danach `scripts/vm-setup.sh --no-build` erneut ausführen. Dauerhafte Units liefert später der Installer (Teilprojekt 6).

## Ablauf

```bash
brew install lima
scripts/vm-setup.sh --provision-only   # VM anlegen (erstes Mal: Ubuntu-Image ~600 MB), Pakete, User, Testnetz
make release                           # Snapshot-Archive nach dist/
scripts/vm-setup.sh --no-build         # installieren + agent und panel starten
scripts/vm-test.sh                     # Abnahmekriterium 5 (benötigt Phase 2)
```

`scripts/vm-setup.sh` ohne Flags führt `make release` selbst aus. Erwartete Ausgabe am Ende von `vm-setup.sh`:

```
{"status":"ok"}
socket: 660 root:mcpanel /run/mc-panel/agent.sock
active
active
nftables: table inet mcpanel present
== done: agent + panel running in VM mcpanel (panel at http://192.0.2.1:8080 inside the VM)
```

`scripts/vm-test.sh` endet mit `PASS: acceptance check 5 (nftables part)` und zeigt vorher das Set mit `192.0.2.2`. Es funktioniert erst nach der Phase-2-Integration (Jail → agent-Ban); vorher bricht es beim Warten auf das Set mit `FAIL: 192.0.2.2 not in inet mcpanel mcpanel_jail4 after 30s` ab.

## OrbStack statt Lima

```bash
VM_TOOL=orb scripts/vm-setup.sh --provision-only
VM_TOOL=orb scripts/vm-setup.sh --no-build
VM_TOOL=orb scripts/vm-test.sh
```

Die Skripte nutzen dann `orb create ubuntu mcpanel`, `orb -m mcpanel -u root …` und `orb push -m mcpanel …`.

## Nützliche Befehle

```bash
limactl shell mcpanel                                         # Shell in der VM
limactl shell mcpanel sudo journalctl -u mcpanel-panel -u mcpanel-agent -f
limactl shell mcpanel sudo nft list ruleset
limactl shell mcpanel sudo nft list set inet mcpanel mcpanel_jail4
limactl shell mcpanel sudo ip netns exec mcpanel-client curl -s http://192.0.2.1:8080/api/health
limactl stop mcpanel
limactl delete --force mcpanel                                # VM komplett entfernen
```

## Fehlersuche

| Symptom | Ursache / Abhilfe |
|---|---|
| `expected exactly one dist/mc-panel_*_linux_arm64.tar.gz, found 0` | `make release` fehlt oder ist fehlgeschlagen |
| `… found 2` | alte Snapshots in `dist/`: `make clean && make release` |
| `agent socket /run/mc-panel/agent.sock missing` | agent-Log steht direkt darüber; häufig falscher `panel_uid` oder fehlende Root-Rechte |
| `panel health check failed` | panel-Log steht darüber; häufig Validierungsfehler in `/etc/mc-panel/panel.toml` |
| `WARNING: table inet mcpanel not present yet` | agent legt Tabelle/Sets beim Start an (Stream C); vor dessen Merge erwartbar |
| Login liefert `403` statt `401` | CSRF/Origin-Prüfung: `public_url` in `panel.toml` und `Origin`-Header in `vm-test.sh` müssen übereinstimmen |
| Jail aus früherem Lauf blockiert | `scripts/vm-setup.sh --no-build` setzt DB und Sets zurück (macht `vm-test.sh` automatisch) |
````

- [ ] **Step 3: Konsistenzprüfung der Doku gegen Repo und Skripte**

```bash
for t in dev test test-race lint generate build release clean; do grep -q "^$t:" Makefile || echo "MISSING make target: $t"; done
for f in scripts/vm-setup.sh scripts/vm-test.sh .golangci.yml .goreleaser.yaml .github/workflows/ci.yml .github/workflows/release.yml; do test -f "$f" || echo "MISSING file: $f"; done
grep -q 'v2.14.0' .github/workflows/ci.yml && grep -q 'v2.14.0' docs/dev/README.md && echo LINT-VERSION-CONSISTENT
grep -q 'v2.18.2' .github/workflows/release.yml && grep -q 'v2.18.2' docs/dev/README.md && echo GORELEASER-VERSION-CONSISTENT
grep -q 'v1.8.0' .github/workflows/ci.yml && grep -q 'v1.8.0' docs/dev/README.md && echo GOVULNCHECK-VERSION-CONSISTENT
grep -q '2.54.2' .github/workflows/ci.yml && grep -q '2.54.2' docs/dev/README.md && echo REDOCLY-VERSION-CONSISTENT
grep -c 'mcpanel_jail4' scripts/vm-test.sh docs/dev/linux-vm.md
grep -c '192.0.2.2' scripts/vm-setup.sh scripts/vm-test.sh
```
Expected: keine `MISSING`-Zeile; vier `…-CONSISTENT`-Zeilen; beide `grep -c`-Aufrufe zählen in jeder Datei ≥ 1.

- [ ] **Step 4: Commit**

```bash
git add docs/dev/README.md docs/dev/linux-vm.md
git commit -m "docs: add developer guide and Linux VM instructions" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Abschlussprüfung des Streams

**Files:** keine neuen

- [ ] **Step 1: Gesamter Check**

```bash
.dev/tools/golangci-lint config verify && echo CONFIG-OK
.dev/tools/golangci-lint run ./... && GOOS=linux .dev/tools/golangci-lint run ./...
goreleaser check
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
shellcheck -x scripts/vm-setup.sh scripts/vm-test.sh && echo SHELLCHECK-OK
make test
git status --porcelain
git log --oneline phase0..HEAD
git diff --stat phase0..HEAD
```
Expected: `CONFIG-OK`; zweimal `0 issues.`; `goreleaser check` Exit 0; actionlint ohne Ausgabe; `SHELLCHECK-OK`; `make test` grün; `git status` leer; im Log nur die Commits dieses Plans; `git diff --stat` listet ausschließlich `.golangci.yml`, `.goreleaser.yaml`, `.github/…`, `scripts/vm-setup.sh`, `scripts/vm-test.sh`, `docs/dev/…` und – nur falls Task 1 Step 5 nötig war – reine Whitespace-Änderungen an Phase-0-Go-Dateien.

- [ ] **Step 2: Merge-Voraussetzung aus der Übersicht**

```bash
make lint
```
Expected: golangci-lint `0 issues.`, ESLint und `tsc` ohne Fehler. Damit ist der Branch `stream/f` bereit für den Merge (laut Übersicht als erster Stream).

## Self-Review

- **Spec-Abdeckung:**
  - §3.5 `make release` → Task 3 (GoReleaser, `before`-Hook `make web-build`, linux/amd64+arm64, SHA256). Linux-VM (Lima/OrbStack, Ubuntu LTS) → Task 6/7.
  - §10 CI → Task 4: `go test ./...` (`make test-go`), `golangci-lint` (Action, v2.14.0), `govulncheck`, `gosec` (in golangci-lint), `npm ci && npm test && npm run build`, `npm audit --audit-level=high`. Ergänzt um Race-Tests, sqlc- und OpenAPI-Typen-Drift, Redocly und nftables-Integration als root.
  - §12 Kriterium 1 → Task 1/2/8: Config besteht auf Phase-0-Code und Stream-Mustern mit `0 issues.`.
  - §12 Kriterium 5 (nftables-Teil) → `scripts/vm-test.sh`. Ausführbar ab Phase 2, als Phase-2-Verifikation festgelegt.
  - §12 Kriterium 7 → Task 3 (Binaries mit eingebettetem Frontend), `scripts/vm-setup.sh` (agent + panel starten in der VM).
- **Platzhalter:** keine. Alle Dateien sind vollständig angegeben. Bedingte Pfade (Befundtabellen Task 1/2, agent-Stub Task 3, Snapshot-Schritt in CI vor dem Stream-C-Merge) sind mit konkreten Aktionen festgelegt.
- **Konsistenz der Namen:** Archiv `mc-panel_{{ .Version }}_{{ .Os }}_{{ .Arch }}.tar.gz` in `.goreleaser.yaml`, CI-Check, `vm-setup.sh` und Doku. `checksums.txt` überall gleich. Set-Namen `inet mcpanel`/`mcpanel_jail4`/`mcpanel_jail6` wie in Spec §3.3. Units `mcpanel-agent`/`mcpanel-panel`, Namespace `mcpanel-client`, Adressen `192.0.2.1`/`192.0.2.2` und Origin `https://mcpanel.test` sind zwischen `vm-setup.sh`, `vm-test.sh` und `linux-vm.md` identisch.
- **Versionen:** Action-SHAs per `git ls-remote` aufgelöst (checkout v7.0.1 `3d3c42e…`, setup-go v7.0.0 `b7ad1da…`, setup-node v7.0.0 `8207627…`, golangci-lint-action v9.3.0 `ba0d7d2…` (dereferenzierter annotierter Tag), goreleaser-action v7.2.3 `f06c13b…`). golangci-lint v2.14.0 unterstützt go1.27 (seit v2.13.0). Alle Werkzeugversionen sind mindestens zwei Wochen alt.
- **Eigentum:** Keine Änderungen an `Makefile`, `go.mod`, `web/`, `cmd/`, `internal/`. Einzige Ausnahme ist die nachweislich reine Whitespace-Formatierung (Task 1 Step 5, `git diff -w --exit-code`). Probe-Paket und agent-Stub werden vor dem Commit entfernt und per `git status` geprüft.
- **Bewusste Abweichungen vom Auftrag:** (1) Das panel läuft in der VM im Prod-Modus mit `https`-`public_url` statt mit `-dev`, weil das panel kein TLS terminiert und `-dev` die Dev-Sonderwege (und beim agent die No-op-Firewall) aktivieren würde. (2) nftables-Tests laufen per `go test -exec sudo` statt `sudo -E env PATH=… go test`, damit keine root-eigenen Dateien in den Go-Caches landen. (3) Der Embed-Check sucht `/assets/index-` statt `index.html`, weil `net/http` das Literal `index.html` immer enthält (Negativtest in Task 3 Step 7).

## Wiring-Vertrag

**Release-Artefakte (für Phase 2, Teilprojekt 6 und Betrieb):**

| Artefakt | Name | Inhalt |
|---|---|---|
| Archiv amd64 | `mc-panel_<version>_linux_amd64.tar.gz` | `panel`, `agent` (Top-Level, Mode 0755, statisch, ohne CGO, gestrippt) |
| Archiv arm64 | `mc-panel_<version>_linux_arm64.tar.gz` | wie oben, `ARM aarch64` |
| Checksummen | `checksums.txt` | SHA-256 beider Archive, Format `<hex>  <dateiname>` |

- `<version>` = Git-Tag ohne `v` (Tag `v0.3.1` → `0.3.1`). Pre-Releases (`v0.3.1-rc.1`) sind auf GitHub als Pre-Release markiert und werden von `/releases/latest` nicht geliefert.
- Beide Binaries erhalten `-X main.version=<version>`. Snapshots: `<x.y.z+1>-snapshot+<shortsha>`.

**Download-Vertrag für den Installer (Teilprojekt 6):**

1. Version ermitteln: `GET https://api.github.com/repos/<owner>/<repo>/releases/latest` → `tag_name` (z. B. `v0.3.1`), `version = tag_name` ohne `v`. `<owner>/<repo>` ist der GitHub-Pfad, der beim Anlegen des Remotes festgelegt wird. Der Installer führt ihn als einzige Konstante.
2. Architektur: `uname -m` → `x86_64` ⇒ `amd64`, `aarch64` ⇒ `arm64`, alles andere ⇒ Abbruch.
3. Herunterladen: `https://github.com/<owner>/<repo>/releases/download/<tag_name>/mc-panel_<version>_linux_<arch>.tar.gz` und `…/<tag_name>/checksums.txt`.
4. Prüfen: `sha256sum --ignore-missing -c checksums.txt` muss für das Archiv `OK` melden, sonst Abbruch. Die Checksummen sichern die Integrität, nicht die Herkunft. Signaturen (z. B. cosign/Attestations) kann Teilprojekt 6 ergänzen.
5. Entpacken: `tar -xzf` liefert `panel` und `agent` direkt im Zielverzeichnis.

**Erwartungen an andere Streams (Grundlage für CI und VM):**

- **Stream C:** `cmd/agent/main.go` existiert (sonst überspringt der CI-`build`-Job den Snapshot mit Warnung, und `make release` schlägt fehl). Es deklariert `var version = "dev"` (befüllt per ldflags, Ausgabe in `HealthResponse.Version`), akzeptiert `-config <path>` (TOML gemäß `config.LoadAgent`) und legt beim Start idempotent `table inet mcpanel` mit den Sets `mcpanel_jail4`/`mcpanel_jail6` an. Root-pflichtige Integrationstests liegen unter `internal/agent/...` mit `//go:build linux && nftintegration` und laufen in CI als `go test -tags nftintegration -exec sudo -count=1 ./internal/agent/...`. Der Socket wird mit Mode `0660` angelegt, und zwar als Literal `0o660` direkt im `os.Chmod`-Aufruf. Nur dann greift die gosec-G302-Ausnahme (`source: "0o?660"`); eine benannte Konstante bräuchte ein `//nolint:gosec // G302: …`. Die Socket-Gruppe ist die primäre Gruppe von `panel_uid` (so plant es Stream C); in der VM ergibt das `root:mcpanel`.
- **Alle Go-Streams:** müssen vor dem Merge `make lint` mit dieser `.golangci.yml` bestehen. Ausnahmen nur gezielt (siehe `docs/dev/README.md`, Lint-Konventionen). Geänderte sqlc-Queries werden mit generiertem Code committet; CI prüft das mit der sqlc-Version aus dem Header.
- **Stream E:** `npm run gen:api` und `npm run build` dürfen nach einem sauberen Checkout keine Änderungen an versionierten Dateien unter `web/` erzeugen (z. B. `src/api/schema.d.ts`, `src/routeTree.gen.ts`), sonst schlägt der `web`-Job fehl. `npm audit --audit-level=high` muss grün sein.

**Phase 2 (Lead) baut darauf auf:**

- `cmd/panel/main.go` deklariert `var version = "dev"` und gibt die Version aus (z. B. Subcommand `panel version`). Die ldflags setzen sie bereits; ohne Deklaration ist `-X` wirkungslos.
- Playwright-E2E (Spec §10) als zusätzlicher Job in `.github/workflows/ci.yml`. Nach Phase 1 geht das Eigentum an `.github/` an den Lead.
- **Phase-2-Verifikation in der VM (Abnahmekriterien 5 und 7):**

```bash
make clean && make release
scripts/vm-setup.sh --no-build
scripts/vm-test.sh
grep -rn 'mcpanel_jail4' internal/agent | head -n 3
```
Expected: `vm-setup.sh` endet mit `{"status":"ok"}`, `socket: 660 root:mcpanel …`, zweimal `active`, `nftables: table inet mcpanel present`. `vm-test.sh` zeigt fünfmal `HTTP 401`, dann das Set mit `192.0.2.2` und endet mit `PASS: acceptance check 5 (nftables part)`. `grep` findet den Set-Namen im agent-Code, also passen VM-Test und Implementierung zusammen.
