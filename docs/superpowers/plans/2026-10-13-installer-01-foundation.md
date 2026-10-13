# mc-panel Teilprojekt 6: Installer – Phase A: Fundament (Aufgaben 1–3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Alles, worauf die Phasen B1 (Schritte von `install`), B2 (`update`/`doctor`/`uninstall`) und C (Test-VM, CI-Job, Doku) aufbauen: Socket-Aktivierung für das panel und `panel admin list` (Go), die Vorlagen unter `deploy/`, die Bash-Bibliothek `lib.sh`, `main.sh` mit Argumenten, Unterbefehlen und Stubs für alle Schritte, ein Test-Harness ohne Fremdwerkzeug, das Zusammenfügen zu einem einzigen `install.sh` und dessen Veröffentlichung als Release-Asset.

**Architecture:** `internal/unixsock.Activated` übernimmt den bisher privaten `activatedListener` aus `cmd/files`; das panel nutzt ihn für `/run/mc-panel-web/panel.sock`. Der Installer besteht aus `scripts/install/lib.sh` (Konstanten, Helfer, `sys_*`-Wrapper mit Dry-Run), Schritt-Dateien (in Phase A nur Stubs) und `main.sh`; `assemble.sh` fügt sie mit den als Funktionen eingebetteten Vorlagen aus `deploy/` zu `build/install.sh` zusammen. `scripts/install/test.sh` führt die Bash-Tests unter `scripts/install/tests/` in Subshells mit eigenem `ROOT` im Dry-Run aus.

