# mc-panel Teilprojekt 5 – Phase B1: Sammler (Aufgaben 4–7)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der Sammler in `internal/monitoring` holt alle 30 s agent-Status, Server-List-Ping und Host-Werte, alle 60 s (live alle 5 s) TPS/MSPT per RCON, schreibt jeden Durchlauf nach `metrics_raw` und verdichtet/löscht alle 5 min.

**Architecture:** Reine Bausteine zuerst (SLP-Client, TPS-Parser mit Erkennung der Quelle, Host-Leser mit injizierbarem `/proc`-Pfad), dann der Durchlauf `pass(ctx)` (testbar mit Fake-Clock, ohne Ticker) und die dünne `Run`-Schleife, zuletzt die Verdichtung. Alles hinter kleinen Funktionsfeldern, die die Tests ersetzen.

**Tech Stack:** Go (Standardbibliothek, `net`, `encoding/json`, `regexp`, `golang.org/x/sys/unix` – bereits im Modul), sqlc-Queries aus Phase A.

**Spec:** `docs/superpowers/specs/2026-10-13-mc-panel-monitoring-design.md` (§3, §4). **Übersicht/Verträge:** `docs/superpowers/plans/2026-10-13-monitoring-00-overview.md` (V2, V4, V5, Global Constraints, Präzisierung TPS-Quelle).

## Ausführung dieser Phase

- Branch `feat/monitoring-collector`, Worktree `../mc-panel-wt/feat/monitoring-collector` von `origin/main` (Phase A gemerged). Teammate „mon-collector“.
- Auf dem Mac nur `make check` (neue Dateien vorher `git add -N`). „Run: go test …“ heißt auf der VM: `make test-vm SUITE=go` bzw. einzeln `ssh claude@192.168.1.117 'cd ~/mc-panel-test/monitoring-collector && PATH=/usr/local/go/bin:$PATH go test -race ./internal/monitoring -run <Name>'` (SSH ohne Sandbox; lange Läufe detached).
- Nur B1-Dateien (Übersicht, „Dateieigentum“): `internal/monitoring/collector.go`, `slp.go`, `tps.go`, `host.go`, `host_linux.go`, `host_other.go`, `write.go`, `rollup.go`, deren `*_test.go` und `internal/monitoring/testdata/**`. `types.go`, `handler.go`, `helpers_test.go` sind fest (Phase A); fehlt dort etwas, Lead fragen.
- **Testdaten nachprüfen:** Die RCON-Ausgaben in `testdata/` stammen aus den Quellen (Minecraft-Sprachdatei, PaperMC-Quellcode, siehe unten). Vor dem Push gegen echte Server auf der Test-VM prüfen (Paper-Container `mcpanel-mc-1`): `ssh claude@192.168.1.117 'sudo docker exec -u 1000 mcpanel-mc-1 rcon-cli "tick query"'` und `… rcon-cli mspt` (UID wie in `agent.toml` `mc_user`), Ausgabe roh (`| od -c | head`) ansehen und `testdata/` ggf. ersetzen. Abweichungen (z. B. Zeilenumbrüche, ANSI) im PR vermerken.

### Was Phase A liefert (benutzt, nicht geändert)

- `types.go`: `Deps`, `Snapshot`, `ServerSample`, `HostSample` (V4); `handler.go`: `NewHandler` ruft `newCollector(d)`, `Run` startet `h.coll.Run(ctx, h.alerts.Evaluate)` und `runRollup(ctx, h.d)`.
- Stubs `collector.go` und `rollup.go`, die diese Phase **ersetzt** (gleiche Signaturen wie V4).
- Store (V2): `store.New(db)` mit `MonitoringInsertRaw(ctx, store.MonitoringInsertRawParams{Series, Metric, At, Value})`, `MonitoringRollup5m/1h(ctx, store.MonitoringRollup5mParams{FromAt, ToAt})`, `MonitoringPruneRaw/5m/1h(ctx, at int64)`; vorhanden: `ServersListInstances(ctx)` (Felder `Name, ServerID, Type, DesiredState, Revision …`), `ServersGetSettings(ctx)` (`BaseDomain sql.NullString`).
- Weitere: `instancespec.FQDN(name, base)`, `instancespec.TypePaper/TypePurpur`, `agentapi.InstancesClient` (`Instances(ctx)`), `agentapi.ConsoleClient` (`Rcon(ctx, id, cmd)`), `servers.DataGate` (`DataJobRunning(ctx, id)`), `storetest.Open/CreateServer`.

### Verbindliche Werte dieser Phase

| Wert | |
|---|---|
| Takte | `collectEvery = 30 * time.Second`, `tpsEvery = 60 * time.Second`, `liveTPSEvery = 5 * time.Second`, `liveWindow = 30 * time.Second`, `rollupEvery = 5 * time.Minute` |
| Fristen | Ping 3 s (gesamter Austausch), agent-Status 10 s, RCON 10 s (agent-Default) |
| Parallelität | 4 Pings gleichzeitig, je Server 1 RCON (Takt und Live teilen die Sperre) |
| Ping | Handshake `0x00`: VarInt Protokoll `-1`, String FQDN (≤ 255 Bytes), Unsigned Short 25565, VarInt `1`; dann Status-Request `0x00` ohne Felder; Antwort `0x00` + String (JSON). Pakete: VarInt-Länge (≤ 5 Bytes) + VarInt Paket-ID + Daten; Paket ≤ 64 KiB, JSON ≤ 64 KiB |
| TPS-Erkennung | erst `tick query`; ungültig → bei `paper`/`purpur` `mspt`, sonst `none`; Quelle fest nach 3 gültigen bzw. 3 ungültigen Antworten, pro Server und `revision` |
| Aufbewahrung | Rohwerte 24 h, 5 min 7 d, 1 h 30 d; Verdichtungsfenster 30 min (5 min) bzw. 3 h (1 h), beim ersten Lauf 24 h bzw. 7 d |
| Metrik-Namen | Server: `up`, `players`, `players_max`, `tps`, `mspt`, `cpu_percent`, `memory_bytes`, `memory_limit_bytes`; Host (`series = 0`): `cpu_percent`, `memory_used`, `memory_total`, `load1`, `disk_free`, `disk_total` |

### Quellen der Protokoll- und Ausgabedetails

- Server-List-Ping: minecraft.wiki „Java Edition protocol/Server List Ping“ (Handshake-Felder, Protokoll `-1` beim Abfragen, Next State `1`, Status-Response als String; `players` optional, `sample` fehlt bei 0 Spielern oder `hide-online-players`).
- `tick query` (Vanilla ≥ 1.20.3, auch Paper/Fabric/NeoForge, Permission-Level 3 – RCON hat 4): Sprachschlüssel aus `en_us.json` 1.21.4: `commands.tick.status.running` „The game is running normally“, `…status.lagging` „The game is running, but can't keep up with the target tick rate“, `…status.frozen` „The game is frozen“, `…status.sprinting` „The game is sprinting“, `commands.tick.query.rate.running` „Target tick rate: %s per second.\nAverage time per tick: %sms (Target: %sms)“, `…rate.sprinting` „Target tick rate: %s per second (ignored, reference only).\nAverage time per tick: %sms“, `commands.tick.query.percentiles` „Percentiles: P50: %sms P95: %sms P99: %sms, sample: %s“. Der Server formatiert die Zahlen mit einer Nachkommastelle (`Locale.ROOT`). Ob RCON die Meldungen mit Zeilenumbruch trennt, ist versionsabhängig: der Parser sucht per Regex im ganzen Text.
- Paper `mspt` (`PaperMSPTCommand`): Zeile 1 „Server tick times (avg/min/max) from last 5s, 10s, 1m:“, Zeile 2 „◴ a/b/c, d/e/f, g/h/i“ (Format `########0.0`, Farbcodes); erster Block = letzte 5 s. `tps` liefert Paper ebenfalls, wird aber nicht abgefragt (sonst vier statt zwei RCON-Zeilen pro Minute; Spec §1.1/§10.2): `tps = min(20, 1000 / mspt)`.
- `DecimalFormat` ohne Locale nutzt die JVM-Locale: der Parser akzeptiert `,` als Dezimaltrenner zwischen zwei Ziffern.
- mc-router (`server/connector.go`, itzg/mc-router): ohne konfigurierte Auto-Scale-MOTD schließt der Router bei unbekannter Route oder nicht erreichbarem Backend (Dial-Frist 2 s) die Verbindung ohne Status-Antwort → `up = 0`. Die 3-s-Frist des panels liegt über diesen 2 s.

## Abweichungen und Präzisierungen

1. **Paper-Fallback nur `mspt`:** Die Präzisierung in der Übersicht nennt `mspt` + `tps`; zwei Befehle pro Minute ergäben vier RCON-Zeilen und verletzten Spec §1.1/§10.2. TPS wird aus MSPT abgeleitet (Ziel 20/s, ältere Versionen kennen keine andere Tickrate).
2. **Gefrorenes Spiel:** Meldet `tick query` „The game is frozen“, ist `tps = 0` (statt der Zielrate), `mspt` wie gemeldet.
3. **Konstanten aus Phase A:** Metrik-Namen (`HostSeries`, `MetricUp` … `MetricDiskTotal`), TPS-Quellen (`TPSSource…`), Takte und Grenzen (`collectEvery`, `tpsEvery`, `liveTPSEvery`, `liveWindow`, `maxParallelPings`, `pingTimeout`, `rollupEvery`, `retainRaw`, `retain5m`, `retain1h`) stehen in `types.go`; diese Phase definiert keine davon neu. Eigene Konstanten nur für Phasen-Details (`liveFresh`, `tpsCarry`, `window5m`, `window1h`, SLP-Grenzen).
4. **Test-Fakes und -Helfer (Lead-Vorgabe):** `helpers_test.go` (Phase A) liefert `fakeAgent`, `fakeConsole` (mit `peak(id)` für gleichzeitige Aufrufe), `pingServer`, `statusJSON`. Wo die Fakes dieser Phase (`cAgent`, `cConsole`) nur dasselbe können, nutzt der Teammate die gemeinsamen; nur zusätzliche Fähigkeiten bleiben als eigene Fakes. Alle eigenen Testhelfer dieser Phase tragen das Präfix `c` (`cSLPServer`, `cReadHandshake`, `cStatusPacket`, `cRconOut`, `cIntp`, `cF64`, `cBp`, `cI64`, `cNear`, `cBase`, `cMinute`, `cInsertRaw`, `cAgg`, `cCountRaw`, `cCountMetric`, `cEnv`, `cGate`), B2 nutzt `a`.
6. **TPS weitertragen (V5, Lead-Vorgabe):** `Latest` und `Live` tragen den letzten gültigen TPS/MSPT-Wert bis 90 s Alter weiter (`carryLocked`, `tpsCarry`); gespeichert werden nur frisch gemessene Werte (Tests `TestPassTPSOnlyEverySixtySeconds`, `TestPassCarriesTPSAtMost90s`).
5. **Takt auslassen:** `time.Ticker` puffert einen Tick; nach einem langen Durchlauf leert `Run` den Kanal, damit kein sofortiger Folge-Durchlauf startet (Global Constraints „lässt den nächsten Takt aus“).

## Review Focus (aus der Übersicht, in dieser Phase)

- **1 Feindliche Ping-Antworten** → Aufgabe 4 (Tests mit Fake-Server für jede Variante).
- **2 Ausfälle im Takt** → Aufgabe 6 (agent weg, RCON hängt, Durchlauf überzieht).
- **4 Zeit und Verdichtung** → Aufgabe 7 (Grenzen, Idempotenz, Lücken, Uhrsprung).

