# Teilprojekt 2 – agent (Aufgaben 4–10) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der agent wird zum einzigen Docker-Client von mc-panel: neue Konfiguration (`docker_socket`, `instances_dir`, `mc_user`, `[images]`), ein schmaler Docker-Engine-Client, die feste gehärtete Container-Vorlage, die Routen-Datei für mc-router, ein Manager mit serieller Warteschlange pro Instanz und die Endpunkte `/v1/instances*` – plus ein Integrationstest gegen einen echten Docker-Daemon.

**Architecture:** `cmd/agent` löst `mc_user` zu UID/GID auf, baut `docker.Client` (HTTP über den Unix-Socket, Pfadpräfix `/v1.44`) und `instances.Manager` und übergibt den Manager per `agent.WithInstances` an den bestehenden `agent.Server`. Schreibende Aufrufe (`ensure`, `power`, `remove`) validiert der Server mit `instancespec`/`agentapi`, der Manager reiht sie in die Warteschlange der Instanz ein und antwortet sofort (`202`); ein Worker pro Instanz arbeitet sie seriell gegen Docker ab und hält Fortschritt (`operation`) und Fehler (`last_error`) im Speicher. Den Ist-Zustand liest `GET /v1/instances` immer frisch aus Docker (Label `mcpanel.instance` + Name `mcpanel-mc-<id>`); CPU/RAM liefert ein Hintergrund-Sampler alle 10 s aus einem Cache. Nach jedem `ensure`/`remove` und in jeder Hintergrundrunde schreibt der Manager die Routen-Datei atomar neu, legt den Router-Container bei Bedarf an und lädt ihn per `SIGHUP` neu. Container ohne passendes Label fasst er nie an.

**Tech Stack:** Go (Stand TP1, `go.mod`: Go 1.27), `net/http` über `net.Dialer` auf `unix`, `encoding/json`, `log/slog`, `os` (`Chown`, `Rename`, `CreateTemp`), `internal/clock`; Tests nur Standardbibliothek mit einem In-Memory-Fake des Docker-Daemons (`internal/agent/docker/dockertest`) auf einem echten Unix-Socket; Integrationstest mit Build-Tag `dockerintegration` gegen Docker Engine (API 1.44, verifiziert für Docker 29.1.3 / API 1.52). Keine neuen Go-Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-10-mc-panel-instances-design.md` (Abschnitte 3.2, 3.3, 3.4, 5.1, 5.4, 10, 11)

## Global Constraints

Es gelten **alle** Global Constraints, die Review-Focus-Punkte und die Verträge V2/V3 aus `docs/superpowers/plans/2026-10-10-instances-00-overview.md` (und damit auch die aus TP1 und `CLAUDE.md`). Die Aufgaben 1–3 sind erledigt: `instancespec` und die Instanz-Typen in `agentapi` samt Client existieren.

Agent-spezifische, verbindliche Werte dieses Teilplans:

| Thema | Wert |
|---|---|
| Eigene Pfade | `internal/config/` (nur `Agent`/`Images`), `internal/agent/docker/**`, `internal/agent/instances/**`, `internal/agent/server.go`, `cmd/agent/**`, `.golangci.yml` (Build-Tag), `.github/workflows/ci.yml` (ein Schritt) |
| Konfiguration (Prod-Defaults) | `docker_socket = "/var/run/docker.sock"`, `instances_dir = "/srv/mc-panel"`, `mc_user = "mcpanel-mc"`, `[images]` = gepinnte Defaults aus der Übersicht; unbekannte Schlüssel (auch in `[images]`) → Startfehler |
| Konfiguration (Dev) | `docker_socket` = erster existierender Pfad aus `$HOME/.orbstack/run/docker.sock`, `$HOME/.docker/run/docker.sock`, `/var/run/docker.sock` (sonst `/var/run/docker.sock` → Docker „unavailable“); `instances_dir = <root>/.dev/mc`; `mc_user = ""` = UID/GID des agent-Prozesses |
| Validierung | `docker_socket`, `instances_dir`: sauberer absoluter Pfad; `instances_dir` ≠ `/`, ohne `:` und `,` (Bind-Syntax); `mc_user` außerhalb von Dev Pflicht; jedes Image `^[A-Za-z0-9][A-Za-z0-9._/:-]*@sha256:[0-9a-f]{64}$`; `mc_user` mit UID 0 oder GID 0 außerhalb von Dev → Startfehler |
| Image-Referenz an Docker | immer die gepinnte Form `repo@sha256:<hex>` (`docker.ImageRef.Pinned`); der Tag aus der Konfiguration ist nur Doku. Pull: `POST /images/create?fromImage=<repo>&tag=sha256:<hex>`; Inspect: `GET /images/<repo>@sha256:<hex>/json` |
| Docker-Client | Präfix `/v1.44`, Basis-URL `http://docker`, kein Proxy, keine Redirects; Timeout 30 s pro Aufruf, Pull 30 min, Stop/Restart `t` + 30 s; Antwort ≤ 8 MiB, Pull-Stream ≤ 64 MiB, Fehlerkörper ≤ 64 KiB; Verbindungsfehler/kaputte Antworten → `docker.ErrUnavailable`; Nicht-2xx → `*docker.Error{StatusCode, Message}`; `304` bei Start/Stop = Erfolg |
| Verzeichnisse | `instances_dir` root 0755; `instances/` root 0700; `instances/<id>` `<uid>:<gid>` 0750; `router/` root 0755; `router/routes.json` 0644; `deleted/` root 0700; Archiv `deleted/<name>-<YYYYMMDDTHHMMSSZ>` (UTC, `clock.Clock`), bei Kollision Suffix `-2`, `-3`, … |
| Instanz-Container | exakt Global Constraints der Übersicht; zusätzlich: `Hostname = name`, `NetworkingConfig.EndpointsConfig = {"mcpanel-mc": {}}`, `Privileged false`, `ReadonlyRootfs false`, kein `Cmd`, keine `ExposedPorts`, Env ausschließlich `instancespec.Env` |
| Router-Container | `mcpanel-router`, Label `mcpanel.router=1`, `Cmd ["-routes-config","/config/routes.json"]`, `ExposedPorts {"25565/tcp"}`, `PortBindings {"25565/tcp": [{HostPort:"25565"}]}`, Bind `<instances_dir>/router:/config:ro`, `CapDrop ALL`, `no-new-privileges:true`, `Memory = MemorySwap = 256 MiB`, `PidsLimit 1024`, `unless-stopped`, gleiche `LogConfig`, kein `User` (Image läuft als 65532) |
| Netz | `mcpanel-mc`, Treiber `bridge`, Label `mcpanel.network=1` |
| Fremde Container | verwaltet ist nur, wer Label `mcpanel.instance=<id>` **und** Namen `mcpanel-mc-<id>` trägt; Router nur mit `mcpanel.router=1`. Alles andere: keine Schreiboperation, nur `last_error` bzw. `RouterStatus.LastError` |
| Warteschlange | pro Instanz max. **4 offene** Operationen (laufend + wartend), max. **256** Instanzen mit Zustand im Speicher; sonst `agentapi.ErrBusy` → `429` `code: "busy"` |
| Operationen | ensure: `creating` (`pulling` während Pull; bei recreate `stopping` → `removing` → `creating` → ggf. `starting`); power start: `starting`; stop: `stopping`; restart: `stopping` → `starting` (Stop + Start, nicht `/restart`); remove: `stopping` → `removing` |
| `last_error` | ≤ 500 Zeichen (Runen), Steuerzeichen → Leerzeichen, CurseForge-Key → `[redacted]`; wird bei der nächsten erfolgreichen Operation geleert |
| Hintergrund | `Run`: sofort und alle `SampleInterval = 10 s` ein `Tick` (Ping mit 5 s Timeout → Routen/Router → Stats); Ticker wie `internal/defense/sync.go`, Zeitstempel über `clock.Clock` |
| Draht | `GET /v1/instances` immer `200` (ohne Docker: `docker:"unavailable"`, `instances:[]`, `router.state:"missing"`); Schreibende: `202` ohne Body, `400`, `405`, `413` (> 16 KiB), `415`, `429` (`busy`), `503` (`docker_unavailable`), `500` (`instance operation failed`, Details nur im Log) |
| Logging | `instance operation queued`/`done`/`failed` (id, op, action, revision – nie Spec/Env), `instance operation accepted` (Server), `docker available`/`docker unavailable`, `router ready`/`router not ready`/`router reloaded`, `router container created`, `pulling image`, `instance data archived` |
| Build-Tags | `linux && dockerintegration` (nur Integrationstest); sonst kein plattformspezifischer Code (`os.Chown` existiert auch auf macOS) |

## Review Focus

1. **Fremde Container (Übersicht #2):** Container `mcpanel-mc-7` ohne `mcpanel.instance`-Label bzw. mit Label für eine andere ID wird weder gestoppt, entfernt, ersetzt noch geroutet; seine Daten bleiben liegen. → `TestForeignContainersAreNeverTouched`, `TestForeignRouterIsNeverTouched` (Aufgabe 8), `TestRoutesFromContainers` (Aufgabe 7).
2. **Env-Injektion (Übersicht #1):** Die Vorlage erzeugt nie andere Env-Schlüssel als die Allowlist, nie Ports/Mounts/Caps; ungültige Specs erreichen Docker nicht. → `TestInstanceContainerInvariants`, `TestInstanceContainerRejectsInvalidRequests` (Aufgabe 6), `TestServerRejectsInvalidInstanceRequests` (Aufgabe 9), `TestInvalidRequestsAreRejected` (Aufgabe 8).
3. **Secrets in `last_error`/Logs:** Ein Docker-Fehler, der den CurseForge-Key enthält, darf ihn weder in `last_error` noch im Log zeigen. → `TestLastErrorIsRedactedAndBounded` (Aufgabe 8).
4. **Routen-Datei:** atomar (temp + fsync + rename), kein `SIGHUP` ohne Änderung, Datei existiert vor dem Router-Container. → `TestWriteRoutesAtomicAndIdempotent` (Aufgabe 7), `TestTickManagesRouter`, `TestRemoveArchivesDataAndRoute` (Aufgabe 8).
5. **Warteschlange:** seriell pro Instanz, 5. offene Operation → `busy`, andere Instanzen unabhängig. → `TestQueueIsSerialAndBounded` (Aufgabe 8), `TestServerInstanceErrorMapping` (Aufgabe 9).

## Dateistruktur

```
internal/config/config.go                         (4)  Agent: docker_socket, instances_dir, mc_user, [images]; Images, DefaultImages, ByJava
internal/config/agent_instances_test.go           (4)
cmd/agent/main.go                                 (4, 9) mcUserIDs; Verdrahtung docker.Client + instances.Manager + Run/Close
cmd/agent/main_test.go                            (4, 9)

internal/agent/docker/types.go                    (5)  ContainerConfig/HostConfig/…, ContainerSummary, ContainerInfo, Stats, ImageRef
internal/agent/docker/client.go                   (5)  Client (Ping, ImageExists, PullImage, EnsureNetwork, CreateContainer, Start,
                                                       Stop, Restart, Kill, Remove, Inspect, List, Stats), Error, ErrUnavailable
internal/agent/docker/client_test.go              (5)
internal/agent/docker/dockertest/daemon.go        (5)  In-Memory-Fake des Docker-Daemons auf Unix-Socket (für Aufgaben 5, 8, 9)
internal/agent/docker/integration_linux_test.go   (10) //go:build linux && dockerintegration

internal/agent/instances/template.go              (6)  Konstanten/Namen/Pfade, InstanceContainer, RouterContainer
internal/agent/instances/template_test.go         (6)
internal/agent/instances/router.go                (7)  managedID, Routes, RenderRoutes, WriteRoutes
internal/agent/instances/router_test.go           (7)
internal/agent/instances/worker.go                (8)  Warteschlange pro Instanz, last_error, Wait/Close
internal/agent/instances/manager.go               (8)  Config, Manager, ensure/power/remove, Router, Status, Stats, Tick/Run
internal/agent/instances/datadir.go               (8)  Datenverzeichnis anlegen (chown) und archivieren
internal/agent/instances/manager_test.go          (8)

internal/agent/server.go                          (9)  Instances-Interface, WithInstances, /v1/instances*
internal/agent/server_instances_test.go           (9)
.golangci.yml, .github/workflows/ci.yml           (10)
```

Abhängigkeiten (keine Zyklen): `docker` → nur Standardbibliothek; `docker/dockertest` → `docker`; `instances` → `docker`, `agentapi`, `instancespec`, `config`, `clock`; `agent` (Server) → `agentapi`, `instancespec`, `banrules`, `peercred` (kennt `instances` nur über das Interface `agent.Instances`); `cmd/agent` → alles. Das panel importiert weiterhin nur `internal/agent` (Client) und `agentapi`/`instancespec` – **nie** `docker` oder `instances`.

## Ergänzungen zu den Verträgen (V3) – nur agent-intern

Die Übersicht bleibt unverändert; diese Namen entstehen zusätzlich und werden vom panel nicht benötigt:

- `agent.NewServer(fw, version, log, opts ...agent.Option)` – variadisch, alle bestehenden Aufrufe kompilieren weiter; `agent.WithInstances(agent.Instances)`.
- `instances.Config.Customize func(name string, cfg *docker.ContainerConfig)` – nur der Integrationstest setzt es (Testimage statt itzg); `cmd/agent` nie.
- Datei `internal/agent/instances/datadir.go` und Paket `internal/agent/docker/dockertest` (in der Übersicht nicht gelistet).

---

### Task 4: agent-Konfiguration (`docker_socket`, `instances_dir`, `mc_user`, `[images]`)

**Files:**
- Modify: `internal/config/config.go` (Typ `Images`, Felder in `Agent`, Defaults, Dev-Profil, Validierung)
- Create: `internal/config/agent_instances_test.go`
- Modify: `cmd/agent/main.go` (`mcUserIDs`, Aufruf in `run`, Start-Log)
- Modify: `cmd/agent/main_test.go` (`TestMCUserIDs`)

**Interfaces:**
- Consumes: `config.Agent`, `config.DefaultAgent`, `config.DevAgent`, `config.LoadAgent` (TP1)
- Produces:
```go
package config

type Images struct {
	Java8  string `toml:"java8"`
	Java17 string `toml:"java17"`
	Java21 string `toml:"java21"`
	Java25 string `toml:"java25"`
	Router string `toml:"router"`
}
func DefaultImages() Images
func (i Images) ByJava(java string) (string, bool) // "java8" … "java25"

const (
	DefaultDockerSocket = "/var/run/docker.sock"
	DefaultInstancesDir = "/srv/mc-panel"
	DefaultMCUser       = "mcpanel-mc"
)

type Agent struct {
	Dev          bool   `toml:"dev"`
	Socket       string `toml:"socket"`
	PanelUID     int    `toml:"panel_uid"`
	Firewall     string `toml:"firewall"`
	DockerSocket string `toml:"docker_socket"`
	InstancesDir string `toml:"instances_dir"`
	MCUser       string `toml:"mc_user"`
	Images       Images `toml:"images"`
}

// package main (cmd/agent)
func mcUserIDs(cfg config.Agent, lookup func(name string) (*user.User, error)) (uid, gid int, err error)
```

- [ ] **Step 1: Failing tests schreiben**

`internal/config/agent_instances_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/config"
)

const testDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func validProdAgent() config.Agent {
	a := config.DefaultAgent()
	a.PanelUID = 998
	return a
}

func TestAgentInstanceDefaults(t *testing.T) {
	a := validProdAgent()
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if a.DockerSocket != "/var/run/docker.sock" || a.InstancesDir != "/srv/mc-panel" || a.MCUser != "mcpanel-mc" {
		t.Fatalf("defaults = %+v", a)
	}
	if a.Images != config.DefaultImages() {
		t.Fatalf("images = %+v", a.Images)
	}
	for _, java := range []string{"java8", "java17", "java21", "java25"} {
		ref, ok := a.Images.ByJava(java)
		if !ok || !strings.HasPrefix(ref, "itzg/minecraft-server:2026.9.2-"+java+"@sha256:") {
			t.Errorf("ByJava(%s) = %q, %v", java, ref, ok)
		}
	}
	if _, ok := a.Images.ByJava("java11"); ok {
		t.Error("ByJava(java11) must fail")
	}
	if !strings.HasPrefix(a.Images.Router, "itzg/mc-router:1.47.2@sha256:") {
		t.Errorf("router = %q", a.Images.Router)
	}
}

func TestDevAgentInstanceSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := config.DevAgent("/repo")
	if err := a.Validate(); err != nil {
		t.Fatalf("dev agent invalid: %v", err)
	}
	// Without OrbStack/Docker Desktop the default path remains (it may or may not exist).
	if a.InstancesDir != "/repo/.dev/mc" || a.MCUser != "" || a.DockerSocket != "/var/run/docker.sock" {
		t.Fatalf("dev agent without docker = %+v", a)
	}

	desktop := filepath.Join(home, ".docker", "run", "docker.sock")
	orb := filepath.Join(home, ".orbstack", "run", "docker.sock")
	for _, p := range []string{desktop, orb} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(desktop, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := config.DevAgent("/repo").DockerSocket; got != desktop {
		t.Fatalf("with Docker Desktop: docker_socket = %q, want %q", got, desktop)
	}
	if err := os.WriteFile(orb, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := config.DevAgent("/repo").DockerSocket; got != orb {
		t.Fatalf("OrbStack must win: docker_socket = %q, want %q", got, orb)
	}
}

func TestAgentInstanceValidation(t *testing.T) {
	cases := map[string]func(*config.Agent){
		"relative docker socket": func(a *config.Agent) { a.DockerSocket = "docker.sock" },
		"empty docker socket":    func(a *config.Agent) { a.DockerSocket = "" },
		"unclean instances dir":  func(a *config.Agent) { a.InstancesDir = "/srv/../etc" },
		"root instances dir":     func(a *config.Agent) { a.InstancesDir = "/" },
		"colon in instances dir": func(a *config.Agent) { a.InstancesDir = "/srv/mc:panel" },
		"comma in instances dir": func(a *config.Agent) { a.InstancesDir = "/srv/mc,panel" },
		"empty mc_user in prod":  func(a *config.Agent) { a.MCUser = "" },
		"tag without digest":     func(a *config.Agent) { a.Images.Java21 = "itzg/minecraft-server:java21" },
		"short digest":           func(a *config.Agent) { a.Images.Router = "itzg/mc-router@sha256:abc" },
		"upper case digest":      func(a *config.Agent) { a.Images.Java8 = "itzg/minecraft-server@" + strings.ToUpper(testDigest) },
		"whitespace":             func(a *config.Agent) { a.Images.Java17 = "itzg/minecraft-server @" + testDigest },
		"empty image":            func(a *config.Agent) { a.Images.Java25 = "" },
	}
	for name, mod := range cases {
		a := validProdAgent()
		mod(&a)
		if err := a.Validate(); err == nil {
			t.Errorf("%s: Validate = nil, want error", name)
		}
	}
	ok := validProdAgent()
	ok.Images.Java21 = "registry.example.com:5000/mc/server:v1@" + testDigest
	ok.Images.Router = "itzg/mc-router@" + testDigest
	if err := ok.Validate(); err != nil {
		t.Fatalf("registry with port / digest without tag: %v", err)
	}
	dev := config.DevAgent("/repo")
	dev.MCUser = ""
	if err := dev.Validate(); err != nil {
		t.Fatalf("dev mode allows an empty mc_user: %v", err)
	}
}

func TestLoadAgentInstanceKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.toml")
	content := `panel_uid = 998
docker_socket = "/run/docker.sock"
instances_dir = "/var/lib/mc"
mc_user = "minecraft"
[images]
router = "itzg/mc-router@` + testDigest + `"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := config.LoadAgent(path, false, "")
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if a.DockerSocket != "/run/docker.sock" || a.InstancesDir != "/var/lib/mc" || a.MCUser != "minecraft" {
		t.Fatalf("agent = %+v", a)
	}
	if a.Images.Router != "itzg/mc-router@"+testDigest || a.Images.Java21 != config.DefaultImages().Java21 {
		t.Fatalf("images must overlay the defaults key by key: %+v", a.Images)
	}
}

func TestLoadAgentRejectsUnknownImageKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.toml")
	content := "panel_uid = 998\n[images]\njava11 = \"x@" + testDigest + "\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadAgent(path, false, ""); err == nil || !strings.Contains(err.Error(), "java11") {
		t.Fatalf("expected unknown key error, got %v", err)
	}
}

func TestLoadAgentRejectsUnpinnedImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.toml")
	content := "panel_uid = 998\n[images]\njava21 = \"itzg/minecraft-server:java21\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadAgent(path, false, ""); err == nil || !strings.Contains(err.Error(), "images.java21") {
		t.Fatalf("expected digest error, got %v", err)
	}
}
```

Ans Ende von `cmd/agent/main_test.go` anhängen (Importe `errors`, `os`, `os/user`, `strings`, `config` sind dort schon vorhanden):

```go
func TestMCUserIDs(t *testing.T) {
	noLookup := func(string) (*user.User, error) { return nil, errors.New("lookup must not run") }
	uid, gid, err := mcUserIDs(config.Agent{Dev: true}, noLookup)
	if err != nil || uid != os.Getuid() || gid != os.Getgid() {
		t.Fatalf("dev without mc_user = %d:%d, %v; want own ids", uid, gid, err)
	}
	if _, _, err := mcUserIDs(config.Agent{}, noLookup); err == nil {
		t.Fatal("prod without mc_user must fail")
	}

	var asked string
	uid, gid, err = mcUserIDs(config.Agent{MCUser: "mcpanel-mc"}, func(name string) (*user.User, error) {
		asked = name
		return &user.User{Username: name, Uid: "996", Gid: "995"}, nil
	})
	if err != nil || uid != 996 || gid != 995 || asked != "mcpanel-mc" {
		t.Fatalf("prod = %d:%d, %v (asked %q)", uid, gid, err, asked)
	}

	for name, u := range map[string]*user.User{
		"root uid":        {Uid: "0", Gid: "995"},
		"root gid":        {Uid: "996", Gid: "0"},
		"non-numeric uid": {Uid: "mc", Gid: "995"},
		"non-numeric gid": {Uid: "996", Gid: "staff"},
		"negative uid":    {Uid: "-1", Gid: "995"},
	} {
		if _, _, err := mcUserIDs(config.Agent{MCUser: "mcpanel-mc"}, func(string) (*user.User, error) { return u, nil }); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, _, err := mcUserIDs(config.Agent{MCUser: "mcpanel-mc"}, func(name string) (*user.User, error) {
		return nil, user.UnknownUserError(name)
	}); err == nil || !strings.Contains(err.Error(), "useradd") {
		t.Fatalf("unknown mc_user: %v", err)
	}
	if _, _, err := mcUserIDs(config.Agent{Dev: true, MCUser: "me"}, func(string) (*user.User, error) {
		return &user.User{Uid: "0", Gid: "0"}, nil
	}); err != nil {
		t.Fatalf("dev may run as root (e.g. agent -dev under sudo): %v", err)
	}
}
```

- [ ] **Step 2: Tests laufen lassen (rot)**

Run: `go test ./internal/config ./cmd/agent -count=1`
Expected: FAIL beim Kompilieren, u. a. `a.DockerSocket undefined (type config.Agent has no field or method DockerSocket)`, `undefined: config.DefaultImages` und `undefined: mcUserIDs`.

- [ ] **Step 3: `internal/config/config.go` erweitern**

Import `"regexp"` ergänzen (zwischen `"path/filepath"` und `"strings"`). Dann **alles ab der Zeile `type Agent struct {` bis zum Dateiende** durch folgenden Block ersetzen (`DefaultAgent`, `DevAgent`, `LoadAgent`, `Validate` ändern sich mit; `LoadAgent` bleibt inhaltlich gleich):

```go
// Images are the digest-pinned container images the agent may run. The panel only
// ever chooses a key (java8 … java25), never an image reference.
type Images struct {
	Java8  string `toml:"java8"`
	Java17 string `toml:"java17"`
	Java21 string `toml:"java21"`
	Java25 string `toml:"java25"`
	Router string `toml:"router"`
}

// DefaultImages are the pinned defaults (verified 2026-10-10).
func DefaultImages() Images {
	return Images{
		Java8:  "itzg/minecraft-server:2026.9.2-java8@sha256:a23a59bc9bd520d2c1bae97225b8672771cf82c90073ea36dc18f18c199f325b",
		Java17: "itzg/minecraft-server:2026.9.2-java17@sha256:0198cb9f0c7e7b1747645040e1245a6915c122662f3106f3b0979473a953ab61",
		Java21: "itzg/minecraft-server:2026.9.2-java21@sha256:edf50d431ddc6f73929368e82f4334a405796874f7b0ae11f1edcbb267d670aa",
		Java25: "itzg/minecraft-server:2026.9.2-java25@sha256:de5d1b1a83eba576f6c8a688fac2a3523ce457724cdebc8ea48d7818b74cdf6e",
		Router: "itzg/mc-router:1.47.2@sha256:64ae69eaa7a6800a79bbf962f027f4020878efa25183359115bc7f6ee569c36e",
	}
}

// ByJava returns the image for a java key ("java8", "java17", "java21", "java25").
func (i Images) ByJava(java string) (string, bool) {
	switch java {
	case "java8":
		return i.Java8, true
	case "java17":
		return i.Java17, true
	case "java21":
		return i.Java21, true
	case "java25":
		return i.Java25, true
	default:
		return "", false
	}
}

// imageRe requires a digest: a tag can be moved upstream, a digest cannot.
var imageRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:-]*@sha256:[0-9a-f]{64}$`)

func (i Images) validate() []error {
	var errs []error
	for _, e := range []struct{ key, ref string }{
		{"java8", i.Java8}, {"java17", i.Java17}, {"java21", i.Java21}, {"java25", i.Java25}, {"router", i.Router},
	} {
		if !imageRe.MatchString(e.ref) {
			errs = append(errs, fmt.Errorf("images.%s: %q must be a reference pinned with @sha256:<64 hex>", e.key, e.ref))
		}
	}
	return errs
}

const (
	DefaultDockerSocket = "/var/run/docker.sock"
	DefaultInstancesDir = "/srv/mc-panel"
	DefaultMCUser       = "mcpanel-mc"
)

type Agent struct {
	Dev          bool   `toml:"dev"`
	Socket       string `toml:"socket"`
	PanelUID     int    `toml:"panel_uid"`
	Firewall     string `toml:"firewall"` // "nftables" | "noop"
	DockerSocket string `toml:"docker_socket"`
	InstancesDir string `toml:"instances_dir"`
	// MCUser is the system user the Minecraft containers run as. Empty is only
	// allowed in dev mode and means the uid:gid of the agent process.
	MCUser string `toml:"mc_user"`
	Images Images `toml:"images"`
}

func DefaultAgent() Agent {
	return Agent{
		Socket:       "/run/mc-panel/agent.sock",
		PanelUID:     -1,
		Firewall:     "nftables",
		DockerSocket: DefaultDockerSocket,
		InstancesDir: DefaultInstancesDir,
		MCUser:       DefaultMCUser,
		Images:       DefaultImages(),
	}
}

func DevAgent(root string) Agent {
	a := DefaultAgent()
	a.Dev = true
	a.Socket = filepath.Join(root, ".dev", "agent.sock")
	a.PanelUID = os.Getuid()
	a.Firewall = "noop"
	a.DockerSocket = devDockerSocket()
	a.InstancesDir = filepath.Join(root, ".dev", "mc")
	a.MCUser = ""
	return a
}

// devDockerSocket returns the first existing socket of OrbStack, Docker Desktop or
// the system daemon. Without any, it returns the default path and the agent reports
// Docker as unavailable.
func devDockerSocket() string {
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".orbstack", "run", "docker.sock"),
			filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	for _, c := range append(candidates, DefaultDockerSocket) {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return DefaultDockerSocket
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
	// The CLI flag is authoritative: the file must not be able to enable or disable dev mode.
	a.Dev = dev
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
	if err := cleanAbsPath("docker_socket", a.DockerSocket); err != nil {
		errs = append(errs, err)
	}
	if err := cleanAbsPath("instances_dir", a.InstancesDir); err != nil {
		errs = append(errs, err)
	} else if a.InstancesDir == "/" || strings.ContainsAny(a.InstancesDir, ":,") {
		// ':' and ',' would break Docker's "src:dst[:opts]" bind syntax.
		errs = append(errs, fmt.Errorf("instances_dir: %q must not be / and must not contain ':' or ','", a.InstancesDir))
	}
	if a.MCUser == "" && !a.Dev {
		errs = append(errs, errors.New("mc_user must be set (system user for the Minecraft containers)"))
	}
	errs = append(errs, a.Images.validate()...)
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return nil
}

func cleanAbsPath(key, p string) error {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsRune(p, 0) {
		return fmt.Errorf("%s: %q must be a clean absolute path", key, p)
	}
	return nil
}
```

Hinweise:
- `toml.DecodeFile` überlagert `[images]` schlüsselweise (nur gesetzte Schlüssel ersetzen den Default); unbekannte Schlüssel in `[images]` meldet `md.Undecoded()` wie bisher.
- `DevAgent` liest `$HOME` über `os.UserHomeDir`; der Test setzt `HOME` per `t.Setenv`.
- Die bestehenden Tests in `config_test.go` und `internal/agent/firewall/factory_test.go` (Literal `config.Agent{…}` ohne neue Felder, ohne `Validate`) bleiben unverändert grün.

- [ ] **Step 4: `cmd/agent/main.go` – `mc_user` auflösen**

In `run` direkt nach dem Block `mode, gid, err := socketPerms(cfg, user.LookupId)` … `}` einfügen:

```go
	mcUID, mcGID, err := mcUserIDs(cfg, user.Lookup)
	if err != nil {
		return err
	}
```

Den Start-Log erweitern (ersetzt die bisherige zweizeilige `log.Info("agent started", …)`-Anweisung):

```go
	log.Info("agent started", "version", version, "socket", cfg.Socket, "firewall", fw.Name(),
		"panel_uid", panelUID, "socket_mode", fmt.Sprintf("%#o", mode), "dev", cfg.Dev,
		"docker_socket", cfg.DockerSocket, "instances_dir", cfg.InstancesDir, "mc_uid", mcUID, "mc_gid", mcGID)
```

Vor `func uidFromConfig` einfügen:

```go
// mcUserIDs resolves mc_user to the uid:gid the Minecraft containers run as. In dev
// mode an empty mc_user means the agent's own ids. Outside dev mode root (uid or
// gid 0) is refused: the containers must never run as root on the host.
func mcUserIDs(cfg config.Agent, lookup func(name string) (*user.User, error)) (int, int, error) {
	if cfg.MCUser == "" {
		if !cfg.Dev {
			return 0, 0, errors.New("mc_user must be set")
		}
		return os.Getuid(), os.Getgid(), nil
	}
	u, err := lookup(cfg.MCUser)
	if err != nil {
		return 0, 0, fmt.Errorf("look up mc_user %q (create it with: useradd --system --no-create-home --shell /usr/sbin/nologin %s): %w",
			cfg.MCUser, cfg.MCUser, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil || uid < 0 {
		return 0, 0, fmt.Errorf("mc_user %q has invalid uid %q", cfg.MCUser, u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil || gid < 0 {
		return 0, 0, fmt.Errorf("mc_user %q has invalid gid %q", cfg.MCUser, u.Gid)
	}
	if !cfg.Dev && (uid == 0 || gid == 0) {
		return 0, 0, fmt.Errorf("mc_user %q resolves to uid %d gid %d: Minecraft containers must not run as root", cfg.MCUser, uid, gid)
	}
	return uid, gid, nil
}
```

Damit startet ein Prod-agent ohne existierenden Benutzer `mcpanel-mc` nicht mehr (Fehlermeldung nennt den `useradd`-Befehl). Die VM legt den Benutzer erst in Aufgabe 23 an; wer vorher in der VM testet, legt ihn einmalig per `sudo useradd --system --user-group --home-dir /nonexistent --no-create-home --shell /usr/sbin/nologin mcpanel-mc` an.

- [ ] **Step 5: Tests laufen lassen (grün)**

Run: `go test ./internal/config ./cmd/agent ./internal/agent/... -count=1`
Expected: PASS (inkl. der bestehenden Config-, Firewall- und agent-Tests).

- [ ] **Step 6: Lint**

Run: `golangci-lint run ./internal/config/... ./cmd/agent/...`
Expected: `0 issues.`

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/agent_instances_test.go cmd/agent/main.go cmd/agent/main_test.go
git commit -m "feat(config): docker socket, instances dir, mc user and pinned images for the agent

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: `internal/agent/docker` – schmaler Docker-Engine-Client und Fake-Daemon

**Files:**
- Create: `internal/agent/docker/types.go` (Request-/Response-Strukturen, `ImageRef`)
- Create: `internal/agent/docker/client.go`
- Create: `internal/agent/docker/dockertest/daemon.go` (In-Memory-Fake, wiederverwendet in Aufgaben 8 und 9)
- Test: `internal/agent/docker/client_test.go`

**Interfaces:**
- Consumes: nur Standardbibliothek
- Produces:
```go
package docker

const (
	APIVersion         = "/v1.44"
	DefaultTimeout     = 30 * time.Second
	DefaultPullTimeout = 30 * time.Minute
)
var ErrUnavailable error // daemon not reachable / broken answer
type Error struct{ StatusCode int; Method, Path, Message string } // non-2xx answer
func IsNotFound(err error) bool
func IsConflict(err error) bool

type Client struct{ /* … */ }
func New(socketPath string) *Client
func NewWithTimeouts(socketPath string, timeout, pullTimeout time.Duration) *Client
func (c *Client) Ping(ctx context.Context) error
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error)
func (c *Client) PullImage(ctx context.Context, ref string) error
func (c *Client) EnsureNetwork(ctx context.Context, name string, labels map[string]string) error
func (c *Client) CreateContainer(ctx context.Context, name string, cfg ContainerConfig) (string, error)
func (c *Client) Start(ctx context.Context, id string) error
func (c *Client) Stop(ctx context.Context, id string, timeoutSeconds int) error
func (c *Client) Restart(ctx context.Context, id string, timeoutSeconds int) error
func (c *Client) Kill(ctx context.Context, id, signal string) error
func (c *Client) Remove(ctx context.Context, id string, force bool) error
func (c *Client) Inspect(ctx context.Context, id string) (ContainerInfo, error)
func (c *Client) List(ctx context.Context, label string) ([]ContainerSummary, error)
func (c *Client) Stats(ctx context.Context, id string) (Stats, error)

