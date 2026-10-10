# Stream C – Agent – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Den privilegierten `agent` bauen: HTTP-über-Unix-Socket-Server mit Peer-UID-Prüfung, strikter JSON-Validierung und den vier `agentapi`-Endpunkten; Firewall-Backends `nftables` (Linux) und `noop` (macOS/Dev); Backend-Auswahl per Konfiguration; den panel-seitigen Client (`agentapi.Client`) und das Binary `cmd/agent`.

**Architecture:** `cmd/agent` lädt `config.Agent`, wählt per `firewall.New` das Backend, öffnet mit `agent.Listen` den Socket (Stale-Socket-Erkennung, Rechte), umhüllt den Listener mit `peercred.Wrap` (nur `panel_uid` und `0` werden angenommen – abgelehnte Verbindungen werden geschlossen, bevor ein Byte HTTP gelesen wird) und bedient ihn mit `agent.Server`. Alle Validierungsregeln liegen einmalig in `internal/agent/banrules` und werden vom Server, vom Client und von beiden Backends benutzt. Das nftables-Backend verwaltet ausschließlich die Tabelle `inet mcpanel`; seine reine Logik (Prefix → Set-Element, Intervall → Prefix) liegt in einer Datei ohne Build-Tag und ist ohne root testbar.

**Tech Stack:** Go (≥ 1.22: range-over-int, `slices`, `cmp`, mehrfaches `%w`), `net/http` über `net.Listen("unix")`, `log/slog`, `github.com/google/nftables` (API verifiziert gegen Tag **v0.3.0**), `golang.org/x/sys/unix` (`GetsockoptUcred`, `GetsockoptXucred`, `SOL_LOCAL`, `LOCAL_PEERCRED`, `Unshare`, `Setns` – verifiziert gegen `golang/sys` master), Tests nur Standardbibliothek.

**Spec:** `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md` (Abschnitte 3, 3.1, 3.3, 3.4, 7.2, 9, 10)

## Global Constraints

Es gelten **alle** Global Constraints aus `docs/superpowers/plans/2026-10-08-foundation-00-overview.md` und die „Regeln für Streams“ aus dem Phase-0-Plan. Phase-0-Dateien (insbesondere `internal/agent/agentapi/`, `internal/config/`, `internal/clock/`, `scripts/dev.sh`, `Makefile`, `go.mod`) sind schreibgeschützt.

Stream-spezifische, verbindliche Werte:

| Thema | Wert |
|---|---|
| Eigene Pfade | `cmd/agent/**`, `internal/agent/**` außer `internal/agent/agentapi/` |
| Socket Prod | `cfg.Socket` (Default `/run/mc-panel/agent.sock`), Modus `0660`, Gruppe = primäre Gruppe von `panel_uid` (per `os/user.LookupId`) |
| Socket Dev | `cfg.Socket` (Default `./.dev/agent.sock`), Modus `0600`, kein `chown`; Elternverzeichnis wird mit `0750` angelegt |
| Erlaubte Peers | genau `panel_uid` und `0` (root) – Entscheidung siehe Self-Review |
| Stale-Socket | Nur entfernen, wenn Pfad ein Socket ist **und** `connect` mit `ECONNREFUSED` scheitert; antwortet jemand → `ErrAlreadyRunning`; Nicht-Socket/Symlink → Fehler, nichts wird gelöscht |
| Request-Body | max. **4096 Byte**, `Content-Type: application/json` Pflicht bei POST, `DisallowUnknownFields`, keine Daten nach dem JSON-Objekt |
| Timeout-Bereich | `timeout_seconds` ∈ [1, 691200] (= `agentapi.MaxBanDuration` 8 Tage); Prüfung **vor** der Multiplikation mit `time.Second` |
| Prefix-Regeln | `agentapi.ValidateBanPrefix` **plus**: kein IPv4-mapped IPv6, keine Host-Bits, kein Link-Local, kein Multicast, exakt kanonische Schreibweise (`p.String() == input`) |
| HTTP-Status | 200 (health, bans), 204 (ban, unban), 400 (Validierung/JSON), 404, 405 (+`Allow`), 409 (Überlappung), 413, 415, 500 (Firewall-Fehler, generische Meldung `firewall operation failed`) |
| Fehlerformat (Agent-Draht) | `agentapi.ErrorResponse{"error":"<lesbare Meldung>"}` – die `httpx`-Codes gelten nur für die Panel-API, nicht für den Agent-Socket |
| Server-Timeouts | ReadHeader 2 s, Read 5 s, Write 10 s, Idle 60 s, `MaxHeaderBytes` 8 KiB; Graceful Shutdown 5 s |
| Client | Timeout pro Aufruf **3 s** (`agent.DefaultTimeout`), Antwortlimit 4 MiB, kein Proxy, keine Redirects, Basis-URL `http://agent` |
| nftables | Tabelle `inet mcpanel`; Set `mcpanel_jail4` (`ipv4_addr`, `flags timeout`); Set `mcpanel_jail6` (`ipv6_addr`, `flags interval,timeout`); Chain `input` (`type filter hook input priority -10; policy accept`); Regeln `meta nfproto ipv4 ip saddr @mcpanel_jail4 counter drop` und `meta nfproto ipv6 ip6 saddr @mcpanel_jail6 counter drop` |
| Build-Tags | `linux` (nftables-Backend, `SO_PEERCRED`), `darwin` (`LOCAL_PEERCRED`), `!linux && !darwin` (Stub), `linux && nftintegration` (Integrationstest, root) |
| Logging | `slog`; Dev: Text/Debug auf stderr; Prod: JSON/Info. Jeder Ban/Unban: `msg="firewall ban"`/`"firewall unban"` mit `prefix`, `timeout_s`, `backend`, `peer_uid`; Ablehnungen: `msg="agent request rejected"` (Warn) |
| Version | `var version = "dev"` in `cmd/agent/main.go`, gesetzt per `-ldflags "-X main.version=<v>"` |

## Review Focus

1. **Stale-Socket und Doppelstart:** Ein Agent-Neustart nach `kill -9` hinterlässt die Socket-Datei; ein naives `os.Remove` würde aber auch den Socket eines **laufenden** Agents (oder eine beliebige Datei/Symlink-Ziel) entfernen. → Tests in Task 7: `TestListenReplacesStaleSocket`, `TestListenRefusesWhenAgentRunning`, `TestListenRefusesNonSocket`, `TestListenRefusesSymlink`.
2. **Prefix-Validierungs-Bypass:** `agentapi.ValidateBanPrefix` lässt `::ffff:203.0.113.7/128`, `::ffff:0:0/96` (ganzer IPv4-Raum!), `fe80::/64` (bricht IPv6-Neighbor-Discovery), Multicast und nicht maskierte Prefixe wie `2001:db8:1:2::5/64` durch; Zonen (`fe80::1%eth0`) und Großschreibung müssen am Draht scheitern. → Tests in Task 1 (`TestCheckPrefix`, `TestParsePrefix`) und Task 8 (`TestServerRejectsInvalidRequests`).
3. **Timeout-Überlauf:** `time.Duration(timeout_seconds) * time.Second` läuft bei großen Werten über und wird negativ bzw. klein. → Tests in Task 1 (`TestTimeoutFromSeconds` mit `math.MaxInt64`/`math.MinInt64`) und Task 8 (Fälle `max int64`, `overflow literal`, `exponent`, `float`).
4. **Re-Ban und Parallelität im nftables-Backend:** Ein `add element` auf ein existierendes Element ist im Kernel (vor 6.x) ein No-op – der Timeout würde **nicht** verlängert. Re-Ban muss daher „delete + add“ in **einem** Netlink-Batch sein, und Lesen→Entscheiden→Schreiben muss serialisiert sein. → Tests in Task 3 (`TestNoopRebanReplacesTimeout`, `TestNoopConcurrent`), Task 5 (`TestNFTablesRebanRefreshesTimeout`, `TestNFTablesConcurrent`, angrenzende /64 in `TestNFTablesBanListUnban`), Task 9 (`TestClientConcurrent` mit `-race`).
5. **Peer-Credentials vor HTTP:** Fremde UIDs dürfen keine einzige HTTP-Antwort bekommen (auch kein 403), und die Prüfung muss pro Verbindung beim `Accept` stattfinden. → Tests in Task 2 (`TestWrapRejectsDisallowedUID` mit injizierter Allow-Funktion) und Task 9 (`TestClientRejectedPeer`: Client sieht `agentapi.ErrUnavailable`, Log enthält `disallowed uid`).

## Dateistruktur

```
cmd/agent/main.go                          Flags (-config, -dev, -version), Config laden, Logger, Firewall-Auswahl,
                                           Socket-Rechte, Listen + peercred.Wrap, Server, Signal-Handling, Shutdown
cmd/agent/main_test.go                     run() Ende-zu-Ende (Dev), Version, Flags, socketPerms, uidFromConfig

internal/agent/banrules/banrules.go        Gemeinsame Validierung: CheckPrefix, ParsePrefix, TimeoutFromSeconds,
                                           SecondsFromDuration, RemainingSeconds, FindOverlap, ErrInvalid, ErrConflict
internal/agent/banrules/banrules_test.go

internal/agent/peercred/peercred.go        PeerUID, Wrap (Listener mit UID-Prüfung), Conn, AllowUIDs, ConnContext, FromContext
internal/agent/peercred/peercred_linux.go  SO_PEERCRED via unix.GetsockoptUcred            (//go:build linux)
internal/agent/peercred/peercred_darwin.go LOCAL_PEERCRED via unix.GetsockoptXucred        (//go:build darwin)
internal/agent/peercred/peercred_other.go  Fehler „nicht unterstützt“                      (//go:build !linux && !darwin)
internal/agent/peercred/peercred_test.go

internal/agent/firewall/firewall.go        Konstanten (Tabelle/Sets/Chain/Priorität), entry, toBans (Sortierung, Sekunden)
internal/agent/firewall/noop.go            Noop-Backend: Map Prefix→Ablauf mit clock.Clock, Logs
internal/agent/firewall/noop_test.go
internal/agent/firewall/nftkeys.go         Reine nftables-Logik ohne Build-Tag: u128, elementFor, prefixFromRange,
                                           rawElem, entriesFromElements, splitExisting
internal/agent/firewall/nftkeys_test.go
internal/agent/firewall/nftables_linux.go  NFTables-Backend (google/nftables), idempotentes Setup   (//go:build linux)
internal/agent/firewall/nftables_integration_test.go  root + eigenes Netzwerk-Namespace  (//go:build linux && nftintegration)
internal/agent/firewall/factory.go         New(cfg, clk, log), choose(name, goos), ErrUnsupported
internal/agent/firewall/factory_linux.go   newNFTables → NewNFTables                                (//go:build linux)
internal/agent/firewall/factory_other.go   newNFTables → ErrUnsupported                             (//go:build !linux)
internal/agent/firewall/factory_test.go

internal/agent/socket.go                   Listen(path, mode, gid), ErrAlreadyRunning, Stale-Erkennung
internal/agent/socket_test.go
internal/agent/server.go                   Server (http.Handler), Routing, strikte Dekodierung, Fehler-Mapping, Logging,
                                           Serve/Shutdown
internal/agent/server_test.go
internal/agent/client.go                   Client (agentapi.Client), NewClient, NewClientWithTimeout, StatusError,
                                           ErrInvalid/ErrConflict (Re-Exports)
internal/agent/client_test.go              Ende-zu-Ende über echten Unix-Socket inkl. Peer-Ablehnung, Timeout, -race
internal/agent/helpers_test.go             Test-Helfer (socketPath, selfUID, syncBuffer, testLogger, newNoop, stubFirewall)
```

Abhängigkeiten zwischen Paketen (keine Zyklen): `banrules → agentapi`; `peercred → x/sys/unix`; `firewall → banrules, agentapi, clock, config, google/nftables (nur linux)`; `agent → banrules, peercred, agentapi`; `cmd/agent → agent, firewall, peercred, clock, config`. Das Panel importiert später nur `internal/agent` (Client) und linkt dadurch **kein** `google/nftables`.

---

### Task 1: `banrules` – gemeinsame Validierung

**Files:**
- Create: `internal/agent/banrules/banrules.go`
- Test: `internal/agent/banrules/banrules_test.go`

**Interfaces:**
- Consumes: `agentapi.ValidateBanPrefix(p netip.Prefix) error`, `agentapi.MaxBanDuration`
- Produces:
```go
var ErrInvalid error  // "invalid ban request" – permanent, nicht wiederholen
var ErrConflict error // "ban overlaps an existing ban"
const MaxTimeoutSeconds = int64(agentapi.MaxBanDuration / time.Second) // 691200
func CheckPrefix(p netip.Prefix) error
func ParsePrefix(s string) (netip.Prefix, error)
func TimeoutFromSeconds(s int64) (time.Duration, error)
func SecondsFromDuration(d time.Duration) (int64, error)
func RemainingSeconds(d time.Duration) int64
func FindOverlap(p netip.Prefix, existing []netip.Prefix) (netip.Prefix, bool)
```

- [ ] **Step 1: Failing tests schreiben** – `internal/agent/banrules/banrules_test.go`

```go
package banrules_test

import (
	"errors"
	"math"
	"net/netip"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
)

func TestMaxTimeoutSeconds(t *testing.T) {
	if banrules.MaxTimeoutSeconds != 691200 {
		t.Fatalf("MaxTimeoutSeconds = %d, want 691200", banrules.MaxTimeoutSeconds)
	}
}

func TestCheckPrefix(t *testing.T) {
	ok := []string{
		"203.0.113.7/32",
		"198.51.100.255/32",
		"2001:db8:1:2::/64",
		"2001:db8::1/128",
		"2001:db8:1:2:3::/80",
	}
	bad := []string{
		"0.0.0.0/0", "203.0.113.0/24", "127.0.0.1/32", "::1/128", "::/0", "2001:db8::/48",
		"::ffff:203.0.113.7/128", // IPv4-mapped: must be sent as plain IPv4
		"::ffff:0:0/96",          // would cover the whole IPv4-mapped space
		"::ffff:127.0.0.1/128",
		"2001:db8:1:2::5/64", // host bits set
		"fe80::/64", "fe80::1/128", "169.254.1.1/32", // link-local
		"ff02::/64", "ff02::1/128", "224.0.0.1/32", // multicast
	}
	for _, s := range ok {
		if err := banrules.CheckPrefix(netip.MustParsePrefix(s)); err != nil {
			t.Errorf("CheckPrefix(%s) = %v, want nil", s, err)
		}
	}
	for _, s := range bad {
		if err := banrules.CheckPrefix(netip.MustParsePrefix(s)); !errors.Is(err, banrules.ErrInvalid) {
			t.Errorf("CheckPrefix(%s) = %v, want ErrInvalid", s, err)
		}
	}
	if err := banrules.CheckPrefix(netip.Prefix{}); !errors.Is(err, banrules.ErrInvalid) {
		t.Errorf("CheckPrefix(zero) = %v, want ErrInvalid", err)
	}
}

func TestParsePrefix(t *testing.T) {
	for _, s := range []string{"203.0.113.7/32", "2001:db8:1:2::/64", "2001:db8::1/128"} {
		p, err := banrules.ParsePrefix(s)
		if err != nil || p.String() != s {
			t.Errorf("ParsePrefix(%q) = %v, %v", s, p, err)
		}
	}
	bad := []string{
		"",
		"203.0.113.7",            // no prefix length
		"fe80::1%eth0/128",       // zone
		"2001:DB8:1:2::/64",      // not canonical (upper case)
		"2001:0db8:1:2::/64",     // not canonical (leading zero)
		" 203.0.113.7/32",        // whitespace
		"203.0.113.7/32 ",        // whitespace
		"203.0.113.07/32",        // leading zero in IPv4
		"203.0.113.7/032",        // leading zero in bits
		"::ffff:203.0.113.7/128", // mapped
		"2001:db8:1:2::5/64",     // host bits
		"203.0.113.0/24",         // too wide
	}
	for _, s := range bad {
		if _, err := banrules.ParsePrefix(s); !errors.Is(err, banrules.ErrInvalid) {
			t.Errorf("ParsePrefix(%q) = %v, want ErrInvalid", s, err)
		}
	}
}

func TestTimeoutFromSeconds(t *testing.T) {
	ok := map[int64]time.Duration{1: time.Second, 900: 15 * time.Minute, 691200: agentapi.MaxBanDuration}
	for s, want := range ok {
		if got, err := banrules.TimeoutFromSeconds(s); err != nil || got != want {
			t.Errorf("TimeoutFromSeconds(%d) = %v, %v; want %v", s, got, err, want)
		}
	}
	for _, s := range []int64{0, -1, 691201, math.MaxInt64, math.MinInt64} {
		if _, err := banrules.TimeoutFromSeconds(s); !errors.Is(err, banrules.ErrInvalid) {
			t.Errorf("TimeoutFromSeconds(%d) = %v, want ErrInvalid", s, err)
		}
	}
}

func TestSecondsFromDuration(t *testing.T) {
	ok := map[time.Duration]int64{
		time.Second:                           1,
		1500 * time.Millisecond:               2,
		15 * time.Minute:                      900,
		agentapi.MaxBanDuration:               691200,
		agentapi.MaxBanDuration - time.Nanosecond: 691200,
	}
	for d, want := range ok {
		if got, err := banrules.SecondsFromDuration(d); err != nil || got != want {
			t.Errorf("SecondsFromDuration(%v) = %d, %v; want %d", d, got, err, want)
		}
	}
	for _, d := range []time.Duration{0, 999 * time.Millisecond, -time.Second, agentapi.MaxBanDuration + time.Nanosecond, time.Duration(math.MaxInt64)} {
		if _, err := banrules.SecondsFromDuration(d); !errors.Is(err, banrules.ErrInvalid) {
			t.Errorf("SecondsFromDuration(%v) = %v, want ErrInvalid", d, err)
		}
	}
}

func TestRemainingSeconds(t *testing.T) {
	cases := map[time.Duration]int64{
		0:                             0,
		-5 * time.Second:              0,
		time.Nanosecond:               1,
		time.Second:                   1,
		1001 * time.Millisecond:       2,
		time.Duration(math.MaxInt64): 9223372037,
	}
	for d, want := range cases {
		if got := banrules.RemainingSeconds(d); got != want {
			t.Errorf("RemainingSeconds(%v) = %d, want %d", d, got, want)
		}
	}
}

func TestFindOverlap(t *testing.T) {
	existing := []netip.Prefix{netip.MustParsePrefix("2001:db8:1:2::/64"), netip.MustParsePrefix("203.0.113.7/32")}
	if q, ok := banrules.FindOverlap(netip.MustParsePrefix("2001:db8:1:2::1/128"), existing); !ok || q.String() != "2001:db8:1:2::/64" {
		t.Errorf("expected overlap with /64, got %v %v", q, ok)
	}
	for _, s := range []string{"2001:db8:1:2::/64", "2001:db8:1:3::/64", "203.0.113.8/32", "203.0.113.7/32"} {
		if q, ok := banrules.FindOverlap(netip.MustParsePrefix(s), existing); ok {
			t.Errorf("FindOverlap(%s) = %v, want none (identical prefixes are a re-ban, not an overlap)", s, q)
		}
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./internal/agent/banrules/`
Expected: FAIL – Build-Fehler, weil das Paket `github.com/Bernotese/mc-panel/internal/agent/banrules` keine Nicht-Test-Dateien enthält bzw. `banrules.CheckPrefix` undefiniert ist.