## Dateistruktur

```
internal/monitoring/slp.go, slp_test.go                 (4) Server-List-Ping
internal/monitoring/tps.go, tps_test.go                 (5) Parser + Erkennung der Quelle
internal/monitoring/testdata/rcon/*.txt                 (5) echte Ausgaben
internal/monitoring/host.go, host_linux.go, host_other.go, host_test.go, host_linux_test.go (5) Host-Leser
internal/monitoring/testdata/proc/{a,b}/{stat,meminfo,loadavg} (5)
internal/monitoring/collector.go, collector_test.go     (6) Durchlauf, Live, Run
internal/monitoring/write.go, write_test.go             (6) Schreiben, Metrik-Namen
internal/monitoring/rollup.go, rollup_test.go           (7) Verdichtung, Löschen
```

---

### Task 4: Server-List-Ping

**Files:**
- Create: `internal/monitoring/slp.go`
- Test: `internal/monitoring/slp_test.go`

**Interfaces:**
- Consumes: –
- Produces: `type slpResult struct { Online, Max *int; Names []string }`; `func slpPing(ctx context.Context, addr, host string) (slpResult, error)`; Konstanten `pingTimeout = 3 * time.Second`, `slpMaxPacket = 64 << 10`; `errSLP` (alle Protokollfehler wickeln ihn).

- [ ] **Step 1: Tests schreiben (rot)**

`internal/monitoring/slp_test.go`:

```go
package monitoring

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// slpServer answers one connection with reply(handshake host); it returns the address.
func cSLPServer(t *testing.T, reply func(c net.Conn, host string)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				host, err := cReadHandshake(br)
				if err != nil {
					return
				}
				if _, err := readPacket(br); err != nil { // status request
					return
				}
				reply(c, host)
			}()
		}
	}()
	return ln.Addr().String()
}

// readHandshake parses the client's handshake packet (test side of the protocol).
func cReadHandshake(br *bufio.Reader) (string, error) {
	p, err := readPacket(br)
	if err != nil {
		return "", err
	}
	r := bytes.NewReader(p)
	if id, _ := readVarInt(r); id != 0 {
		return "", errors.New("handshake id")
	}
	if v, _ := readVarInt(r); v != -1 {
		return "", errors.New("protocol must be -1")
	}
	n, _ := readVarInt(r)
	host := make([]byte, n)
	if _, err := io.ReadFull(r, host); err != nil {
		return "", err
	}
	var port uint16
	_ = binary.Read(r, binary.BigEndian, &port)
	if next, _ := readVarInt(r); next != 1 || port != 25565 {
		return "", errors.New("next state/port")
	}
	return string(host), nil
}

func readPacket(br *bufio.Reader) ([]byte, error) {
	n, err := readVarInt(br)
	if err != nil {
		return nil, err
	}
	p := make([]byte, n)
	_, err = io.ReadFull(br, p)
	return p, err
}

// statusPacket frames a status response with the given JSON.
func cStatusPacket(json string) []byte {
	var body bytes.Buffer
	body.Write(appendVarInt(nil, 0))
	body.Write(appendVarInt(nil, int32(len(json))))
	body.WriteString(json)
	return append(appendVarInt(nil, int32(body.Len())), body.Bytes()...)
}

func cIntp(v int) *int { return &v }

func TestSLPPingParsesPlayers(t *testing.T) {
	var gotHost string
	addr := cSLPServer(t, func(c net.Conn, host string) {
		gotHost = host
		_, _ = c.Write(cStatusPacket(`{"version":{"name":"1.21.4","protocol":769},
			"players":{"max":20,"online":3,"sample":[{"name":"Alice","id":"x"},{"name":"bob_2"},{"name":"<b>evil</b>"},{"name":"waytoolongname12345"}]},
			"description":{"text":"hi"},"favicon":"data:image/png;base64,AAAA"}`))
	})
	res, err := slpPing(context.Background(), addr, "survival.mc.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if gotHost != "survival.mc.example.com" {
		t.Fatalf("handshake host = %q", gotHost)
	}
	if *res.Online != 3 || *res.Max != 20 {
		t.Fatalf("players = %v/%v", *res.Online, *res.Max)
	}
	if strings.Join(res.Names, ",") != "Alice,bob_2" {
		t.Fatalf("names = %v (invalid names must be dropped)", res.Names)
	}
}

func TestSLPPingWithoutPlayersObject(t *testing.T) {
	addr := cSLPServer(t, func(c net.Conn, _ string) { _, _ = c.Write(cStatusPacket(`{"version":{"name":"x","protocol":1}}`)) })
	res, err := slpPing(context.Background(), addr, "a.example.com")
	if err != nil || res.Online != nil || res.Max != nil || len(res.Names) != 0 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestSLPPingAtMostTwelveNames(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"players":{"max":100,"online":40,"sample":[`)
	for i := range 20 {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"p` + string(rune('a'+i)) + `"}`)
	}
	b.WriteString(`]}}`)
	addr := cSLPServer(t, func(c net.Conn, _ string) { _, _ = c.Write(cStatusPacket(b.String())) })
	res, err := slpPing(context.Background(), addr, "a.example.com")
	if err != nil || len(res.Names) != 12 {
		t.Fatalf("names = %d err = %v", len(res.Names), err)
	}
}

func TestSLPPingRejectsHostileAnswers(t *testing.T) {
	cases := map[string]func(c net.Conn){
		"oversized packet": func(c net.Conn) { _, _ = c.Write(appendVarInt(nil, slpMaxPacket+1)) },
		"six-byte varint":  func(c net.Conn) { _, _ = c.Write([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0x01}) },
		"negative length":  func(c net.Conn) { _, _ = c.Write(appendVarInt(nil, -5)) },
		"wrong packet id": func(c net.Conn) {
			p := []byte{0x01, 0x02, '{', '}'}
			_, _ = c.Write(append(appendVarInt(nil, int32(len(p))), p...))
		},
		"string longer than packet": func(c net.Conn) {
			p := append([]byte{0x00}, appendVarInt(nil, 1000)...)
			_, _ = c.Write(append(appendVarInt(nil, int32(len(p))), p...))
		},
		"truncated":       func(c net.Conn) { _, _ = c.Write(appendVarInt(nil, 100)) },
		"not json":        func(c net.Conn) { _, _ = c.Write(cStatusPacket(`nope`)) },
		"negative online": func(c net.Conn) { _, _ = c.Write(cStatusPacket(`{"players":{"max":20,"online":-1}}`)) },
		"huge max":        func(c net.Conn) { _, _ = c.Write(cStatusPacket(`{"players":{"max":100001,"online":1}}`)) },
		"string online":   func(c net.Conn) { _, _ = c.Write(cStatusPacket(`{"players":{"max":20,"online":"3"}}`)) },
		"closed":          func(net.Conn) {},
	}
	for name, reply := range cases {
		t.Run(name, func(t *testing.T) {
			addr := cSLPServer(t, func(c net.Conn, _ string) { reply(c) })
			if _, err := slpPing(context.Background(), addr, "a.example.com"); !errors.Is(err, errSLP) {
				t.Fatalf("err = %v, want errSLP", err)
			}
		})
	}
}

func TestSLPPingSilentServerTimesOut(t *testing.T) {
	addr := cSLPServer(t, func(c net.Conn, _ string) { time.Sleep(10 * time.Second) })
	start := time.Now()
	_, err := slpPing(context.Background(), addr, "a.example.com")
	if !errors.Is(err, errSLP) || time.Since(start) > pingTimeout+time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestSLPPingRefusedConnection(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	_ = ln.Close()
	if _, err := slpPing(context.Background(), addr, "a.example.com"); !errors.Is(err, errSLP) {
		t.Fatalf("err = %v", err)
	}
}

func TestVarIntRoundTrip(t *testing.T) {
	for _, v := range []int32{0, 1, 127, 128, 255, 25565, 2097151, 2147483647, -1} {
		got, err := readVarInt(bytes.NewReader(appendVarInt(nil, v)))
		if err != nil || got != v {
			t.Fatalf("%d → %d (%v)", v, got, err)
		}
	}
	if b := appendVarInt(nil, -1); len(b) != 5 {
		t.Fatalf("-1 must take 5 bytes, got %d", len(b))
	}
}
```

- [ ] **Step 2: Rot prüfen**

Run: `make check`
Expected: FAIL (Kompilierfehler: `slpPing`, `appendVarInt`, `readVarInt`, `errSLP`, `slpMaxPacket` fehlen; `pingTimeout` kommt aus `types.go`).

- [ ] **Step 3: Implementieren**

`internal/monitoring/slp.go`:

```go
package monitoring

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"time"
)

// Server List Ping (spec §3.2): the answer comes from a server we do not trust, so every
// length is bounded before anything is allocated and only three fields are read.
const (
	slpMaxPacket = 64 << 10
	slpMaxNames  = 12
	slpMaxCount  = 100000
	slpPort      = 25565
)

var (
	errSLP      = errors.New("monitoring: server list ping failed")
	slpNameRe   = regexp.MustCompile(`^[A-Za-z0-9_]{1,16}$`)
	errVarInt   = errors.New("varint too long")
)

type slpResult struct {
	Online *int
	Max    *int
	Names  []string
}

// slpPing connects to addr (mc-router) and asks for the status of host.
func slpPing(ctx context.Context, addr, host string) (slpResult, error) {
	if len(host) == 0 || len(host) > 255 {
		return slpResult{}, fmt.Errorf("%w: invalid host", errSLP)
	}
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return slpResult{}, fmt.Errorf("%w: %w", errSLP, err)
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return slpResult{}, fmt.Errorf("%w: %w", errSLP, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

	if _, err := conn.Write(append(handshakePacket(host), statusRequest()...)); err != nil {
		return slpResult{}, fmt.Errorf("%w: %w", errSLP, err)
	}
	payload, err := readStatus(bufio.NewReader(conn))
	if err != nil {
		return slpResult{}, fmt.Errorf("%w: %w", errSLP, err)
	}
	return parseStatus(payload)
}

func handshakePacket(host string) []byte {
	var b []byte
	b = appendVarInt(b, 0x00)
	b = appendVarInt(b, -1) // protocol version unknown: status only
	b = appendVarInt(b, int32(len(host)))
	b = append(b, host...)
	b = binary.BigEndian.AppendUint16(b, slpPort)
	b = appendVarInt(b, 1) // next state: status
	return append(appendVarInt(nil, int32(len(b))), b...)
}

func statusRequest() []byte { return []byte{0x01, 0x00} } // length 1, packet id 0

// readStatus reads one status response packet and returns its JSON string.
func readStatus(br *bufio.Reader) ([]byte, error) {
	n, err := readVarInt(br)
	if err != nil {
		return nil, err
	}
	if n <= 0 || n > slpMaxPacket {
		return nil, fmt.Errorf("packet length %d", n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(br, p); err != nil {
		return nil, err
	}
	r := bytes.NewReader(p)
	if id, err := readVarInt(r); err != nil || id != 0 {
		return nil, fmt.Errorf("packet id %d (%v)", id, err)
	}
	sl, err := readVarInt(r)
	if err != nil {
		return nil, err
	}
	if sl < 0 || int(sl) != r.Len() {
		return nil, fmt.Errorf("string length %d, %d bytes left", sl, r.Len())
	}
	return p[len(p)-int(sl):], nil
}

func parseStatus(payload []byte) (slpResult, error) {
	var st struct {
		Players *struct {
			Max    *int `json:"max"`
			Online *int `json:"online"`
			Sample []struct {
				Name string `json:"name"`
			} `json:"sample"`
		} `json:"players"`
	}
	if err := json.Unmarshal(payload, &st); err != nil {
		return slpResult{}, fmt.Errorf("%w: json: %w", errSLP, err)
	}
	var res slpResult
	if st.Players == nil {
		return res, nil
	}
	for _, v := range []*int{st.Players.Max, st.Players.Online} {
		if v != nil && (*v < 0 || *v > slpMaxCount) {
			return slpResult{}, fmt.Errorf("%w: player count %d out of range", errSLP, *v)
		}
	}
	res.Online, res.Max = st.Players.Online, st.Players.Max
	for _, s := range st.Players.Sample {
		if len(res.Names) == slpMaxNames {
			break
		}
		if slpNameRe.MatchString(s.Name) {
			res.Names = append(res.Names, s.Name)
		}
	}
	return res, nil
}

func appendVarInt(b []byte, v int32) []byte {
	u := uint32(v)
	for u >= 0x80 {
		b = append(b, byte(u)|0x80)
		u >>= 7
	}
	return append(b, byte(u))
}

// readVarInt reads at most 5 bytes (a 32-bit VarInt).
func readVarInt(r io.ByteReader) (int32, error) {
	var u uint32
	for i := range 5 {
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		u |= uint32(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return int32(u), nil
		}
	}
	return 0, errVarInt
}
```