type ContainerConfig, HostConfig, PortBinding, RestartPolicy, LogConfig, NetworkingConfig, EndpointSettings
type ContainerSummary (+ HasName(name string) bool), ContainerInfo, ContainerState, InfoConfig
type Stats, CPUStats, CPUUsage, MemoryStats
type ImageRef struct{ Repo, Tag, Digest string }
func ParseImageRef(ref string) (ImageRef, error)
func (r ImageRef) Pinned() string // "repo@sha256:…"

package dockertest
const OpPing, OpImage, OpPull, OpNetwork, OpCreate, OpStart, OpStop, OpRestart, OpKill, OpRemove, OpInspect, OpList, OpStats
type Container struct{ ID, Name string; Config docker.ContainerConfig; Status string; ExitCode int; StartedAt time.Time; Kills []string; StopTimes []int }
type Daemon struct{ Socket string /* … */ }
func New(t testing.TB) *Daemon
func (d *Daemon) AddImage(pinned string)
func (d *Daemon) AddContainer(name string, cfg docker.ContainerConfig, running bool) Container
func (d *Daemon) Container(name string) (Container, bool)
func (d *Daemon) SetState(name, status string, exitCode int)
func (d *Daemon) SetStats(name string, s docker.Stats)
func (d *Daemon) HasNetwork(name string) bool
func (d *Daemon) HasImage(pinned string) bool
func (d *Daemon) Pulls() []string  // "repo@sha256:…"
func (d *Daemon) Calls() []string  // "<op> <container name | image>"
func (d *Daemon) Fail(op string, status int, message string)
func (d *Daemon) FailPull(message string) // 200 + error line in the stream
func (d *Daemon) Clear()
func (d *Daemon) Hold(op string) (arrived <-chan string, release func())
```

Entscheidungen:
- **Ein eigenes Fehlerbild statt Statuscode-Raten:** Transportfehler, Timeouts und unlesbare Antworten wrappen `ErrUnavailable` (der Manager meldet dann Docker als „unavailable“); jede Nicht-2xx-Antwort ist `*Error` mit Docker's `{"message": …}`. `304 Not Modified` (Start eines laufenden, Stop eines gestoppten Containers) behandeln `Start`/`Stop` als Erfolg.
- **Pull prüft den Stream:** Docker antwortet auch bei fehlgeschlagenem Pull mit `200` und meldet den Fehler in einer JSON-Zeile (`error`/`errorDetail`). `PullImage` liest den Stream bis EOF und gibt die erste Fehlermeldung zurück.
- **Nur gepinnte Images:** `ImageExists`/`PullImage` akzeptieren nur `…@sha256:<64 hex>` und arbeiten mit `repo@sha256:…` (`ImageRef.Pinned`), weil Docker einen Tag neben einem Digest ignoriert und der containerd-Image-Store die Form `name:tag@digest` beim Inspect nicht zuverlässig auflöst.
- **Fake auf echtem Unix-Socket:** `dockertest.Daemon` implementiert genau die verwendeten Endpunkte inkl. Docker-Eigenheiten (Präfix `/v1.44` Pflicht, `304`, `409` bei Namenskonflikt/Kill auf gestopptem Container/Remove ohne `force`, `404` ohne Image/Netz, Label-Filter `key` bzw. `key=value`, `all=1`). Dekodiert `ContainerConfig` mit `DisallowUnknownFields`, damit Tests genau sehen, was Docker erhält. `Hold` blockiert einen Endpunkt, um Warteschlangen ohne `sleep` zu testen.

- [ ] **Step 1: Failing test schreiben** – `internal/agent/docker/client_test.go`

```go
package docker_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
)

const (
	digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	image  = "itzg/minecraft-server:2026.9.2-java21@" + digest
	pinned = "itzg/minecraft-server@" + digest
)

func TestParseImageRef(t *testing.T) {
	r, err := docker.ParseImageRef(image)
	if err != nil || r != (docker.ImageRef{Repo: "itzg/minecraft-server", Tag: "2026.9.2-java21", Digest: digest}) || r.Pinned() != pinned {
		t.Fatalf("ParseImageRef = %+v, %v", r, err)
	}
	r, err = docker.ParseImageRef("registry.example.com:5000/mc/server@" + digest)
	if err != nil || r.Repo != "registry.example.com:5000/mc/server" || r.Tag != "" {
		t.Fatalf("registry with port = %+v, %v", r, err)
	}
	for _, bad := range []string{
		"itzg/minecraft-server:java21",
		"itzg/minecraft-server@sha256:abc",
		"Itzg/minecraft-server@" + digest,
		"itzg/minecraft-server:bad tag@" + digest,
		"@" + digest,
		"itzg//x@" + digest,
		"itzg/minecraft-server@" + digest + "/../x",
	} {
		if _, err := docker.ParseImageRef(bad); err == nil {
			t.Errorf("ParseImageRef(%q) = nil error", bad)
		}
	}
}

func TestPingAndUnavailable(t *testing.T) {
	d := dockertest.New(t)
	ctx := context.Background()
	if err := docker.New(d.Socket).Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	err := docker.New(filepath.Join(t.TempDir(), "missing.sock")).Ping(ctx)
	if !errors.Is(err, docker.ErrUnavailable) {
		t.Fatalf("Ping on a missing socket = %v, want ErrUnavailable", err)
	}
	d.Fail(dockertest.OpPing, http.StatusInternalServerError, "daemon broken")
	err = docker.New(d.Socket).Ping(ctx)
	var de *docker.Error
	if !errors.As(err, &de) || de.StatusCode != 500 || de.Message != "daemon broken" {
		t.Fatalf("Ping on a failing daemon = %v", err)
	}
}

func TestImagePull(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	ctx := context.Background()
	ok, err := c.ImageExists(ctx, image)
	if err != nil || ok {
		t.Fatalf("ImageExists before pull = %v, %v", ok, err)
	}
	if err := c.PullImage(ctx, image); err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	if got := d.Pulls(); !slices.Equal(got, []string{pinned}) {
		t.Fatalf("pulls = %v, want fromImage=repo and tag=digest", got)
	}
	if ok, err := c.ImageExists(ctx, image); err != nil || !ok {
		t.Fatalf("ImageExists after pull = %v, %v", ok, err)
	}
	if _, err := c.ImageExists(ctx, "itzg/minecraft-server:java21"); err == nil {
		t.Fatal("unpinned reference must be refused before calling Docker")
	}
	if err := c.PullImage(ctx, "itzg/minecraft-server:java21"); err == nil {
		t.Fatal("unpinned pull must be refused")
	}
	if len(d.Pulls()) != 1 {
		t.Fatalf("unpinned pull reached Docker: %v", d.Pulls())
	}
}

func TestPullErrorInStream(t *testing.T) {
	d := dockertest.New(t)
	d.FailPull("manifest for itzg/minecraft-server not found: manifest unknown")
	err := docker.New(d.Socket).PullImage(context.Background(), image)
	if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("PullImage = %v, want stream error", err)
	}
	if d.HasImage(pinned) {
		t.Fatal("failed pull must not leave the image")
	}
}

func TestEnsureNetworkIsIdempotent(t *testing.T) {
	d := dockertest.New(t)
	c := docker.New(d.Socket)
	ctx := context.Background()
	for range 2 {
		if err := c.EnsureNetwork(ctx, "mcpanel-mc", map[string]string{"mcpanel.network": "1"}); err != nil {
			t.Fatalf("EnsureNetwork: %v", err)
		}
	}
	if !d.HasNetwork("mcpanel-mc") {
		t.Fatal("network not created")
	}
	creates := 0
	for _, call := range d.Calls() {
		if call == "network create" {
			creates++
		}
	}
	if creates != 1 {
		t.Fatalf("network created %d times, calls %v", creates, d.Calls())
	}
}

func TestContainerLifecycle(t *testing.T) {
	d := dockertest.New(t)
	d.AddImage(pinned)
	c := docker.New(d.Socket)
	ctx := context.Background()
	if err := c.EnsureNetwork(ctx, "mcpanel-mc", nil); err != nil {
		t.Fatal(err)
	}
	cfg := docker.ContainerConfig{
		Image:       pinned,
		User:        "996:995",
		Env:         []string{"EULA=TRUE"},
		Labels:      map[string]string{"mcpanel.instance": "1"},
		Hostname:    "survival",
		StopTimeout: 60,
		HostConfig: docker.HostConfig{
			Binds: []string{"/srv/mc-panel/instances/1:/data"}, NetworkMode: "mcpanel-mc",
			RestartPolicy: docker.RestartPolicy{Name: "unless-stopped"}, CapDrop: []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges:true"}, Memory: 5 << 30, MemorySwap: 5 << 30, PidsLimit: 1024,
			LogConfig: docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		NetworkingConfig: docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{"mcpanel-mc": {}}},
	}
	id, err := c.CreateContainer(ctx, "mcpanel-mc-1", cfg)
	if err != nil || len(id) != 64 {
		t.Fatalf("CreateContainer = %q, %v", id, err)
	}
	got, _ := d.Container("mcpanel-mc-1")
	if !reflect.DeepEqual(got.Config, cfg) {
		t.Fatalf("Docker received\n%+v\nwant\n%+v", got.Config, cfg)
	}
	if _, err := c.CreateContainer(ctx, "mcpanel-mc-1", cfg); !docker.IsConflict(err) {
		t.Fatalf("duplicate name = %v, want conflict", err)
	}

	for range 2 { // second start: 304 Not Modified is success
		if err := c.Start(ctx, "mcpanel-mc-1"); err != nil {
			t.Fatalf("Start: %v", err)
		}
	}
	info, err := c.Inspect(ctx, "mcpanel-mc-1")
	if err != nil || info.ID != id || info.Name != "/mcpanel-mc-1" || !info.State.Running ||
		info.State.Status != "running" || info.Config.Labels["mcpanel.instance"] != "1" || info.State.StartedAt != "2026-10-10T12:00:00Z" {
		t.Fatalf("Inspect = %+v, %v", info, err)
	}
	if err := c.Kill(ctx, "mcpanel-mc-1", "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	if err := c.Restart(ctx, "mcpanel-mc-1", 60); err != nil {
		t.Fatal(err)
	}
	for range 2 { // second stop: 304 Not Modified is success
		if err := c.Stop(ctx, "mcpanel-mc-1", 60); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}
	got, _ = d.Container("mcpanel-mc-1")
	if got.Status != "exited" || !slices.Equal(got.Kills, []string{"SIGHUP"}) || !slices.Equal(got.StopTimes, []int{60, 60}) {
		t.Fatalf("container = %+v", got)
	}
	if err := c.Kill(ctx, "mcpanel-mc-1", "SIGHUP"); !docker.IsConflict(err) {
		t.Fatalf("kill on stopped container = %v, want conflict", err)
	}
	if err := c.Remove(ctx, "mcpanel-mc-1", false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Inspect(ctx, "mcpanel-mc-1"); !docker.IsNotFound(err) {
		t.Fatalf("Inspect after remove = %v, want not found", err)
	}
	if err := c.Start(ctx, "mcpanel-mc-1"); !docker.IsNotFound(err) {
		t.Fatalf("Start of a missing container = %v", err)
	}
}

func TestRemoveRunningNeedsForce(t *testing.T) {
	d := dockertest.New(t)
	d.AddContainer("x", docker.ContainerConfig{Image: pinned}, true)
	c := docker.New(d.Socket)
	ctx := context.Background()
	if err := c.Remove(ctx, "x", false); !docker.IsConflict(err) {
		t.Fatalf("Remove running without force = %v", err)
	}
	if err := c.Remove(ctx, "x", true); err != nil {
		t.Fatal(err)
	}
}

func TestListFiltersByLabel(t *testing.T) {
	d := dockertest.New(t)
	d.AddContainer("mcpanel-mc-1", docker.ContainerConfig{Image: pinned, Labels: map[string]string{"mcpanel.instance": "1"}}, true)
	d.AddContainer("mcpanel-mc-2", docker.ContainerConfig{Image: pinned, Labels: map[string]string{"mcpanel.instance": "2"}}, false)
	d.AddContainer("mcpanel-mc-7", docker.ContainerConfig{Image: pinned, Labels: map[string]string{"com.example": "other"}}, true)
	list, err := docker.New(d.Socket).List(context.Background(), "mcpanel.instance")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range list {
		names = append(names, s.Names[0])
		if !s.HasName(strings.TrimPrefix(s.Names[0], "/")) {
			t.Errorf("HasName(%q) = false", s.Names[0])
		}
	}
	if !slices.Equal(names, []string{"/mcpanel-mc-1", "/mcpanel-mc-2"}) {
		t.Fatalf("List = %v; stopped containers must be included, unlabelled ones never", names)
	}
	if list[1].State != "created" || list[1].Labels["mcpanel.instance"] != "2" {
		t.Fatalf("summary = %+v", list[1])
	}
}

func TestStats(t *testing.T) {
	d := dockertest.New(t)
	d.AddContainer("mcpanel-mc-1", docker.ContainerConfig{Image: pinned}, true)
	want := docker.Stats{
		CPUStats:    docker.CPUStats{CPUUsage: docker.CPUUsage{TotalUsage: 400}, SystemCPUUsage: 10000, OnlineCPUs: 4},
		MemoryStats: docker.MemoryStats{Usage: 3 << 30, Limit: 5 << 30, Stats: map[string]uint64{"inactive_file": 1 << 30}},
	}
	d.SetStats("mcpanel-mc-1", want)
	got, err := docker.New(d.Socket).Stats(context.Background(), "mcpanel-mc-1")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Stats = %+v, %v", got, err)
	}
	calls := d.Calls()
	if calls[len(calls)-1] != "stats mcpanel-mc-1" {
		t.Fatalf("calls = %v", calls)
	}
}

// rawServer serves h on a fresh Unix socket (for responses the fake never sends).
func rawServer(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dkr")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "r.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		_ = srv.Close()
		_ = os.RemoveAll(dir)
	})
	return path
}

func TestRequestShapeAndLimits(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	path := rawServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_, _ = io.WriteString(w, "["+strings.Repeat(" ", 9<<20)+"]")
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	ctx := context.Background()
	c := docker.NewWithTimeouts(path, 200*time.Millisecond, time.Second)
	if _, err := c.List(ctx, "mcpanel.instance"); !errors.Is(err, docker.ErrUnavailable) || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response = %v", err)
	}
	start := time.Now()
	if err := c.Ping(ctx); !errors.Is(err, docker.ErrUnavailable) {
		t.Fatalf("hanging daemon = %v, want ErrUnavailable", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout not applied")
	}
	if err := c.Kill(ctx, "mcpanel-router", "SIGHUP"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`GET /v1.44/containers/json?all=1&filters=%7B%22label%22%3A%5B%22mcpanel.instance%22%5D%7D`,
		"GET /v1.44/_ping",
		"POST /v1.44/containers/mcpanel-router/kill?signal=SIGHUP",
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(seen, want) {
		t.Fatalf("requests =\n%q\nwant\n%q", seen, want)
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./internal/agent/docker/... -count=1`
Expected: FAIL beim Kompilieren (`package github.com/Bernotese/mc-panel/internal/agent/docker` bzw. `…/docker/dockertest` existiert nicht).

- [ ] **Step 3: Typen implementieren** – `internal/agent/docker/types.go`

```go
package docker

import (
	"fmt"
	"regexp"
	"strings"
)

// ContainerConfig is the body of POST /containers/create: the container-level
// fields plus HostConfig and NetworkingConfig. Only fields mc-panel sets exist here,
// so nothing else can ever reach Docker.
type ContainerConfig struct {
	Image            string              `json:"Image"`
	Cmd              []string            `json:"Cmd,omitempty"`
	User             string              `json:"User,omitempty"`
	Env              []string            `json:"Env,omitempty"`
	Labels           map[string]string   `json:"Labels,omitempty"`
	Hostname         string              `json:"Hostname,omitempty"`
	StopTimeout      int                 `json:"StopTimeout,omitempty"`
	ExposedPorts     map[string]struct{} `json:"ExposedPorts,omitempty"`
	HostConfig       HostConfig          `json:"HostConfig"`
	NetworkingConfig NetworkingConfig    `json:"NetworkingConfig"`
}

type HostConfig struct {
	Binds          []string                 `json:"Binds,omitempty"`
	NetworkMode    string                   `json:"NetworkMode,omitempty"`
	PortBindings   map[string][]PortBinding `json:"PortBindings,omitempty"`
	RestartPolicy  RestartPolicy            `json:"RestartPolicy"`
	CapAdd         []string                 `json:"CapAdd,omitempty"`
	CapDrop        []string                 `json:"CapDrop,omitempty"`
	SecurityOpt    []string                 `json:"SecurityOpt,omitempty"`
	Privileged     bool                     `json:"Privileged"`
	ReadonlyRootfs bool                     `json:"ReadonlyRootfs"`
	Memory         int64                    `json:"Memory,omitempty"`
	MemorySwap     int64                    `json:"MemorySwap,omitempty"`
	PidsLimit      int64                    `json:"PidsLimit,omitempty"`
	LogConfig      LogConfig                `json:"LogConfig"`
}

type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type RestartPolicy struct {
	Name string `json:"Name"`
}

type LogConfig struct {
	Type   string            `json:"Type"`
	Config map[string]string `json:"Config,omitempty"`
}

type NetworkingConfig struct {
	EndpointsConfig map[string]EndpointSettings `json:"EndpointsConfig,omitempty"`
}

type EndpointSettings struct {
	Aliases []string `json:"Aliases,omitempty"`
}

// ContainerSummary is one entry of GET /containers/json.
type ContainerSummary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"` // with leading "/"
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
	State  string            `json:"State"` // created, running, paused, restarting, removing, exited, dead
}

// HasName reports whether the container is called name (Docker prefixes names with "/").
func (c ContainerSummary) HasName(name string) bool {
	for _, n := range c.Names {
		if strings.TrimPrefix(n, "/") == name {
			return true
		}
	}
	return false
}

// ContainerInfo is the subset of GET /containers/{id}/json that mc-panel reads.
type ContainerInfo struct {
	ID     string         `json:"Id"`
	Name   string         `json:"Name"` // with leading "/"
	State  ContainerState `json:"State"`
	Config InfoConfig     `json:"Config"`
}

type ContainerState struct {
	Status     string `json:"Status"`
	Running    bool   `json:"Running"`
	Restarting bool   `json:"Restarting"`
	ExitCode   int    `json:"ExitCode"`
	StartedAt  string `json:"StartedAt"` // RFC 3339; "0001-01-01T00:00:00Z" if never started
}

type InfoConfig struct {
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
}

// Stats is the subset of GET /containers/{id}/stats?stream=false&one-shot=true.
// One-shot stats carry no precpu_stats; callers keep the previous sample themselves.
type Stats struct {
	CPUStats    CPUStats    `json:"cpu_stats"`
	MemoryStats MemoryStats `json:"memory_stats"`
}

type CPUStats struct {
	CPUUsage       CPUUsage `json:"cpu_usage"`
	SystemCPUUsage uint64   `json:"system_cpu_usage"`
	OnlineCPUs     uint32   `json:"online_cpus"`
}

type CPUUsage struct {
	TotalUsage uint64 `json:"total_usage"`
}

type MemoryStats struct {
	Usage uint64            `json:"usage"`
	Limit uint64            `json:"limit"`
	Stats map[string]uint64 `json:"stats"` // cgroup v2: inactive_file; v1: cache
}

// ImageRef is a parsed "repo[:tag]@sha256:<hex>" reference.
type ImageRef struct {
	Repo   string // e.g. "itzg/minecraft-server" or "registry:5000/x/y"
	Tag    string // informational only; may be empty
	Digest string // "sha256:<64 hex>"
}

var (
	digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	repoRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]*$`)
	tagRe    = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

// ParseImageRef accepts only digest-pinned references.
func ParseImageRef(ref string) (ImageRef, error) {
	name, digest, ok := strings.Cut(ref, "@")
	if !ok || !digestRe.MatchString(digest) {
		return ImageRef{}, fmt.Errorf("docker: image %q is not pinned with @sha256:<64 hex>", ref)
	}
	repo, tag := name, ""
	if i := strings.LastIndex(name, ":"); i > strings.LastIndex(name, "/") {
		repo, tag = name[:i], name[i+1:]
		if !tagRe.MatchString(tag) {
			return ImageRef{}, fmt.Errorf("docker: image %q has an invalid tag", ref)
		}
	}
	if !repoRe.MatchString(repo) || strings.HasSuffix(repo, "/") || strings.Contains(repo, "//") {
		return ImageRef{}, fmt.Errorf("docker: image %q has an invalid repository", ref)
	}
	return ImageRef{Repo: repo, Tag: tag, Digest: digest}, nil
}

// Pinned is "repo@sha256:…". Docker ignores a tag next to a digest anyway, and the
// containerd image store resolves this form reliably for inspect, pull and create.
func (r ImageRef) Pinned() string { return r.Repo + "@" + r.Digest }
```

- [ ] **Step 4: Client implementieren** – `internal/agent/docker/client.go`

```go
// Package docker is a narrow client for the Docker Engine API over its Unix socket.
// It supports exactly the calls the agent needs and pins API version 1.44, the oldest
// version Docker 29 still accepts. The panel never imports this package.
package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// APIVersion is the path prefix of every request.
	APIVersion = "/v1.44"
	// DefaultTimeout bounds every call except PullImage, Stop and Restart.
	DefaultTimeout = 30 * time.Second
	// DefaultPullTimeout bounds an image pull (itzg images are several hundred MB).
	DefaultPullTimeout = 30 * time.Minute

	baseURL          = "http://docker"
	maxResponseBytes = 8 << 20
	maxErrorBytes    = 64 << 10
	maxPullBytes     = 64 << 20
	// stopGrace is added to the stop timeout: Docker answers only after the
	// container is down, which takes up to the stop timeout plus SIGKILL handling.
	stopGrace = 30 * time.Second
)

// ErrUnavailable wraps every failure to reach the daemon (socket missing, refused,
// timeout, broken response). Callers report Docker as unavailable.
var ErrUnavailable = errors.New("docker unavailable")

// Error is a non-2xx answer from the daemon.
type Error struct {
	StatusCode int
	Method     string
	Path       string
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("docker: %s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
}

func statusIs(err error, code int) bool {
	var de *Error
	return errors.As(err, &de) && de.StatusCode == code
}

// IsNotFound reports a 404 (no such container, image or network).
func IsNotFound(err error) bool { return statusIs(err, http.StatusNotFound) }

// IsConflict reports a 409 (name in use, container not running, …).
func IsConflict(err error) bool { return statusIs(err, http.StatusConflict) }

// Client is safe for concurrent use.
type Client struct {
	hc          *http.Client
	timeout     time.Duration
	pullTimeout time.Duration
}

func New(socketPath string) *Client {
	return NewWithTimeouts(socketPath, DefaultTimeout, DefaultPullTimeout)
}

func NewWithTimeouts(socketPath string, timeout, pullTimeout time.Duration) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		// Proxy stays nil: HTTP(S)_PROXY must never see Docker API traffic.
	}
	return &Client{
		hc: &http.Client{
			Transport:     tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		timeout:     timeout,
		pullTimeout: pullTimeout,
	}
}

// Ping checks that the daemon answers.
func (c *Client) Ping(ctx context.Context) error {
	return c.call(ctx, c.timeout, http.MethodGet, "/_ping", nil, nil, nil)
}

// ImageExists reports whether ref is present locally.
func (c *Client) ImageExists(ctx context.Context, ref string) (bool, error) {
	r, err := ParseImageRef(ref)
	if err != nil {
		return false, err
	}
	err = c.call(ctx, c.timeout, http.MethodGet, "/images/"+r.Pinned()+"/json", nil, nil, nil)
	switch {
	case err == nil:
		return true, nil
	case IsNotFound(err):
		return false, nil
	default:
		return false, err
	}
}

type pullMessage struct {
	Status      string `json:"status"`
	Error       string `json:"error"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

// PullImage pulls ref by digest. Docker answers 200 and reports failures inside the
// JSON stream, so the whole stream is read and checked for error messages.
func (c *Client) PullImage(ctx context.Context, ref string) error {
	r, err := ParseImageRef(ref)
	if err != nil {
		return err
	}
	q := url.Values{"fromImage": {r.Repo}, "tag": {r.Digest}}
	ctx, cancel := context.WithTimeout(ctx, c.pullTimeout)
	defer cancel()
	resp, err := c.send(ctx, http.MethodPost, "/images/create", q, nil)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp, http.MethodPost, "/images/create"); err != nil {
		return err
	}
	lr := &io.LimitedReader{R: resp.Body, N: maxPullBytes + 1}
	dec := json.NewDecoder(lr)
	for {
		var m pullMessage
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if lr.N <= 0 {
				return fmt.Errorf("%w: pull %s: progress output exceeds %d bytes", ErrUnavailable, r.Pinned(), maxPullBytes)
			}
			return fmt.Errorf("%w: pull %s: read progress: %w", ErrUnavailable, r.Pinned(), err)
		}
		if m.ErrorDetail != nil && m.ErrorDetail.Message != "" {
			return fmt.Errorf("docker: pull %s: %s", r.Pinned(), m.ErrorDetail.Message)
		}
		if m.Error != "" {
			return fmt.Errorf("docker: pull %s: %s", r.Pinned(), m.Error)
		}
	}
	return nil
}

