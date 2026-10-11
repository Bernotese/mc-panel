# Teilprojekt 3a – agent (Aufgaben 4–8) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der agent liefert die drei Konsolen-Endpunkte aus Spec §5: Live-Log eines Minecraft-Containers als NDJSON-Stream (`/v1/instances/logs`), Konsolenbefehle über `docker exec rcon-cli` (`/v1/instances/rcon`) und Spielerlisten aus dem Datenverzeichnis (`/v1/instances/players`) – mit Grenzen, die einen hängenden Client, einen hängenden Docker-Daemon und einen kompromittierten Minecraft-Server aushalten.

**Architecture:** `internal/agent/docker` bekommt einen Demultiplexer für Dockers Multiplex-Format und die Aufrufe `ContainerLogs`, `ExecCreate`, `ExecStart`, `ExecInspect` (Streams nur durch den Kontext begrenzt, kein Hijack); der Fake-Daemon `dockertest` lernt Logs (inkl. `follow`) und Exec. `instances.Manager` implementiert die Konsole: `Logs` setzt Zeilen aus Frames zusammen und säubert sie, `Rcon` führt `rcon-cli` als `mcpanel-mc`-Benutzer im Container aus (Timeout 10 s, 64 KiB, 4 parallel pro Instanz), `Players` liest die drei JSON-Dateien über `os.Root` ohne Symlinks, FIFOs oder Riesendateien. `agent.Server` registriert die Endpunkte, hebt für lange Requests die Server-Fristen auf (sonst bricht `net/http` den Kontext nach `ReadTimeout` ab), begrenzt offene Streams auf 16 und beendet sie bei `Shutdown`.

**Tech Stack:** Go (Stand `go.mod`, 1.27: `os.Root`, `http.ResponseController`, `context.AfterFunc`), Docker Engine API v1.44 über den vorhandenen Client, `encoding/binary`, `encoding/json`, `log/slog`; Tests nur Standardbibliothek (`testing/iotest`), Fake-Daemon `internal/agent/docker/dockertest` auf echtem Unix-Socket; Integrationstest mit Build-Tag `dockerintegration` gegen echtes Docker (busybox). Keine neuen Abhängigkeiten.

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md` (Abschnitte 1.2, 2, 3.3, 5, 9)

## Global Constraints

Es gelten **alle** Global Constraints, die Verträge V1/V2 und das Dateieigentum aus `docs/superpowers/plans/2026-10-11-console-00-overview.md` (und damit auch die aus TP1, TP2 und `CLAUDE.md`). Phase A (Aufgaben 1–3) ist auf `main`: `internal/agent/agentapi/console.go` (V1) samt Client (`agent.Client.Logs/Rcon/Players`) existiert, `clearStaleLists` ist aus `internal/agent/instances/datadir.go` entfernt, `instancespec` kennt kein `ops`/`whitelist` mehr.

**Arbeitsweise:** Branch `feat/console-agent`, eigener Worktree: `git worktree add ../mc-panel-wt/feat/console-agent -b feat/console-agent origin/main`. Lokal nur `make check`; Tests laufen in der CI (`gh pr checks <pr> --watch`, Fehler mit `gh run view <id> --log-failed`). Draft-PR beim ersten Push (Aufgabe 4), danach jede Aufgabe als eigener Commit auf denselben Branch.

Agent-spezifische, verbindliche Werte dieses Teilplans:

| Thema | Wert |
|---|---|
| Eigene Pfade | `internal/agent/docker/**` (inkl. `dockertest/`), `internal/agent/instances/**`, `internal/agent/server.go`, `internal/agent/server_console.go` (neu), `internal/agent/server_*_test.go`, `cmd/agent/**`; `.golangci.yml` nur für gezielte, begründete Regeln (voraussichtlich keine) |
| Fremde Dateien | `internal/agent/agentapi/**`, `internal/agent/client*.go`, `internal/agent/instancespec/**` gehören Phase A: nicht ändern; Bedarf → Lead |
| Multiplex-Format | 8-Byte-Header: Byte 0 Stream (`0` stdin, `1` stdout, `2` stderr), Bytes 1–3 = 0, Bytes 4–7 Länge (uint32, Big Endian); Frame ≤ **1 MiB** (`docker.MaxFrameBytes`), sonst `docker.ErrBadFrame`; Ende zwischen Frames = `io.EOF`, mitten im Frame = `io.ErrUnexpectedEOF` |
| `ContainerLogs` | `GET /containers/{id}/logs?follow=0\|1&stdout=1&stderr=1&timestamps=1&tail=<n\|all>[&since=<s>.<9 Ziffern ns>]`; kein Client-Timeout, nur `ctx` |
| Exec | `POST /containers/{id}/exec` mit `{"Cmd":[<rcon>, <befehl>],"User":"<uid>:<gid>","WorkingDir":"/data","Env":["HOME=/data"],"AttachStdin":false,"AttachStdout":true,"AttachStderr":true,"Tty":false}`; `POST /exec/{id}/start` mit `{"Detach":false,"Tty":false}` (Antwort-Body = Multiplex-Stream, kein Hijack, nur `ctx`); `GET /exec/{id}/json` → `Running`, `ExitCode` |
| Log-Zeilen | pro Stream aus Frames zusammengesetzt bis `\n`; Rohzeile (Zeitstempel + Text) ≤ `MaxLogLineBytes + 64`, der Rest einer überlangen Zeile bis zu ihrem `\n` wird verworfen; `\r` am Ende weg; ungültiges UTF-8 → U+FFFD; Text auf `MaxLogLineBytes` (8 KiB) an einer Runen-Grenze gekürzt; Zeile ohne gültigen Zeitstempel erbt den vorigen |
| Logs-Anfrage | `Follow: true` immer; `Since > 0` → `tail=all` (since gewinnt) |
| RCON | Programm `rcon-cli` (`instances.DefaultRconCommand`; nur Tests ändern `Config.RconCommand`); Timeout `instances.RconTimeout = 10 s` (Tests: `Config.RconTimeout`); Ausgabe stdout+stderr in Ankunftsreihenfolge, ≤ `MaxRconOutputBytes` (64 KiB), danach gekürzt mit `\n[… gekürzt]`; abschließende `\r\n` weg; Exit-Code ≠ 0 oder Zeitüberschreitung → `agentapi.ErrRconUnavailable` mit den ersten 500 Zeichen der Ausgabe (leer: `<rcon> exited with code <n>`); höchstens **4** gleichzeitig pro Instanz (`instances.MaxParallelRcon`), sonst `agentapi.ErrBusy` |
| Container-Prüfung | Logs und RCON nur für `mcpanel-mc-<id>` mit Label `mcpanel.instance=<id>` (und, falls bekannt, passendem `mcpanel.name`); sonst `agentapi.ErrNotFound` – fremde Container bekommen nie `logs` oder `exec`. Nicht laufend (oder `restarting`) → `agentapi.ErrNotRunning` |
| Spielerdateien | `ops.json`, `whitelist.json`, `banned-players.json` in `<instances_dir>/instances/<id>`; Verzeichnis per `os.Lstat` geprüft (fehlt → `ErrNotFound`, Symlink/kein Verzeichnis → Fehler), dann `os.OpenRoot`; Datei: `root.Lstat` muss reguläre Datei sein (kein Symlink, auch nicht innerhalb), Öffnen mit `O_RDONLY\|O_NOFOLLOW\|O_NONBLOCK`, `fstat` regulär und `os.SameFile` mit dem Lstat-Ergebnis, ≤ 1 MiB; Strings ≤ 256 Runen, Listen ≤ 1000 Einträge; unlesbar → Dateiname in `errors`, Liste leer, Warn-Log ohne Inhalt; fehlend → leere Liste |
| Server-Endpunkte | `POST` mit `application/json`, `DisallowUnknownFields`, Body ≤ 16 KiB; `logs`: `200 application/x-ndjson` mit `X-Container-Running: true\|false`, Header erst in `start(running)` (Container inspiziert, Docker-Stream offen); Frist pro Schreibvorgang 30 s; höchstens **16** Streams (`agent.MaxLogStreams`), sonst `429 busy`; `rcon`: Schreibfrist 20 s; für `logs` und `rcon` wird die Lesefrist aufgehoben (`SetReadDeadline(time.Time{})`) |
| Fehler-Mapping | `instancespec.ErrInvalid` → 400 (ohne Code); `ErrNotFound` → 404 `not_found`; `ErrNotRunning` → 409 `not_running`; `ErrBusy` → 429 `busy`; `ErrRconUnavailable` → 503 `rcon_unavailable`; `ErrDockerUnavailable` → 503 `docker_unavailable`; sonst 500 `console operation failed` (Details nur im Log) |
| Shutdown | `Server.Shutdown` beendet offene Log-Streams sofort (`RegisterOnShutdown` bricht deren Kontext ab) |
| Ohne Docker | ohne `WithConsole` (Dev ohne `docker_socket`) antworten alle drei Endpunkte `503 docker_unavailable` |
| Build-Tags | keine neuen; FIFO-Tests nutzen `syscall.Mkfifo` (macOS + Linux); Integrationstest `linux && dockerintegration` |

### Vertrag V2 (Stand Übersicht) – was daraus für den agent folgt

```go
Logs(ctx context.Context, req agentapi.LogsRequest, start func(running bool), emit func(agentapi.LogLine) error) error
```

- Der Manager inspiziert den Container zuerst (fehlt/fremd → `ErrNotFound`, als HTTP `404 not_found`), öffnet dann den Docker-Log-Stream und ruft `start(running)` **genau einmal** vor der ersten Zeile auf – mit dem Zustand aus diesem Inspect (`State.Running && !State.Restarting`).
- Der Server setzt daraus `agentapi.HeaderContainerRunning` (`X-Container-Running: true|false`, Konstante aus Phase A), `Content-Type: application/x-ndjson`, schreibt `200` und flusht – auch wenn danach keine Zeile kommt (der panel-Client wartet höchstens 5 s auf die Header).
- Ein gestoppter Container liefert seine Historie (`tail`/`since`) und der Stream endet, sobald Docker den `follow`-Stream beendet; der agent wartet nie selbst auf einen Neustart (`scripts/ci-instances.sh` verlässt sich darauf).
- `agentapi.PlayersRequest.Validate()` (id > 0) existiert; der Server nutzt es. `PlayersResponse.Errors` hat die feste Reihenfolge `ops.json`, `whitelist.json`, `banned-players.json`; eine fehlende Datei ist kein Fehler.
- Phase A (`docs/superpowers/plans/2026-10-11-console-01-contracts.md`): Aufgabe 1 entfernt `clearStaleLists`/`emptyListFile`, Aufgabe 2 legt `agentapi/console.go` und den Client an (`agent.Client.Logs` liefert einen `agentapi.LogStream` mit `Running()` aus dem Header).

## Review Focus

1. **Spielerdateien als Angriffsfläche des root-agents (Übersicht #1):** `ops.json` als Symlink nach außen (absolut und relativ), als Symlink innerhalb des Verzeichnisses, als FIFO, als Verzeichnis, > 1 MiB, kaputtes JSON, Datenverzeichnis selbst als Symlink → nichts außerhalb gelesen, kein Hängen, Datei in `errors`. → `TestPlayersRefusesUntrustedFiles`, `TestPlayersRefusesSymlinkedDataDir` (Aufgabe 6).
2. **Hängende oder endlose Streams (Übersicht #3):** Client verschwindet, `ReadTimeout` läuft ab, Server fährt herunter, 17. Stream → Slot wird frei, Kontext lebt bis zum Ende, `Shutdown` wartet nicht auf Streams, `429 busy`. → `TestServerLogStreamLimit`, `TestServerLongRequestsOutliveReadTimeout`, `TestServerShutdownEndsLogStreams` (Aufgabe 7), `TestLogsEndsOnCancelAndEmitError` (Aufgabe 5).
3. **Fremde Container:** `mcpanel-mc-7` ohne Label bekommt weder `logs` noch `exec`. → `TestLogsErrors`, `TestRconErrors` (Aufgabe 5).
4. **Kaputte Docker-Streams:** Frame > 1 MiB, unbekannter Stream-Typ, Header über Lesegrenzen, Zeile über Frame-Grenzen, überlange Zeile, ungültiges UTF-8 → Fehler bzw. saubere, gekürzte Zeilen. → `TestDemuxer*` (Aufgabe 4), `TestLogsReassemblesAndSanitizesLines` (Aufgabe 5).
5. **RCON hängt oder flutet:** `rcon-cli` antwortet nicht, gibt > 64 KiB aus oder 5 Befehle laufen gleichzeitig → `rcon_unavailable` nach 10 s, gekürzte Ausgabe, `busy`, Slots werden wieder frei. → `TestRconTimeout`, `TestRconOutputIsBoundedAndValid`, `TestRconParallelLimit` (Aufgabe 5).

## Dateistruktur

```
internal/agent/docker/client.go                   (4)  + stream(): 2xx-Body für den Aufrufer
internal/agent/docker/stream.go                   (4)  Demuxer, Frame, StreamType, WriteFrame, ErrBadFrame, MaxFrameBytes
internal/agent/docker/logs.go                     (4)  LogsOptions, ContainerLogs
internal/agent/docker/exec.go                     (4)  ExecConfig, ExecInfo, ExecCreate, ExecStart, ExecInspect
internal/agent/docker/console_test.go             (4)
internal/agent/docker/dockertest/daemon.go        (4)  Routen + Ops logs/exec, Logs wecken
internal/agent/docker/dockertest/console.go       (4)  AppendLog, AppendRawLog, LogQueries, Exec-Fake
internal/agent/instances/manager.go               (5)  Config.RconCommand, Config.RconTimeout; Manager-Felder
internal/agent/instances/console.go               (5)  Manager.Logs, Manager.Rcon, Zeilen-Puffer, Ausgabe-Lesen
internal/agent/instances/console_test.go          (5)
internal/agent/instances/players.go               (6)  Manager.Players, readUntrustedFile
internal/agent/instances/players_test.go          (6)
internal/agent/server.go                          (7)  Server-Felder, Routen, Fristen, RegisterOnShutdown
internal/agent/server_console.go                  (7)  Console, WithConsole, noConsole, logs/rcon/players, consoleError
internal/agent/server_console_test.go             (7)
internal/agent/server_export_test.go              (7)  WithReadTimeout (nur Tests)
cmd/agent/main.go                                 (7)  agent.WithConsole(mgr)
internal/agent/docker/integration_linux_test.go   (8)  TestIntegrationConsole
```

---

### Task 4: Docker-Client – Demultiplexer, Logs, Exec (+ Fake-Daemon)

**Files:**
- Create: `internal/agent/docker/stream.go`, `internal/agent/docker/logs.go`, `internal/agent/docker/exec.go`, `internal/agent/docker/dockertest/console.go`
- Modify: `internal/agent/docker/client.go` (neue Hilfsfunktion `stream`), `internal/agent/docker/dockertest/daemon.go` (Ops, Felder, Routen, `ServeHTTP`, `SetState`)
- Test: `internal/agent/docker/console_test.go`

**Interfaces:**
- Consumes: `docker.Client.send`, `checkStatus`, `containerPath`, `call` (bestehend)
- Produces:
```go
package docker

const MaxFrameBytes = 1 << 20
type StreamType byte
const (Stdin StreamType = 0; Stdout StreamType = 1; Stderr StreamType = 2)
type Frame struct { Stream StreamType; Data []byte }
var ErrBadFrame error
type Demuxer struct{ /* … */ }
func NewDemuxer(r io.Reader) *Demuxer
func (d *Demuxer) Next() (Frame, error)                 // io.EOF | io.ErrUnexpectedEOF | ErrBadFrame
func WriteFrame(w io.Writer, st StreamType, data []byte) error

type LogsOptions struct { Follow bool; Tail int /* < 0 = all */; Since int64 /* ns, 0 = none */ }
func (c *Client) ContainerLogs(ctx context.Context, id string, o LogsOptions) (io.ReadCloser, error)