- [ ] **Step 4: Grün prüfen**

Run: `make check`, dann `make test-vm SUITE=go` (bzw. einzeln `go test -race ./internal/monitoring -run 'TestSLP|TestVarInt'`)
Expected: PASS. `TestSLPPingSilentServerTimesOut` endet nach ~3 s.

- [ ] **Step 5: Commit**

```bash
git add internal/monitoring/slp.go internal/monitoring/slp_test.go
git commit -m "feat(monitoring): server list ping with bounded parsing

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 5: TPS-Parser, Erkennung der Quelle und Host-Leser

**Files:**
- Create: `internal/monitoring/tps.go`, `internal/monitoring/host.go`, `internal/monitoring/host_linux.go`, `internal/monitoring/host_other.go`
- Create: `internal/monitoring/testdata/rcon/tick-query-running.txt`, `tick-query-running-joined.txt`, `tick-query-lagging.txt`, `tick-query-frozen.txt`, `tick-query-sprinting.txt`, `tick-query-unknown.txt`, `paper-mspt.txt`, `paper-mspt-comma.txt`, `garbage.txt`
- Create: `internal/monitoring/testdata/proc/a/{stat,meminfo,loadavg}`, `internal/monitoring/testdata/proc/b/stat`
- Test: `internal/monitoring/tps_test.go`, `internal/monitoring/host_test.go`, `internal/monitoring/host_linux_test.go`

**Interfaces:**
- Consumes: –
- Produces:
  - `type tpsReading struct { TPS, MSPT float64 }`
  - `func parseTickQuery(out string) (tpsReading, bool)`, `func parseMSPT(out string) (tpsReading, bool)`
  - `type tpsDetector struct{…}`; `func (d *tpsDetector) command(serverType string, revision int64) (cmd string, ok bool)`; `func (d *tpsDetector) observe(cmd, out string) (tpsReading, bool)`; `func (d *tpsDetector) source() string` (`"tick_query" | "paper" | "none" | "unknown"`)
  - `type hostReader struct { proc string; disk string; prev *cpuTimes }`; `func newHostReader(instancesDir string) *hostReader`; `func (h *hostReader) read() HostSample`

- [ ] **Step 1: Testdaten anlegen**

`testdata/rcon/tick-query-running.txt` (Zeilen getrennt):

```
The game is running normally
Target tick rate: 20.0 per second.
Average time per tick: 3.4ms (Target: 50.0ms)
Percentiles: P50: 3.1ms P95: 5.9ms P99: 8.2ms, sample: 100
```

`tick-query-running-joined.txt` (ohne Trenner, wie ältere RCON-Ausgaben, eine Zeile):

```
The game is running normallyTarget tick rate: 20.0 per second.
Average time per tick: 3.4ms (Target: 50.0ms)Percentiles: P50: 3.1ms P95: 5.9ms P99: 8.2ms, sample: 100
```

`tick-query-lagging.txt`:

```
The game is running, but can't keep up with the target tick rate
Target tick rate: 20.0 per second.
Average time per tick: 80.0ms (Target: 50.0ms)
Percentiles: P50: 78.5ms P95: 95.0ms P99: 120.3ms, sample: 100
```

`tick-query-frozen.txt`:

```
The game is frozen
Target tick rate: 20.0 per second.
Average time per tick: 0.2ms (Target: 50.0ms)
Percentiles: P50: 0.2ms P95: 0.3ms P99: 0.4ms, sample: 100
```

`tick-query-sprinting.txt`:

```
The game is sprinting
Target tick rate: 20.0 per second (ignored, reference only).
Average time per tick: 4.0ms
Percentiles: P50: 3.9ms P95: 4.5ms P99: 5.0ms, sample: 100
```

`tick-query-unknown.txt` (alte Version ohne `/tick`):

```
Unknown or incomplete command, see below for error
tick query<--[HERE]
```

`paper-mspt.txt` (mit Farbcodes, `§` als UTF-8):

```
§6Server tick times §e(§7avg§e/§7min§e/§7max§e) from last 5s§7,§e 10s§7,§e 1m§e:
§6◴ §a1.2§7/§a0.8§7/§a3.4§e, §a1.1§7/§a0.7§7/§a4.0§e, §a1.3§7/§a0.6§7/§a5.6
```

`paper-mspt-comma.txt` (JVM mit deutscher Locale):

```
§6Server tick times §e(§7avg§e/§7min§e/§7max§e) from last 5s§7,§e 10s§7,§e 1m§e:
§6◴ §a62,5§7/§a40,1§7/§a90,0§e, §a55,0§7/§a30,0§7/§a91,2§e, §a50,3§7/§a20,0§7/§a99,9
```

`garbage.txt`:

```
Target tick rate: lots per second. Average time per tick: -3ms
```

`testdata/proc/a/stat` (erste Messung):

```
cpu  1000 0 500 8000 500 0 0 0 0 0
cpu0 500 0 250 4000 250 0 0 0 0 0
intr 0
```

`testdata/proc/b/stat` (zweite Messung: +600 busy, +400 idle/iowait → 60 %):

```
cpu  1400 0 700 8300 600 0 0 0 0 0
cpu0 700 0 350 4150 300 0 0 0 0 0
intr 0
```

`testdata/proc/a/meminfo`:

```
MemTotal:        8000000 kB
MemFree:          500000 kB
MemAvailable:    6000000 kB
Buffers:          100000 kB
```

`testdata/proc/a/loadavg`:

```
0.52 0.40 0.31 2/345 12345
```

- [ ] **Step 2: Tests schreiben (rot)**

`internal/monitoring/tps_test.go`:

```go
package monitoring

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func cRconOut(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "rcon", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cNear(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestParseTickQuery(t *testing.T) {
	cases := []struct {
		file      string
		tps, mspt float64
	}{
		{"tick-query-running.txt", 20, 3.4},
		{"tick-query-running-joined.txt", 20, 3.4},
		{"tick-query-lagging.txt", 12.5, 80},
		{"tick-query-frozen.txt", 0, 0.2},
		{"tick-query-sprinting.txt", 20, 4.0},
	}
	for _, c := range cases {
		got, ok := parseTickQuery(cRconOut(t, c.file))
		if !ok || !cNear(got.TPS, c.tps) || !cNear(got.MSPT, c.mspt) {
			t.Errorf("%s: %+v ok=%v, want tps %v mspt %v", c.file, got, ok, c.tps, c.mspt)
		}
	}
	for _, f := range []string{"tick-query-unknown.txt", "garbage.txt", "paper-mspt.txt"} {
		if _, ok := parseTickQuery(cRconOut(t, f)); ok {
			t.Errorf("%s must not parse as tick query", f)
		}
	}
}

func TestParseMSPT(t *testing.T) {
	got, ok := parseMSPT(cRconOut(t, "paper-mspt.txt"))
	if !ok || !cNear(got.MSPT, 1.2) || !cNear(got.TPS, 20) {
		t.Fatalf("mspt = %+v ok=%v", got, ok)
	}
	got, ok = parseMSPT(cRconOut(t, "paper-mspt-comma.txt"))
	if !ok || !cNear(got.MSPT, 62.5) || !cNear(got.TPS, 16) {
		t.Fatalf("comma mspt = %+v ok=%v", got, ok)
	}
	for _, f := range []string{"tick-query-unknown.txt", "garbage.txt"} {
		if _, ok := parseMSPT(cRconOut(t, f)); ok {
			t.Errorf("%s must not parse as mspt", f)
		}
	}
}

func TestParseRejectsOutOfRange(t *testing.T) {
	if _, ok := parseTickQuery("Target tick rate: 20.0 per second.\nAverage time per tick: 70000.0ms (Target: 50.0ms)"); ok {
		t.Fatal("mspt above 60000 must be rejected")
	}
	if _, ok := parseTickQuery("Target tick rate: 5000.0 per second.\nAverage time per tick: 0.1ms (Target: 0.2ms)"); ok {
		t.Fatal("tps above 1000 must be rejected")
	}
}

func TestDetectorTickQueryAfterThreeValid(t *testing.T) {
	var d tpsDetector
	for i := range 3 {
		cmd, ok := d.command("vanilla", 1)
		if !ok || cmd != cmdTickQuery {
			t.Fatalf("round %d: cmd %q ok %v", i, cmd, ok)
		}
		if _, ok := d.observe(cmd, cRconOut(t, "tick-query-running.txt")); !ok {
			t.Fatal("valid answer not accepted")
		}
		if i < 2 && d.source() != TPSSourceUnknown {
			t.Fatalf("source fixed too early: %s", d.source())
		}
	}
	if d.source() != TPSSourceTickQuery {
		t.Fatalf("source = %s", d.source())
	}
}

func TestDetectorPaperFallback(t *testing.T) {
	var d tpsDetector
	for range 3 {
		cmd, _ := d.command("paper", 1)
		d.observe(cmd, cRconOut(t, "tick-query-unknown.txt"))
	}
	cmd, ok := d.command("paper", 1)
	if !ok || cmd != cmdMSPT {
		t.Fatalf("after three bad tick queries: cmd %q ok %v", cmd, ok)
	}
	for range 3 {
		cmd, _ := d.command("paper", 1)
		d.observe(cmd, cRconOut(t, "paper-mspt.txt"))
	}
	if d.source() != TPSSourcePaper {
		t.Fatalf("source = %s", d.source())
	}
}

func TestDetectorNoneForVanillaWithoutTick(t *testing.T) {
	var d tpsDetector
	for range 3 {
		cmd, _ := d.command("vanilla", 1)
		d.observe(cmd, cRconOut(t, "tick-query-unknown.txt"))
	}
	if _, ok := d.command("vanilla", 1); ok || d.source() != TPSSourceNone {
		t.Fatalf("source = %s, want none and no command", d.source())
	}
}

func TestDetectorResetsOnNewRevision(t *testing.T) {
	var d tpsDetector
	for range 3 {
		cmd, _ := d.command("vanilla", 1)
		d.observe(cmd, cRconOut(t, "tick-query-unknown.txt"))
	}
	if cmd, ok := d.command("vanilla", 2); !ok || cmd != cmdTickQuery || d.source() != TPSSourceUnknown {
		t.Fatalf("new revision must start over: %q %v %s", cmd, ok, d.source())
	}
}
```

`internal/monitoring/host_test.go` (läuft überall: Plattenwerte über eine injizierte `statfs`):

```go
package monitoring

import (
	"errors"
	"testing"
)

func TestHostReaderDiskFromStatfs(t *testing.T) {
	h := &hostReader{disk: "/srv/mc-panel", statfs: func(dir string) (uint64, uint64, error) {
		if dir != "/srv/mc-panel" {
			t.Fatalf("statfs(%q)", dir)
		}
		return 40 << 30, 100 << 30, nil
	}}
	s := h.read()
	if s.DiskFree == nil || *s.DiskFree != 40<<30 || *s.DiskTotal != 100<<30 {
		t.Fatalf("disk = %v/%v", s.DiskFree, s.DiskTotal)
	}
}

func TestHostReaderWithoutDisk(t *testing.T) {
	h := &hostReader{statfs: func(string) (uint64, uint64, error) { return 0, 0, errors.New("x") }}
	if s := h.read(); s.DiskFree != nil || s.DiskTotal != nil {
		t.Fatalf("no instances_dir must leave disk unset: %+v", s)
	}
	h.disk = "/nope"
	if s := h.read(); s.DiskFree != nil {
		t.Fatal("statfs error must leave disk unset")
	}
}
```

`internal/monitoring/host_linux_test.go`:

```go
//go:build linux

package monitoring

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostReaderProc(t *testing.T) {
	dir := t.TempDir()
	cp := func(src, dst string) {
		b, err := os.ReadFile(filepath.Join("testdata", "proc", src))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, dst), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cp("a/stat", "stat")
	cp("a/meminfo", "meminfo")
	cp("a/loadavg", "loadavg")
	h := &hostReader{proc: dir, statfs: func(string) (uint64, uint64, error) { return 0, 0, os.ErrNotExist }}

	first := h.read()
	if first.CPUPercent != nil {
		t.Fatal("the first reading has no CPU delta yet")
	}
	if *first.MemoryTotal != 8000000*1024 || *first.MemoryUsed != 2000000*1024 {
		t.Fatalf("mem = %d/%d", *first.MemoryUsed, *first.MemoryTotal)
	}
	if !cNear(*first.Load1, 0.52) {
		t.Fatalf("load = %v", *first.Load1)
	}

	cp("b/stat", "stat")
	second := h.read()
	if second.CPUPercent == nil || !cNear(*second.CPUPercent, 60) {
		t.Fatalf("cpu = %v, want 60", second.CPUPercent)
	}
}

func TestHostReaderBrokenProc(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "stat"), []byte("cpu x y z\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "meminfo"), []byte("MemTotal: lots\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "loadavg"), []byte("\n"), 0o644)
	h := &hostReader{proc: dir, statfs: func(string) (uint64, uint64, error) { return 0, 0, os.ErrNotExist }}
	h.read()
	s := h.read()
	if s.CPUPercent != nil || s.MemoryTotal != nil || s.Load1 != nil {
		t.Fatalf("broken files must give no values: %+v", s)
	}
}
```

- [ ] **Step 3: Rot prüfen**

Run: `make check`
Expected: FAIL (Kompilierfehler: `parseTickQuery`, `tpsDetector`, `hostReader` … fehlen).

- [ ] **Step 4: TPS implementieren**

`internal/monitoring/tps.go`:

```go
package monitoring

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
)