- [ ] **Step 3: Implementierung** – `internal/agent/banrules/banrules.go`

```go
// Package banrules holds the agent's validation rules for firewall bans. They
// are stricter than agentapi.ValidateBanPrefix and are shared by the agent
// server, the panel-side client and both firewall backends, so that every
// layer enforces exactly the same rules.
package banrules

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

var (
	// ErrInvalid marks requests that can never succeed (bad prefix or
	// timeout). Callers must not retry them.
	ErrInvalid = errors.New("invalid ban request")
	// ErrConflict marks a ban whose prefix overlaps a different, already
	// banned prefix (e.g. a /128 inside a banned /64).
	ErrConflict = errors.New("ban overlaps an existing ban")
)

// MaxTimeoutSeconds is agentapi.MaxBanDuration in whole seconds (691200).
const MaxTimeoutSeconds = int64(agentapi.MaxBanDuration / time.Second)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// CheckPrefix accepts exactly the prefixes the agent is willing to firewall:
// everything agentapi.ValidateBanPrefix accepts, minus IPv4-mapped IPv6
// prefixes, prefixes with host bits set, link-local and multicast prefixes.
func CheckPrefix(p netip.Prefix) error {
	if err := agentapi.ValidateBanPrefix(p); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	a := p.Addr()
	switch {
	case a.Is4In6():
		return invalidf("IPv4-mapped IPv6 prefix %s is not allowed, send the IPv4 address instead", p)
	case p != p.Masked():
		return invalidf("prefix %s has host bits set, expected %s", p, p.Masked())
	case a.IsLinkLocalUnicast():
		return invalidf("refusing to ban link-local prefix %s", p)
	case a.IsMulticast():
		return invalidf("refusing to ban multicast prefix %s", p)
	}
	return nil
}

// ParsePrefix parses a prefix from the wire. It must pass CheckPrefix and be
// written in canonical form (netip.Prefix.String), so log lines and set
// elements are unambiguous.
func ParsePrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, invalidf("%v", err)
	}
	if err := CheckPrefix(p); err != nil {
		return netip.Prefix{}, err
	}
	if p.String() != s {
		return netip.Prefix{}, invalidf("prefix %q is not in canonical form %q", s, p.String())
	}
	return p, nil
}

// TimeoutFromSeconds converts timeout_seconds from the wire. The range is
// checked before multiplying, so huge values cannot overflow time.Duration.
func TimeoutFromSeconds(s int64) (time.Duration, error) {
	if s < 1 || s > MaxTimeoutSeconds {
		return 0, invalidf("timeout_seconds must be between 1 and %d, got %d", MaxTimeoutSeconds, s)
	}
	return time.Duration(s) * time.Second, nil
}

// SecondsFromDuration converts a ban duration for the wire, rounding up to
// whole seconds.
func SecondsFromDuration(d time.Duration) (int64, error) {
	if d < time.Second || d > agentapi.MaxBanDuration {
		return 0, invalidf("ban duration must be between 1s and %s, got %s", agentapi.MaxBanDuration, d)
	}
	return RemainingSeconds(d), nil
}

// RemainingSeconds rounds d up to whole seconds; non-positive durations give 0.
func RemainingSeconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	s := int64(d / time.Second)
	if d%time.Second != 0 {
		s++
	}
	return s
}

// FindOverlap returns the first prefix in existing that overlaps p without
// being identical to it.
func FindOverlap(p netip.Prefix, existing []netip.Prefix) (netip.Prefix, bool) {
	for _, q := range existing {
		if q != p && q.Overlaps(p) {
			return q, true
		}
	}
	return netip.Prefix{}, false
}
```

- [ ] **Step 4: Test laufen lassen (grün)**

Run: `go test ./internal/agent/banrules/`
Expected: `ok  	github.com/Bernotese/mc-panel/internal/agent/banrules`

- [ ] **Step 5: Commit**

```bash
git add internal/agent/banrules
git commit -m "feat(agent): add strict ban validation rules" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `peercred` – Peer-UID-Prüfung pro Verbindung

**Files:**
- Create: `internal/agent/peercred/peercred.go`, `internal/agent/peercred/peercred_linux.go`, `internal/agent/peercred/peercred_darwin.go`, `internal/agent/peercred/peercred_other.go`
- Test: `internal/agent/peercred/peercred_test.go`

**Interfaces:**
- Consumes: `unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED) (*unix.Ucred, error)` (Linux, Feld `Uid uint32`), `unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED) (*unix.Xucred, error)` (macOS, Feld `Uid uint32`)
- Produces:
```go
type Conn struct {
	net.Conn
	UID uint32
}
func PeerUID(c net.Conn) (uint32, error)
func AllowUIDs(uids ...uint32) func(uid uint32) bool
func Wrap(l net.Listener, allow func(uid uint32) bool, log *slog.Logger) net.Listener
func ConnContext(ctx context.Context, c net.Conn) context.Context // for http.Server.ConnContext
func FromContext(ctx context.Context) (uint32, bool)
```

Entscheidung: Die Prüfung sitzt in einem umhüllenden `net.Listener` (nicht in einer HTTP-Middleware). Abgelehnte Verbindungen werden im `Accept` geschlossen, bevor `net/http` ein Byte liest; `Accept` läuft dann weiter (gibt keinen Fehler zurück, sonst würde `http.Server.Serve` beendet). Die Allow-Funktion ist injizierbar, damit Tests eine fremde UID simulieren können, ohne Benutzer zu wechseln.

- [ ] **Step 1: Failing tests schreiben** – `internal/agent/peercred/peercred_test.go`

```go
package peercred_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/peercred"
)

// socketPath returns a short socket path: Unix socket paths are limited to
// 104 bytes on macOS, and t.TempDir() can exceed that.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mca")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "a.sock")
}

func selfUID(t *testing.T) uint32 {
	t.Helper()
	uid := os.Getuid()
	if uid < 0 {
		t.Skip("no uid on this platform")
	}
	return uint32(uid) //nolint:gosec // G115: non-negative uid fits into uint32
}

func TestPeerUIDIsCurrentUser(t *testing.T) {
	path := socketPath(t)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	uid, err := peercred.PeerUID(server)
	if err != nil {
		t.Fatalf("PeerUID: %v", err)
	}
	if want := selfUID(t); uid != want {
		t.Fatalf("PeerUID = %d, want %d", uid, want)
	}
}

func TestPeerUIDRejectsNonUnixConn(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err := peercred.PeerUID(a); err == nil {
		t.Fatal("expected error for a non-unix connection")
	}
}

func TestWrapRejectsDisallowedUID(t *testing.T) {
	path := socketPath(t)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	me := selfUID(t)
	var calls atomic.Int32
	// Reject the first connection, accept the second. This simulates a
	// foreign UID without needing a second user account.
	allow := func(uid uint32) bool { return calls.Add(1) > 1 && uid == me }
	var logs bytes.Buffer
	wl := peercred.Wrap(l, allow, slog.New(slog.NewTextHandler(&logs, nil)))

	rejected, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer rejected.Close()
	accepted, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()

	c, err := wl.Accept()
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	defer c.Close()
	pc, ok := c.(*peercred.Conn)
	if !ok || pc.UID != me {
		t.Fatalf("Accept returned %T %+v, want *peercred.Conn with UID %d", c, c, me)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("allow called %d times, want 2", n)
	}

	_ = rejected.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := rejected.Read(make([]byte, 1))
	var ne net.Error
	if n != 0 || err == nil || (errors.As(err, &ne) && ne.Timeout()) {
		t.Fatalf("rejected connection: n=%d err=%v, want it closed by the server", n, err)
	}
	if !strings.Contains(logs.String(), "disallowed uid") {
		t.Fatalf("missing rejection log, got: %s", logs.String())
	}
}

func TestAllowUIDs(t *testing.T) {
	allow := peercred.AllowUIDs(1000, 0)
	if !allow(1000) || !allow(0) || allow(1001) {
		t.Fatal("AllowUIDs(1000, 0) decided wrongly")
	}
	if peercred.AllowUIDs()(0) {
		t.Fatal("AllowUIDs() must reject everyone")
	}
}

func TestConnContext(t *testing.T) {
	ctx := peercred.ConnContext(context.Background(), &peercred.Conn{UID: 42})
	if uid, ok := peercred.FromContext(ctx); !ok || uid != 42 {
		t.Fatalf("FromContext = %d, %v; want 42, true", uid, ok)
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, ok := peercred.FromContext(peercred.ConnContext(context.Background(), a)); ok {
		t.Fatal("plain connections must not carry a uid")
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./internal/agent/peercred/`
Expected: FAIL – Build-Fehler (`peercred.PeerUID` undefiniert / keine Nicht-Test-Dateien).

- [ ] **Step 3: Implementierung** – `internal/agent/peercred/peercred.go`

```go
// Package peercred authenticates Unix socket peers by their kernel-provided
// credentials (SO_PEERCRED on Linux, LOCAL_PEERCRED on macOS).
package peercred

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"slices"
)

// Conn is an accepted connection whose peer UID passed the allow check.
type Conn struct {
	net.Conn
	UID uint32
}

// PeerUID returns the effective UID of the process on the other end of c.
// c must be a *net.UnixConn.
func PeerUID(c net.Conn) (uint32, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("peercred: %T is not a unix socket connection", c)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("peercred: %w", err)
	}
	var (
		uid    uint32
		getErr error
	)
	if err := raw.Control(func(fd uintptr) {
		uid, getErr = peerUID(int(fd)) //nolint:gosec // G115: file descriptors fit into int
	}); err != nil {
		return 0, fmt.Errorf("peercred: %w", err)
	}
	if getErr != nil {
		return 0, fmt.Errorf("peercred: %w", getErr)
	}
	return uid, nil
}

// AllowUIDs returns an allow function accepting exactly the given UIDs.
func AllowUIDs(uids ...uint32) func(uid uint32) bool {
	allowed := slices.Clone(uids)
	return func(uid uint32) bool { return slices.Contains(allowed, uid) }
}

// Wrap returns a listener that only hands out connections whose peer UID is
// accepted by allow. Rejected connections are closed before any byte is read
// and Accept keeps waiting for the next connection.
func Wrap(l net.Listener, allow func(uid uint32) bool, log *slog.Logger) net.Listener {
	return &listener{Listener: l, allow: allow, log: log}
}

type listener struct {
	net.Listener
	allow func(uid uint32) bool
	log   *slog.Logger
}

func (l *listener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := PeerUID(c)
		if err != nil {
			l.log.Warn("agent: rejecting connection without peer credentials", "err", err)
			_ = c.Close()
			continue
		}
		if !l.allow(uid) {
			l.log.Warn("agent: rejecting connection from disallowed uid", "uid", uid)
			_ = c.Close()
			continue
		}
		return &Conn{Conn: c, UID: uid}, nil
	}
}

type ctxKey struct{}

// ConnContext stores the peer UID of a *Conn in the context. It has the
// signature of http.Server.ConnContext.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	if pc, ok := c.(*Conn); ok {
		return context.WithValue(ctx, ctxKey{}, pc.UID)
	}
	return ctx
}

// FromContext returns the peer UID stored by ConnContext.
func FromContext(ctx context.Context) (uint32, bool) {
	uid, ok := ctx.Value(ctxKey{}).(uint32)
	return uid, ok
}
```

`internal/agent/peercred/peercred_linux.go`:

```go
//go:build linux

package peercred

import "golang.org/x/sys/unix"

func peerUID(fd int) (uint32, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}
```

`internal/agent/peercred/peercred_darwin.go`:

```go
//go:build darwin

package peercred

import "golang.org/x/sys/unix"

func peerUID(fd int) (uint32, error) {
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}
```

`internal/agent/peercred/peercred_other.go`:

```go
//go:build !linux && !darwin

package peercred

import (
	"errors"
	"runtime"
)

func peerUID(int) (uint32, error) {
	return 0, errors.New("peer credentials are not supported on " + runtime.GOOS)
}
```

- [ ] **Step 4: Tests laufen lassen (grün) + Linux-Compile-Check**

Run: `go test ./internal/agent/peercred/ && GOOS=linux GOARCH=amd64 go vet ./internal/agent/peercred/`
Expected: `ok  	github.com/Bernotese/mc-panel/internal/agent/peercred`, `go vet` ohne Ausgabe.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/peercred
git commit -m "feat(agent): add unix socket peer credential check" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Firewall-Grundlagen und Noop-Backend

**Files:**
- Create: `internal/agent/firewall/firewall.go`, `internal/agent/firewall/noop.go`
- Test: `internal/agent/firewall/noop_test.go`

**Interfaces:**
- Consumes: `agentapi.Firewall`, `agentapi.Ban`, `clock.Clock`, `banrules.CheckPrefix`, `banrules.SecondsFromDuration`, `banrules.RemainingSeconds`, `banrules.FindOverlap`, `banrules.ErrConflict`
- Produces:
```go
const TableName = "mcpanel"; Set4Name = "mcpanel_jail4"; Set6Name = "mcpanel_jail6"; ChainName = "input"; ChainPriority = -10
type entry struct { prefix netip.Prefix; remaining time.Duration } // package-internal
func toBans(entries []entry) []agentapi.Ban                        // package-internal, sorted, never nil
type Noop struct{ /* unexported */ }
func NewNoop(clk clock.Clock, log *slog.Logger) *Noop
func (n *Noop) Name() string // "noop"
func (n *Noop) Ban(ctx context.Context, p netip.Prefix, d time.Duration) error
func (n *Noop) Unban(ctx context.Context, p netip.Prefix) error
func (n *Noop) List(ctx context.Context) ([]agentapi.Ban, error)
```

Semantik (für beide Backends verbindlich): Re-Ban desselben Prefixes **ersetzt** den Timeout (auch verkürzend – Stream B sendet immer die aktuelle Restdauer). Unban eines nicht gebannten Prefixes ist kein Fehler. Ein Ban, der einen **anderen** gebannten Prefix überlappt (z. B. /128 in gebanntem /64), liefert `banrules.ErrConflict`. `List` ist sortiert (IPv4 vor IPv6, dann Adresse, dann Prefix-Länge) und liefert nie `nil`. Der Noop-Zustand lebt nur im Speicher und geht bei einem Agent-Neustart verloren (akzeptiert, siehe Self-Review).

- [ ] **Step 1: Failing tests schreiben** – `internal/agent/firewall/noop_test.go`

```go
package firewall_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
	"github.com/Bernotese/mc-panel/internal/agent/firewall"
	"github.com/Bernotese/mc-panel/internal/clock"
)

func newNoop(t *testing.T) (*firewall.Noop, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
	return firewall.NewNoop(clk, slog.New(slog.NewTextHandler(io.Discard, nil))), clk
}

func assertBans(t *testing.T, fw agentapi.Firewall, want []agentapi.Ban) {
	t.Helper()
	got, err := fw.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got == nil {
		t.Fatal("List returned nil, want non-nil slice")
	}
	if !slices.Equal(got, want) {
		t.Fatalf("List = %+v, want %+v", got, want)
	}
}

func TestNoopName(t *testing.T) {
	fw, _ := newNoop(t)
	if fw.Name() != "noop" {
		t.Fatalf("Name = %q", fw.Name())
	}
}