**Tech Stack:** Go (Standardbibliothek), Bash 5 (Ausführung) bzw. Bash 3.2 (nur `bash -n` und `assemble.sh` auf dem Mac), coreutils, awk, sed, shellcheck (CI/VM), goreleaser v2.18.2.

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-installer-design.md` · **Übersicht/Verträge:** `docs/superpowers/plans/2026-10-13-installer-00-overview.md` (V1–V5, Global Constraints, Dateieigentum)

## Global Constraints

Es gelten alle Global Constraints der Übersicht; zusätzlich für diese Phase:

- `scripts/install/*.sh` und `scripts/install/tests/*.sh` müssen `bash -n` unter **Bash 3.2** bestehen (`make check` läuft auf dem Mac mit `/bin/bash` 3.2): keine `;&`/`;;&`, kein `|&`, kein `coproc`, kein `[[ -v … ]]`; Bash-4-Builtins wie `mapfile` sind erlaubt (das ist kein Syntaxfehler), werden aber nur im Laufzeitcode genutzt, der auf Ubuntu läuft. `assemble.sh` läuft auch auf dem Mac und nutzt nur Bash-3.2-Funktionen.
- `MCPANEL_INSTALL_ROOT` (Tests) ist die einzige Quelle für `ROOT`; eine Umgebungsvariable `ROOT` des Betreibers wird nie gelesen.
- Ein abgebrochener Download (`curl | bash`) darf nie Teile ausführen: Das zusammengefügte Skript definiert bis zur letzten Zeile nur Funktionen und Variablen; erst die letzte Zeile ruft `main`.
- Commit-Nachrichten enden mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>` und `Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`.

## Abweichungen von der Übersicht (V1–V5), hier verbindlich

1. **`public_url` (V3, so auch im Vertrag):** Der Panel-Parser kennt kein `origin`; die Vorlage setzt `public_url = "{{PUBLIC_URL}}"` aus `KEY_PUBLIC_URL`.
2. **Ausgabe `build/install.sh` statt `dist/install.sh` (V5):** `dist/` gehört goreleaser und wird mit `--clean` geleert. `build/` kommt in `.gitignore`.
3. **Zusätzliche Dateien in Phase A:** `scripts/install/.shellcheckrc` (`source-path=SCRIPTDIR`, `external-sources=true`), `scripts/install/tests/lib_test.sh`, Stub-Dateien aller Schritte (die B1/B2 danach ersetzen), `internal/config/deploy_test.go` (prüft die TOML-Vorlagen gegen die echten Parser), `.gitignore`, `.github/workflows/release.yml` (der Release-Workflow läuft mit `--skip=before` und muss `make installer` selbst aufrufen) und `scripts/vm-test.sh` (Lint-Suite: shellcheck + `test.sh`).
4. **`admin list` in `cmd/panel`:** Die Funktion liegt in `cmd/panel/admin_list.go` (nutzt die vorhandene Query `RbacListUsers`), nicht im Paket `users` – so bleibt Phase A in ihrem Dateieigentum.
5. **Zusätzliche `lib.sh`-Wrapper (V2):** `sys_chown` und `sys_runuser` (genutzt von `write_file`/`ensure_dir` bzw. `run_as`), damit Tests als normaler Benutzer laufen; `reset_globals` setzt alle globalen Variablen auf ihre Defaults; `_owner_get`/`_owner_set` führen im Trockenlauf Besitz und Modus in `$WORK/owners`.
6. **Firewall-Unit `Before=network-pre.target` (Spec §4.8 sagt `After=`):** Eine Firewall muss vor dem Netz geladen sein; `Wants=network-pre.target` + `Before=network-pre.target` ist die systemd-Konvention dafür.
7. **`--tls internal` ohne `--domain`:** `DOMAIN` fällt auf `localhost` zurück (mit Warnung); Caddy stellt dafür ein internes Zertifikat aus.
8. **Vorlagen-Rendering verliert Leerzeilen am Dateiende** (Kommandosubstitution); Vorlagen enden mit genau einem Zeilenumbruch, `assemble.sh` prüft das.

## Nachträge nach dem Schnittstellen-Review (verbindlich)

- `main` führt jeden Befehl in einer Subshell mit `set -e` außerhalb eines Bedingungskontexts aus (`set +e; ( set -e; "cmd_$CMD" ); rc=$?; set -e`), damit `set -e` in allen Schritten greift; `cmd_doctor` liefert seinen Code weiterhin per `return`.
- Mit `--from-dir` setzt `validate_args` `TARGET_VERSION` aus `"$FROM_DIR/panel" version` (ohne `v`, ohne Netz); `release_tag VERSION` liefert `v<version>` für Release-URLs (B2).
- Harness zusätzlich: `assert_fails`, `assert_file`, `assert_no_file`, `fake_exec`; `test.sh` nimmt eine Testdatei oder ein Namensmuster. `assert_called`/`assert_not_called` vergleichen Festtext mit den echten `calls.log`-Zeilen (`systemctl …`, `apt-get …`), B1/B2 passen ihre Muster daran an.
- `ROLLBACK_DIR=/var/lib/mc-panel-rollback` (`root:root 0700`).
- `doctor` läuft ohne root mit eingeschränkten Prüfungen (V4).

## Review Focus (diese Phase)

- **Integrität (Übersicht Punkt 4):** `fetch` lehnt Nicht-HTTPS ab, auch bei Weiterleitungen; `sha256_verify` bricht bei fehlender, doppelter, fehlerhafter oder abweichender Zeile ab. → Aufgabe 2, Tests `test_fetch_*`, `test_sha256_verify_*`.
- **Fremde Inhalte (Punkt 2):** `toml_ensure_key` ändert nie vorhandene Werte und fügt Schlüssel vor dem ersten Abschnitt ein; `write_file` ändert nichts, wenn Inhalt, Modus und Besitzer stimmen. → Aufgabe 2.
- **Teil-Download:** Das zusammengefügte Skript ohne letzte Zeile ruft nichts auf. → Aufgabe 3, `test_assemble_partial_runs_nothing`.

## Dateistruktur dieser Phase

```
internal/unixsock/activation.go, activation_test.go      (1)  Activated (aus cmd/files verschoben)
cmd/files/main.go, main_test.go                          (1)  nutzt unixsock.Activated
cmd/panel/main.go, listen_test.go                        (1)  Socket-Aktivierung für listen = unix:…
cmd/panel/admin_list.go, admin_list_test.go              (1)  panel admin list
deploy/systemd/*.service, *.socket                       (2)  Unit-Vorlagen
deploy/caddy/mc-panel.caddy                              (2)  Caddy-Vorlage
deploy/nftables/mcpanel_fw.nft                           (2)  Firewall-Vorlage
deploy/config/panel.toml, agent.toml, files.toml         (2)  Konfigurations-Vorlagen
internal/config/deploy_test.go                           (2)  Vorlagen gegen die Parser
scripts/install/lib.sh, main.sh, .shellcheckrc           (2)  Bibliothek, Einstieg
scripts/install/{preflight,packages,layout,binaries,config,units,caddy,firewall,upgrades,admin,update,doctor,uninstall}.sh (2) Stubs
scripts/install/test.sh, tests/lib_test.sh               (2)  Harness, Tests von lib.sh und main.sh
scripts/install/assemble.sh, tests/assemble_test.sh      (3)  Zusammenfügen
Makefile, .gitignore, .goreleaser.yaml                   (3)  make installer, check, Release-Asset
.github/workflows/ci.yml, release.yml, scripts/vm-test.sh (3) Lint/Build-Job, Release, VM-Lint
```

---

### Task 1: Socket-Aktivierung für das panel und `panel admin list`

**Files:**
- Create: `internal/unixsock/activation.go`, `internal/unixsock/activation_test.go`
- Modify: `cmd/files/main.go` (Funktion `activatedListener` entfernen, `unixsock.Activated` nutzen), `cmd/files/main_test.go` (`TestActivatedListener` entfernen)
- Modify: `cmd/panel/main.go` (`listen`, `usageText`, `run`, `runCLI`)
- Create: `cmd/panel/admin_list.go`, `cmd/panel/admin_list_test.go`
- Modify: `cmd/panel/listen_test.go`

**Interfaces:**
- Produces: `func unixsock.Activated(getenv func(string) string, unsetenv func(string) error, pid int) (net.Listener, error)` (V1); `panel admin list [-config path] [-dev]` – eine Zeile pro Admin-Benutzername, sortiert, Exit 0 (V1).
- Consumes: `store.Queries.RbacListUsers(ctx) ([]store.User, error)` (vorhanden, `ORDER BY username`), `config.Panel.ListenUnix() (string, bool)` (vorhanden).

- [ ] **Step 1: Test für `unixsock.Activated` schreiben**

`internal/unixsock/activation_test.go` (übernimmt die Fälle aus `cmd/files/main_test.go:TestActivatedListener` und ergänzt die echte Übergabe über einen Kindprozess):

```go
//go:build unix

package unixsock

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestActivatedEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	var unset []string
	unsetenv := func(k string) error { unset = append(unset, k); return nil }

	l, err := Activated(env(nil), unsetenv, 42)
	if l != nil || err != nil {
		t.Fatalf("no activation: %v %v", l, err)
	}
	if len(unset) != 3 {
		t.Fatalf("activation variables not removed: %v", unset)
	}
	l, err = Activated(env(map[string]string{"LISTEN_PID": "7", "LISTEN_FDS": "1"}), unsetenv, 42)
	if l != nil || err != nil {
		t.Fatalf("activation for another pid must be ignored: %v %v", l, err)
	}
	if _, err := Activated(env(map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "2"}), unsetenv, 42); err == nil {
		t.Fatal("two sockets accepted")
	}
	if _, err := Activated(env(map[string]string{"LISTEN_PID": "42", "LISTEN_FDS": "x"}), unsetenv, 42); err == nil {
		t.Fatal("malformed LISTEN_FDS accepted")
	}
}

// TestActivatedHandover passes a real unix socket as fd 3 to a child process (this test
// binary, re-executed), which must accept on it – the path systemd takes.
func TestActivatedHandover(t *testing.T) {
	if os.Getenv("UNIXSOCK_CHILD") == "1" {
		l, err := Activated(os.Getenv, os.Unsetenv, os.Getpid())
		if err != nil || l == nil {
			os.Exit(3)
		}
		c, err := l.Accept()
		if err != nil {
			os.Exit(4)
		}
		_, _ = c.Write([]byte("ok"))
		_ = c.Close()
		os.Exit(0)
	}
	dir, err := os.MkdirTemp("", "act") // short path (macOS: 104-byte socket path limit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ul, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := ul.File()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestActivatedHandover$") //nolint:gosec // G204: re-executes this test binary
	cmd.ExtraFiles = []*os.File{f}                                       // becomes fd 3
	cmd.Env = append(os.Environ(), "UNIXSOCK_CHILD=1", "LISTEN_FDS=1")
	// LISTEN_PID must be the child's pid, which is unknown before Start: the child reads it
	// from its own environment, so set it through a wrapper variable the child copies.
	cmd.Env = append(cmd.Env, "LISTEN_PID=self")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Process // pid known now, but the environment is already passed
	t.Skip("replaced in step 3 by the LISTEN_PID=self handling below")
	_ = strconv.Itoa
}
```

Der Kindprozess kann `LISTEN_PID` nicht vorab kennen; deshalb bekommt `Activated` im Test einen `getenv`-Wrapper. Ersetze den Schluss von `TestActivatedHandover` ab `cmd.Env = append(cmd.Env, "LISTEN_PID=self")` durch die endgültige Fassung:

```go
	cmd.Env = append(cmd.Env, "LISTEN_PID=child")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := c.Read(buf); err != nil || string(buf) != "ok" {
		t.Fatalf("child did not answer on the handed-over socket: %q %v", buf, err)
	}
	_ = c.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v", err)
	}
	_ = f.Close()
	_ = ul.Close()
}
```

und im Kindzweig oben die erste Zeile durch

```go
		getenv := func(k string) string {
			if k == "LISTEN_PID" && os.Getenv(k) == "child" {
				return strconv.Itoa(os.Getpid())
			}
			return os.Getenv(k)
		}
		l, err := Activated(getenv, os.Unsetenv, os.Getpid())
```

(der `t.Skip`-Block und `_ = cmd.Process`/`_ = strconv.Itoa` entfallen; `strconv` wird jetzt im Kindzweig gebraucht).

- [ ] **Step 2: Rot prüfen**

Run: `make check`
Expected: FAIL – `undefined: Activated`.

- [ ] **Step 3: `Activated` implementieren**

`internal/unixsock/activation.go`:

```go
//go:build unix

package unixsock

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
)

// activationFD is SD_LISTEN_FDS_START: systemd passes the first socket as fd 3.
const activationFD = 3

// Activated returns the listener systemd passed as fd 3 when LISTEN_PID is pid and
// LISTEN_FDS is 1, otherwise nil. It always unsets LISTEN_PID, LISTEN_FDS and
// LISTEN_FDNAMES, so they never leak into child processes. Used by the files service
// (mcpanel-files.socket) and the panel (mcpanel-panel.socket).
func Activated(getenv func(string) string, unsetenv func(string) error, pid int) (net.Listener, error) {
	lpid, lfds := getenv("LISTEN_PID"), getenv("LISTEN_FDS")
	for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		_ = unsetenv(k)
	}
	if lpid == "" && lfds == "" {
		return nil, nil
	}
	if lpid != strconv.Itoa(pid) {
		return nil, nil // meant for another process
	}
	if n, err := strconv.Atoi(lfds); err != nil || n != 1 {
		return nil, fmt.Errorf("socket activation: expected exactly one socket, got LISTEN_FDS=%q", lfds)
	}
	syscall.CloseOnExec(activationFD)
	f := os.NewFile(activationFD, "activated.sock")
	l, err := net.FileListener(f)
	_ = f.Close() // FileListener holds its own duplicate
	if err != nil {
		return nil, fmt.Errorf("socket activation: %w", err)
	}
	if _, ok := l.(*net.UnixListener); !ok {
		_ = l.Close()
		return nil, errors.New("socket activation: fd 3 is not a Unix socket")
	}
	return l, nil
}
```

In `cmd/files/main.go`: die Konstante `activationFD` und die Funktion `activatedListener` (samt Kommentar) löschen; in `listen` die erste Zeile durch

```go
	l, err := unixsock.Activated(os.Getenv, os.Unsetenv, os.Getpid())
```

ersetzen. Nicht mehr gebrauchte Imports (`strconv`, ggf. `syscall` – `syscall.SIGTERM` in `main` bleibt) entfernen. In `cmd/files/main_test.go` `TestActivatedListener` löschen (die Fälle stehen jetzt in `internal/unixsock`).

- [ ] **Step 4: Test für die Panel-Seite schreiben**

In `cmd/panel/listen_test.go` anfügen (die Logik wird über eine injizierbare Funktion getestet, damit kein echter fd 3 nötig ist):

```go
func TestListenPrefersActivatedSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "act")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "panel.sock")
	cfg := config.DefaultPanel()
	cfg.Listen = "unix:" + sock

	activated, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	l, err := listenWith(context.Background(), cfg, func() (net.Listener, error) { return activated, nil })
	if err != nil || l != activated {
		t.Fatalf("activated socket not used: %v %v", l, err)
	}
	_ = l.Close()

	other, err := net.Listen("unix", filepath.Join(dir, "other.sock"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := listenWith(context.Background(), cfg, func() (net.Listener, error) { return other, nil }); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("socket for another path accepted: %v", err)
	}

	boom := errors.New("boom")
	if _, err := listenWith(context.Background(), cfg, func() (net.Listener, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("activation error not returned: %v", err)
	}

	// Without activation the panel creates the socket itself, as before.
	l, err = listenWith(context.Background(), cfg, func() (net.Listener, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(sock)
	if err != nil || fi.Mode().Type() != fs.ModeSocket || fi.Mode().Perm() != 0o660 {
		t.Fatalf("own socket: %v %v", fi, err)
	}
	_ = l.Close()
}

func TestListenTCPIgnoresActivation(t *testing.T) {
	cfg := config.DefaultPanel()
	cfg.Listen = "127.0.0.1:0"
	called := false
	l, err := listenWith(context.Background(), cfg, func() (net.Listener, error) { called = true; return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	if called {
		t.Fatal("activation consulted for a TCP listen address")
	}
}
```

Imports in `listen_test.go` um `errors` ergänzen (`net`, `fs`, `os`, `filepath`, `strings`, `config` sind schon da).

- [ ] **Step 5: Rot prüfen**

Run: `make check`
Expected: FAIL – `undefined: listenWith`.

- [ ] **Step 6: `listen` im panel umbauen**

In `cmd/panel/main.go` die Funktion `listen` ersetzen:

```go
// listen opens the configured listener: "unix:/path" (production default, behind the reverse
// proxy) or host:port. For a unix path, a socket handed over by systemd (mcpanel-panel.socket,
// installer spec §4.6) wins: systemd owns path, owner caddy group and mode, so the panel needs
// no membership in the proxy's group. Without activation the panel creates the socket itself
// (a stale socket is replaced only if nobody accepts on it, mode 0660).
func listen(ctx context.Context, cfg config.Panel) (net.Listener, error) {
	return listenWith(ctx, cfg, func() (net.Listener, error) {
		return unixsock.Activated(os.Getenv, os.Unsetenv, os.Getpid())
	})
}

func listenWith(ctx context.Context, cfg config.Panel, activated func() (net.Listener, error)) (net.Listener, error) {
	path, ok := cfg.ListenUnix()
	if !ok {
		return (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	}
	l, err := activated()
	if err != nil {
		return nil, err
	}
	if l != nil {
		if got := l.Addr().String(); got != path {
			_ = l.Close()
			return nil, fmt.Errorf("socket activation: socket %q does not match listen %q", got, cfg.Listen)
		}
		slog.Info("using the socket passed by systemd", "socket", path)
		return l, nil
	}
	return unixsock.Listen(path, 0o660, -1)
}
```

- [ ] **Step 7: Test für `admin list` schreiben**

`cmd/panel/admin_list_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func TestRunAdminList(t *testing.T) {
	db := storetest.Open(t)
	storetest.CreateUser(t, db, "bob", "admin")
	storetest.CreateUser(t, db, "alice", "admin")
	storetest.CreateUser(t, db, "carol", "user")
	var out bytes.Buffer
	if err := runAdminList(context.Background(), db, &out); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "alice\nbob\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestRunAdminListEmpty(t *testing.T) {
	db := storetest.Open(t)
	storetest.CreateUser(t, db, "carol", "user")
	var out bytes.Buffer
	if err := runAdminList(context.Background(), db, &out); err != nil || out.Len() != 0 {
		t.Fatalf("no admins: %q %v", out.String(), err)
	}
}

func TestAdminListCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out, errOut bytes.Buffer
	if code := run([]string{"admin", "list", "-dev"}, f, &out, &errOut); code != 0 || out.Len() != 0 {
		t.Fatalf("admin list on an empty dev DB: code %d, out %q, err %q", code, out.String(), errOut.String())
	}
	if code := run([]string{"admin", "list", "-dev", "extra"}, f, &out, &errOut); code != 1 ||
		!strings.Contains(errOut.String(), "unexpected arguments") {
		t.Fatalf("extra argument: code %d, err %q", code, errOut.String())
	}
}

func TestUsageMentionsAdminList(t *testing.T) {
	if !strings.Contains(usageText, "panel admin list") {
		t.Fatal("usage does not mention admin list")
	}
}
```

(`storetest.CreateUser(t, db, username, role string) int64` ist vorhanden.)

- [ ] **Step 8: Rot prüfen**

Run: `make check`
Expected: FAIL – `undefined: runAdminList`.

- [ ] **Step 9: `admin list` implementieren**

`cmd/panel/admin_list.go`:

```go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// runAdminList prints the username of every admin, one per line, sorted (installer spec
// §4.10: install.sh asks for a first admin only when this prints nothing). Read-only.
func runAdminList(ctx context.Context, db *sql.DB, w io.Writer) error {
	users, err := store.New(db).RbacListUsers(ctx) // ORDER BY username
	if err != nil {
		return fmt.Errorf("admin list: %w", err)
	}
	for _, u := range users {
		if u.Role == string(principal.RoleAdmin) {
			if _, err := fmt.Fprintln(w, u.Username); err != nil {
				return err
			}
		}
	}
	return nil
}
```

In `cmd/panel/main.go`:

- `usageText`: nach der Zeile `panel admin unlock …` die Zeile `  panel admin list   [-config path] [-dev]` einfügen.
- `run`: im `switch cmd` den Fall `case "admin create", "admin unlock", "audit verify":` um `"admin list"` erweitern.
- `runCLI`: vor `case "audit verify":` einfügen:

```go
	case "admin list":
		if len(rest) > 0 {
			return fmt.Errorf("unexpected arguments: %v", rest)
		}
		return runAdminList(ctx, db, stdout)
```

- [ ] **Step 10: Grün prüfen**

Run: `make check`, dann `make test-vm SUITE=go`
Expected: PASS (inkl. `internal/unixsock`, `cmd/files`, `cmd/panel`).

- [ ] **Step 11: Commit**

```bash
git add internal/unixsock cmd/files cmd/panel
git commit -m "feat(panel): socket activation for the panel socket and panel admin list

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 2: Vorlagen, `lib.sh`, `main.sh`, Stubs und Test-Harness

**Files:**
- Create: `deploy/systemd/mcpanel-agent.service`, `mcpanel-files.socket`, `mcpanel-files.service`, `mcpanel-panel.socket`, `mcpanel-panel.service`, `mcpanel-fw.service`
- Create: `deploy/caddy/mc-panel.caddy`, `deploy/nftables/mcpanel_fw.nft`, `deploy/config/panel.toml`, `deploy/config/agent.toml`, `deploy/config/files.toml`
- Create: `internal/config/deploy_test.go`
- Create: `scripts/install/.shellcheckrc`, `scripts/install/lib.sh`, `scripts/install/main.sh`
- Create (Stubs): `scripts/install/preflight.sh`, `packages.sh`, `layout.sh`, `binaries.sh`, `config.sh`, `units.sh`, `caddy.sh`, `firewall.sh`, `upgrades.sh`, `admin.sh`, `update.sh`, `doctor.sh`, `uninstall.sh`
- Create: `scripts/install/test.sh`, `scripts/install/tests/lib_test.sh`

**Interfaces:**
- Produces (für B1/B2/C, V2–V4): alle Funktionen und Variablen aus V2 mit genau diesen Namen, zusätzlich `ensure_dir PATH MODE OWNER GROUP` (anlegen/korrigieren, Ausgabe `changed`/`unchanged`), `sys_useradd`, `sys_userdel`, `sys_chown`, `sys_runuser`, `reset_globals`, `tpl_func_suffix`, `need_value`; Ausgabeformat der Schritte auf stdout: `[ok] …`, `[changed] …`, `[skip] …`, `[warn] …` (die CI prüft darauf); globale Variablen zusätzlich `ADMIN_PASSWORD_FILE`, `DOC_SUMMARY` (0/1); in `main.sh`: `usage`, `parse_args`, `validate_args`, `require_root`, `detect_arch`, `setup_work`, `setup_log`, `cmd_install`, `install_summary`, `main`. Schritt-Funktionen (Stubs, B1/B2 ersetzen): `pre_check`, `pkg_install`, `lay_ensure`, `bin_install`, `cfg_ensure`, `unit_ensure`, `cad_ensure`, `fw_ensure`, `upg_ensure`, `adm_ensure`, `cmd_update`, `cmd_doctor`, `cmd_uninstall`.
- Test-Harness (für B1/B2-Tests): Dateien `scripts/install/tests/*.sh` definieren Funktionen `test_*` (automatisch entdeckt); verfügbar sind `fail MSG…`, `assert_eq EXP ACT [MSG]`, `assert_contains HAY NEEDLE`, `assert_not_contains HAY NEEDLE`, `assert_file_eq FILE EXP` (Dateiinhalt ohne letzten Zeilenumbruch), `assert_file_contains FILE NEEDLE`, `assert_exit CODE CMD…` (führt CMD in einer Subshell aus, Ausgabe in `$WORK/out`), `assert_called PATTERN` / `assert_not_called PATTERN` (feste Zeichenkette gegen `$WORK/calls.log`), `stub NAME BODY` (definiert für diesen Test eine Funktion NAME mit dem Rumpf BODY; Funktionen haben Vorrang vor Befehlen – so werden reine Abfragen wie `id`, `ss`, `ip`, `getent`, `dpkg-query`, `sshd`, `restic`, `curl`, `sqlite3`, `findmnt`, `openssl`, `userdel` ersetzt; im Rumpf sind `$1`, `$@` die Argumente). Besitz/Modus im Trockenlauf: `write_file`, `ensure_dir` und `sys_chown` schreiben Zeilen `pfad owner group mode` nach `$WORK/owners` (letzte Zeile je Pfad gilt), `_owner_get PATH` liest sie. Jeder Test läuft in einer eigenen Subshell mit `set -e`, frischem `ROOT=$(mktemp -d)`, `WORK=$ROOT/.work`, `DRYRUN=1`, Default-Globals. Ausgabe TAP-ähnlich, Exit ≠ 0 bei Fehlern.
- Platzhalter (V3): `{{DOMAIN}}`, `{{TLS}}`, `{{PANEL_UID}}`, `{{PUBLIC_URL}}`, `{{INSTANCES_DIR}}`, `{{SSH_PORT}}` (nft-Liste, z. B. `22, 2222`), `{{VERSION}}`, `{{RESTIC_BINARY}}`, `{{PING_ADDR}}` aus `KEY_*`. Für B1 verbindlich, nicht Teil der Vorlagen: `agent.toml` wird `root:root 0600` geschrieben, die Firewall-Datei nach `/etc/mc-panel/mcpanel_fw.nft`, die Caddy-Import-Zeile lautet genau `import mc-panel.caddy # managed by mc-panel`.

- [ ] **Step 1: Vorlagen anlegen**

Die Units entsprechen `scripts/testvm-deploy.sh` mit den TP4-Werten (Stand nach #56) plus Panel-Socket und Firewall. Jede verwaltete Datei trägt einen Kopf; Konfigurationsdateien einen anderen (sie werden nur angelegt).

`deploy/systemd/mcpanel-agent.service`:

```ini
# Managed by mc-panel install.sh {{VERSION}}; install.sh update rewrites this file.
[Unit]
Description=mc-panel agent (nftables, Docker)
Wants=docker.service
After=network-online.target docker.service

[Service]
User=root
Group=mcpanel
RuntimeDirectory=mc-panel
RuntimeDirectoryMode=0750
ExecStart=/usr/local/bin/agent -config /etc/mc-panel/agent.toml
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
```

`deploy/systemd/mcpanel-files.socket`:

```ini
# Managed by mc-panel install.sh {{VERSION}}; install.sh update rewrites this file.
[Unit]
Description=mc-panel files service socket

[Socket]
ListenStream=/run/mc-panel-files/files.sock
SocketUser=mcpanel-mc
SocketGroup=mcpanel
SocketMode=0660
DirectoryMode=0755
RemoveOnStop=yes

[Install]
WantedBy=sockets.target
```

`deploy/systemd/mcpanel-files.service`:

```ini
# Managed by mc-panel install.sh {{VERSION}}; install.sh update rewrites this file.
[Unit]
Description=mc-panel files service (unprivileged, Minecraft data)
Requires=mcpanel-files.socket
Wants=mcpanel-agent.service
After=network-online.target mcpanel-files.socket mcpanel-agent.service

[Service]
User=mcpanel-mc
Group=mcpanel-mc
ExecStart=/usr/local/bin/files -config /etc/mc-panel-files/files.toml
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
ProtectSystem=strict
ReadWritePaths={{INSTANCES_DIR}}/instances {{INSTANCES_DIR}}/backups
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
RestrictAddressFamilies=AF_UNIX
SystemCallArchitectures=native
UMask=0027
MemoryMax=1G
TasksMax=512
IOWeight=50
CPUWeight=50

[Install]
WantedBy=multi-user.target
```

`deploy/systemd/mcpanel-panel.socket`:

```ini
# Managed by mc-panel install.sh {{VERSION}}; install.sh update rewrites this file.
# Installer spec §4.6: systemd owns the panel socket so Caddy (group caddy) can connect
# without being a member of the group mcpanel; the panel takes it over via LISTEN_FDS.
[Unit]
Description=mc-panel web panel socket (reverse proxy)

[Socket]
ListenStream=/run/mc-panel-web/panel.sock
SocketUser=mcpanel
SocketGroup=caddy
SocketMode=0660
DirectoryMode=0755
RemoveOnStop=yes

[Install]
WantedBy=sockets.target
```

`deploy/systemd/mcpanel-panel.service`:

```ini
# Managed by mc-panel install.sh {{VERSION}}; install.sh update rewrites this file.
[Unit]
Description=mc-panel web panel
Requires=mcpanel-panel.socket
Wants=mcpanel-agent.service mcpanel-files.socket
After=network-online.target mcpanel-panel.socket mcpanel-agent.service mcpanel-files.socket

[Service]
User=mcpanel
Group=mcpanel
StateDirectory=mc-panel
StateDirectoryMode=0700
ExecStart=/usr/local/bin/panel serve -config /etc/mc-panel/panel.toml
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

`deploy/systemd/mcpanel-fw.service`:

```ini
# Managed by mc-panel install.sh {{VERSION}}; install.sh update rewrites this file.
# Host firewall of mc-panel (installer spec §4.8): its own nftables table inet mcpanel_fw,
# never the agent's table inet mcpanel.
[Unit]
Description=mc-panel host firewall (nftables table inet mcpanel_fw)
DefaultDependencies=no
Wants=network-pre.target
Before=network-pre.target shutdown.target
Conflicts=shutdown.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /etc/mc-panel/mcpanel_fw.nft
ExecStop=/usr/sbin/nft delete table inet mcpanel_fw

[Install]
WantedBy=multi-user.target
```

`deploy/nftables/mcpanel_fw.nft` (die ersten zwei Anweisungen machen das Laden idempotent: Tabelle anlegen, falls sie fehlt, löschen und neu definieren – `nft -f` wendet die Datei atomar an; `KEY_SSH_PORT` ist eine nft-Liste wie `22, 2222`):

```
# Managed by mc-panel install.sh {{VERSION}}; install.sh update rewrites this file.
table inet mcpanel_fw {}
delete table inet mcpanel_fw
table inet mcpanel_fw {
	chain input {
		type filter hook input priority 0; policy drop;
		iif "lo" accept
		ct state established,related accept
		ct state invalid drop
		meta l4proto icmp accept
		meta l4proto ipv6-icmp accept
		tcp dport { {{SSH_PORT}} } accept
		tcp dport { 80, 443, 25565 } accept
		udp dport 443 accept
	}
}
```

`deploy/caddy/mc-panel.caddy`:

```
# Managed by mc-panel install.sh {{VERSION}}; install.sh update rewrites this file.
# Imported from /etc/caddy/Caddyfile (line "import mc-panel.caddy"); other sites stay there.
{{DOMAIN}} {
	{{TLS}}
	reverse_proxy unix//run/mc-panel-web/panel.sock
}
```

`deploy/config/panel.toml`:

```toml
# Created by mc-panel install.sh {{VERSION}}. Later runs only add missing keys; your edits stay.
listen = "unix:/run/mc-panel-web/panel.sock"
public_url = "{{PUBLIC_URL}}"
data_dir = "/var/lib/mc-panel"
agent_socket = "/run/mc-panel/agent.sock"
files_socket = "/run/mc-panel-files/files.sock"
trusted_proxies = ["127.0.0.1/32", "::1/128"]
monitoring_ping_addr = "{{PING_ADDR}}"
instances_dir = "{{INSTANCES_DIR}}"
```

`deploy/config/agent.toml`:

```toml
# Created by mc-panel install.sh {{VERSION}}. Later runs only add missing keys; your edits stay.
socket = "/run/mc-panel/agent.sock"
panel_uid = {{PANEL_UID}}
firewall = "nftables"
instances_dir = "{{INSTANCES_DIR}}"
mc_user = "mcpanel-mc"
```

`deploy/config/files.toml`:

```toml
# Created by mc-panel install.sh {{VERSION}}. Later runs only add missing keys; your edits stay.
# socket is only used without systemd socket activation (mcpanel-files.socket hands it over).
socket = "/run/mc-panel-files/files.sock"
panel_uid = {{PANEL_UID}}
instances_dir = "{{INSTANCES_DIR}}"
restic_binary = "{{RESTIC_BINARY}}"
```

Hinweis: `monitoring_ping_addr` und `instances_dir` in `panel.toml` gibt es ab TP5 (Phase A von TP5 ist gemerged, bevor diese Phase startet). Prüfe das in `internal/config/config.go`; fehlt einer, melde es dem Lead statt die Zeile zu streichen.

- [ ] **Step 2: Go-Test der TOML-Vorlagen schreiben**

`internal/config/deploy_test.go` – stellt sicher, dass jede Vorlage mit Beispielwerten von den echten Parsern ohne unbekannte Schlüssel angenommen wird (die Parser lehnen unbekannte Schlüssel ab):

```go
package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// renderDeploy substitutes the installer placeholders (installer plan V3) with sample values.
func renderDeploy(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "deploy", "config", name))
	if err != nil {
		t.Fatal(err)
	}
	vals := map[string]string{
		"VERSION": "1.2.3", "PUBLIC_URL": "https://panel.example.com", "PANEL_UID": "998",
		"INSTANCES_DIR": "/srv/mc-panel", "RESTIC_BINARY": "/usr/bin/restic", "PING_ADDR": "127.0.0.1:25565",
	}
	s := regexp.MustCompile(`\{\{([A-Z0-9_]+)\}\}`).ReplaceAllStringFunc(string(b), func(m string) string {
		v, ok := vals[strings.Trim(m, "{}")]
		if !ok {
			t.Fatalf("%s: placeholder %s has no sample value", name, m)
		}
		return v
	})
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDeployPanelTemplate(t *testing.T) {
	p, err := LoadPanel(renderDeploy(t, "panel.toml"), false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if path, ok := p.ListenUnix(); !ok || path != "/run/mc-panel-web/panel.sock" {
		t.Fatalf("listen = %q", p.Listen)
	}
}

func TestDeployAgentTemplate(t *testing.T) {
	a, err := LoadAgent(renderDeploy(t, "agent.toml"), false, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if a.PanelUID != 998 || a.InstancesDir != "/srv/mc-panel" {
		t.Fatalf("agent = %+v", a)
	}
}

func TestDeployFilesTemplate(t *testing.T) {
	f, err := LoadFiles(renderDeploy(t, "files.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if f.ResticBinary != "/usr/bin/restic" {
		t.Fatalf("restic_binary = %q", f.ResticBinary)
	}
}
```

Run: `make check` (kompiliert), dann `make test-vm SUITE=go`
Expected: PASS. Scheitert `TestDeployPanelTemplate` an einem unbekannten Schlüssel, stimmt die Vorlage nicht mit dem Code überein – korrigiere die Vorlage.

- [ ] **Step 3: `lib.sh` schreiben**

`scripts/install/.shellcheckrc`:

```
# Installer sources: main.sh sources lib.sh and the step files from its own directory.
source-path=SCRIPTDIR
external-sources=true
```

`scripts/install/lib.sh`:

```bash
# shellcheck shell=bash
# lib.sh - shared helpers of install.sh (installer plan V2): constants, globals with defaults,
# logging, templates, file writes, downloads, checksums and the sys_* wrappers that tests
# (MCPANEL_INSTALL_DRYRUN=1) only record. Sourced first; defines no side effects besides
# variables. Every system path below is unprefixed; file operations prefix it with $ROOT.

MCPANEL_VERSION=dev

BIN_DIR=/usr/local/bin
ETC_PANEL=/etc/mc-panel
ETC_FILES=/etc/mc-panel-files
DATA_DIR=/var/lib/mc-panel
INSTANCES_DIR=/srv/mc-panel
UNIT_DIR=/etc/systemd/system
CADDY_DIR=/etc/caddy
LOG_FILE=/var/log/mc-panel-install.log
ROLLBACK_DIR=/var/lib/mc-panel-rollback # root:root 0700, outside mcpanel's data dir (overview Global Constraints)
RELEASE_BASE=https://github.com/Bernotese/mc-panel/releases
TEMPLATE_DELIM=__MCPANEL_TEMPLATE_EOF__

# reset_globals sets every global the command line can change to its default (main.sh's
# parse_args fills them; tests start from these values).
reset_globals() {
  CMD=""
  DOMAIN=""
  EMAIL=""
  TLS_MODE=letsencrypt
  TARGET_VERSION=""
  FROM_DIR=""
  SSH_PORT=""
  NO_FIREWALL=0
  ADMIN_USER=""
  ADMIN_PASSWORD_FILE=""
  DOC_SUMMARY=0
  YES=0
  PURGE=0
  CHECK_ONLY=0
  FORCE=0
  JSON=0
  ARCH=""
  WORK="${WORK:-}"
}
reset_globals

# Only tests set these; an operator's ROOT variable is never read.
ROOT="${MCPANEL_INSTALL_ROOT:-}"
DRYRUN="${MCPANEL_INSTALL_DRYRUN:-0}"
TTY="${MCPANEL_INSTALL_TTY:-/dev/tty}"
TEMPLATE_DIR="${TEMPLATE_DIR:-}"

# --- logging -------------------------------------------------------------------------------

# _log TEXT appends to the install log when it exists (best effort, never fails).
_log() {
  local f="$ROOT$LOG_FILE"
  if [[ -w $f ]]; then
    printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" >>"$f" 2>/dev/null || true
  fi
  return 0
}

log_step() {
  printf '\n==> %s\n' "$1"
  _log "== $1"
}
# Step output on stdout, one prefix per result (the CI and doctor tests check these prefixes).
log_ok() {
  printf '[ok] %s\n' "$1"
  _log "[ok] $1"
}
log_changed() {
  printf '[changed] %s\n' "$1"
  _log "[changed] $1"
}
log_skip() {
  printf '[skip] %s\n' "$1"
  _log "[skip] $1"
}
warn() {
  printf '[warn] %s\n' "$1"
  _log "[warn] $1"
}

# die MSG [CODE]: message, pointer to the log, exit CODE (default 1). Inside $(...) only the
# subshell ends; callers rely on set -e (main.sh) to stop.
die() {
  printf 'install.sh: error: %s\n' "$1" >&2
  if [[ -e $ROOT$LOG_FILE ]]; then
    printf '  details: %s\n' "$LOG_FILE" >&2
  fi
  _log "error $1"
  exit "${2:-1}"
}

# confirm PROMPT: 0 = yes. YES=1 answers yes without asking; without a terminal the answer
# is no (curl | bash has no stdin, so the question goes to /dev/tty).
confirm() {
  if [[ $YES == 1 ]]; then
    return 0
  fi
  local ans=""
  if ! { [[ -r $TTY ]] && read -r -p "$1 [y/N] " ans <"$TTY"; } 2>/dev/null; then
    return 1
  fi
  [[ $ans =~ ^[yYjJ] ]]
}

# --- versions ------------------------------------------------------------------------------

# version_ge A B: 0 if version A >= B. MAJOR.MINOR.PATCH with optional leading v,
# -prerelease (older than the release) and +build (ignored); 2 for a malformed version.
version_ge() {
  local a="${1#v}" b="${2#v}" ap="" bp="" i x y
  a="${a%%+*}"
  b="${b%%+*}"
  if [[ $a == *-* ]]; then
    ap="${a#*-}"
    a="${a%%-*}"
  fi
  if [[ $b == *-* ]]; then
    bp="${b#*-}"
    b="${b%%-*}"
  fi
  local -a av bv
  IFS=. read -r -a av <<<"$a"
  IFS=. read -r -a bv <<<"$b"
  if [[ ${#av[@]} -ne 3 || ${#bv[@]} -ne 3 ]]; then
    return 2
  fi
  for i in 0 1 2; do
    x="${av[i]}"
    y="${bv[i]}"
    if ! [[ $x =~ ^[0-9]+$ && $y =~ ^[0-9]+$ ]]; then
      return 2
    fi
    if ((10#$x > 10#$y)); then
      return 0
    fi
    if ((10#$x < 10#$y)); then
      return 1
    fi
  done
  if [[ -z $ap ]]; then
    return 0
  fi
  if [[ -z $bp ]]; then
    return 1
  fi
  [[ ! $ap < $bp ]]
}

# --- templates -----------------------------------------------------------------------------

# tpl_func_suffix PATH: the name part of the embedded template function for PATH below
# deploy/ ("systemd/mcpanel-agent.service" -> "systemd_mcpanel_agent_service"); must match
# assemble.sh.
tpl_func_suffix() {
  local s="${1//\//_}"
  s="${s//./_}"
  s="${s//-/_}"
  printf '%s' "$s"
}

# render_template NAME: template NAME (path below deploy/) on stdout with every {{KEY}}
# replaced by the value of KEY_<KEY>. Source: $FROM_DIR/deploy when FROM_DIR is set, else the
# function embedded by assemble.sh, else $TEMPLATE_DIR (source tree). A placeholder without
# value or an unresolved "{{" ends the run.
render_template() {
  local name="$1" fn src out="" rest key var
  fn="tpl_$(tpl_func_suffix "$name")"
  if [[ -n $FROM_DIR ]]; then
    [[ -f $FROM_DIR/deploy/$name ]] || die "template deploy/$name is missing in $FROM_DIR"
    src="$(cat -- "$FROM_DIR/deploy/$name")"
  elif declare -F "$fn" >/dev/null; then
    src="$("$fn")"
  elif [[ -n $TEMPLATE_DIR && -f $TEMPLATE_DIR/$name ]]; then
    src="$(cat -- "$TEMPLATE_DIR/$name")"
  else
    die "unknown template $name"
  fi
  rest="$src"
  while [[ $rest =~ \{\{([A-Z0-9_]+)\}\} ]]; do
    key="${BASH_REMATCH[1]}"
    var="KEY_$key"
    if [[ -z ${!var+x} ]]; then
      die "template $name: no value for {{$key}} ($var)"
    fi
    out+="${rest%%"{{$key}}"*}${!var}"
    rest="${rest#*"{{$key}}"}"
  done
  out+="$rest"
  if [[ $out == *'{{'* ]]; then
    die "template $name: unresolved placeholder"
  fi
  printf '%s\n' "$out"
}

# --- files ---------------------------------------------------------------------------------

# _owner_get PATH: "owner:group" of $ROOT$PATH. In dry-run mode (tests run as a normal user)
# owners are simulated in $WORK/owners, lines "path owner group mode", the last line per path
# wins; a path without line reports the real owner.
_owner_get() {
  local line=""
  if [[ $DRYRUN == 1 && -f $WORK/owners ]]; then
    line="$(awk -v p="$1" '$1 == p { l = $2 ":" $3 } END { print l }' "$WORK/owners")"
  fi
  if [[ -n $line ]]; then
    printf '%s' "$line"
  else
    stat -c '%U:%G' -- "$ROOT$1"
  fi
}

# _owner_set PATH OWNER GROUP MODE: records the simulated owner (dry-run only).
_owner_set() {
  if [[ $DRYRUN == 1 ]]; then
    printf '%s %s %s %s\n' "$1" "$2" "$3" "$4" >>"$WORK/owners"
  fi
}

# write_file DEST MODE OWNER GROUP < content: writes stdin to $ROOT$DEST atomically (temp file
# in the same directory, then mv) unless content, mode and owner already match; prints
# "changed" or "unchanged". The directory must exist.
write_file() {
  local dest="$ROOT$1" mode="$2" owner="$3" group="$4" tmp
  tmp="$(mktemp "${dest%/*}/.mcpanel-install.XXXXXX")" || die "cannot write to ${1%/*}"
  cat >"$tmp"
  if [[ -f $dest ]] && cmp -s -- "$tmp" "$dest" && [[ $(stat -c '%a' -- "$dest") == "${mode#0}" ]] &&
    [[ $(_owner_get "$1") == "$owner:$group" ]]; then
    rm -f -- "$tmp"
    echo unchanged
    return 0
  fi
  chmod "$mode" -- "$tmp"
  sys_chown "$owner:$group" "$tmp"
  mv -f -- "$tmp" "$dest"
  _owner_set "$1" "$owner" "$group" "$mode"
  echo changed
}

# ensure_dir PATH MODE OWNER GROUP: creates $ROOT$PATH (with parents) or corrects mode and
# owner of an existing directory; never follows a symlink at PATH. Prints changed/unchanged.
ensure_dir() {
  local dir="$ROOT$1" mode="$2" owner="$3" group="$4" changed=0
  if [[ -L $dir ]]; then
    die "$1 is a symlink; refusing to use it"
  fi
  if [[ ! -d $dir ]]; then
    [[ ! -e $dir ]] || die "$1 exists but is not a directory"
    mkdir -p -- "$dir"
    changed=1
  fi
  if [[ $(stat -c '%a' -- "$dir") != "${mode#0}" ]]; then
    chmod "$mode" -- "$dir"
    changed=1
  fi
  if [[ $changed == 1 || $(_owner_get "$1") != "$owner:$group" ]]; then
    sys_chown "$owner:$group" "$dir"
    _owner_set "$1" "$owner" "$group" "$mode"
    changed=1
  fi
  if [[ $changed == 1 ]]; then echo changed; else echo unchanged; fi
}

# toml_ensure_key FILE KEY VALUE: adds "KEY = VALUE" (VALUE is a TOML literal, e.g. "\"x\"" or
# 5) when KEY is missing on the top level of FILE (before the first [section]); inserts it
# before that section with a comment; never changes an existing value. Prints changed/unchanged.
toml_ensure_key() {
  local file="$ROOT$1" key="$2" value="$3" mode owner group
  [[ -f $file ]] || die "$1 is missing"
  if awk -v k="$key" '
      /^[[:space:]]*\[/ { exit }
      { line = $0; sub(/^[[:space:]]+/, "", line)
        if (index(line, k) == 1) { r = substr(line, length(k) + 1); sub(/^[[:space:]]+/, "", r)
          if (substr(r, 1, 1) == "=") { found = 1; exit } } }
      END { exit !found }' "$file"; then
    echo unchanged
    return 0
  fi
  mode="$(stat -c '%a' -- "$file")"
  owner="$(stat -c '%U' -- "$file")"
  group="$(stat -c '%G' -- "$file")"
  awk -v line="$key = $value" -v note="# added by install.sh $MCPANEL_VERSION" '
      !done && /^[[:space:]]*\[/ { print note; print line; print ""; done = 1 }
      { print }
      END { if (!done) { print note; print line } }' "$file" | write_file "$1" "0$mode" "$owner" "$group"
}

# --- downloads -----------------------------------------------------------------------------

# fetch URL DEST: HTTPS only (also for redirects), TLS >= 1.2, 3 retries.
fetch() {
  case "$1" in
  https://*) ;;
  *) die "refusing a non-HTTPS download: $1" ;;
  esac
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 -fsSL --retry 3 --retry-delay 2 \
    --connect-timeout 15 -o "$2" -- "$1" || die "download failed: $1"
}

# sha256_verify FILE CHECKSUMS NAME: the single line for NAME in CHECKSUMS (sha256sum format,
# as goreleaser writes checksums.txt) must match FILE.
sha256_verify() {
  local n want got
  [[ -f $2 ]] || die "checksums file ${2##*/} is missing"
  n="$(awk -v n="$3" '$2 == n || $2 == "*" n { c++ } END { print c + 0 }' "$2")"
  [[ $n == 1 ]] || die "checksums: expected one entry for $3, found $n"
  want="$(awk -v n="$3" '$2 == n || $2 == "*" n { print $1 }' "$2")"
  [[ $want =~ ^[0-9a-f]{64}$ ]] || die "checksums: malformed entry for $3"
  got="$(sha256sum -- "$1" | awk '{ print $1 }')"
  [[ $got == "$want" ]] || die "checksum mismatch for $3 (expected $want, got $got)"
}