// TPS/MSPT over RCON (spec §3.3, plan precision): the source is detected per server by trying
// `tick query` first and, for paper/purpur only, `mspt`. Only one command per sample keeps the
// log at two RCON lines a minute.
const (
	cmdTickQuery = "tick query"
	cmdMSPT      = "mspt"

	detectAfter = 3
	maxTPS      = 1000
	maxMSPT     = 60000
	paperTarget = 20.0
)

var (
	formatCodeRe = regexp.MustCompile(`§.|\x1b\[[0-9;]*[A-Za-z]`)
	decimalComma = regexp.MustCompile(`(\d),(\d)`)
	tickRateRe   = regexp.MustCompile(`Target tick rate: (\d+(?:\.\d+)?) per second`)
	tickAvgRe    = regexp.MustCompile(`Average time per tick: (\d+(?:\.\d+)?)ms`)
	msptRe       = regexp.MustCompile(`from last 5s.*?(\d+(?:\.\d+)?)/(\d+(?:\.\d+)?)/(\d+(?:\.\d+)?)`)
)

type tpsReading struct {
	TPS  float64
	MSPT float64
}

// clean drops § and ANSI codes and turns decimal commas into points.
func clean(out string) string {
	return decimalComma.ReplaceAllString(formatCodeRe.ReplaceAllString(out, ""), "$1.$2")
}

func tpsValid(r tpsReading) (tpsReading, bool) {
	ok := r.TPS >= 0 && r.TPS <= maxTPS && r.MSPT >= 0 && r.MSPT <= maxMSPT
	return r, ok
}

func parseTickQuery(out string) (tpsReading, bool) {
	s := clean(out)
	rate, avg := tickRateRe.FindStringSubmatch(s), tickAvgRe.FindStringSubmatch(s)
	if rate == nil || avg == nil {
		return tpsReading{}, false
	}
	target, err1 := strconv.ParseFloat(rate[1], 64)
	mspt, err2 := strconv.ParseFloat(avg[1], 64)
	if err1 != nil || err2 != nil || target <= 0 || target > maxTPS {
		return tpsReading{}, false
	}
	r := tpsReading{TPS: target, MSPT: mspt}
	switch {
	case strings.Contains(s, "The game is frozen"):
		r.TPS = 0
	case mspt > 0:
		r.TPS = min(target, 1000/mspt)
	}
	return tpsValid(r)
}

func parseMSPT(out string) (tpsReading, bool) {
	m := msptRe.FindStringSubmatch(strings.ReplaceAll(clean(out), "\n", " "))
	if m == nil {
		return tpsReading{}, false
	}
	mspt, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return tpsReading{}, false
	}
	r := tpsReading{TPS: paperTarget, MSPT: mspt}
	if mspt > 0 {
		r.TPS = min(paperTarget, 1000/mspt)
	}
	return tpsValid(r)
}

// tpsDetector remembers per server which command works. The zero value is ready; a new
// revision (version or type change) starts the detection over.
type tpsDetector struct {
	revision int64
	fixed    string // "" while unknown
	tqGood   int
	tqBad    int
	msGood   int
	msBad    int
}

// command returns the next command to send; ok=false means this server has no TPS source.
func (d *tpsDetector) command(serverType string, revision int64) (string, bool) {
	if revision != d.revision {
		*d = tpsDetector{revision: revision}
	}
	paperish := serverType == string(instancespec.TypePaper) || serverType == string(instancespec.TypePurpur)
	switch {
	case d.fixed == TPSSourceTickQuery:
		return cmdTickQuery, true
	case d.fixed == TPSSourcePaper:
		return cmdMSPT, true
	case d.fixed == TPSSourceNone:
		return "", false
	case d.tqBad < detectAfter:
		return cmdTickQuery, true
	case paperish && d.msBad < detectAfter:
		return cmdMSPT, true
	default:
		d.fixed = TPSSourceNone
		return "", false
	}
}

// observe parses the answer of cmd and advances the detection.
func (d *tpsDetector) observe(cmd, out string) (tpsReading, bool) {
	switch cmd {
	case cmdTickQuery:
		r, ok := parseTickQuery(out)
		if d.fixed == "" {
			if ok {
				d.tqGood++
				if d.tqGood >= detectAfter {
					d.fixed = TPSSourceTickQuery
				}
			} else {
				d.tqBad++
			}
		}
		return r, ok
	case cmdMSPT:
		r, ok := parseMSPT(out)
		if d.fixed == "" {
			if ok {
				d.msGood++
				if d.msGood >= detectAfter {
					d.fixed = TPSSourcePaper
				}
			} else {
				d.msBad++
			}
		}
		return r, ok
	}
	return tpsReading{}, false
}

func (d *tpsDetector) source() string {
	if d.fixed == "" {
		return TPSSourceUnknown
	}
	return d.fixed
}
```

- [ ] **Step 5: Host implementieren**

`internal/monitoring/host.go`:

```go
package monitoring

// hostReader reads the host values of spec §3.4. proc is "/proc" in production; tests point
// it at testdata. disk is instances_dir ("" = no disk values).
type hostReader struct {
	proc   string
	disk   string
	prev   *cpuTimes
	statfs func(dir string) (free, total uint64, err error)
}

type cpuTimes struct{ busy, total uint64 }

func newHostReader(instancesDir string) *hostReader {
	return &hostReader{proc: "/proc", disk: instancesDir, statfs: statfsBytes}
}

func (h *hostReader) read() HostSample {
	s := h.readProc() // platform part: CPU, RAM, load
	if h.disk != "" {
		if free, total, err := h.statfs(h.disk); err == nil && total > 0 {
			f, t := int64(free), int64(total)
			s.DiskFree, s.DiskTotal = &f, &t
		}
	}
	return s
}
```

`internal/monitoring/host_linux.go`:

```go
//go:build linux

package monitoring

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func statfsBytes(dir string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}

// readSmall reads a /proc file (they are small; 64 KiB is plenty).
func (h *hostReader) readSmall(name string) []byte {
	f, err := os.Open(filepath.Join(h.proc, name)) //nolint:gosec // G304: fixed names under the configured /proc root
	if err != nil {
		return nil
	}
	defer f.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(io.LimitReader(f, 64<<10))
	return b.Bytes()
}

func (h *hostReader) readProc() HostSample {
	var s HostSample
	if cur, ok := parseCPU(h.readSmall("stat")); ok {
		if h.prev != nil && cur.total > h.prev.total && cur.busy >= h.prev.busy {
			p := 100 * float64(cur.busy-h.prev.busy) / float64(cur.total-h.prev.total)
			s.CPUPercent = &p
		}
		h.prev = &cur
	} else {
		h.prev = nil
	}
	if total, avail, ok := parseMeminfo(h.readSmall("meminfo")); ok && avail <= total {
		t, u := int64(total), int64(total-avail)
		s.MemoryTotal, s.MemoryUsed = &t, &u
	}
	if f := strings.Fields(string(h.readSmall("loadavg"))); len(f) > 0 {
		if v, err := strconv.ParseFloat(f[0], 64); err == nil && v >= 0 {
			s.Load1 = &v
		}
	}
	return s
}

// parseCPU sums the first "cpu" line: idle = idle + iowait, total = the first eight fields
// (guest time is already part of user).
func parseCPU(b []byte) (cpuTimes, bool) {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 9 || f[0] != "cpu" {
			continue
		}
		var v [8]uint64
		for i := range v {
			n, err := strconv.ParseUint(f[i+1], 10, 64)
			if err != nil {
				return cpuTimes{}, false
			}
			v[i] = n
		}
		var total uint64
		for _, n := range v {
			total += n
		}
		idle := v[3] + v[4]
		return cpuTimes{busy: total - idle, total: total}, true
	}
	return cpuTimes{}, false
}

// parseMeminfo returns MemTotal and MemAvailable in bytes.
func parseMeminfo(b []byte) (total, avail uint64, ok bool) {
	var gotT, gotA bool
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 {
			continue
		}
		n, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total, gotT = n*1024, true
		case "MemAvailable:":
			avail, gotA = n*1024, true
		}
	}
	return total, avail, gotT && gotA
}
```

`internal/monitoring/host_other.go`:

```go
//go:build !linux