type ExecConfig struct { Cmd []string; User, WorkingDir string; Env []string; AttachStdin, AttachStdout, AttachStderr, Tty bool }
type ExecInfo struct { Running bool; ExitCode *int }
func (c *Client) ExecCreate(ctx context.Context, id string, cfg ExecConfig) (string, error)
func (c *Client) ExecStart(ctx context.Context, execID string) (io.ReadCloser, error)
func (c *Client) ExecInspect(ctx context.Context, execID string) (ExecInfo, error)

package dockertest
const (OpLogs = "logs"; OpExecCreate = "exec_create"; OpExecStart = "exec_start"; OpExecInspect = "exec_inspect")
func (d *Daemon) AppendLog(name string, stream docker.StreamType, line string) time.Time
func (d *Daemon) AppendRawLog(name string, stream docker.StreamType, payload []byte)
func (d *Daemon) LogQueries() []string
type ExecResult struct { Stdout, Stderr string; ExitCode int }
type ExecRecord struct { Container string; Config docker.ExecConfig }
func (d *Daemon) SetExecHandler(f func(container string, cfg docker.ExecConfig) ExecResult)
func (d *Daemon) Execs() []ExecRecord
```

Hintergrund (für Reviewer): Ohne TTY liefert Docker Logs und Exec-Ausgabe im Multiplex-Format. `POST /exec/{id}/start` ohne `Upgrade`-Header beantwortet Docker mit `HTTP/1.1 200 OK` (Content-Type `application/vnd.docker.raw-stream`, ohne Länge) und schreibt die gemultiplexte Ausgabe, bis der Prozess endet und Docker die Verbindung schließt – der Go-Client liest das als normalen Body bis EOF. Weil stdin nicht angehängt ist, braucht es keinen Hijack. Mit `timestamps=1` beginnt jede Log-Zeile mit `2006-01-02T15:04:05.000000000Z ` (feste 9 Nachkommastellen); `since=<s>.<ns>` ist inklusiv.

- [ ] **Step 1: Worktree, Branch und Draft-PR**

```bash
git -C /Users/jannes/Workspace/mc-panel fetch origin
git -C /Users/jannes/Workspace/mc-panel worktree add ../mc-panel-wt/feat/console-agent -b feat/console-agent origin/main
cd ../mc-panel-wt/feat/console-agent
```

(Die Draft-PR entsteht beim ersten Push in Step 7.)

- [ ] **Step 2: Failing test schreiben** – `internal/agent/docker/console_test.go`

```go
package docker_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
)

func frame(st docker.StreamType, s string) []byte {
	var b bytes.Buffer
	if err := docker.WriteFrame(&b, st, []byte(s)); err != nil {
		panic(err)
	}
	return b.Bytes()
}

// readFrames demultiplexes r completely; every frame as "<stream>:<data>".
func readFrames(t *testing.T, r io.Reader) []string {
	t.Helper()
	d := docker.NewDemuxer(r)
	var out []string
	for {
		f, err := d.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, fmt.Sprintf("%d:%s", f.Stream, f.Data))
	}
}

// logText drops the "<timestamp> " prefix Docker adds with timestamps=1.
func logText(t *testing.T, f string) string {
	t.Helper()
	stream, payload, _ := strings.Cut(f, ":")
	ts, text, ok := strings.Cut(payload, " ")
	if !ok {
		t.Fatalf("frame without timestamp: %q", f)
	}
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Fatalf("timestamp %q: %v", ts, err)
	}
	return stream + ":" + text
}

func TestDemuxer(t *testing.T) {
	stream := slices.Concat(frame(docker.Stdout, "hello\n"), frame(docker.Stderr, "oops\n"),
		frame(docker.Stdout, ""), frame(docker.Stdout, "x"))
	want := []string{"1:hello\n", "2:oops\n", "1:", "1:x"}
	for _, tc := range []struct {
		name string
		r    io.Reader
	}{
		{"whole", bytes.NewReader(stream)},
		{"one byte per read", iotest.OneByteReader(bytes.NewReader(stream))},
		{"half reads", iotest.HalfReader(bytes.NewReader(stream))},
	} {
		if got := readFrames(t, tc.r); !slices.Equal(got, want) {
			t.Fatalf("%s: frames = %q, want %q", tc.name, got, want)
		}
	}
	if got := readFrames(t, bytes.NewReader(nil)); len(got) != 0 {
		t.Fatalf("empty stream = %q", got)
	}
}

func TestDemuxerRejectsBrokenStreams(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"frame larger than 1 MiB", []byte{1, 0, 0, 0, 0, 0x10, 0, 1}, docker.ErrBadFrame},
		{"unknown stream type", []byte{3, 0, 0, 0, 0, 0, 0, 1, 'x'}, docker.ErrBadFrame},
		{"non-zero padding", []byte{1, 0, 1, 0, 0, 0, 0, 1, 'x'}, docker.ErrBadFrame},
		{"cut in header", []byte{1, 0, 0}, io.ErrUnexpectedEOF},
		{"cut in payload", []byte{1, 0, 0, 0, 0, 0, 0, 5, 'a', 'b'}, io.ErrUnexpectedEOF},
	}
	for _, tc := range cases {
		_, err := docker.NewDemuxer(bytes.NewReader(tc.data)).Next()
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: Next = %v, want %v", tc.name, err, tc.want)
		}
	}
	exact := append([]byte{1, 0, 0, 0, 0, 0x10, 0, 0}, make([]byte, docker.MaxFrameBytes)...)
	if f, err := docker.NewDemuxer(bytes.NewReader(exact)).Next(); err != nil || len(f.Data) != docker.MaxFrameBytes {
		t.Fatalf("frame of exactly 1 MiB: %d bytes, %v", len(f.Data), err)
	}
	if err := docker.WriteFrame(io.Discard, docker.Stdout, make([]byte, docker.MaxFrameBytes+1)); !errors.Is(err, docker.ErrBadFrame) {
		t.Fatalf("WriteFrame > 1 MiB = %v", err)
	}
}

func TestContainerLogs(t *testing.T) {
	d := dockertest.New(t)
	d.AddImage(pinned)
	d.AddContainer("mc", docker.ContainerConfig{Image: pinned}, false)
	t1 := d.AppendLog("mc", docker.Stdout, "first")
	d.AppendLog("mc", docker.Stderr, "second")
	d.AppendLog("mc", docker.Stdout, "third")
	c := docker.New(d.Socket)
	ctx := context.Background()

	read := func(o docker.LogsOptions) []string {
		t.Helper()
		body, err := c.ContainerLogs(ctx, "mc", o)
		if err != nil {
			t.Fatalf("ContainerLogs(%+v): %v", o, err)
		}
		defer func() { _ = body.Close() }()
		var out []string
		for _, f := range readFrames(t, body) {
			out = append(out, logText(t, f))
		}
		return out
	}
	if got, want := read(docker.LogsOptions{Tail: 2}), []string{"2:second\n", "1:third\n"}; !slices.Equal(got, want) {
		t.Fatalf("tail 2 = %q, want %q", got, want)
	}
	since := t1.UnixNano() + 1
	if got, want := read(docker.LogsOptions{Tail: -1, Since: since}), []string{"2:second\n", "1:third\n"}; !slices.Equal(got, want) {
		t.Fatalf("since = %q, want %q", got, want)
	}
	if got := read(docker.LogsOptions{Tail: -1}); len(got) != 3 {
		t.Fatalf("all = %q", got)
	}
	if got := read(docker.LogsOptions{Tail: 0}); len(got) != 0 {
		t.Fatalf("tail 0 = %q", got)
	}
	want := []string{
		"follow=0&stderr=1&stdout=1&tail=2&timestamps=1",
		fmt.Sprintf("follow=0&since=%d.%09d&stderr=1&stdout=1&tail=all&timestamps=1", since/int64(time.Second), since%int64(time.Second)),
		"follow=0&stderr=1&stdout=1&tail=all&timestamps=1",
		"follow=0&stderr=1&stdout=1&tail=0&timestamps=1",
	}
	if got := d.LogQueries(); !slices.Equal(got, want) {
		t.Fatalf("queries =\n%q\nwant\n%q", got, want)
	}
	if _, err := c.ContainerLogs(ctx, "missing", docker.LogsOptions{Tail: -1}); !docker.IsNotFound(err) {
		t.Fatalf("logs of a missing container = %v, want 404", err)
	}
}