# --- system wrappers -----------------------------------------------------------------------

# _sys CMD...: runs CMD; in dry-run mode only appends "CMD..." to $WORK/calls.log and succeeds.
_sys() {
  if [[ $DRYRUN == 1 ]]; then
    printf '%s\n' "$*" >>"$WORK/calls.log"
    return 0
  fi
  "$@"
}
sys_apt() { _sys env DEBIAN_FRONTEND=noninteractive apt-get -y -q "$@"; }
sys_systemctl() { _sys systemctl "$@"; }
sys_useradd() { _sys useradd "$@"; }
sys_userdel() { _sys userdel "$@"; }
sys_nft() { _sys nft "$@"; }
sys_docker() { _sys docker "$@"; }
sys_caddy() { _sys caddy "$@"; }
sys_chown() { _sys chown "$@"; }
sys_runuser() {
  local user="$1"
  shift
  _sys runuser -u "$user" -- "$@"
}

# run_as USER CMD...: runs CMD as USER.
run_as() { sys_runuser "$@"; }

# systemd_wait_active UNIT SECS: 0 once UNIT is active, 1 after SECS seconds.
systemd_wait_active() {
  local i
  for ((i = 0; i <= $2; i++)); do
    if sys_systemctl is-active --quiet "$1"; then
      return 0
    fi
    if ((i < $2)); then
      sleep 1
    fi
  done
  return 1
}
```

- [ ] **Step 4: Stub-Dateien anlegen**

Jede Datei bekommt Kopf und Stub; B1/B2 ersetzen den Inhalt ihrer Dateien vollständig. Inhalt nach diesem Muster (Datei – Funktion – Phase):

| Datei | Funktion | ersetzt durch |
|---|---|---|
| `preflight.sh` | `pre_check` | B1, Aufgabe 4 |
| `packages.sh` | `pkg_install` | B1, Aufgabe 4 |
| `layout.sh` | `lay_ensure` | B1, Aufgabe 5 |
| `binaries.sh` | `bin_install` | B1, Aufgabe 5 |
| `config.sh` | `cfg_ensure` | B1, Aufgabe 5 |
| `units.sh` | `unit_ensure` | B1, Aufgabe 6 |
| `caddy.sh` | `cad_ensure` | B1, Aufgabe 6 |
| `firewall.sh` | `fw_ensure` | B1, Aufgabe 6 |
| `upgrades.sh` | `upg_ensure` | B1, Aufgabe 7 |
| `admin.sh` | `adm_ensure` | B1, Aufgabe 7 |
| `update.sh` | `cmd_update` | B2, Aufgabe 8 |
| `doctor.sh` | `cmd_doctor` | B2, Aufgabe 9 |
| `uninstall.sh` | `cmd_uninstall` | B2, Aufgabe 10 |

Beispiel `scripts/install/preflight.sh`:

```bash
# shellcheck shell=bash
# preflight.sh - checks before install changes anything (installer spec §4.1).
# Stub from phase A; phase B1 (task 4) replaces this file.