package monitoring

import "golang.org/x/sys/unix"

// macOS (the only other build target, see make check): only the disk values (spec §3.4).
func statfsBytes(dir string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}

func (h *hostReader) readProc() HostSample { return HostSample{} }
```

- [ ] **Step 6: Grün prüfen und Testdaten auf der VM nachprüfen**

Run: `make check`, `make test-vm SUITE=go` (bzw. `go test -race ./internal/monitoring -run 'TestParse|TestDetector|TestHost'`).
Expected: PASS. Danach die RCON-Ausgaben auf der Test-VM ansehen (Abschnitt „Testdaten nachprüfen“); weichen sie ab, die Dateien unter `testdata/rcon/` durch die echte Ausgabe ersetzen und die Tests erneut laufen lassen.

- [ ] **Step 7: Commit**

```bash
git add internal/monitoring/tps.go internal/monitoring/tps_test.go internal/monitoring/host*.go internal/monitoring/testdata
git commit -m "feat(monitoring): tps detection and parsers, host reader

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 6: Sammel-Durchlauf, Live-Modus und Schreiben

**Files:**
- Modify (ersetzen, Stub aus Phase A): `internal/monitoring/collector.go`
- Create: `internal/monitoring/write.go`
- Test: `internal/monitoring/collector_test.go`, `internal/monitoring/write_test.go`

**Interfaces:**
- Consumes: Aufgaben 4–5 (`slpPing`, `tpsDetector`, `newHostReader`), Phase A (`Deps`, `Snapshot`, `ServerSample`, `HostSample`), Store-Queries (V2).
- Produces (V4): `newCollector(d Deps) *collector`, `(*collector).Run(ctx, onPass)`, `Latest() Snapshot`, `Watch(id)`, `Live(id) (ServerSample, bool)`; zusätzlich (paketintern, für Tests und B2) `(*collector).pass(ctx) Snapshot`; `write.go`: `func writeSnapshot(ctx context.Context, db *sql.DB, s Snapshot) error` (Metrik-Konstanten aus `types.go`).

- [ ] **Step 1: Tests für das Schreiben (rot)**

`internal/monitoring/write_test.go`:

```go
package monitoring

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func cF64(v float64) *float64 { return &v }
func cI64(v int64) *int64     { return &v }
func cBp(v bool) *bool         { return &v }

// countRaw counts the raw rows of one series.
func cCountRaw(t *testing.T, db *sql.DB, series int64) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM metrics_raw WHERE series = ?", series).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func cCountMetric(t *testing.T, db *sql.DB, series int64, metric string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM metrics_raw WHERE series = ? AND metric = ?`, series, metric).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWriteSnapshotOnlySetValues(t *testing.T) {
	db := storetest.Open(t)
	at := time.UnixMilli(1_800_000_000_000)
	s := Snapshot{At: at, Agent: true, Docker: true,
		Servers: []ServerSample{
			{ID: 7, Up: cBp(true), Players: cIntp(3), PlayersMax: cIntp(20), TPS: cF64(19.9), MSPT: cF64(4.2),
				CPUPercent: cF64(12.5), MemoryBytes: cI64(1 << 30), MemoryLimit: cI64(4 << 30)},
			{ID: 8, Up: cBp(false)}, // running, ping failed: only up=0
			{ID: 9},                // stopped: nothing
		},
		Host: HostSample{CPUPercent: cF64(33), DiskFree: cI64(10), DiskTotal: cI64(100)},
	}
	if err := writeSnapshot(context.Background(), db, s); err != nil {
		t.Fatal(err)
	}
	for series, want := range map[int64]int{7: 8, 8: 1, 9: 0, HostSeries: 3} {
		if got := cCountRaw(t, db, series); got != want {
			t.Errorf("series %d: %d rows, want %d", series, got, want)
		}
	}
	var up float64
	if err := db.QueryRowContext(context.Background(),
		"SELECT value FROM metrics_raw WHERE series = 8 AND metric = 'up' AND at = ?", at.UnixMilli()).Scan(&up); err != nil || up != 0 {
		t.Fatalf("up of server 8 = %v (%v), want 0", up, err)
	}
}

func TestWriteSnapshotTwiceReplaces(t *testing.T) {
	db := storetest.Open(t)
	s := Snapshot{At: time.UnixMilli(1_800_000_000_000), Servers: []ServerSample{{ID: 7, Players: cIntp(1)}}}
	_ = writeSnapshot(context.Background(), db, s)
	s.Servers[0].Players = cIntp(2)
	if err := writeSnapshot(context.Background(), db, s); err != nil {
		t.Fatal(err)
	}
	if got := cCountRaw(t, db, 7); got != 1 {
		t.Fatalf("rows = %d, the same at must replace", got)
	}
}
```

- [ ] **Step 2: Tests für den Durchlauf (rot)**

`internal/monitoring/collector_test.go`:

```go
package monitoring

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

// cAgent is a fake InstancesClient.
type cAgent struct {
	mu   sync.Mutex
	resp agentapi.InstancesResponse
	err  error
}

func (a *cAgent) Instances(context.Context) (agentapi.InstancesResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.resp, a.err
}
func (a *cAgent) Ensure(context.Context, agentapi.EnsureRequest) error             { return nil }
func (a *cAgent) Power(context.Context, int64, agentapi.PowerAction) error          { return nil }
func (a *cAgent) Remove(context.Context, int64) error                               { return nil }

// cConsole is a fake ConsoleClient: out maps a command to its answer, calls counts them.
type cConsole struct {
	mu    sync.Mutex
	out   map[string]string
	err   error
	delay time.Duration
	calls []string
	busy  map[int64]bool
	overlap atomic.Bool
}

func (c *cConsole) Logs(context.Context, agentapi.LogsRequest) (agentapi.LogStream, error) {
	return nil, errors.New("not used")
}
func (c *cConsole) Players(context.Context, int64) (agentapi.PlayersResponse, error) {
	return agentapi.PlayersResponse{}, errors.New("not used")
}
func (c *cConsole) Rcon(ctx context.Context, id int64, cmd string) (string, error) {
	c.mu.Lock()
	if c.busy == nil {
		c.busy = map[int64]bool{}
	}
	if c.busy[id] {
		c.overlap.Store(true)
	}
	c.busy[id] = true
	c.calls = append(c.calls, cmd)
	out, err, delay := c.out[cmd], c.err, c.delay
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.busy[id] = false; c.mu.Unlock() }()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return out, err
}

type cGate map[int64]bool

func (g cGate) DataJobRunning(_ context.Context, id int64) bool { return g[id] }

// cEnv is a collector with fakes, a migrated DB and two servers (paper "survival" running,
// vanilla "creative" stopped) under the base domain mc.example.com.
type cEnv struct {
	t       *testing.T
	db      *sql.DB
	clock   *clock.Fake
	agent   *cAgent
	console *cConsole
	c       *collector
	pings   atomic.Int32
	ids     map[string]int64
}