func TestContainerLogsFollow(t *testing.T) {
	d := dockertest.New(t)
	d.AddImage(pinned)
	d.AddContainer("mc", docker.ContainerConfig{Image: pinned}, true)
	d.AppendLog("mc", docker.Stdout, "history")
	body, err := docker.New(d.Socket).ContainerLogs(context.Background(), "mc", docker.LogsOptions{Follow: true, Tail: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	frames, errc := make(chan string, 8), make(chan error, 1)
	go func() {
		dm := docker.NewDemuxer(body)
		for {
			f, err := dm.Next()
			if err != nil {
				errc <- err
				return
			}
			frames <- string(f.Data)
		}
	}()
	d.AppendLog("mc", docker.Stdout, "live")
	select {
	case f := <-frames:
		if !strings.HasSuffix(f, " live\n") {
			t.Fatalf("first followed frame = %q, want the live line (tail 0 skips history)", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no live line")
	}
	d.SetState("mc", "exited", 0)
	select {
	case err := <-errc:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("stream end = %v, want io.EOF", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end when the container stopped")
	}
}

func TestContainerLogsStopsOnCancel(t *testing.T) {
	d := dockertest.New(t)
	d.AddImage(pinned)
	d.AddContainer("mc", docker.ContainerConfig{Image: pinned}, true)
	ctx, cancel := context.WithCancel(context.Background())
	body, err := docker.New(d.Socket).ContainerLogs(ctx, "mc", docker.LogsOptions{Follow: true, Tail: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	errc := make(chan error, 1)
	go func() {
		_, err := docker.NewDemuxer(body).Next()
		errc <- err
	}()
	cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("read after cancel succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the followed stream")
	}
}

func TestExec(t *testing.T) {
	d := dockertest.New(t)
	d.AddImage(pinned)
	d.AddContainer("mc", docker.ContainerConfig{Image: pinned}, true)
	d.AddContainer("stopped", docker.ContainerConfig{Image: pinned}, false)
	c := docker.New(d.Socket)
	ctx := context.Background()
	cfg := docker.ExecConfig{
		Cmd: []string{"rcon-cli", "list"}, User: "1000:1000", WorkingDir: "/data", Env: []string{"HOME=/data"},
		AttachStdout: true, AttachStderr: true,
	}

	id, err := c.ExecCreate(ctx, "mc", cfg)
	if err != nil || id == "" {
		t.Fatalf("ExecCreate = %q, %v", id, err)
	}
	if info, err := c.ExecInspect(ctx, id); err != nil || info.ExitCode != nil {
		t.Fatalf("inspect before start = %+v, %v (exit code must be unknown)", info, err)
	}
	body, err := c.ExecStart(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	got := readFrames(t, body)
	_ = body.Close()
	if !slices.Equal(got, []string{"1:list\n"}) {
		t.Fatalf("output = %q", got)
	}
	info, err := c.ExecInspect(ctx, id)
	if err != nil || info.Running || info.ExitCode == nil || *info.ExitCode != 0 {
		t.Fatalf("inspect after run = %+v, %v", info, err)
	}
	if recs := d.Execs(); len(recs) != 1 || recs[0].Container != "mc" || !slices.Equal(recs[0].Config.Cmd, cfg.Cmd) ||
		recs[0].Config.User != "1000:1000" || recs[0].Config.WorkingDir != "/data" || !slices.Equal(recs[0].Config.Env, cfg.Env) ||
		recs[0].Config.AttachStdin || !recs[0].Config.AttachStdout || !recs[0].Config.AttachStderr || recs[0].Config.Tty {
		t.Fatalf("exec records = %+v", recs)
	}

	d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult {
		return dockertest.ExecResult{Stdout: "out", Stderr: "err", ExitCode: 3}
	})
	id, err = c.ExecCreate(ctx, "mc", cfg)
	if err != nil {
		t.Fatal(err)
	}
	body, err = c.ExecStart(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	got = readFrames(t, body)
	_ = body.Close()
	if !slices.Equal(got, []string{"1:out", "2:err"}) {
		t.Fatalf("output = %q", got)
	}
	if info, err := c.ExecInspect(ctx, id); err != nil || info.ExitCode == nil || *info.ExitCode != 3 {
		t.Fatalf("exit code = %+v, %v", info, err)
	}

	if _, err := c.ExecCreate(ctx, "stopped", cfg); !docker.IsConflict(err) {
		t.Fatalf("exec in a stopped container = %v, want 409", err)
	}
	if _, err := c.ExecCreate(ctx, "missing", cfg); !docker.IsNotFound(err) {
		t.Fatalf("exec in a missing container = %v, want 404", err)
	}
	if _, err := c.ExecInspect(ctx, "nope"); !docker.IsNotFound(err) {
		t.Fatalf("inspect of an unknown exec = %v, want 404", err)
	}
}
```

- [ ] **Step 3: Rot prüfen**

Run: `make check`
Expected: FAIL beim Kompilieren der Tests (`undefined: docker.WriteFrame`, `docker.NewDemuxer`, `d.AppendLog`, …).

- [ ] **Step 4: Demultiplexer** – `internal/agent/docker/stream.go`

```go
package docker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MaxFrameBytes bounds one frame of Docker's multiplexed stream format; a larger length
// in a header means a broken (or hostile) stream.
const MaxFrameBytes = 1 << 20

// StreamType is the first header byte of a multiplexed frame.
type StreamType byte

const (
	Stdin  StreamType = 0
	Stdout StreamType = 1
	Stderr StreamType = 2
)

// Frame is one frame of a multiplexed stream.
type Frame struct {
	Stream StreamType
	Data   []byte
}

// ErrBadFrame reports a frame header that violates the format.
var ErrBadFrame = errors.New("docker: bad multiplexed stream frame")

// Demuxer reads Docker's multiplexed stream format, used for logs and exec output of
// containers without TTY: an 8-byte header (stream type, three zero bytes, payload
// length as big-endian uint32) followed by the payload.
type Demuxer struct {
	r   io.Reader
	hdr [8]byte
}

func NewDemuxer(r io.Reader) *Demuxer { return &Demuxer{r: r} }

// Next returns the next frame: io.EOF at a clean end between frames,
// io.ErrUnexpectedEOF inside a frame, ErrBadFrame for an invalid header.
func (d *Demuxer) Next() (Frame, error) {
	if _, err := io.ReadFull(d.r, d.hdr[:]); err != nil {
		return Frame{}, err // io.EOF if nothing was read, io.ErrUnexpectedEOF if cut
	}
	st := StreamType(d.hdr[0])
	if st > Stderr || d.hdr[1] != 0 || d.hdr[2] != 0 || d.hdr[3] != 0 {
		return Frame{}, fmt.Errorf("%w: header % x", ErrBadFrame, d.hdr)
	}
	n := binary.BigEndian.Uint32(d.hdr[4:])
	if n > MaxFrameBytes {
		return Frame{}, fmt.Errorf("%w: frame of %d bytes exceeds %d", ErrBadFrame, n, MaxFrameBytes)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(d.r, data); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return Frame{}, err
	}
	return Frame{Stream: st, Data: data}, nil
}

// WriteFrame writes data as one frame. The agent only reads frames; the fake daemon
// and tests write them.
func WriteFrame(w io.Writer, st StreamType, data []byte) error {
	if len(data) > MaxFrameBytes {
		return fmt.Errorf("%w: frame of %d bytes exceeds %d", ErrBadFrame, len(data), MaxFrameBytes)
	}
	var hdr [8]byte
	hdr[0] = byte(st)
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}
```

- [ ] **Step 5: Logs und Exec im Client**

In `internal/agent/docker/client.go` direkt unter `call` einfügen:

```go
// stream sends one request and hands the body of a 2xx answer to the caller, who reads
// and closes it. Only ctx bounds the call: streams (followed logs, exec output) last as
// long as the caller wants.
func (c *Client) stream(ctx context.Context, method, path string, q url.Values, in any) (io.ReadCloser, error) {
	resp, err := c.send(ctx, method, path, q, in)
	if err != nil {
		return nil, err
	}
	if err := checkStatus(resp, method, path); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}
```

Falls `bodyclose` hier anschlägt (der Body wird bewusst an den Aufrufer übergeben), in derselben Zeile `resp, err := c.send(...) //nolint:bodyclose // the body is returned to the caller, who closes it` ergänzen.

`internal/agent/docker/logs.go`:

```go
package docker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// LogsOptions selects the part of a container log that ContainerLogs returns.
type LogsOptions struct {
	// Follow keeps the stream open for new lines until the container stops.
	Follow bool
	// Tail is the number of lines from the end; < 0 means all.
	Tail int
	// Since is a Docker timestamp in nanoseconds since the Unix epoch (inclusive); 0 means none.
	Since int64
}

func (o LogsOptions) query() url.Values {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "follow": {"0"}, "tail": {"all"}}
	if o.Follow {
		q.Set("follow", "1")
	}
	if o.Tail >= 0 {
		q.Set("tail", strconv.Itoa(o.Tail))
	}
	if o.Since > 0 {
		q.Set("since", fmt.Sprintf("%d.%09d", o.Since/int64(time.Second), o.Since%int64(time.Second)))
	}
	return q
}

// ContainerLogs opens the log of a container without TTY as a multiplexed stream (read it
// with NewDemuxer). Every line starts with its RFC 3339 timestamp (9 fraction digits)
// and a space. Only ctx bounds the call.
func (c *Client) ContainerLogs(ctx context.Context, id string, o LogsOptions) (io.ReadCloser, error) {
	return c.stream(ctx, http.MethodGet, containerPath(id, "logs"), o.query(), nil)
}
```

`internal/agent/docker/exec.go`:

```go
package docker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ExecConfig is the body of POST /containers/{id}/exec. Only fields mc-panel sets exist.
type ExecConfig struct {
	Cmd          []string `json:"Cmd"`
	User         string   `json:"User,omitempty"`
	WorkingDir   string   `json:"WorkingDir,omitempty"`
	Env          []string `json:"Env,omitempty"`
	AttachStdin  bool     `json:"AttachStdin"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Tty          bool     `json:"Tty"`
}

// ExecInfo is the subset of GET /exec/{id}/json mc-panel reads; ExitCode is nil while
// Docker does not know it yet.
type ExecInfo struct {
	Running  bool `json:"Running"`
	ExitCode *int `json:"ExitCode"`
}

type execStart struct {
	Detach bool `json:"Detach"`
	Tty    bool `json:"Tty"`
}

// ExecCreate prepares cmd in a running container and returns the exec ID
// (404: no such container, 409: container not running).
func (c *Client) ExecCreate(ctx context.Context, id string, cfg ExecConfig) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	if err := c.call(ctx, c.timeout, http.MethodPost, containerPath(id, "exec"), nil, cfg, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("%w: exec create %s: empty exec id", ErrUnavailable, id)
	}
	return out.ID, nil
}

// ExecStart runs a created exec attached (Detach and Tty false) and returns its output as
// a multiplexed stream. Without an Upgrade header Docker answers 200 and streams until
// the process ends; stdin stays closed, so no connection hijack is needed. Only ctx
// bounds the call.
func (c *Client) ExecStart(ctx context.Context, execID string) (io.ReadCloser, error) {
	return c.stream(ctx, http.MethodPost, execPath(execID, "start"), nil, execStart{})
}

// ExecInspect returns whether the exec still runs and its exit code.
func (c *Client) ExecInspect(ctx context.Context, execID string) (ExecInfo, error) {
	var out ExecInfo
	err := c.call(ctx, c.timeout, http.MethodGet, execPath(execID, "json"), nil, nil, &out)
	return out, err
}

func execPath(id, action string) string { return "/exec/" + url.PathEscape(id) + "/" + action }
```

- [ ] **Step 6: Fake-Daemon erweitern**

In `internal/agent/docker/dockertest/daemon.go`:

1. Konstanten ergänzen (nach `OpStats`):

```go
	OpLogs        = "logs"         // GET /containers/{id}/logs
	OpExecCreate  = "exec_create"  // POST /containers/{id}/exec
	OpExecStart   = "exec_start"   // POST /exec/{id}/start
	OpExecInspect = "exec_inspect" // GET /exec/{id}/json
```

2. Felder in `Daemon` ergänzen (nach `stats`):

```go
	logs        map[string][]logEntry // by container name
	logWake     chan struct{}         // closed and replaced on every log or state change
	logSeq      int
	logQueries  []string
	execs       map[string]*execState
	execRecords []ExecRecord
	execHandler func(container string, cfg docker.ExecConfig) ExecResult
```

3. In `New` initialisieren: `logs: map[string][]logEntry{}, logWake: make(chan struct{}), execs: map[string]*execState{},`.

4. `SetState` weckt Log-Leser – den Rumpf ersetzen durch:

```go
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.containers[name]; ok {
		c.Status, c.ExitCode = status, exitCode
	}
	d.wakeLogs()
```

5. In `route` vor `case strings.HasPrefix(path, "/containers/"):` einfügen:

```go
	case strings.HasPrefix(path, "/exec/"):
		id, action, _ := strings.Cut(strings.TrimPrefix(path, "/exec/"), "/")
		switch {
		case method == http.MethodPost && action == "start":
			return OpExecStart, id
		case method == http.MethodGet && action == "json":
			return OpExecInspect, id
		}
```

und im inneren `switch` des Container-Zweigs ergänzen:

```go
		case method == http.MethodGet && action == "logs":
			return OpLogs, id
		case method == http.MethodPost && action == "exec":
			return OpExecCreate, id
```

6. In `ServeHTTP` direkt nach dem `if failing { … }`-Block (vor `d.mu.Lock()`) einfügen – Logs streamen, dürfen also die Sperre nicht halten:

```go
	if op == OpLogs {
		d.serveLogs(w, r, target)
		return
	}
```

und den `switch op` am Ende ersetzen durch:

```go
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
	case OpExecStart:
		d.execStart(w, r, target)
	case OpExecInspect:
		d.execInspect(w, target)
	default:
		d.container(w, r, op, target)
		d.wakeLogs() // start/stop/kill/remove end or continue followed logs
	}
```

7. In `container` im `switch op` ergänzen:

```go
	case OpExecCreate:
		d.execCreate(w, r, c)
```

Neue Datei `internal/agent/docker/dockertest/console.go`:

```go
package dockertest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/docker"
)

// logTimeFormat is Docker's fixed-width timestamp of log lines (timestamps=1).
const logTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

type logEntry struct {
	at      time.Time
	stream  docker.StreamType
	payload []byte
}

// ExecResult is what a fake exec process prints and returns.
type ExecResult struct {
	Stdout, Stderr string
	ExitCode       int
}

// ExecRecord is one exec create request.
type ExecRecord struct {
	Container string
	Config    docker.ExecConfig
}

type execState struct {
	container string
	cfg       docker.ExecConfig
	started   bool
	exitCode  *int
}

// AppendLog adds one line to the log of the container called name the way Docker's
// json-file driver returns it with timestamps=1: "<timestamp> <line>\n" in one frame.
// Every entry is 1 ms after the previous one; its timestamp is returned.
func (d *Daemon) AppendLog(name string, stream docker.StreamType, line string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	at := d.nextLogTime()
	d.logs[name] = append(d.logs[name], logEntry{at: at, stream: stream, payload: []byte(at.UTC().Format(logTimeFormat) + " " + line + "\n")})
	d.wakeLogs()
	return at
}

// AppendRawLog adds one frame with exactly payload (no timestamp or newline added), e.g.
// half a line. For since it counts as written 1 ms after the previous entry.
func (d *Daemon) AppendRawLog(name string, stream docker.StreamType, payload []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.logs[name] = append(d.logs[name], logEntry{at: d.nextLogTime(), stream: stream, payload: slices.Clone(payload)})
	d.wakeLogs()
}

// LogQueries returns the raw query strings of all logs requests in arrival order.
func (d *Daemon) LogQueries() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.logQueries)
}

// SetExecHandler sets what exec processes do. The default prints the arguments after the
// program name plus "\n" (like echo) and exits 0. f runs with the daemon locked and must
// not call Daemon methods.
func (d *Daemon) SetExecHandler(f func(container string, cfg docker.ExecConfig) ExecResult) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.execHandler = f
}

// Execs returns all exec create requests in arrival order.
func (d *Daemon) Execs() []ExecRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.execRecords)
}

func (d *Daemon) nextLogTime() time.Time {
	d.logSeq++
	return d.now.Add(time.Duration(d.logSeq) * time.Millisecond)
}

// wakeLogs wakes all followers (caller holds d.mu).
func (d *Daemon) wakeLogs() {
	close(d.logWake)
	d.logWake = make(chan struct{})
}

func parseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	sec, frac, _ := strings.Cut(s, ".")
	secs, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	var ns int64
	if frac != "" {
		if len(frac) != 9 {
			return time.Time{}, errors.New("want 9 fraction digits")
		}
		if ns, err = strconv.ParseInt(frac, 10, 64); err != nil {
			return time.Time{}, err
		}
	}
	return time.Unix(secs, ns), nil
}

// serveLogs streams the log like Docker: since (inclusive) and tail select the history,
// follow keeps the stream open while the container runs.
func (d *Daemon) serveLogs(w http.ResponseWriter, r *http.Request, ref string) {
	q := r.URL.Query()
	since, err := parseSince(q.Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid since: "+err.Error())
		return
	}
	tail := -1
	if t := q.Get("tail"); t != "" && t != "all" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid tail "+t)
			return
		}
		tail = n
	}
	follow := q.Get("follow") == "1" || q.Get("follow") == "true"

	d.mu.Lock()
	c := d.lookup(ref)
	if c == nil {
		d.mu.Unlock()
		writeError(w, http.StatusNotFound, "No such container: "+ref)
		return
	}
	name := c.Name
	d.logQueries = append(d.logQueries, r.URL.RawQuery)
	d.mu.Unlock()

	w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
	w.WriteHeader(http.StatusOK)
	flush := func() {
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	flush()
	next, first := 0, true
	for {
		d.mu.Lock()
		entries := slices.Clone(d.logs[name])
		cur := d.containers[name]
		running := cur != nil && cur.Status == "running"
		wake := d.logWake
		d.mu.Unlock()

		var out []logEntry
		for _, e := range entries[next:] {
			if since.IsZero() || !e.at.Before(since) {
				out = append(out, e)
			}
		}
		next = len(entries)
		if first && tail >= 0 && len(out) > tail {
			out = out[len(out)-tail:]
		}
		first = false
		for _, e := range out {
			if err := docker.WriteFrame(w, e.stream, e.payload); err != nil {
				return
			}
		}
		flush()
		if !follow || !running {
			return
		}
		select {
		case <-wake:
		case <-r.Context().Done():
			return
		}
	}
}

func echoExec(_ string, cfg docker.ExecConfig) ExecResult {
	return ExecResult{Stdout: strings.Join(cfg.Cmd[1:], " ") + "\n"}
}

// execCreate handles POST /containers/{id}/exec (caller holds d.mu).
func (d *Daemon) execCreate(w http.ResponseWriter, r *http.Request, c *Container) {
	var cfg docker.ExecConfig
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil || len(cfg.Cmd) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("bad exec config: %v", err))
		return
	}
	if c.Status != "running" {
		writeError(w, http.StatusConflict, "container "+c.ID+" is not running")
		return
	}
	d.seq++
	id := fmt.Sprintf("exec-%d", d.seq)
	d.execs[id] = &execState{container: c.Name, cfg: cfg}
	d.execRecords = append(d.execRecords, ExecRecord{Container: c.Name, Config: cfg})
	writeJSON(w, http.StatusCreated, map[string]string{"Id": id})
}