func TestNoopBanListExpire(t *testing.T) {
	fw, clk := newNoop(t)
	ctx := context.Background()
	if err := fw.Ban(ctx, netip.MustParsePrefix("2001:db8:1:2::/64"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := fw.Ban(ctx, netip.MustParsePrefix("203.0.113.7/32"), 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	assertBans(t, fw, []agentapi.Ban{
		{Prefix: "203.0.113.7/32", ExpiresInSeconds: 900},
		{Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 3600},
	})
	clk.Advance(10 * time.Minute)
	assertBans(t, fw, []agentapi.Ban{
		{Prefix: "203.0.113.7/32", ExpiresInSeconds: 300},
		{Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 3000},
	})
	clk.Advance(5 * time.Minute) // IPv4 ban expires exactly now
	assertBans(t, fw, []agentapi.Ban{{Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 2700}})
}

func TestNoopRebanReplacesTimeout(t *testing.T) {
	fw, _ := newNoop(t)
	ctx := context.Background()
	p := netip.MustParsePrefix("203.0.113.7/32")
	for _, step := range []struct {
		d    time.Duration
		want int64
	}{{time.Minute, 60}, {2 * time.Hour, 7200}, {30 * time.Second, 30}} {
		if err := fw.Ban(ctx, p, step.d); err != nil {
			t.Fatal(err)
		}
		assertBans(t, fw, []agentapi.Ban{{Prefix: "203.0.113.7/32", ExpiresInSeconds: step.want}})
	}
}

func TestNoopUnbanIsIdempotent(t *testing.T) {
	fw, _ := newNoop(t)
	ctx := context.Background()
	p := netip.MustParsePrefix("203.0.113.7/32")
	if err := fw.Unban(ctx, p); err != nil {
		t.Fatalf("Unban of unknown prefix: %v", err)
	}
	if err := fw.Ban(ctx, p, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := fw.Unban(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := fw.Unban(ctx, p); err != nil {
		t.Fatalf("second Unban: %v", err)
	}
	assertBans(t, fw, []agentapi.Ban{})
}

func TestNoopRejectsInvalid(t *testing.T) {
	fw, _ := newNoop(t)
	ctx := context.Background()
	cases := []struct {
		p string
		d time.Duration
	}{
		{"203.0.113.0/24", time.Minute},
		{"::ffff:203.0.113.7/128", time.Minute},
		{"203.0.113.7/32", 0},
		{"203.0.113.7/32", agentapi.MaxBanDuration + time.Second},
	}
	for _, tc := range cases {
		if err := fw.Ban(ctx, netip.MustParsePrefix(tc.p), tc.d); !errors.Is(err, banrules.ErrInvalid) {
			t.Errorf("Ban(%s, %v) = %v, want ErrInvalid", tc.p, tc.d, err)
		}
	}
	if err := fw.Unban(ctx, netip.MustParsePrefix("0.0.0.0/0")); !errors.Is(err, banrules.ErrInvalid) {
		t.Errorf("Unban(0.0.0.0/0) = %v, want ErrInvalid", err)
	}
	assertBans(t, fw, []agentapi.Ban{})
}

func TestNoopRejectsOverlap(t *testing.T) {
	fw, _ := newNoop(t)
	ctx := context.Background()
	if err := fw.Ban(ctx, netip.MustParsePrefix("2001:db8:1:2::/64"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := fw.Ban(ctx, netip.MustParsePrefix("2001:db8:1:2::1/128"), time.Hour); !errors.Is(err, banrules.ErrConflict) {
		t.Fatalf("overlapping ban = %v, want ErrConflict", err)
	}
	assertBans(t, fw, []agentapi.Ban{{Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 3600}})
}

func TestNoopConcurrent(t *testing.T) {
	fw, _ := newNoop(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range byte(50) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := netip.PrefixFrom(netip.AddrFrom4([4]byte{203, 0, 113, i}), 32)
			if err := fw.Ban(ctx, p, time.Hour); err != nil {
				t.Errorf("Ban %s: %v", p, err)
				return
			}
			if i%2 == 0 {
				if err := fw.Unban(ctx, p); err != nil {
					t.Errorf("Unban %s: %v", p, err)
				}
			}
			if _, err := fw.List(ctx); err != nil {
				t.Errorf("List: %v", err)
			}
		}()
	}
	wg.Wait()
	bans, err := fw.List(ctx)
	if err != nil || len(bans) != 25 {
		t.Fatalf("List = %d bans, %v; want 25", len(bans), err)
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./internal/agent/firewall/`
Expected: FAIL – Build-Fehler (`firewall.NewNoop` undefiniert).

- [ ] **Step 3: Implementierung** – `internal/agent/firewall/firewall.go`

```go
// Package firewall contains the agent-side firewall backends: nftables
// (Linux) and noop (in-memory, logs only; used on macOS and when configured).
package firewall

import (
	"cmp"
	"net/netip"
	"slices"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
)

// Names of the nftables objects owned by mc-panel. Nothing outside the table
// "inet mcpanel" is ever created, modified or deleted.
const (
	TableName     = "mcpanel"
	Set4Name      = "mcpanel_jail4"
	Set6Name      = "mcpanel_jail6"
	ChainName     = "input"
	ChainPriority = -10
)

// entry is a ban as seen by a backend: prefix plus remaining lifetime.
type entry struct {
	prefix    netip.Prefix
	remaining time.Duration
}

// toBans sorts entries (IPv4 before IPv6, then address, then prefix length)
// and converts them to the wire type. The result is never nil.
func toBans(entries []entry) []agentapi.Ban {
	slices.SortFunc(entries, func(a, b entry) int {
		if c := a.prefix.Addr().Compare(b.prefix.Addr()); c != 0 {
			return c
		}
		return cmp.Compare(a.prefix.Bits(), b.prefix.Bits())
	})
	bans := make([]agentapi.Ban, 0, len(entries))
	for _, e := range entries {
		bans = append(bans, agentapi.Ban{Prefix: e.prefix.String(), ExpiresInSeconds: banrules.RemainingSeconds(e.remaining)})
	}
	return bans
}
```

`internal/agent/firewall/noop.go`:

```go
package firewall

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
	"github.com/Bernotese/mc-panel/internal/clock"
)

// Noop keeps bans in memory and only logs them. No packet is ever dropped.
// State is lost when the agent restarts.
type Noop struct {
	mu   sync.Mutex
	clk  clock.Clock
	log  *slog.Logger
	bans map[netip.Prefix]time.Time // prefix -> expiry
}

var _ agentapi.Firewall = (*Noop)(nil)

func NewNoop(clk clock.Clock, log *slog.Logger) *Noop {
	return &Noop{clk: clk, log: log, bans: make(map[netip.Prefix]time.Time)}
}

func (n *Noop) Name() string { return "noop" }

func (n *Noop) Ban(_ context.Context, p netip.Prefix, d time.Duration) error {
	if err := banrules.CheckPrefix(p); err != nil {
		return err
	}
	if _, err := banrules.SecondsFromDuration(d); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.clk.Now()
	n.purgeLocked(now)
	existing := make([]netip.Prefix, 0, len(n.bans))
	for q := range n.bans {
		existing = append(existing, q)
	}
	if q, ok := banrules.FindOverlap(p, existing); ok {
		return fmt.Errorf("%w: %s overlaps %s", banrules.ErrConflict, p, q)
	}
	n.bans[p] = now.Add(d)
	n.log.Info("noop firewall: ban recorded, packets are NOT dropped", "prefix", p.String(), "timeout", d.String())
	return nil
}

func (n *Noop) Unban(_ context.Context, p netip.Prefix) error {
	if err := banrules.CheckPrefix(p); err != nil {
		return err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.bans, p)
	n.log.Info("noop firewall: unban recorded", "prefix", p.String())
	return nil
}

func (n *Noop) List(_ context.Context) ([]agentapi.Ban, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	now := n.clk.Now()
	n.purgeLocked(now)
	entries := make([]entry, 0, len(n.bans))
	for p, exp := range n.bans {
		entries = append(entries, entry{prefix: p, remaining: exp.Sub(now)})
	}
	return toBans(entries), nil
}

func (n *Noop) purgeLocked(now time.Time) {
	for p, exp := range n.bans {
		if !now.Before(exp) {
			delete(n.bans, p)
		}
	}
}
```

- [ ] **Step 4: Tests laufen lassen (grün, mit Race-Detector)**

Run: `go test ./internal/agent/firewall/ && CGO_ENABLED=1 go test -race -run Noop ./internal/agent/firewall/`
Expected: zweimal `ok  	github.com/Bernotese/mc-panel/internal/agent/firewall`

- [ ] **Step 5: Commit**

```bash
git add internal/agent/firewall
git commit -m "feat(agent): add noop firewall backend" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 4: nftables – reine Logik (ohne root testbar)

**Files:**
- Create: `internal/agent/firewall/nftkeys.go` (kein Build-Tag, importiert **nicht** `google/nftables`, damit es auf macOS kompiliert)
- Test: `internal/agent/firewall/nftkeys_test.go` (Paket `firewall`, intern)

**Interfaces:**
- Consumes: `entry`, `Set4Name`, `Set6Name` (Task 3)
- Produces (paketintern):
```go
type u128 struct{ hi, lo uint64 }
type elementSpec struct {
	Set string // Set4Name or Set6Name
	Key []byte // 4 bytes (IPv4) or 16 bytes (IPv6 interval start)
	End []byte // exclusive IPv6 interval end; nil for IPv4 and for ranges reaching the top of the address space
}
func elementFor(p netip.Prefix) (elementSpec, error)
func prefixFromRange(start, end []byte) (netip.Prefix, error) // end == nil: range reaches 2^128
type rawElem struct {
	Key         []byte
	IntervalEnd bool
	Timeout     time.Duration
	Expires     time.Duration
}
func entriesFromElements(v4, v6 []rawElem) (entries []entry, skipped int)
func splitExisting(p netip.Prefix, entries []entry) (exists bool, others []netip.Prefix)
```

Hintergrund (verifiziert in google/nftables v0.3.0 `set.go`): In einem Set mit `Interval: true` wird ein Bereich als zwei Elemente gespeichert – Start-Element (`Key` = erste Adresse, trägt den `Timeout`) und End-Element (`Key` = erste Adresse **nach** dem Bereich, `IntervalEnd: true`, **ohne** Timeout; neuere Kernel lehnen Timeout-Attribute an End-Elementen mit `EINVAL` ab). Reicht ein Bereich bis `ffff:…:ffff`, entfällt das End-Element. `GetSetElements` liefert die Elemente in Kernel-Reihenfolge (rbtree, nicht garantiert sortiert) mit `Expires` = Restlaufzeit; ein abgelaufenes, noch nicht eingesammeltes Element kann mit `Expires == 0` erscheinen und wird hier herausgefiltert.

- [ ] **Step 1: Failing tests schreiben** – `internal/agent/firewall/nftkeys_test.go`

```go
package firewall

import (
	"bytes"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func a16(s string) []byte {
	b := netip.MustParseAddr(s).As16()
	return b[:]
}

func TestElementForIPv4(t *testing.T) {
	spec, err := elementFor(netip.MustParsePrefix("203.0.113.7/32"))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Set != Set4Name || !bytes.Equal(spec.Key, []byte{203, 0, 113, 7}) || spec.End != nil {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestElementForIPv6(t *testing.T) {
	cases := []struct{ prefix, end string }{
		{"2001:db8:1:2::/64", "2001:db8:1:3::"},
		{"2001:db8::1/128", "2001:db8::2"},
		{"2001:db8:0:ffff::/64", "2001:db8:1::"},
		{"2001:db8:1:2:3::/80", "2001:db8:1:2:4::"},
		{"2001:db8:ffff:ffff:ffff:ffff:ffff:ffff/128", "2001:db9::"},
	}
	for _, tc := range cases {
		p := netip.MustParsePrefix(tc.prefix)
		spec, err := elementFor(p)
		if err != nil {
			t.Fatalf("elementFor(%s): %v", tc.prefix, err)
		}
		if spec.Set != Set6Name || !bytes.Equal(spec.Key, a16(p.Addr().String())) || !bytes.Equal(spec.End, a16(tc.end)) {
			t.Errorf("elementFor(%s) = %+v, want end %s", tc.prefix, spec, tc.end)
		}
	}
	top, err := elementFor(netip.MustParsePrefix("ffff:ffff:ffff:ffff::/64"))
	if err != nil || top.End != nil {
		t.Fatalf("top-of-space range: spec=%+v err=%v, want End == nil", top, err)
	}
}

func TestElementForRejects(t *testing.T) {
	bad := []netip.Prefix{
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("2001:db8:1:2::5/64"),
		netip.MustParsePrefix("::ffff:203.0.113.7/128"),
		netip.MustParsePrefix("2001:db8::/48"),
		{},
	}
	for _, p := range bad {
		if spec, err := elementFor(p); err == nil {
			t.Errorf("elementFor(%s) = %+v, want error", p, spec)
		}
	}
}

func TestPrefixFromRangeRoundTrip(t *testing.T) {
	for _, s := range []string{
		"2001:db8:1:2::/64", "2001:db8::1/128", "2001:db8:0:ffff::/64",
		"2001:db8:1:2:3::/80", "ffff:ffff:ffff:ffff::/64", "2001:db8:1:2::/127",
	} {
		want := netip.MustParsePrefix(s)
		spec, err := elementFor(want)
		if err != nil {
			t.Fatalf("elementFor(%s): %v", s, err)
		}
		got, err := prefixFromRange(spec.Key, spec.End)
		if err != nil || got != want {
			t.Errorf("prefixFromRange(elementFor(%s)) = %v, %v", s, got, err)
		}
	}
}

func TestPrefixFromRangeRejects(t *testing.T) {
	cases := []struct {
		name       string
		start, end []byte
	}{
		{"empty range", a16("2001:db8::"), a16("2001:db8::")},
		{"reversed", a16("2001:db8::2"), a16("2001:db8::1")},
		{"size not power of two", a16("2001:db8::"), a16("2001:db8::3")},
		{"misaligned start", a16("2001:db8::1"), a16("2001:db8::3")},
		{"short key", []byte{1, 2, 3, 4}, a16("2001:db8::1")},
		{"whole address space", a16("::"), nil},
		{"open range not a prefix", a16("2001:db8:5::1"), nil},
	}
	for _, tc := range cases {
		if p, err := prefixFromRange(tc.start, tc.end); err == nil {
			t.Errorf("%s: prefixFromRange = %v, want error", tc.name, p)
		}
	}
}

func TestEntriesFromElements(t *testing.T) {
	v4 := []rawElem{
		{Key: []byte{203, 0, 113, 7}, Timeout: 15 * time.Minute, Expires: 1500 * time.Millisecond},
		{Key: []byte{198, 51, 100, 1}, Timeout: time.Second, Expires: 0}, // expired, not yet collected
		{Key: []byte{1, 2, 3}, Timeout: time.Minute, Expires: time.Minute}, // garbage
	}
	v6 := []rawElem{
		{Key: a16("2001:db8:1:3::"), IntervalEnd: true},                                     // end of A == start of B
		{Key: a16("2001:db8:1:3::"), Timeout: time.Hour, Expires: 30 * time.Minute},         // start of B
		{Key: a16("::"), IntervalEnd: true},                                                 // stray end element
		{Key: a16("2001:db8:1:2::"), Timeout: time.Hour, Expires: time.Hour},                // start of A
		{Key: a16("2001:db8:1:4::"), IntervalEnd: true},                                     // end of B
		{Key: a16("ffff:ffff:ffff:ffff::"), Timeout: time.Hour, Expires: 10 * time.Second},  // reaches the top, no end
		{Key: a16("2001:db8:5::1"), Timeout: time.Hour, Expires: time.Hour},                 // no matching end -> not a prefix
		{Key: a16("2001:db8:7::"), Timeout: time.Second, Expires: 0},                        // expired start
	}
	entries, skipped := entriesFromElements(v4, v6)
	if skipped != 2 {
		t.Errorf("skipped = %d, want 2", skipped)
	}
	got := toBans(entries)
	want := []agentapi.Ban{
		{Prefix: "203.0.113.7/32", ExpiresInSeconds: 2},
		{Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 3600},
		{Prefix: "2001:db8:1:3::/64", ExpiresInSeconds: 1800},
		{Prefix: "ffff:ffff:ffff:ffff::/64", ExpiresInSeconds: 10},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("bans = %+v\nwant   %+v", got, want)
	}
}

func TestSplitExisting(t *testing.T) {
	a := netip.MustParsePrefix("203.0.113.7/32")
	b := netip.MustParsePrefix("2001:db8:1:2::/64")
	entries := []entry{{prefix: a}, {prefix: b}}
	exists, others := splitExisting(a, entries)
	if !exists || !slices.Equal(others, []netip.Prefix{b}) {
		t.Fatalf("splitExisting(a) = %v, %v", exists, others)
	}
	exists, others = splitExisting(netip.MustParsePrefix("198.51.100.1/32"), entries)
	if exists || len(others) != 2 {
		t.Fatalf("splitExisting(unknown) = %v, %v", exists, others)
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test -run 'ElementFor|PrefixFromRange|EntriesFromElements|SplitExisting' ./internal/agent/firewall/`
Expected: FAIL – Build-Fehler (`elementFor`, `prefixFromRange`, `rawElem`, `entriesFromElements`, `splitExisting` undefiniert).

- [ ] **Step 3: Implementierung** – `internal/agent/firewall/nftkeys.go`

```go
package firewall

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"net/netip"
	"slices"
	"sort"
	"time"
)

// This file holds the pure logic of the nftables backend. It must not import
// github.com/google/nftables so that it builds and is tested on every OS.

// u128 is a 128-bit unsigned integer used for IPv6 interval arithmetic.
type u128 struct{ hi, lo uint64 }

func u128From(b [16]byte) u128 {
	return u128{hi: binary.BigEndian.Uint64(b[:8]), lo: binary.BigEndian.Uint64(b[8:])}
}

func (u u128) bytes() [16]byte {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], u.hi)
	binary.BigEndian.PutUint64(b[8:], u.lo)
	return b
}

// add returns u+v and whether the addition overflowed 2^128.
func (u u128) add(v u128) (u128, bool) {
	lo, carry := bits.Add64(u.lo, v.lo, 0)
	hi, carry := bits.Add64(u.hi, v.hi, carry)
	return u128{hi: hi, lo: lo}, carry != 0
}

// sub returns u-v modulo 2^128.
func (u u128) sub(v u128) u128 {
	lo, borrow := bits.Sub64(u.lo, v.lo, 0)
	hi, _ := bits.Sub64(u.hi, v.hi, borrow)
	return u128{hi: hi, lo: lo}
}

func (u u128) cmp(v u128) int {
	switch {
	case u.hi < v.hi:
		return -1
	case u.hi > v.hi:
		return 1
	case u.lo < v.lo:
		return -1
	case u.lo > v.lo:
		return 1
	}
	return 0
}

// pow2 returns 2^k for 0 <= k <= 127.
func pow2(k int) u128 {
	if k >= 64 {
		return u128{hi: 1 << (k - 64)}
	}
	return u128{lo: 1 << k}
}

// log2 returns k if u == 2^k.
func (u u128) log2() (int, bool) {
	switch {
	case u.hi == 0 && u.lo != 0 && u.lo&(u.lo-1) == 0:
		return bits.TrailingZeros64(u.lo), true
	case u.lo == 0 && u.hi != 0 && u.hi&(u.hi-1) == 0:
		return 64 + bits.TrailingZeros64(u.hi), true
	}
	return 0, false
}

// elementSpec describes the set element(s) representing one banned prefix.
type elementSpec struct {
	Set string
	Key []byte
	End []byte
}

// elementFor maps a canonical ban prefix to its set element(s): IPv4 /32 ->
// one key in mcpanel_jail4; IPv6 /64../128 -> interval [start, end) in
// mcpanel_jail6.
func elementFor(p netip.Prefix) (elementSpec, error) {
	if !p.IsValid() || p != p.Masked() || p.Addr().Is4In6() {
		return elementSpec{}, fmt.Errorf("firewall: %s is not a canonical prefix", p)
	}
	a := p.Addr()
	if a.Is4() {
		if p.Bits() != 32 {
			return elementSpec{}, fmt.Errorf("firewall: IPv4 bans must be /32, got %s", p)
		}
		k := a.As4()
		return elementSpec{Set: Set4Name, Key: k[:]}, nil
	}
	if p.Bits() < 64 {
		return elementSpec{}, fmt.Errorf("firewall: IPv6 bans must be /64 or narrower, got %s", p)
	}
	start := a.As16()
	spec := elementSpec{Set: Set6Name, Key: start[:]}
	if end, overflow := u128From(start).add(pow2(128 - p.Bits())); !overflow {
		e := end.bytes()
		spec.End = e[:]
	}
	return spec, nil
}

// prefixFromRange converts an IPv6 interval [start, end) back into a prefix.
// end == nil means the interval reaches the top of the address space.
func prefixFromRange(start, end []byte) (netip.Prefix, error) {
	if len(start) != 16 || (end != nil && len(end) != 16) {
		return netip.Prefix{}, fmt.Errorf("firewall: bad interval key lengths %d/%d", len(start), len(end))
	}
	s := u128From([16]byte(start))
	var size u128
	if end == nil {
		if s == (u128{}) {
			return netip.Prefix{}, errors.New("firewall: interval covers the whole address space")
		}
		size = u128{}.sub(s)
	} else {
		e := u128From([16]byte(end))
		if e.cmp(s) <= 0 {
			return netip.Prefix{}, fmt.Errorf("firewall: interval end %x is not after start %x", end, start)
		}
		size = e.sub(s)
	}
	k, ok := size.log2()
	if !ok {
		return netip.Prefix{}, errors.New("firewall: interval size is not a power of two")
	}
	p := netip.PrefixFrom(netip.AddrFrom16([16]byte(start)), 128-k)
	if p.Masked() != p {
		return netip.Prefix{}, fmt.Errorf("firewall: interval start %s is not aligned to /%d", p.Addr(), 128-k)
	}
	return p, nil
}

// rawElem is the OS-independent view of a google/nftables SetElement.
type rawElem struct {
	Key         []byte
	IntervalEnd bool
	Timeout     time.Duration
	Expires     time.Duration
}

// entriesFromElements reconstructs bans from the raw elements of both sets.
// Expired elements are dropped; malformed ones are counted in skipped.
func entriesFromElements(v4, v6 []rawElem) ([]entry, int) {
	var entries []entry
	skipped := 0
	expired := func(e rawElem) bool { return e.Timeout > 0 && e.Expires <= 0 }

	for _, e := range v4 {
		if e.IntervalEnd || len(e.Key) != 4 {
			skipped++
			continue
		}
		if expired(e) {
			continue
		}
		entries = append(entries, entry{prefix: netip.PrefixFrom(netip.AddrFrom4([4]byte(e.Key)), 32), remaining: e.Expires})
	}

	var ends []u128
	for _, e := range v6 {
		if !e.IntervalEnd {
			continue
		}
		if len(e.Key) != 16 {
			skipped++
			continue
		}
		ends = append(ends, u128From([16]byte(e.Key)))
	}
	slices.SortFunc(ends, u128.cmp)

	for _, e := range v6 {
		if e.IntervalEnd {
			continue
		}
		if len(e.Key) != 16 {
			skipped++
			continue
		}
		if expired(e) {
			continue
		}
		s := u128From([16]byte(e.Key))
		// Intervals never overlap, so the matching end is the smallest end > start.
		i := sort.Search(len(ends), func(i int) bool { return ends[i].cmp(s) > 0 })
		var end []byte
		if i < len(ends) {
			b := ends[i].bytes()
			end = b[:]
		}
		p, err := prefixFromRange(e.Key, end)
		if err != nil {
			skipped++
			continue
		}
		entries = append(entries, entry{prefix: p, remaining: e.Expires})
	}
	return entries, skipped
}

// splitExisting reports whether p is currently banned and returns all other
// banned prefixes (for the overlap check).
func splitExisting(p netip.Prefix, entries []entry) (bool, []netip.Prefix) {
	exists := false
	var others []netip.Prefix
	for _, e := range entries {
		if e.prefix == p {
			exists = true
			continue
		}
		others = append(others, e.prefix)
	}
	return exists, others
}
```

- [ ] **Step 4: Tests laufen lassen (grün)**

Run: `go test ./internal/agent/firewall/`
Expected: `ok  	github.com/Bernotese/mc-panel/internal/agent/firewall`

- [ ] **Step 5: Commit**

```bash
git add internal/agent/firewall/nftkeys.go internal/agent/firewall/nftkeys_test.go
git commit -m "feat(agent): add pure nftables element mapping" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: nftables-Backend (Linux) mit Integrationstest

**Files:**
- Create: `internal/agent/firewall/nftables_linux.go` (`//go:build linux`)
- Test: `internal/agent/firewall/nftables_integration_test.go` (`//go:build linux && nftintegration`)

**Interfaces:**
- Consumes (google/nftables v0.3.0, verifiziert): `nftables.New(opts ...ConnOption) (*Conn, error)`, `nftables.WithNetNSFd(fd int) ConnOption`, `(*Conn).AddTable(*Table) *Table`, `(*Conn).AddSet(*Set, []SetElement) error`, `(*Conn).AddChain(*Chain) *Chain`, `(*Conn).FlushChain(*Chain)`, `(*Conn).AddRule(*Rule) *Rule`, `(*Conn).SetAddElements(*Set, []SetElement) error`, `(*Conn).SetDeleteElements(*Set, []SetElement) error`, `(*Conn).GetSetElements(*Set) ([]SetElement, error)`, `(*Conn).Flush() error`; Typen `Table{Name, Family}`, `Set{Table, Name, KeyType, HasTimeout, Interval}`, `SetElement{Key, IntervalEnd, Timeout, Expires}`, `Chain{Name, Table, Type, Hooknum, Priority, Policy}`, `nftables.TypeIPAddr`, `nftables.TypeIP6Addr`, `nftables.ChainHookInput`, `nftables.ChainPriorityRef`, `nftables.ChainPolicyAccept`, `nftables.ChainTypeFilter`, `nftables.TableFamilyINet`; `expr.Meta{Key: expr.MetaKeyNFPROTO, Register}`, `expr.Cmp{Op: expr.CmpOpEq, Register, Data}`, `expr.Payload{DestRegister, Base: expr.PayloadBaseNetworkHeader, Offset, Len}`, `expr.Lookup{SourceRegister, SetName, SetID}`, `expr.Counter{}`, `expr.Verdict{Kind: expr.VerdictDrop}`. Aus Task 4: `elementFor`, `rawElem`, `entriesFromElements`, `splitExisting`; aus Task 3: `toBans`, Konstanten.
- Produces:
```go
type NFTables struct{ /* unexported */ }
func NewNFTables(log *slog.Logger, opts ...nftables.ConnOption) (*NFTables, error) // runs idempotent setup
func (n *NFTables) Name() string // "nftables"
func (n *NFTables) Ban(ctx context.Context, p netip.Prefix, d time.Duration) error
func (n *NFTables) Unban(ctx context.Context, p netip.Prefix) error
func (n *NFTables) List(ctx context.Context) ([]agentapi.Ban, error)
```

Designentscheidungen:
- **Idempotentes Setup in einem Batch:** `AddTable`/`AddSet`/`AddChain` nutzen `NLM_F_CREATE` ohne `NLM_F_EXCL` (in v0.3.0 so implementiert) und sind daher wiederholbar. Regeln würden mit `AddRule` bei jedem Start dupliziert – deshalb `FlushChain` auf **unsere** Chain und danach genau zwei `AddRule`, alles atomar in einem `Flush`. Set-Elemente (aktive Bans) bleiben dabei erhalten. Es gibt kein `FlushRuleset`, kein `DelTable`, keinen Zugriff auf andere Tabellen.
- **Re-Ban:** Der Kernel ignoriert `add element` für ein existierendes Element (Timeout bliebe alt). Unter dem Mutex: Elemente lesen → falls vorhanden `SetDeleteElements` + `SetAddElements` in **einem** Batch (atomar), sonst nur `SetAddElements`.
- **Unban:** nur löschen, wenn vorhanden (ein `delete` auf ein fehlendes Element würde den Batch mit `ENOENT` scheitern lassen).
- **Überlappung:** Intervall-Sets ohne `auto-merge` lehnen überlappende Bereiche ab; wir prüfen vorher mit `banrules.FindOverlap` und liefern `banrules.ErrConflict` (→ 409) statt eines Netlink-Fehlers.
- Jede Operation öffnet eine kurzlebige Netlink-Verbindung (`nftables.New` ohne `AsLasting`); die Optionen (`WithNetNSFd` im Test) werden gespeichert.

- [ ] **Step 1: Integrationstest schreiben (rot)** – `internal/agent/firewall/nftables_integration_test.go`

```go
//go:build linux && nftintegration

// Run in the Linux VM or in CI as root. Build as the normal user, run as root:
//
//	go test -c -tags nftintegration -o /tmp/firewall.test ./internal/agent/firewall/
//	sudo /tmp/firewall.test -test.v -test.count=1 -test.run NFTables
//
// Every test works in its own fresh network namespace and never touches the
// host ruleset.
package firewall_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
	"github.com/Bernotese/mc-panel/internal/agent/firewall"
)

// newNetNS creates a fresh network namespace and returns an fd referring to
// it. Namespace switches are per OS thread: the thread is locked, unshared,
// and switched back. If switching back fails, t.Fatalf ends the goroutine
// with the thread still locked and Go discards that thread.
func newNetNS(t *testing.T) int {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("nftintegration tests need root (CAP_SYS_ADMIN for unshare, CAP_NET_ADMIN for nftables)")
	}
	runtime.LockOSThread()
	orig, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		runtime.UnlockOSThread()
		t.Fatalf("open current netns: %v", err)
	}
	defer orig.Close()
	if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
		runtime.UnlockOSThread()
		t.Fatalf("unshare(CLONE_NEWNET): %v", err)
	}
	ns, err := os.Open("/proc/thread-self/ns/net")
	if err != nil {
		t.Fatalf("open new netns: %v", err)
	}
	if err := unix.Setns(int(orig.Fd()), unix.CLONE_NEWNET); err != nil { //nolint:gosec // G115: fd fits into int
		t.Fatalf("restore netns: %v", err)
	}
	runtime.UnlockOSThread()
	t.Cleanup(func() { _ = ns.Close() })
	return int(ns.Fd()) //nolint:gosec // G115: fd fits into int
}

func newBackend(t *testing.T, fd int) *firewall.NFTables {
	t.Helper()
	fw, err := firewall.NewNFTables(slog.New(slog.NewTextHandler(io.Discard, nil)), nftables.WithNetNSFd(fd))
	if err != nil {
		t.Fatalf("NewNFTables: %v", err)
	}
	return fw
}

func rawConn(t *testing.T, fd int) *nftables.Conn {
	t.Helper()
	c, err := nftables.New(nftables.WithNetNSFd(fd))
	if err != nil {
		t.Fatalf("nftables.New: %v", err)
	}
	return c
}

func listBans(t *testing.T, fw *firewall.NFTables) []agentapi.Ban {
	t.Helper()
	bans, err := fw.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return bans
}

func assertPrefixes(t *testing.T, got []agentapi.Ban, want []string) {
	t.Helper()
	var prefixes []string
	for _, b := range got {
		prefixes = append(prefixes, b.Prefix)
	}
	if !slices.Equal(prefixes, want) {
		t.Fatalf("bans = %v, want %v", prefixes, want)
	}
}

func TestNFTablesSetupIsIdempotentAndScoped(t *testing.T) {
	fd := newNetNS(t)
	c := rawConn(t, fd)
	other := c.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: "othertable"})
	c.AddChain(&nftables.Chain{Name: "keepme", Table: other})
	if err := c.Flush(); err != nil {
		t.Fatalf("create foreign table: %v", err)
	}

	newBackend(t, fd)
	newBackend(t, fd) // a second start must not duplicate anything

	tables, err := c.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tb := range tables {
		names = append(names, tb.Name)
	}
	if !slices.Contains(names, firewall.TableName) || !slices.Contains(names, "othertable") {
		t.Fatalf("tables = %v", names)
	}
	if _, err := c.ListChain(other, "keepme"); err != nil {
		t.Fatalf("foreign chain was touched: %v", err)
	}

	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: firewall.TableName}
	chain, err := c.ListChain(table, firewall.ChainName)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Type != nftables.ChainTypeFilter ||
		chain.Hooknum == nil || *chain.Hooknum != *nftables.ChainHookInput ||
		chain.Priority == nil || *chain.Priority != nftables.ChainPriority(firewall.ChainPriority) ||
		chain.Policy == nil || *chain.Policy != nftables.ChainPolicyAccept {
		t.Fatalf("chain = %+v", chain)
	}

	rules, err := c.GetRules(table, &nftables.Chain{Name: firewall.ChainName, Table: table})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want exactly 2", len(rules))
	}
	for i, want := range []string{firewall.Set4Name, firewall.Set6Name} {
		var lookup, drop bool
		for _, e := range rules[i].Exprs {
			switch e := e.(type) {
			case *expr.Lookup:
				lookup = e.SetName == want
			case *expr.Verdict:
				drop = e.Kind == expr.VerdictDrop
			}
		}
		if !lookup || !drop {
			t.Errorf("rule %d does not drop @%s: %#v", i, want, rules[i].Exprs)
		}
	}

	s4, err := c.GetSetByName(table, firewall.Set4Name)
	if err != nil {
		t.Fatal(err)
	}
	if !s4.HasTimeout || s4.Interval || s4.KeyType.Name != nftables.TypeIPAddr.Name {
		t.Errorf("set4 = %+v", s4)
	}
	s6, err := c.GetSetByName(table, firewall.Set6Name)
	if err != nil {
		t.Fatal(err)
	}
	if !s6.HasTimeout || !s6.Interval || s6.KeyType.Name != nftables.TypeIP6Addr.Name {
		t.Errorf("set6 = %+v", s6)
	}
}

func TestNFTablesBanListUnban(t *testing.T) {
	fw := newBackend(t, newNetNS(t))
	ctx := context.Background()
	durations := map[string]time.Duration{
		"203.0.113.7/32":    15 * time.Minute,
		"2001:db8:1:2::/64": time.Hour,
		"2001:db8:1:3::/64": 30 * time.Minute, // adjacent to the previous /64
		"2001:db8:9::1/128": 10 * time.Minute,
	}
	for s, d := range durations {
		if err := fw.Ban(ctx, netip.MustParsePrefix(s), d); err != nil {
			t.Fatalf("Ban %s: %v", s, err)
		}
	}
	got := listBans(t, fw)
	assertPrefixes(t, got, []string{"203.0.113.7/32", "2001:db8:1:2::/64", "2001:db8:1:3::/64", "2001:db8:9::1/128"})
	for _, b := range got {
		want := banrules.RemainingSeconds(durations[b.Prefix])
		if b.ExpiresInSeconds > want || b.ExpiresInSeconds < want-10 {
			t.Errorf("%s expires in %ds, want about %ds", b.Prefix, b.ExpiresInSeconds, want)
		}
	}

	if err := fw.Unban(ctx, netip.MustParsePrefix("2001:db8:1:2::/64")); err != nil {
		t.Fatalf("Unban: %v", err)
	}
	assertPrefixes(t, listBans(t, fw), []string{"203.0.113.7/32", "2001:db8:1:3::/64", "2001:db8:9::1/128"})
	if err := fw.Unban(ctx, netip.MustParsePrefix("2001:db8:1:2::/64")); err != nil {
		t.Fatalf("second Unban must be a no-op: %v", err)
	}
	if err := fw.Unban(ctx, netip.MustParsePrefix("203.0.113.7/32")); err != nil {
		t.Fatalf("Unban v4: %v", err)
	}
	assertPrefixes(t, listBans(t, fw), []string{"2001:db8:1:3::/64", "2001:db8:9::1/128"})
}

func TestNFTablesRebanRefreshesTimeout(t *testing.T) {
	fw := newBackend(t, newNetNS(t))
	ctx := context.Background()
	for _, s := range []string{"203.0.113.7/32", "2001:db8:1:2::/64"} {
		p := netip.MustParsePrefix(s)
		if err := fw.Ban(ctx, p, time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := fw.Ban(ctx, p, 2*time.Hour); err != nil {
			t.Fatalf("re-ban %s: %v", s, err)
		}
	}
	got := listBans(t, fw)
	if len(got) != 2 {
		t.Fatalf("got %d bans, want 2: %+v", len(got), got)
	}
	for _, b := range got {
		if b.ExpiresInSeconds < 7190 {
			t.Errorf("%s: timeout not refreshed, expires in %ds", b.Prefix, b.ExpiresInSeconds)
		}
	}
	if err := fw.Ban(ctx, netip.MustParsePrefix("203.0.113.7/32"), 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if b := listBans(t, fw)[0]; b.Prefix != "203.0.113.7/32" || b.ExpiresInSeconds > 30 {
		t.Errorf("shorter re-ban must replace the timeout: %+v", b)
	}
}

func TestNFTablesRejectsOverlap(t *testing.T) {
	fw := newBackend(t, newNetNS(t))
	ctx := context.Background()
	if err := fw.Ban(ctx, netip.MustParsePrefix("2001:db8:1:2::/64"), time.Hour); err != nil {
		t.Fatal(err)
	}
	err := fw.Ban(ctx, netip.MustParsePrefix("2001:db8:1:2::1/128"), time.Hour)
	if !errors.Is(err, banrules.ErrConflict) {
		t.Fatalf("overlapping ban = %v, want ErrConflict", err)
	}
	assertPrefixes(t, listBans(t, fw), []string{"2001:db8:1:2::/64"})
}

func TestNFTablesExpiry(t *testing.T) {
	fw := newBackend(t, newNetNS(t))
	ctx := context.Background()
	p4 := netip.MustParsePrefix("203.0.113.7/32")
	for _, p := range []netip.Prefix{p4, netip.MustParsePrefix("2001:db8:1:2::/64")} {
		if err := fw.Ban(ctx, p, time.Second); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(2500 * time.Millisecond)
	assertPrefixes(t, listBans(t, fw), nil)
	if err := fw.Ban(ctx, p4, time.Hour); err != nil {
		t.Fatalf("ban after expiry: %v", err)
	}
	assertPrefixes(t, listBans(t, fw), []string{"203.0.113.7/32"})
}

func TestNFTablesConcurrent(t *testing.T) {
	fw := newBackend(t, newNetNS(t))
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range byte(20) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p4 := netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, i}), 32)
			p6 := netip.PrefixFrom(netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0, i}), 64)
			for _, p := range []netip.Prefix{p4, p6} {
				if err := fw.Ban(ctx, p, time.Hour); err != nil {
					t.Errorf("Ban %s: %v", p, err)
					return
				}
			}
			if i%2 == 0 {
				for _, p := range []netip.Prefix{p4, p6} {
					if err := fw.Unban(ctx, p); err != nil {
						t.Errorf("Unban %s: %v", p, err)
					}
				}
			}
		}()
	}
	wg.Wait()
	if got := listBans(t, fw); len(got) != 20 {
		t.Fatalf("got %d bans, want 20 (10 IPv4 + 10 IPv6)", len(got))
	}
}

func TestNFTablesBansSurviveAgentRestart(t *testing.T) {
	fd := newNetNS(t)
	fw := newBackend(t, fd)
	if err := fw.Ban(context.Background(), netip.MustParsePrefix("203.0.113.7/32"), time.Hour); err != nil {
		t.Fatal(err)
	}
	restarted := newBackend(t, fd) // setup flushes only the chain, never the sets
	assertPrefixes(t, listBans(t, restarted), []string{"203.0.113.7/32"})
}
```

- [ ] **Step 2: Compile-Check auf macOS (rot)**

Run: `GOOS=linux GOARCH=amd64 go vet -tags nftintegration ./internal/agent/firewall/`
Expected: FAIL – `undefined: firewall.NewNFTables` / `firewall.NFTables`.

- [ ] **Step 3: Implementierung** – `internal/agent/firewall/nftables_linux.go`

```go
//go:build linux

package firewall

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"golang.org/x/sys/unix"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
)

// NFTables manages the table "inet mcpanel" with the sets mcpanel_jail4
// (ipv4_addr, timeout) and mcpanel_jail6 (ipv6_addr, interval+timeout) and a
// filter chain on the input hook at priority -10. It never touches any other
// table.
type NFTables struct {
	mu    sync.Mutex // serialises read-modify-write sequences (list -> delete/add)
	opts  []nftables.ConnOption
	log   *slog.Logger
	table *nftables.Table
	set4  *nftables.Set
	set6  *nftables.Set
}

var _ agentapi.Firewall = (*NFTables)(nil)

// NewNFTables creates the backend and runs the idempotent setup. opts are
// passed to every nftables.New call (tests use nftables.WithNetNSFd).
func NewNFTables(log *slog.Logger, opts ...nftables.ConnOption) (*NFTables, error) {
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: TableName}
	n := &NFTables{
		opts:  opts,
		log:   log,
		table: table,
		set4:  &nftables.Set{Table: table, Name: Set4Name, KeyType: nftables.TypeIPAddr, HasTimeout: true},
		set6:  &nftables.Set{Table: table, Name: Set6Name, KeyType: nftables.TypeIP6Addr, HasTimeout: true, Interval: true},
	}
	if err := n.setup(); err != nil {
		return nil, fmt.Errorf("nftables: setup: %w", err)
	}
	return n, nil
}

func (n *NFTables) Name() string { return "nftables" }

// setup creates table, sets and chain if missing and replaces the chain's
// rules with exactly the two drop rules, atomically in one batch. Existing
// set elements (active bans) are kept.
func (n *NFTables) setup() error {
	c, err := nftables.New(n.opts...)
	if err != nil {
		return err
	}
	c.AddTable(n.table)
	if err := c.AddSet(n.set4, nil); err != nil {
		return err
	}
	if err := c.AddSet(n.set6, nil); err != nil {
		return err
	}
	policy := nftables.ChainPolicyAccept
	chain := c.AddChain(&nftables.Chain{
		Name:     ChainName,
		Table:    n.table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookInput,
		Priority: nftables.ChainPriorityRef(ChainPriority),
		Policy:   &policy,
	})
	c.FlushChain(chain)
	c.AddRule(&nftables.Rule{Table: n.table, Chain: chain, Exprs: dropExprs(unix.NFPROTO_IPV4, 12, 4, n.set4)})
	c.AddRule(&nftables.Rule{Table: n.table, Chain: chain, Exprs: dropExprs(unix.NFPROTO_IPV6, 8, 16, n.set6)})
	return c.Flush()
}

// dropExprs builds "meta nfproto <proto> <ip|ip6> saddr @set counter drop".
// offset/length address the source address in the network header
// (IPv4: 12/4, IPv6: 8/16).
func dropExprs(proto byte, offset, length uint32, set *nftables.Set) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{proto}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseNetworkHeader, Offset: offset, Len: length},
		&expr.Lookup{SourceRegister: 1, SetName: set.Name, SetID: set.ID},
		&expr.Counter{},
		&expr.Verdict{Kind: expr.VerdictDrop},
	}
}

func (n *NFTables) Ban(_ context.Context, p netip.Prefix, d time.Duration) error {
	if err := banrules.CheckPrefix(p); err != nil {
		return err
	}
	if _, err := banrules.SecondsFromDuration(d); err != nil {
		return err
	}
	spec, err := elementFor(p)
	if err != nil {
		return fmt.Errorf("%w: %v", banrules.ErrInvalid, err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	c, err := nftables.New(n.opts...)
	if err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	current, err := n.entries(c)
	if err != nil {
		return err
	}
	exists, others := splitExisting(p, current)
	if q, ok := banrules.FindOverlap(p, others); ok {
		return fmt.Errorf("%w: %s overlaps %s", banrules.ErrConflict, p, q)
	}
	set := n.setFor(spec)
	if exists {
		// The kernel ignores "add element" for an existing element, so a
		// re-ban deletes and re-adds it in the same atomic batch.
		if err := c.SetDeleteElements(set, setElements(spec, 0)); err != nil {
			return fmt.Errorf("nftables: %w", err)
		}
	}
	if err := c.SetAddElements(set, setElements(spec, d)); err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("nftables: ban %s: %w", p, err)
	}
	n.log.Debug("nftables: element added", "set", spec.Set, "prefix", p.String(), "timeout", d.String(), "refreshed", exists)
	return nil
}

func (n *NFTables) Unban(_ context.Context, p netip.Prefix) error {
	if err := banrules.CheckPrefix(p); err != nil {
		return err
	}
	spec, err := elementFor(p)
	if err != nil {
		return fmt.Errorf("%w: %v", banrules.ErrInvalid, err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	c, err := nftables.New(n.opts...)
	if err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	current, err := n.entries(c)
	if err != nil {
		return err
	}
	if exists, _ := splitExisting(p, current); !exists {
		n.log.Debug("nftables: unban of absent element ignored", "prefix", p.String())
		return nil
	}
	if err := c.SetDeleteElements(n.setFor(spec), setElements(spec, 0)); err != nil {
		return fmt.Errorf("nftables: %w", err)
	}
	if err := c.Flush(); err != nil {
		return fmt.Errorf("nftables: unban %s: %w", p, err)
	}
	n.log.Debug("nftables: element deleted", "set", spec.Set, "prefix", p.String())
	return nil
}

func (n *NFTables) List(_ context.Context) ([]agentapi.Ban, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	c, err := nftables.New(n.opts...)
	if err != nil {
		return nil, fmt.Errorf("nftables: %w", err)
	}
	current, err := n.entries(c)
	if err != nil {
		return nil, err
	}
	return toBans(current), nil
}

func (n *NFTables) setFor(spec elementSpec) *nftables.Set {
	if spec.Set == Set4Name {
		return n.set4
	}
	return n.set6
}

func (n *NFTables) entries(c *nftables.Conn) ([]entry, error) {
	e4, err := c.GetSetElements(n.set4)
	if err != nil {
		return nil, fmt.Errorf("nftables: list %s: %w", Set4Name, err)
	}
	e6, err := c.GetSetElements(n.set6)
	if err != nil {
		return nil, fmt.Errorf("nftables: list %s: %w", Set6Name, err)
	}
	entries, skipped := entriesFromElements(toRaw(e4), toRaw(e6))
	if skipped > 0 {
		n.log.Warn("nftables: ignoring unexpected set elements", "count", skipped)
	}
	return entries, nil
}

// setElements returns the element(s) for spec. Only the interval start
// carries the timeout; the end element must not have one.
func setElements(spec elementSpec, timeout time.Duration) []nftables.SetElement {
	els := []nftables.SetElement{{Key: spec.Key, Timeout: timeout}}
	if spec.End != nil {
		els = append(els, nftables.SetElement{Key: spec.End, IntervalEnd: true})
	}
	return els
}

func toRaw(els []nftables.SetElement) []rawElem {
	out := make([]rawElem, 0, len(els))
	for _, e := range els {
		out = append(out, rawElem{Key: e.Key, IntervalEnd: e.IntervalEnd, Timeout: e.Timeout, Expires: e.Expires})
	}
	return out
}
```

- [ ] **Step 4: Compile-Checks (grün)**

Run:
```bash
GOOS=linux GOARCH=amd64 go vet ./internal/agent/firewall/
GOOS=linux GOARCH=amd64 go vet -tags nftintegration ./internal/agent/firewall/
GOOS=linux GOARCH=arm64 go vet ./internal/agent/firewall/
go test ./internal/agent/firewall/
```
Expected: `go vet` ohne Ausgabe; `ok  	github.com/Bernotese/mc-panel/internal/agent/firewall` (auf macOS laufen nur Noop- und nftkeys-Tests).

- [ ] **Step 5: Integrationstest in der Linux-VM (root)**

In der OrbStack/Lima-VM (Ubuntu LTS, Repo gemountet bzw. geklont):
```bash
go test -c -tags nftintegration -o /tmp/firewall.test ./internal/agent/firewall/
sudo /tmp/firewall.test -test.v -test.count=1 -test.run NFTables
```
Expected: alle sieben `TestNFTables…` mit `--- PASS`, am Ende `PASS`. Danach zeigt `sudo nft list tables` auf dem Host **keine** Tabelle `inet mcpanel` (Tests liefen im eigenen Namespace).

Falls `TestNFTablesSetupIsIdempotentAndScoped` nur an `chain.Priority` scheitert (Byte-Order-Dekodierung in einer anderen google/nftables-Version), mit `sudo nft list chain inet mcpanel input` in einem manuellen Lauf prüfen, dass `priority -10` gesetzt ist, und die Assertion an die tatsächlich dekodierte Darstellung anpassen – die Kernel-Konfiguration ist maßgeblich, nicht die Bibliotheksdekodierung.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/firewall/nftables_linux.go internal/agent/firewall/nftables_integration_test.go
git commit -m "feat(agent): add nftables firewall backend" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Backend-Auswahl (`firewall.New`)

**Files:**
- Create: `internal/agent/firewall/factory.go`, `internal/agent/firewall/factory_linux.go` (`//go:build linux`), `internal/agent/firewall/factory_other.go` (`//go:build !linux`)
- Test: `internal/agent/firewall/factory_test.go` (Paket `firewall`, intern)

**Interfaces:**
- Consumes: `config.Agent{Dev bool; Socket string; PanelUID int; Firewall string}`, `clock.Clock`, `NewNoop`, `NewNFTables`
- Produces:
```go
var ErrUnsupported error
func New(cfg config.Agent, clk clock.Clock, log *slog.Logger) (agentapi.Firewall, error)
func choose(name, goos string) (string, error) // package-internal, pure
```

- [ ] **Step 1: Failing tests schreiben** – `internal/agent/firewall/factory_test.go`

```go
package firewall

import (
	"errors"
	"io"
	"log/slog"
	"runtime"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

func TestChoose(t *testing.T) {
	cases := []struct {
		name, goos, want string
		unsupported, err bool
	}{
		{name: "noop", goos: "darwin", want: "noop"},
		{name: "noop", goos: "linux", want: "noop"},
		{name: "nftables", goos: "linux", want: "nftables"},
		{name: "nftables", goos: "darwin", unsupported: true, err: true},
		{name: "iptables", goos: "linux", err: true},
		{name: "", goos: "linux", err: true},
	}
	for _, tc := range cases {
		got, err := choose(tc.name, tc.goos)
		if (err != nil) != tc.err || got != tc.want || errors.Is(err, ErrUnsupported) != tc.unsupported {
			t.Errorf("choose(%q, %q) = %q, %v", tc.name, tc.goos, got, err)
		}
	}
}

func TestNewNoop(t *testing.T) {
	cfg := config.Agent{Dev: true, Socket: "/tmp/x.sock", PanelUID: 1000, Firewall: "noop"}
	fw, err := New(cfg, clock.NewFake(time.Unix(0, 0)), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil || fw.Name() != "noop" {
		t.Fatalf("New(noop) = %v, %v", fw, err)
	}
}

func TestNewNFTablesUnsupportedOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("nftables on linux is covered by the nftintegration tests")
	}
	cfg := config.Agent{Socket: "/tmp/x.sock", PanelUID: 1000, Firewall: "nftables"}
	if _, err := New(cfg, clock.Real{}, slog.New(slog.NewTextHandler(io.Discard, nil))); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("New(nftables) on %s = %v, want ErrUnsupported", runtime.GOOS, err)
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test -run 'Choose|TestNew' ./internal/agent/firewall/`
Expected: FAIL – `undefined: choose`, `undefined: New`, `undefined: ErrUnsupported`.

- [ ] **Step 3: Implementierung** – `internal/agent/firewall/factory.go`

```go
package firewall

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/config"
)

// ErrUnsupported is returned when the configured backend cannot run on this OS.
var ErrUnsupported = errors.New("firewall backend not supported on this platform")

// New returns the backend selected by cfg.Firewall for the running OS.
func New(cfg config.Agent, clk clock.Clock, log *slog.Logger) (agentapi.Firewall, error) {
	backend, err := choose(cfg.Firewall, runtime.GOOS)
	if err != nil {
		return nil, err
	}
	if backend == "noop" {
		if !cfg.Dev {
			log.Warn("firewall backend is noop: bans are kept in memory only and NOT enforced")
		}
		return NewNoop(clk, log), nil
	}
	return newNFTables(log)
}

func choose(name, goos string) (string, error) {
	switch name {
	case "noop":
		return "noop", nil
	case "nftables":
		if goos != "linux" {
			return "", fmt.Errorf("%w: nftables requires linux (running on %s); set firewall = \"noop\" for development", ErrUnsupported, goos)
		}
		return "nftables", nil
	default:
		return "", fmt.Errorf("firewall: unknown backend %q", name)
	}
}
```

`internal/agent/firewall/factory_linux.go`:

```go
//go:build linux

package firewall

import (
	"log/slog"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func newNFTables(log *slog.Logger) (agentapi.Firewall, error) {
	fw, err := NewNFTables(log)
	if err != nil {
		return nil, err // never return a typed nil pointer inside the interface
	}
	return fw, nil
}
```

`internal/agent/firewall/factory_other.go`:

```go
//go:build !linux

package firewall

import (
	"log/slog"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

func newNFTables(*slog.Logger) (agentapi.Firewall, error) { return nil, ErrUnsupported }
```

- [ ] **Step 4: Tests laufen lassen (grün) + Linux-Compile-Check**

Run: `go test ./internal/agent/firewall/ && GOOS=linux GOARCH=amd64 go vet ./internal/agent/firewall/`
Expected: `ok  	github.com/Bernotese/mc-panel/internal/agent/firewall`; `go vet` ohne Ausgabe.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/firewall/factory.go internal/agent/firewall/factory_linux.go internal/agent/firewall/factory_other.go internal/agent/firewall/factory_test.go
git commit -m "feat(agent): select firewall backend by config and OS" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: Socket öffnen (`agent.Listen`) und Test-Helfer

**Files:**
- Create: `internal/agent/socket.go`
- Test: `internal/agent/helpers_test.go`, `internal/agent/socket_test.go` (Paket `agent_test`)

**Interfaces:**
- Consumes: –
- Produces:
```go
var ErrAlreadyRunning error
// Listen removes a stale socket, listens on path, chowns the socket to gid
// (gid < 0: unchanged) and chmods it to mode. Closing the listener removes the file.
func Listen(path string, mode os.FileMode, gid int) (net.Listener, error)
```
Test-Helfer (Paket `agent_test`, von Task 8 und 9 benutzt):
```go
func socketPath(t *testing.T) string
func selfUID(t *testing.T) uint32
type syncBuffer struct{ /* mutex-protected bytes.Buffer */ }
func (b *syncBuffer) Write(p []byte) (int, error)
func (b *syncBuffer) String() string
func testLogger(w io.Writer) *slog.Logger
func newNoop() (*firewall.Noop, *clock.Fake)
type stubFirewall struct{ err error } // implements agentapi.Firewall, Name() == "stub"
```

Hinweise: Zwischen `bind` und `chmod` hat der Socket kurz die umask-Rechte (typisch `0755`); `connect` braucht Schreibrecht, Fremde können sich in diesem Fenster also nicht verbinden – und die Peer-UID-Prüfung greift ohnehin. Das Elternverzeichnis wird **nicht** von `Listen` angelegt (Prod: `/run/mc-panel` kommt von systemd `RuntimeDirectory`, Dev: `cmd/agent` legt `.dev/` an).

- [ ] **Step 1: Test-Helfer schreiben** – `internal/agent/helpers_test.go`

```go
package agent_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/firewall"
	"github.com/Bernotese/mc-panel/internal/clock"
)

// socketPath returns a short socket path: Unix socket paths are limited to
// 104 bytes on macOS, and t.TempDir() can exceed that.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mca")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "a.sock")
}

func selfUID(t *testing.T) uint32 {
	t.Helper()
	uid := os.Getuid()
	if uid < 0 {
		t.Skip("no uid on this platform")
	}
	return uint32(uid) //nolint:gosec // G115: non-negative uid fits into uint32
}

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

func testLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func newNoop() (*firewall.Noop, *clock.Fake) {
	clk := clock.NewFake(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
	return firewall.NewNoop(clk, testLogger(io.Discard)), clk
}

// stubFirewall fails every operation with err.
type stubFirewall struct{ err error }

func (s stubFirewall) Name() string                                           { return "stub" }
func (s stubFirewall) Ban(context.Context, netip.Prefix, time.Duration) error { return s.err }
func (s stubFirewall) Unban(context.Context, netip.Prefix) error              { return s.err }
func (s stubFirewall) List(context.Context) ([]agentapi.Ban, error)           { return nil, s.err }
```

- [ ] **Step 2: Failing tests schreiben** – `internal/agent/socket_test.go`

```go
package agent_test

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/Bernotese/mc-panel/internal/agent"
)

func TestListenCreatesSocketWithMode(t *testing.T) {
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Type() != fs.ModeSocket || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want socket with 0600", fi.Mode())
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket not removed on close: %v", err)
	}
}

func TestListenSetsGroup(t *testing.T) {
	path := socketPath(t)
	gid := os.Getgid()
	l, err := agent.Listen(path, 0o660, gid)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no syscall.Stat_t on this platform")
	}
	if int(st.Gid) != gid || fi.Mode().Perm() != 0o660 {
		t.Fatalf("gid=%d mode=%v, want gid=%d mode=0660", st.Gid, fi.Mode(), gid)
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	path := socketPath(t)
	old, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = old.Close() // simulates a crashed agent: file stays, nobody listens
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("stale socket missing: %v", err)
	}
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatalf("Listen over stale socket: %v", err)
	}
	defer l.Close()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial new socket: %v", err)
	}
	_ = c.Close()
}

func TestListenRefusesWhenAgentRunning(t *testing.T) {
	path := socketPath(t)
	first, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := agent.Listen(path, 0o600, -1); !errors.Is(err, agent.ErrAlreadyRunning) {
		t.Fatalf("second Listen = %v, want ErrAlreadyRunning", err)
	}
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("first agent's socket was broken: %v", err)
	}
	_ = c.Close()
}

func TestListenRefusesNonSocket(t *testing.T) {
	path := socketPath(t)
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Listen(path, 0o600, -1); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("Listen over regular file = %v, want 'not a socket' error", err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "keep me" {
		t.Fatalf("regular file was modified: %q, %v", b, err)
	}
}

func TestListenRefusesSymlink(t *testing.T) {
	path := socketPath(t)
	target := path + ".target"
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := agent.Listen(path, 0o600, -1); err == nil {
		t.Fatal("Listen over a symlink must fail")
	}
	if _, err := os.Lstat(target); err != nil {
		t.Fatalf("symlink target was removed: %v", err)
	}
}
```

- [ ] **Step 3: Test laufen lassen (rot)**

Run: `go test ./internal/agent/`
Expected: FAIL – Build-Fehler (`agent.Listen`, `agent.ErrAlreadyRunning` undefiniert; Paket `github.com/Bernotese/mc-panel/internal/agent` hat keine Nicht-Test-Dateien).

- [ ] **Step 4: Implementierung** – `internal/agent/socket.go`

```go
package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"syscall"
	"time"
)

// ErrAlreadyRunning is returned by Listen when another process accepts
// connections on the socket path.
var ErrAlreadyRunning = errors.New("another agent is already listening on this socket")

// Listen removes a stale socket file, listens on path, sets the socket's group
// to gid (gid < 0 leaves it unchanged) and its permissions to mode. Closing the
// returned listener removes the socket file.
func Listen(path string, mode os.FileMode, gid int) (net.Listener, error) {
	if err := removeStale(path); err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("agent: listen on %s: %w", path, err)
	}
	if gid >= 0 {
		if err := os.Chown(path, -1, gid); err != nil {
			_ = l.Close()
			return nil, fmt.Errorf("agent: chown %s to gid %d: %w", path, gid, err)
		}
	}
	if err := os.Chmod(path, mode); err != nil { //nolint:gosec // G302: 0600 (dev) or 0660 (prod, panel group) by design
		_ = l.Close()
		return nil, fmt.Errorf("agent: chmod %s: %w", path, err)
	}
	return l, nil
}

// removeStale deletes path only if it is a socket nobody listens on.
// Regular files, directories and symlinks are never removed.
func removeStale(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("agent: stat %s: %w", path, err)
	}
	if fi.Mode().Type() != fs.ModeSocket {
		return fmt.Errorf("agent: %s exists and is not a socket (%v); refusing to remove it", path, fi.Mode().Type())
	}
	c, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err == nil {
		_ = c.Close()
		return fmt.Errorf("%w: %s", ErrAlreadyRunning, path)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("agent: cannot tell whether %s is stale: %w", path, err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("agent: remove stale socket %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 5: Tests laufen lassen (grün)**

Run: `go test -run Listen ./internal/agent/`
Expected: `ok  	github.com/Bernotese/mc-panel/internal/agent`

- [ ] **Step 6: Commit**

```bash
git add internal/agent/socket.go internal/agent/socket_test.go internal/agent/helpers_test.go
git commit -m "feat(agent): add unix socket listener with stale socket handling" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Agent-Server

**Files:**
- Create: `internal/agent/server.go`
- Test: `internal/agent/server_test.go` (Paket `agent_test`)

**Interfaces:**
- Consumes: `agentapi.Path*`, `agentapi.HealthResponse`, `agentapi.BanRequest`, `agentapi.UnbanRequest`, `agentapi.BansResponse`, `agentapi.ErrorResponse`, `agentapi.Firewall`; `banrules.ParsePrefix`, `banrules.TimeoutFromSeconds`, `banrules.ErrInvalid`, `banrules.ErrConflict`; `peercred.ConnContext`, `peercred.FromContext`; Test-Helfer aus Task 7
- Produces:
```go
type Server struct{ /* unexported */ }
func NewServer(fw agentapi.Firewall, version string, log *slog.Logger) *Server
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request)
func (s *Server) Serve(l net.Listener) error         // returns nil after Shutdown
func (s *Server) Shutdown(ctx context.Context) error
```

Routing ist bewusst eine feste Tabelle `Pfad → Methode` statt `http.ServeMux`, damit auch 404/405 im JSON-Format `agentapi.ErrorResponse` antworten.

- [ ] **Step 1: Failing tests schreiben** – `internal/agent/server_test.go`

```go
package agent_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
)

const jsonCT = "application/json"

func serve(h http.Handler, method, path, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://agent"+path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServerHealth(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "1.2.3", testLogger(io.Discard))
	rec := serve(srv, http.MethodGet, agentapi.PathHealth, "", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != jsonCT {
		t.Fatalf("status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var h agentapi.HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if h != (agentapi.HealthResponse{Status: "ok", Version: "1.2.3", Firewall: "noop"}) {
		t.Fatalf("health = %+v", h)
	}
}

func TestServerBanListUnban(t *testing.T) {
	fw, clk := newNoop()
	var logs syncBuffer
	srv := agent.NewServer(fw, "test", testLogger(&logs))

	for _, body := range []string{
		`{"prefix":"203.0.113.7/32","timeout_seconds":900}`,
		`{"prefix":"2001:db8:1:2::/64","timeout_seconds":691200}`,
	} {
		if rec := serve(srv, http.MethodPost, agentapi.PathBan, jsonCT, body); rec.Code != http.StatusNoContent {
			t.Fatalf("ban %s: status=%d body=%s", body, rec.Code, rec.Body)
		}
	}
	clk.Advance(100 * time.Second)
	rec := serve(srv, http.MethodGet, agentapi.PathBans, "", "")
	want := `{"bans":[{"prefix":"203.0.113.7/32","expires_in_seconds":800},{"prefix":"2001:db8:1:2::/64","expires_in_seconds":691100}]}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("bans: status=%d body=%s", rec.Code, rec.Body)
	}

	for range 2 { // unban is idempotent
		if rec := serve(srv, http.MethodPost, agentapi.PathUnban, jsonCT, `{"prefix":"203.0.113.7/32"}`); rec.Code != http.StatusNoContent {
			t.Fatalf("unban: status=%d body=%s", rec.Code, rec.Body)
		}
	}
	rec = serve(srv, http.MethodGet, agentapi.PathBans, "", "")
	if want := `{"bans":[{"prefix":"2001:db8:1:2::/64","expires_in_seconds":691100}]}` + "\n"; rec.Body.String() != want {
		t.Fatalf("bans after unban: %s", rec.Body)
	}

	for _, want := range []string{`msg="firewall ban"`, "prefix=203.0.113.7/32", "timeout_s=900", "backend=noop", `msg="firewall unban"`} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log missing %q:\n%s", want, logs.String())
		}
	}
}

func TestServerEmptyBanListIsArray(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	if rec := serve(srv, http.MethodGet, agentapi.PathBans, "", ""); rec.Body.String() != `{"bans":[]}`+"\n" {
		t.Fatalf("empty list body = %s", rec.Body)
	}
}

func TestServerRejectsInvalidRequests(t *testing.T) {
	ban := func(prefix string, timeout string) string {
		return fmt.Sprintf(`{"prefix":%q,"timeout_seconds":%s}`, prefix, timeout)
	}
	cases := []struct {
		name, method, path, ct, body string
		want                         int
	}{
		{"unknown field", "POST", agentapi.PathBan, jsonCT, `{"prefix":"203.0.113.7/32","timeout_seconds":60,"extra":1}`, 400},
		{"missing prefix", "POST", agentapi.PathBan, jsonCT, `{"timeout_seconds":60}`, 400},
		{"missing timeout", "POST", agentapi.PathBan, jsonCT, `{"prefix":"203.0.113.7/32"}`, 400},
		{"ipv4 /24", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.0/24", "60"), 400},
		{"mapped ipv6", "POST", agentapi.PathBan, jsonCT, ban("::ffff:203.0.113.7/128", "60"), 400},
		{"mapped ipv4 space", "POST", agentapi.PathBan, jsonCT, ban("::ffff:0:0/96", "60"), 400},
		{"host bits", "POST", agentapi.PathBan, jsonCT, ban("2001:db8:1:2::5/64", "60"), 400},
		{"zone", "POST", agentapi.PathBan, jsonCT, ban("fe80::1%eth0/128", "60"), 400},
		{"link-local", "POST", agentapi.PathBan, jsonCT, ban("fe80::/64", "60"), 400},
		{"multicast", "POST", agentapi.PathBan, jsonCT, ban("ff02::/64", "60"), 400},
		{"loopback", "POST", agentapi.PathBan, jsonCT, ban("127.0.0.1/32", "60"), 400},
		{"upper case", "POST", agentapi.PathBan, jsonCT, ban("2001:DB8:1:2::/64", "60"), 400},
		{"timeout zero", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "0"), 400},
		{"timeout negative", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "-1"), 400},
		{"timeout too long", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "691201"), 400},
		{"max int64", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "9223372036854775807"), 400},
		{"overflow literal", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "99999999999999999999"), 400},
		{"float", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "1.5"), 400},
		{"exponent", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "1e3"), 400},
		{"string timeout", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", `"60"`), 400},
		{"trailing data", "POST", agentapi.PathBan, jsonCT, ban("203.0.113.7/32", "60") + ban("203.0.113.8/32", "60"), 400},
		{"not json", "POST", agentapi.PathBan, jsonCT, `prefix=203.0.113.7/32`, 400},
		{"too large", "POST", agentapi.PathBan, jsonCT, `{"prefix":"` + strings.Repeat("a", 5000) + `","timeout_seconds":60}`, 413},
		{"wrong content type", "POST", agentapi.PathBan, "text/plain", ban("203.0.113.7/32", "60"), 415},
		{"missing content type", "POST", agentapi.PathBan, "", ban("203.0.113.7/32", "60"), 415},
		{"GET on ban", "GET", agentapi.PathBan, "", "", 405},
		{"POST on bans", "POST", agentapi.PathBans, jsonCT, `{}`, 405},
		{"unknown path", "GET", "/v1/nope", "", "", 404},
		{"unban invalid", "POST", agentapi.PathUnban, jsonCT, `{"prefix":"0.0.0.0/0"}`, 400},
		{"unban unknown field", "POST", agentapi.PathUnban, jsonCT, `{"prefix":"203.0.113.7/32","timeout_seconds":1}`, 400},
	}
	fw, _ := newNoop()
	var logs syncBuffer
	srv := agent.NewServer(fw, "test", testLogger(&logs))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(srv, tc.method, tc.path, tc.ct, tc.body)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
			if rec.Header().Get("Content-Type") != jsonCT {
				t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
			}
			var er agentapi.ErrorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &er); err != nil || er.Error == "" {
				t.Fatalf("error body = %s (%v)", rec.Body, err)
			}
			if tc.want == http.StatusMethodNotAllowed && rec.Header().Get("Allow") == "" {
				t.Fatal("405 without Allow header")
			}
		})
	}
	if bans, err := fw.List(t.Context()); err != nil || len(bans) != 0 {
		t.Fatalf("invalid requests reached the firewall: %+v %v", bans, err)
	}
	if !strings.Contains(logs.String(), `msg="agent request rejected"`) {
		t.Fatal("rejections are not logged")
	}
}