type networkCreate struct {
	Name   string            `json:"Name"`
	Driver string            `json:"Driver"`
	Labels map[string]string `json:"Labels,omitempty"`
}

// EnsureNetwork creates the bridge network name unless it exists.
func (c *Client) EnsureNetwork(ctx context.Context, name string, labels map[string]string) error {
	err := c.call(ctx, c.timeout, http.MethodGet, "/networks/"+url.PathEscape(name), nil, nil, nil)
	if err == nil || !IsNotFound(err) {
		return err
	}
	err = c.call(ctx, c.timeout, http.MethodPost, "/networks/create", nil,
		networkCreate{Name: name, Driver: "bridge", Labels: labels}, nil)
	if IsConflict(err) { // created concurrently
		return nil
	}
	return err
}

// CreateContainer creates (but does not start) a container and returns its ID.
func (c *Client) CreateContainer(ctx context.Context, name string, cfg ContainerConfig) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.call(ctx, c.timeout, http.MethodPost, "/containers/create", url.Values{"name": {name}}, cfg, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// Start starts a container; an already running container is not an error.
func (c *Client) Start(ctx context.Context, id string) error {
	return notModifiedOK(c.call(ctx, c.timeout, http.MethodPost, containerPath(id, "start"), nil, nil, nil))
}

// Stop stops a container (SIGTERM, SIGKILL after timeoutSeconds); a stopped
// container is not an error.
func (c *Client) Stop(ctx context.Context, id string, timeoutSeconds int) error {
	q := url.Values{"t": {strconv.Itoa(timeoutSeconds)}}
	return notModifiedOK(c.call(ctx, stopTimeout(timeoutSeconds), http.MethodPost, containerPath(id, "stop"), q, nil, nil))
}

// Restart stops (like Stop) and starts a container.
func (c *Client) Restart(ctx context.Context, id string, timeoutSeconds int) error {
	q := url.Values{"t": {strconv.Itoa(timeoutSeconds)}}
	return c.call(ctx, stopTimeout(timeoutSeconds), http.MethodPost, containerPath(id, "restart"), q, nil, nil)
}

// Kill sends signal (e.g. "SIGHUP") to a running container.
func (c *Client) Kill(ctx context.Context, id, signal string) error {
	return c.call(ctx, c.timeout, http.MethodPost, containerPath(id, "kill"), url.Values{"signal": {signal}}, nil, nil)
}

// Remove deletes a container; force also removes a running one.
func (c *Client) Remove(ctx context.Context, id string, force bool) error {
	return c.call(ctx, c.timeout, http.MethodDelete, containerPath(id, ""), url.Values{"force": {strconv.FormatBool(force)}}, nil, nil)
}

// Inspect returns the container's state, labels and image.
func (c *Client) Inspect(ctx context.Context, id string) (ContainerInfo, error) {
	var out ContainerInfo
	err := c.call(ctx, c.timeout, http.MethodGet, containerPath(id, "json"), nil, nil, &out)
	return out, err
}

// List returns all containers (running or not) that carry the label key.
func (c *Client) List(ctx context.Context, label string) ([]ContainerSummary, error) {
	filters, err := json.Marshal(map[string][]string{"label": {label}})
	if err != nil {
		return nil, err
	}
	var out []ContainerSummary
	q := url.Values{"all": {"1"}, "filters": {string(filters)}}
	if err := c.call(ctx, c.timeout, http.MethodGet, "/containers/json", q, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Stats returns one snapshot without waiting for a second sample.
func (c *Client) Stats(ctx context.Context, id string) (Stats, error) {
	var out Stats
	q := url.Values{"stream": {"false"}, "one-shot": {"true"}}
	err := c.call(ctx, c.timeout, http.MethodGet, containerPath(id, "stats"), q, nil, &out)
	return out, err
}

func containerPath(id, action string) string {
	p := "/containers/" + url.PathEscape(id)
	if action != "" {
		p += "/" + action
	}
	return p
}

func stopTimeout(seconds int) time.Duration {
	return time.Duration(max(seconds, 0))*time.Second + stopGrace
}

func notModifiedOK(err error) error {
	if statusIs(err, http.StatusNotModified) {
		return nil
	}
	return err
}

// call sends one request and decodes a JSON answer into out (if non-nil).
func (c *Client) call(ctx context.Context, timeout time.Duration, method, path string, q url.Values, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := c.send(ctx, method, path, q, in)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkStatus(resp, method, path); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: %s %s: read response: %w", ErrUnavailable, method, path, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("%w: %s %s: response exceeds %d bytes", ErrUnavailable, method, path, maxResponseBytes)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: %s %s: decode response: %w", ErrUnavailable, method, path, err)
	}
	return nil
}

func (c *Client) send(ctx context.Context, method, path string, q url.Values, in any) (*http.Response, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, fmt.Errorf("docker: encode %s %s: %w", method, path, err)
		}
		body = bytes.NewReader(b)
	}
	u := baseURL + APIVersion + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, fmt.Errorf("docker: build %s %s: %w", method, path, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s %s: %w", ErrUnavailable, method, path, err)
	}
	return resp, nil
}

// checkStatus turns a non-2xx answer into *Error with Docker's {"message": …}.
func checkStatus(resp *http.Response, method, path string) error {
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBytes))
	msg := http.StatusText(resp.StatusCode)
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &m) == nil && m.Message != "" {
		msg = m.Message
	}
	return &Error{StatusCode: resp.StatusCode, Method: method, Path: path, Message: msg}
}
```

- [ ] **Step 5: Fake-Daemon implementieren** – `internal/agent/docker/dockertest/daemon.go`

```go
// Package dockertest is an in-memory fake of the Docker Engine API subset used by
// internal/agent/docker. It serves HTTP on a Unix socket, so tests exercise the real
// client. Not for production use.
package dockertest

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
)

// Op names used by Fail and Hold.
const (
	OpPing    = "ping"
	OpImage   = "image" // GET /images/{ref}/json
	OpPull    = "pull"
	OpNetwork = "network" // GET /networks/{name} and POST /networks/create
	OpCreate  = "create"
	OpStart   = "start"
	OpStop    = "stop"
	OpRestart = "restart"
	OpKill    = "kill"
	OpRemove  = "remove"
	OpInspect = "inspect"
	OpList    = "list"
	OpStats   = "stats"
)

// Container is a snapshot of a fake container.
type Container struct {
	ID        string
	Name      string
	Config    docker.ContainerConfig
	Status    string // created, running, exited
	ExitCode  int
	StartedAt time.Time
	Kills     []string // signals received
	StopTimes []int    // t= of every stop/restart
}

type failure struct {
	status int
	msg    string
}

// Daemon is safe for concurrent use.
type Daemon struct {
	// Socket is the path of the Unix socket the fake listens on.
	Socket string

	mu         sync.Mutex
	now        time.Time
	seq        int
	containers map[string]*Container // by name
	images     map[string]bool       // pinned refs "repo@sha256:…"
	networks   map[string]map[string]string
	pulls      []string
	calls      []string
	fail       map[string]failure
	pullError  string
	holds      map[string]*hold
	stats      map[string]docker.Stats
}

type hold struct {
	arrived chan string
	release chan struct{}
}

// New starts a fake daemon on a short socket path; it stops when the test ends.
func New(t testing.TB) *Daemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "dkr")
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{
		Socket:     filepath.Join(dir, "d.sock"),
		now:        time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
		containers: map[string]*Container{},
		images:     map[string]bool{},
		networks:   map[string]map[string]string{},
		fail:       map[string]failure{},
		holds:      map[string]*hold{},
		stats:      map[string]docker.Stats{},
	}
	l, err := net.Listen("unix", d.Socket)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: d, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() {
		d.mu.Lock()
		for _, h := range d.holds {
			close(h.release)
		}
		d.holds = map[string]*hold{}
		d.mu.Unlock()
		_ = srv.Close()
		_ = os.RemoveAll(dir)
	})
	return d
}

// AddImage marks a pinned image ("repo@sha256:…") as present.
func (d *Daemon) AddImage(pinned string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.images[pinned] = true
}

// AddContainer seeds a container (e.g. a foreign one without mc-panel labels).
func (d *Daemon) AddContainer(name string, cfg docker.ContainerConfig, running bool) Container {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.newContainer(name, cfg)
	if running {
		c.Status = "running"
		c.StartedAt = d.now
	}
	return *c
}

// Container returns a snapshot of the container called name.
func (d *Daemon) Container(name string) (Container, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.containers[name]
	if !ok {
		return Container{}, false
	}
	cp := *c
	cp.Kills = slices.Clone(c.Kills)
	cp.StopTimes = slices.Clone(c.StopTimes)
	return cp, true
}

// SetState changes a container's state, e.g. to simulate a crash ("exited", 1).
func (d *Daemon) SetState(name, status string, exitCode int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.containers[name]; ok {
		c.Status, c.ExitCode = status, exitCode
	}
}

// SetStats sets the answer of the stats endpoint for the container called name.
func (d *Daemon) SetStats(name string, s docker.Stats) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stats[name] = s
}

// HasNetwork reports whether the network exists.
func (d *Daemon) HasNetwork(name string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.networks[name]
	return ok
}

// HasImage reports whether the pinned image is present.
func (d *Daemon) HasImage(pinned string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.images[pinned]
}

// Pulls returns the pulled references as "fromImage@tag".
func (d *Daemon) Pulls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.pulls)
}

// Calls returns every request as "<op> <container or ref>" in arrival order.
func (d *Daemon) Calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

// Fail makes every request of op answer status with message until Clear.
func (d *Daemon) Fail(op string, status int, message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail[op] = failure{status: status, msg: message}
}

// FailPull makes pulls answer 200 with an error line in the progress stream.
func (d *Daemon) FailPull(message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pullError = message
}

// Clear removes all failures set by Fail and FailPull.
func (d *Daemon) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fail = map[string]failure{}
	d.pullError = ""
}

// Hold makes requests of op wait until release is called. Every held request
// sends its target (container name or image) on arrived first.
func (d *Daemon) Hold(op string) (arrived <-chan string, release func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	h := &hold{arrived: make(chan string, 16), release: make(chan struct{})}
	d.holds[op] = h
	var once sync.Once
	return h.arrived, func() {
		once.Do(func() {
			d.mu.Lock()
			if d.holds[op] == h {
				delete(d.holds, op)
			}
			d.mu.Unlock()
			close(h.release)
		})
	}
}

func (d *Daemon) newContainer(name string, cfg docker.ContainerConfig) *Container {
	d.seq++
	c := &Container{ID: fmt.Sprintf("%064x", d.seq), Name: name, Config: cfg, Status: "created"}
	d.containers[name] = c
	return c
}