pre_check() {
  log_step "preflight"
  log_skip "not implemented yet"
}
```

`scripts/install/doctor.sh` (Stub gibt einen Status zurück, wie später die echte Funktion):

```bash
# shellcheck shell=bash
# doctor.sh - checks the installation (installer spec §6). Returns 0 (ok), 1 (warnings) or
# 2 (errors) instead of exiting (main uses it as exit code); DOC_SUMMARY=1 (from install)
# asks for the short form.
# Stub from phase A; phase B2 (task 9) replaces this file.

cmd_doctor() {
  log_step "doctor"
  log_skip "not implemented yet"
  return 0
}
```

Die übrigen elf Dateien entsprechend mit ihrer Funktion, dem `log_step`-Namen (`packages`, `layout`, `binaries`, `configuration`, `systemd units`, `caddy`, `firewall`, `security updates`, `first admin`, `update`, `uninstall`) und der Zeile `log_skip "not implemented yet"`.

- [ ] **Step 5: `main.sh` schreiben**

`scripts/install/main.sh`:

```bash
#!/usr/bin/env bash
# shellcheck shell=bash
# main.sh - entry point of install.sh (installer plan V4): arguments, subcommands, logging.
# Usage: install.sh <install|update|doctor|uninstall> [options]; see usage below.
#
# Only the very last line of the assembled script calls main, so a download that breaks off
# (curl | bash) never runs a partial script.