func newCEnv(t *testing.T) *cEnv {
	t.Helper()
	db := storetest.Open(t)
	e := &cEnv{t: t, db: db, clock: clock.NewFake(time.UnixMilli(1_800_000_000_000)),
		agent: &cAgent{}, console: &cConsole{out: map[string]string{
			cmdTickQuery: "Target tick rate: 20.0 per second.\nAverage time per tick: 4.0ms (Target: 50.0ms)",
		}}, ids: map[string]int64{}}
	q := store.New(db)
	ctx := context.Background()
	for _, srv := range []struct{ name, typ string }{{"survival", "paper"}, {"creative", "vanilla"}} {
		id := storetest.CreateServer(t, db, srv.name)
		if _, err := q.ServersCreateInstance(ctx, store.ServersCreateInstanceParams{
			ServerID: id, Type: srv.typ, Version: "1.21.4", Java: "java21", MemoryMib: 2048, Properties: "{}",
		}); err != nil {
			t.Fatal(err)
		}
		e.ids[srv.name] = id
	}
	if err := q.ServersUpdateSettings(ctx, store.ServersUpdateSettingsParams{
		BaseDomain: sql.NullString{String: "mc.example.com", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	e.agent.resp = agentapi.InstancesResponse{Docker: agentapi.DockerOK, Instances: []agentapi.InstanceStatus{
		{ID: e.ids["survival"], State: agentapi.StateRunning, StartedAt: 1_799_999_000_000, RestartCount: 2,
			CPUPercent: 12.5, MemoryBytes: 1 << 30, MemoryLimitBytes: 4 << 30, Revision: 1},
		{ID: e.ids["creative"], State: agentapi.StateExited},
	}}
	e.c = newCollector(Deps{DB: db, Clock: e.clock, Instances: e.agent, Console: e.console,
		Gate: cGate{}, PingAddr: "127.0.0.1:25565"})
	e.c.ping = func(_ context.Context, addr, host string) (slpResult, error) {
		e.pings.Add(1)
		if addr != "127.0.0.1:25565" || host != "survival.mc.example.com" {
			t.Errorf("ping %s %s", addr, host)
		}
		return slpResult{Online: cIntp(2), Max: cIntp(20), Names: []string{"Alice", "Bob"}}, nil
	}
	e.c.host = &hostReader{statfs: func(string) (uint64, uint64, error) { return 0, 0, errors.New("none") }}
	return e
}

func (e *cEnv) sample(s Snapshot, name string) ServerSample {
	e.t.Helper()
	for _, x := range s.Servers {
		if x.ID == e.ids[name] {
			return x
		}
	}
	e.t.Fatalf("no sample for %s in %+v", name, s.Servers)
	return ServerSample{}
}

func TestPassSamplesRunningAndStoppedServers(t *testing.T) {
	e := newCEnv(t)
	s := e.c.pass(context.Background())
	if !s.Agent || !s.Docker || !s.At.Equal(e.clock.Now()) {
		t.Fatalf("snapshot head = %+v", s)
	}
	sv := e.sample(s, "survival")
	if !sv.DesiredRunning || sv.State != agentapi.StateRunning || sv.RestartCount != 2 || sv.Revision != 1 {
		t.Fatalf("survival state = %+v", sv)
	}
	if sv.Up == nil || !*sv.Up || *sv.Players != 2 || *sv.PlayersMax != 20 || len(sv.PlayerNames) != 2 {
		t.Fatalf("survival ping = %+v", sv)
	}
	if sv.TPS == nil || !cNear(*sv.TPS, 20) || !cNear(*sv.MSPT, 4) || sv.TPSSource != TPSSourceUnknown {
		t.Fatalf("survival tps = %v %v %s", sv.TPS, sv.MSPT, sv.TPSSource)
	}
	if *sv.CPUPercent != 12.5 || *sv.MemoryBytes != 1<<30 || *sv.MemoryLimit != 4<<30 {
		t.Fatalf("survival resources = %+v", sv)
	}
	cr := e.sample(s, "creative")
	if cr.Up != nil || cr.Players != nil || cr.TPS != nil || cr.CPUPercent != nil {
		t.Fatalf("stopped server must have no values: %+v", cr)
	}
	if e.pings.Load() != 1 {
		t.Fatalf("pings = %d, only the running server is pinged", e.pings.Load())
	}
	if got := cCountRaw(t, e.db, e.ids["survival"]); got != 8 {
		t.Fatalf("raw rows = %d", got)
	}
	if !e.c.Latest().At.Equal(s.At) {
		t.Fatal("Latest must return the last pass")
	}
}

func TestPassTPSOnlyEverySixtySeconds(t *testing.T) {
	e := newCEnv(t)
	first := e.sample(e.c.pass(context.Background()), "survival")
	e.clock.Advance(30 * time.Second)
	sv := e.sample(e.c.pass(context.Background()), "survival")
	if n := len(e.console.calls); n != 1 {
		t.Fatalf("rcon calls = %d after 30 s, want 1", n)
	}
	// V5: the last valid reading is carried (at most 90 s), but only fresh readings are stored
	if sv.TPS == nil || first.TPS == nil || *sv.TPS != *first.TPS {
		t.Fatalf("carried tps = %v, want %v", sv.TPS, first.TPS)
	}
	if n := cCountMetric(t, e.db, e.ids["survival"], MetricTPS); n != 1 {
		t.Fatalf("stored tps rows = %d, want 1", n)
	}
	e.clock.Advance(30 * time.Second)
	e.c.pass(context.Background())
	if n := len(e.console.calls); n != 2 {
		t.Fatalf("rcon calls = %d, want 2 in 60 s", n)
	}
}

func TestPassCarriesTPSAtMost90s(t *testing.T) {
	e := newCEnv(t)
	e.c.pass(context.Background()) // valid reading at t0
	e.console.delay = time.Hour     // from now on every TPS question times out
	e.c.rconTimeout = 50 * time.Millisecond
	e.clock.Advance(60 * time.Second)
	if sv := e.sample(e.c.pass(context.Background()), "survival"); sv.TPS == nil {
		t.Fatal("a 60-s-old reading must be carried")
	}
	e.clock.Advance(60 * time.Second)
	if sv := e.sample(e.c.pass(context.Background()), "survival"); sv.TPS != nil {
		t.Fatalf("a 120-s-old reading must not be carried: %v", *sv.TPS)
	}
	if n := cCountMetric(t, e.db, e.ids["survival"], MetricTPS); n != 1 {
		t.Fatalf("stored tps rows = %d, want only the fresh one", n)
	}
}

func TestPassNoTPSWhenPingFails(t *testing.T) {
	e := newCEnv(t)
	e.c.ping = func(context.Context, string, string) (slpResult, error) { return slpResult{}, errSLP }
	s := e.c.pass(context.Background())
	sv := e.sample(s, "survival")
	if sv.Up == nil || *sv.Up || sv.TPS != nil || len(e.console.calls) != 0 {
		t.Fatalf("failed ping: up=%v tps=%v calls=%v", sv.Up, sv.TPS, e.console.calls)
	}
	if got := cCountRaw(t, e.db, e.ids["survival"]); got != 4 { // up, cpu_percent, memory_bytes, memory_limit_bytes
		t.Fatalf("raw rows = %d, want 4", got)
	}
}

func TestPassWithoutPingAddr(t *testing.T) {
	e := newCEnv(t)
	e.c.d.PingAddr = ""
	s := e.c.pass(context.Background())
	if sv := e.sample(s, "survival"); sv.Up != nil || e.pings.Load() != 0 {
		t.Fatalf("no ping address: up=%v pings=%d", sv.Up, e.pings.Load())
	}
}

func TestPassAgentDown(t *testing.T) {
	e := newCEnv(t)
	e.agent.err = agentapi.ErrUnavailable
	s := e.c.pass(context.Background())
	if s.Agent || len(s.Servers) != 0 {
		t.Fatalf("agent down: %+v", s)
	}
	if cCountRaw(t, e.db, e.ids["survival"]) != 0 {
		t.Fatal("no server values without the agent")
	}
}

func TestPassDockerDown(t *testing.T) {
	e := newCEnv(t)
	e.agent.resp = agentapi.InstancesResponse{Docker: agentapi.DockerUnavailable}
	s := e.c.pass(context.Background())
	if !s.Agent || s.Docker {
		t.Fatalf("docker down: agent=%v docker=%v", s.Agent, s.Docker)
	}
	if sv := e.sample(s, "survival"); sv.State != agentapi.StateMissing || sv.Up != nil {
		t.Fatalf("unknown state must not look like running: %+v", sv)
	}
}

func TestPassGate(t *testing.T) {
	e := newCEnv(t)
	e.c.d.Gate = cGate{e.ids["survival"]: true}
	if sv := e.sample(e.c.pass(context.Background()), "survival"); !sv.Gated {
		t.Fatal("Gated must reflect the data gate")
	}
}

func TestPassRCONHangDoesNotBlockLongerThanTheFrist(t *testing.T) {
	e := newCEnv(t)
	e.console.delay = time.Hour
	e.c.rconTimeout = 50 * time.Millisecond
	start := time.Now()
	s := e.c.pass(context.Background())
	if time.Since(start) > 2*time.Second {
		t.Fatalf("pass took %v", time.Since(start))
	}
	if sv := e.sample(s, "survival"); sv.TPS != nil {
		t.Fatal("hanging RCON must leave TPS unset")
	}
}

func TestPassPingsAtMostFourAtOnce(t *testing.T) {
	e := newCEnv(t)
	q := store.New(e.db)
	var inst []agentapi.InstanceStatus
	for i := range 9 {
		id := storetest.CreateServer(t, e.db, "s"+string(rune('a'+i)))
		_, _ = q.ServersCreateInstance(context.Background(), store.ServersCreateInstanceParams{
			ServerID: id, Type: "vanilla", Version: "1.21.4", Java: "java21", MemoryMib: 1024, Properties: "{}"})
		inst = append(inst, agentapi.InstanceStatus{ID: id, State: agentapi.StateRunning})
	}
	e.agent.resp.Instances = inst
	var cur, peak atomic.Int32
	e.c.ping = func(context.Context, string, string) (slpResult, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		cur.Add(-1)
		return slpResult{}, nil
	}
	e.c.pass(context.Background())
	if peak.Load() > 4 {
		t.Fatalf("peak concurrent pings = %d", peak.Load())
	}
}

func TestLiveModeAsksTPSEveryFiveSeconds(t *testing.T) {
	e := newCEnv(t)
	id := e.ids["survival"]
	e.c.pass(context.Background())
	e.c.Watch(id)
	e.clock.Advance(5 * time.Second)
	e.c.liveTick(context.Background())
	e.clock.Advance(5 * time.Second)
	e.c.liveTick(context.Background())
	if n := len(e.console.calls); n != 3 {
		t.Fatalf("rcon calls = %d, want 1 (pass) + 2 (live)", n)
	}
	sv, ok := e.c.Live(id)
	if !ok || sv.TPS == nil {
		t.Fatalf("live = %+v %v", sv, ok)
	}
	e.clock.Advance(liveWindow) // nobody looked for 30 s
	e.c.liveTick(context.Background())
	if n := len(e.console.calls); n != 3 {
		t.Fatalf("live mode must end after %v without Watch: %d calls", liveWindow, n)
	}
	if _, ok := e.c.Live(9999); ok {
		t.Fatal("unknown server must report ok=false")
	}
}

func TestLiveAndPassNeverOverlapPerServer(t *testing.T) {
	e := newCEnv(t)
	id := e.ids["survival"]
	e.console.delay = 30 * time.Millisecond
	e.c.pass(context.Background())
	e.c.Watch(id)
	e.clock.Advance(60 * time.Second)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); e.c.pass(context.Background()) }()
	go func() { defer wg.Done(); e.c.liveTick(context.Background()) }()
	wg.Wait()
	if e.console.overlap.Load() {
		t.Fatal("two RCON calls for one server at the same time")
	}
}

func TestDetectionFixesAfterThreeAnswers(t *testing.T) {
	e := newCEnv(t)
	for range 3 {
		e.c.pass(context.Background())
		e.clock.Advance(tpsEvery)
	}
	if sv := e.sample(e.c.pass(context.Background()), "survival"); sv.TPSSource != TPSSourceTickQuery {
		t.Fatalf("source = %s", sv.TPSSource)
	}
}

func TestRunSkipsATickAfterALongPass(t *testing.T) {
	e := newCEnv(t)
	e.c.every = 20 * time.Millisecond
	var passes atomic.Int32
	e.c.ping = func(context.Context, string, string) (slpResult, error) {
		if passes.Add(1) == 1 {
			time.Sleep(70 * time.Millisecond) // overruns more than one tick
		}
		return slpResult{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Millisecond)
	defer cancel()
	var calls atomic.Int32
	e.c.Run(ctx, func(context.Context, Snapshot) { calls.Add(1) })
	if n := calls.Load(); n > 3 {
		t.Fatalf("passes = %d: a long pass must not be followed by a burst", n)
	}
}
```

- [ ] **Step 3: Rot prüfen**

Run: `make check`
Expected: FAIL (`writeSnapshot`, `collector.pass`, `liveTick`, Felder `ping`, `host`, `every`, `rconTimeout`, `d` fehlen).

- [ ] **Step 4: `write.go` implementieren**

```go
package monitoring

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Bernotese/mc-panel/internal/store"
)

// Metric names and HostSeries come from types.go (phase A, V5).

// writeSnapshot stores every set value of one pass in one transaction (unset = gap, V5).
func writeSnapshot(ctx context.Context, db *sql.DB, s Snapshot) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := store.New(tx)
	at := s.At.UnixMilli()
	put := func(series int64, metric string, v float64) error {
		if err := q.MonitoringInsertRaw(ctx, store.MonitoringInsertRawParams{Series: series, Metric: metric, At: at, Value: v}); err != nil {
			return fmt.Errorf("monitoring: write %s/%d: %w", metric, series, err)
		}
		return nil
	}
	for _, sv := range s.Servers {
		vals := map[string]*float64{
			MetricPlayers: intf(sv.Players), MetricPlayersMax: intf(sv.PlayersMax),
			MetricTPS: sv.TPS, MetricMSPT: sv.MSPT, MetricCPU: sv.CPUPercent,
			MetricMemory: int64f(sv.MemoryBytes), MetricMemoryLimit: int64f(sv.MemoryLimit),
		}
		if sv.Up != nil {
			up := 0.0
			if *sv.Up {
				up = 1
			}
			vals[MetricUp] = &up
		}
		for m, v := range vals {
			if v != nil {
				if err := put(sv.ID, m, *v); err != nil {
					return err
				}
			}
		}
	}
	h := s.Host
	for m, v := range map[string]*float64{
		MetricCPU: h.CPUPercent, MetricLoad1: h.Load1, MetricMemUsed: int64f(h.MemoryUsed),
		MetricMemTotal: int64f(h.MemoryTotal), MetricDiskFree: int64f(h.DiskFree), MetricDiskTotal: int64f(h.DiskTotal),
	} {
		if v != nil {
			if err := put(HostSeries, m, *v); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func intf(p *int) *float64 {
	if p == nil {
		return nil
	}
	v := float64(*p)
	return &v
}

func int64f(p *int64) *float64 {
	if p == nil {
		return nil
	}
	v := float64(*p)
	return &v
}
```

- [ ] **Step 5: `collector.go` implementieren (Stub ersetzen)**

```go
package monitoring

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/instancespec"
	"github.com/Bernotese/mc-panel/internal/store"
)

// collectEvery, tpsEvery, liveTPSEvery, liveWindow and maxParallelPings come from types.go
// (phase A).
const (
	statusTimeout = 10 * time.Second
	rconTimeout   = 10 * time.Second
	liveFresh     = 10 * time.Second // a 5-s live reading counts as current this long
	tpsCarry      = 90 * time.Second // the last valid TPS/MSPT is carried this long (V5)
)

// collector runs the passes of spec §3.1. All state is guarded by mu; RCON per server is
// serialised by rconLocks so a pass and the live loop never ask the same server at once.
type collector struct {
	d           Deps
	log         *slog.Logger
	ping        func(ctx context.Context, addr, host string) (slpResult, error)
	host        *hostReader
	every       time.Duration
	rconTimeout time.Duration

	mu        sync.Mutex
	latest    Snapshot
	detectors map[int64]*tpsDetector
	lastTPS   map[int64]time.Time
	tps       map[int64]liveTPS // last TPS sample per server (pass or live)
	watch     map[int64]time.Time
	rconLocks map[int64]*sync.Mutex
}

type liveTPS struct {
	at     time.Time
	r      tpsReading
	source string
}

func newCollector(d Deps) *collector {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &collector{
		d: d, log: log, ping: slpPing, host: newHostReader(d.InstancesDir),
		every: collectEvery, rconTimeout: rconTimeout,
		detectors: map[int64]*tpsDetector{}, lastTPS: map[int64]time.Time{},
		tps: map[int64]liveTPS{}, watch: map[int64]time.Time{}, rconLocks: map[int64]*sync.Mutex{},
	}
}

// Run passes every 30 s (one right away) and runs the live loop; it returns when ctx ends.
func (c *collector) Run(ctx context.Context, onPass func(context.Context, Snapshot)) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); c.runLive(ctx) }()
	defer wg.Wait()
	t := time.NewTicker(c.every)
	defer t.Stop()
	for {
		s := c.pass(ctx)
		if ctx.Err() != nil {
			return
		}
		if onPass != nil {
			onPass(ctx, s)
		}
		select { // a pass that overran drops the buffered tick (no burst, deviation 5)
		case <-t.C:
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *collector) runLive(ctx context.Context) {
	t := time.NewTicker(liveTPSEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.liveTick(ctx)
		}
	}
}