// lookup finds a container by name or full ID (caller holds d.mu).
func (d *Daemon) lookup(ref string) *Container {
	if c, ok := d.containers[ref]; ok {
		return c
	}
	for _, c := range d.containers {
		if c.ID == ref {
			return c
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

// route maps a request to (op, target, action).
func route(method, path string) (op, target string) {
	switch {
	case method == http.MethodGet && path == "/_ping":
		return OpPing, ""
	case method == http.MethodPost && path == "/images/create":
		return OpPull, ""
	case method == http.MethodGet && strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		return OpImage, strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json")
	case path == "/networks/create" || strings.HasPrefix(path, "/networks/"):
		return OpNetwork, strings.TrimPrefix(path, "/networks/")
	case method == http.MethodPost && path == "/containers/create":
		return OpCreate, ""
	case method == http.MethodGet && path == "/containers/json":
		return OpList, ""
	case strings.HasPrefix(path, "/containers/"):
		rest := strings.TrimPrefix(path, "/containers/")
		id, action, _ := strings.Cut(rest, "/")
		switch {
		case method == http.MethodDelete && action == "":
			return OpRemove, id
		case method == http.MethodGet && action == "json":
			return OpInspect, id
		case method == http.MethodGet && action == "stats":
			return OpStats, id
		case method == http.MethodPost && slices.Contains([]string{OpStart, OpStop, OpRestart, OpKill}, action):
			return action, id
		}
	}
	return "", ""
}

func (d *Daemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, ok := strings.CutPrefix(r.URL.Path, docker.APIVersion)
	if !ok {
		writeError(w, http.StatusBadRequest, "missing API version prefix "+docker.APIVersion)
		return
	}
	op, target := route(r.Method, path)
	if op == "" {
		writeError(w, http.StatusNotFound, "page not found")
		return
	}
	if op == OpCreate {
		target = r.URL.Query().Get("name")
	}
	if op == OpPull {
		target = r.URL.Query().Get("fromImage") + "@" + r.URL.Query().Get("tag")
	}

	d.mu.Lock()
	d.calls = append(d.calls, op+" "+target)
	h := d.holds[op]
	f, failing := d.fail[op]
	d.mu.Unlock()

	if h != nil {
		h.arrived <- target
		select {
		case <-h.release:
		case <-r.Context().Done():
			return
		}
	}
	if failing {
		writeError(w, f.status, f.msg)
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	switch op {
	case OpPing:
		w.Header().Set("Api-Version", "1.52")
		_, _ = io.WriteString(w, "OK")
	case OpImage:
		if !d.images[target] {
			writeError(w, http.StatusNotFound, "No such image: "+target)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"Id": "sha256:" + strings.Repeat("ab", 32)})
	case OpPull:
		d.pull(w, target)
	case OpNetwork:
		d.network(w, r, target)
	case OpCreate:
		d.create(w, r, target)
	case OpList:
		d.list(w, r)
	default:
		d.container(w, r, op, target)
	}
}

func (d *Daemon) pull(w http.ResponseWriter, target string) {
	d.pulls = append(d.pulls, target)
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"Pulling from library/x","id":"1"}`+"\n")
	if d.pullError != "" {
		b, _ := json.Marshal(map[string]any{"errorDetail": map[string]string{"message": d.pullError}, "error": d.pullError})
		_, _ = w.Write(append(b, '\n'))
		return
	}
	d.images[target] = true
	_, _ = io.WriteString(w, `{"status":"Digest: x"}`+"\n"+`{"status":"Status: Downloaded newer image"}`+"\n")
}

func (d *Daemon) network(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method == http.MethodGet {
		labels, ok := d.networks[name]
		if !ok {
			writeError(w, http.StatusNotFound, "network "+name+" not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"Name": name, "Driver": "bridge", "Labels": labels})
		return
	}
	var req struct {
		Name   string
		Driver string
		Labels map[string]string
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Driver != "bridge" {
		writeError(w, http.StatusBadRequest, "bad network request")
		return
	}
	if _, ok := d.networks[req.Name]; ok {
		writeError(w, http.StatusConflict, "network with name "+req.Name+" already exists")
		return
	}
	d.networks[req.Name] = req.Labels
	writeJSON(w, http.StatusCreated, map[string]string{"Id": "net-" + req.Name})
}

func (d *Daemon) create(w http.ResponseWriter, r *http.Request, name string) {
	var cfg docker.ContainerConfig
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "bad container config: "+err.Error())
		return
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if _, ok := d.containers[name]; ok {
		writeError(w, http.StatusConflict, `Conflict. The container name "/`+name+`" is already in use`)
		return
	}
	if !d.images[cfg.Image] {
		writeError(w, http.StatusNotFound, "No such image: "+cfg.Image)
		return
	}
	if mode := cfg.HostConfig.NetworkMode; mode != "" && mode != "default" && mode != "bridge" {
		if _, ok := d.networks[mode]; !ok {
			writeError(w, http.StatusNotFound, "network "+mode+" not found")
			return
		}
	}
	c := d.newContainer(name, cfg)
	writeJSON(w, http.StatusCreated, map[string]any{"Id": c.ID, "Warnings": []string{}})
}

func (d *Daemon) list(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "1"
	var filters map[string][]string
	if f := r.URL.Query().Get("filters"); f != "" {
		if err := json.Unmarshal([]byte(f), &filters); err != nil {
			writeError(w, http.StatusBadRequest, "invalid filters")
			return
		}
	}
	out := []docker.ContainerSummary{}
	names := make([]string, 0, len(d.containers))
	for n := range d.containers {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		c := d.containers[n]
		if !all && c.Status != "running" {
			continue
		}
		if !matchLabels(c.Config.Labels, filters["label"]) {
			continue
		}
		out = append(out, docker.ContainerSummary{
			ID: c.ID, Names: []string{"/" + c.Name}, Image: c.Config.Image, Labels: c.Config.Labels, State: c.Status,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// matchLabels implements Docker's label filter ("key" or "key=value", all must match).
func matchLabels(labels map[string]string, filters []string) bool {
	for _, f := range filters {
		k, v, hasValue := strings.Cut(f, "=")
		got, ok := labels[k]
		if !ok || (hasValue && got != v) {
			return false
		}
	}
	return true
}

func (d *Daemon) container(w http.ResponseWriter, r *http.Request, op, ref string) {
	c := d.lookup(ref)
	if c == nil {
		writeError(w, http.StatusNotFound, "No such container: "+ref)
		return
	}
	q := r.URL.Query()
	switch op {
	case OpStart:
		if c.Status == "running" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		c.Status, c.ExitCode, c.StartedAt = "running", 0, d.now
		w.WriteHeader(http.StatusNoContent)
	case OpStop:
		if c.Status != "running" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		t, _ := strconv.Atoi(q.Get("t"))
		c.StopTimes = append(c.StopTimes, t)
		c.Status, c.ExitCode = "exited", 0
		w.WriteHeader(http.StatusNoContent)
	case OpRestart:
		t, _ := strconv.Atoi(q.Get("t"))
		c.StopTimes = append(c.StopTimes, t)
		c.Status, c.ExitCode, c.StartedAt = "running", 0, d.now
		w.WriteHeader(http.StatusNoContent)
	case OpKill:
		if c.Status != "running" {
			writeError(w, http.StatusConflict, "Container "+c.ID+" is not running")
			return
		}
		sig := q.Get("signal")
		if sig == "" {
			sig = "SIGKILL"
		}
		c.Kills = append(c.Kills, sig)
		if sig == "SIGKILL" || sig == "SIGTERM" {
			c.Status, c.ExitCode = "exited", 137
		}
		w.WriteHeader(http.StatusNoContent)
	case OpRemove:
		if c.Status == "running" && q.Get("force") != "true" {
			writeError(w, http.StatusConflict, "You cannot remove a running container "+c.ID+". Stop the container before attempting removal or force remove")
			return
		}
		delete(d.containers, c.Name)
		w.WriteHeader(http.StatusNoContent)
	case OpInspect:
		started := "0001-01-01T00:00:00Z"
		if !c.StartedAt.IsZero() {
			started = c.StartedAt.Format(time.RFC3339Nano)
		}
		writeJSON(w, http.StatusOK, docker.ContainerInfo{
			ID:   c.ID,
			Name: "/" + c.Name,
			State: docker.ContainerState{
				Status: c.Status, Running: c.Status == "running", Restarting: c.Status == "restarting",
				ExitCode: c.ExitCode, StartedAt: started,
			},
			Config: docker.InfoConfig{Image: c.Config.Image, Labels: c.Config.Labels},
		})
	case OpStats:
		writeJSON(w, http.StatusOK, d.stats[c.Name])
	}
}
```

`net.Listen("unix", …)` ist durch die bestehende Regel in `.golangci.yml` (noctx, Pfad `(cmd|internal)/agent/`, Text `net\.Listen`) abgedeckt; `ReadHeaderTimeout` verhindert gosec G112.

- [ ] **Step 6: Tests laufen lassen (grün, auch mit `-race`)**

Run: `go test ./internal/agent/docker/... -count=1 && CGO_ENABLED=1 go test -race -count=3 ./internal/agent/docker/...`
Expected: `ok  github.com/Bernotese/mc-panel/internal/agent/docker` (beide Läufe), `dockertest [no test files]`.

- [ ] **Step 7: Lint**

Run: `golangci-lint run ./internal/agent/docker/... && GOOS=linux golangci-lint run ./internal/agent/docker/...`
Expected: `0 issues.` (zweimal)

- [ ] **Step 8: Commit**

```bash
git add internal/agent/docker
git commit -m "feat(agent): narrow Docker Engine client with an in-memory fake daemon

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Container-Vorlage (`internal/agent/instances/template.go`)

**Files:**
- Create: `internal/agent/instances/template.go`
- Test: `internal/agent/instances/template_test.go`

**Interfaces:**
- Consumes: `agentapi.EnsureRequest` + `Validate` (V3), `instancespec.Env`, `instancespec.ErrInvalid` (V2), `config.Images`/`ByJava`/`DefaultImages` (Aufgabe 4), `docker.ContainerConfig`, `docker.ParseImageRef` (Aufgabe 5)
- Produces:
```go
package instances

const (
	NetworkName     = "mcpanel-mc"
	RouterName      = "mcpanel-router"
	ContainerPrefix = "mcpanel-mc-"
	LabelInstance   = "mcpanel.instance"
	LabelRevision   = "mcpanel.revision"
	LabelName       = "mcpanel.name"
	LabelFQDN       = "mcpanel.fqdn"
	LabelRouter     = "mcpanel.router"
	LabelNetwork    = "mcpanel.network"
	MinecraftPort      = 25565
	StopTimeoutSeconds = 60
	PidsLimit          = 1024
	RouterMemoryBytes  = 256 << 20
)
type Host struct {
	InstancesDir string
	UID, GID     int
	Images       config.Images
}
func ContainerName(id int64) string           // "mcpanel-mc-<id>"
func DataDir(instancesDir string, id int64) string // <dir>/instances/<id>
func RouterDir(instancesDir string) string        // <dir>/router
func DeletedDir(instancesDir string) string       // <dir>/deleted
func InstanceContainer(h Host, req agentapi.EnsureRequest) (docker.ContainerConfig, error)
func RouterContainer(h Host) (docker.ContainerConfig, error)
```

Die Vorlage ist eine reine Funktion: sie validiert `req` erneut (der agent vertraut dem panel nicht), nimmt die Umgebung ausschließlich aus `instancespec.Env` und setzt alles andere fest. `Memory = memory_mib × 1.25 MiB` ist bei Vielfachen von 256 MiB exakt (`× 5 / 4`).

- [ ] **Step 1: Failing test schreiben** – `internal/agent/instances/template_test.go`

```go
package instances_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/config"
)

func ptr[T any](v T) *T { return &v }

func testHost() instances.Host {
	return instances.Host{InstancesDir: "/srv/mc-panel", UID: 996, GID: 995, Images: config.DefaultImages()}
}

func paperRequest() agentapi.EnsureRequest {
	return agentapi.EnsureRequest{
		ID: 7, Name: "survival", FQDN: "survival.mc.example.com", Revision: 3,
		Spec: instancespec.Spec{
			Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096,
			Properties: instancespec.Properties{MOTD: ptr("Hallo"), Ops: []string{"Notch"}},
		},
	}
}

func TestInstanceContainerGolden(t *testing.T) {
	got, err := instances.InstanceContainer(testHost(), paperRequest())
	if err != nil {
		t.Fatal(err)
	}
	want := docker.ContainerConfig{
		Image: "itzg/minecraft-server@sha256:edf50d431ddc6f73929368e82f4334a405796874f7b0ae11f1edcbb267d670aa",
		User:  "996:995",
		Env: []string{
			"EULA=TRUE", "TYPE=PAPER", "VERSION=1.21.4", "MEMORY=4096M", "ENABLE_AUTOPAUSE=false",
			"ENABLE_RCON=true", "OVERRIDE_SERVER_PROPERTIES=true",
			"MOTD=Hallo", "DIFFICULTY=normal", "MODE=survival", "MAX_PLAYERS=20", "PVP=true", "HARDCORE=false",
			"ALLOW_FLIGHT=false", "ENABLE_COMMAND_BLOCK=false", "ONLINE_MODE=true", "ENABLE_WHITELIST=false",
			"OPS=Notch", "VIEW_DISTANCE=10", "SIMULATION_DISTANCE=10", "SPAWN_PROTECTION=16",
			"LEVEL_TYPE=minecraft:normal",
		},
		Hostname: "survival",
		Labels: map[string]string{
			"mcpanel.instance": "7",
			"mcpanel.revision": "3",
			"mcpanel.name":     "survival",
			"mcpanel.fqdn":     "survival.mc.example.com",
		},
		StopTimeout: 60,
		HostConfig: docker.HostConfig{
			Binds:         []string{"/srv/mc-panel/instances/7:/data"},
			NetworkMode:   "mcpanel-mc",
			RestartPolicy: docker.RestartPolicy{Name: "unless-stopped"},
			CapDrop:       []string{"ALL"},
			SecurityOpt:   []string{"no-new-privileges:true"},
			Memory:        5120 << 20, // 4096 MiB × 1.25
			MemorySwap:    5120 << 20,
			PidsLimit:     1024,
			LogConfig:     docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		NetworkingConfig: docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{"mcpanel-mc": {}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("template =\n%+v\nwant\n%+v", got, want)
	}
}

func TestRouterContainerGolden(t *testing.T) {
	got, err := instances.RouterContainer(testHost())
	if err != nil {
		t.Fatal(err)
	}
	want := docker.ContainerConfig{
		Image:        "itzg/mc-router@sha256:64ae69eaa7a6800a79bbf962f027f4020878efa25183359115bc7f6ee569c36e",
		Cmd:          []string{"-routes-config", "/config/routes.json"},
		Labels:       map[string]string{"mcpanel.router": "1"},
		ExposedPorts: map[string]struct{}{"25565/tcp": {}},
		HostConfig: docker.HostConfig{
			Binds:         []string{"/srv/mc-panel/router:/config:ro"},
			NetworkMode:   "mcpanel-mc",
			PortBindings:  map[string][]docker.PortBinding{"25565/tcp": {{HostPort: "25565"}}},
			RestartPolicy: docker.RestartPolicy{Name: "unless-stopped"},
			CapDrop:       []string{"ALL"},
			SecurityOpt:   []string{"no-new-privileges:true"},
			Memory:        256 << 20,
			MemorySwap:    256 << 20,
			PidsLimit:     1024,
			LogConfig:     docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		NetworkingConfig: docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{"mcpanel-mc": {}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("router template =\n%+v\nwant\n%+v", got, want)
	}
	if slices.Contains(got.Cmd, "-routes-config-watch") {
		t.Fatal("the watch flag never removes routes; reload happens via SIGHUP")
	}
}

// allowedEnvKeys mirrors the allowlist of instancespec.Env: the template must never add anything.
var allowedEnvKeys = []string{
	"EULA", "TYPE", "VERSION", "MEMORY", "ENABLE_AUTOPAUSE", "ENABLE_RCON", "OVERRIDE_SERVER_PROPERTIES",
	"FABRIC_LOADER_VERSION", "NEOFORGE_VERSION", "MODRINTH_MODPACK", "CF_SLUG", "CF_PAGE_URL", "CF_API_KEY",
	"MOTD", "DIFFICULTY", "MODE", "MAX_PLAYERS", "PVP", "HARDCORE", "ALLOW_FLIGHT", "ENABLE_COMMAND_BLOCK",
	"ONLINE_MODE", "ENABLE_WHITELIST", "WHITELIST", "OPS", "VIEW_DISTANCE", "SIMULATION_DISTANCE",
	"SPAWN_PROTECTION", "SEED", "LEVEL_TYPE",
}

func variedRequests() []agentapi.EnsureRequest {
	names := make([]string, 200)
	for i := range names {
		names[i] = "player_" + strings.Repeat("x", i%9)
	}
	full := instancespec.Properties{
		MOTD: ptr("§aMOTD with = and : and ,"), Difficulty: ptr("hard"), Gamemode: ptr("creative"), MaxPlayers: ptr(500),
		PVP: ptr(false), Hardcore: ptr(true), AllowFlight: ptr(true), EnableCommandBlock: ptr(true), OnlineMode: ptr(false),
		WhitelistEnabled: ptr(true), Whitelist: names, Ops: names[:50], ViewDistance: ptr(32), SimulationDistance: ptr(3),
		SpawnProtection: ptr(0), Seed: ptr("-42=JVM_OPTS"), LevelType: ptr("amplified"),
	}
	specs := []instancespec.Spec{
		{Type: instancespec.TypeVanilla, Version: "LATEST", Java: instancespec.Java25, MemoryMiB: 1024},
		{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 32768, Properties: full},
		{Type: instancespec.TypePurpur, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 2048},
		{Type: instancespec.TypeFabric, Version: "26.3", LoaderVersion: ptr("0.19.5"), Java: instancespec.Java25, MemoryMiB: 4096},
		{Type: instancespec.TypeNeoForge, Version: "1.21.4", LoaderVersion: ptr("21.4.140"), Java: instancespec.Java21, MemoryMiB: 6144},
		{Type: instancespec.TypeModrinth, Modpack: ptr("cobblemon-fabric"), Java: instancespec.Java21, MemoryMiB: 8192},
		{Type: instancespec.TypeCurseForge, Modpack: ptr("atm10"), CurseForgeAPIKey: ptr("$2a$10$key"), Java: instancespec.Java17, MemoryMiB: 8192},
		{Type: instancespec.TypeCurseForge, Modpack: ptr("https://www.curseforge.com/minecraft/modpacks/atm10"), CurseForgeAPIKey: ptr("k"), Java: instancespec.Java21, MemoryMiB: 8192, Properties: full},
		{Type: instancespec.TypeVanilla, Version: "1.16.5", Java: instancespec.Java8, MemoryMiB: 1280},
	}
	reqs := make([]agentapi.EnsureRequest, len(specs))
	for i, s := range specs {
		reqs[i] = agentapi.EnsureRequest{ID: int64(i + 1), Name: "srv-" + string(rune('a'+i)), Revision: int64(i + 1), Spec: s}
		reqs[i].FQDN = reqs[i].Name + ".mc.example.com"
	}
	return reqs
}

// TestInstanceContainerInvariants: no spec can add mounts, ports, capabilities,
// privileges or environment variables beyond the allowlist.
func TestInstanceContainerInvariants(t *testing.T) {
	h := testHost()
	for _, req := range variedRequests() {
		c, err := instances.InstanceContainer(h, req)
		if err != nil {
			t.Fatalf("%s (%s): %v", req.Name, req.Spec.Type, err)
		}
		hc := c.HostConfig
		if len(hc.Binds) != 1 || hc.Binds[0] != instances.DataDir("/srv/mc-panel", req.ID)+":/data" {
			t.Errorf("%s: binds = %v", req.Name, hc.Binds)
		}
		if hc.PortBindings != nil || c.ExposedPorts != nil || hc.CapAdd != nil || hc.Privileged || hc.ReadonlyRootfs {
			t.Errorf("%s: ports/caps/privileges must stay unset: %+v", req.Name, hc)
		}
		if !slices.Equal(hc.CapDrop, []string{"ALL"}) || !slices.Equal(hc.SecurityOpt, []string{"no-new-privileges:true"}) ||
			hc.NetworkMode != "mcpanel-mc" || hc.PidsLimit != 1024 || hc.RestartPolicy.Name != "unless-stopped" {
			t.Errorf("%s: hardening changed: %+v", req.Name, hc)
		}
		if want := int64(req.Spec.MemoryMiB) * 5 << 20 / 4; hc.Memory != want || hc.MemorySwap != want {
			t.Errorf("%s: memory = %d/%d, want %d", req.Name, hc.Memory, hc.MemorySwap, want)
		}
		if c.User != "996:995" || c.Cmd != nil || c.StopTimeout != 60 || c.Hostname != req.Name {
			t.Errorf("%s: user/cmd/stop/hostname = %q %v %d %q", req.Name, c.User, c.Cmd, c.StopTimeout, c.Hostname)
		}
		if len(c.Labels) != 4 || c.Labels["mcpanel.instance"] == "" || c.Labels["mcpanel.fqdn"] != req.FQDN {
			t.Errorf("%s: labels = %v", req.Name, c.Labels)
		}
		ref, _ := h.Images.ByJava(string(req.Spec.Java))
		if !strings.HasSuffix(ref, strings.TrimPrefix(c.Image, "itzg/minecraft-server")) {
			t.Errorf("%s: image %q does not match %s", req.Name, c.Image, req.Spec.Java)
		}
		seen := map[string]bool{}
		for _, kv := range c.Env {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || !slices.Contains(allowedEnvKeys, k) || seen[k] || strings.ContainsAny(v, "\x00\n\r") {
				t.Errorf("%s: env entry %q not allowed", req.Name, kv)
			}
			seen[k] = true
		}
	}
}

func TestInstanceContainerRejectsInvalidRequests(t *testing.T) {
	h := testHost()
	cases := map[string]func(*agentapi.EnsureRequest){
		"motd newline": func(r *agentapi.EnsureRequest) { r.Spec.Properties.MOTD = ptr("x\nJVM_OPTS=-Xmx64g") },
		"seed nul":     func(r *agentapi.EnsureRequest) { r.Spec.Properties.Seed = ptr("1\x002") },
		"name":         func(r *agentapi.EnsureRequest) { r.Name = "../etc" },
		"fqdn":         func(r *agentapi.EnsureRequest) { r.FQDN = "evil.example.com" },
		"id":           func(r *agentapi.EnsureRequest) { r.ID = -1 },
		"java":         func(r *agentapi.EnsureRequest) { r.Spec.Java = "java11" },
	}
	for name, mod := range cases {
		r := paperRequest()
		mod(&r)
		if c, err := instances.InstanceContainer(h, r); !errors.Is(err, instancespec.ErrInvalid) || c.Image != "" {
			t.Errorf("%s: err = %v, config = %+v", name, err, c)
		}
	}
	bad := testHost()
	bad.Images.Java21 = "itzg/minecraft-server:java21"
	if _, err := instances.InstanceContainer(bad, paperRequest()); err == nil {
		t.Fatal("an unpinned image must never reach Docker")
	}
	bad.Images.Router = "itzg/mc-router:latest"
	if _, err := instances.RouterContainer(bad); err == nil {
		t.Fatal("an unpinned router image must never reach Docker")
	}
}

func TestPaths(t *testing.T) {
	if instances.ContainerName(12) != "mcpanel-mc-12" || instances.DataDir("/srv/mc-panel", 12) != "/srv/mc-panel/instances/12" ||
		instances.RouterDir("/srv/mc-panel") != "/srv/mc-panel/router" || instances.DeletedDir("/srv/mc-panel") != "/srv/mc-panel/deleted" {
		t.Fatal("paths changed")
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./internal/agent/instances/ -count=1`
Expected: FAIL beim Kompilieren (Paket `internal/agent/instances` existiert nicht).

- [ ] **Step 3: Implementieren** – `internal/agent/instances/template.go`

```go
// Package instances runs the Minecraft containers on the agent side: a fixed,
// hardened container template, the mc-router routes file and a manager with one
// serial work queue per instance. Containers without the mcpanel.instance label
// are never touched.
package instances

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/config"
)

const (
	NetworkName     = "mcpanel-mc"
	RouterName      = "mcpanel-router"
	ContainerPrefix = "mcpanel-mc-"

	LabelInstance = "mcpanel.instance"
	LabelRevision = "mcpanel.revision"
	LabelName     = "mcpanel.name"
	LabelFQDN     = "mcpanel.fqdn"
	LabelRouter   = "mcpanel.router"
	LabelNetwork  = "mcpanel.network"

	MinecraftPort      = 25565
	StopTimeoutSeconds = 60
	PidsLimit          = 1024
	// RouterMemoryBytes caps mc-router (it needs a few MiB).
	RouterMemoryBytes = 256 << 20

	routerPort        = "25565/tcp"
	routerConfigDir   = "/config"
	routesFileName    = "routes.json"
	mib               = 1 << 20
	memoryNumerator   = 5 // Memory = memory_mib × 1.25 MiB: heap plus JVM/native overhead
	memoryDenominator = 4
)

// Host holds the agent-side settings the templates need; none of them come from the panel.
type Host struct {
	InstancesDir string
	UID, GID     int
	Images       config.Images
}

func ContainerName(id int64) string { return ContainerPrefix + strconv.FormatInt(id, 10) }

// DataDir is the host directory mounted at /data: <instances_dir>/instances/<id>.
func DataDir(instancesDir string, id int64) string {
	return filepath.Join(instancesDir, "instances", strconv.FormatInt(id, 10))
}

// RouterDir is mounted read-only at /config in the router container.
func RouterDir(instancesDir string) string { return filepath.Join(instancesDir, "router") }

// DeletedDir receives the data of removed instances.
func DeletedDir(instancesDir string) string { return filepath.Join(instancesDir, "deleted") }

func logConfig() docker.LogConfig {
	return docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}}
}

func network() docker.NetworkingConfig {
	return docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{NetworkName: {}}}
}

func pinnedImage(ref string) (string, error) {
	r, err := docker.ParseImageRef(ref)
	if err != nil {
		return "", err
	}
	return r.Pinned(), nil
}

// InstanceContainer builds the complete container configuration for one instance.
// It validates req again (the agent never trusts the panel); everything not derived
// from req is fixed here and cannot be influenced by the panel.
func InstanceContainer(h Host, req agentapi.EnsureRequest) (docker.ContainerConfig, error) {
	if err := req.Validate(); err != nil {
		return docker.ContainerConfig{}, err
	}
	env, err := instancespec.Env(req.Name, req.Spec)
	if err != nil {
		return docker.ContainerConfig{}, err
	}
	ref, ok := h.Images.ByJava(string(req.Spec.Java))
	if !ok {
		return docker.ContainerConfig{}, fmt.Errorf("%w: java: no image for %q", instancespec.ErrInvalid, req.Spec.Java)
	}
	img, err := pinnedImage(ref)
	if err != nil {
		return docker.ContainerConfig{}, err
	}
	memory := int64(req.Spec.MemoryMiB) * mib * memoryNumerator / memoryDenominator
	return docker.ContainerConfig{
		Image:    img,
		User:     fmt.Sprintf("%d:%d", h.UID, h.GID),
		Env:      env,
		Hostname: req.Name,
		Labels: map[string]string{
			LabelInstance: strconv.FormatInt(req.ID, 10),
			LabelRevision: strconv.FormatInt(req.Revision, 10),
			LabelName:     req.Name,
			LabelFQDN:     req.FQDN,
		},
		StopTimeout: StopTimeoutSeconds,
		HostConfig: docker.HostConfig{
			Binds:          []string{DataDir(h.InstancesDir, req.ID) + ":/data"},
			NetworkMode:    NetworkName,
			RestartPolicy:  docker.RestartPolicy{Name: "unless-stopped"},
			CapDrop:        []string{"ALL"},
			SecurityOpt:    []string{"no-new-privileges:true"},
			Privileged:     false,
			ReadonlyRootfs: false, // itzg writes outside /data (e.g. /tmp)
			Memory:         memory,
			MemorySwap:     memory,
			PidsLimit:      PidsLimit,
			LogConfig:      logConfig(),
		},
		NetworkingConfig: network(),
	}, nil
}

// RouterContainer builds the mc-router container: the only container with a
// published port. The image already runs as uid 65532, and the router gets no
// Docker socket; it only reads the routes file and reloads it on SIGHUP.
func RouterContainer(h Host) (docker.ContainerConfig, error) {
	img, err := pinnedImage(h.Images.Router)
	if err != nil {
		return docker.ContainerConfig{}, err
	}
	return docker.ContainerConfig{
		Image:        img,
		Cmd:          []string{"-routes-config", routerConfigDir + "/" + routesFileName},
		Labels:       map[string]string{LabelRouter: "1"},
		ExposedPorts: map[string]struct{}{routerPort: {}},
		HostConfig: docker.HostConfig{
			// Directory, not file: an atomic rename of routes.json stays visible.
			Binds:         []string{RouterDir(h.InstancesDir) + ":" + routerConfigDir + ":ro"},
			NetworkMode:   NetworkName,
			PortBindings:  map[string][]docker.PortBinding{routerPort: {{HostPort: strconv.Itoa(MinecraftPort)}}},
			RestartPolicy: docker.RestartPolicy{Name: "unless-stopped"},
			CapDrop:       []string{"ALL"},
			SecurityOpt:   []string{"no-new-privileges:true"},
			Memory:        RouterMemoryBytes,
			MemorySwap:    RouterMemoryBytes,
			PidsLimit:     PidsLimit,
			LogConfig:     logConfig(),
		},
		NetworkingConfig: network(),
	}, nil
}
```

- [ ] **Step 4: Tests laufen lassen (grün)**

Run: `go test ./internal/agent/instances/ -count=1 -v -run 'Container|Paths'`
Expected: PASS für `TestInstanceContainerGolden`, `TestRouterContainerGolden`, `TestInstanceContainerInvariants`, `TestInstanceContainerRejectsInvalidRequests`, `TestPaths`.

- [ ] **Step 5: Lint**

Run: `golangci-lint run ./internal/agent/instances/...`
Expected: `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/agent/instances/template.go internal/agent/instances/template_test.go
git commit -m "feat(agent): fixed, hardened container templates for instances and mc-router

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Routen-Datei für mc-router (`internal/agent/instances/router.go`)

**Files:**
- Create: `internal/agent/instances/router.go`
- Test: `internal/agent/instances/router_test.go`

**Interfaces:**
- Consumes: `docker.ContainerSummary` (Aufgabe 5), `ContainerName`, `LabelInstance`/`LabelName`/`LabelFQDN`, `MinecraftPort` (Aufgabe 6), `instancespec.CheckFQDN` (V2)
- Produces:
```go
func Routes(containers []docker.ContainerSummary) map[string]string // fqdn -> "mcpanel-mc-<id>:25565"
func RenderRoutes(mappings map[string]string) ([]byte, error)      // {"mappings": {...}}, sorted, trailing newline
func WriteRoutes(dir string, data []byte) (changed bool, err error) // atomic <dir>/routes.json
// unexported, used by Aufgabe 8:
func managedID(labels map[string]string, names ...string) (int64, bool)
```

Format nach mc-router 1.47.2: `{"mappings": {"<fqdn>": "<host>:<port>"}}`, `default-server` entfällt. `encoding/json` sortiert Map-Schlüssel – gleiche Routen ergeben byte-gleiche Dateien, sodass „unverändert“ ein einfacher Bytevergleich ist und kein unnötiges `SIGHUP` auslöst. Gestoppte Container behalten ihre Route (sonst flattert die Datei bei jedem Stop/Start).

- [ ] **Step 1: Failing test schreiben** – `internal/agent/instances/router_test.go`

```go
package instances_test

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
)

func summary(name string, labels map[string]string) docker.ContainerSummary {
	return docker.ContainerSummary{ID: "id-" + name, Names: []string{"/" + name}, Labels: labels, State: "running"}
}

func mcLabels(id, name, fqdn string) map[string]string {
	return map[string]string{"mcpanel.instance": id, "mcpanel.revision": "1", "mcpanel.name": name, "mcpanel.fqdn": fqdn}
}

func TestRoutesFromContainers(t *testing.T) {
	got := instances.Routes([]docker.ContainerSummary{
		summary("mcpanel-mc-2", mcLabels("2", "creative", "creative.mc.example.com")),
		summary("mcpanel-mc-1", mcLabels("1", "survival", "survival.mc.example.com")),
		// stopped containers keep their route
		{ID: "id-3", Names: []string{"/mcpanel-mc-3"}, Labels: mcLabels("3", "lobby", "lobby.mc.example.com"), State: "exited"},
		// foreign: label points to another id than the name
		summary("mcpanel-mc-4", mcLabels("5", "evil", "evil.mc.example.com")),
		// foreign: no label at all
		summary("mcpanel-mc-6", map[string]string{"mcpanel.fqdn": "x.mc.example.com"}),
		// fqdn does not belong to the name
		summary("mcpanel-mc-7", mcLabels("7", "seven", "other.mc.example.com")),
		// invalid fqdn
		summary("mcpanel-mc-8", mcLabels("8", "eight", "eight.mc.example.com\n")),
		// duplicate fqdn: lowest id (1) wins
		summary("mcpanel-mc-9", mcLabels("9", "survival", "survival.mc.example.com")),
	})
	want := map[string]string{
		"survival.mc.example.com": "mcpanel-mc-1:25565",
		"creative.mc.example.com": "mcpanel-mc-2:25565",
		"lobby.mc.example.com":    "mcpanel-mc-3:25565",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("Routes = %v, want %v", got, want)
	}
}

func TestRenderRoutes(t *testing.T) {
	b, err := instances.RenderRoutes(map[string]string{
		"survival.mc.example.com": "mcpanel-mc-1:25565",
		"creative.mc.example.com": "mcpanel-mc-2:25565",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"mappings\": {\n    \"creative.mc.example.com\": \"mcpanel-mc-2:25565\",\n    \"survival.mc.example.com\": \"mcpanel-mc-1:25565\"\n  }\n}\n"
	if string(b) != want {
		t.Fatalf("RenderRoutes =\n%s\nwant\n%s", b, want)
	}
	empty, err := instances.RenderRoutes(nil)
	if err != nil || string(empty) != "{\n  \"mappings\": {}\n}\n" {
		t.Fatalf("empty = %q, %v", empty, err)
	}
}

func TestWriteRoutesAtomicAndIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "router")
	first := []byte("{\n  \"mappings\": {}\n}\n")
	changed, err := instances.WriteRoutes(dir, first)
	if err != nil || !changed {
		t.Fatalf("first write = %v, %v", changed, err)
	}
	path := filepath.Join(dir, "routes.json")
	assertFile(t, path, first)
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("dir mode = %v, %v; want 0755 (router runs as uid 65532)", fi.Mode(), err)
	}

	changed, err = instances.WriteRoutes(dir, first)
	if err != nil || changed {
		t.Fatalf("identical write = %v, %v; want unchanged", changed, err)
	}

	second := []byte("{\n  \"mappings\": {\n    \"a.mc.example.com\": \"mcpanel-mc-1:25565\"\n  }\n}\n")
	changed, err = instances.WriteRoutes(dir, second)
	if err != nil || !changed {
		t.Fatalf("second write = %v, %v", changed, err)
	}
	assertFile(t, path, second)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temp files left behind: %v (%v)", entries, err)
	}
}

func TestWriteRoutesRefusesNonFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "router")
	if err := os.MkdirAll(filepath.Join(dir, "routes.json"), 0o755); err != nil { // a directory where the file belongs
		t.Fatal(err)
	}
	if _, err := instances.WriteRoutes(dir, []byte("{}\n")); err == nil {
		t.Fatal("a directory at routes.json must fail")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("unexpected files: %v", entries)
	}
}

func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(want) {
		t.Fatalf("%s = %q, %v; want %q", path, got, err, want)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("%s mode = %v, %v; want 0644", path, fi.Mode(), err)
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./internal/agent/instances/ -count=1`
Expected: FAIL beim Kompilieren (`undefined: instances.Routes`, `instances.RenderRoutes`, `instances.WriteRoutes`).

- [ ] **Step 3: Implementieren** – `internal/agent/instances/router.go`

```go
package instances

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// managedID returns the instance ID of a container that mc-panel manages: it carries
// a positive mcpanel.instance label AND is called mcpanel-mc-<that id>. Anything
// else (no label, other name, label pointing elsewhere) is foreign and never touched.
func managedID(labels map[string]string, names ...string) (int64, bool) {
	id, err := strconv.ParseInt(labels[LabelInstance], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	want := ContainerName(id)
	for _, n := range names {
		if strings.TrimPrefix(n, "/") == want {
			return id, true
		}
	}
	return 0, false
}

// Routes maps the FQDN of every managed container (running or not) to its
// backend "mcpanel-mc-<id>:25565". Containers with labels that do not form a
// valid name/FQDN pair are skipped; for a duplicate FQDN the lowest ID wins.
func Routes(containers []docker.ContainerSummary) map[string]string {
	sorted := slices.Clone(containers)
	ids := make(map[string]int64, len(sorted))
	for _, c := range sorted {
		id, _ := managedID(c.Labels, c.Names...)
		ids[c.ID] = id
	}
	slices.SortFunc(sorted, func(a, b docker.ContainerSummary) int { return cmp.Compare(ids[a.ID], ids[b.ID]) })
	routes := map[string]string{}
	for _, c := range sorted {
		id, ok := managedID(c.Labels, c.Names...)
		if !ok {
			continue
		}
		fqdn := c.Labels[LabelFQDN]
		if instancespec.CheckFQDN(c.Labels[LabelName], fqdn) != nil {
			continue
		}
		if _, dup := routes[fqdn]; dup {
			continue
		}
		routes[fqdn] = ContainerName(id) + ":" + strconv.Itoa(MinecraftPort)
	}
	return routes
}

// RenderRoutes returns the mc-router routes file. encoding/json sorts map keys,
// so equal mappings always give equal bytes.
func RenderRoutes(mappings map[string]string) ([]byte, error) {
	if mappings == nil {
		mappings = map[string]string{}
	}
	b, err := json.MarshalIndent(struct {
		Mappings map[string]string `json:"mappings"`
	}{mappings}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// WriteRoutes atomically replaces <dir>/routes.json with data (temp file in the
// same directory, fsync, rename, fsync of the directory). It reports changed=false
// and writes nothing when the file already has exactly this content.
func WriteRoutes(dir string, data []byte) (changed bool, err error) {
	// The router runs as uid 65532 and must read the directory and the file.
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: mc-router (uid 65532) reads this directory; it holds no secrets
		return false, err
	}
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // G302: see above, MkdirAll is subject to the umask
		return false, err
	}
	path := filepath.Join(dir, routesFileName)
	old, err := os.ReadFile(path) //nolint:gosec // G304: fixed file name below the configured instances_dir, never request input
	switch {
	case err == nil && bytes.Equal(old, data):
		return false, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".routes-*.json")
	if err != nil {
		return false, err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err = tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err = tmp.Close(); err != nil {
		return false, err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	if err = syncDir(dir); err != nil {
		return true, fmt.Errorf("routes written, but fsync of %s failed: %w", dir, err)
	}
	return true, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: directory below the configured instances_dir
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
```

Die vier `//nolint:gosec`-Kommentare sind nötig (G301/G302: der Router läuft als UID 65532 und muss Verzeichnis und Datei lesen; G304: fester Dateiname unter dem konfigurierten `instances_dir`). `tmp.Chmod` (Methode auf `*os.File`) meldet gosec nicht.

- [ ] **Step 4: Tests laufen lassen (grün)**

Run: `go test ./internal/agent/instances/ -count=1`
Expected: PASS (Vorlage- und Routen-Tests).

- [ ] **Step 5: Lint**

Run: `golangci-lint run ./internal/agent/instances/...`
Expected: `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/agent/instances/router.go internal/agent/instances/router_test.go
git commit -m "feat(agent): deterministic mc-router routes file with atomic writes

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Manager – Warteschlangen, ensure/power/remove, Router, Status, Stats

**Files:**
- Create: `internal/agent/instances/worker.go` (Warteschlange pro Instanz, `last_error`, `Wait`, `Close`)
- Create: `internal/agent/instances/manager.go` (Operationen, Router, Status, Stats, `Tick`, `Run`)
- Create: `internal/agent/instances/datadir.go` (Datenverzeichnis anlegen/archivieren)
- Test: `internal/agent/instances/manager_test.go`

**Interfaces:**
- Consumes: `docker.Client`, `dockertest.Daemon` (Aufgabe 5), Vorlage/Routen (Aufgaben 6, 7), `agentapi.*` (V3: `EnsureRequest`, `PowerAction`, `ValidPowerAction`, `ErrBusy`, `ErrDockerUnavailable`, `DockerOK`, `DockerUnavailable`, `ContainerState`/`State*`, `Operation`/`Op*`, `InstanceStatus`, `RouterStatus`, `InstancesResponse`), `instancespec.ErrInvalid`/`ValidName` (V2), `clock.Clock`, `config.Images`
- Produces:
```go
package instances

const (
	MaxOpenOperations   = 4   // per instance: running + waiting
	MaxTrackedInstances = 256
	SampleInterval      = 10 * time.Second
)
type Config struct {
	Docker       *docker.Client
	InstancesDir string
	UID, GID     int
	Images       config.Images
	Clock        clock.Clock
	Log          *slog.Logger
	Customize    func(name string, cfg *docker.ContainerConfig) // tests only
}
type Manager struct{ /* … */ }
func New(cfg Config) *Manager
func (m *Manager) Ensure(ctx context.Context, req agentapi.EnsureRequest) error            // ErrInvalid | ErrBusy | ErrDockerUnavailable | nil (queued)
func (m *Manager) Power(ctx context.Context, id int64, action agentapi.PowerAction) error // same
func (m *Manager) Remove(ctx context.Context, id int64) error                             // same
func (m *Manager) Status(ctx context.Context) agentapi.InstancesResponse
func (m *Manager) DockerStatus() string // cached: agentapi.DockerOK | agentapi.DockerUnavailable
func (m *Manager) CheckDocker(ctx context.Context) bool
func (m *Manager) RefreshRouter(ctx context.Context) error
func (m *Manager) SampleStats(ctx context.Context)
func (m *Manager) Tick(ctx context.Context) // CheckDocker -> RefreshRouter -> SampleStats
func (m *Manager) Run(ctx context.Context)  // Tick now and every SampleInterval until ctx is done
func (m *Manager) Wait()                    // until no operation is queued or running
func (m *Manager) Close()                   // reject new work, cancel running Docker calls, drop queued, Wait
```

Ablauf und Entscheidungen (Spec §5.1):

- **Annahme vs. Ausführung:** `Ensure`/`Power`/`Remove` validieren, prüfen Docker (gecachter Zustand; ist er „unavailable“, sofort ein frischer Ping – so funktionieren Schreibzugriffe gleich wieder, wenn Docker zurück ist) und reihen ein. Ausgeführt wird im Worker der Instanz mit dem Manager-Kontext (nicht dem Request-Kontext, der mit der `202`-Antwort endet). Pro Instanz läuft höchstens eine Worker-Goroutine; sie beendet sich, wenn die Schlange leer ist.
- **ensure:** Vorlage bauen (validiert erneut) → `Inspect mcpanel-mc-<id>`. Fremder Container → Fehler, nichts anfassen. Gleiche Revision oder `recreate=false` → nichts am Container tun (nur Routen auffrischen). Sonst: Image prüfen/ziehen (**vor** dem Stoppen, kurze Ausfallzeit) → Datenverzeichnis (0750, `chown uid:gid`) → Netz → bei vorhandenem Container `Stop(60)` + `Remove(force)` → `Create` → `Start` nur, wenn der alte Container lief → Routen/Router auffrischen. Ein neu angelegter Container wird **nicht** gestartet (das panel sendet danach `power start`, Spec §5.3).
- **power:** fehlender Container → `last_error` „container mcpanel-mc-<id> does not exist“ (die Anfrage selbst bekam `202`). Restart = `Stop(60)` + `Start`, damit `operation` korrekt `stopping` → `starting` zeigt.
- **remove:** Container stoppen und entfernen (fehlt er, weiter), `instances/<id>` nach `deleted/<name>-<UTC>` verschieben (Name aus Label `mcpanel.name`, sonst aus dem letzten ensure, sonst `instance-<id>`), Routen auffrischen; danach verschwindet die Instanz aus dem Status. Wiederholtes `remove` ist idempotent.
- **Router:** `RefreshRouter` (seriell über `routerMu`) schreibt zuerst die Routen-Datei (ohne Watch-Flag überspringt mc-router eine fehlende Datei beim Start), legt dann bei Bedarf `mcpanel-router` an (Image ziehen, Netz, Create, Start), startet einen gestoppten Router, ersetzt ihn bei geändertem gepinntem Image (agent-Update) und sendet `SIGHUP` nur, wenn sich die Datei geändert hat und der Router schon lief. Router-Fehler landen in `RouterStatus.LastError`, nicht im `last_error` der Instanz.
- **Status:** `List(label)` → je verwaltetem Container `Inspect` (Exit-Code, `StartedAt` nur bei running/restarting) + Stats aus dem Cache (nur running) + `operation`/`last_error` des Workers. Instanzen mit Worker-Zustand ohne Container erscheinen als `missing`. Sortiert nach ID. Docker nicht erreichbar → `Docker: "unavailable"`, `Instances: []`, Router `missing`.
- **Stats:** `SampleStats` holt pro laufendem verwalteten Container einen One-Shot-Snapshot. One-Shot liefert kein `precpu_stats`, daher merkt sich der Manager den vorigen Snapshot pro Instanz: `CPU% = Δtotal_usage / Δsystem_cpu_usage × online_cpus × 100` (erster Snapshot: 0). RAM = `usage − inactive_file` (cgroup v2) bzw. `− cache` (v1). Nicht mehr laufende Container fallen aus dem Cache.
- **Testbarkeit ohne `sleep`:** `Wait()` (Bedingungsvariable über die Zahl aktiver Worker) wartet, bis alles abgearbeitet ist; `Tick`/`SampleStats`/`RefreshRouter` sind direkt aufrufbar (wie `defense.SyncOnce`); `dockertest.Daemon.Hold` hält einen Docker-Aufruf an, um laufende Operationen und die Warteschlangengrenze zu beobachten. Zeit für Archivnamen kommt aus `clock.Fake`.

- [ ] **Step 1: Failing test schreiben** – `internal/agent/instances/manager_test.go`

```go
package instances_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

const (
	java21Pinned = "itzg/minecraft-server@sha256:edf50d431ddc6f73929368e82f4334a405796874f7b0ae11f1edcbb267d670aa"
	routerPinned = "itzg/mc-router@sha256:64ae69eaa7a6800a79bbf962f027f4020878efa25183359115bc7f6ee569c36e"
)

// syncBuffer is a bytes.Buffer safe for concurrent writers and readers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type env struct {
	m    *instances.Manager
	d    *dockertest.Daemon
	dir  string
	clk  *clock.Fake
	logs *syncBuffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d := dockertest.New(t)
	e := &env{d: d, dir: t.TempDir(), clk: clock.NewFake(time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)), logs: &syncBuffer{}}
	e.m = instances.New(instances.Config{
		Docker: docker.New(d.Socket), InstancesDir: e.dir, UID: os.Getuid(), GID: os.Getgid(),
		Images: config.DefaultImages(), Clock: e.clk,
		Log: slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	t.Cleanup(e.m.Close)
	return e
}

func ensureReq(id int64, name string, revision int64) agentapi.EnsureRequest {
	return agentapi.EnsureRequest{
		ID: id, Name: name, FQDN: name + ".mc.example.com", Revision: revision,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096},
	}
}

// do runs one operation to completion.
func (e *env) do(t *testing.T, op func(context.Context) error) {
	t.Helper()
	if err := op(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.m.Wait()
}

func (e *env) ensure(t *testing.T, req agentapi.EnsureRequest) {
	t.Helper()
	e.do(t, func(ctx context.Context) error { return e.m.Ensure(ctx, req) })
}

func (e *env) power(t *testing.T, id int64, a agentapi.PowerAction) {
	t.Helper()
	e.do(t, func(ctx context.Context) error { return e.m.Power(ctx, id, a) })
}

func (e *env) status(t *testing.T, id int64) agentapi.InstanceStatus {
	t.Helper()
	for _, s := range e.m.Status(context.Background()).Instances {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("instance %d not in status", id)
	return agentapi.InstanceStatus{}
}

func (e *env) container(t *testing.T, name string) dockertest.Container {
	t.Helper()
	c, ok := e.d.Container(name)
	if !ok {
		t.Fatalf("container %s missing", name)
	}
	return c
}

func (e *env) routes(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.dir, "router", "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// callsAfter returns the Docker calls since mark (len of Calls() earlier).
func (e *env) callsAfter(mark int) []string { return e.d.Calls()[mark:] }

func TestEnsureCreatesInstance(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))

	c := e.container(t, "mcpanel-mc-1")
	if c.Status != "created" || c.Config.Labels["mcpanel.revision"] != "1" || c.Config.Image != java21Pinned {
		t.Fatalf("container = %+v", c)
	}
	if !e.d.HasNetwork("mcpanel-mc") {
		t.Fatal("network mcpanel-mc not created")
	}
	if got := e.d.Pulls(); !slices.Equal(got, []string{java21Pinned, routerPinned}) {
		t.Fatalf("pulls = %v", got)
	}
	data := filepath.Join(e.dir, "instances", "1")
	if fi, err := os.Stat(data); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o750 {
		t.Fatalf("data dir = %v, %v; want 0750 directory", fi, err)
	}
	if fi, err := os.Stat(filepath.Join(e.dir, "instances")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("instances dir = %v, %v; want 0700", fi, err)
	}
	if !strings.Contains(e.routes(t), `"survival.mc.example.com": "mcpanel-mc-1:25565"`) {
		t.Fatalf("routes = %s", e.routes(t))
	}
	r := e.container(t, "mcpanel-router")
	if r.Status != "running" || r.Config.Labels["mcpanel.router"] != "1" {
		t.Fatalf("router = %+v", r)
	}
	want := agentapi.InstanceStatus{ID: 1, Name: "survival", Revision: 1, State: agentapi.StateCreated}
	if got := e.status(t, 1); got != want {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
	resp := e.m.Status(context.Background())
	if resp.Docker != agentapi.DockerOK || resp.Router != (agentapi.RouterStatus{State: agentapi.StateRunning}) {
		t.Fatalf("status = %+v", resp)
	}
}

func TestEnsureWithoutRecreateKeepsOldRevision(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	before := e.container(t, "mcpanel-mc-1")

	e.ensure(t, ensureReq(1, "survival", 2)) // recreate false
	after := e.container(t, "mcpanel-mc-1")
	if after.ID != before.ID || after.Status != "running" || after.Config.Labels["mcpanel.revision"] != "1" {
		t.Fatalf("container changed without recreate: %+v", after)
	}
	if s := e.status(t, 1); s.Revision != 1 || s.State != agentapi.StateRunning || s.StartedAt == 0 {
		t.Fatalf("status = %+v", s)
	}
}

func TestEnsureRecreate(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	old := e.container(t, "mcpanel-mc-1")

	mark := len(e.d.Calls())
	req := ensureReq(1, "survival", 2)
	req.Recreate = true
	req.Spec.MemoryMiB = 6144
	e.ensure(t, req)
	c := e.container(t, "mcpanel-mc-1")
	if c.ID == old.ID || c.Status != "running" || c.Config.Labels["mcpanel.revision"] != "2" || c.Config.HostConfig.Memory != 7680<<20 {
		t.Fatalf("recreated running container = %+v", c)
	}
	var lifecycle []string
	for _, call := range e.callsAfter(mark) {
		if strings.HasSuffix(call, " mcpanel-mc-1") && !strings.HasPrefix(call, "inspect") {
			lifecycle = append(lifecycle, call)
		}
	}
	if want := []string{"stop mcpanel-mc-1", "remove mcpanel-mc-1", "create mcpanel-mc-1", "start mcpanel-mc-1"}; !slices.Equal(lifecycle, want) {
		t.Fatalf("lifecycle = %v, want %v", lifecycle, want)
	}

	e.power(t, 1, agentapi.PowerStop)
	req.Revision = 3
	e.ensure(t, req)
	if c := e.container(t, "mcpanel-mc-1"); c.Status != "created" || c.Config.Labels["mcpanel.revision"] != "3" {
		t.Fatalf("a stopped container must stay stopped after recreate: %+v", c)
	}
	same := e.container(t, "mcpanel-mc-1")
	e.ensure(t, req) // same revision + recreate: nothing to do
	if e.container(t, "mcpanel-mc-1").ID != same.ID {
		t.Fatal("ensure with the current revision must not recreate")
	}
}

func TestPowerActions(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	if c := e.container(t, "mcpanel-mc-1"); c.Status != "running" {
		t.Fatalf("after start: %+v", c)
	}
	e.power(t, 1, agentapi.PowerStop)
	if c := e.container(t, "mcpanel-mc-1"); c.Status != "exited" || !slices.Equal(c.StopTimes, []int{60}) {
		t.Fatalf("after stop: %+v", c)
	}
	e.power(t, 1, agentapi.PowerStart)
	mark := len(e.d.Calls())
	e.power(t, 1, agentapi.PowerRestart)
	if got := e.callsAfter(mark); !slices.Equal(got, []string{"inspect mcpanel-mc-1", "stop mcpanel-mc-1", "start mcpanel-mc-1"}) {
		t.Fatalf("restart calls = %v", got)
	}
	if s := e.status(t, 1); s.State != agentapi.StateRunning || s.Operation != agentapi.OpNone || s.LastError != "" {
		t.Fatalf("status = %+v", s)
	}
}

func TestPowerOnMissingContainerSetsLastError(t *testing.T) {
	e := newEnv(t)
	e.power(t, 9, agentapi.PowerStart) // accepted (202), fails asynchronously
	s := e.status(t, 9)
	if s.State != agentapi.StateMissing || !strings.Contains(s.LastError, "mcpanel-mc-9 does not exist") {
		t.Fatalf("status = %+v", s)
	}
	if _, ok := e.d.Container("mcpanel-mc-9"); ok {
		t.Fatal("power must never create a container")
	}
}

// TestForeignContainersAreNeverTouched is Review Focus #2: a container that only
// shares the name (no mcpanel.instance label, or a label for another id) is
// neither stopped nor removed nor replaced, and its data stays in place.
func TestForeignContainersAreNeverTouched(t *testing.T) {
	e := newEnv(t)
	foreign := map[int64]docker.ContainerConfig{
		7: {Image: "nginx@sha256:" + strings.Repeat("ab", 32), Labels: map[string]string{"com.example.app": "blog"}},
		8: {Image: "nginx@sha256:" + strings.Repeat("ab", 32), Labels: map[string]string{
			"mcpanel.instance": "9", "mcpanel.name": "evil", "mcpanel.fqdn": "evil.mc.example.com",
		}},
	}
	before := map[int64]dockertest.Container{}
	for id, cfg := range foreign {
		before[id] = e.d.AddContainer(instances.ContainerName(id), cfg, true)
		dataDir := instances.DataDir(e.dir, id)
		if err := os.MkdirAll(filepath.Join(dataDir, "world"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "world", "level.dat"), []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for id := range foreign {
		req := ensureReq(id, "srv"+string(rune('0'+id)), 1)
		req.Recreate = true
		e.ensure(t, req)
		e.power(t, id, agentapi.PowerStop)
		e.power(t, id, agentapi.PowerRestart)
		e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, id) })
	}
	for id := range foreign {
		name := instances.ContainerName(id)
		c := e.container(t, name)
		if c.ID != before[id].ID || c.Status != "running" || len(c.Kills) != 0 || len(c.StopTimes) != 0 || c.Config.Image != before[id].Config.Image {
			t.Errorf("%s was touched: %+v", name, c)
		}
		for _, call := range e.d.Calls() {
			if strings.HasSuffix(call, " "+name) && !strings.HasPrefix(call, "inspect ") {
				t.Errorf("forbidden call on foreign container: %q", call)
			}
		}
		if _, err := os.Stat(filepath.Join(instances.DataDir(e.dir, id), "world", "level.dat")); err != nil {
			t.Errorf("data of %s moved or deleted: %v", name, err)
		}
		if s := e.status(t, id); !strings.Contains(s.LastError, "not managed by mc-panel") {
			t.Errorf("%s: last_error = %q", name, s.LastError)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(e.dir, "deleted")); len(entries) != 0 {
		t.Fatalf("deleted/ = %v", entries)
	}
	e.m.Tick(context.Background())
	if strings.Contains(e.routes(t), "evil") {
		t.Fatalf("foreign container got a route: %s", e.routes(t))
	}
}

func TestRemoveArchivesDataAndRoute(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	if err := os.WriteFile(filepath.Join(e.dir, "instances", "1", "level.dat"), []byte("world"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })

	if _, ok := e.d.Container("mcpanel-mc-1"); ok {
		t.Fatal("container still exists")
	}
	got, err := os.ReadFile(filepath.Join(e.dir, "deleted", "survival-20261010T120000Z", "level.dat"))
	if err != nil || string(got) != "world" {
		t.Fatalf("archived data = %q, %v", got, err)
	}
	if strings.Contains(e.routes(t), "survival") {
		t.Fatalf("route not removed: %s", e.routes(t))
	}
	// The router was created after the first routes file was written, so only the
	// removal needs a reload.
	if r := e.container(t, "mcpanel-router"); !slices.Equal(r.Kills, []string{"SIGHUP"}) {
		t.Fatalf("router kills = %v; want exactly one SIGHUP", r.Kills)
	}
	if st := e.m.Status(context.Background()); len(st.Instances) != 0 {
		t.Fatalf("removed instance still listed: %+v", st.Instances)
	}

	// Same name removed again within the same second: numeric suffix, nothing overwritten.
	e.ensure(t, ensureReq(1, "survival", 1))
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	if _, err := os.Stat(filepath.Join(e.dir, "deleted", "survival-20261010T120000Z-2")); err != nil {
		t.Fatalf("second archive: %v", err)
	}
	// Removing an unknown instance is idempotent.
	e.do(t, func(ctx context.Context) error { return e.m.Remove(ctx, 1) })
	if st := e.m.Status(context.Background()); len(st.Instances) != 0 {
		t.Fatalf("status = %+v", st.Instances)
	}
}

func TestQueueIsSerialAndBounded(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1))
	ctx := context.Background()

	arrived, release := e.d.Hold(dockertest.OpStart)
	defer release()
	if err := e.m.Power(ctx, 1, agentapi.PowerStart); err != nil {
		t.Fatal(err)
	}
	<-arrived // the first operation now blocks inside Docker
	if s := e.status(t, 1); s.Operation != agentapi.OpStarting {
		t.Fatalf("operation while starting = %q", s.Operation)
	}
	for _, a := range []agentapi.PowerAction{agentapi.PowerStop, agentapi.PowerStart, agentapi.PowerStop} {
		if err := e.m.Power(ctx, 1, a); err != nil {
			t.Fatalf("queue %s: %v", a, err)
		}
	}
	if err := e.m.Power(ctx, 1, agentapi.PowerStart); !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("fifth open operation = %v, want ErrBusy", err)
	}
	if err := e.m.Power(ctx, 2, agentapi.PowerStop); err != nil {
		t.Fatalf("other instances have their own queue: %v", err)
	}
	release()
	e.m.Wait()

	var order []string
	for _, call := range e.d.Calls() {
		if call == "start mcpanel-mc-1" || call == "stop mcpanel-mc-1" {
			order = append(order, call)
		}
	}
	want := []string{"start mcpanel-mc-1", "stop mcpanel-mc-1", "start mcpanel-mc-1", "stop mcpanel-mc-1"}
	if !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if err := e.m.Power(ctx, 1, agentapi.PowerStart); err != nil {
		t.Fatalf("queue must accept work again: %v", err)
	}
	e.m.Wait()
}

func TestLastErrorIsRedactedAndBounded(t *testing.T) {
	e := newEnv(t)
	key := "$2a$10$verysecretcurseforgekey"
	e.d.Fail(dockertest.OpCreate, http.StatusInternalServerError,
		"create failed: CF_API_KEY="+key+"\ninjected line "+strings.Repeat("x", 2000))
	req := ensureReq(3, "packs", 1)
	req.Spec = instancespec.Spec{
		Type: instancespec.TypeCurseForge, Modpack: ptr("atm10"), CurseForgeAPIKey: &key,
		Java: instancespec.Java21, MemoryMiB: 8192,
	}
	e.ensure(t, req)
	s := e.status(t, 3)
	if s.State != agentapi.StateMissing || !strings.Contains(s.LastError, "create failed") ||
		!strings.Contains(s.LastError, "[redacted]") || strings.Contains(s.LastError, key) ||
		strings.ContainsAny(s.LastError, "\n\r") || utf8.RuneCountInString(s.LastError) > 500 {
		t.Fatalf("last_error = %q", s.LastError)
	}
	if strings.Contains(e.logs.String(), key) {
		t.Fatal("the CurseForge key reached the log")
	}

	e.d.Clear()
	e.ensure(t, req)
	if s := e.status(t, 3); s.LastError != "" || s.State != agentapi.StateCreated {
		t.Fatalf("a successful retry must clear last_error: %+v", s)
	}
}

func TestPullFailureIsReported(t *testing.T) {
	e := newEnv(t)
	e.d.FailPull("manifest unknown")
	e.ensure(t, ensureReq(1, "survival", 1))
	if s := e.status(t, 1); s.State != agentapi.StateMissing || !strings.Contains(s.LastError, "manifest unknown") {
		t.Fatalf("status = %+v", s)
	}
	if _, ok := e.d.Container("mcpanel-mc-1"); ok {
		t.Fatal("no container may be created without its image")
	}
}

func TestDockerUnavailable(t *testing.T) {
	logs := &syncBuffer{}
	m := instances.New(instances.Config{
		Docker: docker.New(filepath.Join(t.TempDir(), "missing.sock")), InstancesDir: t.TempDir(),
		UID: os.Getuid(), GID: os.Getgid(), Images: config.DefaultImages(),
		Clock: clock.NewFake(time.Unix(0, 0)), Log: slog.New(slog.NewTextHandler(logs, nil)),
	})
	defer m.Close()
	ctx := context.Background()
	if m.CheckDocker(ctx) || m.DockerStatus() != agentapi.DockerUnavailable {
		t.Fatal("docker must be unavailable")
	}
	for name, err := range map[string]error{
		"ensure": m.Ensure(ctx, ensureReq(1, "survival", 1)),
		"power":  m.Power(ctx, 1, agentapi.PowerStart),
		"remove": m.Remove(ctx, 1),
	} {
		if !errors.Is(err, agentapi.ErrDockerUnavailable) {
			t.Errorf("%s = %v, want ErrDockerUnavailable", name, err)
		}
	}
	st := m.Status(ctx)
	if st.Docker != agentapi.DockerUnavailable || st.Instances == nil || len(st.Instances) != 0 || st.Router.State != agentapi.StateMissing {
		t.Fatalf("status = %+v", st)
	}
	m.Tick(ctx) // must not panic or block without Docker
}

func TestDockerRecoversImmediately(t *testing.T) {
	e := newEnv(t)
	e.d.Fail(dockertest.OpPing, http.StatusInternalServerError, "daemon restarting")
	if e.m.CheckDocker(context.Background()) {
		t.Fatal("ping failed, docker must be unavailable")
	}
	if err := e.m.Ensure(context.Background(), ensureReq(1, "survival", 1)); !errors.Is(err, agentapi.ErrDockerUnavailable) {
		t.Fatalf("Ensure = %v", err)
	}
	e.d.Clear()
	e.ensure(t, ensureReq(1, "survival", 1)) // re-checks right away, no need to wait for Tick
	if e.m.DockerStatus() != agentapi.DockerOK {
		t.Fatal("docker must be ok again")
	}
}

func TestInvalidRequestsAreRejected(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	bad := ensureReq(1, "survival", 1)
	bad.Spec.Properties.MOTD = ptr("a\nJVM_OPTS=-Xmx1g")
	for name, err := range map[string]error{
		"ensure": e.m.Ensure(ctx, bad),
		"power":  e.m.Power(ctx, 1, "kill"),
		"id":     e.m.Power(ctx, 0, agentapi.PowerStart),
		"remove": e.m.Remove(ctx, -1),
	} {
		if !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("%s = %v, want ErrInvalid", name, err)
		}
	}
	if calls := e.d.Calls(); len(calls) != 0 {
		t.Fatalf("invalid requests reached Docker: %v", calls)
	}
}

func TestStatsSampling(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.ensure(t, ensureReq(1, "survival", 1))
	e.power(t, 1, agentapi.PowerStart)
	stats := func(total, system uint64, mem docker.MemoryStats) docker.Stats {
		return docker.Stats{
			CPUStats:    docker.CPUStats{CPUUsage: docker.CPUUsage{TotalUsage: total}, SystemCPUUsage: system, OnlineCPUs: 4},
			MemoryStats: mem,
		}
	}
	v2 := docker.MemoryStats{Usage: 3 << 30, Limit: 5 << 30, Stats: map[string]uint64{"inactive_file": 1 << 30}}
	e.d.SetStats("mcpanel-mc-1", stats(1000, 100000, v2))
	e.m.SampleStats(ctx)
	if s := e.status(t, 1); s.CPUPercent != 0 || s.MemoryBytes != 2<<30 || s.MemoryLimitBytes != 5<<30 {
		t.Fatalf("first sample = %+v (no previous sample: cpu 0)", s)
	}
	v1 := docker.MemoryStats{Usage: 3 << 30, Limit: 5 << 30, Stats: map[string]uint64{"cache": 512 << 20}}
	e.d.SetStats("mcpanel-mc-1", stats(1500, 110000, v1))
	e.m.SampleStats(ctx)
	if s := e.status(t, 1); s.CPUPercent != 20 || s.MemoryBytes != (3<<30)-(512<<20) {
		t.Fatalf("second sample = %+v; want cpu 500/10000*4*100 = 20", s)
	}
	e.power(t, 1, agentapi.PowerStop)
	e.m.SampleStats(ctx)
	if s := e.status(t, 1); s.CPUPercent != 0 || s.MemoryBytes != 0 || s.MemoryLimitBytes != 0 {
		t.Fatalf("stopped container keeps stats: %+v", s)
	}
}

func TestTickManagesRouter(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.m.Tick(ctx)
	if e.routes(t) != "{\n  \"mappings\": {}\n}\n" {
		t.Fatalf("routes = %q", e.routes(t))
	}
	r := e.container(t, "mcpanel-router")
	if r.Status != "running" || r.Config.Image != routerPinned {
		t.Fatalf("router = %+v", r)
	}
	mark := len(e.d.Calls())
	e.m.Tick(ctx)
	for _, call := range e.callsAfter(mark) {
		if strings.HasPrefix(call, "create") || strings.HasPrefix(call, "kill") || strings.HasPrefix(call, "start") {
			t.Fatalf("unchanged routes must not touch the router: %v", e.callsAfter(mark))
		}
	}
	e.d.SetState("mcpanel-router", "exited", 2)
	e.m.Tick(ctx)
	if again := e.container(t, "mcpanel-router"); again.Status != "running" || again.ID != r.ID {
		t.Fatalf("stopped router must be started again (same container): %+v", again)
	}

	// Agent upgrade with a new pinned router image: the router container is replaced.
	images := config.DefaultImages()
	images.Router = "itzg/mc-router:1.48.0@sha256:" + strings.Repeat("cd", 32)
	m2 := instances.New(instances.Config{
		Docker: docker.New(e.d.Socket), InstancesDir: e.dir, UID: os.Getuid(), GID: os.Getgid(),
		Images: images, Clock: e.clk, Log: slog.New(slog.NewTextHandler(e.logs, nil)),
	})
	defer m2.Close()
	m2.Tick(ctx)
	if r2 := e.container(t, "mcpanel-router"); r2.ID == r.ID || r2.Config.Image != "itzg/mc-router@sha256:"+strings.Repeat("cd", 32) || r2.Status != "running" {
		t.Fatalf("router not replaced: %+v", r2)
	}
}

func TestForeignRouterIsNeverTouched(t *testing.T) {
	e := newEnv(t)
	before := e.d.AddContainer("mcpanel-router", docker.ContainerConfig{Image: "nginx@sha256:" + strings.Repeat("ab", 32)}, true)
	e.m.Tick(context.Background())
	after := e.container(t, "mcpanel-router")
	if after.ID != before.ID || len(after.Kills) != 0 || after.Status != "running" {
		t.Fatalf("foreign router touched: %+v", after)
	}
	st := e.m.Status(context.Background())
	if st.Router.State != agentapi.StateMissing || !strings.Contains(st.Router.LastError, "not managed by mc-panel") {
		t.Fatalf("router status = %+v", st.Router)
	}
}

func TestCloseRejectsNewWork(t *testing.T) {
	e := newEnv(t)
	e.m.Close()
	if err := e.m.Power(context.Background(), 1, agentapi.PowerStart); !errors.Is(err, agentapi.ErrDockerUnavailable) {
		t.Fatalf("Power after Close = %v", err)
	}
}
```

(`ptr` stammt aus `template_test.go`, gleiches Paket `instances_test`.)

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./internal/agent/instances/ -count=1`
Expected: FAIL beim Kompilieren (`undefined: instances.New`, `instances.Config`, `instances.Manager`).

- [ ] **Step 3: Warteschlange implementieren** – `internal/agent/instances/worker.go`

```go
package instances

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
)

const (
	// MaxOpenOperations bounds the operations per instance (running + waiting).
	MaxOpenOperations = 4
	// MaxTrackedInstances bounds the per-instance state the agent keeps in memory.
	MaxTrackedInstances = 256

	maxLastErrorRunes = 500
	redacted          = "[redacted]"
)

type jobKind string

const (
	jobEnsure jobKind = "ensure"
	jobPower  jobKind = "power"
	jobRemove jobKind = "remove"
)

type job struct {
	kind   jobKind
	ensure agentapi.EnsureRequest
	action agentapi.PowerAction
}

// instanceState is the in-memory worker state of one instance. It exists while
// operations are queued or running, and afterwards only to keep a last_error.
type instanceState struct {
	pending   []job
	running   bool
	op        agentapi.Operation
	lastError string
	name      string
}

func (m *Manager) enqueue(ctx context.Context, id int64, j job) error {
	if !m.dockerAvailable(ctx) {
		return agentapi.ErrDockerUnavailable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("%w: agent is shutting down", agentapi.ErrDockerUnavailable)
	}
	st := m.inst[id]
	if st == nil {
		if len(m.inst) >= MaxTrackedInstances {
			return agentapi.ErrBusy
		}
		st = &instanceState{}
		m.inst[id] = st
	}
	open := len(st.pending)
	if st.running {
		open++
	}
	if open >= MaxOpenOperations {
		return agentapi.ErrBusy
	}
	if j.kind == jobEnsure {
		st.name = j.ensure.Name
	}
	st.pending = append(st.pending, j)
	m.log.Info("instance operation queued", "id", id, "op", j.kind, "action", j.action, "revision", j.ensure.Revision)
	if !st.running {
		st.running = true
		m.active++
		go m.work(id)
	}
	return nil
}

// work drains the queue of one instance; exactly one worker runs per instance.
func (m *Manager) work(id int64) {
	for {
		m.mu.Lock()
		st := m.inst[id]
		if m.closed {
			st.pending = nil
		}
		if len(st.pending) == 0 {
			st.running = false
			st.op = agentapi.OpNone
			if st.lastError == "" {
				delete(m.inst, id)
			}
			m.active--
			m.idle.Broadcast()
			m.mu.Unlock()
			return
		}
		j := st.pending[0]
		st.pending = st.pending[1:]
		st.op = initialOp(j)
		m.mu.Unlock()

		err := m.run(id, j)

		m.mu.Lock()
		st.op = agentapi.OpNone
		if err != nil {
			st.lastError = sanitizeError(err, secretsOf(j)...)
		} else {
			st.lastError = ""
		}
		msg := st.lastError
		m.mu.Unlock()
		if err != nil {
			m.log.Warn("instance operation failed", "id", id, "op", j.kind, "action", j.action, "err", msg)
		} else {
			m.log.Info("instance operation done", "id", id, "op", j.kind, "action", j.action)
		}
	}
}

func initialOp(j job) agentapi.Operation {
	switch {
	case j.kind == jobEnsure:
		return agentapi.OpCreating
	case j.kind == jobRemove:
		return agentapi.OpRemoving
	case j.action == agentapi.PowerStart:
		return agentapi.OpStarting
	default:
		return agentapi.OpStopping
	}
}

func secretsOf(j job) []string {
	if j.kind == jobEnsure && j.ensure.Spec.CurseForgeAPIKey != nil {
		return []string{*j.ensure.Spec.CurseForgeAPIKey}
	}
	return nil
}

func (m *Manager) run(id int64, j job) error {
	var err error
	switch j.kind {
	case jobEnsure:
		err = m.ensure(m.ctx, j.ensure)
	case jobPower:
		err = m.power(m.ctx, id, j.action)
	case jobRemove:
		err = m.remove(m.ctx, id)
	}
	if errors.Is(err, docker.ErrUnavailable) {
		m.setDocker(false, err)
	}
	return err
}

func (m *Manager) setOp(id int64, op agentapi.Operation) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st := m.inst[id]; st != nil {
		st.op = op
	}
}

// sanitizeError makes an error safe for last_error: secrets replaced, control
// characters flattened, at most 500 characters.
func sanitizeError(err error, secrets ...string) string {
	s := err.Error()
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, redacted)
		}
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "?"))
	if utf8.RuneCountInString(s) > maxLastErrorRunes {
		s = string([]rune(s)[:maxLastErrorRunes-1]) + "…"
	}
	return s
}

// Wait blocks until no operation is queued or running.
func (m *Manager) Wait() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.active > 0 {
		m.idle.Wait()
	}
}

// Close rejects new operations, cancels running Docker calls, drops queued
// operations and waits for the workers to finish.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.cancel()
	m.Wait()
}
```

- [ ] **Step 4: Manager implementieren** – `internal/agent/instances/manager.go`

```go
package instances

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

const (
	// SampleInterval is the period of the background loop (Docker check, router, stats).
	SampleInterval = 10 * time.Second

	pingTimeout = 5 * time.Second
)

// Config wires a Manager. Docker, Clock and Log are required.
type Config struct {
	Docker       *docker.Client
	InstancesDir string
	UID, GID     int
	Images       config.Images
	Clock        clock.Clock
	Log          *slog.Logger
	// Customize may adjust a container configuration right before it is created.
	// Only the dockerintegration test sets it (small test image); nil in production.
	Customize func(name string, cfg *docker.ContainerConfig)
}

type sample struct {
	cpuTotal, systemTotal uint64
	memory, limit         int64
	cpuPercent            float64
}

// Manager executes instance operations asynchronously, one serial queue per
// instance, and reports the actual state read from Docker. It keeps no
// persistent state: managed containers are found via their labels.
type Manager struct {
	dc        *docker.Client
	host      Host
	clock     clock.Clock
	log       *slog.Logger
	customize func(string, *docker.ContainerConfig)

	ctx    context.Context // cancelled by Close; bounds all queued work
	cancel context.CancelFunc

	routerMu sync.Mutex // serialises routes file + router container changes

	mu        sync.Mutex
	idle      *sync.Cond
	active    int // running workers
	closed    bool
	inst      map[int64]*instanceState
	stats     map[int64]sample
	dockerOK  bool
	routerErr string
}

func New(cfg Config) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		dc:        cfg.Docker,
		host:      Host{InstancesDir: cfg.InstancesDir, UID: cfg.UID, GID: cfg.GID, Images: cfg.Images},
		clock:     cfg.Clock,
		log:       cfg.Log,
		customize: cfg.Customize,
		ctx:       ctx,
		cancel:    cancel,
		inst:      map[int64]*instanceState{},
		stats:     map[int64]sample{},
	}
	m.idle = sync.NewCond(&m.mu)
	return m
}

// Ensure queues an ensure operation. It returns instancespec.ErrInvalid,
// agentapi.ErrBusy or agentapi.ErrDockerUnavailable without queueing anything.
func (m *Manager) Ensure(ctx context.Context, req agentapi.EnsureRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	return m.enqueue(ctx, req.ID, job{kind: jobEnsure, ensure: req})
}

// Power queues start, stop or restart of an existing container.
func (m *Manager) Power(ctx context.Context, id int64, action agentapi.PowerAction) error {
	if id <= 0 || !agentapi.ValidPowerAction(action) {
		return fmt.Errorf("%w: power: id must be > 0 and action start, stop or restart", instancespec.ErrInvalid)
	}
	return m.enqueue(ctx, id, job{kind: jobPower, action: action})
}

// Remove queues stop + removal of the container and archiving of its data.
func (m *Manager) Remove(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("%w: remove: id must be > 0", instancespec.ErrInvalid)
	}
	return m.enqueue(ctx, id, job{kind: jobRemove})
}

// checkManaged refuses containers that only share the name: mc-panel never
// stops, removes or replaces a container without its own mcpanel.instance label.
func checkManaged(info docker.ContainerInfo, id int64) error {
	if got, ok := managedID(info.Config.Labels, info.Name); !ok || got != id {
		return fmt.Errorf("container %s exists but is not managed by mc-panel (label %s=%d missing); leaving it untouched",
			ContainerName(id), LabelInstance, id)
	}
	return nil
}

func labelRevision(labels map[string]string) int64 {
	r, err := strconv.ParseInt(labels[LabelRevision], 10, 64)
	if err != nil {
		return 0
	}
	return r
}

func (m *Manager) ensure(ctx context.Context, req agentapi.EnsureRequest) error {
	cfg, err := InstanceContainer(m.host, req)
	if err != nil {
		return err
	}
	name := ContainerName(req.ID)
	if m.customize != nil {
		m.customize(name, &cfg)
	}
	info, err := m.dc.Inspect(ctx, name)
	exists := err == nil
	if err != nil && !docker.IsNotFound(err) {
		return err
	}
	if exists {
		if err := checkManaged(info, req.ID); err != nil {
			return err
		}
		if labelRevision(info.Config.Labels) == req.Revision || !req.Recreate {
			m.refreshRouterAfter(ctx)
			return nil
		}
	}
	// Pull before touching the old container: keeps the downtime of a recreate short.
	if err := m.ensureImage(ctx, req.ID, cfg.Image); err != nil {
		return err
	}
	if err := m.ensureDataDir(req.ID); err != nil {
		return err
	}
	if err := m.dc.EnsureNetwork(ctx, NetworkName, map[string]string{LabelNetwork: "1"}); err != nil {
		return err
	}
	wasRunning := false
	if exists {
		wasRunning = info.State.Running || info.State.Restarting
		m.setOp(req.ID, agentapi.OpStopping)
		if err := m.dc.Stop(ctx, name, StopTimeoutSeconds); err != nil && !docker.IsNotFound(err) {
			return err
		}
		m.setOp(req.ID, agentapi.OpRemoving)
		if err := m.dc.Remove(ctx, name, true); err != nil && !docker.IsNotFound(err) {
			return err
		}
	}
	m.setOp(req.ID, agentapi.OpCreating)
	if _, err := m.dc.CreateContainer(ctx, name, cfg); err != nil {
		return err
	}
	if wasRunning {
		m.setOp(req.ID, agentapi.OpStarting)
		if err := m.dc.Start(ctx, name); err != nil {
			return err
		}
	}
	m.refreshRouterAfter(ctx)
	return nil
}

// ensureImage pulls ref if it is missing; id < 0 means "no instance operation to report".
func (m *Manager) ensureImage(ctx context.Context, id int64, ref string) error {
	ok, err := m.dc.ImageExists(ctx, ref)
	if err != nil || ok {
		return err
	}
	m.setOp(id, agentapi.OpPulling)
	defer m.setOp(id, agentapi.OpCreating)
	m.log.Info("pulling image", "image", ref)
	return m.dc.PullImage(ctx, ref)
}

func (m *Manager) power(ctx context.Context, id int64, action agentapi.PowerAction) error {
	name := ContainerName(id)
	info, err := m.dc.Inspect(ctx, name)
	if docker.IsNotFound(err) {
		return fmt.Errorf("container %s does not exist", name)
	}
	if err != nil {
		return err
	}
	if err := checkManaged(info, id); err != nil {
		return err
	}
	switch action {
	case agentapi.PowerStart:
		m.setOp(id, agentapi.OpStarting)
		return m.dc.Start(ctx, name)
	case agentapi.PowerStop:
		m.setOp(id, agentapi.OpStopping)
		return m.dc.Stop(ctx, name, StopTimeoutSeconds)
	default: // restart: stop and start separately so the reported operation is accurate
		m.setOp(id, agentapi.OpStopping)
		if err := m.dc.Stop(ctx, name, StopTimeoutSeconds); err != nil {
			return err
		}
		m.setOp(id, agentapi.OpStarting)
		return m.dc.Start(ctx, name)
	}
}

func (m *Manager) remove(ctx context.Context, id int64) error {
	name := ContainerName(id)
	info, err := m.dc.Inspect(ctx, name)
	exists := err == nil
	if err != nil && !docker.IsNotFound(err) {
		return err
	}
	m.mu.Lock()
	instName := ""
	if st := m.inst[id]; st != nil {
		instName = st.name
	}
	m.mu.Unlock()
	if exists {
		if err := checkManaged(info, id); err != nil {
			return err
		}
		if n := info.Config.Labels[LabelName]; instancespec.ValidName(n) {
			instName = n
		}
		m.setOp(id, agentapi.OpStopping)
		if err := m.dc.Stop(ctx, name, StopTimeoutSeconds); err != nil && !docker.IsNotFound(err) {
			return err
		}
		m.setOp(id, agentapi.OpRemoving)
		if err := m.dc.Remove(ctx, name, true); err != nil && !docker.IsNotFound(err) {
			return err
		}
	}
	m.setOp(id, agentapi.OpRemoving)
	if err := m.archiveData(id, instName); err != nil {
		return err
	}
	m.refreshRouterAfter(ctx)
	return nil
}

// refreshRouterAfter updates routes and router after an instance operation. Router
// problems are reported in RouterStatus, not as the instance's last_error.
func (m *Manager) refreshRouterAfter(ctx context.Context) {
	_ = m.RefreshRouter(ctx)
}

// RefreshRouter rewrites the routes file from the managed containers, makes sure
// the router container exists, runs the pinned image and is started, and sends
// SIGHUP when the routes changed under a running router.
func (m *Manager) RefreshRouter(ctx context.Context) error {
	m.routerMu.Lock()
	err := m.refreshRouter(ctx)
	m.routerMu.Unlock()

	msg := ""
	if err != nil {
		msg = sanitizeError(err)
		if errors.Is(err, docker.ErrUnavailable) {
			m.setDocker(false, err)
		}
	}
	m.mu.Lock()
	changed := m.routerErr != msg
	m.routerErr = msg
	m.mu.Unlock()
	if changed && err != nil {
		m.log.Warn("router not ready", "err", msg)
	} else if changed {
		m.log.Info("router ready")
	}
	return err
}

func (m *Manager) refreshRouter(ctx context.Context) error {
	list, err := m.dc.List(ctx, LabelInstance)
	if err != nil {
		return err
	}
	data, err := RenderRoutes(Routes(list))
	if err != nil {
		return err
	}
	// Always write the file before the router container exists: without the watch
	// flag a missing file is silently skipped at start.
	changed, err := WriteRoutes(RouterDir(m.host.InstancesDir), data)
	if err != nil {
		return err
	}
	cfg, err := RouterContainer(m.host)
	if err != nil {
		return err
	}
	if m.customize != nil {
		m.customize(RouterName, &cfg)
	}
	info, err := m.dc.Inspect(ctx, RouterName)
	switch {
	case docker.IsNotFound(err):
		return m.createRouter(ctx, cfg)
	case err != nil:
		return err
	}
	if info.Config.Labels[LabelRouter] != "1" {
		return fmt.Errorf("container %s exists but is not managed by mc-panel (label %s=1 missing); leaving it untouched",
			RouterName, LabelRouter)
	}
	if info.Config.Image != cfg.Image { // the agent was upgraded to a new pinned router image
		m.log.Info("replacing router container", "old_image", info.Config.Image, "new_image", cfg.Image)
		if err := m.ensureImage(ctx, -1, cfg.Image); err != nil {
			return err
		}
		if err := m.dc.Remove(ctx, RouterName, true); err != nil && !docker.IsNotFound(err) {
			return err
		}
		return m.createRouter(ctx, cfg)
	}
	if !info.State.Running {
		return m.dc.Start(ctx, RouterName) // a fresh start reads the current file
	}
	if changed {
		if err := m.dc.Kill(ctx, RouterName, "SIGHUP"); err != nil && !docker.IsConflict(err) {
			return err
		}
		m.log.Info("router reloaded", "routes", len(data))
	}
	return nil
}

func (m *Manager) createRouter(ctx context.Context, cfg docker.ContainerConfig) error {
	if err := m.ensureImage(ctx, -1, cfg.Image); err != nil {
		return err
	}
	if err := m.dc.EnsureNetwork(ctx, NetworkName, map[string]string{LabelNetwork: "1"}); err != nil {
		return err
	}
	if _, err := m.dc.CreateContainer(ctx, RouterName, cfg); err != nil {
		return err
	}
	m.log.Info("router container created", "image", cfg.Image)
	return m.dc.Start(ctx, RouterName)
}

// dockerAvailable uses the cached state and re-checks right away when it is bad,
// so writes succeed as soon as Docker is back.
func (m *Manager) dockerAvailable(ctx context.Context) bool {
	m.mu.Lock()
	ok := m.dockerOK
	m.mu.Unlock()
	return ok || m.CheckDocker(ctx)
}

// CheckDocker pings Docker and updates the cached state.
func (m *Manager) CheckDocker(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	err := m.dc.Ping(ctx)
	m.setDocker(err == nil, err)
	return err == nil
}

func (m *Manager) setDocker(ok bool, err error) {
	m.mu.Lock()
	changed := m.dockerOK != ok
	m.dockerOK = ok
	m.mu.Unlock()
	switch {
	case changed && ok:
		m.log.Info("docker available")
	case changed:
		m.log.Warn("docker unavailable", "err", err)
	}
}

// DockerStatus returns the cached state: agentapi.DockerOK or agentapi.DockerUnavailable.
func (m *Manager) DockerStatus() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dockerOK {
		return agentapi.DockerOK
	}
	return agentapi.DockerUnavailable
}

func mapState(dockerStatus string) agentapi.ContainerState {
	switch dockerStatus {
	case "running", "paused":
		return agentapi.StateRunning
	case "restarting":
		return agentapi.StateRestarting
	case "created":
		return agentapi.StateCreated
	default: // exited, dead, removing
		return agentapi.StateExited
	}
}

func startedAtMillis(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() <= 1 {
		return 0
	}
	return t.UnixMilli()
}

// Status reads the actual state from Docker and merges the worker state and the
// cached stats. Instances with queued work or a last_error but no container are
// reported with state "missing".
func (m *Manager) Status(ctx context.Context) agentapi.InstancesResponse {
	resp := agentapi.InstancesResponse{
		Docker:    agentapi.DockerUnavailable,
		Instances: []agentapi.InstanceStatus{},
		Router:    agentapi.RouterStatus{State: agentapi.StateMissing},
	}
	list, listErr := m.dc.List(ctx, LabelInstance)

	m.mu.Lock()
	work := make(map[int64]instanceState, len(m.inst))
	for id, st := range m.inst {
		work[id] = instanceState{op: st.op, lastError: st.lastError, name: st.name}
	}
	stats := make(map[int64]sample, len(m.stats))
	for id, s := range m.stats {
		stats[id] = s
	}
	resp.Router.LastError = m.routerErr
	m.mu.Unlock()

	if listErr != nil {
		m.setDocker(false, listErr)
		return resp
	}
	m.setDocker(true, nil)
	resp.Docker = agentapi.DockerOK

	seen := map[int64]bool{}
	for _, c := range list {
		id, ok := managedID(c.Labels, c.Names...)
		if !ok || seen[id] {
			continue
		}
		st := agentapi.InstanceStatus{
			ID: id, Name: c.Labels[LabelName], Revision: labelRevision(c.Labels), State: mapState(c.State),
		}
		info, err := m.dc.Inspect(ctx, ContainerName(id))
		switch {
		case docker.IsNotFound(err):
			continue // removed since the listing
		case err == nil:
			st.State = mapState(info.State.Status)
			st.ExitCode = info.State.ExitCode
			if st.State == agentapi.StateRunning || st.State == agentapi.StateRestarting {
				st.StartedAt = startedAtMillis(info.State.StartedAt)
			}
		default:
			m.log.Debug("inspect failed while listing", "id", id, "err", err)
		}
		if s, ok := stats[id]; ok && st.State == agentapi.StateRunning {
			st.MemoryBytes, st.MemoryLimitBytes, st.CPUPercent = s.memory, s.limit, s.cpuPercent
		}
		if w, ok := work[id]; ok {
			st.Operation, st.LastError = w.op, w.lastError
		}
		seen[id] = true
		resp.Instances = append(resp.Instances, st)
	}
	for id, w := range work {
		if !seen[id] {
			resp.Instances = append(resp.Instances, agentapi.InstanceStatus{
				ID: id, Name: w.name, State: agentapi.StateMissing, Operation: w.op, LastError: w.lastError,
			})
		}
	}
	slices.SortFunc(resp.Instances, func(a, b agentapi.InstanceStatus) int { return cmp.Compare(a.ID, b.ID) })

	if info, err := m.dc.Inspect(ctx, RouterName); err == nil && info.Config.Labels[LabelRouter] == "1" {
		resp.Router.State = mapState(info.State.Status)
	}
	return resp
}

// SampleStats takes one stats snapshot of every running managed container and
// computes CPU% from the previous snapshot (one-shot stats carry no precpu).
func (m *Manager) SampleStats(ctx context.Context) {
	list, err := m.dc.List(ctx, LabelInstance)
	if err != nil {
		m.setDocker(false, err)
		return
	}
	m.mu.Lock()
	prev := m.stats
	m.mu.Unlock()
	next := map[int64]sample{}
	for _, c := range list {
		id, ok := managedID(c.Labels, c.Names...)
		if !ok || c.State != "running" {
			continue
		}
		s, err := m.dc.Stats(ctx, c.ID)
		if err != nil {
			m.log.Debug("stats failed", "id", id, "err", err)
			continue
		}
		cur := sample{
			cpuTotal:    s.CPUStats.CPUUsage.TotalUsage,
			systemTotal: s.CPUStats.SystemCPUUsage,
			memory:      memoryUsed(s.MemoryStats),
			limit:       int64(s.MemoryStats.Limit),
		}
		if p, ok := prev[id]; ok {
			cur.cpuPercent = cpuPercent(p, cur, s.CPUStats.OnlineCPUs)
		}
		next[id] = cur
	}
	m.mu.Lock()
	m.stats = next
	m.mu.Unlock()
}

// memoryUsed follows the Docker CLI: usage minus page cache (inactive_file on cgroup v2, cache on v1).
func memoryUsed(s docker.MemoryStats) int64 {
	used := s.Usage
	if v, ok := s.Stats["inactive_file"]; ok && v <= used {
		used -= v
	} else if v, ok := s.Stats["cache"]; ok && v <= used {
		used -= v
	}
	return int64(used)
}

// cpuPercent is the Docker API formula: Δcontainer / Δsystem × online CPUs × 100.
func cpuPercent(prev, cur sample, onlineCPUs uint32) float64 {
	if cur.cpuTotal < prev.cpuTotal || cur.systemTotal <= prev.systemTotal {
		return 0
	}
	cpus := float64(max(onlineCPUs, 1))
	return float64(cur.cpuTotal-prev.cpuTotal) / float64(cur.systemTotal-prev.systemTotal) * cpus * 100
}

// Tick is one round of the background loop: Docker check, routes and router, stats.
func (m *Manager) Tick(ctx context.Context) {
	if !m.CheckDocker(ctx) {
		return
	}
	_ = m.RefreshRouter(ctx)
	m.SampleStats(ctx)
}

// Run calls Tick at start and every SampleInterval until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	m.Tick(ctx)
	t := time.NewTicker(SampleInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.Tick(ctx)
		}
	}
}
```

- [ ] **Step 5: Datenverzeichnisse implementieren** – `internal/agent/instances/datadir.go`

```go
package instances

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// deletedTimeFormat is the UTC timestamp suffix of archived data directories.
const deletedTimeFormat = "20060102T150405Z"

// ensureDataDir creates <instances_dir>/instances/<id> (0750, owned by the
// container user). itzg skips its own chown when started with a non-root User,
// so the directory must belong to that user beforehand. instances/ itself is
// 0700 root: the panel user must not read world data.
func (m *Manager) ensureDataDir(id int64) error {
	if err := os.MkdirAll(m.host.InstancesDir, 0o755); err != nil { //nolint:gosec // G301: spec §3.3, root:root 0755; contains no secrets itself
		return err
	}
	parent := filepath.Join(m.host.InstancesDir, "instances")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	dir := DataDir(m.host.InstancesDir, id)
	if err := os.Mkdir(dir, 0o750); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() { // e.g. a symlink planted in place of the directory
		return fmt.Errorf("data directory %s is not a directory", dir)
	}
	if err := os.Chown(dir, m.host.UID, m.host.GID); err != nil {
		return fmt.Errorf("chown %s to %d:%d: %w", dir, m.host.UID, m.host.GID, err)
	}
	return os.Chmod(dir, 0o750) //nolint:gosec // G302: directory, group mcpanel-mc needs x to traverse
}

// archiveData moves <instances_dir>/instances/<id> to
// <instances_dir>/deleted/<name>-<UTC timestamp> (with a numeric suffix if that
// exists already). A missing data directory is not an error.
func (m *Manager) archiveData(id int64, name string) error {
	src := DataDir(m.host.InstancesDir, id)
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	deleted := DeletedDir(m.host.InstancesDir)
	if err := os.MkdirAll(deleted, 0o700); err != nil {
		return err
	}
	if !instancespec.ValidName(name) {
		name = "instance-" + strconv.FormatInt(id, 10)
	}
	base := name + "-" + m.clock.Now().UTC().Format(deletedTimeFormat)
	dst := filepath.Join(deleted, base)
	for i := 2; ; i++ {
		if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
			break
		} else if err != nil {
			return err
		}
		if i > 100 {
			return fmt.Errorf("archive %s: too many directories named %s-*", src, base)
		}
		dst = filepath.Join(deleted, base+"-"+strconv.Itoa(i))
	}
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	m.log.Info("instance data archived", "id", id, "to", dst)
	return nil
}
```

`os.Chown` existiert auf Linux und macOS (Dev: Besitzer = eigener Benutzer, daher erlaubt); ein Build-Tag ist nicht nötig.

- [ ] **Step 6: Tests laufen lassen (grün, auch mit `-race`)**

Run: `go test ./internal/agent/instances/ -count=1 && CGO_ENABLED=1 go test -race -count=5 ./internal/agent/instances/`
Expected: PASS in beiden Läufen (u. a. `TestForeignContainersAreNeverTouched`, `TestQueueIsSerialAndBounded`, `TestLastErrorIsRedactedAndBounded`).

- [ ] **Step 7: Lint**

Run: `golangci-lint run ./internal/agent/... && GOOS=linux golangci-lint run ./internal/agent/...`
Expected: `0 issues.` (zweimal)

- [ ] **Step 8: Commit**

```bash
git add internal/agent/instances/worker.go internal/agent/instances/manager.go internal/agent/instances/datadir.go internal/agent/instances/manager_test.go
git commit -m "feat(agent): instance manager with per-instance queues, router upkeep and stats

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Endpunkte `/v1/instances*` und Verdrahtung in `cmd/agent`

**Files:**
- Modify: `internal/agent/server.go` (Interface `Instances`, `Option`/`WithInstances`, vier Routen, Body-Limit pro Route, Fehlercodes, Health mit echtem Docker-Status)
- Create: `internal/agent/server_instances_test.go`
- Modify: `cmd/agent/main.go` (Docker-Client + Manager bauen, `Run` im Hintergrund, `Close` beim Herunterfahren)
- Modify: `cmd/agent/main_test.go` (Dev-Configs ohne echten Docker, Ende-zu-Ende-Test gegen den Fake-Daemon)

**Interfaces:**
- Consumes: `instances.Manager` (Aufgabe 8), `docker.New` (Aufgabe 5), `dockertest` (Aufgabe 5), `agentapi.*` (V3), `instancespec.ErrInvalid` (V2)
- Produces:
```go
package agent

type Instances interface {
	DockerStatus() string
	Status(ctx context.Context) agentapi.InstancesResponse
	Ensure(ctx context.Context, req agentapi.EnsureRequest) error
	Power(ctx context.Context, id int64, action agentapi.PowerAction) error
	Remove(ctx context.Context, id int64) error
}
type Option func(*Server)
func WithInstances(in Instances) Option
func NewServer(fw agentapi.Firewall, version string, log *slog.Logger, opts ...Option) *Server // variadic: old calls compile
```

Draht (Spec §5.1, Übersicht „agent-Draht“):

| Pfad | Methode | Body-Limit | Erfolg | Fehler |
|---|---|---|---|---|
| `/v1/health` | GET | – | `200`, `docker` = `Instances.DockerStatus()` (ohne Backend `unavailable`) | – |
| `/v1/instances` | GET | – | `200 InstancesResponse` (auch ohne Docker) | `405` |
| `/v1/instances/ensure` | POST | 16 KiB | `202` ohne Body | `400` (JSON, unbekanntes Feld – auch in `spec`/`properties` –, `EnsureRequest.Validate`), `413`, `415`, `429 busy`, `503 docker_unavailable`, `500` |
| `/v1/instances/power` | POST | 16 KiB | `202` | `400` (`id ≤ 0`, Aktion ∉ start/stop/restart), sonst wie ensure |
| `/v1/instances/remove` | POST | 16 KiB | `202` | `400` (`id ≤ 0`), sonst wie ensure |

`power`/`remove` für eine Instanz ohne Container werden angenommen (`202`); das Ergebnis steht danach als `last_error` im Status (Spec: „fehlt er, setzt der Worker `last_error`“). Die Firewall-Endpunkte behalten ihr 4-KiB-Limit.

- [ ] **Step 1: Failing tests schreiben**

`internal/agent/server_instances_test.go`:

```go
package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// stubInstances records calls and answers every write with err.
type stubInstances struct {
	mu      sync.Mutex
	err     error
	docker  string
	status  agentapi.InstancesResponse
	ensured []agentapi.EnsureRequest
	powered []agentapi.PowerRequest
	removed []int64
}

func (s *stubInstances) DockerStatus() string { return s.docker }

func (s *stubInstances) Status(context.Context) agentapi.InstancesResponse { return s.status }

func (s *stubInstances) Ensure(_ context.Context, req agentapi.EnsureRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensured = append(s.ensured, req)
	return s.err
}

func (s *stubInstances) Power(_ context.Context, id int64, a agentapi.PowerAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.powered = append(s.powered, agentapi.PowerRequest{ID: id, Action: a})
	return s.err
}

func (s *stubInstances) Remove(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removed = append(s.removed, id)
	return s.err
}

func (s *stubInstances) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ensured) + len(s.powered) + len(s.removed)
}