if ! declare -F log_step >/dev/null; then # running from the source tree (tests, make check)
  _src_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  # shellcheck source=lib.sh
  . "$_src_dir/lib.sh"
  for _f in "$_src_dir"/*.sh; do
    case "${_f##*/}" in
    lib.sh | main.sh | assemble.sh | test.sh) continue ;;
    esac
    # shellcheck disable=SC1090 # step files are found at run time; shellcheck checks each file itself
    . "$_f"
  done
  TEMPLATE_DIR="${TEMPLATE_DIR:-$_src_dir/../../deploy}"
  unset _f _src_dir
fi

usage() {
  cat <<'EOF'
usage: install.sh <command> [options]

  install    --domain FQDN --email ADDRESS [--version vX.Y.Z | --from-dir DIR]
             [--tls letsencrypt|internal] [--ssh-port N] [--no-firewall]
             [--admin-user NAME [--admin-password-file FILE]] [--yes]
  update     [--version vX.Y.Z | --from-dir DIR] [--check] [--force] [--yes]
  doctor     [--json]
  uninstall  [--purge] [--yes]

Example:
  curl -fsSL https://github.com/Bernotese/mc-panel/releases/latest/download/install.sh \
    | sudo bash -s -- install --domain panel.example.com --email you@example.com
EOF
}

# need_value OPT ARGS...: the option OPT needs a value (the first of ARGS).
need_value() {
  if [[ $# -lt 2 || -z $2 || $2 == --* ]]; then
    die "$1 needs a value"
  fi
}

parse_args() {
  if [[ $# -eq 0 ]]; then
    usage >&2
    exit 1
  fi
  CMD="$1"
  shift
  case "$CMD" in
  install | update | doctor | uninstall) ;;
  -h | --help | help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    die "unknown command: $CMD"
    ;;
  esac
  local opt
  while [[ $# -gt 0 ]]; do
    opt="$1"
    shift
    if [[ $opt == --*=* ]]; then
      set -- "${opt#*=}" "$@"
      opt="${opt%%=*}"
    fi
    case "$CMD:$opt" in
    *:-h | *:--help)
      usage
      exit 0
      ;;
    install:--domain)
      need_value "$opt" "$@"
      DOMAIN="$1"
      shift
      ;;
    install:--email)
      need_value "$opt" "$@"
      EMAIL="$1"
      shift
      ;;
    install:--version | update:--version)
      need_value "$opt" "$@"
      TARGET_VERSION="$1"
      shift
      ;;
    install:--from-dir | update:--from-dir)
      need_value "$opt" "$@"
      FROM_DIR="$1"
      shift
      ;;
    install:--tls)
      need_value "$opt" "$@"
      TLS_MODE="$1"
      shift
      ;;
    install:--ssh-port)
      need_value "$opt" "$@"
      SSH_PORT="$1"
      shift
      ;;
    install:--admin-user)
      need_value "$opt" "$@"
      ADMIN_USER="$1"
      shift
      ;;
    install:--admin-password-file)
      need_value "$opt" "$@"
      ADMIN_PASSWORD_FILE="$1"
      shift
      ;;
    install:--no-firewall) NO_FIREWALL=1 ;;
    update:--check) CHECK_ONLY=1 ;;
    update:--force) FORCE=1 ;;
    doctor:--json) JSON=1 ;;
    uninstall:--purge) PURGE=1 ;;
    install:--yes | update:--yes | uninstall:--yes) YES=1 ;;
    *) die "unknown option for $CMD: $opt (see install.sh --help)" ;;
    esac
  done
}

validate_args() {
  local fqdn='^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z][a-z0-9-]{0,61}[a-z0-9]$'
  if [[ -n $TARGET_VERSION ]]; then
    [[ $TARGET_VERSION =~ ^v?[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$ ]] ||
      die "--version: $TARGET_VERSION is not a version (vX.Y.Z)"
    TARGET_VERSION="${TARGET_VERSION#v}"
  fi
  if [[ -n $TARGET_VERSION && -n $FROM_DIR ]]; then
    die "--version and --from-dir exclude each other"
  fi
  if [[ -n $FROM_DIR ]]; then
    [[ -d $FROM_DIR ]] || die "--from-dir: $FROM_DIR is not a directory"
    FROM_DIR="$(cd "$FROM_DIR" && pwd)"
  fi
  [[ -z $SSH_PORT || ($SSH_PORT =~ ^[0-9]+$ && $SSH_PORT -ge 1 && $SSH_PORT -le 65535) ]] ||
    die "--ssh-port: $SSH_PORT is not a port"
  [[ -z $ADMIN_USER || $ADMIN_USER =~ ^[A-Za-z0-9._-]{1,64}$ ]] || die "--admin-user: invalid name"
  if [[ -n $ADMIN_PASSWORD_FILE ]]; then
    # curl | bash: stdin is the script itself, so a password comes from a file (spec §4.10).
    [[ -n $ADMIN_USER ]] || die "--admin-password-file needs --admin-user"
    [[ -f $ADMIN_PASSWORD_FILE && ! -L $ADMIN_PASSWORD_FILE ]] || die "--admin-password-file: not a regular file"
    [[ $(stat -c '%a' -- "$ADMIN_PASSWORD_FILE") == 600 ]] || die "--admin-password-file must have mode 0600"
  fi
  if [[ $CMD != install ]]; then
    return 0
  fi
  case "$TLS_MODE" in
  letsencrypt)
    [[ -n $DOMAIN ]] || die "--domain is required (the panel's DNS name)"
    [[ -n $EMAIL ]] || die "--email is required for Let's Encrypt"
    ;;
  internal)
    if [[ -z $DOMAIN ]]; then
      DOMAIN=localhost
      warn "no --domain: using localhost with Caddy's internal certificate"
    fi
    ;;
  *) die "--tls must be letsencrypt or internal" ;;
  esac
  [[ $DOMAIN == localhost || $DOMAIN =~ $fqdn ]] || die "--domain: $DOMAIN is not a lowercase DNS name"
  [[ -z $EMAIL || $EMAIL =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]] || die "--email: invalid address"
  if [[ -n $FROM_DIR ]]; then
    # The build in FROM_DIR is the target: its own version, no network (overview V4).
    [[ -x $FROM_DIR/panel ]] || die "--from-dir: $FROM_DIR/panel is missing"
    TARGET_VERSION="$("$FROM_DIR/panel" version)"
    TARGET_VERSION="${TARGET_VERSION#v}"
  elif [[ -z $TARGET_VERSION && $CMD == install ]]; then
    [[ $MCPANEL_VERSION != dev ]] || die "this is a development build of install.sh: pass --version or --from-dir"
    TARGET_VERSION="${MCPANEL_VERSION#v}"
  fi
}

# release_tag VERSION: the git tag of a release (always with a leading v).
release_tag() {
  printf 'v%s\n' "${1#v}"
}

require_root() {
  [[ $(id -u) -eq 0 ]] || die "install.sh must run as root (sudo)"
}

detect_arch() {
  case "$(uname -m)" in
  x86_64 | amd64) echo amd64 ;;
  aarch64 | arm64) echo arm64 ;;
  *) die "unsupported architecture: $(uname -m) (amd64 and arm64 only)" ;;
  esac
}

setup_work() {
  # update hands over to the install.sh of the target version with exec (B2): the child takes
  # the parent's WORK (downloads are already there) and removes it at exit.
  if [[ -n ${MCPANEL_HANDOFF_WORK:-} && -d $MCPANEL_HANDOFF_WORK && $MCPANEL_HANDOFF_WORK == */mc-panel-install.* ]]; then
    WORK="$MCPANEL_HANDOFF_WORK"
    unset MCPANEL_HANDOFF_WORK
  else
    WORK="$(mktemp -d "${TMPDIR:-/tmp}/mc-panel-install.XXXXXX")"
  fi
  # shellcheck disable=SC2064 # expand WORK now: the trap must remove exactly this directory
  trap "rm -rf -- '$WORK'" EXIT
}

setup_log() {
  local f="$ROOT$LOG_FILE"
  if [[ $(id -u) -ne 0 ]]; then
    return 0 # doctor without root: no log
  fi
  if [[ ! -e $f ]]; then
    (umask 077 && : >"$f")
  fi
  chmod 0600 -- "$f"
}

cmd_install() {
  pre_check
  pkg_install
  lay_ensure
  bin_install
  cfg_ensure
  unit_ensure
  cad_ensure
  if [[ $NO_FIREWALL == 1 ]]; then
    log_step "firewall"
    log_skip "--no-firewall given"
  else
    fw_ensure
  fi
  upg_ensure
  adm_ensure
  install_summary
}

install_summary() {
  local rc=0
  DOC_SUMMARY=1
  cmd_doctor || rc=$?
  DOC_SUMMARY=0
  printf '\nmc-panel is installed: https://%s/\n' "$DOMAIN"
  if [[ $rc -ne 0 ]]; then
    printf 'doctor reported %s; run: sudo bash install.sh doctor\n' "$([[ $rc -eq 1 ]] && echo warnings || echo errors)"
  fi
  cat <<'EOF'
Next steps:
  1. Log in and set up two-factor authentication.
  2. Settings: enter the base domain for Minecraft servers (wildcard DNS *.<domain> -> this host).
  3. Settings: add a notification channel (Discord or ntfy).
  4. Back up /srv/mc-panel/backups/keys/ - without it the backups cannot be read.
EOF
}