func TestServerAcceptsMaxTimeout(t *testing.T) {
	fw, _ := newNoop()
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	if rec := serve(srv, http.MethodPost, agentapi.PathBan, "application/json; charset=utf-8", `{"prefix":"203.0.113.7/32","timeout_seconds":691200}`); rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestServerFirewallFailure(t *testing.T) {
	var logs syncBuffer
	srv := agent.NewServer(stubFirewall{err: errors.New("netlink: permission denied")}, "test", testLogger(&logs))
	for _, rec := range []*httptest.ResponseRecorder{
		serve(srv, http.MethodPost, agentapi.PathBan, jsonCT, `{"prefix":"203.0.113.7/32","timeout_seconds":60}`),
		serve(srv, http.MethodPost, agentapi.PathUnban, jsonCT, `{"prefix":"203.0.113.7/32"}`),
		serve(srv, http.MethodGet, agentapi.PathBans, "", ""),
	} {
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "netlink") || !strings.Contains(body, "firewall operation failed") {
			t.Fatalf("body = %s", body)
		}
	}
	if !strings.Contains(logs.String(), "netlink: permission denied") {
		t.Fatal("firewall error was not logged")
	}
}

func TestServerConflict(t *testing.T) {
	fw := stubFirewall{err: fmt.Errorf("%w: 2001:db8:1:2::1/128 overlaps 2001:db8:1:2::/64", banrules.ErrConflict)}
	srv := agent.NewServer(fw, "test", testLogger(io.Discard))
	rec := serve(srv, http.MethodPost, agentapi.PathBan, jsonCT, `{"prefix":"2001:db8:1:2::1/128","timeout_seconds":60}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "overlaps") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}
```

(`t.Context()` gibt es seit Go 1.24; bei älterer Toolchain `context.Background()` verwenden und `context` importieren.)

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test -run Server ./internal/agent/`
Expected: FAIL – `undefined: agent.NewServer`.

- [ ] **Step 3: Implementierung** – `internal/agent/server.go`

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
	"github.com/Bernotese/mc-panel/internal/agent/peercred"
)

const maxRequestBytes = 4 << 10

type route struct {
	method  string
	handler http.HandlerFunc
}

// Server serves the agentapi endpoints. It implements http.Handler; Serve
// adds the peer UID to each request context and applies conservative timeouts.
type Server struct {
	fw      agentapi.Firewall
	version string
	log     *slog.Logger
	routes  map[string]route
	srv     *http.Server
}

func NewServer(fw agentapi.Firewall, version string, log *slog.Logger) *Server {
	s := &Server{fw: fw, version: version, log: log}
	s.routes = map[string]route{
		agentapi.PathHealth: {method: http.MethodGet, handler: s.health},
		agentapi.PathBan:    {method: http.MethodPost, handler: s.ban},
		agentapi.PathUnban:  {method: http.MethodPost, handler: s.unban},
		agentapi.PathBans:   {method: http.MethodGet, handler: s.bans},
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
	writeJSON(w, http.StatusOK, agentapi.HealthResponse{Status: "ok", Version: s.version, Firewall: s.fw.Name()})
}

func (s *Server) ban(w http.ResponseWriter, r *http.Request) {
	var req agentapi.BanRequest
	if !s.decode(w, r, &req) {
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
	if !s.decode(w, r, &req) {
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

// decode strictly decodes exactly one JSON object of at most maxRequestBytes.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		s.decodeError(w, r, err)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected data after JSON body")
		}
		s.decodeError(w, r, err)
		return false
	}
	return true
}

func (s *Server) decodeError(w http.ResponseWriter, r *http.Request, err error) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		s.reject(w, r, http.StatusRequestEntityTooLarge, fmt.Errorf("request body exceeds %d bytes", maxRequestBytes))
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
	s.log.Warn("agent request rejected", "method", r.Method, "path", r.URL.Path, "status", status, "err", err.Error(), "peer_uid", peerUID(r))
	writeJSON(w, status, agentapi.ErrorResponse{Error: err.Error()})
}

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

- [ ] **Step 4: Tests laufen lassen (grün)**

Run: `go test -run 'Server|Listen' ./internal/agent/`
Expected: `ok  	github.com/Bernotese/mc-panel/internal/agent`

- [ ] **Step 5: Commit**

```bash
git add internal/agent/server.go internal/agent/server_test.go
git commit -m "feat(agent): add strict JSON API server" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Panel-seitiger Client und Ende-zu-Ende-Tests

**Files:**
- Create: `internal/agent/client.go`
- Test: `internal/agent/client_test.go` (Paket `agent_test`)

**Interfaces:**
- Consumes: `agentapi.Client` (Interface), `agentapi.ErrUnavailable`, `agentapi.Path*`, Request/Response-Typen; `banrules.CheckPrefix`, `banrules.SecondsFromDuration`, `banrules.ErrInvalid`, `banrules.ErrConflict`; Task 7/8 (`Listen`, `NewServer`, Helfer); `peercred.Wrap`, `peercred.AllowUIDs`
- Produces:
```go
const DefaultTimeout = 3 * time.Second
var ErrInvalid = banrules.ErrInvalid   // re-export for the panel
var ErrConflict = banrules.ErrConflict // re-export for the panel
type StatusError struct {
	StatusCode int
	Message    string
}
func (e *StatusError) Error() string
func (e *StatusError) Is(target error) bool // 409 → ErrConflict; other 4xx → ErrInvalid; 5xx → agentapi.ErrUnavailable
type Client struct{ /* unexported */ }
func NewClient(socketPath string) *Client
func NewClientWithTimeout(socketPath string, timeout time.Duration) *Client
func (c *Client) Health(ctx context.Context) (agentapi.HealthResponse, error)
func (c *Client) Ban(ctx context.Context, prefix netip.Prefix, d time.Duration) error
func (c *Client) Unban(ctx context.Context, prefix netip.Prefix) error
func (c *Client) Bans(ctx context.Context) ([]agentapi.Ban, error)
```

Fehlersemantik für Aufrufer (Stream B / Phase 2):
- Verbindungsfehler (Socket fehlt, `ECONNREFUSED`, Peer abgelehnt → EOF), Timeout, unlesbare Antwort und HTTP 5xx → `errors.Is(err, agentapi.ErrUnavailable) == true` (vorübergehend, später erneut versuchen). Der ursprüngliche Fehler bleibt per `%w` erhalten (`errors.Is(err, context.DeadlineExceeded)` funktioniert).
- Clientseitige Validierungsfehler und HTTP 4xx (außer 409) → `errors.Is(err, agent.ErrInvalid)` (dauerhaft, **nicht** wiederholen). HTTP 409 → `errors.Is(err, agent.ErrConflict)`.
- Ungültige Eingaben werden **vor** dem Senden abgelehnt (gleiche `banrules` wie der Server).

- [ ] **Step 1: Failing tests schreiben** – `internal/agent/client_test.go`

```go
package agent_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/peercred"
)

var _ agentapi.Client = (*agent.Client)(nil)

// startAgent runs a real server on a temporary Unix socket behind the peer
// credential check and stops it when the test ends.
func startAgent(t *testing.T, fw agentapi.Firewall, allow func(uint32) bool) (string, *syncBuffer) {
	t.Helper()
	path := socketPath(t)
	logs := &syncBuffer{}
	log := testLogger(logs)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	srv := agent.NewServer(fw, "test", log)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(peercred.Wrap(l, allow, log)) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return path, logs
}

func TestClientHealth(t *testing.T) {
	fw, _ := newNoop()
	path, _ := startAgent(t, fw, peercred.AllowUIDs(selfUID(t)))
	h, err := agent.NewClient(path).Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h != (agentapi.HealthResponse{Status: "ok", Version: "test", Firewall: "noop"}) {
		t.Fatalf("health = %+v", h)
	}
}

func TestClientBanListUnban(t *testing.T) {
	fw, clk := newNoop()
	path, _ := startAgent(t, fw, peercred.AllowUIDs(selfUID(t)))
	c := agent.NewClient(path)
	ctx := context.Background()
	p4 := netip.MustParsePrefix("203.0.113.7/32")
	p6 := netip.MustParsePrefix("2001:db8:1:2::/64")
	if err := c.Ban(ctx, p4, 15*time.Minute); err != nil {
		t.Fatalf("Ban v4: %v", err)
	}
	if err := c.Ban(ctx, p6, 90*time.Minute+500*time.Millisecond); err != nil { // rounded up to 5401 s
		t.Fatalf("Ban v6: %v", err)
	}
	clk.Advance(time.Minute)
	bans, err := c.Bans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []agentapi.Ban{{Prefix: "203.0.113.7/32", ExpiresInSeconds: 840}, {Prefix: "2001:db8:1:2::/64", ExpiresInSeconds: 5341}}
	if !slices.Equal(bans, want) {
		t.Fatalf("Bans = %+v, want %+v", bans, want)
	}
	if err := c.Unban(ctx, p4); err != nil {
		t.Fatalf("Unban: %v", err)
	}
	if err := c.Unban(ctx, p4); err != nil {
		t.Fatalf("second Unban: %v", err)
	}
	bans, err = c.Bans(ctx)
	if err != nil || !slices.Equal(bans, want[1:]) {
		t.Fatalf("Bans after unban = %+v, %v", bans, err)
	}
}

func TestClientRebanRefreshes(t *testing.T) {
	fw, _ := newNoop()
	path, _ := startAgent(t, fw, peercred.AllowUIDs(selfUID(t)))
	c := agent.NewClient(path)
	ctx := context.Background()
	p := netip.MustParsePrefix("203.0.113.7/32")
	if err := c.Ban(ctx, p, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Ban(ctx, p, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	bans, err := c.Bans(ctx)
	if err != nil || !slices.Equal(bans, []agentapi.Ban{{Prefix: "203.0.113.7/32", ExpiresInSeconds: 7200}}) {
		t.Fatalf("Bans = %+v, %v", bans, err)
	}
}

func TestClientValidatesBeforeSending(t *testing.T) {
	fw, _ := newNoop()
	path, logs := startAgent(t, fw, peercred.AllowUIDs(selfUID(t)))
	c := agent.NewClient(path)
	ctx := context.Background()
	cases := []struct {
		name string
		p    netip.Prefix
		d    time.Duration
	}{
		{"ipv4 /24", netip.MustParsePrefix("203.0.113.0/24"), time.Minute},
		{"mapped", netip.MustParsePrefix("::ffff:203.0.113.7/128"), time.Minute},
		{"host bits", netip.MustParsePrefix("2001:db8:1:2::5/64"), time.Minute},
		{"zero prefix", netip.Prefix{}, time.Minute},
		{"too short", netip.MustParsePrefix("203.0.113.7/32"), 999 * time.Millisecond},
		{"too long", netip.MustParsePrefix("203.0.113.7/32"), agentapi.MaxBanDuration + time.Second},
	}
	for _, tc := range cases {
		err := c.Ban(ctx, tc.p, tc.d)
		if !errors.Is(err, agent.ErrInvalid) || errors.Is(err, agentapi.ErrUnavailable) {
			t.Errorf("%s: Ban = %v, want ErrInvalid only", tc.name, err)
		}
	}
	if err := c.Unban(ctx, netip.MustParsePrefix("0.0.0.0/0")); !errors.Is(err, agent.ErrInvalid) {
		t.Errorf("Unban(0.0.0.0/0) = %v, want ErrInvalid", err)
	}
	if bans, err := c.Bans(ctx); err != nil || len(bans) != 0 {
		t.Fatalf("Bans = %+v, %v", bans, err)
	}
	if strings.Contains(logs.String(), "agent request rejected") {
		t.Fatalf("invalid requests reached the server:\n%s", logs.String())
	}
}

func TestClientMapsServerErrors(t *testing.T) {
	ctx := context.Background()
	p := netip.MustParsePrefix("203.0.113.7/32")

	path, _ := startAgent(t, stubFirewall{err: errors.New("netlink: boom")}, peercred.AllowUIDs(selfUID(t)))
	c := agent.NewClient(path)
	err := c.Ban(ctx, p, time.Minute)
	var se *agent.StatusError
	if !errors.As(err, &se) || se.StatusCode != 500 || se.Message != "firewall operation failed" {
		t.Fatalf("Ban error = %#v", err)
	}
	if !errors.Is(err, agentapi.ErrUnavailable) || errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("5xx must be ErrUnavailable only: %v", err)
	}
	if _, err := c.Bans(ctx); !errors.Is(err, agentapi.ErrUnavailable) {
		t.Fatalf("Bans error = %v, want ErrUnavailable", err)
	}

	conflict := stubFirewall{err: fmt.Errorf("%w: test", agent.ErrConflict)}
	path2, _ := startAgent(t, conflict, peercred.AllowUIDs(selfUID(t)))
	err = agent.NewClient(path2).Ban(ctx, p, time.Minute)
	if !errors.Is(err, agent.ErrConflict) || errors.Is(err, agentapi.ErrUnavailable) || !errors.As(err, &se) || se.StatusCode != 409 {
		t.Fatalf("conflict error = %v", err)
	}
}

func TestClientUnavailableWithoutAgent(t *testing.T) {
	ctx := context.Background()
	missing := socketPath(t)
	if _, err := agent.NewClient(missing).Health(ctx); !errors.Is(err, agentapi.ErrUnavailable) {
		t.Fatalf("missing socket: %v, want ErrUnavailable", err)
	}

	stale := socketPath(t)
	l, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()
	if err := agent.NewClient(stale).Ban(ctx, netip.MustParsePrefix("203.0.113.7/32"), time.Minute); !errors.Is(err, agentapi.ErrUnavailable) {
		t.Fatalf("stale socket: %v, want ErrUnavailable", err)
	}
}

func TestClientTimeout(t *testing.T) {
	path := socketPath(t)
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	held := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			held <- c // accept, never answer
		}
	}()
	t.Cleanup(func() {
		for {
			select {
			case c := <-held:
				_ = c.Close()
			default:
				return
			}
		}
	})

	start := time.Now()
	_, err = agent.NewClientWithTimeout(path, 200*time.Millisecond).Health(context.Background())
	if !errors.Is(err, agentapi.ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Health = %v, want ErrUnavailable wrapping DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
}

func TestClientRejectedPeer(t *testing.T) {
	fw, _ := newNoop()
	path, logs := startAgent(t, fw, func(uint32) bool { return false })
	_, err := agent.NewClient(path).Health(context.Background())
	if !errors.Is(err, agentapi.ErrUnavailable) {
		t.Fatalf("Health from disallowed uid = %v, want ErrUnavailable", err)
	}
	if !strings.Contains(logs.String(), "disallowed uid") {
		t.Fatalf("rejection not logged:\n%s", logs.String())
	}
}

func TestClientLogsPeerUID(t *testing.T) {
	fw, _ := newNoop()
	me := selfUID(t)
	path, logs := startAgent(t, fw, peercred.AllowUIDs(me))
	if err := agent.NewClient(path).Ban(context.Background(), netip.MustParsePrefix("203.0.113.7/32"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("peer_uid=%d", me); !strings.Contains(logs.String(), want) {
		t.Fatalf("log missing %q:\n%s", want, logs.String())
	}
}

func TestClientConcurrent(t *testing.T) {
	fw, _ := newNoop()
	path, _ := startAgent(t, fw, peercred.AllowUIDs(selfUID(t)))
	c := agent.NewClient(path)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range byte(50) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, i}), 32)
			if err := c.Ban(ctx, p, time.Hour); err != nil {
				t.Errorf("Ban %s: %v", p, err)
				return
			}
			if i%2 == 0 {
				if err := c.Unban(ctx, p); err != nil {
					t.Errorf("Unban %s: %v", p, err)
				}
			}
		}()
	}
	wg.Wait()
	bans, err := c.Bans(ctx)
	if err != nil || len(bans) != 25 {
		t.Fatalf("Bans = %d, %v; want 25", len(bans), err)
	}
}

func TestServerShutdownRemovesSocket(t *testing.T) {
	fw, _ := newNoop()
	path := socketPath(t)
	l, err := agent.Listen(path, 0o600, -1)
	if err != nil {
		t.Fatal(err)
	}
	log := testLogger(io.Discard)
	srv := agent.NewServer(fw, "test", log)
	allow := peercred.AllowUIDs(selfUID(t))
	done := make(chan error, 1)
	go func() { done <- srv.Serve(peercred.Wrap(l, allow, log)) }()
	if _, err := agent.NewClient(path).Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve = %v, want nil after Shutdown", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket still present after Shutdown: %v", err)
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test -run Client ./internal/agent/`
Expected: FAIL – `undefined: agent.Client`, `agent.NewClient`, `agent.StatusError`, `agent.ErrInvalid`, `agent.ErrConflict`, `agent.NewClientWithTimeout`.

- [ ] **Step 3: Implementierung** – `internal/agent/client.go`

```go
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/agent/banrules"
)

const (
	// DefaultTimeout bounds every client call, including connect.
	DefaultTimeout   = 3 * time.Second
	maxResponseBytes = 4 << 20
	baseURL          = "http://agent"
)

var (
	// ErrInvalid: the request can never succeed; do not retry.
	ErrInvalid = banrules.ErrInvalid
	// ErrConflict: the prefix overlaps a different, already banned prefix.
	ErrConflict = banrules.ErrConflict
)

// StatusError is a non-2xx answer from the agent.
type StatusError struct {
	StatusCode int
	Message    string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("agent: HTTP %d: %s", e.StatusCode, e.Message)
}

// Is maps status codes to the sentinel errors: 409 -> ErrConflict, other 4xx
// -> ErrInvalid, 5xx -> agentapi.ErrUnavailable.
func (e *StatusError) Is(target error) bool {
	switch {
	case e.StatusCode == http.StatusConflict:
		return target == banrules.ErrConflict
	case e.StatusCode >= 500:
		return target == agentapi.ErrUnavailable
	case e.StatusCode >= 400:
		return target == banrules.ErrInvalid
	}
	return false
}

// Client talks to the agent over its Unix socket. It implements
// agentapi.Client and is safe for concurrent use.
type Client struct {
	hc      *http.Client
	timeout time.Duration
}

var _ agentapi.Client = (*Client)(nil)

func NewClient(socketPath string) *Client { return NewClientWithTimeout(socketPath, DefaultTimeout) }

func NewClientWithTimeout(socketPath string, timeout time.Duration) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		// Proxy stays nil: HTTP(S)_PROXY must never redirect agent traffic.
	}
	return &Client{
		hc: &http.Client{
			Transport:     tr,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		timeout: timeout,
	}
}

func (c *Client) Health(ctx context.Context) (agentapi.HealthResponse, error) {
	var out agentapi.HealthResponse
	err := c.do(ctx, http.MethodGet, agentapi.PathHealth, nil, &out)
	return out, err
}

func (c *Client) Ban(ctx context.Context, prefix netip.Prefix, d time.Duration) error {
	if err := banrules.CheckPrefix(prefix); err != nil {
		return err
	}
	secs, err := banrules.SecondsFromDuration(d)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, agentapi.PathBan, agentapi.BanRequest{Prefix: prefix.String(), TimeoutSeconds: secs}, nil)
}

func (c *Client) Unban(ctx context.Context, prefix netip.Prefix) error {
	if err := banrules.CheckPrefix(prefix); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, agentapi.PathUnban, agentapi.UnbanRequest{Prefix: prefix.String()}, nil)
}

func (c *Client) Bans(ctx context.Context) ([]agentapi.Ban, error) {
	var out agentapi.BansResponse
	if err := c.do(ctx, http.MethodGet, agentapi.PathBans, nil, &out); err != nil {
		return nil, err
	}
	return out.Bans, nil
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("agent: encode request: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return fmt.Errorf("agent: build request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", agentapi.ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read response: %w", agentapi.ErrUnavailable, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("%w: response exceeds %d bytes", agentapi.ErrUnavailable, maxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := http.StatusText(resp.StatusCode)
		var er agentapi.ErrorResponse
		if json.Unmarshal(data, &er) == nil && er.Error != "" {
			msg = er.Error
		}
		return &StatusError{StatusCode: resp.StatusCode, Message: msg}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%w: decode response: %w", agentapi.ErrUnavailable, err)
	}
	return nil
}
```

- [ ] **Step 4: Tests laufen lassen (grün, auch mit Race-Detector)**

Run: `go test ./internal/agent/... && CGO_ENABLED=1 go test -race ./internal/agent/...`
Expected: `ok` für `github.com/Bernotese/mc-panel/internal/agent`, `…/agent/agentapi`, `…/agent/banrules`, `…/agent/firewall`, `…/agent/peercred` – in beiden Läufen, keine `DATA RACE`-Meldung.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/client.go internal/agent/client_test.go
git commit -m "feat(agent): add panel-side agent client" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Binary `cmd/agent`

**Files:**
- Create: `cmd/agent/main.go`
- Test: `cmd/agent/main_test.go` (Paket `main`)

**Interfaces:**
- Consumes: `config.LoadAgent(path string, dev bool, root string) (config.Agent, error)`, `config.Agent`, `clock.Real`, `firewall.New`, `agent.Listen`, `agent.NewServer`, `(*agent.Server).Serve/Shutdown`, `peercred.Wrap`, `peercred.AllowUIDs`, `agent.NewClient` (nur Test), `os/user.LookupId`
- Produces:
```go
var version = "dev" // -ldflags "-X main.version=<v>"
func run(ctx context.Context, args []string, stdout, stderr io.Writer) error
func socketPerms(cfg config.Agent, lookup func(uid string) (*user.User, error)) (os.FileMode, int, error)
func uidFromConfig(uid int) (uint32, error)
func newLogger(dev bool, w io.Writer) *slog.Logger
```
CLI: `agent [-config path] [-dev] [-version]`. Ohne `-config` und ohne `-dev` wird `/etc/mc-panel/agent.toml` gelesen; mit `-dev` ohne `-config` gilt das reine Dev-Profil (`config.DevAgent(cwd)`), mit `-dev -config f` wird `f` über das Dev-Profil gelegt.

- [ ] **Step 1: Failing tests schreiben** – `cmd/agent/main_test.go`

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/config"
)

// socketPath returns a short socket path (macOS limits them to 104 bytes).
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "mca")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "a.sock")
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

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