const ensureBody = `{"id":1,"name":"survival","fqdn":"survival.mc.example.com","revision":2,"recreate":true,` +
	`"spec":{"type":"paper","version":"1.21.4","loader_version":null,"modpack":null,"java":"java21",` +
	`"memory_mib":4096,"properties":{"motd":"Hallo","max_players":10},"curseforge_api_key":null}}`

func newInstanceServer(st *stubInstances) *agent.Server {
	fw, _ := newNoop()
	return agent.NewServer(fw, "test", testLogger(io.Discard), agent.WithInstances(st))
}

func TestServerHealthReportsDocker(t *testing.T) {
	rec := serve(newInstanceServer(&stubInstances{docker: agentapi.DockerOK}), http.MethodGet, agentapi.PathHealth, "", "")
	var h agentapi.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil || h.Docker != "ok" {
		t.Fatalf("health = %s (%v)", rec.Body, err)
	}
}

func TestServerInstanceEndpoints(t *testing.T) {
	st := &stubInstances{docker: agentapi.DockerOK, status: agentapi.InstancesResponse{
		Docker:    agentapi.DockerOK,
		Instances: []agentapi.InstanceStatus{{ID: 1, Name: "survival", Revision: 2, State: agentapi.StateRunning, CPUPercent: 12.5}},
		Router:    agentapi.RouterStatus{State: agentapi.StateRunning},
	}}
	srv := newInstanceServer(st)

	rec := serve(srv, http.MethodGet, agentapi.PathInstances, "", "")
	want := `{"docker":"ok","instances":[{"id":1,"name":"survival","revision":2,"state":"running","exit_code":0,` +
		`"started_at":0,"memory_bytes":0,"memory_limit_bytes":0,"cpu_percent":12.5,"operation":"","last_error":""}],` +
		`"router":{"state":"running","last_error":""}}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("GET instances: %d %s", rec.Code, rec.Body)
	}

	for _, tc := range []struct{ path, body string }{
		{agentapi.PathInstanceEnsure, ensureBody},
		{agentapi.PathInstancePower, `{"id":1,"action":"restart"}`},
		{agentapi.PathInstanceRemove, `{"id":1}`},
	} {
		if rec := serve(srv, http.MethodPost, tc.path, jsonCT, tc.body); rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body)
		}
	}
	if len(st.ensured) != 1 || st.ensured[0].Revision != 2 || !st.ensured[0].Recreate || *st.ensured[0].Spec.Properties.MaxPlayers != 10 {
		t.Fatalf("ensured = %+v", st.ensured)
	}
	if len(st.powered) != 1 || st.powered[0] != (agentapi.PowerRequest{ID: 1, Action: agentapi.PowerRestart}) || len(st.removed) != 1 || st.removed[0] != 1 {
		t.Fatalf("powered = %+v removed = %v", st.powered, st.removed)
	}
}

func TestServerInstanceErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{agentapi.ErrBusy, http.StatusTooManyRequests, agentapi.ErrorCodeBusy},
		{fmt.Errorf("%w: shutting down", agentapi.ErrDockerUnavailable), http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable},
		{errors.New("disk on fire at /srv/mc-panel"), http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		srv := newInstanceServer(&stubInstances{err: tc.err})
		rec := serve(srv, http.MethodPost, agentapi.PathInstancePower, jsonCT, `{"id":1,"action":"start"}`)
		var er agentapi.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != tc.status || er.Code != tc.code {
			t.Errorf("%v: %d %s", tc.err, rec.Code, rec.Body)
		}
		if tc.status == http.StatusInternalServerError && strings.Contains(rec.Body.String(), "/srv") {
			t.Errorf("internal error leaked: %s", rec.Body)
		}
	}
}

func TestServerRejectsInvalidInstanceRequests(t *testing.T) {
	withSpec := func(old, repl string) string { return strings.Replace(ensureBody, old, repl, 1) }
	cases := []struct {
		name, method, path, ct, body string
		want                         int
	}{
		{"unknown top-level field", "POST", agentapi.PathInstanceEnsure, jsonCT, strings.Replace(ensureBody, `"id":1`, `"id":1,"image":"evil"`, 1), 400},
		{"unknown spec field", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"java":"java21"`, `"java":"java21","jvm_opts":"-X"`), 400},
		{"unknown property", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"Hallo","plugins":"x"`), 400},
		{"motd newline", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"a\nENABLE_RCON=false"`), 400},
		{"motd nul", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"a\u0000b"`), 400},
		{"memory", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"memory_mib":4096`, `"memory_mib":4000`), 400},
		{"fqdn of other server", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"fqdn":"survival.mc.example.com"`, `"fqdn":"creative.mc.example.com"`), 400},
		{"revision 0", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"revision":2`, `"revision":0`), 400},
		{"recreate string", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"recreate":true`, `"recreate":"yes"`), 400},
		{"trailing data", "POST", agentapi.PathInstanceEnsure, jsonCT, ensureBody + `{}`, 400},
		{"too large", "POST", agentapi.PathInstanceEnsure, jsonCT, withSpec(`"motd":"Hallo"`, `"motd":"`+strings.Repeat("a", 17<<10)+`"`), 413},
		{"wrong content type", "POST", agentapi.PathInstanceEnsure, "text/plain", ensureBody, 415},
		{"power kill", "POST", agentapi.PathInstancePower, jsonCT, `{"id":1,"action":"kill"}`, 400},
		{"power id 0", "POST", agentapi.PathInstancePower, jsonCT, `{"id":0,"action":"start"}`, 400},
		{"power unknown field", "POST", agentapi.PathInstancePower, jsonCT, `{"id":1,"action":"start","signal":9}`, 400},
		{"remove id 0", "POST", agentapi.PathInstanceRemove, jsonCT, `{"id":0}`, 400},
		{"remove negative", "POST", agentapi.PathInstanceRemove, jsonCT, `{"id":-5}`, 400},
		{"remove unknown field", "POST", agentapi.PathInstanceRemove, jsonCT, `{"id":1,"keep_data":false}`, 400},
		{"GET on ensure", "GET", agentapi.PathInstanceEnsure, "", "", 405},
		{"POST on instances", "POST", agentapi.PathInstances, jsonCT, `{}`, 405},
	}
	st := &stubInstances{docker: agentapi.DockerOK}
	srv := newInstanceServer(st)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(srv, tc.method, tc.path, tc.ct, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
			var er agentapi.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || er.Error == "" {
				t.Fatalf("error body = %s (%v)", rec.Body, err)
			}
		})
	}
	if n := st.calls(); n != 0 {
		t.Fatalf("invalid requests reached the manager (%d calls)", n)
	}
}

func TestServerInstanceBodyLimitIs16KiB(t *testing.T) {
	names := make([]string, 200)
	for i := range names {
		names[i] = fmt.Sprintf(`"player_%09d"`, i) // 16 characters each
	}
	list := strings.Join(names, ",")
	body := strings.Replace(ensureBody, `"max_players":10`,
		`"max_players":10,"whitelist":[`+list+`],"ops":[`+strings.Join(names[:50], ",")+`]`, 1)
	if len(body) <= 4<<10 || len(body) > 16<<10 {
		t.Fatalf("body has %d bytes; the test needs 4 KiB < body <= 16 KiB", len(body))
	}
	st := &stubInstances{docker: agentapi.DockerOK}
	if rec := serve(newInstanceServer(st), http.MethodPost, agentapi.PathInstanceEnsure, jsonCT, body); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d (body %s)", rec.Code, rec.Body)
	}
	if len(st.ensured) != 1 || len(st.ensured[0].Spec.Properties.Whitelist) != 200 || len(st.ensured[0].Spec.Properties.Ops) != 50 {
		t.Fatalf("ensured = %d requests", len(st.ensured))
	}
}

func TestServerWithoutInstanceBackend(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	rec := serve(srv, http.MethodGet, agentapi.PathInstances, "", "")
	want := `{"docker":"unavailable","instances":[],"router":{"state":"missing","last_error":""}}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("GET instances: %d %s", rec.Code, rec.Body)
	}
	rec = serve(srv, http.MethodPost, agentapi.PathInstanceEnsure, jsonCT, ensureBody)
	var er agentapi.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != http.StatusServiceUnavailable || er.Code != agentapi.ErrorCodeDockerUnavailable {
		t.Fatalf("ensure without backend: %d %s", rec.Code, rec.Body)
	}
}