main() {
  set -euo pipefail
  umask 022
  parse_args "$@"
  validate_args
  if [[ $CMD != doctor ]]; then
    require_root
  fi
  ARCH="$(detect_arch)"
  setup_work
  setup_log
  _log "install.sh $MCPANEL_VERSION: $CMD $*"
  # Each command runs in a subshell with errexit on and outside any conditional context:
  # `cmd || rc=$?` would switch errexit off for the whole call tree (overview V4).
  local rc
  set +e
  (
    set -e
    "cmd_$CMD"
  )
  rc=$?
  set -e
  exit "$rc"
}

if [[ ${MCPANEL_INSTALL_NO_MAIN:-0} != 1 ]]; then main "$@"; fi
```

(Die letzte Zeile ist bewusst ein einzelnes `if`: Fehlt sie bei einem abgebrochenen Download, passiert nichts.)

- [ ] **Step 6: Test-Harness schreiben**

`scripts/install/test.sh`:

```bash
#!/usr/bin/env bash
# shellcheck shell=bash
# test.sh [FILTER] - runs the installer's Bash tests (scripts/install/tests/*.sh) without a
# framework. Every test_* function runs in its own subshell with set -e, a fresh ROOT and WORK
# and dry-run mode, so nothing touches the system. TAP-like output; exit 1 if a test failed.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export MCPANEL_INSTALL_NO_MAIN=1 MCPANEL_INSTALL_DRYRUN=1
# shellcheck source=main.sh
. "$here/main.sh"
for _f in "$here"/tests/*.sh; do
  # shellcheck disable=SC1090 # test files are found at run time
  . "$_f"
done

# --- assertions (exit the test's subshell on failure) ----------------------------------------
fail() {
  printf '%s\n' "$@" >&2
  exit 1
}
assert_eq() {
  [[ $1 == "$2" ]] || fail "expected: $1" "actual:   $2" "${3:-}"
}
assert_contains() {
  [[ $1 == *"$2"* ]] || fail "missing: $2" "in: $1"
}
assert_not_contains() {
  [[ $1 != *"$2"* ]] || fail "unexpected: $2" "in: $1"
}
assert_file_contains() {
  [[ -f $1 ]] || fail "missing file: $1"
  grep -qF -- "$2" "$1" || fail "missing in $1: $2" "$(cat -- "$1")"
}
# assert_file_eq FILE EXP: the content of FILE (without its last newline) is EXP.
assert_file_eq() {
  [[ -f $1 ]] || fail "missing file: $1"
  assert_eq "$2" "$(cat -- "$1")" "content of $1"
}
# assert_exit CODE CMD...: runs CMD in a subshell (output in $WORK/out) and expects exit CODE.
assert_exit() {
  local want="$1" s=0
  shift
  ("$@") >"$WORK/out" 2>&1 || s=$?
  [[ $s == "$want" ]] || fail "exit $s, want $want: $*" "$(cat -- "$WORK/out")"
}
# stub NAME BODY: defines function NAME { BODY } for the current test (functions win over
# commands, so this replaces queries like id, ss, getent, sshd -T or curl --unix-socket).
# Sourcing a generated definition keeps install.sh itself free of eval.
stub() {
  # shellcheck disable=SC1090 # the definition is generated here
  . <(printf '%s() {\n%s\n}\n' "$1" "$2")
}
assert_called() {
  grep -qF -- "$1" "$WORK/calls.log" 2>/dev/null || fail "not called: $1" "$(cat -- "$WORK/calls.log" 2>/dev/null)"
}
assert_not_called() {
  if grep -qF -- "$1" "$WORK/calls.log" 2>/dev/null; then fail "unexpectedly called: $1"; fi
}
# assert_fails CMD...: CMD exits with any code other than 0 (output in $WORK/out).
assert_fails() {
  local s=0
  ("$@") >"$WORK/out" 2>&1 || s=$?
  [[ $s -ne 0 ]] || fail "expected failure: $*" "$(cat -- "$WORK/out")"
}
assert_file() {
  [[ -e $1 || -L $1 ]] || fail "missing: $1"
}
assert_no_file() {
  [[ ! -e $1 && ! -L $1 ]] || fail "unexpected: $1"
}
# fake_exec: replaces the exec builtin for the current test; the call is recorded in
# calls.log as "exec <args>" and the caller returns 0 instead of being replaced.
fake_exec() {
  stub exec 'printf "exec %s\n" "$*" >>"$WORK/calls.log"'
}

# --- runner ----------------------------------------------------------------------------------
# test.sh [FILE|PATTERN]: an argument with a "/" or ending in ".sh" selects the tests defined
# in that file (path relative to scripts/install or absolute), anything else filters by name.
filter="${1:-}"
only=""
if [[ $filter == */* || $filter == *.sh ]]; then
  [[ $filter == /* ]] || filter="$here/$filter"
  [[ -f $filter ]] || { echo "# no such test file: $filter" >&2; exit 1; }
  only=" $(grep -oE '^test_[A-Za-z0-9_]+' "$filter" | tr '\n' ' ')"
  filter=""
fi
n=0
failed=0
for t in $(declare -F | awk '{ print $3 }' | grep '^test_' | LC_ALL=C sort); do
  if [[ -n $only && $only != *" $t "* ]]; then
    continue
  fi
  if [[ -n $filter && $t != *"$filter"* ]]; then
    continue
  fi
  n=$((n + 1))
  out="$(mktemp)"
  set +e
  (
    set -e
    ROOT="$(mktemp -d)"
    WORK="$ROOT/.work"
    mkdir -p "$WORK"
    : >"$WORK/calls.log"
    DRYRUN=1
    trap 'rm -rf -- "$ROOT"' EXIT
    "$t"
  ) >"$out" 2>&1
  s=$?
  set -e
  if [[ $s -eq 0 ]]; then
    echo "ok $n - $t"
  else
    echo "not ok $n - $t"
    sed 's/^/#   /' "$out"
    failed=$((failed + 1))
  fi
  rm -f -- "$out"
done
echo "1..$n"
if [[ $n -eq 0 ]]; then
  echo "# no tests matched '${filter}'" >&2
  exit 1
fi
if [[ $failed -ne 0 ]]; then
  echo "# $failed of $n tests failed" >&2
  exit 1
fi
```

- [ ] **Step 7: Tests für `lib.sh` und `main.sh` schreiben**

`scripts/install/tests/lib_test.sh`:

```bash
# shellcheck shell=bash
# Tests of lib.sh and main.sh (installer plan V2, V4). Run: bash scripts/install/test.sh

test_version_ge() {
  version_ge 1.2.3 1.2.3
  version_ge v1.10.0 1.9.9
  version_ge 2.0.0 1.99.99
  version_ge 1.2.3 1.2.3-rc.1
  version_ge 1.2.3+build.5 1.2.3
  version_ge 1.2.3-rc.2 1.2.3-rc.1
  assert_exit 1 version_ge 1.2.2 1.2.3
  assert_exit 1 version_ge 1.2.3-rc.1 1.2.3
  assert_exit 2 version_ge 1.2 1.2.3
  assert_exit 2 version_ge 1.x.3 1.2.3
}

test_render_template_from_dir() {
  FROM_DIR="$ROOT/src"
  mkdir -p "$FROM_DIR/deploy/x"
  printf 'host {{DOMAIN}}\nport {{SSH_PORT}} and {{DOMAIN}}\n' >"$FROM_DIR/deploy/x/a.conf"
  KEY_DOMAIN=panel.example.com KEY_SSH_PORT=2222
  assert_eq $'host panel.example.com\nport 2222 and panel.example.com' "$(render_template x/a.conf)"
}

test_render_template_missing_value_dies() {
  FROM_DIR="$ROOT/src"
  mkdir -p "$FROM_DIR/deploy"
  printf 'x {{NOPE}}\n' >"$FROM_DIR/deploy/a"
  assert_exit 1 render_template a
  assert_contains "$(cat "$WORK/out")" "no value for {{NOPE}}"
}

test_render_template_embedded_wins_over_template_dir() {
  tpl_x_y_conf() { printf 'embedded {{VERSION}}\n'; }
  TEMPLATE_DIR="$ROOT/tpl"
  mkdir -p "$TEMPLATE_DIR/x"
  printf 'tree\n' >"$TEMPLATE_DIR/x/y.conf"
  KEY_VERSION=1.2.3
  assert_eq "embedded 1.2.3" "$(render_template x/y.conf)"
}

test_render_template_unresolved_braces_die() {
  FROM_DIR="$ROOT/src"
  mkdir -p "$FROM_DIR/deploy"
  printf 'x {{lower}}\n' >"$FROM_DIR/deploy/a"
  assert_exit 1 render_template a
}

test_every_deploy_template_renders() {
  local f rel
  KEY_DOMAIN=panel.example.com KEY_TLS="tls you@example.com" KEY_PANEL_UID=998 \
    KEY_PUBLIC_URL=https://panel.example.com KEY_INSTANCES_DIR=/srv/mc-panel KEY_SSH_PORT="22, 2222" \
    KEY_VERSION=1.2.3 KEY_RESTIC_BINARY=/usr/bin/restic KEY_PING_ADDR=127.0.0.1:25565
  while IFS= read -r f; do
    rel="${f#"$TEMPLATE_DIR"/}"
    assert_not_contains "$(render_template "$rel")" '{{'
  done < <(find "$TEMPLATE_DIR" -type f)
  assert_contains "$(render_template systemd/mcpanel-panel.socket)" "SocketGroup=caddy"
  assert_contains "$(render_template nftables/mcpanel_fw.nft)" "tcp dport { 22, 2222 } accept"
  assert_contains "$(render_template config/panel.toml)" 'public_url = "https://panel.example.com"'
}

test_write_file_changed_then_unchanged() {
  mkdir -p "$ROOT/etc"
  assert_eq changed "$(printf 'a\n' | write_file /etc/x.conf 0640 root root)"
  assert_eq 640 "$(stat -c '%a' "$ROOT/etc/x.conf")"
  assert_eq unchanged "$(printf 'a\n' | write_file /etc/x.conf 0640 root root)"
  assert_eq changed "$(printf 'a\n' | write_file /etc/x.conf 0600 root root)"
  assert_eq changed "$(printf 'b\n' | write_file /etc/x.conf 0600 root root)"
  assert_eq b "$(cat "$ROOT/etc/x.conf")"
  assert_called "chown root:root"
  assert_eq "" "$(find "$ROOT/etc" -name '.mcpanel-install.*')"
  assert_eq "root:root" "$(_owner_get /etc/x.conf)"
  # a simulated owner change makes the next write "changed" even with equal content and mode
  _owner_set /etc/x.conf mcpanel mcpanel 0600
  assert_eq changed "$(printf 'b\n' | write_file /etc/x.conf 0600 root root)"
}

test_ensure_dir() {
  assert_eq changed "$(ensure_dir /srv/mc-panel 0755 root root)"
  [[ -d $ROOT/srv/mc-panel ]] || fail "not created"
  assert_eq unchanged "$(ensure_dir /srv/mc-panel 0755 root root)"
  assert_eq changed "$(ensure_dir /srv/mc-panel 0750 root root)"
  assert_eq 750 "$(stat -c '%a' "$ROOT/srv/mc-panel")"
  assert_eq changed "$(ensure_dir /srv/mc-panel 0750 root mcpanel-mc)"
  assert_eq "root:mcpanel-mc" "$(_owner_get /srv/mc-panel)"
  mkdir -p "$ROOT/real"
  ln -s "$ROOT/real" "$ROOT/link"
  assert_exit 1 ensure_dir /link 0755 root root
  : >"$ROOT/file"
  assert_exit 1 ensure_dir /file 0755 root root
}

test_log_prefixes() {
  assert_eq "[ok] a" "$(log_ok a)"
  assert_eq "[changed] b" "$(log_changed b)"
  assert_eq "[skip] c" "$(log_skip c)"
  assert_eq "[warn] d" "$(warn d)"
}

test_stub_replaces_commands() {
  stub id 'echo 4242'
  assert_eq 4242 "$(id -u mcpanel)"
  stub sshd '[[ $1 == -T ]] && printf "port 2222\n"'
  assert_eq "port 2222" "$(sshd -T)"
}

test_toml_ensure_key_adds_before_first_section() {
  mkdir -p "$ROOT/etc"
  printf 'a = 1\n\n[limits]\nmax = 2\n' >"$ROOT/etc/c.toml"
  chmod 0640 "$ROOT/etc/c.toml"
  assert_eq changed "$(toml_ensure_key /etc/c.toml restic_binary '"/usr/bin/restic"')"
  local got
  got="$(cat "$ROOT/etc/c.toml")"
  assert_contains "$got" $'restic_binary = "/usr/bin/restic"\n\n[limits]'
  assert_eq 640 "$(stat -c '%a' "$ROOT/etc/c.toml")"
}

test_toml_ensure_key_keeps_existing_value() {
  mkdir -p "$ROOT/etc"
  printf '  restic_binary="/opt/restic"\n[x]\nrestic_binary = "nested"\n' >"$ROOT/etc/c.toml"
  assert_eq unchanged "$(toml_ensure_key /etc/c.toml restic_binary '"/usr/bin/restic"')"
  assert_eq '  restic_binary="/opt/restic"' "$(head -n1 "$ROOT/etc/c.toml")"
}

test_toml_ensure_key_ignores_key_only_in_section() {
  mkdir -p "$ROOT/etc"
  printf '[x]\nmc_user = "a"\n' >"$ROOT/etc/c.toml"
  assert_eq changed "$(toml_ensure_key /etc/c.toml mc_user '"mcpanel-mc"')"
  assert_eq 'mc_user = "mcpanel-mc"' "$(sed -n 2p "$ROOT/etc/c.toml")"
}

test_toml_ensure_key_prefix_is_not_a_match() {
  mkdir -p "$ROOT/etc"
  printf 'instances_dir_old = "/x"\n' >"$ROOT/etc/c.toml"
  assert_eq changed "$(toml_ensure_key /etc/c.toml instances_dir '"/srv/mc-panel"')"
}

test_sha256_verify() {
  printf 'payload' >"$WORK/f.tar.gz"
  local sum
  sum="$(sha256sum "$WORK/f.tar.gz" | awk '{ print $1 }')"
  printf '%s  f.tar.gz\n%s  other\n' "$sum" "$sum" >"$WORK/checksums.txt"
  sha256_verify "$WORK/f.tar.gz" "$WORK/checksums.txt" f.tar.gz
  printf 'changed' >"$WORK/f.tar.gz"
  assert_exit 1 sha256_verify "$WORK/f.tar.gz" "$WORK/checksums.txt" f.tar.gz
  assert_contains "$(cat "$WORK/out")" "checksum mismatch"
}

test_sha256_verify_missing_double_or_malformed_entry() {
  printf 'x' >"$WORK/f"
  printf 'abc  other\n' >"$WORK/c1"
  assert_exit 1 sha256_verify "$WORK/f" "$WORK/c1" f
  printf '%064d  f\n%064d  f\n' 0 0 >"$WORK/c2"
  assert_exit 1 sha256_verify "$WORK/f" "$WORK/c2" f
  printf 'zz  f\n' >"$WORK/c3"
  assert_exit 1 sha256_verify "$WORK/f" "$WORK/c3" f
  assert_exit 1 sha256_verify "$WORK/f" "$WORK/nope" f
}

test_fetch_https_only() {
  curl() { printf '%s\n' "$*" >"$WORK/curl.args"; }
  fetch https://github.com/x "$WORK/x"
  assert_file_contains "$WORK/curl.args" "--proto =https --proto-redir =https --tlsv1.2"
  assert_exit 1 fetch http://github.com/x "$WORK/x"
  assert_exit 1 fetch file:///etc/passwd "$WORK/x"
}

test_fetch_failure_dies() {
  curl() { return 22; }
  assert_exit 1 fetch https://github.com/x "$WORK/x"
  assert_contains "$(cat "$WORK/out")" "download failed"
}

test_confirm() {
  YES=1
  confirm "go?"
  YES=0
  TTY="$WORK/tty"
  printf 'j\n' >"$TTY"
  confirm "go?"
  printf 'n\n' >"$TTY"
  assert_exit 1 confirm "go?"
  TTY="$WORK/none"
  assert_exit 1 confirm "go?"
}

test_sys_wrappers_record_in_dry_run() {
  sys_apt install docker.io
  sys_systemctl daemon-reload
  sys_userdel mcpanel
  run_as mcpanel /usr/local/bin/panel admin list
  assert_called "apt-get -y -q install docker.io"
  assert_called "systemctl daemon-reload"
  assert_called "userdel mcpanel"
  assert_called "runuser -u mcpanel -- /usr/local/bin/panel admin list"
}

test_systemd_wait_active() {
  systemd_wait_active mcpanel-panel 0
  local n=0
  sys_systemctl() {
    n=$((n + 1))
    [[ $n -ge 2 ]]
  }
  systemd_wait_active mcpanel-panel 3
  sys_systemctl() { return 3; }
  assert_exit 1 systemd_wait_active mcpanel-panel 0
}

test_parse_args_install() {
  parse_args install --domain panel.example.com --email=you@example.com --ssh-port 2222 --no-firewall --yes
  assert_eq install "$CMD"
  assert_eq panel.example.com "$DOMAIN"
  assert_eq you@example.com "$EMAIL"
  assert_eq 2222 "$SSH_PORT"
  assert_eq 1 "$NO_FIREWALL"
  assert_eq 1 "$YES"
}

test_parse_args_rejects() {
  assert_exit 1 parse_args
  assert_exit 1 parse_args frobnicate
  assert_exit 1 parse_args install --domain
  assert_exit 1 parse_args install --domain --email x@y.z
  assert_exit 1 parse_args doctor --purge
  assert_exit 1 parse_args update --no-firewall
  assert_exit 0 parse_args --help
}

test_validate_args_install() {
  MCPANEL_VERSION=1.2.3
  parse_args install --domain panel.example.com --email you@example.com
  validate_args
  assert_eq 1.2.3 "$TARGET_VERSION"
  reset_globals
  parse_args install --domain Panel.Example.com --email you@example.com
  assert_exit 1 validate_args
  reset_globals
  parse_args install --domain panel.example.com
  assert_exit 1 validate_args
  reset_globals
  parse_args install --tls internal
  validate_args
  assert_eq localhost "$DOMAIN"
  reset_globals
  parse_args install --domain panel.example.com --email you@example.com --version 1.2
  assert_exit 1 validate_args
}

test_validate_args_dev_build_needs_source() {
  MCPANEL_VERSION=dev
  parse_args install --domain panel.example.com --email you@example.com
  assert_exit 1 validate_args
  assert_contains "$(cat "$WORK/out")" "development build"
  reset_globals
  mkdir -p "$ROOT/build"
  printf '#!/bin/sh\necho v1.4.0\n' >"$ROOT/build/panel"
  chmod +x "$ROOT/build/panel"
  parse_args install --domain panel.example.com --email you@example.com --from-dir "$ROOT/build"
  validate_args
  assert_eq "$ROOT/build" "$FROM_DIR"
  assert_eq 1.4.0 "$TARGET_VERSION" "--from-dir takes the version of the build"
  assert_eq v1.4.0 "$(release_tag "$TARGET_VERSION")"
}

test_main_keeps_errexit_in_commands() {
  # cmd_* run in a subshell with set -e: a failing step stops the command with its code.
  MCPANEL_VERSION=1.2.3
  cmd_doctor() {
    false
    echo "not reached"
  }
  require_root() { :; }
  setup_log() { :; }
  assert_exit 1 main doctor
  assert_not_contains "$(cat "$WORK/out")" "not reached"
}

test_validate_args_admin_password_file() {
  MCPANEL_VERSION=1.2.3
  printf 'secret\n' >"$WORK/pw"
  chmod 0644 "$WORK/pw"
  parse_args install --domain panel.example.com --email you@example.com --admin-user alice --admin-password-file "$WORK/pw"
  assert_exit 1 validate_args
  assert_contains "$(cat "$WORK/out")" "mode 0600"
  chmod 0600 "$WORK/pw"
  validate_args
  reset_globals
  parse_args install --domain panel.example.com --email you@example.com --admin-password-file "$WORK/pw"
  assert_exit 1 validate_args
}

test_setup_work_takes_handoff_dir() {
  local parent
  parent="$(mktemp -d "${TMPDIR:-/tmp}/mc-panel-install.XXXXXX")"
  MCPANEL_HANDOFF_WORK="$parent"
  setup_work
  assert_eq "$parent" "$WORK"
  trap 'rm -rf -- "$ROOT"' EXIT # setup_work replaced the harness's cleanup trap
  rm -rf -- "$parent"
  MCPANEL_HANDOFF_WORK=/etc
  setup_work
  [[ $WORK != /etc ]] || fail "a foreign directory was taken as WORK"
  trap 'rm -rf -- "$ROOT"' EXIT
  rm -rf -- "$WORK"
}

test_validate_args_version_and_from_dir_exclude() {
  mkdir -p "$ROOT/build"
  parse_args update --version v1.2.3 --from-dir "$ROOT/build"
  assert_exit 1 validate_args
}

test_detect_arch() {
  uname() { echo aarch64; }
  assert_eq arm64 "$(detect_arch)"
  uname() { echo x86_64; }
  assert_eq amd64 "$(detect_arch)"
  uname() { echo riscv64; }
  assert_exit 1 detect_arch
}

test_cmd_install_calls_every_step_in_order() {
  local order=""
  pre_check() { order+="pre "; }
  pkg_install() { order+="pkg "; }
  lay_ensure() { order+="lay "; }
  bin_install() { order+="bin "; }
  cfg_ensure() { order+="cfg "; }
  unit_ensure() { order+="unit "; }
  cad_ensure() { order+="cad "; }
  fw_ensure() { order+="fw "; }
  upg_ensure() { order+="upg "; }
  adm_ensure() { order+="adm "; }
  cmd_doctor() {
    order+="doc"
    [[ $DOC_SUMMARY == 1 ]] || order+="(long)"
  }
  DOMAIN=panel.example.com
  cmd_install >/dev/null
  assert_eq "pre pkg lay bin cfg unit cad fw upg adm doc" "$order"
  order=""
  NO_FIREWALL=1
  cmd_install >/dev/null
  assert_eq "pre pkg lay bin cfg unit cad upg adm doc" "$order"
}
```

- [ ] **Step 8: Rot/Grün prüfen**

Run: `bash -n scripts/install/*.sh scripts/install/tests/*.sh` (auch auf dem Mac), dann `make check`, dann auf der VM: `ssh claude@192.168.1.117 'cd ~/mc-panel-test/<worktree> && bash scripts/install/test.sh'` (nach `make test-vm SUITE=lint`, das den Baum kopiert; ab Aufgabe 3 läuft `test.sh` dort automatisch mit)
Expected: alle Tests `ok`, Abschluss `1..N` ohne `not ok`.

- [ ] **Step 9: Commit**

```bash
git add deploy internal/config/deploy_test.go scripts/install
git commit -m "feat(install): templates, shared library, entry point and test harness of install.sh

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 3: Zusammenfügen, `make installer`, Release-Asset, Lint in CI und auf der VM

**Files:**
- Create: `scripts/install/assemble.sh`, `scripts/install/tests/assemble_test.sh`
- Modify: `Makefile`, `.gitignore`, `.goreleaser.yaml`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `scripts/vm-test.sh`

**Interfaces:**
- Produces: `scripts/install/assemble.sh VERSION OUT` (V5); `make installer [INSTALLER_VERSION=x.y.z]` → `build/install.sh`; Release-Assets `install.sh` und Tarballs mit `deploy/`; CI-Job `installer-lint`; VM-Lint-Suite führt shellcheck und `test.sh` aus.
- Consumes: Aufgabe 2 (`lib.sh` mit der Zeile `MCPANEL_VERSION=dev`, `tpl_func_suffix`, Dateinamen der Schritt-Dateien).

- [ ] **Step 1: Test für das Zusammenfügen schreiben**

`scripts/install/tests/assemble_test.sh`:

```bash
# shellcheck shell=bash
# Tests of assemble.sh (installer plan V5).

_assemble() { # _assemble VERSION -> path of the assembled script
  local repo
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
  "$repo/scripts/install/assemble.sh" "$1" "$WORK/install.sh" >/dev/null
  printf '%s' "$WORK/install.sh"
}

test_assemble_embeds_version_and_templates() {
  local f
  f="$(_assemble v1.2.3)"
  bash -n "$f"
  assert_file_contains "$f" "MCPANEL_VERSION=1.2.3"
  assert_file_contains "$f" "tpl_systemd_mcpanel_panel_socket() {"
  assert_file_contains "$f" "tpl_config_panel_toml() {"
  [[ $(head -n1 "$f") == '#!/usr/bin/env bash' ]] || fail "shebang"
  [[ $(grep -c '^#!' "$f") == 1 ]] || fail "more than one shebang"
}

test_assembled_script_renders_embedded_templates() {
  local f out
  f="$(_assemble 1.2.3)"
  out="$(MCPANEL_INSTALL_NO_MAIN=1 bash -c '
    . "$1"
    KEY_VERSION=1.2.3 KEY_INSTANCES_DIR=/srv/mc-panel
    render_template systemd/mcpanel-files.service' _ "$f")"
  assert_contains "$out" "ReadWritePaths=/srv/mc-panel/instances /srv/mc-panel/backups"
  assert_contains "$out" "install.sh 1.2.3"
}

test_assembled_script_shows_usage() {
  local f
  f="$(_assemble 1.2.3)"
  assert_exit 0 bash "$f" --help
  assert_contains "$(cat "$WORK/out")" "usage: install.sh"
  assert_exit 1 bash "$f" frobnicate
}

test_assemble_partial_runs_nothing() {
  local f
  f="$(_assemble 1.2.3)"
  head -n -1 "$f" >"$WORK/partial.sh"
  assert_exit 0 bash "$WORK/partial.sh" install --domain x.example.com --email a@b.c
  assert_eq "" "$(cat "$WORK/out")"
}

test_assemble_rejects_bad_version() {
  local repo
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
  assert_exit 2 "$repo/scripts/install/assemble.sh" '1.2;rm' "$WORK/x.sh"
  assert_exit 2 "$repo/scripts/install/assemble.sh" "" "$WORK/x.sh"
}
```

(`head -n -1` ist GNU-coreutils; die Tests laufen nur auf der VM/CI.)

- [ ] **Step 2: Rot prüfen**

Run (VM): `bash scripts/install/test.sh assemble`
Expected: FAIL – `assemble.sh: No such file or directory`.

- [ ] **Step 3: `assemble.sh` schreiben**

`scripts/install/assemble.sh` (Bash 3.2, läuft auch auf dem Mac):

```bash
#!/usr/bin/env bash
# assemble.sh VERSION OUT - joins lib.sh, the step files (alphabetical) and main.sh into one
# install.sh and embeds every file below deploy/ as a function tpl_<path> (installer plan V5).
# Bash 3.2 compatible: make installer and make check run it on macOS.
set -euo pipefail

if [ "$#" -ne 2 ]; then
  echo "usage: assemble.sh VERSION OUT" >&2
  exit 2
fi
version="${1#v}"
out="$2"
case "$version" in
"" | *[!0-9A-Za-z.+-]*)
  echo "assemble.sh: invalid version '$1'" >&2
  exit 2
  ;;
esac

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
delim=__MCPANEL_TEMPLATE_EOF__
tmp="$(mktemp "${TMPDIR:-/tmp}/install.sh.XXXXXX")"
trap 'rm -f "$tmp"' EXIT

# strip FILE: FILE without its shebang and the shellcheck shell directive.
strip() {
  sed -e '1{/^#!/d;}' -e '/^# shellcheck shell=bash$/d' "$1"
}

{
  echo '#!/usr/bin/env bash'
  echo '# shellcheck shell=bash'
  echo "# mc-panel install.sh $version - generated by scripts/install/assemble.sh, do not edit."
  echo '# Sources: https://github.com/Bernotese/mc-panel/tree/main/scripts/install and deploy/'
  strip "$here/lib.sh" | sed -e "s/^MCPANEL_VERSION=dev\$/MCPANEL_VERSION=$version/"
  for f in "$here"/*.sh; do
    case "${f##*/}" in
    lib.sh | main.sh | assemble.sh | test.sh) continue ;;
    esac
    echo
    echo "# --- ${f##*/} ---"
    strip "$f"
  done
  echo
  echo '# --- templates (deploy/) ---'
  (cd "$repo/deploy" && find . -type f | LC_ALL=C sort) | while IFS= read -r rel; do
    rel="${rel#./}"
    src="$repo/deploy/$rel"
    if grep -q "$delim" "$src"; then
      echo "assemble.sh: deploy/$rel contains $delim" >&2
      exit 1
    fi
    if [ -n "$(tail -c 1 "$src")" ]; then
      echo "assemble.sh: deploy/$rel must end with a newline" >&2
      exit 1
    fi
    fn="$(printf '%s' "$rel" | tr '/.-' '___')"
    printf "tpl_%s() {\n  cat <<'%s'\n" "$fn" "$delim"
    cat "$src"
    printf '%s\n}\n' "$delim"
  done
  echo
  echo '# --- main.sh ---'
  strip "$here/main.sh"
} >"$tmp"