func waitForSocket(t *testing.T, path string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("run returned early: %v", err)
		default:
		}
		if fi, err := os.Lstat(path); err == nil && fi.Mode().Type() == fs.ModeSocket {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s did not appear", path)
}

func TestRunDevServesHealthAndShutsDown(t *testing.T) {
	sock := socketPath(t)
	cfg := writeConfig(t, fmt.Sprintf("socket = %q\n", sock))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr syncBuffer
	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"-dev", "-config", cfg}, &stdout, &stderr) }()
	waitForSocket(t, sock, done)

	h, err := agent.NewClient(sock).Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.Status != "ok" || h.Firewall != "noop" || h.Version != version {
		t.Fatalf("health = %+v", h)
	}
	if fi, err := os.Lstat(sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("dev socket mode = %v, %v; want 0600", fi.Mode(), err)
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
	if _, err := os.Lstat(sock); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket still present after shutdown: %v", err)
	}
	for _, want := range []string{"agent started", "agent shutting down"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("log missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestRunPrintsVersion(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"-version"}, &stdout, io.Discard); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != version+"\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{{"-nope"}, {"-dev", "serve"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Errorf("run(%v) = nil, want error", args)
		}
	}
}

func TestRunFailsWhenFirewallUnavailable(t *testing.T) {
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		t.Skip("as root this would modify the host firewall")
	}
	sock := socketPath(t)
	cfg := writeConfig(t, fmt.Sprintf("socket = %q\nfirewall = \"nftables\"\n", sock))
	err := run(context.Background(), []string{"-dev", "-config", cfg}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "firewall") {
		t.Fatalf("run = %v, want firewall error", err)
	}
	if _, err := os.Lstat(sock); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket must not be created when the firewall fails: %v", err)
	}
}