// pass collects, stores and returns one snapshot.
func (c *collector) pass(ctx context.Context) Snapshot {
	now := c.d.Clock.Now()
	s := Snapshot{At: now}
	s.Servers, s.Agent, s.Docker = c.servers(ctx, now)
	s.Host = c.host.read()
	if err := writeSnapshot(ctx, c.d.DB, s); err != nil && ctx.Err() == nil {
		c.log.ErrorContext(ctx, "monitoring: write samples", "err", err)
	}
	c.mu.Lock()
	c.carryLocked(&s, now) // after writing: only fresh readings are stored (V5)
	c.latest = s
	c.mu.Unlock()
	return s
}

// carryLocked gives servers that were not asked for TPS in this pass the last valid reading
// (pass or live) if it is at most tpsCarry old, so Latest and Live do not flip between a
// value and null every other pass (V5).
func (c *collector) carryLocked(s *Snapshot, now time.Time) {
	for i := range s.Servers {
		sv := &s.Servers[i]
		if sv.TPS != nil || sv.Up == nil || !*sv.Up {
			continue
		}
		if t, ok := c.tps[sv.ID]; ok && !now.Before(t.at) && now.Sub(t.at) <= tpsCarry {
			tps, mspt := t.r.TPS, t.r.MSPT
			sv.TPS, sv.MSPT = &tps, &mspt
		}
	}
}

type instRow struct {
	id       int64
	name     string
	typ      string
	desired  bool
	revision int64
	fqdn     string
}

func (c *collector) rows(ctx context.Context) ([]instRow, error) {
	q := store.New(c.d.DB)
	list, err := q.ServersListInstances(ctx)
	if err != nil {
		return nil, err
	}
	set, err := q.ServersGetSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]instRow, 0, len(list))
	for _, r := range list {
		row := instRow{id: r.ServerID, name: r.Name, typ: r.Type, desired: r.DesiredState == "running", revision: r.Revision}
		if set.BaseDomain.Valid {
			if f, err := instancespec.FQDN(r.Name, set.BaseDomain.String); err == nil {
				row.fqdn = f
			}
		}
		out = append(out, row)
	}
	return out, nil
}

func (c *collector) servers(ctx context.Context, now time.Time) ([]ServerSample, bool, bool) {
	rows, err := c.rows(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.log.ErrorContext(ctx, "monitoring: list instances", "err", err)
		}
		return nil, false, false
	}
	if c.d.Instances == nil {
		return nil, false, false
	}
	sctx, cancel := context.WithTimeout(ctx, statusTimeout)
	resp, err := c.d.Instances.Instances(sctx)
	cancel()
	if err != nil {
		return nil, false, false // V5: no agent answer, no server samples
	}
	docker := resp.Docker == agentapi.DockerOK
	status := map[int64]agentapi.InstanceStatus{}
	for _, st := range resp.Instances {
		status[st.ID] = st
	}
	out := make([]ServerSample, len(rows))
	for i, r := range rows {
		sv := ServerSample{ID: r.id, Name: r.name, DesiredRunning: r.desired, Revision: r.revision, State: agentapi.StateMissing}
		if c.d.Gate != nil {
			sv.Gated = c.d.Gate.DataJobRunning(ctx, r.id)
		}
		if st, ok := status[r.id]; ok && docker {
			sv.State, sv.StartedAt, sv.RestartCount = st.State, st.StartedAt, st.RestartCount
			if st.Revision > 0 {
				sv.Revision = st.Revision
			}
			if st.State == agentapi.StateRunning {
				cpu, mem, lim := st.CPUPercent, st.MemoryBytes, st.MemoryLimitBytes
				sv.CPUPercent, sv.MemoryBytes = &cpu, &mem
				if lim > 0 {
					sv.MemoryLimit = &lim
				}
			}
		}
		out[i] = sv
	}
	c.pingAll(ctx, rows, out)
	for i, r := range rows {
		if out[i].Up != nil && *out[i].Up && c.dueTPS(r.id, now) {
			c.sampleTPS(ctx, r, &out[i], now)
		}
		out[i].TPSSource = c.source(r.id)
	}
	return out, true, docker
}

func (c *collector) pingAll(ctx context.Context, rows []instRow, out []ServerSample) {
	if c.d.PingAddr == "" {
		return
	}
	sem := make(chan struct{}, maxParallelPings)
	var wg sync.WaitGroup
	for i, r := range rows {
		if out[i].State != agentapi.StateRunning || r.fqdn == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			res, err := c.ping(ctx, c.d.PingAddr, r.fqdn)
			up := err == nil
			out[i].Up = &up
			if up {
				out[i].Players, out[i].PlayersMax, out[i].PlayerNames = res.Online, res.Max, res.Names
			}
		}()
	}
	wg.Wait()
}

func (c *collector) dueTPS(id int64, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.lastTPS[id]
	return !ok || now.Sub(last) >= tpsEvery || now.Before(last)
}

// sampleTPS asks the next detection command; the answer also feeds the live view.
func (c *collector) sampleTPS(ctx context.Context, r instRow, sv *ServerSample, now time.Time) {
	reading, ok := c.askTPS(ctx, r)
	c.mu.Lock()
	c.lastTPS[r.id] = now
	c.mu.Unlock()
	if ok {
		t, m := reading.TPS, reading.MSPT
		sv.TPS, sv.MSPT = &t, &m
	}
}

func (c *collector) lockFor(id int64) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, ok := c.rconLocks[id]
	if !ok {
		l = &sync.Mutex{}
		c.rconLocks[id] = l
	}
	return l
}

func (c *collector) askTPS(ctx context.Context, r instRow) (tpsReading, bool) {
	if c.d.Console == nil {
		return tpsReading{}, false
	}
	lock := c.lockFor(r.id)
	lock.Lock()
	defer lock.Unlock()
	c.mu.Lock()
	det, ok := c.detectors[r.id]
	if !ok {
		det = &tpsDetector{}
		c.detectors[r.id] = det
	}
	cmd, ok := det.command(r.typ, r.revision)
	c.mu.Unlock()
	if !ok {
		return tpsReading{}, false
	}
	rctx, cancel := context.WithTimeout(ctx, c.rconTimeout)
	out, err := c.d.Console.Rcon(rctx, r.id, cmd)
	cancel()
	if err != nil {
		return tpsReading{}, false // RCON errors do not count for the detection
	}
	c.mu.Lock()
	reading, good := det.observe(cmd, out)
	if good {
		c.tps[r.id] = liveTPS{at: c.d.Clock.Now(), r: reading, source: det.source()}
	}
	c.mu.Unlock()
	return reading, good
}

func (c *collector) source(id int64) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, ok := c.detectors[id]; ok {
		return d.source()
	}
	return TPSSourceUnknown
}

// Watch marks a server as watched for the next 30 s (spec §6.2).
func (c *collector) Watch(id int64) {
	c.mu.Lock()
	c.watch[id] = c.d.Clock.Now().Add(liveWindow)
	c.mu.Unlock()
}

// liveTick asks TPS for every watched server that answered the last ping.
func (c *collector) liveTick(ctx context.Context) {
	now := c.d.Clock.Now()
	c.mu.Lock()
	var due []int64
	for id, until := range c.watch {
		if now.After(until) {
			delete(c.watch, id)
			continue
		}
		due = append(due, id)
	}
	latest := c.latest
	c.mu.Unlock()
	if len(due) == 0 {
		return
	}
	rows, err := c.rows(ctx)
	if err != nil {
		return
	}
	byID := map[int64]instRow{}
	for _, r := range rows {
		byID[r.id] = r
	}
	for _, id := range due {
		r, ok := byID[id]
		sv, okS := findSample(latest, id)
		if !ok || !okS || sv.Up == nil || !*sv.Up {
			continue
		}
		c.askTPS(ctx, r)
	}
}

func findSample(s Snapshot, id int64) (ServerSample, bool) {
	for _, sv := range s.Servers {
		if sv.ID == id {
			return sv, true
		}
	}
	return ServerSample{}, false
}

// Latest returns a copy of the last pass.
func (c *collector) Latest() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.latest
	s.Servers = append([]ServerSample(nil), c.latest.Servers...)
	return s
}

// Live returns the server's sample of the last pass with the freshest TPS (≤ 10 s old).
func (c *collector) Live(id int64) (ServerSample, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sv, ok := findSample(c.latest, id)
	if !ok {
		return ServerSample{}, false
	}
	if t, ok := c.tps[id]; ok && c.d.Clock.Now().Sub(t.at) <= liveFresh {
		tps, mspt := t.r.TPS, t.r.MSPT
		sv.TPS, sv.MSPT, sv.TPSSource = &tps, &mspt, t.source
	}
	return sv, true
}
```

- [ ] **Step 6: Grün prüfen**

Run: `make check`, `make test-vm SUITE=go` (Race!), zusätzlich auf der VM `go test -race -count=5 ./internal/monitoring -run 'TestPass|TestLive|TestRun|TestDetection|TestWrite'`.
Expected: PASS, stabil.

- [ ] **Step 7: Commit**

```bash
git add internal/monitoring/collector.go internal/monitoring/collector_test.go internal/monitoring/write.go internal/monitoring/write_test.go
git commit -m "feat(monitoring): collector pass, live mode and sample storage

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 7: Verdichtung und Löschen

**Files:**
- Modify (ersetzen, Stub aus Phase A): `internal/monitoring/rollup.go`
- Test: `internal/monitoring/rollup_test.go`

**Interfaces:**
- Consumes: Store-Queries (V2) `MonitoringRollup5m/1h`, `MonitoringPruneRaw/5m/1h`.
- Produces (V4): `func runRollup(ctx context.Context, d Deps)`; paketintern `type roller struct{…}`, `func (r *roller) once(ctx context.Context) error`.