func TestClientServerInstancesEndToEnd(t *testing.T) {
	st := &stubInstances{docker: agentapi.DockerOK, err: agentapi.ErrBusy}
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	srv := newInstanceServer(st)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	err = agent.NewClient(path).Power(context.Background(), 1, agentapi.PowerStart)
	if !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("Power = %v, want ErrBusy through the real client", err)
	}
}
```

`cmd/agent/main_test.go` – ab dieser Aufgabe startet `run -dev` den Manager. Ohne eigene Werte würde er den Docker-Socket des Entwicklers (OrbStack) benutzen und nach `cmd/agent/.dev/mc` schreiben; deshalb bekommen alle Dev-Configs der Tests einen nicht existierenden Docker-Socket und ein temporäres `instances_dir`:

1. Importe ergänzen:
```go
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
```
2. Vor `func writeConfig` einfügen:
```go
// devConfig is a -dev config that never touches a real Docker daemon or ./.dev/mc.
func devConfig(t *testing.T, sock, dockerSock string) string {
	t.Helper()
	return fmt.Sprintf("socket = %q\ndocker_socket = %q\ninstances_dir = %q\n", sock, dockerSock, t.TempDir())
}
```
3. In `TestRunDevServesHealthAndShutsDown` die Zeile `cfg := writeConfig(t, fmt.Sprintf("socket = %q\n", sock))` ersetzen durch
```go
	cfg := writeConfig(t, devConfig(t, sock, filepath.Join(t.TempDir(), "no-docker.sock")))