// execStart handles POST /exec/{id}/start (caller holds d.mu): only attached, without TTY.
func (d *Daemon) execStart(w http.ResponseWriter, r *http.Request, id string) {
	e := d.execs[id]
	if e == nil {
		writeError(w, http.StatusNotFound, "No such exec instance: "+id)
		return
	}
	var req struct {
		Detach bool
		Tty    bool
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || req.Detach || req.Tty {
		writeError(w, http.StatusBadRequest, "fake supports only attached exec without TTY")
		return
	}
	if e.started {
		writeError(w, http.StatusConflict, "exec "+id+" already started")
		return
	}
	e.started = true
	h := d.execHandler
	if h == nil {
		h = echoExec
	}
	res := h(e.container, e.cfg)
	w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
	w.WriteHeader(http.StatusOK)
	if res.Stdout != "" {
		_ = docker.WriteFrame(w, docker.Stdout, []byte(res.Stdout))
	}
	if res.Stderr != "" {
		_ = docker.WriteFrame(w, docker.Stderr, []byte(res.Stderr))
	}
	code := res.ExitCode
	e.exitCode = &code
}

// execInspect handles GET /exec/{id}/json (caller holds d.mu).
func (d *Daemon) execInspect(w http.ResponseWriter, id string) {
	e := d.execs[id]
	if e == nil {
		writeError(w, http.StatusNotFound, "No such exec instance: "+id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ID": id, "Running": false, "ExitCode": e.exitCode})
}
```

- [ ] **Step 7: Grün prüfen, committen, Draft-PR**

Run: `make check`
Expected: PASS.

```bash
git add internal/agent/docker
git commit -m "feat(agent): docker logs and exec with a multiplexed-stream demuxer

The Docker client gains ContainerLogs, ExecCreate, ExecStart and ExecInspect
for the console (spec 5.2, 5.3). Streams are bounded only by the context;
frames above 1 MiB or with an unknown stream type are rejected. The fake
daemon learns followed logs and exec.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin feat/console-agent
gh pr create --draft --base main --title "feat(agent): console – logs, rcon, player lists (TP3a B1)" \
  --body "Plan: docs/superpowers/plans/2026-10-11-console-02-agent.md (Aufgaben 4–8). Spec: docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md §5."
gh pr checks --watch
```

Expected: CI grün (`TestDemuxer*`, `TestContainerLogs*`, `TestExec` PASS).

---

### Task 5: Manager – Live-Log und RCON (`internal/agent/instances/console.go`)

**Files:**
- Create: `internal/agent/instances/console.go`
- Modify: `internal/agent/instances/manager.go` (Config-Felder, Manager-Felder, `New`)
- Test: `internal/agent/instances/console_test.go`

**Interfaces:**
- Consumes: Aufgabe 4 (`docker.ContainerLogs`, `NewDemuxer`, `ExecCreate/Start/Inspect`, `dockertest.AppendLog/AppendRawLog/LogQueries/SetExecHandler/Execs`, `OpExecStart`, `OpLogs`); V1 (`agentapi.LogsRequest`, `LogLine`, `RconRequest.Validate`, `MaxLogLineBytes`, `MaxRconOutputBytes`, `StreamOut/StreamErr`, `ErrNotFound`, `ErrNotRunning`, `ErrRconUnavailable`, `ErrBusy`, `ErrDockerUnavailable`); bestehend `checkManaged`, `checkOwner`, `expectedName`, `dockerAvailable`, `ContainerName`
- Produces:
```go
// Config (neu)
RconCommand string        // "" = DefaultRconCommand; only tests change it
RconTimeout time.Duration // 0 = RconTimeout; only tests change it

const (
	DefaultRconCommand = "rcon-cli"
	RconTimeout        = 10 * time.Second
	MaxParallelRcon    = 4
)
func (m *Manager) Logs(ctx context.Context, req agentapi.LogsRequest, start func(running bool), emit func(agentapi.LogLine) error) error
func (m *Manager) Rcon(ctx context.Context, id int64, command string) (string, error)
```

- [ ] **Step 1: Failing test schreiben** – `internal/agent/instances/console_test.go`

```go
package instances_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
	"github.com/Bernotese/mc-panel/internal/agent/docker/dockertest"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// startInstance creates and starts mcpanel-mc-<id> the way the panel does.
func (e *env) startInstance(t *testing.T, id int64, name string) {
	t.Helper()
	e.ensure(t, ensureReq(id, name, 1))
	e.power(t, id, agentapi.PowerStart)
}

// collectLogs runs Logs in the background: lines arrive on lines, the result on done,
// started receives the running flag of every start call (the tests check it was exactly one).
func collectLogs(ctx context.Context, m *instances.Manager, req agentapi.LogsRequest) (lines chan agentapi.LogLine, started chan bool, done chan error) {
	lines, started, done = make(chan agentapi.LogLine, 64), make(chan bool, 2), make(chan error, 1)
	go func() {
		done <- m.Logs(ctx, req, func(running bool) { started <- running }, func(l agentapi.LogLine) error {
			lines <- l
			return nil
		})
	}()
	return lines, started, done
}

func nextLine(t *testing.T, lines <-chan agentapi.LogLine) agentapi.LogLine {
	t.Helper()
	select {
	case l := <-lines:
		return l
	case <-time.After(5 * time.Second):
		t.Fatal("no log line within 5 s")
		return agentapi.LogLine{}
	}
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Logs did not return within 5 s")
		return nil
	}
}

// drain returns the lines already delivered (call after Logs returned).
func drain(lines chan agentapi.LogLine) []agentapi.LogLine {
	var out []agentapi.LogLine
	for {
		select {
		case l := <-lines:
			out = append(out, l)
		default:
			return out
		}
	}
}

func TestLogsStreamsLines(t *testing.T) {
	e := newEnv(t)
	e.startInstance(t, 1, "survival")
	name := instances.ContainerName(1)
	e.d.AppendLog(name, docker.Stdout, "[12:00:00 INFO]: Starting minecraft server")
	t2 := e.d.AppendLog(name, docker.Stderr, "\x1b[33mWARN\x1b[0m slow tick")
	t3 := e.d.AppendLog(name, docker.Stdout, "[12:00:03 INFO]: Done (3.2s)!")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lines, started, done := collectLogs(ctx, e.m, agentapi.LogsRequest{ID: 1, Tail: 2})
	for _, want := range []agentapi.LogLine{
		{TS: t2.UnixNano(), Stream: agentapi.StreamErr, Line: "\x1b[33mWARN\x1b[0m slow tick"},
		{TS: t3.UnixNano(), Stream: agentapi.StreamOut, Line: "[12:00:03 INFO]: Done (3.2s)!"},
	} {
		if got := nextLine(t, lines); got != want {
			t.Fatalf("line = %+v, want %+v", got, want)
		}
	}
	select {
	case running := <-started:
		if !running {
			t.Fatal("start(false) for a running container")
		}
	default:
		t.Fatal("start was not called before the first line")
	}
	t4 := e.d.AppendLog(name, docker.Stdout, "live")
	if got := nextLine(t, lines); got != (agentapi.LogLine{TS: t4.UnixNano(), Stream: agentapi.StreamOut, Line: "live"}) {
		t.Fatalf("live line = %+v", got)
	}
	e.d.SetState(name, "exited", 0)
	if err := waitDone(t, done); err != nil {
		t.Fatalf("Logs after the container stopped = %v, want nil", err)
	}
	if len(started) != 0 {
		t.Fatal("start called more than once")
	}
	if q := e.d.LogQueries(); len(q) != 1 || !strings.Contains(q[0], "follow=1") || !strings.Contains(q[0], "tail=2") {
		t.Fatalf("log queries = %q", q)
	}
}

func TestLogsSinceWinsOverTail(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1)) // created, not running: the stream ends after the history
	name := instances.ContainerName(1)
	e.d.AppendLog(name, docker.Stdout, "a")
	t2 := e.d.AppendLog(name, docker.Stdout, "b")
	t3 := e.d.AppendLog(name, docker.Stdout, "c")
	since := t2.UnixNano() + 1
	lines, started, done := collectLogs(context.Background(), e.m, agentapi.LogsRequest{ID: 1, Tail: 1, Since: since})
	if err := waitDone(t, done); err != nil {
		t.Fatal(err) // a stopped container: history, then the stream ends by itself
	}
	if running := <-started; running {
		t.Fatal("start(true) for a container that is not running")
	}
	if got := drain(lines); !reflect.DeepEqual(got, []agentapi.LogLine{{TS: t3.UnixNano(), Stream: agentapi.StreamOut, Line: "c"}}) {
		t.Fatalf("lines after since = %+v", got)
	}
	q := e.d.LogQueries()
	wantSince := fmt.Sprintf("since=%d.%09d", since/int64(time.Second), since%int64(time.Second))
	if len(q) != 1 || !strings.Contains(q[0], "tail=all") || !strings.Contains(q[0], wantSince) {
		t.Fatalf("log query = %q, want tail=all and %s", q, wantSince)
	}
}

func TestLogsReassemblesAndSanitizesLines(t *testing.T) {
	e := newEnv(t)
	e.ensure(t, ensureReq(1, "survival", 1)) // not running: the stream ends after the history
	name := instances.ContainerName(1)
	const (
		ts1 = "2026-10-10T12:00:01.000000001Z"
		ts2 = "2026-10-10T12:00:02.000000002Z"
		ts3 = "2026-10-10T12:00:03.000000003Z"
		ts4 = "2026-10-10T12:00:04.000000004Z"
		ts5 = "2026-10-10T12:00:05.000000005Z"
		ts6 = "2026-10-10T12:00:06.000000006Z"
	)
	raw := func(st docker.StreamType, s string) { e.d.AppendRawLog(name, st, []byte(s)) }
	raw(docker.Stdout, ts1+" split ")                                      // line continues in a later frame
	raw(docker.Stderr, ts2+" err line\n")                                  // other stream in between
	raw(docker.Stdout, "line\r\n")                                         // end of the split line, CRLF
	raw(docker.Stdout, ts3+" a"+strings.Repeat("ä", 5000)+"\n")            // 10001 bytes: cut at a rune boundary
	raw(docker.Stdout, ts4+" "+strings.Repeat("x", 9000))                  // overlong, no newline yet
	raw(docker.Stdout, strings.Repeat("y", 100)+"\n"+ts5+" bad\xff\xfe end\n") // rest dropped; invalid UTF-8
	raw(docker.Stdout, "no timestamp\n")                                   // inherits ts5
	raw(docker.Stdout, ts6+" last line without newline")                   // flushed at the end

	lines, _, done := collectLogs(context.Background(), e.m, agentapi.LogsRequest{ID: 1, Tail: 1000})
	if err := waitDone(t, done); err != nil {
		t.Fatal(err)
	}
	ts := func(s string) int64 {
		tt, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return tt.UnixNano()
	}
	want := []agentapi.LogLine{
		{TS: ts(ts2), Stream: agentapi.StreamErr, Line: "err line"},
		{TS: ts(ts1), Stream: agentapi.StreamOut, Line: "split line"},
		{TS: ts(ts3), Stream: agentapi.StreamOut, Line: "a" + strings.Repeat("ä", 4095)},
		{TS: ts(ts4), Stream: agentapi.StreamOut, Line: strings.Repeat("x", agentapi.MaxLogLineBytes)},
		{TS: ts(ts5), Stream: agentapi.StreamOut, Line: "bad� end"},
		{TS: ts(ts5), Stream: agentapi.StreamOut, Line: "no timestamp"},
		{TS: ts(ts6), Stream: agentapi.StreamOut, Line: "last line without newline"},
	}
	if got := drain(lines); !reflect.DeepEqual(got, want) {
		for i := range max(len(got), len(want)) {
			var g, w agentapi.LogLine
			if i < len(got) {
				g = got[i]
			}
			if i < len(want) {
				w = want[i]
			}
			if g != w {
				t.Errorf("line %d: got %d %q (%d bytes), want %d %q (%d bytes)", i, g.TS, firstN(g.Line, 40), len(g.Line), w.TS, firstN(w.Line, 40), len(w.Line))
			}
		}
		t.FailNow()
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func TestLogsErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	startCalled := false
	start := func(bool) { startCalled = true }
	noop := func(agentapi.LogLine) error { return nil }

	if err := e.m.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10}, start, noop); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("missing container = %v, want ErrNotFound", err)
	}
	e.d.AddContainer(instances.ContainerName(7), docker.ContainerConfig{Image: java21Pinned}, true) // foreign: no labels
	if err := e.m.Logs(ctx, agentapi.LogsRequest{ID: 7, Tail: 10}, start, noop); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("foreign container = %v, want ErrNotFound", err)
	}
	for _, bad := range []agentapi.LogsRequest{{ID: 0}, {ID: 1, Tail: -1}, {ID: 1, Tail: agentapi.MaxLogTail + 1}, {ID: 1, Since: -1}} {
		if err := e.m.Logs(ctx, bad, start, noop); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("Logs(%+v) = %v, want ErrInvalid", bad, err)
		}
	}
	if slices.ContainsFunc(e.d.Calls(), func(c string) bool { return strings.HasPrefix(c, dockertest.OpLogs+" ") }) {
		t.Fatalf("logs requested for a refused call: %v", e.d.Calls())
	}

	e.startInstance(t, 1, "survival")
	e.d.Fail(dockertest.OpLogs, http.StatusInternalServerError, "log driver broken")
	if err := e.m.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10}, start, noop); err == nil || errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("failing log driver = %v", err)
	}
	e.d.Clear()

	cfg := e.config()
	cfg.Docker = docker.New(filepath.Join(t.TempDir(), "missing.sock"))
	down := instances.New(cfg)
	defer down.Close()
	if err := down.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10}, start, noop); !errors.Is(err, agentapi.ErrDockerUnavailable) {
		t.Fatalf("Docker down = %v, want ErrDockerUnavailable", err)
	}
	if startCalled {
		t.Fatal("start called for a stream that never opened")
	}
}

func TestLogsEndsOnCancelAndEmitError(t *testing.T) {
	e := newEnv(t)
	e.startInstance(t, 1, "survival")
	e.d.AppendLog(instances.ContainerName(1), docker.Stdout, "one")
	req := agentapi.LogsRequest{ID: 1, Tail: 10}

	ctx, cancel := context.WithCancel(context.Background())
	lines, _, done := collectLogs(ctx, e.m, req)
	nextLine(t, lines)
	cancel() // the panel went away
	if err := waitDone(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Logs after cancel = %v, want context.Canceled", err)
	}

	errStop := errors.New("panel gone")
	err := e.m.Logs(context.Background(), req, func(bool) {}, func(agentapi.LogLine) error { return errStop })
	if !errors.Is(err, errStop) {
		t.Fatalf("Logs with failing emit = %v, want %v", err, errStop)
	}
}

func TestRconRunsCommandInContainer(t *testing.T) {
	e := newEnv(t)
	e.startInstance(t, 1, "survival")
	out, err := e.m.Rcon(context.Background(), 1, "say Hallo Welt")
	if err != nil || out != "say Hallo Welt" { // the fake echoes the arguments after the program
		t.Fatalf("Rcon = %q, %v", out, err)
	}
	want := dockertest.ExecRecord{Container: instances.ContainerName(1), Config: docker.ExecConfig{
		Cmd: []string{"rcon-cli", "say Hallo Welt"}, User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		WorkingDir: "/data", Env: []string{"HOME=/data"}, AttachStdout: true, AttachStderr: true,
	}}
	if execs := e.d.Execs(); len(execs) != 1 || !reflect.DeepEqual(execs[0], want) {
		t.Fatalf("execs = %+v, want %+v", execs, want)
	}
}

func TestRconCommandIsConfigurable(t *testing.T) {
	e := newEnv(t, func(c *instances.Config) { c.RconCommand = "echo" })
	e.startInstance(t, 1, "survival")
	if _, err := e.m.Rcon(context.Background(), 1, "list"); err != nil {
		t.Fatal(err)
	}
	if execs := e.d.Execs(); len(execs) != 1 || execs[0].Config.Cmd[0] != "echo" {
		t.Fatalf("execs = %+v", execs)
	}
}

func TestRconErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("missing container = %v, want ErrNotFound", err)
	}
	e.ensure(t, ensureReq(1, "survival", 1)) // created, not running
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrNotRunning) {
		t.Fatalf("stopped container = %v, want ErrNotRunning", err)
	}
	e.d.AddContainer(instances.ContainerName(7), docker.ContainerConfig{Image: java21Pinned}, true) // foreign
	if _, err := e.m.Rcon(ctx, 7, "list"); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("foreign container = %v, want ErrNotFound", err)
	}
	for _, bad := range []string{"", "   ", "say a\nop b", "say\ta", "say \x00", "say \x7f", strings.Repeat("a", agentapi.MaxCommandBytes+1)} {
		if _, err := e.m.Rcon(ctx, 1, bad); !errors.Is(err, instancespec.ErrInvalid) {
			t.Errorf("Rcon(%q) = %v, want ErrInvalid", bad, err)
		}
	}
	if _, err := e.m.Rcon(ctx, 0, "list"); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("id 0 = %v, want ErrInvalid", err)
	}
	if n := len(e.d.Execs()); n != 0 {
		t.Fatalf("%d execs for refused commands; foreign or stopped containers must never get one", n)
	}

	e.power(t, 1, agentapi.PowerStart)
	e.d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult {
		return dockertest.ExecResult{Stderr: "Failed to connect to RCON serverdial tcp [::1]:25575: connect: connection refused\n", ExitCode: 1}
	})
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrRconUnavailable) || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("rcon-cli exit 1 = %v, want ErrRconUnavailable with its output", err)
	}
	e.d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult {
		return dockertest.ExecResult{Stdout: strings.Repeat("x", 2000), ExitCode: 1}
	})
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrRconUnavailable) || strings.Count(err.Error(), "x") != 500 {
		t.Fatalf("long failure output must be cut to 500 characters: %d", strings.Count(fmt.Sprint(err), "x"))
	}
	e.d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult { return dockertest.ExecResult{ExitCode: 2} })
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrRconUnavailable) || !strings.Contains(err.Error(), "rcon-cli exited with code 2") {
		t.Fatalf("silent failure = %v", err)
	}
}

func TestRconOutputIsBoundedAndValid(t *testing.T) {
	e := newEnv(t)
	e.startInstance(t, 1, "survival")
	ctx := context.Background()
	const mark = "\n[… gekürzt]"
	e.d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult {
		return dockertest.ExecResult{Stdout: strings.Repeat("a", agentapi.MaxRconOutputBytes) + "OVERFLOW"}
	})
	out, err := e.m.Rcon(ctx, 1, "list")
	if err != nil || !strings.HasSuffix(out, mark) || strings.Contains(out, "OVERFLOW") || len(out) > agentapi.MaxRconOutputBytes+len(mark) {
		t.Fatalf("overlong output: %d bytes, suffix %q, %v", len(out), out[max(len(out)-20, 0):], err)
	}
	e.d.SetExecHandler(func(string, docker.ExecConfig) dockertest.ExecResult {
		return dockertest.ExecResult{Stdout: "ok \xff\n", Stderr: "warn\n"}
	})
	if out, err := e.m.Rcon(ctx, 1, "list"); err != nil || out != "ok �\nwarn" {
		t.Fatalf("output = %q, %v", out, err)
	}
}

func TestRconParallelLimit(t *testing.T) {
	e := newEnv(t)
	e.startInstance(t, 1, "survival")
	ctx := context.Background()
	arrived, release := e.d.Hold(dockertest.OpExecStart)
	defer release()
	errs := make(chan error, instances.MaxParallelRcon)
	for range instances.MaxParallelRcon {
		go func() {
			_, err := e.m.Rcon(ctx, 1, "list")
			errs <- err
		}()
	}
	for range instances.MaxParallelRcon {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("exec start not reached")
		}
	}
	if _, err := e.m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("5th parallel command = %v, want ErrBusy", err)
	}
	release()
	for range instances.MaxParallelRcon {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if out, err := e.m.Rcon(ctx, 1, "list"); err != nil || out != "list" {
		t.Fatalf("after the burst = %q, %v (slots must be free again)", out, err)
	}
}