- [ ] **Step 1: Tests schreiben (rot)**

`internal/monitoring/rollup_test.go`:

```go
package monitoring

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

const cMinute = int64(time.Minute / time.Millisecond)

func cInsertRaw(t *testing.T, db *sql.DB, at int64, v float64) {
	t.Helper()
	if err := store.New(db).MonitoringInsertRaw(context.Background(), store.MonitoringInsertRawParams{
		Series: 7, Metric: MetricPlayers, At: at, Value: v}); err != nil {
		t.Fatal(err)
	}
}

func cAgg(t *testing.T, db *sql.DB, table string, at int64) (avg, mn, mx float64, n int64, ok bool) {
	t.Helper()
	err := db.QueryRowContext(context.Background(),
		"SELECT avg, min, max, samples FROM "+table+" WHERE series = 7 AND metric = 'players' AND at = ?", at). //nolint:gosec // G202: table from a fixed test list
		Scan(&avg, &mn, &mx, &n)
	return avg, mn, mx, n, err == nil
}

// cBase is a full hour (Unix ms), so 5-min and 1-h boundaries are easy to read.
var cBase = time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC).UnixMilli()

func TestRollupFiveMinutes(t *testing.T) {
	db := storetest.Open(t)
	// 10:00–10:05: 0, 2, 4 …; 10:05:00 belongs to the next interval
	for i, v := range []float64{0, 2, 4, 6, 8, 10, 12, 14, 16, 18} {
		cInsertRaw(t, db, cBase+int64(i)*cMinute/2, v)
	}
	cInsertRaw(t, db, cBase+5*cMinute, 100)
	fc := clock.NewFake(time.UnixMilli(cBase + 5*cMinute + 10_000)) // 10:05:10: only 10:00–10:05 is complete
	r := newRoller(Deps{DB: db, Clock: fc})
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	avg, mn, mx, n, ok := cAgg(t, db, "metrics_5m", cBase)
	if !ok || avg != 9 || mn != 0 || mx != 18 || n != 10 {
		t.Fatalf("5m = %v %v %v %d %v", avg, mn, mx, n, ok)
	}
	if _, _, _, _, ok := cAgg(t, db, "metrics_5m", cBase+5*cMinute); ok {
		t.Fatal("the running interval must not be rolled up")
	}
}

func TestRollupIsIdempotentAndCatchesUp(t *testing.T) {
	db := storetest.Open(t)
	cInsertRaw(t, db, cBase, 4)
	fc := clock.NewFake(time.UnixMilli(cBase + 6*cMinute))
	r := newRoller(Deps{DB: db, Clock: fc})
	_ = r.once(context.Background())
	cInsertRaw(t, db, cBase+cMinute, 8) // late sample in an already rolled-up interval
	_ = r.once(context.Background())
	if avg, _, _, n, _ := cAgg(t, db, "metrics_5m", cBase); avg != 6 || n != 2 {
		t.Fatalf("recomputed 5m = %v (%d samples)", avg, n)
	}
}

func TestRollupHourWeighted(t *testing.T) {
	db := storetest.Open(t)
	// two 5-min rows: avg 10 over 10 samples, avg 40 over 2 samples → weighted 15
	for _, r := range []struct {
		at  int64
		avg float64
		n   int64
	}{{cBase, 10, 10}, {cBase + 5*cMinute, 40, 2}} {
		if _, err := db.ExecContext(context.Background(),
			"INSERT INTO metrics_5m (series, metric, at, avg, min, max, samples) VALUES (7, 'players', ?, ?, ?, ?, ?)",
			r.at, r.avg, r.avg-1, r.avg+1, r.n); err != nil {
			t.Fatal(err)
		}
	}
	fc := clock.NewFake(time.UnixMilli(cBase + 61*cMinute))
	if err := newRoller(Deps{DB: db, Clock: fc}).once(context.Background()); err != nil {
		t.Fatal(err)
	}
	avg, mn, mx, n, ok := cAgg(t, db, "metrics_1h", cBase)
	if !ok || avg != 15 || mn != 9 || mx != 41 || n != 12 {
		t.Fatalf("1h = %v %v %v %d %v", avg, mn, mx, n, ok)
	}
}

func TestPruneKeepsWindows(t *testing.T) {
	db := storetest.Open(t)
	now := cBase + 40*24*60*cMinute
	cInsertRaw(t, db, now-25*60*cMinute, 1) // older than 24 h: gone
	cInsertRaw(t, db, now-23*60*cMinute, 1) // stays
	for _, x := range []struct {
		table string
		age   int64 // minutes
	}{{"metrics_5m", 8 * 24 * 60}, {"metrics_5m", 6 * 24 * 60}, {"metrics_1h", 31 * 24 * 60}, {"metrics_1h", 29 * 24 * 60}} {
		if _, err := db.ExecContext(context.Background(),
			"INSERT INTO "+x.table+" (series, metric, at, avg, min, max, samples) VALUES (7, 'cpu_percent', ?, 1, 1, 1, 1)", //nolint:gosec // G202: fixed test tables
			now-x.age*cMinute); err != nil {
			t.Fatal(err)
		}
	}
	r := newRoller(Deps{DB: db, Clock: clock.NewFake(time.UnixMilli(now))})
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	count := func(query string) int {
		var n int
		if err := db.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count("SELECT count(*) FROM metrics_raw"); n != 1 {
		t.Errorf("raw rows = %d, want 1", n)
	}
	if n := count("SELECT count(*) FROM metrics_5m WHERE metric = 'cpu_percent'"); n != 1 {
		t.Errorf("5m rows = %d, want 1", n)
	}
	if n := count("SELECT count(*) FROM metrics_1h WHERE metric = 'cpu_percent'"); n != 1 {
		t.Errorf("1h rows = %d, want 1", n)
	}
}

func TestRollupSurvivesClockGoingBack(t *testing.T) {
	db := storetest.Open(t)
	cInsertRaw(t, db, cBase, 2)
	fc := clock.NewFake(time.UnixMilli(cBase + 10*cMinute))
	r := newRoller(Deps{DB: db, Clock: fc})
	_ = r.once(context.Background())
	fc.Set(time.UnixMilli(cBase - 60*cMinute)) // clock jumps back an hour
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if avg, _, _, _, ok := cAgg(t, db, "metrics_5m", cBase); !ok || avg != 2 {
		t.Fatal("a clock jump must not lose rolled-up intervals")
	}
}
```

- [ ] **Step 2: Rot prüfen**

Run: `make check`
Expected: FAIL (`newRoller`, `roller.once` fehlen).

- [ ] **Step 3: Implementieren (Stub ersetzen)**

`internal/monitoring/rollup.go`:

```go
package monitoring

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

// rollupEvery, retainRaw, retain5m and retain1h come from types.go (phase A).
const (
	window5m = 30 * time.Minute // recomputed on every run: late samples and missed runs
	window1h = 3 * time.Hour
)

// roller rolls raw samples up to 5-min and 1-h rows and prunes old rows (spec §4). Every run
// recomputes a window of complete intervals (INSERT OR REPLACE: idempotent); the first run
// after a start covers the whole retention of the source table.
type roller struct {
	d     Deps
	log   *slog.Logger
	first bool
}

func newRoller(d Deps) *roller {
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	return &roller{d: d, log: log, first: true}
}

// runRollup runs once right away and then every 5 minutes until ctx is done.
func runRollup(ctx context.Context, d Deps) {
	r := newRoller(d)
	t := time.NewTicker(rollupEvery)
	defer t.Stop()
	for {
		if err := r.once(ctx); err != nil && ctx.Err() == nil {
			r.log.ErrorContext(ctx, "monitoring: rollup", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (r *roller) once(ctx context.Context) error {
	q := store.New(r.d.DB)
	now := r.d.Clock.Now()
	to5 := now.Truncate(5 * time.Minute)
	to1 := now.Truncate(time.Hour)
	from5, from1 := to5.Add(-window5m), to1.Add(-window1h)
	if r.first {
		from5, from1 = now.Add(-retainRaw), now.Add(-retain5m)
	}
	err := errors.Join(
		q.MonitoringRollup5m(ctx, store.MonitoringRollup5mParams{FromAt: from5.UnixMilli(), ToAt: to5.UnixMilli()}),
		q.MonitoringRollup1h(ctx, store.MonitoringRollup1hParams{FromAt: from1.UnixMilli(), ToAt: to1.UnixMilli()}),
		q.MonitoringPruneRaw(ctx, now.Add(-retainRaw).UnixMilli()),
		q.MonitoringPrune5m(ctx, now.Add(-retain5m).UnixMilli()),
		q.MonitoringPrune1h(ctx, now.Add(-retain1h).UnixMilli()),
	)
	if err == nil {
		r.first = false
	}
	return err
}
```

Hinweise:
- `time.Truncate` rundet auf Vielfache seit der Zeit-Null in UTC; 5 min und 1 h teilen 24 h, die Intervallgrenzen stimmen daher mit `(at / 300000) * 300000` in SQL überein.
- Reihenfolge Rollup vor Prune: ein Rohwert wird nie gelöscht, bevor sein Intervall verdichtet ist (Rohwerte leben 24 h, das 5-min-Fenster ist 30 min).
- Geht die Uhr zurück, liegen `to5`/`to1` in der Vergangenheit; bereits verdichtete Zeilen bleiben, nichts wird doppelt (Primärschlüssel).

- [ ] **Step 4: Grün prüfen**

Run: `make check`, `make test-vm SUITE=go` (bzw. `go test -race ./internal/monitoring -run 'TestRollup|TestPrune'`).
Expected: PASS.

- [ ] **Step 5: Gesamtlauf, Lint, Push, PR**

Run: `make check`, `make test-vm` (alle Suites; golangci-lint ohne Befund – gosec-Ausnahmen nur mit Begründung auf der Zeile).
Dann `git push -u origin feat/monitoring-collector`, Draft-PR „feat(monitoring): collector (sub-project 5)“ mit Zusammenfassung, Abweichungen 1–5, Ergebnis der Testdaten-Prüfung auf der VM; `gh pr checks <n> --watch` bis `CI OK`; beim Lead melden.

- [ ] **Step 6: Commit**

```bash
git add internal/monitoring/rollup.go internal/monitoring/rollup_test.go
git commit -m "feat(monitoring): roll up and prune samples

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

## Self-Review

- **Spec-Abdeckung:** §3.1 Takte, Parallelität, Auslassen → Aufgabe 6; §3.2 → Aufgabe 4; §3.3 → Aufgabe 5 (mit Abweichungen 1–2); §3.4 → Aufgabe 5; §4 Schreiben/Verdichtung/Löschen → Aufgaben 6–7; §6.2 Live-Modus → Aufgabe 6. Löschen der Reihen eines gelöschten Servers liegt bei Phase A (`servers`), Alerts und Routen bei B2.
- **V5:** `Agent = false` → keine Server-Samples (TestPassAgentDown); `Up` nil/true/false wie verlangt (TestPassWithoutPingAddr, TestPassNoTPSWhenPingFails); gestoppte Server mit `State`/`DesiredRunning`/`Gated` und ohne Messwerte (TestPassSamplesRunningAndStoppedServers, TestPassGate); nur gesetzte Werte geschrieben (TestWriteSnapshotOnlySetValues); `Live` nach `Watch` mit 5-s-TPS (TestLiveModeAsksTPSEveryFiveSeconds).
- **Bekannte Lücke für B2:** `Docker = false` liefert Server mit `State = missing`; B2 darf daraus keinen Down-Alert ableiten (Review Focus 2).