func TestSocketPerms(t *testing.T) {
	mode, gid, err := socketPerms(config.Agent{Dev: true, PanelUID: 501}, func(string) (*user.User, error) {
		return nil, errors.New("lookup must not run in dev mode")
	})
	if err != nil || mode != 0o600 || gid != -1 {
		t.Fatalf("dev = %v %d %v", mode, gid, err)
	}

	var asked string
	mode, gid, err = socketPerms(config.Agent{PanelUID: 998}, func(uid string) (*user.User, error) {
		asked = uid
		return &user.User{Uid: uid, Gid: "997"}, nil
	})
	if err != nil || mode != 0o660 || gid != 997 || asked != "998" {
		t.Fatalf("prod = %v %d %v (asked %q)", mode, gid, err, asked)
	}

	if _, _, err := socketPerms(config.Agent{PanelUID: 998}, func(string) (*user.User, error) {
		return nil, user.UnknownUserIdError(998)
	}); err == nil {
		t.Fatal("unknown panel uid must fail")
	}
	if _, _, err := socketPerms(config.Agent{PanelUID: 998}, func(string) (*user.User, error) {
		return &user.User{Gid: "staff"}, nil
	}); err == nil {
		t.Fatal("non-numeric gid must fail")
	}
}

func TestUIDFromConfig(t *testing.T) {
	for in, want := range map[int]uint32{0: 0, 998: 998, math.MaxUint32: math.MaxUint32} {
		if got, err := uidFromConfig(in); err != nil || got != want {
			t.Errorf("uidFromConfig(%d) = %d, %v", in, got, err)
		}
	}
	for _, in := range []int{-1, math.MaxUint32 + 1} {
		if _, err := uidFromConfig(in); err == nil {
			t.Errorf("uidFromConfig(%d) must fail", in)
		}
	}
}
```

- [ ] **Step 2: Test laufen lassen (rot)**

Run: `go test ./cmd/agent/`
Expected: FAIL – `undefined: run`, `version`, `socketPerms`, `uidFromConfig`.

- [ ] **Step 3: Implementierung** – `cmd/agent/main.go`

```go
// Command agent is the privileged mc-panel helper. It serves a small, strictly
// validated JSON API on a Unix socket that only the panel user (and root) may
// connect to, and manages the nftables jail sets.
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
	"github.com/Bernotese/mc-panel/internal/agent/firewall"
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
	dev := fs.Bool("dev", false, "development mode: socket ./.dev/agent.sock (0600), noop firewall, text logs")
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
	fw, err := firewall.New(cfg, clock.Real{}, log)
	if err != nil {
		return fmt.Errorf("firewall: %w", err)
	}
	mode, gid, err := socketPerms(cfg, user.LookupId)
	if err != nil {
		return err
	}
	if cfg.Dev {
		if err := os.MkdirAll(filepath.Dir(cfg.Socket), 0o750); err != nil {
			return err
		}
	}
	l, err := agent.Listen(cfg.Socket, mode, gid)
	if err != nil {
		return err
	}

	srv := agent.NewServer(fw, version, log)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(peercred.Wrap(l, peercred.AllowUIDs(panelUID, 0), log)) }()
	log.Info("agent started", "version", version, "socket", cfg.Socket, "firewall", fw.Name(),
		"panel_uid", panelUID, "socket_mode", fmt.Sprintf("%#o", mode), "dev", cfg.Dev)

	select {
	case err := <-errc:
		if err == nil {
			err = errors.New("server stopped unexpectedly")
		}
		return err
	case <-ctx.Done():
	}
	log.Info("agent shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
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

- [ ] **Step 4: Tests laufen lassen (grün) + Builds**

Run:
```bash
go test ./cmd/agent/
go build -ldflags "-X main.version=0.0.0-test" -o /tmp/mcp-agent ./cmd/agent && /tmp/mcp-agent -version
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/agent
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /dev/null ./cmd/agent
```
Expected: `ok  	github.com/Bernotese/mc-panel/cmd/agent`; Ausgabe `0.0.0-test`; beide Linux-Builds ohne Ausgabe.

- [ ] **Step 5: Commit**

```bash
git add cmd/agent
git commit -m "feat(agent): add agent daemon binary" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Gesamtverifikation und Smoke-Tests

**Files:** keine neuen Dateien.

**Interfaces:** – (prüft nur)

- [ ] **Step 1: Alle Tests, Race, Vet, Cross-Compile**

```bash
go test ./internal/agent/... ./cmd/agent/...
CGO_ENABLED=1 go test -race -count=1 ./internal/agent/... ./cmd/agent/...
go vet ./internal/agent/... ./cmd/agent/...
GOOS=linux GOARCH=amd64 go vet ./internal/agent/... ./cmd/agent/...
GOOS=linux GOARCH=amd64 go vet -tags nftintegration ./internal/agent/firewall/
golangci-lint run ./internal/agent/... ./cmd/agent/...
make test
```
Expected: alle `ok`; `go vet` und `golangci-lint` ohne Befunde (falls die `.golangci.yml` von Stream F noch nicht gemerged ist, läuft `golangci-lint` mit Standardkonfiguration – Befunde trotzdem beheben, nicht unterdrücken; vorhandene `//nolint:gosec`-Kommentare sind begründet); `make test` grün.

- [ ] **Step 2: Smoke-Test macOS (Dev)**

```bash
mkdir -p .dev/bin
go build -ldflags "-X main.version=0.0.0-smoke" -o .dev/bin/agent ./cmd/agent
.dev/bin/agent -dev & AGENT_PID=$!
sleep 1
curl -s --unix-socket .dev/agent.sock http://agent/v1/health; echo
curl -s -o /dev/null -w "%{http_code}\n" --unix-socket .dev/agent.sock -H 'Content-Type: application/json' \
  -d '{"prefix":"203.0.113.7/32","timeout_seconds":900}' http://agent/v1/firewall/ban
curl -s --unix-socket .dev/agent.sock http://agent/v1/firewall/bans; echo
curl -s --unix-socket .dev/agent.sock -H 'Content-Type: application/json' \
  -d '{"prefix":"::ffff:203.0.113.7/128","timeout_seconds":900}' http://agent/v1/firewall/ban; echo
stat -f '%Sp' .dev/agent.sock
kill $AGENT_PID; wait $AGENT_PID; ls .dev/agent.sock
```
Expected: `{"status":"ok","version":"0.0.0-smoke","firewall":"noop"}`; `204`; `{"bans":[{"prefix":"203.0.113.7/32","expires_in_seconds":900}]}`; `{"error":"invalid ban request: IPv4-mapped IPv6 prefix ::ffff:203.0.113.7/128 is not allowed, send the IPv4 address instead"}`; `srw-------`; nach `kill`: `ls: .dev/agent.sock: No such file or directory`. Im Agent-Log (stderr): `msg="agent started"`, `msg="firewall ban"`, `msg="agent request rejected"`, `msg="agent shutting down"`.

Danach `.dev/bin/agent -dev &` zweimal starten: der zweite Prozess beendet sich mit `agent: another agent is already listening on this socket: …/.dev/agent.sock`. Ersten Prozess per `kill -9` beenden, erneut starten: startet (Stale-Socket wird ersetzt).

- [ ] **Step 3: Smoke-Test Linux-VM (nftables, prod-ähnlich)**

```bash
go build -ldflags "-X main.version=0.0.0-vm" -o /tmp/mcp-agent ./cmd/agent
mkdir -p /tmp/mcp
cat > /tmp/mcp/agent.toml <<EOF
socket = "/tmp/mcp/agent.sock"
panel_uid = $(id -u)
firewall = "nftables"
EOF
sudo /tmp/mcp-agent -config /tmp/mcp/agent.toml & sleep 1
ls -l /tmp/mcp/agent.sock
curl -s --unix-socket /tmp/mcp/agent.sock http://agent/v1/health; echo
curl -s -o /dev/null -w "%{http_code}\n" --unix-socket /tmp/mcp/agent.sock -H 'Content-Type: application/json' \
  -d '{"prefix":"203.0.113.7/32","timeout_seconds":900}' http://agent/v1/firewall/ban
curl -s -o /dev/null -w "%{http_code}\n" --unix-socket /tmp/mcp/agent.sock -H 'Content-Type: application/json' \
  -d '{"prefix":"2001:db8:1:2::/64","timeout_seconds":900}' http://agent/v1/firewall/ban
sudo nft list set inet mcpanel mcpanel_jail4
sudo nft list set inet mcpanel mcpanel_jail6
sudo nft list chain inet mcpanel input
sudo chmod 0666 /tmp/mcp/agent.sock
sudo -u nobody curl -s --unix-socket /tmp/mcp/agent.sock http://agent/v1/health; echo "exit=$?"
sudo pkill -TERM -f '/tmp/mcp-agent -config'; sleep 1
sudo nft list set inet mcpanel mcpanel_jail4
sudo nft delete table inet mcpanel
```
Expected: Socket `srw-rw---- 1 root <primäre Gruppe des Users>`; Health mit `"firewall":"nftables"`; zweimal `204`; `jail4` enthält `203.0.113.7 timeout 15m expires …`; `jail6` enthält `2001:db8:1:2::/64 timeout 15m expires …`; die Chain zeigt `type filter hook input priority -10; policy accept;` und die beiden Drop-Regeln. Der `nobody`-Aufruf erhält keine Antwort (`exit=52`, „Empty reply from server“) und das Agent-Log enthält `rejecting connection from disallowed uid`. Nach dem Stoppen sind die Set-Elemente weiterhin vorhanden (Bans überleben einen Agent-Neustart); `nft delete table inet mcpanel` räumt auf.

- [ ] **Step 4: Abschluss-Commit (nur falls Steps 1–3 Korrekturen erforderten)**

```bash
git add cmd/agent internal/agent
git commit -m "fix(agent): address verification findings" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
## Self-Review

**Spec-Abdeckung (Stream C laut Spec Abschnitt 11 und Auftrag):**

| Anforderung | Task |
|---|---|
| net/http über Unix-Socket, Stale-Socket entfernen, Doppelstart ablehnen, 0660 Prod / 0600 Dev | 7, 10 |
| Peer-Credentials pro Verbindung (Linux `SO_PEERCRED`, macOS `LOCAL_PEERCRED`, Build-Tags), injizierbare Prüfung | 2, 9 |
| Strikte JSON-Dekodierung (`DisallowUnknownFields`, 4 KiB), Endpunkte health/ban/unban/bans, `ErrorResponse` + Statuscodes | 8 |
| Validierung mit `agentapi.ValidateBanPrefix` (verschärft), Timeout 1 s … `MaxBanDuration` | 1, 8 |
| Strukturiertes Logging jedes Ban/Unban, Graceful Shutdown, Version per `-ldflags` | 8, 10 |
| Noop-Backend mit Clock und Ablauf | 3 |
| nftables-Backend: Tabelle/Sets/Chain/Regeln idempotent, Re-Ban erneuert Timeout, Unban ohne Fehler bei Abwesenheit, List mit Restlaufzeit, fremde Tabellen unberührt | 4, 5 |
| nftables-Integrationstest (Build-Tag `nftintegration`, root, eigenes Netzwerk-Namespace) | 5 |
| Reine nftables-Logik ohne root testbar | 4 |
| Factory nach Config + `runtime.GOOS` (nftables auf Nicht-Linux → Fehler) | 6 |
| Client: Unix-`DialContext`, 3 s pro Aufruf, Verbindungsfehler → `ErrUnavailable` (gewrappt), Nicht-2xx → Fehler mit Servermeldung, E2E-Tests | 9 |
| `cmd/agent`: Flags `-config`, `-dev`, `config.LoadAgent`, Signale | 10 |

**Placeholder-Scan:** Keine „TBD“/„später“-Stellen; jeder Task enthält vollständigen Test- und Implementierungscode, exakte Befehle und erwartete Ausgaben. Einzige bewusst bedingte Anweisung: Task 5 Step 5 (Assertion der Chain-Priorität an die tatsächliche Dekodierung anpassen, falls eine andere google/nftables-Version sie anders liefert) – mit konkreter Prüfmethode.

**Typ-/Namenskonsistenz:** `banrules.ErrInvalid`/`ErrConflict` werden als `agent.ErrInvalid`/`agent.ErrConflict` re-exportiert (identische Werte, `errors.Is` funktioniert über beide Namen). `firewall.New` liefert `agentapi.Firewall`; `agent.NewServer` konsumiert `agentapi.Firewall`; `*agent.Client` erfüllt `agentapi.Client` (Compile-Zeit-Assertion in `client.go` und `client_test.go`). Konstanten `firewall.TableName/Set4Name/Set6Name/ChainName/ChainPriority` werden in Backend und Integrationstest gemeinsam benutzt.

**Getroffene Entscheidungen (bitte im Review bestätigen):**

1. **UID 0 ist zusätzlich zu `panel_uid` erlaubt.** root kann nftables ohnehin direkt ändern, gewinnt also keine Rechte dazu; dafür funktioniert Admin-Debugging per `sudo curl --unix-socket /run/mc-panel/agent.sock http://agent/v1/firewall/bans`. Alle anderen UIDs – auch Mitglieder der Gruppe `mcpanel` – werden abgewiesen. Dateirechte (0660) und Peer-UID sind zwei unabhängige Schichten.
2. **Strengere Validierung als Phase 0.** `banrules.CheckPrefix` lehnt zusätzlich IPv4-mapped IPv6, Host-Bits, Link-Local und Multicast ab; der Draht verlangt kanonische Schreibweise. Für Stream B ändert sich nichts: `httpx.IPKey` liefert immer unmapped, maskierte /32 bzw. /64 (Link-Local/Multicast kommen als echte Client-IPs nicht vor).
3. **Überlappende Bans → 409.** Ein /128 innerhalb eines gebannten /64 wird abgelehnt (nftables-Intervall-Sets ohne `auto-merge` würden ohnehin scheitern). Unban erwartet exakt den gebannten Prefix.
4. **Re-Ban ersetzt den Timeout, auch verkürzend.** Stream B sendet stets die aktuelle Restdauer des Jails; „längster gewinnt“ würde aufgehobene Eskalationen nicht abbilden.
5. **Zustand nach Neustart:** Noop verliert alle Bans bei einem Agent-Neustart (akzeptiert: nur Dev/Notbetrieb, Prod-Warnung im Log). nftables-Bans überleben einen Agent-Neustart (Setup leert nur die Chain, nie die Sets; pinned durch `TestNFTablesBansSurviveAgentRestart`), aber **keinen Host-Reboot**. Abgleich siehe Wiring-Vertrag.
6. **Input-Hook schützt nur den Host.** Pakete zu Docker-veröffentlichten Ports laufen über `forward`, nicht `input`. Für Teilprojekt 1 (nur Panel hinter Caddy) genügt `input`; Teilprojekt 2 muss eine `forward`-Chain (gleiche Sets) ergänzen.
7. **Fehlertexte auf dem Draht:** `ErrorResponse.Error` enthält eine lesbare Meldung (Phase-0-Typ hat nur dieses Feld). Firewall-interne Fehler werden nur geloggt; der Client sieht `firewall operation failed`.

**Versionsannahmen:** google/nftables-API gegen Tag **v0.3.0** geprüft (`set.go`, `conn.go`, `chain.go`, `table.go`, `rule.go`, `expr/*`): `SetElement{Key, IntervalEnd, Timeout, Expires}`, `AddSet` mit `NLM_F_CREATE`, `Flush` als atomarer Batch, `WithNetNSFd`. Falls `go get` in Phase 0 eine neuere Version gezogen hat: `go doc github.com/google/nftables SetElement` und `go doc github.com/google/nftables Conn.AddSet` gegen die Annahmen prüfen. x/sys/unix gegen `golang/sys` master geprüft (`GetsockoptUcred`, `GetsockoptXucred`, `SOL_LOCAL = 0x0`, `LOCAL_PEERCRED = 0x1`, `Unshare`, `Setns`, `CLONE_NEWNET`). Kernel-Verhalten (nicht per Bibliothek verifizierbar, durch Task 5 gepinnt): Timeout-Attribut nur am Intervall-Start; `add element` auf existierendes Element ändert den Timeout nicht; abgelaufene Elemente können bis zur GC mit `Expires == 0` erscheinen (wird gefiltert). Go ≥ 1.22 (range-over-int, Loop-Variablen pro Iteration), `t.Context()` in `server_test.go` ≥ 1.24.

## Wiring-Vertrag

**Panel (Phase 2, `cmd/panel serve`):**

```go
import (
	"github.com/Bernotese/mc-panel/internal/agent"
	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// cfg is config.Panel; cfg.AgentSocket = /run/mc-panel/agent.sock (prod) or <root>/.dev/agent.sock (dev).
var agentClient agentapi.Client = agent.NewClient(cfg.AgentSocket) // 3 s timeout per call, safe for concurrent use
// Hand agentClient to Stream B (defense) wherever it expects an agentapi.Client.
```

- Konstruktoren: `func NewClient(socketPath string) *Client`, `func NewClientWithTimeout(socketPath string, timeout time.Duration) *Client`. Kein `Close` nötig (Idle-Verbindungen schließen nach 30 s).
- Fehlerbehandlung im Panel: `errors.Is(err, agentapi.ErrUnavailable)` → vorübergehend (Sync später wiederholen, UI `503 agent_unavailable`); `errors.Is(err, agent.ErrInvalid)` → dauerhaft (nicht endlos wiederholen, loggen); `errors.Is(err, agent.ErrConflict)` → dauerhaft (überlappender Ban existiert bereits). `*agent.StatusError` trägt Statuscode und Servermeldung.
- `GET /api/security/agent` (Stream B, `AgentStatus`): `reachable = Health(ctx) == nil`, `firewall`/`version` aus `agentapi.HealthResponse`.
- Empfehlung an Stream B/Phase 2 (nicht Teil dieses Streams): Nach (Wieder-)Erreichbarkeit des Agents `Bans()` mit den aktiven Jails abgleichen und fehlende Bans erneut senden – deckt Host-Reboot (leere nftables-Sets) und Noop-Neustart ab.
- Das Panel importiert nur `internal/agent` (+ `agentapi`); `internal/agent/firewall` und damit `google/nftables` landen nicht im Panel-Binary.

**Agent-Binary:**

| | Dev (macOS / Linux-VM) | Prod (Linux) |
|---|---|---|
| Start | `scripts/dev.sh` baut `.dev/bin/agent` und startet `.dev/bin/agent -dev` aus dem Repo-Root | `agent -config /etc/mc-panel/agent.toml` (ohne `-config` wird genau dieser Pfad gelesen) |
| Socket | `./.dev/agent.sock`, `0600`, Verzeichnis wird angelegt | `/run/mc-panel/agent.sock`, `0660`, Gruppe = primäre Gruppe von `panel_uid` |
| Erlaubte UIDs | eigene UID (`config.DevAgent`), 0 | `panel_uid` aus der Config, 0 |
| Firewall | `noop` | `nftables` (Default) |
| Logs | Text, Debug, stderr | JSON, Info, stderr (journald) |
| Version | `go build` ohne ldflags → `dev` | `-ldflags "-X main.version={{.Version}}"` (GoReleaser, Stream F) |

Beispiel `/etc/mc-panel/agent.toml` (Teilprojekt 6 erzeugt sie im Installer):

```toml
socket = "/run/mc-panel/agent.sock"
panel_uid = 998          # uid of the mcpanel user
firewall = "nftables"
```

**Benötigte Laufzeit-Privilegien (systemd-Unit selbst ist Teilprojekt 6):**

- `CAP_NET_ADMIN` für nftables über Netlink (`NETLINK_NETFILTER`).
- Schreibrecht im Socket-Verzeichnis: `/run/mc-panel` (systemd `RuntimeDirectory=mc-panel`, Modus `0750`, Gruppe `mcpanel`, damit der Panel-User den Pfad traversieren kann).
- `CAP_CHOWN`, falls der Agent nicht als root läuft (Socket-Gruppe setzen); Lesezugriff auf `/etc/passwd` (`os/user.LookupId`, mit `CGO_ENABLED=0` reine Go-Implementierung).
- Netzwerk-Adressfamilien: `AF_UNIX`, `AF_NETLINK` (für `RestrictAddressFamilies=`).
- Kein Zugriff auf den Docker-Socket in Teilprojekt 1 (kommt mit Teilprojekt 2, dann zusätzlich Gruppe `docker`).
- Einfachste korrekte Variante: Agent als root mit Härtung (`NoNewPrivileges=yes`, `ProtectSystem=strict`, `ProtectHome=yes`, `PrivateTmp=yes`, `CapabilityBoundingSet=CAP_NET_ADMIN CAP_CHOWN`, `RestrictAddressFamilies=AF_UNIX AF_NETLINK`); Ausgestaltung in Teilprojekt 6.
- Für den Integrationstest zusätzlich `CAP_SYS_ADMIN` (`unshare(CLONE_NEWNET)`): CI-Job (Stream F) mit `go test -c -tags nftintegration -o /tmp/firewall.test ./internal/agent/firewall/ && sudo /tmp/firewall.test -test.v -test.run NFTables` auf `ubuntu-latest`.