func TestRconTimeout(t *testing.T) {
	e := newEnv(t, func(c *instances.Config) { c.RconTimeout = 200 * time.Millisecond })
	e.startInstance(t, 1, "survival")
	ctx := context.Background()
	_, release := e.d.Hold(dockertest.OpExecStart)
	begin := time.Now()
	_, err := e.m.Rcon(ctx, 1, "list")
	if !errors.Is(err, agentapi.ErrRconUnavailable) || time.Since(begin) > 5*time.Second {
		t.Fatalf("hanging rcon-cli = %v after %v, want ErrRconUnavailable after the timeout", err, time.Since(begin))
	}
	release()
	if _, err := e.m.Rcon(ctx, 1, "list"); err != nil {
		t.Fatalf("slot not released after a timeout: %v", err)
	}
}
```

- [ ] **Step 2: Rot prüfen**

Run: `make check`
Expected: FAIL beim Kompilieren (`e.m.Logs undefined`, `instances.MaxParallelRcon`, `Config.RconCommand`, …).

- [ ] **Step 3: Konfiguration** – `internal/agent/instances/manager.go`

In `Config` nach `StatusTimeout` ergänzen:

```go
	// RconCommand is the program run for console commands; "" means DefaultRconCommand.
	// Only the dockerintegration test changes it (busybox has no rcon-cli).
	RconCommand string
	// RconTimeout bounds one console command; 0 means RconTimeout.
	RconTimeout time.Duration
```

In `Manager` nach `statusTimeout time.Duration` ergänzen:

```go
	rconCmd     string
	rconTimeout time.Duration
	console     consoleState
```

In `New` im Struct-Literal ergänzen:

```go
		rconCmd:       cmp.Or(cfg.RconCommand, DefaultRconCommand),
		rconTimeout:   cmp.Or(cfg.RconTimeout, RconTimeout),
```

- [ ] **Step 4: Implementierung** – `internal/agent/instances/console.go`

```go
package instances

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/docker"
)

const (
	// DefaultRconCommand runs every console command; itzg/minecraft-server ships it and
	// keeps the RCON password it generates for it in /data (spec §2).
	DefaultRconCommand = "rcon-cli"
	// RconTimeout bounds one console command: inspect, exec, output and exit code.
	RconTimeout = 10 * time.Second
	// MaxParallelRcon bounds the console commands running at once per instance.
	MaxParallelRcon = 4

	maxRconErrorRunes = 500
	// maxRawLogLine bounds the bytes kept of one log line (timestamp + text); the rest
	// of a longer line up to its newline is dropped.
	maxRawLogLine    = agentapi.MaxLogLineBytes + 64
	truncatedMark    = "\n[… gekürzt]"
	exitPollInterval = 20 * time.Millisecond
)

// consoleState counts the running console commands per instance; the zero value is ready.
type consoleState struct {
	mu   sync.Mutex
	rcon map[int64]int
}

func (s *consoleState) acquire(id int64) (release func(), ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rcon == nil {
		s.rcon = map[int64]int{}
	}
	if s.rcon[id] >= MaxParallelRcon {
		return nil, false
	}
	s.rcon[id]++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.rcon[id]--
		if s.rcon[id] == 0 {
			delete(s.rcon, id)
		}
	}, true
}

// dockerErr marks errors of an unreachable daemon as agentapi.ErrDockerUnavailable.
func dockerErr(err error) error {
	if errors.Is(err, docker.ErrUnavailable) {
		return fmt.Errorf("%w: %w", agentapi.ErrDockerUnavailable, err)
	}
	return err
}

// consoleContainer inspects mcpanel-mc-<id> for a console call. A missing container, one
// mc-panel does not manage or one of another server is agentapi.ErrNotFound: console
// calls never reach a foreign container.
func (m *Manager) consoleContainer(ctx context.Context, id int64) (docker.ContainerInfo, error) {
	name := ContainerName(id)
	info, err := m.dc.Inspect(ctx, name)
	switch {
	case docker.IsNotFound(err):
		return info, fmt.Errorf("%w: container %s does not exist", agentapi.ErrNotFound, name)
	case err != nil:
		return info, dockerErr(err)
	}
	if err := checkManaged(info, id); err != nil {
		return info, fmt.Errorf("%w: %w", agentapi.ErrNotFound, err)
	}
	if err := checkOwner(info, id, m.expectedName(id)); err != nil {
		return info, fmt.Errorf("%w: %w", agentapi.ErrNotFound, err)
	}
	return info, nil
}

// Logs streams the log of mcpanel-mc-<id> (spec §5.2). Failures before Docker opened the
// stream are returned (ErrInvalid, ErrNotFound, ErrDockerUnavailable, …); then start is
// called once with the container state seen by the inspect, and emit for every line until
// Docker ends the stream (container stopped or removed, or a stopped container's history
// is done: nil), ctx ends (ctx.Err()) or emit fails (its error). It never waits for a
// stopped container to come back; the panel reconnects.
func (m *Manager) Logs(ctx context.Context, req agentapi.LogsRequest, start func(running bool), emit func(agentapi.LogLine) error) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if !m.dockerAvailable(ctx) {
		return agentapi.ErrDockerUnavailable
	}
	info, err := m.consoleContainer(ctx, req.ID)
	if err != nil {
		return err
	}
	opts := docker.LogsOptions{Follow: true, Tail: req.Tail, Since: req.Since}
	if req.Since > 0 {
		opts.Tail = -1 // since wins over tail
	}
	name := ContainerName(req.ID)
	body, err := m.dc.ContainerLogs(ctx, name, opts)
	switch {
	case docker.IsNotFound(err):
		return fmt.Errorf("%w: container %s does not exist", agentapi.ErrNotFound, name)
	case err != nil:
		return dockerErr(err)
	}
	defer func() { _ = body.Close() }()
	start(info.State.Running && !info.State.Restarting)
	err = copyLogLines(body, emit)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

// copyLogLines demultiplexes a log stream with timestamps and emits one LogLine per line.
// Frames need not align with lines: stdout and stderr are reassembled separately.
func copyLogLines(r io.Reader, emit func(agentapi.LogLine) error) error {
	dm := docker.NewDemuxer(r)
	var out, errs lineBuffer
	var lastTS int64
	flush := func(b *lineBuffer, stream string, final bool) error {
		for {
			raw, ok := b.next(final)
			if !ok {
				return nil
			}
			if err := emit(parseLogLine(raw, &lastTS, stream)); err != nil {
				return err
			}
		}
	}
	for {
		f, err := dm.Next()
		if errors.Is(err, io.EOF) {
			if err := flush(&out, agentapi.StreamOut, true); err != nil {
				return err
			}
			return flush(&errs, agentapi.StreamErr, true)
		}
		if err != nil {
			return err
		}
		switch f.Stream {
		case docker.Stdout:
			out.write(f.Data)
			err = flush(&out, agentapi.StreamOut, false)
		case docker.Stderr:
			errs.write(f.Data)
			err = flush(&errs, agentapi.StreamErr, false)
		}
		if err != nil {
			return err
		}
	}
}

// lineBuffer reassembles the lines of one stream. A line longer than maxRawLogLine is
// cut there and the rest up to its newline is dropped.
type lineBuffer struct {
	buf      []byte
	dropping bool // inside the dropped rest of an overlong line
}

func (b *lineBuffer) write(p []byte) {
	if b.dropping {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			return
		}
		b.dropping = false
		p = p[i+1:]
	}
	b.buf = append(b.buf, p...)
}

// next returns the next line without its "\n"; an unterminated line only once it is too
// long or when final is set (end of stream).
func (b *lineBuffer) next(final bool) ([]byte, bool) {
	i := bytes.IndexByte(b.buf, '\n')
	switch {
	case i >= 0 && i <= maxRawLogLine:
		line := b.buf[:i]
		b.buf = b.buf[i+1:]
		return line, true
	case i > maxRawLogLine:
		line := b.buf[:maxRawLogLine]
		b.buf = b.buf[i+1:]
		return line, true
	case len(b.buf) > maxRawLogLine:
		line := b.buf[:maxRawLogLine]
		b.buf, b.dropping = nil, true
		return line, true
	case final && len(b.buf) > 0:
		line := b.buf
		b.buf = nil
		return line, true
	}
	return nil, false
}

// parseLogLine splits "<RFC 3339 timestamp> <text>". A line without a valid timestamp
// keeps the previous one, so the panel's resume position never goes backwards. The text
// loses a trailing "\r", invalid UTF-8 becomes U+FFFD and it is cut to MaxLogLineBytes
// at a rune boundary.
func parseLogLine(raw []byte, lastTS *int64, stream string) agentapi.LogLine {
	text := raw
	if ts, rest, ok := bytes.Cut(raw, []byte{' '}); ok {
		if t, err := time.Parse(time.RFC3339Nano, string(ts)); err == nil {
			*lastTS = t.UnixNano()
			text = rest
		}
	}
	s := strings.ToValidUTF8(string(bytes.TrimRight(text, "\r")), "�")
	return agentapi.LogLine{TS: *lastTS, Stream: stream, Line: truncateUTF8(s, agentapi.MaxLogLineBytes)}
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func firstRunes(s string, n int) string {
	i := 0
	for j := range s {
		if i == n {
			return s[:j]
		}
		i++
	}
	return s
}

// Rcon runs one console command in mcpanel-mc-<id> as `<rcon-cli> <command>` with the
// container user's uid:gid in /data (spec §5.3) and returns its output.
func (m *Manager) Rcon(ctx context.Context, id int64, command string) (string, error) {
	if err := (agentapi.RconRequest{ID: id, Command: command}).Validate(); err != nil {
		return "", err
	}
	if !m.dockerAvailable(ctx) {
		return "", agentapi.ErrDockerUnavailable
	}
	release, ok := m.console.acquire(id)
	if !ok {
		return "", fmt.Errorf("%w: %d console commands of instance %d are running", agentapi.ErrBusy, MaxParallelRcon, id)
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, m.rconTimeout)
	defer cancel()
	// fail turns errors into rcon_unavailable once our own deadline has passed.
	fail := func(err error) error {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%w: %s did not answer within %s", agentapi.ErrRconUnavailable, m.rconCmd, m.rconTimeout)
		}
		return dockerErr(err)
	}

	name := ContainerName(id)
	info, err := m.consoleContainer(ctx, id)
	if errors.Is(err, agentapi.ErrNotFound) {
		return "", err
	}
	if err != nil {
		return "", fail(err)
	}
	if !info.State.Running || info.State.Restarting {
		return "", fmt.Errorf("%w: container %s is %s", agentapi.ErrNotRunning, name, info.State.Status)
	}
	execID, err := m.dc.ExecCreate(ctx, name, docker.ExecConfig{
		Cmd:          []string{m.rconCmd, command},
		User:         strconv.Itoa(m.host.UID) + ":" + strconv.Itoa(m.host.GID),
		WorkingDir:   "/data",
		Env:          []string{"HOME=/data"},
		AttachStdout: true,
		AttachStderr: true,
	})
	switch {
	case docker.IsNotFound(err):
		return "", fmt.Errorf("%w: container %s does not exist", agentapi.ErrNotFound, name)
	case docker.IsConflict(err):
		return "", fmt.Errorf("%w: %w", agentapi.ErrNotRunning, err)
	case err != nil:
		return "", fail(err)
	}
	body, err := m.dc.ExecStart(ctx, execID)
	if err != nil {
		return "", fail(err)
	}
	out, truncated, err := readExecOutput(body, agentapi.MaxRconOutputBytes)
	_ = body.Close()
	if err != nil {
		return "", fail(err)
	}
	if truncated {
		// We stopped reading; the process may still be blocked writing, so its exit code
		// would only arrive with the timeout. The answer itself is complete enough.
		return out, nil
	}
	code, err := m.execExitCode(ctx, execID)
	if err != nil {
		return "", fail(err)
	}
	if code != 0 {
		msg := out
		if strings.TrimSpace(msg) == "" {
			msg = fmt.Sprintf("%s exited with code %d", m.rconCmd, code)
		}
		return "", fmt.Errorf("%w: %s", agentapi.ErrRconUnavailable, firstRunes(msg, maxRconErrorRunes))
	}
	return out, nil
}

// readExecOutput collects stdout and stderr in arrival order, at most limit bytes (then
// truncated is set and the rest is not read). The result is valid UTF-8 without trailing
// line breaks; a truncated output ends with truncatedMark.
func readExecOutput(r io.Reader, limit int) (out string, truncated bool, err error) {
	dm := docker.NewDemuxer(r)
	var buf []byte
	for {
		f, err := dm.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, err
		}
		if f.Stream == docker.Stdin {
			continue
		}
		buf = append(buf, f.Data...)
		if len(buf) > limit {
			truncated = true
			break
		}
	}
	s := truncateUTF8(strings.ToValidUTF8(string(buf), "�"), limit)
	s = strings.TrimRight(s, "\r\n")
	if truncated {
		s += truncatedMark
	}
	return s, truncated, nil
}

// execExitCode waits until Docker reports the exec as finished (the output stream may
// end slightly before the exit code is recorded) and returns the exit code.
func (m *Manager) execExitCode(ctx context.Context, execID string) (int, error) {
	for {
		info, err := m.dc.ExecInspect(ctx, execID)
		if err != nil {
			return 0, err
		}
		if !info.Running && info.ExitCode != nil {
			return *info.ExitCode, nil
		}
		t := time.NewTimer(exitPollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return 0, ctx.Err()
		case <-t.C:
		}
	}
}
```

- [ ] **Step 5: Grün prüfen, committen**

Run: `make check`
Expected: PASS. Danach:

```bash
git add internal/agent/instances
git commit -m "feat(agent): live log and rcon commands in the instance manager

Logs reassembles lines from Docker frames, cuts them to 8 KiB and replaces
invalid UTF-8; Rcon runs rcon-cli as the container user in /data with a
10 s timeout, 64 KiB output and 4 parallel commands per instance. Foreign
or stopped containers never get a logs or exec call (spec 5.2, 5.3).

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI grün (`TestLogs*`, `TestRcon*` PASS, auch mit `-race`).

---

### Task 6: Manager – Spielerlisten (`internal/agent/instances/players.go`)

**Files:**
- Create: `internal/agent/instances/players.go`
- Test: `internal/agent/instances/players_test.go`

**Interfaces:**
- Consumes: V1 (`agentapi.PlayersResponse`, `PlayerOp`, `PlayerEntry`, `PlayerBan`, `ErrNotFound`), `DataDir`, `instancespec.ErrInvalid`
- Produces:
```go
func (m *Manager) Players(ctx context.Context, id int64) (agentapi.PlayersResponse, error)
```

Sicherheitsbegründung (für Reviewer): `instances/<id>` gehört dem Container-Benutzer; ein kompromittierter Server kann darin beliebige Einträge anlegen, aber `instances/` selbst ist root 0700 (TP2), das Verzeichnis `<id>` kann er also nicht ersetzen. `os.Root` verhindert jeden Weg aus dem Verzeichnis heraus, folgt aber Symlinks **innerhalb** des Verzeichnisses (auch mit `O_NOFOLLOW`, siehe `rootOpenFileNolog` in der Go-Quelle). Deshalb zusätzlich: `root.Lstat` muss eine reguläre Datei melden (Symlink, FIFO, Gerät, Verzeichnis → abgelehnt, bevor geöffnet wird), `O_NONBLOCK` verhindert trotzdem jedes Hängen an einer FIFO, und `os.SameFile` zwischen `Lstat` und `fstat` erkennt einen Tausch zwischen beiden Aufrufen.

- [ ] **Step 1: Failing test schreiben** – `internal/agent/instances/players_test.go`