```
und die Health-Prüfung durch
```go
	if h.Status != "ok" || h.Firewall != "noop" || h.Version != version || h.Docker != "unavailable" {
		t.Fatalf("health = %+v", h)
	}
```
4. In `TestRunFailsWhenFirewallUnavailable` und `TestRunRefusesSecondStartBeforeTouchingFirewall` jeweils `cfg := writeConfig(t, fmt.Sprintf("socket = %q\nfirewall = \"nftables\"\n", sock))` ersetzen durch
```go
	cfg := writeConfig(t, devConfig(t, sock, filepath.Join(t.TempDir(), "no-docker.sock"))+"firewall = \"nftables\"\n")
```
5. Ans Dateiende anhängen:
```go
// TestRunDevManagesInstances runs the whole agent against the fake Docker daemon:
// the background loop creates the router, and an ensure through the real client
// creates the instance container.
func TestRunDevManagesInstances(t *testing.T) {
	d := dockertest.New(t)
	sock := socketPath(t)
	cfg := writeConfig(t, devConfig(t, sock, d.Socket))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"-dev", "-config", cfg}, io.Discard, io.Discard) }()
	waitForSocket(t, sock, done)

	c := agent.NewClient(sock)
	waitFor(t, "router running", func() bool {
		st, err := c.Instances(context.Background())
		return err == nil && st.Docker == agentapi.DockerOK && st.Router.State == agentapi.StateRunning
	})
	if h, err := c.Health(context.Background()); err != nil || h.Docker != agentapi.DockerOK {
		t.Fatalf("health = %+v, %v", h, err)
	}
	err := c.Ensure(context.Background(), agentapi.EnsureRequest{
		ID: 1, Name: "survival", FQDN: "survival.mc.example.com", Revision: 1,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 4096},
	})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	waitFor(t, "instance created", func() bool {
		st, err := c.Instances(context.Background())
		return err == nil && len(st.Instances) == 1 && st.Instances[0].State == agentapi.StateCreated && st.Instances[0].Operation == ""
	})
	if ctr, ok := d.Container("mcpanel-mc-1"); !ok || ctr.Config.User != fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()) {
		t.Fatalf("container = %+v, %v; dev mode runs it as the agent's own uid:gid", ctr, ok)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}
```

- [ ] **Step 2: Tests laufen lassen (rot)**

Run: `go test ./internal/agent ./cmd/agent -count=1`
Expected: FAIL beim Kompilieren (`undefined: agent.WithInstances`).

- [ ] **Step 3: Server erweitern** – `internal/agent/server.go` vollständig ersetzen durch:

```go
// Package agent implements the privileged mc-panel agent: the HTTP server on
// the Unix socket (Server, Listen) and the panel-side client (Client).
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/agent/peercred"
)

const maxRequestBytes = 4 << 10

type route struct {
	method  string
	handler http.HandlerFunc
}

// Instances is the instance backend of the server (*instances.Manager).
type Instances interface {
	DockerStatus() string
	Status(ctx context.Context) agentapi.InstancesResponse
	Ensure(ctx context.Context, req agentapi.EnsureRequest) error
	Power(ctx context.Context, id int64, action agentapi.PowerAction) error
	Remove(ctx context.Context, id int64) error
}

// Option configures a Server.
type Option func(*Server)

// WithInstances enables the /v1/instances endpoints. Without it they answer as
// if Docker were unavailable.
func WithInstances(in Instances) Option { return func(s *Server) { s.inst = in } }

// Server serves the agentapi endpoints. It implements http.Handler; Serve
// adds the peer UID to each request context and applies conservative timeouts.
type Server struct {
	fw      agentapi.Firewall
	inst    Instances
	version string
	log     *slog.Logger
	routes  map[string]route
	srv     *http.Server
}

func NewServer(fw agentapi.Firewall, version string, log *slog.Logger, opts ...Option) *Server {
	s := &Server{fw: fw, inst: noInstances{}, version: version, log: log}
	for _, o := range opts {
		o(s)
	}
	s.routes = map[string]route{
		agentapi.PathHealth:         {method: http.MethodGet, handler: s.health},
		agentapi.PathBan:            {method: http.MethodPost, handler: s.ban},
		agentapi.PathUnban:          {method: http.MethodPost, handler: s.unban},
		agentapi.PathBans:           {method: http.MethodGet, handler: s.bans},
		agentapi.PathInstances:      {method: http.MethodGet, handler: s.instances},
		agentapi.PathInstanceEnsure: {method: http.MethodPost, handler: s.ensure},
		agentapi.PathInstancePower:  {method: http.MethodPost, handler: s.power},
		agentapi.PathInstanceRemove: {method: http.MethodPost, handler: s.remove},
	}
	s.srv = &http.Server{
		Handler:           s,
		ConnContext:       peercred.ConnContext,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return s
}

// Serve accepts connections on l until Shutdown is called; it then returns nil.
func (s *Server) Serve(l net.Listener) error {
	if err := s.srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown stops accepting, closes the listener (removing the socket file)
// and waits for running requests.
func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := s.routes[r.URL.Path]
	if !ok {
		s.reject(w, r, http.StatusNotFound, errors.New("not found"))
		return
	}
	if r.Method != rt.method {
		w.Header().Set("Allow", rt.method)
		s.reject(w, r, http.StatusMethodNotAllowed, fmt.Errorf("method %s not allowed", r.Method))
		return
	}
	if r.Method == http.MethodPost {
		mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "application/json" {
			s.reject(w, r, http.StatusUnsupportedMediaType, errors.New("content type must be application/json"))
			return
		}
	}
	rt.handler(w, r)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, agentapi.HealthResponse{
		Status: "ok", Version: s.version, Firewall: s.fw.Name(), Docker: s.inst.DockerStatus(),
	})
}

func (s *Server) ban(w http.ResponseWriter, r *http.Request) {
	var req agentapi.BanRequest
	if !s.decode(w, r, &req, maxRequestBytes) {
		return
	}
	p, err := banrules.ParsePrefix(req.Prefix)
	if err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	d, err := banrules.TimeoutFromSeconds(req.TimeoutSeconds)
	if err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.fw.Ban(r.Context(), p, d); err != nil {
		s.firewallError(w, r, "ban", p.String(), err)
		return
	}
	s.log.Info("firewall ban", "prefix", p.String(), "timeout_s", req.TimeoutSeconds, "backend", s.fw.Name(), "peer_uid", peerUID(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unban(w http.ResponseWriter, r *http.Request) {
	var req agentapi.UnbanRequest
	if !s.decode(w, r, &req, maxRequestBytes) {
		return
	}
	p, err := banrules.ParsePrefix(req.Prefix)
	if err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.fw.Unban(r.Context(), p); err != nil {
		s.firewallError(w, r, "unban", p.String(), err)
		return
	}
	s.log.Info("firewall unban", "prefix", p.String(), "backend", s.fw.Name(), "peer_uid", peerUID(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) bans(w http.ResponseWriter, r *http.Request) {
	list, err := s.fw.List(r.Context())
	if err != nil {
		s.firewallError(w, r, "list", "", err)
		return
	}
	if list == nil {
		list = []agentapi.Ban{}
	}
	s.log.Debug("firewall list", "count", len(list), "peer_uid", peerUID(r))
	writeJSON(w, http.StatusOK, agentapi.BansResponse{Bans: list})
}

func (s *Server) instances(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.inst.Status(r.Context()))
}

func (s *Server) ensure(w http.ResponseWriter, r *http.Request) {
	var req agentapi.EnsureRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if err := req.Validate(); err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	s.queued(w, r, "ensure", req.ID, s.inst.Ensure(r.Context(), req))
}

func (s *Server) power(w http.ResponseWriter, r *http.Request) {
	var req agentapi.PowerRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if req.ID <= 0 || !agentapi.ValidPowerAction(req.Action) {
		s.reject(w, r, http.StatusBadRequest, errors.New("id must be > 0 and action start, stop or restart"))
		return
	}
	s.queued(w, r, "power "+string(req.Action), req.ID, s.inst.Power(r.Context(), req.ID, req.Action))
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	var req agentapi.RemoveRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if req.ID <= 0 {
		s.reject(w, r, http.StatusBadRequest, errors.New("id must be > 0"))
		return
	}
	s.queued(w, r, "remove", req.ID, s.inst.Remove(r.Context(), req.ID))
}

// queued answers a write to the instance queue: 202, or the mapped error.
func (s *Server) queued(w http.ResponseWriter, r *http.Request, op string, id int64, err error) {
	switch {
	case err == nil:
		s.log.Info("instance operation accepted", "op", op, "id", id, "peer_uid", peerUID(r))
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, instancespec.ErrInvalid), errors.Is(err, banrules.ErrInvalid):
		s.reject(w, r, http.StatusBadRequest, err)
	case errors.Is(err, agentapi.ErrBusy):
		s.rejectCode(w, r, http.StatusTooManyRequests, agentapi.ErrorCodeBusy, err)
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		s.rejectCode(w, r, http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable, err)
	default:
		s.log.Error("instance operation failed", "op", op, "id", id, "err", err, "peer_uid", peerUID(r))
		writeJSON(w, http.StatusInternalServerError, agentapi.ErrorResponse{Error: "instance operation failed"})
	}
}

// decode strictly decodes exactly one JSON object of at most limit bytes.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		s.decodeError(w, r, err, limit)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data after JSON body")
		}
		s.decodeError(w, r, err, limit)
		return false
	}
	return true
}

func (s *Server) decodeError(w http.ResponseWriter, r *http.Request, err error, limit int64) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		s.reject(w, r, http.StatusRequestEntityTooLarge, fmt.Errorf("request body exceeds %d bytes", limit))
		return
	}
	s.reject(w, r, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
}

func (s *Server) firewallError(w http.ResponseWriter, r *http.Request, op, prefix string, err error) {
	switch {
	case errors.Is(err, banrules.ErrConflict):
		s.reject(w, r, http.StatusConflict, err)
	case errors.Is(err, banrules.ErrInvalid):
		s.reject(w, r, http.StatusBadRequest, err)
	default:
		s.log.Error("firewall operation failed", "op", op, "prefix", prefix, "backend", s.fw.Name(), "err", err, "peer_uid", peerUID(r))
		writeJSON(w, http.StatusInternalServerError, agentapi.ErrorResponse{Error: "firewall operation failed"})
	}
}

func (s *Server) reject(w http.ResponseWriter, r *http.Request, status int, err error) {
	s.rejectCode(w, r, status, "", err)
}

func (s *Server) rejectCode(w http.ResponseWriter, r *http.Request, status int, code string, err error) {
	s.log.Warn("agent request rejected", "method", r.Method, "path", r.URL.Path, "status", status, "code", code,
		"err", err.Error(), "peer_uid", peerUID(r))
	writeJSON(w, status, agentapi.ErrorResponse{Error: err.Error(), Code: code})
}

// noInstances is the backend of a server without WithInstances.
type noInstances struct{}

func (noInstances) DockerStatus() string { return agentapi.DockerUnavailable }

func (noInstances) Status(context.Context) agentapi.InstancesResponse {
	return agentapi.InstancesResponse{
		Docker: agentapi.DockerUnavailable, Instances: []agentapi.InstanceStatus{},
		Router: agentapi.RouterStatus{State: agentapi.StateMissing},
	}
}

func (noInstances) Ensure(context.Context, agentapi.EnsureRequest) error {
	return agentapi.ErrDockerUnavailable
}

func (noInstances) Power(context.Context, int64, agentapi.PowerAction) error {
	return agentapi.ErrDockerUnavailable
}

func (noInstances) Remove(context.Context, int64) error { return agentapi.ErrDockerUnavailable }

// peerUID returns the UID stored by peercred.ConnContext, or -1 if unknown.
func peerUID(r *http.Request) int64 {
	if uid, ok := peercred.FromContext(r.Context()); ok {
		return int64(uid)
	}
	return -1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
```

Gegenüber TP1 geändert: `Instances`/`Option`/`WithInstances`/`noInstances`, variadisches `NewServer`, vier neue Routen, `health` mit `s.inst.DockerStatus()`, `decode`/`decodeError` mit Limit-Parameter (Firewall-Routen übergeben weiter `maxRequestBytes` = 4 KiB), `queued` (Fehlerabbildung) und `rejectCode` (setzt `ErrorResponse.Code`; `reject` ruft es mit leerem Code). Die Validierung im Server (`EnsureRequest.Validate`, Power-Aktion, `id > 0`) läuft vor dem Manager, damit ungültige Anfragen nie die Warteschlange erreichen; der Manager prüft trotzdem erneut.

- [ ] **Step 4: `cmd/agent/main.go` verdrahten** – Datei vollständig ersetzen durch:

```go
// Command agent is the privileged mc-panel helper. It serves a small, strictly
// validated JSON API on a Unix socket that only the panel user (and root) may
// connect to, manages the nftables jail sets and is the only Docker client
// (Minecraft containers and mc-router).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/firewall"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/peercred"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

// version is set at build time: go build -ldflags "-X main.version=1.2.3".
var version = "dev"

const (
	defaultConfigPath = "/etc/mc-panel/agent.toml"
	shutdownTimeout   = 5 * time.Second
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to agent.toml (default "+defaultConfigPath+" unless -dev)")
	dev := fs.Bool("dev", false, "development mode: socket ./.dev/agent.sock (0600), noop firewall, local Docker (OrbStack/Docker Desktop), data in ./.dev/mc, text logs")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if *showVersion {
		_, err := fmt.Fprintln(stdout, version)
		return err
	}

	path := *configPath
	if path == "" && !*dev {
		path = defaultConfigPath
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := config.LoadAgent(path, *dev, root)
	if err != nil {
		return err
	}
	log := newLogger(cfg.Dev, stderr)

	panelUID, err := uidFromConfig(cfg.PanelUID)
	if err != nil {
		return err
	}
	mode, gid, err := socketPerms(cfg, user.LookupId)
	if err != nil {
		return err
	}
	mcUID, mcGID, err := mcUserIDs(cfg, user.Lookup)
	if err != nil {
		return err
	}
	if cfg.Dev {
		if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0o750); err != nil {
			return err
		}
	}
	// Listen first: it detects an already running agent, so a refused second
	// start never touches the firewall.
	l, err := agent.Listen(cfg.Socket, mode, gid)
	if err != nil {
		return err
	}
	fw, err := firewall.New(cfg, clock.Real{}, log)
	if err != nil {
		_ = l.Close() // removes the socket file again
		return fmt.Errorf("firewall: %w", err)
	}

	mgr := instances.New(instances.Config{
		Docker:       docker.New(cfg.DockerSocket),
		InstancesDir: cfg.InstancesDir,
		UID:          mcUID,
		GID:          mcGID,
		Images:       cfg.Images,
		Clock:        clock.Real{},
		Log:          log,
	})
	runCtx, stopRun := context.WithCancel(ctx)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		mgr.Run(runCtx)
	}()
	// stopInstances ends the background loop and the queued operations.
	stopInstances := func() {
		stopRun()
		<-runDone
		mgr.Close()
	}

	srv := agent.NewServer(fw, version, log, agent.WithInstances(mgr))
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(panelUID, 0), log)) }()
	log.Info("agent started", "version", version, "socket", cfg.Socket, "firewall", fw.Name(),
		"panel_uid", panelUID, "socket_mode", fmt.Sprintf("%#o", mode), "dev", cfg.Dev,
		"docker_socket", cfg.DockerSocket, "instances_dir", cfg.InstancesDir, "mc_uid", mcUID, "mc_gid", mcGID)

	select {
	case err := <-errc:
		stopInstances()
		if err == nil {
			err = errors.New("server stopped unexpectedly")
		}
		return err
	case <-ctx.Done():
	}
	log.Info("agent shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	stopInstances()
	if err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return <-errc
}