if ! grep -q "^MCPANEL_VERSION=$version\$" "$tmp"; then
  echo "assemble.sh: the line MCPANEL_VERSION=dev is missing in lib.sh" >&2
  exit 1
fi
mkdir -p "$(dirname "$out")"
chmod 0755 "$tmp"
mv -f "$tmp" "$out"
trap - EXIT
```

`chmod +x scripts/install/assemble.sh scripts/install/test.sh scripts/install/main.sh`.

- [ ] **Step 4: Makefile und `.gitignore`**

In `Makefile`:

```make
# Version written into build/install.sh (release: the tag without v; goreleaser passes it).
INSTALLER_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')

# Assembles the installer (installer spec §3) into build/install.sh.
installer:
	scripts/install/assemble.sh "$(INSTALLER_VERSION)" build/install.sh
```

`installer` in die `.PHONY`-Zeile aufnehmen. Im Ziel `check` am Ende anfügen:

```make
	bash -n scripts/install/*.sh scripts/install/tests/*.sh
	scripts/install/assemble.sh 0.0.0-check build/install.sh && bash -n build/install.sh
```

Im Ziel `clean` `build` mit entfernen (`rm -rf bin build dist .dev`). `.gitignore`: Zeile `/build/` ergänzen.

Hinweis: `git describe` liefert z. B. `0.2.0-15-gabc1234-dirty`; das erfüllt die Zeichenregel von `assemble.sh`.

- [ ] **Step 5: goreleaser und Release-Workflow**

`.goreleaser.yaml`:

```yaml
before:
  hooks:
    - make web-build
    # The installer (installer spec §3) as its own release asset, with the release version.
    - make installer INSTALLER_VERSION={{ .Version }}
```

Unter `archives` (id `default`) den Block `files: [none*]` ersetzen durch:

```yaml
    # Binaries plus the templates the installer uses with --from-dir (installer spec §3).
    files:
      - src: deploy/**/*
        strip_parent: false