```go
package instances_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instances"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// dataDir creates the data directory of instance id like ensure does.
func (e *env) dataDir(t *testing.T, id int64) string {
	t.Helper()
	dir := instances.DataDir(e.dir, id)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// players calls Players with a deadline: a FIFO must never make it hang.
func (e *env) players(t *testing.T, id int64) (agentapi.PlayersResponse, error) {
	t.Helper()
	type result struct {
		resp agentapi.PlayersResponse
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := e.m.Players(context.Background(), id)
		ch <- result{resp, err}
	}()
	select {
	case r := <-ch:
		return r.resp, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("Players hangs")
		return agentapi.PlayersResponse{}, nil
	}
}

func TestPlayersReadsLists(t *testing.T) {
	e := newEnv(t)
	dir := e.dataDir(t, 1)
	writeFile(t, filepath.Join(dir, "ops.json"),
		`[{"uuid":"069a79f4-44e9-4726-a5be-fca90e38aaf5","name":"Notch","level":4,"bypassesPlayerLimit":false}]`)
	writeFile(t, filepath.Join(dir, "whitelist.json"),
		`[{"uuid":"069a79f4-44e9-4726-a5be-fca90e38aaf5","name":"Notch"},{"uuid":"853c80ef-3c37-49fd-aa49-938b674adae6","name":"jeb_"}]`)
	writeFile(t, filepath.Join(dir, "banned-players.json"),
		`[{"uuid":"61699b2e-d327-4a01-9f1e-0ea8c3f06bc6","name":"Griefer","created":"2026-10-11 09:32:51 +0000","source":"Server","expires":"forever","reason":"Banned by an operator."}]`)
	got, err := e.players(t, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := agentapi.PlayersResponse{
		Ops: []agentapi.PlayerOp{{UUID: "069a79f4-44e9-4726-a5be-fca90e38aaf5", Name: "Notch", Level: 4}},
		Whitelist: []agentapi.PlayerEntry{
			{UUID: "069a79f4-44e9-4726-a5be-fca90e38aaf5", Name: "Notch"},
			{UUID: "853c80ef-3c37-49fd-aa49-938b674adae6", Name: "jeb_"},
		},
		Bans: []agentapi.PlayerBan{{
			UUID: "61699b2e-d327-4a01-9f1e-0ea8c3f06bc6", Name: "Griefer", Reason: "Banned by an operator.",
			Source: "Server", Created: "2026-10-11 09:32:51 +0000", Expires: "forever",
		}},
		Errors: []string{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Players =\n%+v\nwant\n%+v", got, want)
	}
}

func TestPlayersMissingFilesAreEmpty(t *testing.T) {
	e := newEnv(t)
	e.dataDir(t, 1)
	got, err := e.players(t, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(got)
	if string(b) != `{"ops":[],"whitelist":[],"bans":[],"errors":[]}` {
		t.Fatalf("Players without files = %s", b)
	}
}

func TestPlayersMissingDataDir(t *testing.T) {
	e := newEnv(t)
	if _, err := e.players(t, 1); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("missing data directory = %v, want ErrNotFound", err)
	}
	if _, err := e.m.Players(context.Background(), 0); !errors.Is(err, instancespec.ErrInvalid) {
		t.Fatalf("id 0 = %v, want ErrInvalid", err)
	}
}

func TestPlayersRefusesUntrustedFiles(t *testing.T) {
	e := newEnv(t)
	outside := filepath.Join(t.TempDir(), "secret.json")
	writeFile(t, outside, `[{"uuid":"x","name":"LEAKED","level":4}]`)
	cases := []struct {
		name  string
		setup func(t *testing.T, path string)
	}{
		{"absolute symlink to a file outside", func(t *testing.T, path string) {
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink to /etc/passwd", func(t *testing.T, path string) {
			if err := os.Symlink("/etc/passwd", path); err != nil {
				t.Fatal(err)
			}
		}},
		{"relative symlink leaving the directory", func(t *testing.T, path string) {
			rel, err := filepath.Rel(filepath.Dir(path), outside)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(rel, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink inside the directory", func(t *testing.T, path string) {
			writeFile(t, filepath.Join(filepath.Dir(path), "other.json"), `[{"uuid":"x","name":"LEAKED","level":4}]`)
			if err := os.Symlink("other.json", path); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", func(t *testing.T, path string) {
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"fifo", func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Skipf("mkfifo: %v", err)
			}
		}},
		{"larger than 1 MiB", func(t *testing.T, path string) {
			writeFile(t, path, "["+strings.Repeat(" ", 1<<20)+"]")
		}},
		{"not a list", func(t *testing.T, path string) { writeFile(t, path, `{"name":"LEAKED"}`) }},
		{"broken JSON", func(t *testing.T, path string) { writeFile(t, path, `[{"name":`) }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := int64(i + 1)
			tc.setup(t, filepath.Join(e.dataDir(t, id), "ops.json"))
			got, err := e.players(t, id)
			if err != nil {
				t.Fatalf("Players = %v, want an answer with errors", err)
			}
			if len(got.Ops) != 0 || !reflect.DeepEqual(got.Errors, []string{"ops.json"}) {
				t.Fatalf("Players = %+v, want no ops and errors [ops.json]", got)
			}
			if strings.Contains(fmt.Sprint(got), "LEAKED") || strings.Contains(fmt.Sprint(got), "root") {
				t.Fatalf("content outside the data directory leaked: %+v", got)
			}
		})
	}
	if !strings.Contains(e.logs.String(), "player list unreadable") || strings.Contains(e.logs.String(), "LEAKED") {
		t.Fatalf("log must name the file, never its content:\n%s", e.logs.String())
	}
}

func TestPlayersRefusesSymlinkedDataDir(t *testing.T) {
	e := newEnv(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "ops.json"), `[{"uuid":"x","name":"LEAKED","level":4}]`)
	if err := os.MkdirAll(filepath.Join(e.dir, "instances"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, instances.DataDir(e.dir, 9)); err != nil {
		t.Fatal(err)
	}
	got, err := e.players(t, 9)
	if err == nil || strings.Contains(fmt.Sprint(got), "LEAKED") {
		t.Fatalf("symlinked data directory = %+v, %v; want an error and nothing read", got, err)
	}
}

func TestPlayersClipsEntries(t *testing.T) {
	e := newEnv(t)
	dir := e.dataDir(t, 1)
	entries := make([]map[string]string, 1500)
	for i := range entries {
		entries[i] = map[string]string{"uuid": fmt.Sprintf("u%d", i), "name": fmt.Sprintf("p%d", i)}
	}
	entries[0]["name"] = strings.Repeat("ä", 300)
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "whitelist.json"), string(b))
	got, err := e.players(t, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Whitelist) != 1000 || utf8.RuneCountInString(got.Whitelist[0].Name) != 256 || got.Whitelist[999].Name != "p999" {
		t.Fatalf("whitelist: %d entries, first name %d runes", len(got.Whitelist), utf8.RuneCountInString(got.Whitelist[0].Name))
	}
}
```

- [ ] **Step 2: Rot prüfen**

Run: `make check`
Expected: FAIL beim Kompilieren (`e.m.Players undefined`).

- [ ] **Step 3: Implementierung** – `internal/agent/instances/players.go`

```go
package instances

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	maxPlayerFileBytes = 1 << 20
	maxPlayerEntries   = 1000
	maxPlayerTextRunes = 256

	opsFile       = "ops.json"
	whitelistFile = "whitelist.json"
	bansFile      = "banned-players.json"
)

type opEntry struct {
	UUID  string `json:"uuid"`
	Name  string `json:"name"`
	Level int    `json:"level"`
}

type whitelistEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

type banEntry struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Reason  string `json:"reason"`
	Source  string `json:"source"`
	Created string `json:"created"`
	Expires string `json:"expires"`
}

// Players reads ops.json, whitelist.json and banned-players.json of instance id (spec
// §5.4); it works with a stopped or missing container. The files belong to the container
// user and are untrusted: an unreadable one (symlink, not a regular file, above 1 MiB,
// no JSON list) is named in Errors and its list stays empty; a missing one is an empty
// list. agentapi.ErrNotFound if the data directory does not exist.
func (m *Manager) Players(_ context.Context, id int64) (agentapi.PlayersResponse, error) {
	resp := agentapi.PlayersResponse{
		Ops: []agentapi.PlayerOp{}, Whitelist: []agentapi.PlayerEntry{}, Bans: []agentapi.PlayerBan{}, Errors: []string{},
	}
	if id <= 0 {
		return resp, fmt.Errorf("%w: players: id must be > 0", instancespec.ErrInvalid)
	}
	dir := DataDir(m.host.InstancesDir, id)
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return resp, fmt.Errorf("%w: data directory of instance %d does not exist", agentapi.ErrNotFound, id)
	case err != nil:
		return resp, err
	case !fi.IsDir(): // also a symlink: Lstat does not follow it
		return resp, fmt.Errorf("data directory %s is not a directory", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return resp, err
	}
	defer func() { _ = root.Close() }()

	var ops []opEntry
	if m.readPlayerFile(root, id, opsFile, &ops, &resp) {
		for _, o := range ops[:min(len(ops), maxPlayerEntries)] {
			resp.Ops = append(resp.Ops, agentapi.PlayerOp{UUID: clipText(o.UUID), Name: clipText(o.Name), Level: o.Level})
		}
	}
	var wl []whitelistEntry
	if m.readPlayerFile(root, id, whitelistFile, &wl, &resp) {
		for _, w := range wl[:min(len(wl), maxPlayerEntries)] {
			resp.Whitelist = append(resp.Whitelist, agentapi.PlayerEntry{UUID: clipText(w.UUID), Name: clipText(w.Name)})
		}
	}
	var bans []banEntry
	if m.readPlayerFile(root, id, bansFile, &bans, &resp) {
		for _, b := range bans[:min(len(bans), maxPlayerEntries)] {
			resp.Bans = append(resp.Bans, agentapi.PlayerBan{
				UUID: clipText(b.UUID), Name: clipText(b.Name), Reason: clipText(b.Reason),
				Source: clipText(b.Source), Created: clipText(b.Created), Expires: clipText(b.Expires),
			})
		}
	}
	return resp, nil
}

// readPlayerFile decodes the JSON list name into dst. It returns false when there is
// nothing to use; an unreadable file is logged (never its content) and added to Errors.
func (m *Manager) readPlayerFile(root *os.Root, id int64, name string, dst any, resp *agentapi.PlayersResponse) bool {
	data, err := readUntrustedFile(root, name, maxPlayerFileBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err == nil {
		err = json.Unmarshal(data, dst) // a JSON object instead of a list fails here
	}
	if err != nil {
		m.log.Warn("player list unreadable", "id", id, "file", name, "err", err)
		resp.Errors = append(resp.Errors, name)
		return false
	}
	return true
}

// readUntrustedFile reads name below root: only a regular file (no symlink, not even one
// pointing inside root) of at most limit bytes. O_NONBLOCK keeps a FIFO swapped in after
// the Lstat from blocking the open; SameFile catches any swap between Lstat and open.
func readUntrustedFile(root *os.Root, name string, limit int64) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (%v)", name, before.Mode().Type())
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("%s changed while it was opened", name)
	}
	if after.Size() > limit {
		return nil, fmt.Errorf("%s has %d bytes, more than %d", name, after.Size(), limit)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s grew beyond %d bytes", name, limit)
	}
	return data, nil
}

// clipText cuts s to maxPlayerTextRunes runes (json.Unmarshal already replaced invalid UTF-8).
func clipText(s string) string { return firstRunes(s, maxPlayerTextRunes) }
```

Falls gosec bei `root.OpenFile` G304 meldet: `//nolint:gosec // G304: fixed file name inside the data directory; os.Root prevents leaving it` an die Zeile.

- [ ] **Step 4: Grün prüfen, committen**

Run: `make check`
Expected: PASS. Danach:

```bash
git add internal/agent/instances/players.go internal/agent/instances/players_test.go
git commit -m "feat(agent): read player lists from the data directory without trusting it

Players reads ops.json, whitelist.json and banned-players.json through
os.Root and refuses symlinks, FIFOs, directories, files above 1 MiB and
broken JSON (named in errors). Strings are cut to 256 runes, lists to
1000 entries (spec 5.4).

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI grün (`TestPlayers*` PASS auf Linux; die FIFO-Fälle laufen dort ohne Skip).

---

### Task 7: Endpunkte `/v1/instances/logs|rcon|players` und Verdrahtung

**Files:**
- Create: `internal/agent/server_console.go`, `internal/agent/server_export_test.go`
- Modify: `internal/agent/server.go` (Struct, `NewServer`), `cmd/agent/main.go` (eine Zeile)
- Test: `internal/agent/server_console_test.go`

**Interfaces:**
- Consumes: V1 (`agentapi.PathInstanceLogs/Rcon/Players`, Request-Typen und `Validate`, Fehlercodes, `agent.Client.Logs/Rcon/Players` aus Phase A); Aufgaben 5 und 6 (`*instances.Manager` erfüllt `Console`)
- Produces:
```go
package agent
const MaxLogStreams = 16
type Console interface {
	Logs(ctx context.Context, req agentapi.LogsRequest, start func(running bool), emit func(agentapi.LogLine) error) error
	Rcon(ctx context.Context, id int64, command string) (string, error)
	Players(ctx context.Context, id int64) (agentapi.PlayersResponse, error)
}
func WithConsole(c Console) Option
// nur in Tests (server_export_test.go):
func WithReadTimeout(d time.Duration) Option
```

Hintergrund (für Reviewer): `net/http` liest nach dem Body im Hintergrund weiter, um ein Schließen der Verbindung zu erkennen. Läuft dabei die per `ReadTimeout` gesetzte Lesefrist ab (agent: 5 s ab Request-Beginn), bricht der Server den **Request-Kontext** ab (`connReader.handleReadErrorLocked` → `cancelCtx`). Ein Log-Stream oder ein 10-s-RCON-Befehl würde so nach 5 s abgebrochen. Darum heben `logs` und `rcon` die Lesefrist nach dem Dekodieren auf; das Schließen der Verbindung durch das panel erkennt der Hintergrund-Read weiterhin. `WriteTimeout` (10 s) ersetzen sie durch eigene Fristen. `Server.Shutdown` wartet auf laufende Requests – `RegisterOnShutdown` beendet die Streams deshalb aktiv.

- [ ] **Step 1: Test-Option** – `internal/agent/server_export_test.go`

```go
package agent

import "time"

// WithReadTimeout overrides the server's ReadTimeout (tests of long-running requests).
func WithReadTimeout(d time.Duration) Option { return func(s *Server) { s.readTimeout = d } }
```

- [ ] **Step 2: Failing test schreiben** – `internal/agent/server_console_test.go`

```go
package agent_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

type stubConsoleConfig struct {
	running    bool // passed to start
	lines      []agentapi.LogLine
	logsErr    error         // returned instead of opening the stream
	pause      time.Duration // before the second line
	block      bool          // after the lines: wait until ctx ends
	rconOut    string
	rconErr    error
	rconDelay  time.Duration
	players    agentapi.PlayersResponse
	playersErr error
}

// stubConsole is a configurable Console; set changes it between requests.
type stubConsole struct {
	mu    sync.Mutex
	cfg   stubConsoleConfig
	calls []string
	reqs  []agentapi.LogsRequest
}

var _ agent.Console = (*stubConsole)(nil)

func newStubConsole(cfg stubConsoleConfig) *stubConsole { return &stubConsole{cfg: cfg} }

func (s *stubConsole) set(f func(*stubConsoleConfig)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.cfg)
}

func (s *stubConsole) enter(call string) stubConsoleConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
	return s.cfg
}

func (s *stubConsole) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *stubConsole) logsRequests() []agentapi.LogsRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agentapi.LogsRequest(nil), s.reqs...)
}

func waitCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *stubConsole) Logs(ctx context.Context, req agentapi.LogsRequest, start func(running bool), emit func(agentapi.LogLine) error) error {
	cfg := s.enter("logs")
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	if cfg.logsErr != nil {
		return cfg.logsErr
	}
	start(cfg.running)
	for i, l := range cfg.lines {
		if i == 1 && cfg.pause > 0 {
			if err := waitCtx(ctx, cfg.pause); err != nil {
				return err
			}
		}
		if err := emit(l); err != nil {
			return err
		}
	}
	if cfg.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func (s *stubConsole) Rcon(ctx context.Context, _ int64, command string) (string, error) {
	cfg := s.enter("rcon " + command)
	if cfg.rconDelay > 0 {
		if err := waitCtx(ctx, cfg.rconDelay); err != nil {
			return "", err
		}
	}
	return cfg.rconOut, cfg.rconErr
}

func (s *stubConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	cfg := s.enter("players")
	return cfg.players, cfg.playersErr
}

func newConsoleServer(c agent.Console) *agent.Server {
	fw, _ := newNoop()
	return agent.NewServer(fw, "test", testLogger(io.Discard), agent.WithConsole(c))
}

// startConsoleServer serves on a real Unix socket (deadlines, background reads, shutdown).
func startConsoleServer(t *testing.T, c agent.Console, opts ...agent.Option) (string, *agent.Server) {
	t.Helper()
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(io.Discard), append([]agent.Option{agent.WithConsole(c)}, opts...)...)
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return path, srv
}

func unixHTTPClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}}}
}

func postLogs(ctx context.Context, hc *http.Client, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://agent"+agentapi.PathInstanceLogs, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", jsonCT)
	return hc.Do(req)
}