// socketPerms returns the socket mode and group: dev 0600 without chown; prod
// 0660 with the primary group of the panel user, so only that group (and
// root) can even open the socket.
func socketPerms(cfg config.Agent, lookup func(uid string) (*user.User, error)) (os.FileMode, int, error) {
	if cfg.Dev {
		return 0o600, -1, nil
	}
	u, err := lookup(strconv.Itoa(cfg.PanelUID))
	if err != nil {
		return 0, 0, fmt.Errorf("look up panel_uid %d: %w", cfg.PanelUID, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("panel_uid %d has non-numeric gid %q: %w", cfg.PanelUID, u.Gid, err)
	}
	return 0o660, gid, nil
}

// mcUserIDs resolves mc_user to the uid:gid the Minecraft containers run as. In dev
// mode an empty mc_user means the agent's own ids. Outside dev mode root (uid or
// gid 0) is refused: the containers must never run as root on the host.
func mcUserIDs(cfg config.Agent, lookup func(name string) (*user.User, error)) (int, int, error) {
	if cfg.MCUser == "" {
		if !cfg.Dev {
			return 0, 0, errors.New("mc_user must be set")
		}
		return os.Getuid(), os.Getgid(), nil
	}
	u, err := lookup(cfg.MCUser)
	if err != nil {
		return 0, 0, fmt.Errorf("look up mc_user %q (create it with: useradd --system --no-create-home --shell /usr/sbin/nologin %s): %w",
			cfg.MCUser, cfg.MCUser, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil || uid < 0 {
		return 0, 0, fmt.Errorf("mc_user %q has invalid uid %q", cfg.MCUser, u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil || gid < 0 {
		return 0, 0, fmt.Errorf("mc_user %q has invalid gid %q", cfg.MCUser, u.Gid)
	}
	if !cfg.Dev && (uid == 0 || gid == 0) {
		return 0, 0, fmt.Errorf("mc_user %q resolves to uid %d gid %d: Minecraft containers must not run as root", cfg.MCUser, uid, gid)
	}
	return uid, gid, nil
}

func uidFromConfig(uid int) (uint32, error) {
	if uid < 0 || int64(uid) > math.MaxUint32 {
		return 0, fmt.Errorf("panel_uid %d out of range", uid)
	}
	return uint32(uid), nil //nolint:gosec // G115: range checked above
}

func newLogger(dev bool, w io.Writer) *slog.Logger {
	if dev {
		return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewJSONHandler(w, nil))
}
```

Reihenfolge beim Start: Listen → Firewall → Manager (`Run` startet sofort einen `Tick`: Ping, Routen-Datei, Router) → Server. Beim Herunterfahren: Server `Shutdown` (keine neuen Anfragen), dann Hintergrundschleife beenden und `mgr.Close()` (laufende Docker-Aufrufe werden abgebrochen; Docker führt z. B. ein begonnenes Stop selbst zu Ende, der Abgleich im panel holt den Rest nach). Auch wenn `Serve` unerwartet endet, wird der Manager geschlossen.

Hinweis: `make dev` startet den agent mit `-dev`; auf einem Mac mit OrbStack/Docker Desktop legt er damit beim Start das Netz `mcpanel-mc` und den Container `mcpanel-router` an (Port 25565) und schreibt nach `./.dev/mc`. Im CI-Job `e2e` (Ubuntu-Runner mit Docker) passiert dasselbe; das ist harmlos und gewollt (Spec §10).

- [ ] **Step 5: Tests laufen lassen (grün, auch mit `-race`)**

Run: `go test ./... -count=1 && CGO_ENABLED=1 go test -race -count=1 ./cmd/agent ./internal/agent/...`
Expected: PASS (alle Pakete; `TestRunDevManagesInstances` legt Router und `mcpanel-mc-1` im Fake an).

- [ ] **Step 6: Lint**

Run: `make lint && GOOS=linux golangci-lint run ./...`
Expected: keine Befunde.

- [ ] **Step 7: Commit**

```bash
git add internal/agent/server.go internal/agent/server_instances_test.go cmd/agent/main.go cmd/agent/main_test.go
git commit -m "feat(agent): serve /v1/instances endpoints backed by the instance manager

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Integrationstest gegen echten Docker-Daemon (`dockerintegration`)

**Files:**
- Create: `internal/agent/docker/integration_linux_test.go` (`//go:build linux && dockerintegration`)
- Modify: `.golangci.yml` (Build-Tag `dockerintegration`, damit der Linter die Datei sieht)
- Modify: `.github/workflows/ci.yml` (neuer Schritt im bestehenden Root-Job `nftables`)

**Interfaces:**
- Consumes: `docker.Client`, `docker.ParseImageRef`, `docker.APIVersion` (Aufgabe 5), `instances.New`/`Config.Customize`/`Wait`/`Status`/`SampleStats`, `instances.ContainerName`/`DataDir`/`DeletedDir`/`RouterName`/`NetworkName`/`LabelInstance`/`LabelRevision` (Aufgaben 6–8), `clock.Real`
- Produces: nichts (nur Tests und CI)

Testimage statt itzg (spec §11): `busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e` – Multi-Arch-Index-Digest, am 2026-10-10 gegen Docker Hub verifiziert (`curl -s https://hub.docker.com/v2/repositories/library/busybox/tags/1.37.0` → Feld `digest`; Tag `1.37` hat denselben Digest). Der Container führt `sh -c "trap 'exit 0' TERM; while true; do sleep 1; done"` aus: er läuft dauerhaft, beendet sich auf SIGTERM sofort (Stop dauert keine 60 s) und ignoriert als PID 1 ohne Handler ein SIGHUP – genau wie es der Router beim Neuladen nicht stören darf.

Zwei Tests:
- `TestIntegrationClientLifecycle` – eigene Namen (`mcpanel-it-<pid>`, Label `mcpanel.integration`, eigenes Netz), läuft überall: Pull, Netz idempotent, Create (Konflikt beim zweiten Mal), Start (zweimal), Inspect, Label-Filter, Kill SIGHUP (läuft weiter), Stats (Limit 64 MiB), Restart, Stop (zweimal), Remove, 404 danach.
- `TestIntegrationManagerCycle` – der echte Manager mit den echten Namen (`mcpanel-mc-1`, `mcpanel-router`, Netz `mcpanel-mc`), Images per `[images]`-Override auf busybox, `Customize` setzt das Testkommando und entfernt beim Router die Portfreigabe (Port 25565 bleibt frei): ensure → Besitzer `65534:65534` und 0750 des Datenverzeichnisses, Route → start → Stats (Limit 1280 MiB) → restart → ensure mit `recreate` (neuer Container läuft wieder, Revision 2) → stop → remove (Archiv unter `deleted/it-…`, Route weg, Router läuft nach SIGHUP weiter). Existieren schon mc-panel-Ressourcen (z. B. in der VM mit laufendem agent), wird der Test übersprungen; mit `MCPANEL_IT_STRICT=1` (CI) schlägt er stattdessen fehl.

- [ ] **Step 1: Integrationstest schreiben** – `internal/agent/docker/integration_linux_test.go`

```go
//go:build linux && dockerintegration

// Integration tests against the real Docker daemon. Build as the normal user, run as root
// (Docker socket, chown of the data directory):
//
//	go test -tags dockerintegration -exec sudo -count=1 -v -run Integration ./internal/agent/docker/
//
// TestIntegrationClientLifecycle uses its own names and runs anywhere. The manager test
// uses the real names (mcpanel-mc-<id>, mcpanel-router, network mcpanel-mc); on a host
// where they exist (e.g. the VM with a running agent) it is skipped, with
// MCPANEL_IT_STRICT=1 (CI) it fails instead:
//
//	go test -tags dockerintegration -exec "sudo env MCPANEL_IT_STRICT=1" -count=1 ./internal/agent/...
package docker_test

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

// testImage is a small multi-arch image (verified on Docker Hub 2026-10-10: tag 1.37.0 = 1.37).
const testImage = "busybox:1.37.0@sha256:bdf57e528e45e4433820e045b29b4597825a1c9e38353532d90a01445013f82e"

// testCmd keeps the container running and exits on SIGTERM. As PID 1 without a
// handler, SIGHUP is ignored, like mc-router reloading its routes.
var testCmd = []string{"sh", "-c", "trap 'exit 0' TERM; while true; do sleep 1; done"}

const nobody = 65534

func dockerSocket() string {
	if s := os.Getenv("MCPANEL_DOCKER_SOCKET"); s != "" {
		return s
	}
	return "/var/run/docker.sock"
}

func requireDocker(t *testing.T) *docker.Client {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("dockerintegration tests need root (Docker socket, chown)")
	}
	c := docker.New(dockerSocket())
	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Docker not reachable at %s: %v", dockerSocket(), err)
	}
	pinned := pinnedRef(t, testImage)
	ok, err := c.ImageExists(context.Background(), pinned)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		if err := c.PullImage(context.Background(), pinned); err != nil {
			t.Fatalf("pull %s: %v", testImage, err)
		}
	}
	return c
}

func pinnedRef(t *testing.T, ref string) string {
	t.Helper()
	r, err := docker.ParseImageRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	return r.Pinned()
}

// rawRequest talks to the daemon directly for calls the client deliberately lacks.
func rawRequest(t *testing.T, method, path string) int {
	t.Helper()
	hc := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", dockerSocket())
		},
	}}
	req, err := http.NewRequestWithContext(context.Background(), method, "http://docker"+docker.APIVersion+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func forceRemove(t *testing.T, c *docker.Client, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := c.Remove(context.Background(), n, true); err != nil && !docker.IsNotFound(err) {
			t.Errorf("cleanup %s: %v", n, err)
		}
	}
}

func TestIntegrationClientLifecycle(t *testing.T) {
	c := requireDocker(t)
	ctx := context.Background()
	name := fmt.Sprintf("mcpanel-it-%d", os.Getpid())
	label := "mcpanel.integration"
	network := name + "-net"
	t.Cleanup(func() {
		forceRemove(t, c, name)
		if code := rawRequest(t, http.MethodDelete, "/networks/"+network); code != http.StatusNoContent && code != http.StatusNotFound {
			t.Errorf("cleanup network: HTTP %d", code)
		}
	})

	for range 2 {
		if err := c.EnsureNetwork(ctx, network, map[string]string{label: "1"}); err != nil {
			t.Fatalf("EnsureNetwork: %v", err)
		}
	}
	cfg := docker.ContainerConfig{
		Image: pinnedRef(t, testImage), Cmd: testCmd, User: fmt.Sprintf("%d:%d", nobody, nobody),
		Labels: map[string]string{label: name}, Hostname: "it", StopTimeout: 5,
		HostConfig: docker.HostConfig{
			NetworkMode: network, RestartPolicy: docker.RestartPolicy{Name: "no"},
			CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"},
			Memory: 64 << 20, MemorySwap: 64 << 20, PidsLimit: 64,
			LogConfig: docker.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "1m", "max-file": "1"}},
		},
		NetworkingConfig: docker.NetworkingConfig{EndpointsConfig: map[string]docker.EndpointSettings{network: {}}},
	}
	id, err := c.CreateContainer(ctx, name, cfg)
	if err != nil {
		t.Fatalf("CreateContainer: %v", err)
	}
	if _, err := c.CreateContainer(ctx, name, cfg); !docker.IsConflict(err) {
		t.Fatalf("duplicate create = %v, want conflict", err)
	}
	info, err := c.Inspect(ctx, name)
	if err != nil || info.ID != id || info.State.Status != "created" || info.Config.Labels[label] != name {
		t.Fatalf("Inspect after create = %+v, %v", info, err)
	}

	if err := c.Start(ctx, name); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Start(ctx, name); err != nil {
		t.Fatalf("second Start (304) must succeed: %v", err)
	}
	info, err = c.Inspect(ctx, name)
	if err != nil || !info.State.Running {
		t.Fatalf("Inspect after start = %+v, %v", info, err)
	}
	if started, err := time.Parse(time.RFC3339Nano, info.State.StartedAt); err != nil || time.Since(started) > time.Minute {
		t.Fatalf("StartedAt = %q, %v", info.State.StartedAt, err)
	}

	list, err := c.List(ctx, label)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, s := range list {
		if _, ok := s.Labels[label]; !ok {
			t.Errorf("label filter returned %v", s.Names)
		}
		if s.HasName(name) {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("List(%s) = %+v", label, list)
	}

	if err := c.Kill(ctx, name, "SIGHUP"); err != nil {
		t.Fatalf("Kill SIGHUP: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if info, err := c.Inspect(ctx, name); err != nil || !info.State.Running {
		t.Fatalf("SIGHUP must not stop the container: %+v, %v", info.State, err)
	}

	stats, err := c.Stats(ctx, name)
	if err != nil || stats.MemoryStats.Limit != 64<<20 || stats.MemoryStats.Usage == 0 || stats.CPUStats.SystemCPUUsage == 0 {
		t.Fatalf("Stats = %+v, %v", stats, err)
	}

	if err := c.Restart(ctx, name, 5); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	for range 2 {
		if err := c.Stop(ctx, name, 5); err != nil {
			t.Fatalf("Stop: %v", err)
		}
	}
	if info, err := c.Inspect(ctx, name); err != nil || info.State.Running || info.State.Status != "exited" {
		t.Fatalf("after stop: %+v, %v", info.State, err)
	}
	if err := c.Remove(ctx, name, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := c.Inspect(ctx, name); !docker.IsNotFound(err) {
		t.Fatalf("Inspect after remove = %v", err)
	}
}

// requireCleanHost skips (or, with MCPANEL_IT_STRICT=1, fails) when mc-panel resources exist.
func requireCleanHost(t *testing.T, c *docker.Client) {
	t.Helper()
	ctx := context.Background()
	var dirty []string
	if list, err := c.List(ctx, instances.LabelInstance); err != nil || len(list) > 0 {
		dirty = append(dirty, fmt.Sprintf("containers with %s (%d, %v)", instances.LabelInstance, len(list), err))
	}
	if _, err := c.Inspect(ctx, instances.RouterName); !docker.IsNotFound(err) {
		dirty = append(dirty, "container "+instances.RouterName)
	}
	if code := rawRequest(t, http.MethodGet, "/networks/"+instances.NetworkName); code != http.StatusNotFound {
		dirty = append(dirty, "network "+instances.NetworkName)
	}
	if len(dirty) == 0 {
		return
	}
	msg := fmt.Sprintf("host is not clean (%s); stop the agent and remove them, or run in CI", strings.Join(dirty, ", "))
	if os.Getenv("MCPANEL_IT_STRICT") == "1" {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

func TestIntegrationManagerCycle(t *testing.T) {
	c := requireDocker(t)
	ctx := context.Background()
	requireCleanHost(t, c)
	t.Cleanup(func() {
		forceRemove(t, c, instances.ContainerName(1), instances.RouterName)
		_ = rawRequest(t, http.MethodDelete, "/networks/"+instances.NetworkName)
	})

	dir := t.TempDir()
	images := config.Images{Java8: testImage, Java17: testImage, Java21: testImage, Java25: testImage, Router: testImage}
	m := instances.New(instances.Config{
		Docker: c, InstancesDir: dir, UID: nobody, GID: nobody, Images: images, Clock: clock.Real{},
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Customize: func(name string, cfg *docker.ContainerConfig) {
			cfg.Cmd = testCmd
			if name == instances.RouterName { // keep the host port free on developer machines
				cfg.HostConfig.PortBindings = nil
				cfg.ExposedPorts = nil
			}
		},
	})
	t.Cleanup(m.Close)

	status := func() agentapi.InstanceStatus {
		t.Helper()
		for _, s := range m.Status(ctx).Instances {
			if s.ID == 1 {
				return s
			}
		}
		return agentapi.InstanceStatus{State: agentapi.StateMissing}
	}
	run := func(op func() error) {
		t.Helper()
		if err := op(); err != nil {
			t.Fatal(err)
		}
		m.Wait()
		if s := status(); s.LastError != "" {
			t.Fatalf("last_error = %q", s.LastError)
		}
	}
	req := agentapi.EnsureRequest{
		ID: 1, Name: "it", FQDN: "it.mc.localhost", Revision: 1,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 1024},
	}
	run(func() error { return m.Ensure(ctx, req) })
	if s := status(); s.State != agentapi.StateCreated || s.Revision != 1 {
		t.Fatalf("after ensure: %+v", s)
	}
	fi, err := os.Stat(instances.DataDir(dir, 1))
	if err != nil {
		t.Fatal(err)
	}
	if st := fi.Sys().(*syscall.Stat_t); st.Uid != nobody || st.Gid != nobody || fi.Mode().Perm() != 0o750 {
		t.Fatalf("data dir owner %d:%d mode %v; want %d:%d 0750", st.Uid, st.Gid, fi.Mode().Perm(), nobody, nobody)
	}
	routes, err := os.ReadFile(filepath.Join(dir, "router", "routes.json"))
	if err != nil || !strings.Contains(string(routes), `"it.mc.localhost": "mcpanel-mc-1:25565"`) {
		t.Fatalf("routes = %s, %v", routes, err)
	}

	run(func() error { return m.Power(ctx, 1, agentapi.PowerStart) })
	if s := status(); s.State != agentapi.StateRunning || s.StartedAt == 0 {
		t.Fatalf("after start: %+v", s)
	}
	m.SampleStats(ctx)
	m.SampleStats(ctx)
	if s := status(); s.MemoryBytes <= 0 || s.MemoryLimitBytes != 1280<<20 {
		t.Fatalf("stats: %+v (limit must be 1024 MiB × 1.25)", s)
	}
	run(func() error { return m.Power(ctx, 1, agentapi.PowerRestart) })
	before, err := c.Inspect(ctx, instances.ContainerName(1))
	if err != nil {
		t.Fatal(err)
	}

	req.Revision, req.Recreate = 2, true
	run(func() error { return m.Ensure(ctx, req) })
	after, err := c.Inspect(ctx, instances.ContainerName(1))
	if err != nil || after.ID == before.ID || !after.State.Running || after.Config.Labels[instances.LabelRevision] != "2" {
		t.Fatalf("recreate: %+v, %v", after, err)
	}
	if r, err := c.Inspect(ctx, instances.RouterName); err != nil || !r.State.Running {
		t.Fatalf("router: %+v, %v", r.State, err)
	}

	run(func() error { return m.Power(ctx, 1, agentapi.PowerStop) })
	if s := status(); s.State != agentapi.StateExited {
		t.Fatalf("after stop: %+v", s)
	}
	run(func() error { return m.Remove(ctx, 1) })
	if _, err := c.Inspect(ctx, instances.ContainerName(1)); !docker.IsNotFound(err) {
		t.Fatalf("container after remove: %v", err)
	}
	entries, err := os.ReadDir(instances.DeletedDir(dir))
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "it-") {
		t.Fatalf("deleted/ = %v, %v", entries, err)
	}
	routes, _ = os.ReadFile(filepath.Join(dir, "router", "routes.json"))
	if strings.Contains(string(routes), "it.mc.localhost") {
		t.Fatalf("route not removed: %s", routes)
	}
	if r, err := c.Inspect(ctx, instances.RouterName); err != nil || !r.State.Running {
		t.Fatalf("router must survive the SIGHUP: %+v, %v", r.State, err)
	}
	if slices.ContainsFunc(m.Status(ctx).Instances, func(s agentapi.InstanceStatus) bool { return s.ID == 1 }) {
		t.Fatal("removed instance still listed")
	}
}
```

- [ ] **Step 2: Kompilieren und Lint ohne Docker (macOS)**

`.golangci.yml`, Abschnitt `run`: den Kommentar und die Build-Tags ersetzen durch

```yaml
  # Lint the Linux-only integration tests too (they only compile with these tags).
  build-tags:
    - nftintegration
    - dockerintegration
```

Run:
```bash
GOOS=linux go vet -tags dockerintegration ./internal/agent/docker/
GOOS=linux go test -tags dockerintegration -c -o /dev/null ./internal/agent/docker/
GOOS=linux golangci-lint run ./internal/agent/... && golangci-lint run ./...
```
Expected: keine Ausgabe von `go vet`, Kompilieren ohne Fehler, `0 issues.` (zweimal). Ohne das Tag (`go test ./...`) wird die Datei nicht gebaut.

- [ ] **Step 3: Integrationstest ausführen (Linux mit Docker, als root)**

Run (Linux-Host oder CI; in der Lima-VM erst nach Aufgabe 23, die Docker installiert – dort Testbinary auf dem Mac bauen):
```bash
go test -tags dockerintegration -exec "sudo env MCPANEL_IT_STRICT=1" -count=1 -v -run Integration ./internal/agent/docker/
# Lima-VM (Go ist dort nicht installiert):
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c -tags dockerintegration -o .dev/bin/agent-docker.test ./internal/agent/docker
limactl copy .dev/bin/agent-docker.test mcpanel:/tmp/agent-docker.test
limactl shell mcpanel sudo /tmp/agent-docker.test -test.v -test.count=1 -test.run Integration
```
Expected: `--- PASS: TestIntegrationClientLifecycle`, `--- PASS: TestIntegrationManagerCycle` (in der VM mit laufendem agent: `--- SKIP: TestIntegrationManagerCycle (… host is not clean …)`), `PASS`.

Ist kein Linux mit Docker verfügbar, diesen Schritt dem CI-Lauf (Step 4) überlassen und das im PR vermerken.

- [ ] **Step 4: CI-Schritt ergänzen** – `.github/workflows/ci.yml`, Job `nftables` („nftables integration (root)“), direkt nach dem Schritt `Integration tests as root` anfügen:

```yaml
      # GitHub's Ubuntu runners ship a running Docker daemon. The tests pull a small,
      # digest-pinned busybox image instead of the itzg images. MCPANEL_IT_STRICT=1 turns
      # the "host not clean" skip of the manager test into a failure.
      - name: Docker integration tests as root
        run: |
          sudo docker version
          go test -tags dockerintegration -exec "sudo env MCPANEL_IT_STRICT=1" -count=1 -v ./internal/agent/...
```

Job-ID und -Name bleiben unverändert (eventuelle Branch-Protection-Regeln referenzieren den Namen). `go test -exec` zerlegt den Befehl in Felder; `sudo env VAR=1 <testbinary>` reicht die Variable trotz `env_reset` durch (lokal geprüft: `go test -exec "env MCPANEL_IT_STRICT=1" ./internal/clock` → `ok`). Die Testbinaries werden als Runner-Benutzer gebaut und nur als root ausgeführt, wie beim nftables-Schritt.

- [ ] **Step 5: Gesamtprüfung**

Run: `make test && make lint && GOOS=linux golangci-lint run ./...`
Expected: alles grün.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/docker/integration_linux_test.go .golangci.yml .github/workflows/ci.yml
git commit -m "test(agent): docker integration tests against a real daemon in CI

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-Review

1. **Gepinnte Form statt `name:tag@digest`.** Docker ignoriert den Tag neben einem Digest; der containerd-Image-Store (Standard bei Neuinstallationen von Docker 29) löst `name:tag@digest` beim Image-Inspect nicht zuverlässig auf. Der agent verwendet deshalb überall `repo@sha256:…`; der Tag in `agent.toml` dient nur der Lesbarkeit. Golden-Tests erwarten diese Form.
2. **Neuer Container wird nicht gestartet.** `ensure` legt an, startet aber nur nach `recreate` eines vorher laufenden Containers. Das entspricht Spec §5.1/§5.3 („ensure …, danach ggf. start“) und verhindert, dass ein `stopped` gewünschter Server kurz hochfährt.
3. **Gestoppte Container behalten ihre Route.** Die Routen-Datei hängt nur von existierenden verwalteten Containern ab, nicht von deren Zustand: kein `SIGHUP` bei jedem Start/Stop, Verbindungsversuche zu gestoppten Servern scheitern ohnehin.
4. **Router-Selbstheilung pro Tick.** `Tick` ruft `RefreshRouter` jedes Mal (2–3 Docker-Aufrufe alle 10 s): ein gelöschter oder gestoppter Router ist nach spätestens 10 s zurück, eine verpasste Routen-Änderung wird nachgeholt. Fehler werden nur bei Änderung geloggt.
5. **Router-Image-Wechsel.** Weicht das Image eines vorhandenen `mcpanel-router` vom konfigurierten ab (agent-Update mit neuem Digest), ersetzt der agent den Router-Container. Instanz-Container werden bei neuem itzg-Digest **nicht** automatisch ersetzt – das geschieht beim nächsten `recreate` (Revision), damit kein Server ungefragt neu startet.
6. **Docker „unavailable“ ist ein Zustand, kein Fehler.** `GET /v1/instances` antwortet immer `200`; das panel leitet `unknown` aus `docker: "unavailable"` ab (V4). Schreibende Aufrufe antworten `503 docker_unavailable` erst nach einem frischen Ping (5 s Timeout).
7. **Zustand im Speicher ist begrenzt.** Max. 4 offene Operationen pro Instanz und 256 Instanzen mit Zustand; Einträge verschwinden, sobald die Schlange leer und `last_error` leer ist bzw. nach erfolgreichem `remove`. Ein agent-Neustart verliert `last_error` und Warteschlangen (akzeptiert: der Abgleich im panel sendet erneut).
8. **`mc_user` fehlt → agent startet nicht.** Lieber laut scheitern als Container als falscher Benutzer anlegen. UID/GID 0 sind außerhalb von Dev verboten. Konsequenz: Die Lima-VM braucht ab Aufgabe 4 den Benutzer `mcpanel-mc` (Aufgabe 23 legt ihn in `vm-setup.sh` an; bis dahin einmalig manuell, siehe Aufgabe 4 Step 4).
9. **Kein Docker-SDK.** ~350 Zeilen eigener Client mit festem API-Pfad, Größen- und Zeitlimits; nur die 13 benötigten Aufrufe existieren. Der Fake-Daemon testet den echten Client über einen echten Unix-Socket; der Integrationstest prüft die Annahmen gegen echtes Docker.

**Versionsannahmen (2026-10-10):** Docker Engine API v1.44 (älteste von Docker 29 akzeptierte; Ubuntu 26.04: Docker 29.1.3 / API 1.52; GitHub-Runner `ubuntu-24.04`: Docker ≥ 26). Verwendete Endpunkte und Felder siehe Aufgabe 5; One-Shot-Stats (`one-shot=true`) seit API 1.41. itzg/minecraft-server `2026.9.2` überspringt mit Docker-`User` den root-Pfad (kein gosu/chown) und schreibt nach `/tmp` (daher `ReadonlyRootfs: false`). itzg/mc-router `1.47.2`: `-routes-config` ohne `-routes-config-watch`, Neuladen per SIGHUP, Image läuft als UID 65532. busybox `1.37.0` Index-Digest wie in Aufgabe 10.

## Wiring-Vertrag (für die panel-Aufgaben 11 ff.)

- Das panel spricht ausschließlich über `*agent.Client` (implementiert `agentapi.Client` und `agentapi.InstancesClient`, Aufgabe 3) mit dem agent; `internal/agent/docker` und `internal/agent/instances` landen nie im panel-Binary.
- `Instances(ctx)` liefert immer eine Antwort, solange der agent erreichbar ist: `Docker == agentapi.DockerUnavailable` ⇒ alle Instanzen `unknown`; `Instances` enthält nur Container mit passendem Label **und** Namen sowie Instanzen mit Warteschlangen-Zustand (`StateMissing`), sortiert nach ID. Container-IDs, die die DB nicht kennt, sind die „unbekannten Container“ der Einstellungen (`InstanceStatus.Name` aus dem Label).
- `InstanceStatus.Revision` ist die Label-Revision des Containers (0 bei `missing`); `StartedAt` nur bei `running`/`restarting` ≠ 0; `MemoryBytes`/`MemoryLimitBytes`/`CPUPercent` nur bei `running` (Cache, bis 10 s alt; erster Wert CPU 0).
- `Operation` ist während einer laufenden Operation gesetzt; ein nur wartender Auftrag zeigt noch `""`. `LastError` bleibt bis zur nächsten erfolgreichen Operation derselben Instanz stehen – der Abgleich darf daraus keine Endlosschleife machen (Übersicht Review Focus #4).
- Fehler beim Schreiben (`Ensure`/`Power`/`Remove`): `errors.Is(err, agentapi.ErrDockerUnavailable)` → später erneut; `agentapi.ErrBusy` → diesen Durchlauf überspringen; `instancespec.ErrInvalid`/`agent.ErrInvalid` → dauerhaft (Bug im panel, loggen); `agentapi.ErrUnavailable` → agent weg.
- `Ensure` auf einen existierenden Container mit älterer Revision ohne `Recreate` ist ein No-op; `Recreate: true` startet ihn danach nur wieder, wenn er vorher lief. Nach `Ensure` eines neuen Containers ist dieser `created` – der Abgleich sendet bei `desired_state = running` anschließend `Power(start)`.
- Health: `HealthResponse.Docker` ist der gecachte Zustand (bis 10 s alt); für aktuelle Daten `Instances` verwenden.