```

und anfügen:

```yaml
checksum:
  name_template: checksums.txt
  algorithm: sha256
  extra_files:
    - glob: ./build/install.sh

release:
  # (bestehende Schlüssel bleiben)
  extra_files:
    - glob: ./build/install.sh
```

(`checksum` und `release` existieren bereits – nur `extra_files` ergänzen.) Prüfe mit `goreleaser check` auf der VM bzw. im CI-Schritt „GoReleaser config check“, ob `strip_parent: false` die Pfade `deploy/…` im Tarball erhält; falls goreleaser die Form ablehnt, die String-Form `- deploy/**/*` verwenden.

`.github/workflows/release.yml`: Der Workflow läuft mit `--skip=before`; vor dem Schritt „GoReleaser“ einfügen:

```yaml
      - name: Assemble the installer
        run: make installer INSTALLER_VERSION="${GITHUB_REF_NAME#v}"
```

- [ ] **Step 6: CI – Snapshot-Prüfung, neuer Lint-Job, Filter**

In `.github/workflows/ci.yml`:

1. Job `changes`: Output `installer: ${{ steps.diff.outputs.installer }}` ergänzen und im Schritt „Classify changed files“ nach `out release …`:

```bash
          out installer '^scripts/install/|^deploy/|^Makefile$|^\.goreleaser\.yaml$'
```

   Den Filter `go` um `^deploy/` erweitern (`internal/config/deploy_test.go` liest die Vorlagen); den Filter `release` um `^deploy/|^scripts/install/`.

2. Neuer Job (vor `ci-ok`):

```yaml
  installer-lint:
    name: Installer (shellcheck, Bash tests)
    needs: changes
    if: needs.changes.outputs.installer == 'true'
    runs-on: ubuntu-24.04
    timeout-minutes: 10
    steps:
      - name: Checkout
        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false

      - name: Assemble
        run: make installer INSTALLER_VERSION=0.0.0-ci

      - name: shellcheck
        run: shellcheck --version && shellcheck -x scripts/install/*.sh scripts/install/tests/*.sh build/install.sh

      - name: Bash tests
        run: bash scripts/install/test.sh
```

   `installer-lint` in `ci-ok.needs` aufnehmen.

3. Job `build`, Schritt „Verify snapshot artifacts“: die Tarball-Prüfung `contents != "agent files panel"` ersetzen durch eine Prüfung, dass die drei Binaries und `deploy/systemd/mcpanel-panel.socket` enthalten sind, und die Prüfsummenzeile anpassen:

```bash
            contents="$(tar -tzf "$1" | sort)"
            for want in agent files panel deploy/systemd/mcpanel-panel.socket deploy/config/panel.toml; do
              if ! grep -qx "${want}" <<<"${contents}"; then
                echo "::error::$1 lacks ${want} (contents: $(paste -sd' ' <<<"${contents}"))"
                exit 1
              fi
            done
          done
          (cd dist && sha256sum -c --ignore-missing checksums.txt)
          want="$(sha256sum build/install.sh | awk '{ print $1 }')"
          if ! grep -qx "${want}  install.sh" dist/checksums.txt; then
            echo "::error::checksums.txt has no matching entry for install.sh"
            exit 1
          fi
          grep -q '^MCPANEL_VERSION=' build/install.sh
```

   (Die bisherige Zeile `(cd dist && sha256sum -c checksums.txt)` entfällt; `install.sh` liegt bei einem Snapshot nicht in `dist/`.)

- [ ] **Step 7: VM-Lint-Suite**

In `scripts/vm-test.sh` eine Funktion nach dem Muster der vorhandenen `need_*`-Helfer ergänzen und in `suite_lint` aufrufen:

```bash
need_shellcheck() {
  if ! command -v shellcheck >/dev/null 2>&1; then
    log "installing shellcheck"
    sudo apt-get install -y -q shellcheck >/dev/null
  fi
}
```

und am Ende von `suite_lint`:

```bash
  need_shellcheck
  log "installer: assemble, shellcheck, Bash tests"
  make installer INSTALLER_VERSION=0.0.0-vm
  shellcheck -x scripts/install/*.sh scripts/install/tests/*.sh build/install.sh
  bash scripts/install/test.sh
```

- [ ] **Step 8: Grün prüfen**

Run: `make check` (Mac: `bash -n` und Zusammenfügen), `make installer && bash build/install.sh --help` (Mac, nur Hilfe), dann `make test-vm SUITE=lint` und `make test-vm` (alle Suites).
Expected: PASS; `test.sh` meldet alle Tests `ok`; shellcheck ohne Befund.

- [ ] **Step 9: Commit, Push, PR**

```bash
git add scripts/install Makefile .gitignore .goreleaser.yaml .github/workflows/ci.yml .github/workflows/release.yml scripts/vm-test.sh
git commit -m "build(install): assemble install.sh, publish it with every release, lint it in CI

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
git push -u origin feat/installer-foundation
gh pr create --draft --title "feat(install): foundation of the installer (sub-project 6, phase A)" --body "…"
gh pr checks <n> --watch
```

Der PR-Body nennt die Abweichungen dieses Plans, die Testergebnisse (VM `test.sh`, shellcheck, Go) und endet mit `🤖 Generated with [Claude Code](https://claude.com/claude-code)`, Leerzeile, `https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R`.

---

## Self-Review

- **Spec-Abdeckung (Phase A):** §3 Aufbau (Dateien, Zusammenfügen, Logging-Ziel, Idempotenz-Helfer) → Aufgaben 2–3; §3.1 Parameter → `parse_args`/`validate_args`; §4.6 Panel-Socket-Aktivierung → Aufgabe 1 + `mcpanel-panel.socket`; §4.10 `panel admin list` → Aufgabe 1; §9 Bash-Tests ohne Abhängigkeit, Go-Socket-Test → Aufgaben 1–3; Release-Asset und `checksums.txt` → Aufgabe 3. Die eigentlichen Schritte (§4.1–4.10), `update`, `doctor`, `uninstall` sind Stubs für B1/B2.
- **Platzhalter:** keine offenen Stellen.
- **Typ-/Namenskonsistenz:** Funktionsnamen der Stubs = V4 (`pre_check` … `cmd_uninstall`), Wrapper = V2 plus `sys_chown`/`sys_runuser`; `tpl_func_suffix` und `assemble.sh` bilden Namen gleich (`/`, `.`, `-` → `_`).
- **Review Focus:** Integrität (`fetch`, `sha256_verify`), fremde Inhalte (`toml_ensure_key`, `write_file`), Teil-Download (`test_assemble_partial_runs_nothing`) haben Tests.