func ndjson(t *testing.T, lines ...agentapi.LogLine) string {
	t.Helper()
	var b strings.Builder
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestServerConsoleEndpoints(t *testing.T) {
	lines := []agentapi.LogLine{
		{TS: 1760097600000000000, Stream: agentapi.StreamOut, Line: "[12:00:00 INFO]: Done (3.2s)!"},
		{TS: 1760097601000000000, Stream: agentapi.StreamErr, Line: "\x1b[33mWARN\x1b[0m slow"},
	}
	players := agentapi.PlayersResponse{
		Ops:       []agentapi.PlayerOp{{UUID: "069a79f4-44e9-4726-a5be-fca90e38aaf5", Name: "Notch", Level: 4}},
		Whitelist: []agentapi.PlayerEntry{},
		Bans: []agentapi.PlayerBan{{UUID: "u2", Name: "Griefer", Reason: "x", Source: "Server",
			Created: "2026-10-11 09:32:51 +0000", Expires: "forever"}},
		Errors: []string{},
	}
	st := newStubConsole(stubConsoleConfig{running: true, lines: lines, rconOut: "There are 0 of a max of 20 players online: ", players: players})
	srv := newConsoleServer(st)

	rec := serve(srv, http.MethodPost, agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":2,"since":0}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-ndjson" ||
		rec.Header().Get(agentapi.HeaderContainerRunning) != "true" || rec.Body.String() != ndjson(t, lines...) {
		t.Fatalf("logs: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if reqs := st.logsRequests(); len(reqs) != 1 || reqs[0] != (agentapi.LogsRequest{ID: 1, Tail: 2}) {
		t.Fatalf("logs requests = %+v", reqs)
	}
	rec = serve(srv, http.MethodPost, agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"list"}`)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"output":"There are 0 of a max of 20 players online: "}`+"\n" {
		t.Fatalf("rcon: %d %s", rec.Code, rec.Body)
	}
	rec = serve(srv, http.MethodPost, agentapi.PathInstancePlayers, jsonCT, `{"id":1}`)
	want := `{"ops":[{"uuid":"069a79f4-44e9-4726-a5be-fca90e38aaf5","name":"Notch","level":4}],"whitelist":[],` +
		`"bans":[{"uuid":"u2","name":"Griefer","reason":"x","source":"Server","created":"2026-10-11 09:32:51 +0000","expires":"forever"}],"errors":[]}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("players: %d %s", rec.Code, rec.Body)
	}
}

func TestServerLogsOfStoppedContainer(t *testing.T) {
	// A stopped container: start(false), no lines, the stream ends by itself.
	rec := serve(newConsoleServer(newStubConsole(stubConsoleConfig{})), http.MethodPost, agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":0,"since":0}`)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-ndjson" ||
		rec.Header().Get(agentapi.HeaderContainerRunning) != "false" || rec.Body.Len() != 0 {
		t.Fatalf("empty log: %d %v %q", rec.Code, rec.Header(), rec.Body)
	}
}

func TestServerRejectsInvalidConsoleRequests(t *testing.T) {
	cases := []struct {
		name, method, path, ct, body string
		want                         int
	}{
		{"logs tail -1", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":-1,"since":0}`, 400},
		{"logs tail 1001", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":1001,"since":0}`, 400},
		{"logs since -1", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":10,"since":-1}`, 400},
		{"logs id 0", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":0,"tail":10,"since":0}`, 400},
		{"logs unknown field", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":10,"since":0,"follow":false}`, 400},
		{"logs trailing data", "POST", agentapi.PathInstanceLogs, jsonCT, `{"id":1,"tail":10,"since":0}{}`, 400},
		{"logs wrong content type", "POST", agentapi.PathInstanceLogs, "text/plain", `{"id":1,"tail":10,"since":0}`, 415},
		{"logs GET", "GET", agentapi.PathInstanceLogs, "", "", 405},
		{"rcon empty", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":""}`, 400},
		{"rcon spaces", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"   "}`, 400},
		{"rcon newline", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"say a\nop evil"}`, 400},
		{"rcon carriage return", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"say a\rop evil"}`, 400},
		{"rcon tab", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"say\ta"}`, 400},
		{"rcon nul", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"say \u0000"}`, 400},
		{"rcon del", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"say \u007f"}`, 400},
		{"rcon 1001 bytes", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"` + strings.Repeat("a", 1001) + `"}`, 400},
		{"rcon id 0", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":0,"command":"list"}`, 400},
		{"rcon unknown field", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"list","user":"root"}`, 400},
		{"rcon too large", "POST", agentapi.PathInstanceRcon, jsonCT, `{"id":1,"command":"` + strings.Repeat("a", 17<<10) + `"}`, 413},
		{"players id 0", "POST", agentapi.PathInstancePlayers, jsonCT, `{"id":0}`, 400},
		{"players negative", "POST", agentapi.PathInstancePlayers, jsonCT, `{"id":-3}`, 400},
		{"players unknown field", "POST", agentapi.PathInstancePlayers, jsonCT, `{"id":1,"file":"/etc/shadow"}`, 400},
	}
	st := newStubConsole(stubConsoleConfig{})
	srv := newConsoleServer(st)
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
	if n := st.callCount(); n != 0 {
		t.Fatalf("invalid requests reached the console backend (%d calls)", n)
	}
}

func TestServerConsoleErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: container mcpanel-mc-1 does not exist", agentapi.ErrNotFound), http.StatusNotFound, agentapi.ErrorCodeNotFound},
		{agentapi.ErrNotRunning, http.StatusConflict, agentapi.ErrorCodeNotRunning},
		{agentapi.ErrBusy, http.StatusTooManyRequests, agentapi.ErrorCodeBusy},
		{fmt.Errorf("%w: Failed to connect to RCON", agentapi.ErrRconUnavailable), http.StatusServiceUnavailable, agentapi.ErrorCodeRconUnavailable},
		{agentapi.ErrDockerUnavailable, http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable},
		{fmt.Errorf("%w: id", instancespec.ErrInvalid), http.StatusBadRequest, ""},
		{errors.New("disk on fire at /srv/mc-panel"), http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		st := newStubConsole(stubConsoleConfig{logsErr: tc.err, rconErr: tc.err, playersErr: tc.err})
		srv := newConsoleServer(st)
		for _, ep := range []struct{ path, body string }{
			{agentapi.PathInstanceLogs, `{"id":1,"tail":10,"since":0}`},
			{agentapi.PathInstanceRcon, `{"id":1,"command":"list"}`},
			{agentapi.PathInstancePlayers, `{"id":1}`},
		} {
			rec := serve(srv, http.MethodPost, ep.path, jsonCT, ep.body)
			var er agentapi.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != tc.status || er.Code != tc.code {
				t.Errorf("%s with %v: %d %s", ep.path, tc.err, rec.Code, rec.Body)
			}
			if tc.status == http.StatusInternalServerError && strings.Contains(rec.Body.String(), "/srv") {
				t.Errorf("%s: internal error leaked: %s", ep.path, rec.Body)
			}
			if tc.code == agentapi.ErrorCodeRconUnavailable && !strings.Contains(er.Error, "Failed to connect") {
				t.Errorf("%s: rcon output missing in %q", ep.path, er.Error)
			}
		}
	}
}

func TestServerWithoutConsoleBackend(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	for _, ep := range []struct{ path, body string }{
		{agentapi.PathInstanceLogs, `{"id":1,"tail":10,"since":0}`},
		{agentapi.PathInstanceRcon, `{"id":1,"command":"list"}`},
		{agentapi.PathInstancePlayers, `{"id":1}`},
	} {
		rec := serve(srv, http.MethodPost, ep.path, jsonCT, ep.body)
		var er agentapi.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || rec.Code != http.StatusServiceUnavailable || er.Code != agentapi.ErrorCodeDockerUnavailable {
			t.Errorf("%s without backend: %d %s", ep.path, rec.Code, rec.Body)
		}
	}
}

func TestServerLogStreamLimit(t *testing.T) {
	st := newStubConsole(stubConsoleConfig{running: true, lines: []agentapi.LogLine{{TS: 1, Stream: agentapi.StreamOut, Line: "hi"}}, block: true})
	path, _ := startConsoleServer(t, st)
	hc := unixHTTPClient(path)
	const body = `{"id":1,"tail":10,"since":0}`
	var cancels []context.CancelFunc
	t.Cleanup(func() {
		for _, c := range cancels {
			c()
		}
	})
	for i := range agent.MaxLogStreams {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		resp, err := postLogs(ctx, hc, body)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: %v %v", i, resp, err)
		}
		if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
			t.Fatalf("stream %d: first line: %v", i, err)
		}
	}
	resp, err := postLogs(context.Background(), hc, body)
	if err != nil {
		t.Fatal(err)
	}
	var er agentapi.ErrorResponse
	_ = json.NewDecoder(resp.Body).Decode(&er)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests || er.Code != agentapi.ErrorCodeBusy {
		t.Fatalf("stream %d: %d %+v, want 429 busy", agent.MaxLogStreams+1, resp.StatusCode, er)
	}

	cancels[0]() // the panel went away: its slot must be freed
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, cancel := context.WithCancel(context.Background())
		resp, err := postLogs(ctx, hc, body)
		ok := err == nil && resp.StatusCode == http.StatusOK
		if resp != nil {
			_ = resp.Body.Close()
		}
		cancel()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the slot of a cancelled stream was not released")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestServerLongRequestsOutliveReadTimeout(t *testing.T) {
	st := newStubConsole(stubConsoleConfig{
		running: true,
		lines:   []agentapi.LogLine{{TS: 1, Stream: agentapi.StreamOut, Line: "one"}, {TS: 2, Stream: agentapi.StreamOut, Line: "two"}},
		pause: 600 * time.Millisecond, rconOut: "pong", rconDelay: 600 * time.Millisecond,
	})
	path, _ := startConsoleServer(t, st, agent.WithReadTimeout(200*time.Millisecond))
	c := agent.NewClient(path)
	ctx := context.Background()
	stream, err := c.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if !stream.Running() {
		t.Fatal("X-Container-Running did not reach the client")
	}
	for _, want := range []string{"one", "two"} {
		l, err := stream.Next()
		if err != nil || l.Line != want {
			t.Fatalf("line = %+v, %v; want %q (the stream must outlive ReadTimeout)", l, err, want)
		}
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("end of stream = %v, want io.EOF", err)
	}
	if out, err := c.Rcon(ctx, 1, "ping"); err != nil || out != "pong" {
		t.Fatalf("slow rcon = %q, %v (must outlive ReadTimeout)", out, err)
	}
}

func TestServerShutdownEndsLogStreams(t *testing.T) {
	st := newStubConsole(stubConsoleConfig{lines: []agentapi.LogLine{{TS: 1, Stream: agentapi.StreamOut, Line: "hi"}}, block: true})
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	srv := newConsoleServer(st)
	go func() { _ = srv.Serve(l) }()
	stream, err := agent.NewClient(path).Logs(context.Background(), agentapi.LogsRequest{ID: 1, Tail: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if _, err := stream.Next(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown with an open log stream = %v", err)
	}
	if _, err := stream.Next(); err == nil {
		t.Fatal("log stream still open after shutdown")
	}
}

func TestClientConsoleEndToEnd(t *testing.T) {
	players := agentapi.PlayersResponse{
		Ops:       []agentapi.PlayerOp{{UUID: "u1", Name: "Notch", Level: 4}},
		Whitelist: []agentapi.PlayerEntry{}, Bans: []agentapi.PlayerBan{}, Errors: []string{"whitelist.json"},
	}
	st := newStubConsole(stubConsoleConfig{players: players})
	path, _ := startConsoleServer(t, st)
	c := agent.NewClient(path)
	ctx := context.Background()
	if got, err := c.Players(ctx, 1); err != nil || !reflect.DeepEqual(got, players) {
		t.Fatalf("Players = %+v, %v", got, err)
	}
	for _, tc := range []struct{ err, want error }{
		{agentapi.ErrNotFound, agentapi.ErrNotFound},
		{agentapi.ErrNotRunning, agentapi.ErrNotRunning},
		{fmt.Errorf("%w: connection refused", agentapi.ErrRconUnavailable), agentapi.ErrRconUnavailable},
		{agentapi.ErrBusy, agentapi.ErrBusy},
		{agentapi.ErrDockerUnavailable, agentapi.ErrDockerUnavailable},
	} {
		st.set(func(c *stubConsoleConfig) { c.rconErr = tc.err })
		if _, err := c.Rcon(ctx, 1, "list"); !errors.Is(err, tc.want) {
			t.Errorf("Rcon with %v = %v, want %v through the real client", tc.err, err, tc.want)
		}
	}
	st.set(func(c *stubConsoleConfig) { c.logsErr = agentapi.ErrNotFound })
	if _, err := c.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 10}); !errors.Is(err, agentapi.ErrNotFound) {
		t.Fatalf("Logs of a missing container = %v, want ErrNotFound before any line", err)
	}
}
```

- [ ] **Step 3: Rot prüfen**

Run: `make check`
Expected: FAIL beim Kompilieren (`agent.WithConsole`, `agent.Console`, `agent.MaxLogStreams`, `s.readTimeout` undefined).

- [ ] **Step 4: Server erweitern** – `internal/agent/server.go`

Struct und `NewServer` ersetzen durch:

```go
// Server serves the agentapi endpoints. It implements http.Handler; Serve
// adds the peer UID to each request context and applies conservative timeouts.
type Server struct {
	fw      agentapi.Firewall
	inst    Instances
	console Console
	version string
	log     *slog.Logger
	routes  map[string]route
	srv     *http.Server

	streams     chan struct{}   // one token per open log stream (MaxLogStreams)
	streamCtx   context.Context // cancelled by Shutdown: ends all log streams
	stopStreams context.CancelFunc

	readTimeout, writeTimeout time.Duration
}

func NewServer(fw agentapi.Firewall, version string, log *slog.Logger, opts ...Option) *Server {
	s := &Server{
		fw: fw, inst: noInstances{}, console: noConsole{}, version: version, log: log,
		streams: make(chan struct{}, MaxLogStreams), readTimeout: 5 * time.Second, writeTimeout: 10 * time.Second,
	}
	for _, o := range opts {
		o(s)
	}
	// Shutdown waits for running requests, and a log stream only ends with its context.
	s.streamCtx, s.stopStreams = context.WithCancel(context.Background())
	s.routes = map[string]route{
		agentapi.PathHealth:          {method: http.MethodGet, handler: s.health},
		agentapi.PathBan:             {method: http.MethodPost, handler: s.ban},
		agentapi.PathUnban:           {method: http.MethodPost, handler: s.unban},
		agentapi.PathBans:            {method: http.MethodGet, handler: s.bans},
		agentapi.PathInstances:       {method: http.MethodGet, handler: s.instances},
		agentapi.PathInstanceEnsure:  {method: http.MethodPost, handler: s.ensure},
		agentapi.PathInstancePower:   {method: http.MethodPost, handler: s.power},
		agentapi.PathInstanceRemove:  {method: http.MethodPost, handler: s.remove},
		agentapi.PathInstanceLogs:    {method: http.MethodPost, handler: s.logs},
		agentapi.PathInstanceRcon:    {method: http.MethodPost, handler: s.rcon},
		agentapi.PathInstancePlayers: {method: http.MethodPost, handler: s.players},
	}
	s.srv = &http.Server{
		Handler:           s,
		ConnContext:       peercred.ConnContext,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       s.readTimeout,
		WriteTimeout:      s.writeTimeout,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	s.srv.RegisterOnShutdown(s.stopStreams)
	return s
}
```

- [ ] **Step 5: Endpunkte** – `internal/agent/server_console.go`

```go
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

const (
	// MaxLogStreams bounds the log streams open at once (spec §5.2).
	MaxLogStreams = 16
	// streamWriteTimeout is the deadline of every write of a log stream (spec §5.2).
	streamWriteTimeout = 30 * time.Second
	// rconWriteTimeout covers one console command (instances.RconTimeout = 10 s) plus margin.
	rconWriteTimeout = 20 * time.Second
)

// Console is the console backend of the server (*instances.Manager).
type Console interface {
	// Logs validates req, inspects the container, opens its log, calls start exactly once
	// before the first line with the container state seen when the stream opened, and then
	// emit for every line until the log ends (nil), ctx ends or emit fails. A stopped
	// container delivers its history and the stream ends.
	Logs(ctx context.Context, req agentapi.LogsRequest, start func(running bool), emit func(agentapi.LogLine) error) error
	Rcon(ctx context.Context, id int64, command string) (string, error)
	Players(ctx context.Context, id int64) (agentapi.PlayersResponse, error)
}

// WithConsole enables the console endpoints. Without it they answer as if Docker were
// unavailable.
func WithConsole(c Console) Option { return func(s *Server) { s.console = c } }

// longRequest lifts the server-wide deadlines for a request that legitimately runs longer
// than ReadTimeout/WriteTimeout. The read deadline must go: once the body is consumed,
// net/http keeps reading in the background to notice a closed connection and cancels the
// request context when that read times out. write > 0 sets a new write deadline.
// Deadlines are I/O deadlines on the real socket (time.Now, not the business clock);
// httptest recorders do not support them, which is ignored.
func longRequest(w http.ResponseWriter, write time.Duration) *http.ResponseController {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	if write > 0 {
		_ = rc.SetWriteDeadline(time.Now().Add(write))
	}
	return rc
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	var req agentapi.LogsRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if err := req.Validate(); err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		s.rejectCode(w, r, http.StatusTooManyRequests, agentapi.ErrorCodeBusy,
			fmt.Errorf("%w: %d log streams are open", agentapi.ErrBusy, MaxLogStreams))
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(s.streamCtx, cancel) // Shutdown ends the stream
	defer stop()

	rc := longRequest(w, 0)
	enc := json.NewEncoder(w)
	started := false
	start := func(running bool) {
		if started {
			return
		}
		started = true
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set(agentapi.HeaderContainerRunning, strconv.FormatBool(running))
		w.WriteHeader(http.StatusOK)
		_ = rc.Flush()
	}
	emit := func(line agentapi.LogLine) error {
		_ = rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
		if err := enc.Encode(line); err != nil {
			return err
		}
		return rc.Flush()
	}
	err := s.console.Logs(ctx, req, start, emit)
	switch {
	case started:
		if err != nil && ctx.Err() == nil {
			s.log.Warn("log stream ended with an error", "id", req.ID, "err", err, "peer_uid", peerUID(r))
		}
	case err == nil:
		start(false) // a backend that returned without opening a stream: an empty log
	default:
		s.consoleError(w, r, "logs", req.ID, err)
	}
}

func (s *Server) rcon(w http.ResponseWriter, r *http.Request) {
	var req agentapi.RconRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if err := req.Validate(); err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	longRequest(w, rconWriteTimeout)
	out, err := s.console.Rcon(r.Context(), req.ID, req.Command)
	if err != nil {
		s.consoleError(w, r, "rcon", req.ID, err)
		return
	}
	// The panel audits the command text; the agent logs only that one ran.
	s.log.Info("console command", "id", req.ID, "bytes", len(req.Command), "peer_uid", peerUID(r))
	writeJSON(w, http.StatusOK, agentapi.RconResponse{Output: out})
}

func (s *Server) players(w http.ResponseWriter, r *http.Request) {
	var req agentapi.PlayersRequest
	if !s.decode(w, r, &req, agentapi.MaxInstanceRequestBytes) {
		return
	}
	if err := req.Validate(); err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	resp, err := s.console.Players(r.Context(), req.ID)
	if err != nil {
		s.consoleError(w, r, "players", req.ID, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// consoleError maps console errors to status and code; anything unknown becomes a generic
// 500 with the details only in the log.
func (s *Server) consoleError(w http.ResponseWriter, r *http.Request, op string, id int64, err error) {
	switch {
	case errors.Is(err, instancespec.ErrInvalid):
		s.reject(w, r, http.StatusBadRequest, err)
	case errors.Is(err, agentapi.ErrNotFound):
		s.rejectCode(w, r, http.StatusNotFound, agentapi.ErrorCodeNotFound, err)
	case errors.Is(err, agentapi.ErrNotRunning):
		s.rejectCode(w, r, http.StatusConflict, agentapi.ErrorCodeNotRunning, err)
	case errors.Is(err, agentapi.ErrBusy):
		s.rejectCode(w, r, http.StatusTooManyRequests, agentapi.ErrorCodeBusy, err)
	case errors.Is(err, agentapi.ErrRconUnavailable):
		s.rejectCode(w, r, http.StatusServiceUnavailable, agentapi.ErrorCodeRconUnavailable, err)
	case errors.Is(err, agentapi.ErrDockerUnavailable):
		s.rejectCode(w, r, http.StatusServiceUnavailable, agentapi.ErrorCodeDockerUnavailable, err)
	default:
		s.log.Error("console operation failed", "op", op, "id", id, "err", err, "peer_uid", peerUID(r))
		writeJSON(w, http.StatusInternalServerError, agentapi.ErrorResponse{Error: "console operation failed"})
	}
}

// noConsole is the backend of a server without WithConsole (dev without Docker).
type noConsole struct{}

func (noConsole) Logs(context.Context, agentapi.LogsRequest, func(bool), func(agentapi.LogLine) error) error {
	return agentapi.ErrDockerUnavailable
}

func (noConsole) Rcon(context.Context, int64, string) (string, error) {
	return "", agentapi.ErrDockerUnavailable
}

func (noConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, agentapi.ErrDockerUnavailable
}
```

- [ ] **Step 6: Verdrahtung** – `cmd/agent/main.go`

```go
		opts = append(opts, agent.WithInstances(mgr), agent.WithConsole(mgr))
```

(ersetzt `opts = append(opts, agent.WithInstances(mgr))`; der Kommentar über dem `if cfg.DockerSocket != ""`-Block lautet danach: „Without docker_socket (dev default) there is no instance manager: the server answers instance writes and console calls with 503 docker_unavailable and creates no containers.“)

- [ ] **Step 7: Grün prüfen, committen**

Run: `make check`
Expected: PASS. Danach:

```bash
git add internal/agent/server.go internal/agent/server_console.go internal/agent/server_console_test.go internal/agent/server_export_test.go cmd/agent/main.go
git commit -m "feat(agent): console endpoints for logs, rcon and player lists

/v1/instances/logs inspects the container, then streams NDJSON with
X-Container-Running once Docker opened the log, with a 30 s deadline per write, at most 16 streams and an end on shutdown; logs and rcon
lift the read deadline so net/http does not cancel them after ReadTimeout.
Errors map to not_found, not_running, busy, rcon_unavailable and
docker_unavailable (spec 5.1).

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI grün (`TestServer*Console*`, `TestServerLogStreamLimit`, `TestServerLongRequestsOutliveReadTimeout`, `TestServerShutdownEndsLogStreams`, `TestClientConsoleEndToEnd` PASS, auch mit `-race`).

---

### Task 8: Integrationstest gegen echtes Docker (`dockerintegration`)

**Files:**
- Modify: `internal/agent/docker/integration_linux_test.go`

**Interfaces:**
- Consumes: Aufgaben 4–6 (`Manager.Logs`, `Manager.Rcon`, `Manager.Players`, `Config.RconCommand`), bestehende Helfer `requireDocker`, `requireCleanHost`, `forceRemove`, `rawRequest`, `testImage`, `testCmd`, `nobody`
- Produces: `TestIntegrationConsole`

Der CI-Job `nftables` führt bereits `go test -tags dockerintegration -exec "sudo env MCPANEL_IT_STRICT=1" -count=1 -v ./internal/agent/...` aus; der neue Test läuft dort ohne Workflow-Änderung. busybox hat kein `rcon-cli`: `RconCommand = "echo"` prüft den Exec-Weg (Benutzer `nobody`, `WorkingDir /data`, Ausgabe), `RconCommand = "false"` den Exit-Code ≠ 0.

- [ ] **Step 1: Test schreiben** – in `internal/agent/docker/integration_linux_test.go` den Import `"errors"` ergänzen und am Ende anfügen:

```go
// consoleCmd prints to stdout and stderr like a server, then ticks once a second and
// exits on SIGTERM.
var consoleCmd = []string{"sh", "-c", "echo hello; echo oops >&2; trap 'exit 0' TERM; i=0; while true; do i=$((i+1)); echo tick $i; sleep 1; done"}

func TestIntegrationConsole(t *testing.T) {
	c := requireDocker(t)
	ctx := context.Background()
	requireCleanHost(t, c)
	t.Cleanup(func() {
		forceRemove(t, c, instances.ContainerName(1), instances.RouterName)
		_ = rawRequest(t, http.MethodDelete, "/networks/"+instances.NetworkName)
	})

	dir := t.TempDir()
	images := config.Images{Java8: testImage, Java17: testImage, Java21: testImage, Java25: testImage, Router: testImage}
	newManager := func(rcon string) *instances.Manager {
		m := instances.New(instances.Config{
			Docker: c, InstancesDir: dir, UID: nobody, GID: nobody, Images: images, Clock: clock.Real{},
			Log:         slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
			RconCommand: rcon,
			Customize: func(name string, cfg *docker.ContainerConfig) {
				if name == instances.RouterName {
					cfg.Cmd = testCmd
					cfg.HostConfig.PortBindings = nil // keep the host port free on developer machines
					cfg.ExposedPorts = nil
					return
				}
				cfg.Cmd = consoleCmd
			},
		})
		t.Cleanup(m.Close)
		return m
	}
	m := newManager("echo")
	run := func(op func() error) {
		t.Helper()
		if err := op(); err != nil {
			t.Fatal(err)
		}
		m.Wait()
	}
	req := agentapi.EnsureRequest{
		ID: 1, Name: "it", FQDN: "it.mc.localhost", Revision: 1,
		Spec: instancespec.Spec{Type: instancespec.TypePaper, Version: "1.21.4", Java: instancespec.Java21, MemoryMiB: 1024},
	}
	run(func() error { return m.Ensure(ctx, req) })
	run(func() error { return m.Power(ctx, 1, agentapi.PowerStart) })

	// Live log: history plus new lines, stdout and stderr apart.
	lctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	lines, done := make(chan agentapi.LogLine, 256), make(chan error, 1)
	go func() {
		done <- m.Logs(lctx, agentapi.LogsRequest{ID: 1, Tail: 100}, func(running bool) {
			if !running {
				t.Error("start(false) for the running container")
			}
		}, func(l agentapi.LogLine) error {
			select {
			case lines <- l:
			default: // never block the stream
			}
			return nil
		})
	}()
	seen := map[string]agentapi.LogLine{}
	timeout := time.After(30 * time.Second)
	for seen["hello"].TS == 0 || seen["oops"].TS == 0 || seen["tick 3"].TS == 0 {
		select {
		case l := <-lines:
			seen[l.Line] = l
		case <-timeout:
			t.Fatalf("log lines missing after 30 s: %v", seen)
		}
	}
	if seen["hello"].Stream != agentapi.StreamOut || seen["oops"].Stream != agentapi.StreamErr {
		t.Fatalf("streams: hello=%+v oops=%+v", seen["hello"], seen["oops"])
	}

	// Console command: exec as the container user in /data, output back.
	if out, err := m.Rcon(ctx, 1, "say hallo"); err != nil || out != "say hallo" {
		t.Fatalf("Rcon = %q, %v", out, err)
	}
	// A failing rcon-cli (exit code != 0) is rcon_unavailable.
	if _, err := newManager("false").Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrRconUnavailable) {
		t.Fatalf("failing rcon command = %v, want ErrRconUnavailable", err)
	}

	// Player lists from the real data directory.
	if err := os.WriteFile(filepath.Join(instances.DataDir(dir, 1), "ops.json"), []byte(`[{"uuid":"u1","name":"Notch","level":4}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	players, err := m.Players(ctx, 1)
	if err != nil || len(players.Ops) != 1 || players.Ops[0].Name != "Notch" || len(players.Errors) != 0 {
		t.Fatalf("Players = %+v, %v", players, err)
	}

	// Stopping the container ends the stream; commands then report not running.
	run(func() error { return m.Power(ctx, 1, agentapi.PowerStop) })
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Logs after stop = %v, want nil", err)
		}
	case <-time.After(90 * time.Second):
		t.Fatal("log stream did not end after the container stopped")
	}
	if _, err := m.Rcon(ctx, 1, "list"); !errors.Is(err, agentapi.ErrNotRunning) {
		t.Fatalf("Rcon on a stopped container = %v, want ErrNotRunning", err)
	}

	// Resume after "hello": since wins over tail, no repeated line, and the stream of a
	// stopped container ends after the history.
	var resumed []string
	var resumedRunning bool
	err = m.Logs(ctx, agentapi.LogsRequest{ID: 1, Tail: 1, Since: seen["hello"].TS + 1}, func(running bool) { resumedRunning = running }, func(l agentapi.LogLine) error {
		resumed = append(resumed, l.Line)
		return nil
	})
	if err != nil || resumedRunning || slices.Contains(resumed, "hello") || !slices.Contains(resumed, "tick 1") {
		t.Fatalf("resumed lines = %q (running %v), %v", resumed, resumedRunning, err)
	}
}
```

- [ ] **Step 2: Kompilieren prüfen**

Run: `make check`
Expected: PASS (`make check` führt `GOOS=linux go vet -tags nftintegration,dockerintegration ./...` aus und kompiliert den Test damit).

- [ ] **Step 3: Committen, CI abwarten**

```bash
git add internal/agent/docker/integration_linux_test.go
git commit -m "test(agent): console against a real Docker daemon

Streams the log of a busybox container (history, live lines, end at stop,
resume with since), runs commands through exec as the container user and
reads a player list from the real data directory.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: CI grün; im Log des Jobs `nftables` erscheint `--- PASS: TestIntegrationConsole`. Schlägt nur dieser Test fehl, mit `gh run view <id> --log-failed` prüfen, ob `POST /exec/{id}/start` ohne `Upgrade` tatsächlich mit `200` und Multiplex-Daten antwortet (Annahme aus Aufgabe 4); falls nicht, Befund an den Lead melden, nicht still auf Hijack umbauen.

- [ ] **Step 4: Abschluss**

PR `feat/console-agent` von Draft auf „Ready for review“ setzen (`gh pr ready`), wenn alle Aufgaben grün sind; dem Lead melden: PR-Nummer und alles, was vom Plan abweicht. Mergen nur durch den Nutzer.
