# Stream B – Abwehr & Benachrichtigungen – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Die Abwehrschicht von mc-panel bauen: Token-Bucket-Rate-Limits, Jail mit Eskalation, IP-Allow-/Denylist, Länderfilter über DB-IP-Lite (MMDB), IP-Filter- und API-Rate-Limit-Middleware, Firewall-Sync zum agent, Security-Center-API (11 Endpunkte), Notifier (Discord, ntfy) mit asynchronem Dispatcher und CRUD-API (5 Endpunkte) sowie die CLI-Funktion für `panel admin unlock`.

**Architecture:** `defense.Service` implementiert `defense.LoginGuard` (Phase 0) und hält Regeln, aktive Jails und Sicherheitseinstellungen als unveränderlichen Snapshot im Speicher (`atomic.Pointer`, Copy-on-Write); Middleware liest nur den Snapshot, nie die DB. Persistenz ausschließlich über sqlc-Queries mit Präfix `Defense` in `internal/store/queries/defense.sql`. Der Firewall-Sync gleicht `jails.fw_banned_at`/`fw_unbanned_at` mit dem agent ab und toleriert dessen Ausfall. `notify.Dispatcher` implementiert `notify.Notifier` (nicht blockierend, gepufferter Kanal, Dedup, Retry) und lädt die Notifier-Konfiguration verschlüsselt (`secretbox`) aus der DB. HTTP-Handler registrieren sich mit vollem Pfad auf dem übergebenen `*rbac.Router`.

**Tech Stack:** Go, `golang.org/x/time/rate` (Token-Bucket, Methoden mit explizitem `time.Time`), `github.com/oschwald/maxminddb-golang/v2` (geprüft gegen v2.0.0 und `main`: `maxminddb.Open(path string, ...ReaderOption) (*Reader, error)`, `(*Reader).Lookup(netip.Addr) Result`, `Result.Decode(any) error` – liefert `nil` und lässt das Ziel unverändert, wenn nichts gefunden wurde; IPv6-Lookups in einer reinen IPv4-DB liefern einen Fehler), chi v5 (nur in Tests für den Mux), sqlc, modernc.org/sqlite, stdlib `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md` (§7 Abwehr und Security Center, §7.5 Benachrichtigungen, §9 Fehlerbehandlung)

## Global Constraints

Es gelten vollständig die Global Constraints der Übersicht (`docs/superpowers/plans/2026-10-08-foundation-00-overview.md`) und die „Regeln für Streams“ aus Phase 0. Phase-0-Dateien (insbesondere `internal/defense/guard.go`, `internal/defense/fake.go`, `internal/notify/notify.go`, `internal/store/*` außer `defense.sql`/`defense.sql.go`, `api/openapi.yaml`) sind schreibgeschützt.

Stream-spezifisch, mit exakten Werten:

- **Eigentum:** `internal/defense/**` (außer `guard.go`, `fake.go`), `internal/notify/**` (außer `notify.go`), `internal/store/queries/defense.sql`, `internal/store/defense.sql.go`. Keine anderen Dateien anfassen.
- **sqlc:** Alle Query-Namen beginnen mit `Defense` (auch die Notifier-Queries). Nach jeder Änderung an `defense.sql`: `sqlc generate`; `internal/store/defense.sql.go` wird mit committet. Benannte Parameter immer per `sqlc.arg(name)` (stabile Feldnamen). Falls sqlc für einen Parameter in einem arithmetischen Ausdruck (`sqlc.arg(start)`, `sqlc.arg(bucket_ms)`, `sqlc.arg(any_success)`, `sqlc.arg(any_ip)`) den Typ `interface{}` erzeugt, kompiliert der Plan-Code unverändert, weil nur `int64`-Werte zugewiesen werden.
- **Zeit:** ausschließlich `clock.Clock`; DB-Zeitstempel in Unix-ms. Ticker in `Run`-Schleifen dürfen Echtzeit nutzen (Infrastruktur, nicht Geschäftslogik); Tests rufen die `…Once`-Methoden direkt auf.
- **IP-Normalisierung:** Client-IP = `httpx.ClientIP(ctx)`, intern immer `ip.Unmap().WithZone("")`. Schlüssel für Rate-Limits und Jails = `httpx.IPKey(ip)` (IPv4 `/32`, IPv6 `/64`), als String in `login_attempts.ip_key` und `jails.ip_key`. `login_attempts.ip` = volle, ungemappte IP.
- **Rate-Limits** (Token-Bucket, `rate.Every(Every)`, Burst):
  - Login pro IP-Key: `Every = 6s` (10/min), `Burst = 5`
  - Login pro Benutzername (`strings.ToLower(strings.TrimSpace(username))`): `Every = 12s` (5/min), `Burst = 5`
  - 2FA pro `key + "|" + ipKey`: `Every = 12s` (5/min), `Burst = 5`
  - API pro Session (`"session:<id>"`) bzw. anonym pro IP-Key (`"ip:<ipkey>"`), nur Pfade mit Präfix `/api/`: `Every = 200ms` (300/min), `Burst = 60`
  - Leerlaufende Buckets werden nach `10 min` ohne Zugriff entfernt (Aufräumen jede Minute).
  - Überschreitung → `429 rate_limited`, Header `Retry-After` = aufgerundete Sekunden, mindestens 1.
- **Jail:** Fehlversuche (`success = 0`, alle Stufen `password`, `totp`, `webauthn`, `recovery`, `sudo` und alle Stream-A-Gründe wie `bad_password`, `unknown_user`, `bad_code`, `replayed_code`, `bad_assertion`, `no_ceremony`, `bad_current_password`; **bewusst auch `admin_network`**: ein korrektes Admin-Passwort aus einem nicht erlaubten Netz ist verdächtig und darf nicht beliebig oft probiert werden – allowlistete IPs sind ohnehin ausgenommen) pro `ip_key` in den letzten `10 min`, gezählt nur nach dem `created_at` des letzten Jails dieses Keys; ab `5` → Jail. Stufe `n = 1 + Anzahl Jails dieses ip_key mit created_at in den letzten 30 Tagen`; Dauer `15 min · 2^(n-1)`, gedeckelt auf `7 d` (`168 h`). Nie gejailt: IPs, die eine aktive Allow-Regel trifft, und Loopback-Adressen (`agentapi.ValidateBanPrefix` lehnt Loopback ohnehin ab). Audit `audit.ActionJailAuto` mit `ActorType: audit.ActorSystem`, `ActorName: "system"`; Benachrichtigung `notify.EventJailed`.
- **IP-Filter (vor Sessions, für alle Pfade inkl. statischer Dateien), Reihenfolge:** (1) aktive Allow-Regel trifft → durchlassen (Allow gewinnt gegen Deny, Jail und Land); (2) aktives Jail für den IP-Key → `403 jailed` + `Retry-After`; (3) aktive Deny-Regel trifft → `403 ip_blocked`; (4) Länderfilter: `AllowedCountries` nicht leer, IP nicht loopback/privat/link-local/unspecified, Land bekannt (`!= ""`) und nicht in der Liste → `403 ip_blocked`. Unbekanntes Land (oder keine GeoIP-DB) → durchlassen.
- **Cache:** Snapshot wird (a) beim Start in `NewService`, (b) synchron nach jeder Mutation über die API (Regel anlegen/löschen, Jail anlegen/aufheben, Einstellungen) und (c) alle `30 s` in `Run` neu aus der DB geladen. Neue Auto-Jails werden zusätzlich sofort per Copy-on-Write in den Snapshot eingetragen. Regel- und Jail-Abläufe werden bei jedem Request gegen `clock.Now()` geprüft, nicht erst beim Reload.
- **Firewall-Sync:** alle `10 s` und sofort nach jedem neuen/aufgehobenen Jail (Kick-Kanal, Kapazität 1). Pro Zyklus: (1) Unbans aufgehobener Jails, (2) Bans neuer Jails, (3) Abgleich (Reconcile): aktive Jails mit gesetztem `fw_banned_at`, deren Präfix in `client.Bans()` fehlt (agent-Neustart, Host-Reboot, leere nftables-Sets), werden mit Restlaufzeit erneut gebannt. Fehlerklassen des Stream-C-Clients: Fehler, die `agentapi.ErrUnavailable` wrappen (`errors.Is`), brechen den Zyklus ab und werden im nächsten Zyklus wiederholt; alle anderen Fehler sind permanent (HTTP 400/409 des agents, z. B. ungültiges/überlappendes Präfix): `slog.Error`, Jail-ID im Speicher als „nicht bannbar“ markiert und nicht erneut versucht (permanent fehlgeschlagene Unbans werden als `fw_unbanned_at` markiert, weil es nichts mehr zu entfernen gibt). Agent-Aufrufe mit `5 s` Timeout. Ban-Dauer = Restlaufzeit des Jails (≥ 1 s, sonst übersprungen).
- **Aufbewahrung:** `login_attempts` älter als `90 d` werden stündlich gelöscht (und einmal beim Start von `Run`).
- **Selbstaussperrung:** Jede Änderung an IP-Regeln oder Einstellungen wird gegen einen Kandidaten-Snapshot geprüft; würde die aktuelle Client-IP des Aufrufers danach durch Deny-Regel, Länderfilter oder „Admin nur aus Allowlist“ ausgeschlossen (Loopback gilt für Letzteres immer als erlaubt) → `409 conflict`. Manuelles Jail auf den eigenen IP-Key oder einen allowlisteten Bereich → `409 conflict`.
- **Notifier:** Kanal-Kapazität `100`; voll → Event verwerfen + `slog.Warn`. Dedup: gleicher `Type` + nicht leere `IP` höchstens einmal pro `5 min` (Events ohne IP und `EventTest` werden nie dedupliziert). Zustellung: 1 Versuch + 3 Wiederholungen mit Backoff `1 s, 2 s, 4 s`; `4xx` außer `429` sowie Redirects gelten als permanent (kein Retry). HTTP-Client: Timeout `5 s`, folgt keinen Redirects. Konfiguration als JSON verschlüsselt in `notifiers.config_enc`, AAD `"notifier:" + kind`.
- **Notifier-URL-Validierung:** absolute URL, max. 2048 Zeichen, keine Credentials, keine Query, kein Fragment; Schema `https` (`http` nur bei `cfg.Dev`). Discord: Host exakt `discord.com` oder `discordapp.com`, kein Port, Pfad `/api/webhooks/<numerische id>/<token>`, `topic`/`token` müssen leer sein. ntfy: `topic` passt auf `^[A-Za-z0-9_-]{1,64}$`, `token` ≤ 256 Zeichen ohne Leer-/Steuerzeichen; außerhalb von Dev keine Hosts `localhost`/`*.localhost` oder Loopback/privat/link-local-IP-Literale.
- **Testbenachrichtigung (`POST /security/notifiers/{id}/test`):** synchron, ein Versuch. Erfolg → `204`; unbekannte ID → `404`; Zustellfehler → `400 validation_failed` mit bereinigter Meldung (ohne URL/Token). **Abweichung vom OpenAPI-Vertrag** (dort nur 204/404) → Lead ergänzt in Phase 2 `'400': BadRequest` für `testNotifier` (siehe Wiring-Vertrag).
- **Event-Eigentum:** Stream B sendet `notify.EventNewDeviceLogin` (in `RecordAttempt` bei der ersten vollständigen Anmeldung eines Benutzers von einem IP-Key; vollständige Anmeldung = `Success: true` und `Reason: "complete"` laut Stream-A-Vertrag, unabhängig von der Stufe; `second_factor_pending` und `sudo` zählen nicht) und `notify.EventJailed`. Stream A sendet `EventNewDeviceLogin` **nicht** selbst.
- **Audit:** Jeder mutierende Endpunkt schreibt genau einen Eintrag mit den Phase-0-Konstanten (`ActionJailCreate`, `ActionJailLift`, `ActionIPRuleCreate`, `ActionIPRuleDelete`, `ActionSettingsUpdate`, `ActionNotifierCreate`, `ActionNotifierUpdate`, `ActionNotifierDelete`); `ActorType` bleibt leer (Recorder füllt aus Principal). Details enthalten nie Webhook-URLs, Topics oder Tokens. Stream B öffnet keine eigenen DB-Transaktionen; `audit.Recorder.Record` wird immer erst **nach** dem erfolgreichen Schreiben aufgerufen (Stream Ds `audit.SQLRecorder` nutzt eine eigene `BEGIN IMMEDIATE`-Transaktion und würde sonst blockieren).

## Review Focus

Aus der Übersicht gehören Stream B:

1. **IPv4-mapped IPv6 und IPv6-Rotation (Übersicht Punkt 1, Jail-Teil):** Zwei Adressen aus demselben `/64` teilen sich einen Fehlversuchszähler und ein Jail; `::ffff:203.0.113.7` und `203.0.113.7` zählen gemeinsam. → Task 4: `TestJailIPv6SameSlash64SharesCounter`, `TestJailMappedIPv4SharesCounter`; Task 3: `TestCheckLoginIPv6Slash64SharesBucket`.
2. **Agent nicht erreichbar (Übersicht Punkt 4):** Login, Jail in der App und Filter funktionieren ohne agent; Bans werden nachgetragen, sobald er antwortet. → Task 7: `TestSyncCatchesUpAfterAgentOutage` (Fake-Client schlägt erst mit `agentapi.ErrUnavailable` fehl, dann Erfolg), `TestSyncReconcilesMissingBans` (leere Ban-Liste nach agent-Neustart → erneuter Ban), `TestSyncPermanentErrorIsNotRetried`, `TestRunKicksSyncAfterJail`.

Stream-spezifische Fehlermodi:

3. **Selbstaussperrung über die API:** Eine Deny-Regel, das Löschen der eigenen Allow-Regel, „Admin nur aus Allowlist“ oder ein Länderfilter darf den aufrufenden Admin nicht aussperren. → Task 9: `TestCreateIPRuleRejectsSelfLockout`, `TestDeleteIPRuleRejectsSelfLockout`; Task 10: `TestPutSettingsRejectsSelfLockout`; Task 8: `TestCreateJailRejectsOwnIPAndAllowlisted`.
4. **Veralteter Cache:** Weil Middleware nur den Snapshot liest, könnte ein neues Jail oder eine neue Deny-Regel bis zu 30 s wirkungslos bleiben. → Task 5: `TestJailBlocksImmediatelyWithoutReload`; Task 9: `TestCreatedDenyRuleBlocksImmediately`.
5. **Secret-Leck bei Notifiern:** Webhook-Token, ntfy-Topic und Access-Token dürfen weder in API-Antworten (`target_masked`), noch in Fehlermeldungen, noch im Audit-Log auftauchen. → Task 13: `TestMaskTargetHidesSecrets`; Task 14: `TestSenderErrorsDoNotLeakURL`; Task 16: `TestNotifierAPINeverReturnsSecrets`.

## Dateistruktur

```
internal/defense/
  limiter.go            keyedLimiter: Token-Buckets pro Schlüssel (x/time/rate), Leerlauf-Aufräumen, RetryAfterSeconds
  limiter_test.go
  service.go            Service, Deps, NewService, Limits/Konstanten, Kick-Kanal; erfüllt LoginGuard
  rules.go              snapshot (Allow/Deny/Jails/Settings), Matching, Clone, Reload, addJail, Sperrgründe
  rules_test.go
  testutil_test.go      env-Testumgebung (storetest, clock.Fake, MemoryRecorder, notify.Recording, fakeGeo)
  guard.go              (Phase 0, nur lesen)
  fake.go               (Phase 0, nur lesen)
  check.go              CheckLogin, CheckSecondFactor, CheckAdminNetwork
  check_test.go
  jail.go               RecordAttempt, Jail-Eskalation (JailDuration), New-Device-Benachrichtigung
  jail_test.go
  middleware.go         IPFilter, APIRateLimit
  middleware_test.go
  geoip.go              GeoIP (MMDB), LoadGeo, isLocal
  geoip_test.go         inkl. Mini-MMDB-Writer für Tests
  sync.go               SyncOnce (Firewall), PruneOnce, cleanupLimiters, Run
  sync_test.go          fakeAgent
  handler.go            Handler, NewHandler, gemeinsame JSON-Typen und Helfer
  routes.go             Routes(api *rbac.Router) – 11 Security-Endpunkte
  routes_test.go        Routen-Tabellen-Test (Pfad → rbac-Regel)
  handler_test.go       Router-/Request-Helfer für Handler-Tests
  lockout.go            Kandidaten-Snapshots (withRule/withoutRule/withSettings) und locksOut
  jails_api.go          GET/POST /security/jails, DELETE /security/jails/{id}
  jails_api_test.go
  rules_api.go          GET/POST /security/ip-rules, DELETE /security/ip-rules/{id}
  rules_api_test.go
  settings_api.go       GET/PUT /security/settings
  settings_api_test.go
  stats_api.go          GET /security/overview, /security/attempts, /security/agent
  stats_api_test.go
  unlock.go             Unlock (CLI `panel admin unlock`)
  unlock_test.go
internal/notify/
  notify.go             (Phase 0, nur lesen)
  config.go             Kind, Config, ValidateConfig, MaskTarget, SealConfig/OpenConfig
  config_test.go
  sender.go             Sender, PermanentError, NewHTTPClient, NewSender, Textaufbereitung
  discord.go            Discord-Webhook-Sender
  ntfy.go               ntfy-Sender
  sender_test.go
  dispatcher.go         Dispatcher (Notifier), Dedup, Queue, Retry, SendTest
  dispatcher_test.go
  api.go                Handler + Routes – 5 Notifier-Endpunkte
  api_test.go
internal/store/queries/defense.sql   alle Stream-B-Queries (Präfix Defense)
internal/store/defense.sql.go        generiert (sqlc)
```

---

### Task 1: Token-Bucket-Limiter pro Schlüssel

**Files:**
- Create: `internal/defense/limiter.go`
- Test: `internal/defense/limiter_test.go`

**Interfaces:**
- Consumes: `golang.org/x/time/rate` – `rate.NewLimiter(rate.Limit, int) *rate.Limiter`, `rate.Every(time.Duration) rate.Limit`, `(*Limiter).ReserveN(time.Time, int) *Reservation`, `(*Reservation).OK() bool`, `(*Reservation).DelayFrom(time.Time) time.Duration`, `(*Reservation).CancelAt(time.Time)`.
- Produces:
```go
type Limit struct{ Every time.Duration; Burst int }
func newKeyedLimiter(l Limit) *keyedLimiter
func (k *keyedLimiter) take(key string, now time.Time) time.Duration   // 0 = allowed, else wait time (no token consumed)
func (k *keyedLimiter) cleanup(now time.Time, idle time.Duration) int   // removed entries
func (k *keyedLimiter) size() int
func RetryAfterSeconds(d time.Duration) int                           // ceil seconds, min 1
```

- [ ] **Step 1: Failing test `internal/defense/limiter_test.go`**

```go
package defense

import (
	"testing"
	"time"
)

var limT0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func TestKeyedLimiterBurstThenRefill(t *testing.T) {
	k := newKeyedLimiter(Limit{Every: 6 * time.Second, Burst: 5})
	for i := 0; i < 5; i++ {
		if d := k.take("a", limT0); d != 0 {
			t.Fatalf("take %d denied (wait %v)", i, d)
		}
	}
	d := k.take("a", limT0)
	if d <= 5900*time.Millisecond || d > 6*time.Second {
		t.Fatalf("6th take wait = %v, want ~6s", d)
	}
	// a denied take must not consume a token
	if d2 := k.take("a", limT0); d2 <= 5900*time.Millisecond {
		t.Fatalf("second denied take wait = %v, want ~6s", d2)
	}
	if d := k.take("a", limT0.Add(6*time.Second+time.Millisecond)); d != 0 {
		t.Fatalf("after refill denied (wait %v)", d)
	}
	if d := k.take("b", limT0); d != 0 {
		t.Fatalf("other key must have its own bucket, wait %v", d)
	}
}

func TestKeyedLimiterCleanup(t *testing.T) {
	k := newKeyedLimiter(Limit{Every: time.Second, Burst: 1})
	k.take("old", limT0)
	k.take("new", limT0.Add(9*time.Minute))
	if n := k.cleanup(limT0.Add(10*time.Minute), 10*time.Minute); n != 1 {
		t.Fatalf("cleanup removed %d, want 1", n)
	}
	if k.size() != 1 {
		t.Fatalf("size = %d, want 1", k.size())
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	cases := map[time.Duration]int{
		0:                       1,
		200 * time.Millisecond:  1,
		time.Second:             1,
		1001 * time.Millisecond: 2,
		5999 * time.Millisecond: 6,
	}
	for d, want := range cases {
		if got := RetryAfterSeconds(d); got != want {
			t.Errorf("RetryAfterSeconds(%v) = %d, want %d", d, got, want)
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/defense/ -run 'KeyedLimiter|RetryAfter'` → FAIL (`undefined: newKeyedLimiter`).

- [ ] **Step 3: Implementation `internal/defense/limiter.go`**

```go
package defense

import (
	"math"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limit describes a token bucket: one token every Every, at most Burst tokens.
type Limit struct {
	Every time.Duration
	Burst int
}

type limiterEntry struct {
	lim  *rate.Limiter
	last time.Time
}

// keyedLimiter keeps one token bucket per key. All methods take the current
// time explicitly so tests can use clock.Fake.
type keyedLimiter struct {
	limit   Limit
	mu      sync.Mutex
	entries map[string]*limiterEntry
}

func newKeyedLimiter(l Limit) *keyedLimiter {
	return &keyedLimiter{limit: l, entries: make(map[string]*limiterEntry)}
}

// take consumes one token for key. It returns 0 if the request is allowed,
// otherwise the duration until a token becomes available; in that case no
// token is consumed.
func (k *keyedLimiter) take(key string, now time.Time) time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()
	e, ok := k.entries[key]
	if !ok {
		e = &limiterEntry{lim: rate.NewLimiter(rate.Every(k.limit.Every), k.limit.Burst)}
		k.entries[key] = e
	}
	e.last = now
	r := e.lim.ReserveN(now, 1)
	if !r.OK() {
		return k.limit.Every
	}
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return d
	}
	return 0
}

// cleanup removes buckets that were not used for at least idle.
func (k *keyedLimiter) cleanup(now time.Time, idle time.Duration) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for key, e := range k.entries {
		if now.Sub(e.last) >= idle {
			delete(k.entries, key)
			n++
		}
	}
	return n
}

func (k *keyedLimiter) size() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.entries)
}

// RetryAfterSeconds converts a wait time into a Retry-After header value
// (whole seconds, rounded up, at least 1).
func RetryAfterSeconds(d time.Duration) int {
	s := int(math.Ceil(d.Seconds()))
	if s < 1 {
		return 1
	}
	return s
}
```

- [ ] **Step 4: Run** `go test ./internal/defense/ -run 'KeyedLimiter|RetryAfter'` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/defense/limiter.go internal/defense/limiter_test.go
git commit -m "feat(defense): add keyed token-bucket limiter" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Service-Grundgerüst, Regel-/Jail-Snapshot und Reload

**Files:**
- Create: `internal/store/queries/defense.sql`, `internal/defense/service.go`, `internal/defense/rules.go`
- Generated: `internal/store/defense.sql.go`
- Test: `internal/defense/testutil_test.go`, `internal/defense/rules_test.go`

**Interfaces:**
- Consumes: `store.New(db DBTX) *Queries`, `store.LoadSecuritySettings(ctx, *Queries) (SecuritySettings, error)`, `store.SaveSecuritySettings(ctx, *Queries, SecuritySettings, time.Time) error`, `storetest.Open(testing.TB) *sql.DB`, `storetest.CreateUser(testing.TB, *sql.DB, username, role string) int64`, `clock.Clock`, `clock.NewFake(time.Time) *clock.Fake`, `audit.Recorder`, `audit.MemoryRecorder`, `notify.Notifier`, `notify.Nop`, `notify.Recording`, `agentapi.Client`, `defense.GeoResolver`, `defense.NopGeo`.
- Produces:
```go
var LoginPerIPLimit, LoginPerUserLimit, SecondFactorLimit, APILimit Limit
const FailureWindow, FailureThreshold, JailBase, JailMax, RepeatWindow, AttemptRetention,
      RulesRefreshInterval, SyncInterval, PruneInterval, LimiterCleanupInterval, LimiterIdle,
      AgentCallTimeout, AgentStatusTimeout
type Deps struct{ DB *sql.DB; Clock clock.Clock; Geo GeoResolver; GeoAvailable bool; Audit audit.Recorder;
                  Notifier notify.Notifier; Agent agentapi.Client; Logger *slog.Logger }
func NewService(ctx context.Context, d Deps) (*Service, error)   // performs the initial Reload
func (s *Service) Reload(ctx context.Context) error
func (s *Service) GeoAvailable() bool
// internal (used by later tasks)
func (s *Service) reloadOrLog(ctx context.Context)
func (s *Service) addJail(key netip.Prefix, expires time.Time)
func (s *Service) kickSync()
func normalizeIP(ip netip.Addr) netip.Addr
func isLocal(ip netip.Addr) bool
type ruleEntry struct{ id int64; prefix netip.Prefix; expires time.Time }
type snapshot struct{ allow, deny []ruleEntry; jails map[netip.Prefix]time.Time; settings store.SecuritySettings }
func (s *snapshot) allowed(ip netip.Addr, now time.Time) bool
func (s *snapshot) denied(ip netip.Addr, now time.Time) bool
func (s *snapshot) allowOverlaps(p netip.Prefix, now time.Time) bool
func (s *snapshot) jailedUntil(key netip.Prefix, now time.Time) (time.Time, bool)
func (s *snapshot) clone() *snapshot
// sqlc
func (q *Queries) DefenseListIPRules(ctx context.Context) ([]IpRule, error)
func (q *Queries) DefenseInsertIPRule(ctx context.Context, arg DefenseInsertIPRuleParams) (IpRule, error)
func (q *Queries) DefenseListActiveJails(ctx context.Context, now int64) ([]Jail, error)
func (q *Queries) DefenseInsertJail(ctx context.Context, arg DefenseInsertJailParams) (Jail, error)
```
sqlc-Modelle (aus Phase-0-Schema generiert, Feldnamen nach sqlc-Standard, Initialismus nur `id`): `store.Jail{ID int64; IpKey, Reason string; Level, Manual, CreatedAt, ExpiresAt int64; LiftedAt sql.NullInt64; LiftedBy sql.NullString; FwBannedAt, FwUnbannedAt sql.NullInt64}`, `store.IpRule{ID int64; Cidr, Kind, Comment string; ExpiresAt sql.NullInt64; CreatedBy string; CreatedAt int64}`, `store.LoginAttempt{ID, Ts int64; Ip, IpKey, Username string; UserID sql.NullInt64; Stage string; Success int64; Reason, UserAgent, Country string}`, `store.Notifier{ID int64; Kind, Name string; ConfigEnc []byte; Enabled, CreatedAt, UpdatedAt int64}`.

- [ ] **Step 1: Queries anlegen `internal/store/queries/defense.sql`**

```sql
-- Stream B (defense + notify). All query names are prefixed with "Defense".

-- name: DefenseListIPRules :many
SELECT * FROM ip_rules ORDER BY kind, cidr;

-- name: DefenseInsertIPRule :one
INSERT INTO ip_rules (cidr, kind, comment, expires_at, created_by, created_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DefenseListActiveJails :many
SELECT * FROM jails
WHERE lifted_at IS NULL AND expires_at > sqlc.arg(now)
ORDER BY created_at DESC, id DESC;

-- name: DefenseInsertJail :one
INSERT INTO jails (ip_key, reason, level, manual, created_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;
```

```bash
sqlc generate && go build ./internal/store/ && grep -n "^func (q \*Queries) Defense" internal/store/defense.sql.go
```
Expected: vier Funktionen `DefenseListIPRules`, `DefenseInsertIPRule`, `DefenseListActiveJails(ctx context.Context, now int64)`, `DefenseInsertJail`.

- [ ] **Step 2: Test-Umgebung `internal/defense/testutil_test.go`**

```go
package defense

import (
	"context"
	"database/sql"
	"net/netip"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// fakeGeo maps normalized IP strings to country codes.
type fakeGeo map[string]string

func (g fakeGeo) Country(ip netip.Addr) string { return g[normalizeIP(ip).String()] }

type env struct {
	db      *sql.DB
	q       *store.Queries
	clk     *clock.Fake
	audit   *audit.MemoryRecorder
	notes   *notify.Recording
	geo     fakeGeo
	svc     *Service
	adminID int64
}

func newEnv(t *testing.T, opts ...func(*Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		db: db, q: store.New(db), clk: clock.NewFake(t0),
		audit: &audit.MemoryRecorder{}, notes: &notify.Recording{}, geo: fakeGeo{},
	}
	e.adminID = storetest.CreateUser(t, db, "admin", "admin")
	d := Deps{DB: db, Clock: e.clk, Geo: e.geo, GeoAvailable: true, Audit: e.audit, Notifier: e.notes}
	for _, o := range opts {
		o(&d)
	}
	svc, err := NewService(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	return e
}

func (e *env) reload(t *testing.T) {
	t.Helper()
	if err := e.svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (e *env) addRule(t *testing.T, cidr, kind string, expires *time.Time) int64 {
	t.Helper()
	var exp sql.NullInt64
	if expires != nil {
		exp = sql.NullInt64{Int64: expires.UnixMilli(), Valid: true}
	}
	r, err := e.q.DefenseInsertIPRule(context.Background(), store.DefenseInsertIPRuleParams{
		Cidr: cidr, Kind: kind, Comment: "", ExpiresAt: exp, CreatedBy: "test", CreatedAt: e.clk.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.reload(t)
	return r.ID
}

func (e *env) saveSettings(t *testing.T, s store.SecuritySettings) {
	t.Helper()
	if err := store.SaveSecuritySettings(context.Background(), e.q, s, e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	e.reload(t)
}

func (e *env) insertJail(t *testing.T, key string, created, expires time.Time) int64 {
	t.Helper()
	j, err := e.q.DefenseInsertJail(context.Background(), store.DefenseInsertJailParams{
		IpKey: key, Reason: "test", Level: 1, Manual: 0, CreatedAt: created.UnixMilli(), ExpiresAt: expires.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return j.ID
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }
```

- [ ] **Step 3: Failing test `internal/defense/rules_test.go`**

```go
package defense

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

func TestReloadBuildsSnapshot(t *testing.T) {
	e := newEnv(t)
	exp := t0.Add(time.Hour)
	e.addRule(t, "203.0.113.0/24", "allow", nil)
	e.addRule(t, "198.51.100.0/24", "deny", nil)
	e.addRule(t, "192.0.2.0/24", "deny", &exp)
	e.addRule(t, "2001:db8:1::/48", "deny", nil)
	e.saveSettings(t, store.SecuritySettings{AdminAllowlistOnly: true, AllowedCountries: []string{"DE"}})

	s := e.svc.snap.Load()
	checks := []struct {
		name      string
		got, want bool
	}{
		{"allow v4", s.allowed(addr("203.0.113.9"), t0), true},
		{"allow mapped v4", s.allowed(addr("::ffff:203.0.113.9"), t0), true},
		{"not allowed", s.allowed(addr("203.0.114.1"), t0), false},
		{"deny v4", s.denied(addr("198.51.100.7"), t0), true},
		{"deny v6", s.denied(addr("2001:db8:1:ff::1"), t0), true},
		{"deny v6 zone stripped", s.denied(addr("2001:db8:1::1%en0"), t0), true},
		{"expiring deny before expiry", s.denied(addr("192.0.2.1"), t0), true},
		{"expiring deny at expiry", s.denied(addr("192.0.2.1"), exp), false},
		{"allow overlaps /64 key", s.allowOverlaps(netip.MustParsePrefix("203.0.113.9/32"), t0), true},
		{"no overlap", s.allowOverlaps(netip.MustParsePrefix("198.51.100.7/32"), t0), false},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
	if !s.settings.AdminAllowlistOnly || len(s.settings.AllowedCountries) != 1 || s.settings.AllowedCountries[0] != "DE" {
		t.Fatalf("settings not loaded: %+v", s.settings)
	}
}

func TestReloadLoadsOnlyActiveJails(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.insertJail(t, "203.0.113.1/32", t0, t0.Add(15*time.Minute))
	e.insertJail(t, "203.0.113.2/32", t0.Add(-time.Hour), t0.Add(-time.Minute))
	lifted := e.insertJail(t, "203.0.113.3/32", t0, t0.Add(time.Hour))
	if _, err := e.db.ExecContext(ctx, "UPDATE jails SET lifted_at = ?, lifted_by = 'x' WHERE id = ?", t0.UnixMilli(), lifted); err != nil {
		t.Fatal(err)
	}
	e.reload(t)
	s := e.svc.snap.Load()
	until, ok := s.jailedUntil(netip.MustParsePrefix("203.0.113.1/32"), t0)
	if !ok || !until.Equal(t0.Add(15*time.Minute)) {
		t.Fatalf("active jail: until=%v ok=%v", until, ok)
	}
	for _, k := range []string{"203.0.113.2/32", "203.0.113.3/32"} {
		if _, ok := s.jailedUntil(netip.MustParsePrefix(k), t0); ok {
			t.Errorf("%s must not be jailed", k)
		}
	}
	if _, ok := s.jailedUntil(netip.MustParsePrefix("203.0.113.1/32"), t0.Add(15*time.Minute)); ok {
		t.Error("expiry must be evaluated against now, not only at reload")
	}
}

func TestAddJailIsCopyOnWrite(t *testing.T) {
	e := newEnv(t)
	before := e.svc.snap.Load()
	key := netip.MustParsePrefix("2001:db8:1:2::/64")
	e.svc.addJail(key, t0.Add(time.Hour))
	if _, ok := before.jailedUntil(key, t0); ok {
		t.Fatal("previous snapshot must not be mutated")
	}
	if _, ok := e.svc.snap.Load().jailedUntil(key, t0); !ok {
		t.Fatal("current snapshot must contain the new jail")
	}
}

func TestIsLocal(t *testing.T) {
	for s, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "192.168.1.1": true, "fd00::1": true,
		"fe80::1": true, "0.0.0.0": true, "203.0.113.7": false, "2001:db8::1": false, "::ffff:10.0.0.1": true,
	} {
		if got := isLocal(addr(s)); got != want {
			t.Errorf("isLocal(%s) = %v, want %v", s, got, want)
		}
	}
}
```

- [ ] **Step 4: Run** `go test ./internal/defense/ -run 'Reload|AddJail|IsLocal'` → FAIL (`undefined: NewService`, `normalizeIP`, …).

- [ ] **Step 5: Implementation `internal/defense/service.go`**

```go
package defense

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Rate limits (spec §7.1).
var (
	LoginPerIPLimit   = Limit{Every: 6 * time.Second, Burst: 5}         // 10/min per IP key
	LoginPerUserLimit = Limit{Every: 12 * time.Second, Burst: 5}        // 5/min per username
	SecondFactorLimit = Limit{Every: 12 * time.Second, Burst: 5}        // 5/min per session+IP
	APILimit          = Limit{Every: 200 * time.Millisecond, Burst: 60} // 300/min per session
)

const (
	FailureWindow    = 10 * time.Minute
	FailureThreshold = 5
	JailBase         = 15 * time.Minute
	JailMax          = 7 * 24 * time.Hour
	RepeatWindow     = 30 * 24 * time.Hour
	AttemptRetention = 90 * 24 * time.Hour

	RulesRefreshInterval   = 30 * time.Second
	SyncInterval           = 10 * time.Second
	PruneInterval          = time.Hour
	LimiterCleanupInterval = time.Minute
	LimiterIdle            = 10 * time.Minute
	AgentCallTimeout       = 5 * time.Second
	AgentStatusTimeout     = 2 * time.Second
)

// Deps are the collaborators of the defense service. Agent may be nil
// (firewall sync is then disabled and the agent is reported unreachable).
type Deps struct {
	DB           *sql.DB
	Clock        clock.Clock
	Geo          GeoResolver
	GeoAvailable bool
	Audit        audit.Recorder
	Notifier     notify.Notifier
	Agent        agentapi.Client
	Logger       *slog.Logger
}

// Service implements LoginGuard, the IP filter, the API rate limit,
// the firewall sync and the security-center API.
type Service struct {
	db           *sql.DB
	q            *store.Queries
	clock        clock.Clock
	geo          GeoResolver
	geoAvailable bool
	audit        audit.Recorder
	notifier     notify.Notifier
	agent        agentapi.Client
	log          *slog.Logger

	loginIP      *keyedLimiter
	loginUser    *keyedLimiter
	secondFactor *keyedLimiter
	api          *keyedLimiter

	snap   atomic.Pointer[snapshot] // read lock-free by middleware
	snapMu sync.Mutex               // serializes snapshot writers (Reload, addJail)
	jailMu sync.Mutex               // serializes jail decisions
	kick   chan struct{}

	syncMu     sync.Mutex
	unbannable map[int64]bool // jail IDs whose ban failed permanently
}

func NewService(ctx context.Context, d Deps) (*Service, error) {
	if d.DB == nil || d.Clock == nil || d.Audit == nil {
		return nil, errors.New("defense: DB, Clock and Audit are required")
	}
	if d.Geo == nil {
		d.Geo = NopGeo{}
	}
	if d.Notifier == nil {
		d.Notifier = notify.Nop{}
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	s := &Service{
		db: d.DB, q: store.New(d.DB), clock: d.Clock, geo: d.Geo, geoAvailable: d.GeoAvailable,
		audit: d.Audit, notifier: d.Notifier, agent: d.Agent, log: d.Logger,
		loginIP:      newKeyedLimiter(LoginPerIPLimit),
		loginUser:    newKeyedLimiter(LoginPerUserLimit),
		secondFactor: newKeyedLimiter(SecondFactorLimit),
		api:          newKeyedLimiter(APILimit),
		kick:         make(chan struct{}, 1),
		unbannable:   make(map[int64]bool),
	}
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// GeoAvailable reports whether a GeoIP database was loaded.
func (s *Service) GeoAvailable() bool { return s.geoAvailable }

// kickSync asks the firewall sync loop to run soon (non-blocking).
func (s *Service) kickSync() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}
```

- [ ] **Step 6: Implementation `internal/defense/rules.go`**

```go
package defense

import (
	"context"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

type ruleEntry struct {
	id      int64
	prefix  netip.Prefix
	expires time.Time // zero = never expires
}

func (r ruleEntry) active(now time.Time) bool { return r.expires.IsZero() || now.Before(r.expires) }

// snapshot is an immutable view of rules, active jails and settings.
// Writers build a new snapshot (copy-on-write) and store it atomically.
type snapshot struct {
	allow    []ruleEntry
	deny     []ruleEntry
	jails    map[netip.Prefix]time.Time // ip_key -> latest expiry
	settings store.SecuritySettings
}

func normalizeIP(ip netip.Addr) netip.Addr { return ip.Unmap().WithZone("") }

// isLocal reports addresses that are never subject to the country filter.
func isLocal(ip netip.Addr) bool {
	ip = normalizeIP(ip)
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()
}

func matchAny(rules []ruleEntry, ip netip.Addr, now time.Time) bool {
	ip = normalizeIP(ip)
	for _, r := range rules {
		if r.active(now) && r.prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *snapshot) allowed(ip netip.Addr, now time.Time) bool { return matchAny(s.allow, ip, now) }
func (s *snapshot) denied(ip netip.Addr, now time.Time) bool  { return matchAny(s.deny, ip, now) }

// allowOverlaps reports whether any active allow rule overlaps p (used for manual jails of /64 keys).
func (s *snapshot) allowOverlaps(p netip.Prefix, now time.Time) bool {
	for _, r := range s.allow {
		if r.active(now) && r.prefix.Overlaps(p) {
			return true
		}
	}
	return false
}

func (s *snapshot) jailedUntil(key netip.Prefix, now time.Time) (time.Time, bool) {
	until, ok := s.jails[key]
	if !ok || !now.Before(until) {
		return time.Time{}, false
	}
	return until, true
}

func (s *snapshot) clone() *snapshot {
	c := &snapshot{
		allow:    slices.Clone(s.allow),
		deny:     slices.Clone(s.deny),
		jails:    maps.Clone(s.jails),
		settings: s.settings,
	}
	if c.jails == nil {
		c.jails = make(map[netip.Prefix]time.Time)
	}
	c.settings.AllowedCountries = slices.Clone(s.settings.AllowedCountries)
	return c
}

// Reload rebuilds the snapshot from the database.
func (s *Service) Reload(ctx context.Context) error {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	now := s.clock.Now()
	rules, err := s.q.DefenseListIPRules(ctx)
	if err != nil {
		return fmt.Errorf("defense: load ip rules: %w", err)
	}
	jails, err := s.q.DefenseListActiveJails(ctx, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("defense: load jails: %w", err)
	}
	settings, err := store.LoadSecuritySettings(ctx, s.q)
	if err != nil {
		return fmt.Errorf("defense: load settings: %w", err)
	}
	snap := &snapshot{jails: make(map[netip.Prefix]time.Time, len(jails)), settings: settings}
	for _, r := range rules {
		p, err := netip.ParsePrefix(r.Cidr)
		if err != nil {
			s.log.Error("defense: skipping invalid ip rule", "id", r.ID, "cidr", r.Cidr)
			continue
		}
		e := ruleEntry{id: r.ID, prefix: p.Masked()}
		if r.ExpiresAt.Valid {
			e.expires = time.UnixMilli(r.ExpiresAt.Int64)
		}
		if r.Kind == "allow" {
			snap.allow = append(snap.allow, e)
		} else {
			snap.deny = append(snap.deny, e)
		}
	}
	for _, j := range jails {
		p, err := netip.ParsePrefix(j.IpKey)
		if err != nil {
			s.log.Error("defense: skipping jail with invalid ip_key", "id", j.ID, "ip_key", j.IpKey)
			continue
		}
		exp := time.UnixMilli(j.ExpiresAt)
		if cur, ok := snap.jails[p]; !ok || exp.After(cur) {
			snap.jails[p] = exp
		}
	}
	s.snap.Store(snap)
	return nil
}

// reloadOrLog is used after successful mutations: the change is committed, so a
// failed reload is only logged (the periodic refresh repairs the cache).
func (s *Service) reloadOrLog(ctx context.Context) {
	if err := s.Reload(ctx); err != nil {
		s.log.Error("defense: reload failed", "err", err)
	}
}

// addJail makes a new jail visible to the middleware immediately.
func (s *Service) addJail(key netip.Prefix, expires time.Time) {
	s.snapMu.Lock()
	defer s.snapMu.Unlock()
	c := s.snap.Load().clone()
	if cur, ok := c.jails[key]; !ok || expires.After(cur) {
		c.jails[key] = expires
	}
	s.snap.Store(c)
}
```

- [ ] **Step 7: Run** `go test ./internal/defense/ -run 'Reload|AddJail|IsLocal'` → PASS. Dann `go vet ./internal/defense/` → keine Meldungen.

- [ ] **Step 8: Commit**

```bash
git add internal/store/queries/defense.sql internal/store/defense.sql.go internal/defense/service.go internal/defense/rules.go internal/defense/testutil_test.go internal/defense/rules_test.go
git commit -m "feat(defense): add service skeleton with in-memory rule and jail snapshot" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Prüfungen vor Login und zweitem Faktor (`CheckLogin`, `CheckSecondFactor`, `CheckAdminNetwork`)

**Files:**
- Create: `internal/defense/check.go`
- Test: `internal/defense/check_test.go`

**Interfaces:**
- Consumes: `defense.ErrJailed`, `defense.ErrAdminNetwork`, `*defense.RateLimitError{RetryAfter}`, `httpx.IPKey(netip.Addr) netip.Prefix`, Task 1 (`keyedLimiter.take`), Task 2 (`snapshot`, `normalizeIP`, `Service.addJail`).
- Produces (Teil von `defense.LoginGuard`, Stream A ruft diese auf):
```go
func (s *Service) CheckLogin(ctx context.Context, ip netip.Addr, username string) error         // nil | ErrJailed | *RateLimitError
func (s *Service) CheckSecondFactor(ctx context.Context, ip netip.Addr, key string) error       // nil | ErrJailed | *RateLimitError
func (s *Service) CheckAdminNetwork(ctx context.Context, ip netip.Addr) error                   // nil | ErrAdminNetwork
func (s *Service) jailed(ip netip.Addr, now time.Time) bool
func normalizeUsername(u string) string
```
Semantik: Ein Login verbraucht je einen Token aus IP-Key- und Benutzernamen-Bucket (Benutzername nur, wenn der IP-Bucket erlaubt). Allowlistete IPs gelten nie als gejailt, unterliegen aber den Rate-Limits.

- [ ] **Step 1: Failing test `internal/defense/check_test.go`**

```go
package defense

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

func wantRateLimited(t *testing.T, err error, max time.Duration) {
	t.Helper()
	var rl *RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if rl.RetryAfter <= 0 || rl.RetryAfter > max {
		t.Fatalf("RetryAfter = %v, want (0, %v]", rl.RetryAfter, max)
	}
}

func TestCheckLoginRateLimitPerIP(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ip := addr("203.0.113.7")
	for i := 0; i < 5; i++ {
		if err := e.svc.CheckLogin(ctx, ip, fmt.Sprintf("user%d", i)); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, ip, "user5"), 6*time.Second)
	// IPv4-mapped form shares the bucket
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("::ffff:203.0.113.7"), "user6"), 6*time.Second)
	e.clk.Advance(6*time.Second + time.Millisecond)
	if err := e.svc.CheckLogin(ctx, ip, "user7"); err != nil {
		t.Fatalf("after refill: %v", err)
	}
}

func TestCheckLoginIPv6Slash64SharesBucket(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		ip := addr(fmt.Sprintf("2001:db8:1:2::%d", i))
		if err := e.svc.CheckLogin(ctx, ip, fmt.Sprintf("user%d", i)); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("2001:db8:1:2:ffff::9"), "user9"), 6*time.Second)
	if err := e.svc.CheckLogin(ctx, addr("2001:db8:1:3::1"), "user10"); err != nil {
		t.Fatalf("other /64 must have its own bucket: %v", err)
	}
}

func TestCheckLoginRateLimitPerUsername(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	names := []string{"admin", "Admin", " ADMIN ", "admin", "aDmIn"}
	for i, n := range names {
		ip := addr(fmt.Sprintf("198.51.100.%d", i+1))
		if err := e.svc.CheckLogin(ctx, ip, n); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	wantRateLimited(t, e.svc.CheckLogin(ctx, addr("198.51.100.99"), "admin"), 12*time.Second)
}

func TestCheckLoginJailed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.addJail(netip.MustParsePrefix("203.0.113.7/32"), t0.Add(15*time.Minute))
	for _, ip := range []string{"203.0.113.7", "::ffff:203.0.113.7"} {
		if err := e.svc.CheckLogin(ctx, addr(ip), "admin"); !errors.Is(err, ErrJailed) {
			t.Fatalf("%s: err = %v, want ErrJailed", ip, err)
		}
		if err := e.svc.CheckSecondFactor(ctx, addr(ip), "s1"); !errors.Is(err, ErrJailed) {
			t.Fatalf("%s 2FA: err = %v, want ErrJailed", ip, err)
		}
	}
	e.clk.Advance(15 * time.Minute)
	if err := e.svc.CheckLogin(ctx, addr("203.0.113.7"), "admin"); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	// an allowlisted address is never treated as jailed
	// (jail persisted in the DB so that the reload triggered by addRule keeps it)
	e.insertJail(t, "203.0.113.8/32", e.clk.Now(), e.clk.Now().Add(time.Hour))
	e.addRule(t, "203.0.113.8/32", "allow", nil)
	if _, ok := e.svc.snap.Load().jailedUntil(netip.MustParsePrefix("203.0.113.8/32"), e.clk.Now()); !ok {
		t.Fatal("precondition: jail must be in the snapshot")
	}
	if err := e.svc.CheckLogin(ctx, addr("203.0.113.8"), "bob"); err != nil {
		t.Fatalf("allowlisted: %v", err)
	}
}

func TestCheckSecondFactorRateLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ip := addr("203.0.113.7")
	for i := 0; i < 5; i++ {
		if err := e.svc.CheckSecondFactor(ctx, ip, "session-1"); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	wantRateLimited(t, e.svc.CheckSecondFactor(ctx, ip, "session-1"), 12*time.Second)
	if err := e.svc.CheckSecondFactor(ctx, ip, "session-2"); err != nil {
		t.Fatalf("other session: %v", err)
	}
	if err := e.svc.CheckSecondFactor(ctx, addr("203.0.113.8"), "session-1"); err != nil {
		t.Fatalf("same session from other IP has its own bucket: %v", err)
	}
}

func TestCheckAdminNetwork(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	public := addr("203.0.113.7")
	if err := e.svc.CheckAdminNetwork(ctx, public); err != nil {
		t.Fatalf("restriction off: %v", err)
	}
	e.saveSettings(t, store.SecuritySettings{AdminAllowlistOnly: true, AllowedCountries: []string{}})
	if err := e.svc.CheckAdminNetwork(ctx, public); !errors.Is(err, ErrAdminNetwork) {
		t.Fatalf("not allowlisted: err = %v", err)
	}
	for _, lo := range []string{"127.0.0.1", "::1"} {
		if err := e.svc.CheckAdminNetwork(ctx, addr(lo)); err != nil {
			t.Fatalf("loopback %s must always be allowed: %v", lo, err)
		}
	}
	e.addRule(t, "203.0.113.0/24", "allow", nil)
	if err := e.svc.CheckAdminNetwork(ctx, public); err != nil {
		t.Fatalf("allowlisted: %v", err)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/defense/ -run 'CheckLogin|CheckSecondFactor|CheckAdminNetwork'` → FAIL (`e.svc.CheckLogin undefined`).

- [ ] **Step 3: Implementation `internal/defense/check.go`**

```go
package defense

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/httpx"
)

func normalizeUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

// jailed reports whether ip's key has an active jail. Allowlisted IPs are never jailed.
func (s *Service) jailed(ip netip.Addr, now time.Time) bool {
	snap := s.snap.Load()
	ip = normalizeIP(ip)
	if snap.allowed(ip, now) {
		return false
	}
	_, ok := snap.jailedUntil(httpx.IPKey(ip), now)
	return ok
}

func (s *Service) CheckLogin(_ context.Context, ip netip.Addr, username string) error {
	now := s.clock.Now()
	if s.jailed(ip, now) {
		return ErrJailed
	}
	if d := s.loginIP.take(httpx.IPKey(normalizeIP(ip)).String(), now); d > 0 {
		return &RateLimitError{RetryAfter: d}
	}
	if d := s.loginUser.take(normalizeUsername(username), now); d > 0 {
		return &RateLimitError{RetryAfter: d}
	}
	return nil
}

func (s *Service) CheckSecondFactor(_ context.Context, ip netip.Addr, key string) error {
	now := s.clock.Now()
	if s.jailed(ip, now) {
		return ErrJailed
	}
	if d := s.secondFactor.take(key+"|"+httpx.IPKey(normalizeIP(ip)).String(), now); d > 0 {
		return &RateLimitError{RetryAfter: d}
	}
	return nil
}

func (s *Service) CheckAdminNetwork(_ context.Context, ip netip.Addr) error {
	snap := s.snap.Load()
	if !snap.settings.AdminAllowlistOnly {
		return nil
	}
	ip = normalizeIP(ip)
	if ip.IsLoopback() || snap.allowed(ip, s.clock.Now()) {
		return nil
	}
	return ErrAdminNetwork
}
```

- [ ] **Step 4: Run** `go test ./internal/defense/ -run 'CheckLogin|CheckSecondFactor|CheckAdminNetwork'` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/defense/check.go internal/defense/check_test.go
git commit -m "feat(defense): add login, second-factor and admin-network checks" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: `RecordAttempt`, Jail-Eskalation und Benachrichtigung bei neuem Gerät

**Files:**
- Modify: `internal/store/queries/defense.sql` (anhängen), regeneriert `internal/store/defense.sql.go`
- Create: `internal/defense/jail.go`
- Test: `internal/defense/jail_test.go`

**Interfaces:**
- Consumes: `defense.Attempt`, `defense.AttemptStage` + `Stage*`-Konstanten, `defense.LoginGuard`, `httpx.IPKey`, `audit.Entry`, `audit.ActionJailAuto`, `audit.ActorSystem`, `audit.ResultSuccess`, `notify.Event`, `notify.EventJailed`, `notify.EventNewDeviceLogin`, `GeoResolver.Country`, Task 2 (`addJail`, `kickSync`, `snapshot.allowed`, `jailedUntil`).
- Produces:
```go
var _ LoginGuard = (*Service)(nil)
func (s *Service) RecordAttempt(ctx context.Context, a Attempt) error
func JailDuration(level int64) time.Duration          // 15m·2^(level-1), max 7d
const ReasonComplete = "complete"
func (s *Service) maybeJail(ctx context.Context, ip netip.Addr, key netip.Prefix, now time.Time) error
func truncateRunes(s string, n int) string
// sqlc
func (q *Queries) DefenseInsertAttempt(ctx context.Context, arg DefenseInsertAttemptParams) error
func (q *Queries) DefenseCountFullLoginsFrom(ctx context.Context, arg DefenseCountFullLoginsFromParams) (int64, error)
func (q *Queries) DefenseLastJailCreatedAt(ctx context.Context, ipKey string) (int64, error)
func (q *Queries) DefenseCountFailuresSince(ctx context.Context, arg DefenseCountFailuresSinceParams) (int64, error)
func (q *Queries) DefenseCountJailsSince(ctx context.Context, arg DefenseCountJailsSinceParams) (int64, error)
```
Semantik: Gespeichert werden `username` (max. 64 Runen), `user_agent` (max. 256), `reason` (max. 64). „Neues Gerät“ = Versuch mit `Success: true`, `Reason: "complete"` (Stream-A-Vertrag: vollständige Anmeldung; `second_factor_pending` = Passwort ok, 2FA ausstehend) und `UserID`, ohne frühere Zeile mit `success = 1 AND reason = 'complete'` für denselben `user_id` + `ip_key` (geprüft vor dem Einfügen). Für das Jail zählt jede Zeile mit `success = 0`, auch `reason = 'admin_network'`. Jail-Entscheidungen sind über `jailMu` serialisiert; Audit und Benachrichtigung erfolgen nach dem Insert.

- [ ] **Step 1: Queries an `internal/store/queries/defense.sql` anhängen**

```sql
-- name: DefenseInsertAttempt :exec
INSERT INTO login_attempts (ts, ip, ip_key, username, user_id, stage, success, reason, user_agent, country)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: DefenseCountFullLoginsFrom :one
SELECT count(*) FROM login_attempts
WHERE user_id = sqlc.arg(user_id) AND ip_key = sqlc.arg(ip_key) AND success = 1
  AND reason = 'complete';

-- name: DefenseLastJailCreatedAt :one
SELECT CAST(COALESCE(MAX(created_at), 0) AS INTEGER) AS last_created_at FROM jails WHERE ip_key = ?;

-- name: DefenseCountFailuresSince :one
SELECT count(*) FROM login_attempts
WHERE ip_key = sqlc.arg(ip_key) AND success = 0 AND ts >= sqlc.arg(since);

-- name: DefenseCountJailsSince :one
SELECT count(*) FROM jails
WHERE ip_key = sqlc.arg(ip_key) AND created_at >= sqlc.arg(since);
```

```bash
sqlc generate && go build ./internal/store/
```
Expected: kein Fehler; `DefenseInsertAttemptParams` hat die Felder `Ts, Ip, IpKey, Username, UserID, Stage, Success, Reason, UserAgent, Country`.

- [ ] **Step 2: Failing test `internal/defense/jail_test.go`**

```go
package defense

import (
	"context"
	"database/sql"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

func (e *env) fail(t *testing.T, ip string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		err := e.svc.RecordAttempt(context.Background(), Attempt{
			IP: addr(ip), Username: "admin", Stage: StagePassword, Reason: "bad_password", UserAgent: "test",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) activeJails(t *testing.T) []store.Jail {
	t.Helper()
	js, err := e.q.DefenseListActiveJails(context.Background(), e.clk.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func TestJailDuration(t *testing.T) {
	cases := map[int64]time.Duration{
		0: 15 * time.Minute, 1: 15 * time.Minute, 2: 30 * time.Minute, 3: time.Hour,
		10: 128 * time.Hour, 11: 168 * time.Hour, 50: 168 * time.Hour,
	}
	for lvl, want := range cases {
		if got := JailDuration(lvl); got != want {
			t.Errorf("JailDuration(%d) = %v, want %v", lvl, got, want)
		}
	}
}

func TestRecordAttemptStoresRow(t *testing.T) {
	e := newEnv(t)
	e.geo["203.0.113.7"] = "DE"
	uid := storetest.CreateUser(t, e.db, "alice", "user")
	err := e.svc.RecordAttempt(context.Background(), Attempt{
		IP: addr("::ffff:203.0.113.7"), Username: strings.Repeat("x", 100), UserID: &uid,
		Stage: StageTOTP, Success: false, Reason: "bad_code", UserAgent: "Firefox",
	})
	if err != nil {
		t.Fatal(err)
	}
	var ip, key, country, stage, username string
	var success int64
	var userID sql.NullInt64
	row := e.db.QueryRow("SELECT ip, ip_key, country, stage, success, user_id, username FROM login_attempts")
	if err := row.Scan(&ip, &key, &country, &stage, &success, &userID, &username); err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.7" || key != "203.0.113.7/32" || country != "DE" || stage != "totp" || success != 0 ||
		!userID.Valid || userID.Int64 != uid || len(username) != 64 {
		t.Fatalf("row = %q %q %q %q %d %v len(username)=%d", ip, key, country, stage, success, userID, len(username))
	}
}

func TestRecordAttemptRejectsInvalidIP(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.RecordAttempt(context.Background(), Attempt{Stage: StagePassword}); err == nil {
		t.Fatal("expected error for missing IP")
	}
}

func TestJailAfterFiveFailures(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 4)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("jailed after 4 failures: %+v", js)
	}
	e.fail(t, "203.0.113.7", 1)
	js := e.activeJails(t)
	if len(js) != 1 || js[0].IpKey != "203.0.113.7/32" || js[0].Level != 1 || js[0].Manual != 0 ||
		js[0].ExpiresAt != t0.Add(15*time.Minute).UnixMilli() {
		t.Fatalf("jails = %+v", js)
	}
	if _, ok := e.svc.snap.Load().jailedUntil(netip.MustParsePrefix("203.0.113.7/32"), t0); !ok {
		t.Fatal("jail must be visible in the snapshot without reload")
	}
	if len(e.audit.Entries) != 1 {
		t.Fatalf("audit entries = %+v", e.audit.Entries)
	}
	a := e.audit.Entries[0]
	if a.Action != audit.ActionJailAuto || a.ActorType != audit.ActorSystem || a.ActorName != "system" ||
		a.Target != "203.0.113.7/32" || a.Result != audit.ResultSuccess {
		t.Fatalf("audit entry = %+v", a)
	}
	if len(e.notes.Events) != 1 || e.notes.Events[0].Type != notify.EventJailed || e.notes.Events[0].IP != "203.0.113.7/32" {
		t.Fatalf("events = %+v", e.notes.Events)
	}
}

func TestJailIPv6SameSlash64SharesCounter(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "2001:db8:1:2::1", 3)
	e.fail(t, "2001:db8:1:2:ffff::9", 2)
	e.fail(t, "2001:db8:1:3::1", 4)
	js := e.activeJails(t)
	if len(js) != 1 || js[0].IpKey != "2001:db8:1:2::/64" {
		t.Fatalf("jails = %+v", js)
	}
}

func TestJailMappedIPv4SharesCounter(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 3)
	e.fail(t, "::ffff:203.0.113.7", 2)
	js := e.activeJails(t)
	if len(js) != 1 || js[0].IpKey != "203.0.113.7/32" {
		t.Fatalf("jails = %+v", js)
	}
}

func TestJailWindowIsTenMinutes(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 4)
	e.clk.Advance(10*time.Minute + time.Millisecond)
	e.fail(t, "203.0.113.7", 1)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("failures outside the window must not count: %+v", js)
	}
}

func TestJailEscalation(t *testing.T) {
	e := newEnv(t)
	wantLevel := func(level int64, d time.Duration) {
		t.Helper()
		e.fail(t, "203.0.113.7", 5)
		js := e.activeJails(t)
		if len(js) != 1 || js[0].Level != level || js[0].ExpiresAt != e.clk.Now().Add(d).UnixMilli() {
			t.Fatalf("want level %d / %v, got %+v", level, d, js)
		}
		e.clk.Advance(d)
	}
	wantLevel(1, 15*time.Minute)
	wantLevel(2, 30*time.Minute)
	wantLevel(3, time.Hour)
	e.clk.Advance(31 * 24 * time.Hour)
	wantLevel(1, 15*time.Minute)
}

func TestFailuresBeforeLiftDoNotRejail(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 5)
	if _, err := e.db.Exec("UPDATE jails SET lifted_at = ?, lifted_by = 'admin'", t0.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	e.reload(t)
	e.clk.Advance(time.Second)
	e.fail(t, "203.0.113.7", 1)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("failures before the last jail must not count again: %+v", js)
	}
	e.fail(t, "203.0.113.7", 4)
	js := e.activeJails(t)
	if len(js) != 1 || js[0].Level != 2 {
		t.Fatalf("expected level-2 jail, got %+v", js)
	}
}

func TestAllowlistedAndLoopbackNeverJailed(t *testing.T) {
	e := newEnv(t)
	e.addRule(t, "203.0.113.0/24", "allow", nil)
	e.fail(t, "203.0.113.7", 10)
	e.fail(t, "127.0.0.1", 10)
	e.fail(t, "::1", 10)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("jails = %+v", js)
	}
	var n int
	_ = e.db.QueryRow("SELECT count(*) FROM login_attempts").Scan(&n)
	if n != 30 {
		t.Fatalf("attempts recorded = %d, want 30", n)
	}
}

func TestSuccessNotifiesNewDeviceOnce(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "alice", "user")
	ok := func(ip string, stage AttemptStage, reason string) {
		t.Helper()
		if err := e.svc.RecordAttempt(context.Background(), Attempt{
			IP: addr(ip), Username: "alice", UserID: &uid, Stage: stage, Success: true, Reason: reason,
		}); err != nil {
			t.Fatal(err)
		}
	}
	ok("203.0.113.7", StagePassword, "second_factor_pending")
	ok("203.0.113.7", StageSudo, "sudo")
	if len(e.notes.Events) != 0 {
		t.Fatalf("pending second factor and sudo are not full logins: %+v", e.notes.Events)
	}
	ok("203.0.113.7", StageTOTP, "complete")
	ok("203.0.113.7", StageWebAuthn, "complete")
	ok("198.51.100.1", StageRecovery, "complete")
	ok("198.51.100.1", StagePassword, "complete") // user without 2FA: full login at password stage
	types := e.notes.Types()
	if len(types) != 2 || types[0] != notify.EventNewDeviceLogin || types[1] != notify.EventNewDeviceLogin {
		t.Fatalf("events = %+v", e.notes.Events)
	}
	if e.notes.Events[0].Username != "alice" {
		t.Fatalf("event = %+v", e.notes.Events[0])
	}
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("successes must never jail: %+v", js)
	}
}

func TestAdminNetworkDenialsCountTowardJail(t *testing.T) {
	e := newEnv(t)
	uid := storetest.CreateUser(t, e.db, "root", "admin")
	for i := 0; i < 5; i++ {
		if err := e.svc.RecordAttempt(context.Background(), Attempt{
			IP: addr("203.0.113.7"), Username: "root", UserID: &uid, Stage: StagePassword,
			Success: false, Reason: "admin_network",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if js := e.activeJails(t); len(js) != 1 {
		t.Fatalf("admin_network denials must count toward the jail: %+v", js)
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/defense/ -run 'Jail|RecordAttempt|NewDevice|Allowlisted|Failures|AdminNetworkDenials'` → FAIL (`e.svc.RecordAttempt undefined`).

- [ ] **Step 4: Implementation `internal/defense/jail.go`**

```go
package defense

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/store"
)

var _ LoginGuard = (*Service)(nil)

const (
	maxUsernameLen  = 64
	maxUserAgentLen = 256
	maxReasonLen    = 64
)

// ReasonComplete marks a fully successful login (Stream A contract).
const ReasonComplete = "complete"

// JailDuration returns the jail duration for an escalation level (spec §7.2).
func JailDuration(level int64) time.Duration {
	if level < 1 {
		level = 1
	}
	if level > 10 {
		return JailMax
	}
	d := JailBase << uint(level-1)
	if d > JailMax {
		return JailMax
	}
	return d
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func countryOrUnknown(c string) string {
	if c == "" {
		return "unknown country"
	}
	return c
}

// RecordAttempt stores the attempt, notifies about logins from new devices and
// jails the IP key when the failure threshold is reached.
func (s *Service) RecordAttempt(ctx context.Context, a Attempt) error {
	now := s.clock.Now()
	ip := normalizeIP(a.IP)
	key := httpx.IPKey(ip)
	if !key.IsValid() {
		return fmt.Errorf("defense: record attempt: invalid ip %v", a.IP)
	}
	var uid sql.NullInt64
	if a.UserID != nil {
		uid = sql.NullInt64{Int64: *a.UserID, Valid: true}
	}
	newDevice := false
	if a.Success && a.Reason == ReasonComplete && uid.Valid {
		n, err := s.q.DefenseCountFullLoginsFrom(ctx, store.DefenseCountFullLoginsFromParams{UserID: uid, IpKey: key.String()})
		if err != nil {
			return fmt.Errorf("defense: check new device: %w", err)
		}
		newDevice = n == 0
	}
	country := s.geo.Country(ip)
	err := s.q.DefenseInsertAttempt(ctx, store.DefenseInsertAttemptParams{
		Ts:        now.UnixMilli(),
		Ip:        ip.String(),
		IpKey:     key.String(),
		Username:  truncateRunes(a.Username, maxUsernameLen),
		UserID:    uid,
		Stage:     string(a.Stage),
		Success:   boolInt(a.Success),
		Reason:    truncateRunes(a.Reason, maxReasonLen),
		UserAgent: truncateRunes(a.UserAgent, maxUserAgentLen),
		Country:   country,
	})
	if err != nil {
		return fmt.Errorf("defense: record attempt: %w", err)
	}
	if a.Success {
		if newDevice {
			s.notifier.Notify(ctx, notify.Event{
				Type:     notify.EventNewDeviceLogin,
				Title:    "Login from a new device",
				Message:  fmt.Sprintf("%s signed in from %s (%s).", a.Username, ip, countryOrUnknown(country)),
				IP:       key.String(),
				Username: a.Username,
				Time:     now,
			})
		}
		return nil
	}
	return s.maybeJail(ctx, ip, key, now)
}

// maybeJail jails key if it reached the failure threshold. Allowlisted and
// loopback addresses are never jailed.
func (s *Service) maybeJail(ctx context.Context, ip netip.Addr, key netip.Prefix, now time.Time) error {
	if ip.IsLoopback() || s.snap.Load().allowed(ip, now) {
		return nil
	}
	s.jailMu.Lock()
	defer s.jailMu.Unlock()
	if _, ok := s.snap.Load().jailedUntil(key, now); ok {
		return nil
	}
	last, err := s.q.DefenseLastJailCreatedAt(ctx, key.String())
	if err != nil {
		return fmt.Errorf("defense: last jail: %w", err)
	}
	since := now.Add(-FailureWindow).UnixMilli()
	if last >= since {
		since = last + 1
	}
	failures, err := s.q.DefenseCountFailuresSince(ctx, store.DefenseCountFailuresSinceParams{IpKey: key.String(), Since: since})
	if err != nil {
		return fmt.Errorf("defense: count failures: %w", err)
	}
	if failures < FailureThreshold {
		return nil
	}
	prior, err := s.q.DefenseCountJailsSince(ctx, store.DefenseCountJailsSinceParams{
		IpKey: key.String(), Since: now.Add(-RepeatWindow).UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("defense: count jails: %w", err)
	}
	level := prior + 1
	expires := now.Add(JailDuration(level))
	j, err := s.q.DefenseInsertJail(ctx, store.DefenseInsertJailParams{
		IpKey:     key.String(),
		Reason:    fmt.Sprintf("%d failed login attempts within %s", failures, FailureWindow),
		Level:     level,
		Manual:    0,
		CreatedAt: now.UnixMilli(),
		ExpiresAt: expires.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("defense: insert jail: %w", err)
	}
	s.addJail(key, expires)
	s.kickSync()
	if err := s.audit.Record(ctx, audit.Entry{
		Action:    audit.ActionJailAuto,
		Target:    key.String(),
		Result:    audit.ResultSuccess,
		ActorType: audit.ActorSystem,
		ActorName: "system",
		IP:        ip.String(),
		Details:   map[string]any{"jail_id": j.ID, "level": level, "failures": failures, "expires_at": expires.UnixMilli()},
	}); err != nil {
		s.log.Error("defense: audit jail", "err", err)
	}
	s.notifier.Notify(ctx, notify.Event{
		Type:    notify.EventJailed,
		Title:   "IP jailed",
		Message: fmt.Sprintf("%s was jailed for %s after %d failed login attempts (level %d).", key, JailDuration(level), failures, level),
		IP:      key.String(),
		Time:    now,
	})
	return nil
}
```

- [ ] **Step 5: Run** `go test ./internal/defense/` → PASS (alle bisherigen Tests).

- [ ] **Step 6: Commit**

```bash
git add internal/store/queries/defense.sql internal/store/defense.sql.go internal/defense/jail.go internal/defense/jail_test.go
git commit -m "feat(defense): record login attempts, escalate jails and notify on new devices" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: IP-Filter- und API-Rate-Limit-Middleware

**Files:**
- Create: `internal/defense/middleware.go`
- Test: `internal/defense/middleware_test.go`

**Interfaces:**
- Consumes: `httpx.ClientIP(ctx) netip.Addr`, `httpx.WithClientIP`, `httpx.IPKey`, `httpx.WriteError`, `httpx.CodeJailed`, `httpx.CodeIPBlocked`, `httpx.CodeRateLimited`, `httpx.ErrorBody`, `principal.From(ctx) (Principal, bool)`, `principal.With`, `principal.Principal.SessionID`, Task 1 (`RetryAfterSeconds`, `keyedLimiter`), Task 2 (`snapshot`, `isLocal`).
- Produces:
```go
func (s *Service) IPFilter(next http.Handler) http.Handler       // Phase 2: global, after httpx.BodyLimit, before session loader
func (s *Service) APIRateLimit(next http.Handler) http.Handler   // Phase 2: global, after session loader + CSRF
func (s *snapshot) blockReason(ip netip.Addr, now time.Time, geo GeoResolver) string   // "" | "deny_rule" | "country:XX"
```

- [ ] **Step 1: Failing test `internal/defense/middleware_test.go`**

```go
package defense

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

func errCode(rec *httptest.ResponseRecorder) string {
	var b httpx.ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	return b.Error
}

func (e *env) filter(ip, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if ip != "" {
		req = req.WithContext(httpx.WithClientIP(req.Context(), addr(ip)))
	}
	rec := httptest.NewRecorder()
	e.svc.IPFilter(okHandler()).ServeHTTP(rec, req)
	return rec
}

func TestIPFilterPassesByDefault(t *testing.T) {
	e := newEnv(t)
	if rec := e.filter("203.0.113.7", "/api/health"); rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d", rec.Code)
	}
	if rec := e.filter("", "/api/health"); rec.Code != http.StatusNoContent {
		t.Fatalf("request without client IP: code = %d", rec.Code)
	}
}

func TestIPFilterJailed(t *testing.T) {
	e := newEnv(t)
	e.svc.addJail(netip.MustParsePrefix("2001:db8:1:2::/64"), t0.Add(15*time.Minute))
	for _, path := range []string{"/api/auth/login", "/assets/app.js", "/"} {
		rec := e.filter("2001:db8:1:2::abcd", path)
		if rec.Code != http.StatusForbidden || errCode(rec) != httpx.CodeJailed || rec.Header().Get("Retry-After") != "900" {
			t.Fatalf("%s: code=%d err=%q retry=%q", path, rec.Code, errCode(rec), rec.Header().Get("Retry-After"))
		}
	}
	e.clk.Advance(15 * time.Minute)
	if rec := e.filter("2001:db8:1:2::abcd", "/"); rec.Code != http.StatusNoContent {
		t.Fatalf("after expiry: code = %d", rec.Code)
	}
}

func TestJailBlocksImmediatelyWithoutReload(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 5)
	rec := e.filter("203.0.113.7", "/api/auth/login")
	if rec.Code != http.StatusForbidden || errCode(rec) != httpx.CodeJailed {
		t.Fatalf("code=%d err=%q", rec.Code, errCode(rec))
	}
}

func TestIPFilterDenyAndAllow(t *testing.T) {
	e := newEnv(t)
	e.addRule(t, "198.51.100.0/24", "deny", nil)
	if rec := e.filter("198.51.100.8", "/"); rec.Code != http.StatusForbidden || errCode(rec) != httpx.CodeIPBlocked {
		t.Fatalf("deny: code=%d err=%q", rec.Code, errCode(rec))
	}
	e.addRule(t, "198.51.100.7/32", "allow", nil)
	if rec := e.filter("198.51.100.7", "/"); rec.Code != http.StatusNoContent {
		t.Fatalf("allow must win over deny: code=%d", rec.Code)
	}
	if rec := e.filter("198.51.100.8", "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("other address still denied: code=%d", rec.Code)
	}
}

func TestIPFilterCountry(t *testing.T) {
	e := newEnv(t)
	e.geo["203.0.113.7"] = "US"
	e.geo["203.0.113.8"] = "DE"
	e.geo["10.0.0.1"] = "US"
	e.saveSettings(t, store.SecuritySettings{AllowedCountries: []string{"DE", "AT"}})
	cases := map[string]int{
		"203.0.113.7": http.StatusForbidden,
		"203.0.113.8": http.StatusNoContent,
		"203.0.113.9": http.StatusNoContent, // unknown country passes
		"10.0.0.1":    http.StatusNoContent, // private addresses are never country-filtered
	}
	for ip, want := range cases {
		rec := e.filter(ip, "/")
		if rec.Code != want {
			t.Errorf("%s: code = %d, want %d", ip, rec.Code, want)
		}
		if want == http.StatusForbidden && errCode(rec) != httpx.CodeIPBlocked {
			t.Errorf("%s: err = %q", ip, errCode(rec))
		}
	}
	e.addRule(t, "203.0.113.7/32", "allow", nil)
	if rec := e.filter("203.0.113.7", "/"); rec.Code != http.StatusNoContent {
		t.Fatalf("allowlisted IP must bypass the country filter: code=%d", rec.Code)
	}
}

func (e *env) apiReq(path string, sessionID int64, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx := httpx.WithClientIP(req.Context(), addr(ip))
	if sessionID != 0 {
		ctx = principal.With(ctx, principal.Principal{UserID: 1, SessionID: sessionID, Role: principal.RoleUser, Stage: principal.StageFull})
	}
	rec := httptest.NewRecorder()
	e.svc.APIRateLimit(okHandler()).ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestAPIRateLimit(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 60; i++ {
		if rec := e.apiReq("/api/servers", 7, "203.0.113.7"); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: code = %d", i, rec.Code)
		}
	}
	rec := e.apiReq("/api/servers", 7, "203.0.113.7")
	if rec.Code != http.StatusTooManyRequests || errCode(rec) != httpx.CodeRateLimited || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("code=%d err=%q retry=%q", rec.Code, errCode(rec), rec.Header().Get("Retry-After"))
	}
	if rec := e.apiReq("/api/servers", 8, "203.0.113.7"); rec.Code != http.StatusNoContent {
		t.Fatalf("other session must have its own bucket: code=%d", rec.Code)
	}
	for i := 0; i < 100; i++ {
		if rec := e.apiReq("/assets/app.js", 7, "203.0.113.7"); rec.Code != http.StatusNoContent {
			t.Fatalf("non-API paths must not be limited: code=%d", rec.Code)
		}
	}
	e.clk.Advance(200*time.Millisecond + time.Millisecond)
	if rec := e.apiReq("/api/servers", 7, "203.0.113.7"); rec.Code != http.StatusNoContent {
		t.Fatalf("after refill: code=%d", rec.Code)
	}
}

func TestAPIRateLimitAnonymousByIPKey(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 60; i++ {
		if rec := e.apiReq("/api/health", 0, "2001:db8:1:2::1"); rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: code = %d", i, rec.Code)
		}
	}
	if rec := e.apiReq("/api/health", 0, "2001:db8:1:2::2"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("same /64 must share the anonymous bucket: code=%d", rec.Code)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/defense/ -run 'IPFilter|JailBlocks|APIRateLimit'` → FAIL (`e.svc.IPFilter undefined`).

- [ ] **Step 3: Implementation `internal/defense/middleware.go`**

```go
package defense

import (
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
)

// blockReason returns why ip is blocked by deny rules or the country filter
// ("" if it is not). Allowlisted IPs are never blocked.
func (s *snapshot) blockReason(ip netip.Addr, now time.Time, geo GeoResolver) string {
	ip = normalizeIP(ip)
	if s.allowed(ip, now) {
		return ""
	}
	if s.denied(ip, now) {
		return "deny_rule"
	}
	if len(s.settings.AllowedCountries) > 0 && !isLocal(ip) {
		if c := geo.Country(ip); c != "" && !slices.Contains(s.settings.AllowedCountries, c) {
			return "country:" + c
		}
	}
	return ""
}

// IPFilter rejects jailed and blocked clients for every request (API and static
// files). It only reads the in-memory snapshot.
func (s *Service) IPFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := normalizeIP(httpx.ClientIP(r.Context()))
		if !ip.IsValid() {
			next.ServeHTTP(w, r)
			return
		}
		now := s.clock.Now()
		snap := s.snap.Load()
		if snap.allowed(ip, now) {
			next.ServeHTTP(w, r)
			return
		}
		if until, ok := snap.jailedUntil(httpx.IPKey(ip), now); ok {
			w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds(until.Sub(now))))
			httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeJailed,
				"too many failed login attempts; access is temporarily blocked")
			return
		}
		if reason := snap.blockReason(ip, now, s.geo); reason != "" {
			httpx.WriteError(w, r, http.StatusForbidden, httpx.CodeIPBlocked, "access from your network is not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// APIRateLimit limits /api/ requests per session (or per IP key for anonymous requests).
func (s *Service) APIRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		key := "ip:" + httpx.IPKey(normalizeIP(httpx.ClientIP(r.Context()))).String()
		if p, ok := principal.From(r.Context()); ok && p.SessionID != 0 {
			key = "session:" + strconv.FormatInt(p.SessionID, 10)
		}
		if d := s.api.take(key, s.clock.Now()); d > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds(d)))
			httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run** `go test ./internal/defense/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/defense/middleware.go internal/defense/middleware_test.go
git commit -m "feat(defense): add IP filter and API rate-limit middleware" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: GeoIP-Resolver (DB-IP Lite, MMDB)

**Files:**
- Create: `internal/defense/geoip.go`
- Test: `internal/defense/geoip_test.go`

**Interfaces:**
- Consumes: `defense.GeoResolver`, `defense.NopGeo`, `maxminddb.Open(path string, ...maxminddb.ReaderOption) (*maxminddb.Reader, error)`, `(*maxminddb.Reader).Lookup(netip.Addr) maxminddb.Result`, `maxminddb.Result.Decode(any) error`, `(*maxminddb.Reader).Close() error` (Modul `github.com/oschwald/maxminddb-golang/v2`, Import-Name `maxminddb`), Task 2 (`normalizeIP`, `isLocal`).
- Produces:
```go
type GeoIP struct{ /* unexported */ }                       // implements GeoResolver
func OpenGeoIP(path string) (*GeoIP, error)
func (g *GeoIP) Country(ip netip.Addr) string               // "" for unknown, local, multicast, IPv6 in IPv4-only DB
func (g *GeoIP) Close() error
func LoadGeo(path string, log *slog.Logger) (GeoResolver, bool, func() error)   // missing/invalid → NopGeo{}, false
```
Datenformat: DB-IP „IP to Country Lite“ im MMDB-Format nutzt dieselbe Struktur wie GeoLite2-Country (`{"country": {"iso_code": "DE", …}}`).

- [ ] **Step 1: Failing test `internal/defense/geoip_test.go`** (enthält einen minimalen MMDB-Writer nur für Tests: IPv4-only, `record_size` 24, Format laut https://maxmind.github.io/MaxMind-DB/)

```go
package defense

import (
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// --- minimal MaxMind DB writer (test only) ---

func mmdbString(s string) []byte {
	if len(s) >= 29 {
		panic("mmdbString: string too long for the short encoding")
	}
	return append([]byte{0x40 | byte(len(s))}, s...) // type 2 (UTF-8 string)
}
func mmdbMap(n int) []byte       { return []byte{0xE0 | byte(n)} }                 // type 7 (map)
func mmdbUint16(v uint16) []byte { return []byte{0xA2, byte(v >> 8), byte(v)} }    // type 5, 2 bytes
func mmdbArray(n int) []byte     { return []byte{byte(n), 0x04} }                  // extended type 11 = 7+4
func mmdbUint32(v uint32) []byte {                                                  // type 6, 4 bytes
	return []byte{0xC4, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

func countryRecord(code string) []byte {
	var b []byte
	b = append(b, mmdbMap(1)...)
	b = append(b, mmdbString("country")...)
	b = append(b, mmdbMap(1)...)
	b = append(b, mmdbString("iso_code")...)
	b = append(b, mmdbString(code)...)
	return b
}

// writeTestMMDB writes an IPv4-only database mapping each (non-overlapping) prefix
// to {"country":{"iso_code":code}}.
func writeTestMMDB(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	type node struct{ rec [2]int } // >= 0: child node, -1: empty, <= -2: data index -(rec+2)
	nodes := []node{{rec: [2]int{-1, -1}}}
	var data []byte
	var offsets []int
	for cidr, code := range entries {
		p := netip.MustParsePrefix(cidr)
		if !p.Addr().Is4() || p.Bits() == 0 {
			t.Fatalf("writeTestMMDB supports IPv4 prefixes with bits > 0 only: %s", cidr)
		}
		offsets = append(offsets, len(data))
		data = append(data, countryRecord(code)...)
		ref := -(len(offsets) - 1 + 2)
		a := p.Addr().As4()
		cur := 0
		for i := 0; i < p.Bits(); i++ {
			bit := int(a[i/8]>>(7-uint(i%8))) & 1
			if i == p.Bits()-1 {
				if nodes[cur].rec[bit] != -1 {
					t.Fatalf("overlapping prefix %s", cidr)
				}
				nodes[cur].rec[bit] = ref
				break
			}
			next := nodes[cur].rec[bit]
			if next <= -2 {
				t.Fatalf("overlapping prefix %s", cidr)
			}
			if next == -1 {
				nodes = append(nodes, node{rec: [2]int{-1, -1}})
				next = len(nodes) - 1
				nodes[cur].rec[bit] = next
			}
			cur = next
		}
	}
	n := len(nodes)
	var buf []byte
	for _, nd := range nodes {
		for _, r := range nd.rec {
			v := n // "no data"
			if r >= 0 {
				v = r
			} else if r <= -2 {
				v = n + 16 + offsets[-(r+2)]
			}
			buf = append(buf, byte(v>>16), byte(v>>8), byte(v))
		}
	}
	buf = append(buf, make([]byte, 16)...) // data section separator
	buf = append(buf, data...)
	buf = append(buf, "\xAB\xCD\xEFMaxMind.com"...)
	buf = append(buf, mmdbMap(9)...)
	kv := func(k string, v []byte) {
		buf = append(buf, mmdbString(k)...)
		buf = append(buf, v...)
	}
	desc := append(mmdbMap(1), mmdbString("en")...)
	desc = append(desc, mmdbString("test")...)
	kv("binary_format_major_version", mmdbUint16(2))
	kv("binary_format_minor_version", mmdbUint16(0))
	kv("build_epoch", mmdbUint32(1759924800))
	kv("database_type", mmdbString("Test-Country"))
	kv("description", desc)
	kv("ip_version", mmdbUint16(4))
	kv("languages", append(mmdbArray(1), mmdbString("en")...))
	kv("node_count", mmdbUint32(uint32(n)))
	kv("record_size", mmdbUint16(24))
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGeoIPWithMMDBFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.mmdb")
	writeTestMMDB(t, path, map[string]string{"203.0.113.0/24": "DE", "198.51.100.0/24": "us"})
	g, err := OpenGeoIP(path)
	if err != nil {
		t.Fatalf("OpenGeoIP: %v", err)
	}
	defer g.Close()
	cases := map[string]string{
		"203.0.113.7":        "DE",
		"::ffff:203.0.113.7": "DE",
		"198.51.100.1":       "US", // normalized to upper case
		"192.0.2.1":          "",   // not in database
		"2001:db8::1":        "",   // IPv6 lookup in IPv4-only database fails → ""
		"10.0.0.1":           "",   // private: never looked up
	}
	for ip, want := range cases {
		if got := g.Country(addr(ip)); got != want {
			t.Errorf("Country(%s) = %q, want %q", ip, got, want)
		}
	}
}

type fakeLookup struct {
	calls int
	code  string
	err   error
}

func (f *fakeLookup) lookupCountry(netip.Addr) (string, error) { f.calls++; return f.code, f.err }
func (f *fakeLookup) Close() error                             { return nil }

func TestGeoIPSeam(t *testing.T) {
	f := &fakeLookup{code: "de"}
	g := &GeoIP{l: f}
	for _, ip := range []string{"127.0.0.1", "::1", "192.168.1.1", "fe80::1", "224.0.0.1"} {
		if got := g.Country(addr(ip)); got != "" {
			t.Errorf("Country(%s) = %q, want empty", ip, got)
		}
	}
	if g.Country(netip.Addr{}) != "" || f.calls != 0 {
		t.Fatalf("local/invalid addresses must not be looked up (calls=%d)", f.calls)
	}
	if got := g.Country(addr("203.0.113.7")); got != "DE" {
		t.Fatalf("Country = %q, want DE", got)
	}
	f.err = errors.New("boom")
	if got := g.Country(addr("203.0.113.7")); got != "" {
		t.Fatalf("lookup error must yield empty country, got %q", got)
	}
	f.err, f.code = nil, "Germany"
	if got := g.Country(addr("203.0.113.7")); got != "" {
		t.Fatalf("invalid code must yield empty country, got %q", got)
	}
}

func TestLoadGeo(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	dir := t.TempDir()
	for name, path := range map[string]string{"empty path": "", "missing": filepath.Join(dir, "missing.mmdb")} {
		r, ok, closeFn := LoadGeo(path, log)
		if ok || r.Country(addr("203.0.113.7")) != "" || closeFn() != nil {
			t.Errorf("%s: ok=%v", name, ok)
		}
		if _, isNop := r.(NopGeo); !isNop {
			t.Errorf("%s: resolver = %T, want NopGeo", name, r)
		}
	}
	garbage := filepath.Join(dir, "garbage.mmdb")
	if err := os.WriteFile(garbage, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadGeo(garbage, log); ok {
		t.Error("garbage file must not be available")
	}
	good := filepath.Join(dir, "good.mmdb")
	writeTestMMDB(t, good, map[string]string{"203.0.113.0/24": "DE"})
	r, ok, closeFn := LoadGeo(good, log)
	if !ok || r.Country(addr("203.0.113.7")) != "DE" {
		t.Fatalf("valid database: ok=%v", ok)
	}
	if err := closeFn(); err != nil {
		t.Fatal(err)
	}
}

// TestGeoIPRealDatabase is a manual check against the real DB-IP Lite file;
// it only runs when MCPANEL_GEOIP_TEST_DB points to an .mmdb file.
func TestGeoIPRealDatabase(t *testing.T) {
	path := os.Getenv("MCPANEL_GEOIP_TEST_DB")
	if path == "" {
		t.Skip("set MCPANEL_GEOIP_TEST_DB to a dbip-country-lite .mmdb file")
	}
	g, err := OpenGeoIP(path)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	code := regexp.MustCompile(`^[A-Z]{2}$`)
	for _, ip := range []string{"8.8.8.8", "2001:4860:4860::8888", "::ffff:8.8.8.8"} {
		if got := g.Country(addr(ip)); !code.MatchString(got) {
			t.Errorf("Country(%s) = %q, want ISO code", ip, got)
		}
	}
	if got := g.Country(addr("10.0.0.1")); got != "" {
		t.Errorf("private address: %q", got)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/defense/ -run 'GeoIP|LoadGeo'` → FAIL (`undefined: OpenGeoIP`).

- [ ] **Step 3: Implementation `internal/defense/geoip.go`**

```go
package defense

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
)

var countryCodeRE = regexp.MustCompile(`^[A-Z]{2}$`)

// countryLookup is the seam between GeoIP and the MMDB reader.
type countryLookup interface {
	lookupCountry(ip netip.Addr) (string, error)
	Close() error
}

type mmdbLookup struct{ r *maxminddb.Reader }

func (m mmdbLookup) lookupCountry(ip netip.Addr) (string, error) {
	var rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	if err := m.r.Lookup(ip).Decode(&rec); err != nil {
		return "", err
	}
	return rec.Country.ISOCode, nil
}

func (m mmdbLookup) Close() error { return m.r.Close() }

// GeoIP resolves countries from a local DB-IP Lite (or GeoLite2) country database.
type GeoIP struct{ l countryLookup }

func OpenGeoIP(path string) (*GeoIP, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return nil, fmt.Errorf("geoip: open %s: %w", path, err)
	}
	return &GeoIP{l: mmdbLookup{r: r}}, nil
}

func (g *GeoIP) Country(ip netip.Addr) string {
	ip = normalizeIP(ip)
	if !ip.IsValid() || isLocal(ip) || ip.IsMulticast() {
		return ""
	}
	c, err := g.l.lookupCountry(ip)
	if err != nil {
		return ""
	}
	c = strings.ToUpper(c)
	if !countryCodeRE.MatchString(c) {
		return ""
	}
	return c
}

func (g *GeoIP) Close() error { return g.l.Close() }

// LoadGeo opens the database at path. A missing or unreadable database disables
// country features: it returns NopGeo{}, available=false and a no-op closer.
func LoadGeo(path string, log *slog.Logger) (GeoResolver, bool, func() error) {
	nop := func() error { return nil }
	if path == "" {
		return NopGeo{}, false, nop
	}
	g, err := OpenGeoIP(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Info("geoip database not found; country features disabled", "path", path)
		} else {
			log.Warn("geoip database unusable; country features disabled", "path", path, "err", err)
		}
		return NopGeo{}, false, nop
	}
	return g, true, g.Close
}
```

- [ ] **Step 4: Run** `go test ./internal/defense/ -run 'GeoIP|LoadGeo' -v` → PASS (`TestGeoIPRealDatabase` SKIP). Falls `TestGeoIPWithMMDBFixture` mit einem Format-Fehler scheitert: den Test-Writer gegen den Reader-Quelltext abgleichen (`ls $(go env GOMODCACHE)/github.com/oschwald/maxminddb-golang/v2@*/` → `reader.go` `searchTreeSizeBytes`/`resolveDataPointer`, `internal/decoder`), nicht den Produktionscode anpassen.

- [ ] **Step 5: Manuelle Prüfung mit der echten Datenbank** – **Vor dem Download den Nutzer um Erlaubnis fragen** (Datei ca. 5–8 MB von `download.db-ip.com`, Lizenz CC BY 4.0, Attribution „IP Geolocation by DB-IP“ im UI ist Aufgabe von Stream E/Phase 2):

```bash
mkdir -p .dev/geoip-download
curl -fsSL "https://download.db-ip.com/free/dbip-country-lite-$(date +%Y-%m).mmdb.gz" -o .dev/geoip-download/dbip.mmdb.gz
gunzip -c .dev/geoip-download/dbip.mmdb.gz > .dev/dbip-country-lite.mmdb
MCPANEL_GEOIP_TEST_DB="$PWD/.dev/dbip-country-lite.mmdb" go test ./internal/defense/ -run TestGeoIPRealDatabase -v
```
Expected: `--- PASS: TestGeoIPRealDatabase`. Zu Monatsbeginn kann die neue Datei noch fehlen (404) → Vormonat (`date -v-1m +%Y-%m`) verwenden. `.dev/` ist in `.gitignore`; nichts committen.

- [ ] **Step 6: Commit**

```bash
git add internal/defense/geoip.go internal/defense/geoip_test.go
git commit -m "feat(defense): add MMDB-based GeoIP resolver" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Firewall-Sync, Abgleich, Aufbewahrung und `Run`

**Files:**
- Modify: `internal/store/queries/defense.sql` (anhängen), regeneriert `internal/store/defense.sql.go`
- Create: `internal/defense/sync.go`
- Test: `internal/defense/sync_test.go`

**Interfaces:**
- Consumes: `agentapi.Client` (`Health`, `Ban(ctx, netip.Prefix, time.Duration)`, `Unban(ctx, netip.Prefix)`, `Bans(ctx) ([]agentapi.Ban, error)`), `agentapi.Ban{Prefix, ExpiresInSeconds}`, `agentapi.ErrUnavailable`, `agentapi.ValidateBanPrefix(netip.Prefix) error`, `agentapi.HealthResponse`, Task 2 (`kick`, `reloadOrLog`, `unbannable`, `syncMu`), Task 1 (`keyedLimiter.cleanup`).
- Produces:
```go
func (s *Service) SyncOnce(ctx context.Context) error          // returns error wrapping agentapi.ErrUnavailable when the agent is down
func (s *Service) PruneOnce(ctx context.Context) (int64, error)
func (s *Service) Run(ctx context.Context)                     // blocks until ctx is done; Phase 2: go svc.Run(ctx)
func (s *Service) cleanupLimiters(now time.Time)
// sqlc
func (q *Queries) DefenseJailsToBan(ctx context.Context, now int64) ([]Jail, error)
func (q *Queries) DefenseJailsToUnban(ctx context.Context) ([]Jail, error)
func (q *Queries) DefenseActiveBannedJails(ctx context.Context, now int64) ([]Jail, error)
func (q *Queries) DefenseMarkJailBanned(ctx context.Context, arg DefenseMarkJailBannedParams) error
func (q *Queries) DefenseMarkJailUnbanned(ctx context.Context, arg DefenseMarkJailUnbannedParams) error
func (q *Queries) DefenseCountPendingSync(ctx context.Context, now int64) (int64, error)
func (q *Queries) DefenseDeleteAttemptsBefore(ctx context.Context, before int64) (int64, error)
```
Fehlerklassen: `errors.Is(err, agentapi.ErrUnavailable)` oder `context.DeadlineExceeded` → vorübergehend (Zyklus abbrechen, später wiederholen). Alle anderen Agent-Fehler → permanent (Ban: Jail-ID in `unbannable`, nie wieder versucht, solange der Prozess läuft; Unban: als erledigt markieren).

- [ ] **Step 1: Queries an `internal/store/queries/defense.sql` anhängen**

```sql
-- name: DefenseJailsToBan :many
SELECT * FROM jails
WHERE fw_banned_at IS NULL AND lifted_at IS NULL AND expires_at > sqlc.arg(now)
ORDER BY id;

-- name: DefenseJailsToUnban :many
SELECT * FROM jails
WHERE lifted_at IS NOT NULL AND fw_banned_at IS NOT NULL AND fw_unbanned_at IS NULL
ORDER BY id;

-- name: DefenseActiveBannedJails :many
SELECT * FROM jails
WHERE fw_banned_at IS NOT NULL AND lifted_at IS NULL AND expires_at > sqlc.arg(now)
ORDER BY id;

-- name: DefenseMarkJailBanned :exec
UPDATE jails SET fw_banned_at = sqlc.arg(fw_banned_at) WHERE id = sqlc.arg(id);

-- name: DefenseMarkJailUnbanned :exec
UPDATE jails SET fw_unbanned_at = sqlc.arg(fw_unbanned_at) WHERE id = sqlc.arg(id);

-- name: DefenseCountPendingSync :one
SELECT count(*) FROM jails
WHERE (fw_banned_at IS NULL AND lifted_at IS NULL AND expires_at > sqlc.arg(now))
   OR (lifted_at IS NOT NULL AND fw_banned_at IS NOT NULL AND fw_unbanned_at IS NULL);

-- name: DefenseDeleteAttemptsBefore :execrows
DELETE FROM login_attempts WHERE ts < sqlc.arg(before);
```

```bash
sqlc generate && go build ./internal/store/
```
Expected: kein Fehler; `DefenseMarkJailBannedParams{FwBannedAt sql.NullInt64; ID int64}`, `DefenseMarkJailUnbannedParams{FwUnbannedAt sql.NullInt64; ID int64}`.

- [ ] **Step 2: Failing test `internal/defense/sync_test.go`**

```go
package defense

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
)

// fakeAgent is an in-memory agentapi.Client.
type fakeAgent struct {
	mu         sync.Mutex
	err        error // returned by every call while set
	banErr     error // returned by Ban only
	health     agentapi.HealthResponse
	bans       map[netip.Prefix]time.Duration
	banCalls   []netip.Prefix
	banDurs    []time.Duration
	unbanCalls []netip.Prefix
}

func newFakeAgent() *fakeAgent {
	return &fakeAgent{
		health: agentapi.HealthResponse{Status: "ok", Version: "test", Firewall: "noop"},
		bans:   make(map[netip.Prefix]time.Duration),
	}
}

var errAgentDown = fmt.Errorf("dial unix /run/mc-panel/agent.sock: %w", agentapi.ErrUnavailable)

func withAgent(a agentapi.Client) func(*Deps) { return func(d *Deps) { d.Agent = a } }

func (f *fakeAgent) setErr(err error)    { f.mu.Lock(); f.err = err; f.mu.Unlock() }
func (f *fakeAgent) setBanErr(err error) { f.mu.Lock(); f.banErr = err; f.mu.Unlock() }
func (f *fakeAgent) clearBans()          { f.mu.Lock(); f.bans = make(map[netip.Prefix]time.Duration); f.mu.Unlock() }

func (f *fakeAgent) calls() (bans, unbans []netip.Prefix, durs []time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]netip.Prefix(nil), f.banCalls...), append([]netip.Prefix(nil), f.unbanCalls...),
		append([]time.Duration(nil), f.banDurs...)
}

func (f *fakeAgent) Health(context.Context) (agentapi.HealthResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return agentapi.HealthResponse{}, f.err
	}
	return f.health, nil
}

func (f *fakeAgent) Ban(_ context.Context, p netip.Prefix, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if f.banErr != nil {
		return f.banErr
	}
	f.banCalls = append(f.banCalls, p)
	f.banDurs = append(f.banDurs, d)
	f.bans[p] = d
	return nil
}

func (f *fakeAgent) Unban(_ context.Context, p netip.Prefix) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.unbanCalls = append(f.unbanCalls, p)
	delete(f.bans, p)
	return nil
}

func (f *fakeAgent) Bans(context.Context) ([]agentapi.Ban, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	out := make([]agentapi.Ban, 0, len(f.bans))
	for p, d := range f.bans {
		out = append(out, agentapi.Ban{Prefix: p.String(), ExpiresInSeconds: int64(d.Seconds())})
	}
	return out, nil
}

func (e *env) pending(t *testing.T) int64 {
	t.Helper()
	n, err := e.q.DefenseCountPendingSync(context.Background(), e.clk.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSyncCatchesUpAfterAgentOutage(t *testing.T) {
	fa := newFakeAgent()
	fa.setErr(errAgentDown)
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()

	// login handling and the in-app jail work while the agent is down
	e.fail(t, "203.0.113.7", 5)
	if err := e.svc.CheckLogin(ctx, addr("203.0.113.7"), "admin"); !errors.Is(err, ErrJailed) {
		t.Fatalf("CheckLogin = %v, want ErrJailed", err)
	}
	if rec := e.filter("203.0.113.7", "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("filter code = %d", rec.Code)
	}
	if err := e.svc.SyncOnce(ctx); !errors.Is(err, agentapi.ErrUnavailable) {
		t.Fatalf("SyncOnce = %v, want ErrUnavailable", err)
	}
	if n := e.pending(t); n != 1 {
		t.Fatalf("pending = %d, want 1", n)
	}

	fa.setErr(nil)
	e.clk.Advance(time.Minute)
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce after recovery: %v", err)
	}
	bans, _, durs := fa.calls()
	if len(bans) != 1 || bans[0] != netip.MustParsePrefix("203.0.113.7/32") || durs[0] != 14*time.Minute {
		t.Fatalf("ban calls = %v %v", bans, durs)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("pending = %d, want 0", n)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 1 {
		t.Fatalf("already banned jails must not be banned again: %v", bans)
	}
}

func TestSyncUnbansLiftedJails(t *testing.T) {
	fa := newFakeAgent()
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()
	id := e.insertJail(t, "2001:db8:1:2::/64", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec("UPDATE jails SET lifted_at = ?, lifted_by = 'admin' WHERE id = ?", t0.UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
	if n := e.pending(t); n != 1 {
		t.Fatalf("pending = %d, want 1", n)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	_, unbans, _ := fa.calls()
	if len(unbans) != 1 || unbans[0] != netip.MustParsePrefix("2001:db8:1:2::/64") {
		t.Fatalf("unban calls = %v", unbans)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, unbans, _ := fa.calls(); len(unbans) != 1 {
		t.Fatalf("unban must happen once: %v", unbans)
	}
	if n := e.pending(t); n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestSyncReconcilesMissingBans(t *testing.T) {
	fa := newFakeAgent()
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()
	e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	fa.clearBans() // agent restart / host reboot: nftables set is empty
	e.clk.Advance(10 * time.Minute)
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	bans, _, durs := fa.calls()
	if len(bans) != 2 || bans[1] != netip.MustParsePrefix("203.0.113.7/32") || durs[1] != 50*time.Minute {
		t.Fatalf("expected re-ban with remaining time, got %v %v", bans, durs)
	}
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 2 {
		t.Fatalf("present bans must not be re-banned: %v", bans)
	}
}

func TestSyncPermanentErrorIsNotRetried(t *testing.T) {
	fa := newFakeAgent()
	fa.setBanErr(errors.New("agent: 409 conflict: overlapping ban"))
	e := newEnv(t, withAgent(fa))
	ctx := context.Background()
	e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
	e.insertJail(t, "203.0.113.8/32", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatalf("permanent errors must not abort the cycle: %v", err)
	}
	fa.setBanErr(nil)
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 0 {
		t.Fatalf("permanently failed jails must not be retried: %v", bans)
	}
	e.insertJail(t, "203.0.113.9/32", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if bans, _, _ := fa.calls(); len(bans) != 1 || bans[0] != netip.MustParsePrefix("203.0.113.9/32") {
		t.Fatalf("new jails must still be synced: %v", bans)
	}
}

func TestSyncWithoutAgentIsNoop(t *testing.T) {
	e := newEnv(t)
	e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
	if err := e.svc.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRunKicksSyncAfterJail(t *testing.T) {
	fa := newFakeAgent()
	e := newEnv(t, withAgent(fa))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond) // let the initial sync pass
	e.fail(t, "203.0.113.7", 5)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if bans, _, _ := fa.calls(); len(bans) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("jail was not synced after kick (SyncInterval is 10s, kick must trigger earlier)")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestPruneOnce(t *testing.T) {
	e := newEnv(t)
	e.clk.Set(t0.Add(-91 * 24 * time.Hour))
	e.fail(t, "203.0.113.7", 1)
	e.clk.Set(t0.Add(-89 * 24 * time.Hour))
	e.fail(t, "203.0.113.7", 1)
	e.clk.Set(t0)
	n, err := e.svc.PruneOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("PruneOnce = %d, %v; want 1", n, err)
	}
}

func TestCleanupLimiters(t *testing.T) {
	e := newEnv(t)
	_ = e.svc.CheckLogin(context.Background(), addr("203.0.113.7"), "admin")
	e.clk.Advance(LimiterIdle)
	e.svc.cleanupLimiters(e.clk.Now())
	if e.svc.loginIP.size() != 0 || e.svc.loginUser.size() != 0 {
		t.Fatalf("idle buckets not removed: %d/%d", e.svc.loginIP.size(), e.svc.loginUser.size())
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/defense/ -run 'Sync|RunKicks|Prune|CleanupLimiters'` → FAIL (`e.svc.SyncOnce undefined`).

- [ ] **Step 4: Implementation `internal/defense/sync.go`**

```go
package defense

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/agent/agentapi"
	"github.com/Bernotese/mc-panel/internal/store"
)

// isUnavailable reports transient agent failures that must be retried later.
func isUnavailable(err error) bool {
	return errors.Is(err, agentapi.ErrUnavailable) || errors.Is(err, context.DeadlineExceeded)
}

func (s *Service) callAgent(ctx context.Context, fn func(context.Context) error) error {
	cctx, cancel := context.WithTimeout(ctx, AgentCallTimeout)
	defer cancel()
	return fn(cctx)
}

func msNull(t time.Time) sql.NullInt64 { return sql.NullInt64{Int64: t.UnixMilli(), Valid: true} }

// SyncOnce reconciles jails with the agent's firewall: unbans lifted jails,
// bans new jails and re-bans active jails missing from the firewall.
func (s *Service) SyncOnce(ctx context.Context) error {
	if s.agent == nil {
		return nil
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	now := s.clock.Now()
	if err := s.syncUnbans(ctx, now); err != nil {
		return err
	}
	if err := s.syncBans(ctx, now); err != nil {
		return err
	}
	return s.reconcile(ctx, now)
}

func (s *Service) syncUnbans(ctx context.Context, now time.Time) error {
	jails, err := s.q.DefenseJailsToUnban(ctx)
	if err != nil {
		return fmt.Errorf("defense: jails to unban: %w", err)
	}
	for _, j := range jails {
		p, perr := netip.ParsePrefix(j.IpKey)
		if perr != nil {
			s.log.Error("defense: invalid ip_key; marking unban as done", "jail_id", j.ID, "ip_key", j.IpKey)
		} else if err := s.callAgent(ctx, func(c context.Context) error { return s.agent.Unban(c, p) }); err != nil {
			if isUnavailable(err) {
				return fmt.Errorf("defense: unban %s: %w", p, err)
			}
			s.log.Error("defense: agent rejected unban; marking as done", "jail_id", j.ID, "prefix", p, "err", err)
		}
		if err := s.q.DefenseMarkJailUnbanned(ctx, store.DefenseMarkJailUnbannedParams{FwUnbannedAt: msNull(now), ID: j.ID}); err != nil {
			return fmt.Errorf("defense: mark unbanned: %w", err)
		}
	}
	return nil
}

func (s *Service) syncBans(ctx context.Context, now time.Time) error {
	jails, err := s.q.DefenseJailsToBan(ctx, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("defense: jails to ban: %w", err)
	}
	for _, j := range jails {
		if s.unbannable[j.ID] {
			continue
		}
		ok, err := s.banJail(ctx, j, now)
		if err != nil {
			return err
		}
		if ok {
			if err := s.q.DefenseMarkJailBanned(ctx, store.DefenseMarkJailBannedParams{FwBannedAt: msNull(now), ID: j.ID}); err != nil {
				return fmt.Errorf("defense: mark banned: %w", err)
			}
		}
	}
	return nil
}

// banJail returns (true, nil) when the agent accepted the ban, (false, nil) when
// the jail was skipped or failed permanently, and an error when the agent is unavailable.
func (s *Service) banJail(ctx context.Context, j store.Jail, now time.Time) (bool, error) {
	p, err := netip.ParsePrefix(j.IpKey)
	if err == nil {
		err = agentapi.ValidateBanPrefix(p)
	}
	if err != nil {
		s.unbannable[j.ID] = true
		s.log.Error("defense: jail cannot be banned in the firewall", "jail_id", j.ID, "ip_key", j.IpKey, "err", err)
		return false, nil
	}
	remaining := time.UnixMilli(j.ExpiresAt).Sub(now)
	if remaining < time.Second {
		return false, nil
	}
	err = s.callAgent(ctx, func(c context.Context) error { return s.agent.Ban(c, p, remaining) })
	if err == nil {
		return true, nil
	}
	if isUnavailable(err) {
		return false, fmt.Errorf("defense: ban %s: %w", p, err)
	}
	s.unbannable[j.ID] = true
	s.log.Error("defense: agent rejected ban; not retrying", "jail_id", j.ID, "prefix", p, "err", err)
	return false, nil
}

// reconcile re-bans active jails whose ban is missing in the firewall
// (agent restart, host reboot).
func (s *Service) reconcile(ctx context.Context, now time.Time) error {
	jails, err := s.q.DefenseActiveBannedJails(ctx, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("defense: banned jails: %w", err)
	}
	if len(jails) == 0 {
		return nil
	}
	var bans []agentapi.Ban
	err = s.callAgent(ctx, func(c context.Context) error {
		var e error
		bans, e = s.agent.Bans(c)
		return e
	})
	if err != nil {
		if isUnavailable(err) {
			return fmt.Errorf("defense: list bans: %w", err)
		}
		s.log.Error("defense: agent rejected ban listing; skipping reconcile", "err", err)
		return nil
	}
	present := make(map[netip.Prefix]bool, len(bans))
	for _, b := range bans {
		if p, err := netip.ParsePrefix(b.Prefix); err == nil {
			present[p.Masked()] = true
		}
	}
	for _, j := range jails {
		p, err := netip.ParsePrefix(j.IpKey)
		if err != nil || present[p.Masked()] || s.unbannable[j.ID] {
			continue
		}
		s.log.Warn("defense: ban missing in firewall; re-banning", "jail_id", j.ID, "prefix", p)
		ok, err := s.banJail(ctx, j, now)
		if err != nil {
			return err
		}
		if ok {
			if err := s.q.DefenseMarkJailBanned(ctx, store.DefenseMarkJailBannedParams{FwBannedAt: msNull(now), ID: j.ID}); err != nil {
				return fmt.Errorf("defense: mark banned: %w", err)
			}
		}
	}
	return nil
}

// PruneOnce deletes login attempts older than AttemptRetention.
func (s *Service) PruneOnce(ctx context.Context) (int64, error) {
	return s.q.DefenseDeleteAttemptsBefore(ctx, s.clock.Now().Add(-AttemptRetention).UnixMilli())
}

func (s *Service) cleanupLimiters(now time.Time) {
	for _, l := range []*keyedLimiter{s.loginIP, s.loginUser, s.secondFactor, s.api} {
		l.cleanup(now, LimiterIdle)
	}
}

// Run starts the background loops (firewall sync, rule refresh, retention,
// limiter cleanup) and blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.runSync(ctx)
	}()
	s.runMaintenance(ctx)
	wg.Wait()
}

func (s *Service) runSync(ctx context.Context) {
	t := time.NewTicker(SyncInterval)
	defer t.Stop()
	failing := false
	for {
		err := s.SyncOnce(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			return
		case err != nil && !failing:
			s.log.Warn("defense: firewall sync failed; will retry", "err", err)
			failing = true
		case err == nil && failing:
			s.log.Info("defense: firewall sync recovered")
			failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
	}
}

func (s *Service) pruneOrLog(ctx context.Context) {
	if n, err := s.PruneOnce(ctx); err != nil {
		s.log.Error("defense: prune login attempts", "err", err)
	} else if n > 0 {
		s.log.Info("defense: pruned old login attempts", "count", n)
	}
}

func (s *Service) runMaintenance(ctx context.Context) {
	refresh := time.NewTicker(RulesRefreshInterval)
	prune := time.NewTicker(PruneInterval)
	limiters := time.NewTicker(LimiterCleanupInterval)
	defer refresh.Stop()
	defer prune.Stop()
	defer limiters.Stop()
	s.pruneOrLog(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			s.reloadOrLog(ctx)
		case <-prune.C:
			s.pruneOrLog(ctx)
		case <-limiters.C:
			s.cleanupLimiters(s.clock.Now())
		}
	}
}
```

- [ ] **Step 5: Run** `go test ./internal/defense/` → PASS. Zusätzlich `make test-race` (bzw. `CGO_ENABLED=1 go test -race ./internal/defense/`) → PASS ohne Data-Race-Meldung.

- [ ] **Step 6: Commit**

```bash
git add internal/store/queries/defense.sql internal/store/defense.sql.go internal/defense/sync.go internal/defense/sync_test.go
git commit -m "feat(defense): sync jails to the agent firewall with reconcile and retention" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Handler-Grundlage und Jail-Endpunkte

**Files:**
- Modify: `internal/store/queries/defense.sql` (anhängen), regeneriert `internal/store/defense.sql.go`
- Create: `internal/defense/handler.go`, `internal/defense/routes.go`, `internal/defense/jails_api.go`
- Test: `internal/defense/handler_test.go`, `internal/defense/jails_api_test.go`

**Interfaces:**
- Consumes: `rbac.NewRouter(chi.Router, rbac.Checker, clock.Clock) *rbac.Router`, `(*rbac.Router).Get/Post/Delete(pattern string, rule rbac.Rule, h http.HandlerFunc)`, `rbac.Admin()`, `rbac.Rule.WithSudo()`, `rbac.Permission`, `chi.URLParam`, `httpx.DecodeJSON`, `httpx.WriteJSON`, `httpx.WriteError`, `httpx.Internal`, `httpx.CodeValidation`, `httpx.CodeConflict`, `httpx.CodeNotFound`, `httpx.CodeBadRequest`, `principal.From`, `audit.ActionJailCreate`, `audit.ActionJailLift`, Task 2–7.
- Produces:
```go
type Handler struct{ /* unexported */ }
func NewHandler(s *Service) *Handler
func (h *Handler) Routes(api *rbac.Router)     // in this task: jail routes; Tasks 9–11 extend routes.go
func (s *Service) createManualJail(ctx context.Context, key netip.Prefix, reason string, d time.Duration, now time.Time) (store.Jail, error)
var errAlreadyJailed error
type jailResponse struct{ ID int64; IPKey string; Reason string; Level int64; Manual bool; CreatedAt, ExpiresAt int64;
                          LiftedAt *int64; LiftedBy *string; FirewallSynced bool }   // JSON = OpenAPI schema Jail
func toJailResponse(j store.Jail, now time.Time) jailResponse
func firewallSynced(j store.Jail, now time.Time) bool
// helpers
func pathID(w http.ResponseWriter, r *http.Request, what string) (int64, bool)
func validation(w http.ResponseWriter, r *http.Request, msg string)
func conflict(w http.ResponseWriter, r *http.Request, msg string)
func isUniqueViolation(err error) bool
func nullInt64Ptr(v sql.NullInt64) *int64
func callerName(ctx context.Context) string
func (h *Handler) record(ctx context.Context, e audit.Entry)
// sqlc
func (q *Queries) DefenseListJails(ctx context.Context, lim int64) ([]Jail, error)
func (q *Queries) DefenseGetJail(ctx context.Context, id int64) (Jail, error)
func (q *Queries) DefenseLiftJail(ctx context.Context, arg DefenseLiftJailParams) (int64, error)
```
Endpunkte: `GET /security/jails` (Query `active`, Standard `true`; `false` → die letzten 200 Jails), `POST /security/jails` (201), `DELETE /security/jails/{id}` (204; unbekannt oder bereits aufgehoben → 404). `firewall_synced`: aufgehoben → `fw_banned_at IS NULL OR fw_unbanned_at IS NOT NULL`; abgelaufen → `true`; aktiv → `fw_banned_at IS NOT NULL`.

- [ ] **Step 1: Queries an `internal/store/queries/defense.sql` anhängen**

```sql
-- name: DefenseListJails :many
SELECT * FROM jails ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(lim);

-- name: DefenseGetJail :one
SELECT * FROM jails WHERE id = sqlc.arg(id);

-- name: DefenseLiftJail :execrows
UPDATE jails SET lifted_at = sqlc.arg(lifted_at), lifted_by = sqlc.arg(lifted_by)
WHERE id = sqlc.arg(id) AND lifted_at IS NULL;
```

```bash
sqlc generate && go build ./internal/store/
```
Expected: kein Fehler; `DefenseLiftJailParams{LiftedAt sql.NullInt64; LiftedBy sql.NullString; ID int64}`.

- [ ] **Step 2: Test-Helfer `internal/defense/handler_test.go`**

```go
package defense

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

type denyChecker struct{}

func (denyChecker) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, nil
}

const callerIP = "198.51.100.10"

func (e *env) router() http.Handler {
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, denyChecker{}, e.clk)
	NewHandler(e.svc).Routes(rt)
	return mux
}

// call performs a request as admin "admin" with valid sudo from ip.
// body may be nil, a raw JSON string or a value that is marshalled.
func (e *env) call(t *testing.T, method, path string, body any, ip string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	ctx := httpx.WithClientIP(req.Context(), addr(ip))
	ctx = principal.With(ctx, principal.Principal{
		UserID: e.adminID, Username: "admin", Role: principal.RoleAdmin, Stage: principal.StageFull,
		SessionID: 1, SudoUntil: e.clk.Now().Add(5 * time.Minute),
	})
	rec := httptest.NewRecorder()
	e.router().ServeHTTP(rec, req.WithContext(ctx))
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

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, code int, errCodeWant string) {
	t.Helper()
	if rec.Code != code || (errCodeWant != "" && errCode(rec) != errCodeWant) {
		t.Fatalf("status = %d err = %q (body %s), want %d %q", rec.Code, errCode(rec), rec.Body, code, errCodeWant)
	}
}
```

- [ ] **Step 3: Failing test `internal/defense/jails_api_test.go`**

```go
package defense

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

func TestListJails(t *testing.T) {
	e := newEnv(t)
	active := e.insertJail(t, "203.0.113.1/32", t0, t0.Add(time.Hour))
	e.insertJail(t, "203.0.113.2/32", t0.Add(-2*time.Hour), t0.Add(-time.Hour))
	lifted := e.insertJail(t, "203.0.113.3/32", t0, t0.Add(time.Hour))
	if _, err := e.db.Exec("UPDATE jails SET lifted_at = ?, lifted_by = 'admin' WHERE id = ?", t0.UnixMilli(), lifted); err != nil {
		t.Fatal(err)
	}
	rec := e.call(t, http.MethodGet, "/security/jails", nil, callerIP)
	wantStatus(t, rec, http.StatusOK, "")
	items := decode[[]map[string]any](t, rec)
	if len(items) != 1 || items[0]["id"] != float64(active) || items[0]["firewall_synced"] != false ||
		items[0]["lifted_at"] != nil || items[0]["manual"] != false || items[0]["ip_key"] != "203.0.113.1/32" {
		t.Fatalf("active jails = %v", items)
	}
	for _, k := range []string{"id", "ip_key", "reason", "level", "manual", "created_at", "expires_at", "lifted_at", "lifted_by", "firewall_synced"} {
		if _, ok := items[0][k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	all := decode[[]map[string]any](t, e.call(t, http.MethodGet, "/security/jails?active=false", nil, callerIP))
	if len(all) != 3 {
		t.Fatalf("all jails = %v", all)
	}
	for _, j := range all {
		if j["id"] == float64(lifted) && (j["lifted_by"] != "admin" || j["firewall_synced"] != true) {
			t.Fatalf("lifted jail = %v", j)
		}
	}
	wantStatus(t, e.call(t, http.MethodGet, "/security/jails?active=maybe", nil, callerIP), http.StatusBadRequest, httpx.CodeBadRequest)
}

func TestCreateJail(t *testing.T) {
	e := newEnv(t)
	rec := e.call(t, http.MethodPost, "/security/jails",
		map[string]any{"ip": "2001:db8:1:2::5", "duration_minutes": 60, "reason": "scanner"}, callerIP)
	wantStatus(t, rec, http.StatusCreated, "")
	j := decode[map[string]any](t, rec)
	if j["ip_key"] != "2001:db8:1:2::/64" || j["manual"] != true || j["level"] != float64(1) ||
		j["expires_at"] != float64(t0.Add(time.Hour).UnixMilli()) || j["reason"] != "scanner" {
		t.Fatalf("jail = %v", j)
	}
	if rec := e.filter("2001:db8:1:2::99", "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("manual jail must block immediately, code=%d", rec.Code)
	}
	if !slices.Contains(e.audit.Actions(), audit.ActionJailCreate) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	wantStatus(t, e.call(t, http.MethodPost, "/security/jails",
		map[string]any{"ip": "2001:db8:1:2::6", "duration_minutes": 5, "reason": ""}, callerIP), http.StatusConflict, httpx.CodeConflict)
}

func TestCreateJailValidation(t *testing.T) {
	e := newEnv(t)
	cases := []any{
		map[string]any{"ip": "nope", "duration_minutes": 5, "reason": ""},
		map[string]any{"ip": "203.0.113.0/24", "duration_minutes": 5, "reason": ""},
		map[string]any{"ip": "127.0.0.1", "duration_minutes": 5, "reason": ""},
		map[string]any{"ip": "::", "duration_minutes": 5, "reason": ""},
		map[string]any{"ip": "203.0.113.7", "duration_minutes": 0, "reason": ""},
		map[string]any{"ip": "203.0.113.7", "duration_minutes": 10081, "reason": ""},
		map[string]any{"ip": "203.0.113.7", "duration_minutes": 5, "reason": strings.Repeat("x", 201)},
	}
	for i, body := range cases {
		rec := e.call(t, http.MethodPost, "/security/jails", body, callerIP)
		if rec.Code != http.StatusBadRequest || errCode(rec) != httpx.CodeValidation {
			t.Errorf("case %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	wantStatus(t, e.call(t, http.MethodPost, "/security/jails", `{"ip":"203.0.113.7","duration_minutes":5,"reason":"","x":1}`, callerIP),
		http.StatusBadRequest, httpx.CodeBadRequest)
}

func TestCreateJailRejectsOwnIPAndAllowlisted(t *testing.T) {
	e := newEnv(t)
	body := func(ip string) map[string]any { return map[string]any{"ip": ip, "duration_minutes": 5, "reason": ""} }
	wantStatus(t, e.call(t, http.MethodPost, "/security/jails", body(callerIP), callerIP), http.StatusConflict, httpx.CodeConflict)
	wantStatus(t, e.call(t, http.MethodPost, "/security/jails", body("2001:db8:9:9::2"), "2001:db8:9:9::1"), http.StatusConflict, httpx.CodeConflict)
	e.addRule(t, "203.0.113.0/24", "allow", nil)
	wantStatus(t, e.call(t, http.MethodPost, "/security/jails", body("203.0.113.7"), callerIP), http.StatusConflict, httpx.CodeConflict)
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("no jail may be created: %+v", js)
	}
}

func TestLiftJail(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 5)
	js := e.activeJails(t)
	if len(js) != 1 {
		t.Fatalf("precondition: %+v", js)
	}
	path := fmt.Sprintf("/security/jails/%d", js[0].ID)
	wantStatus(t, e.call(t, http.MethodDelete, path, nil, callerIP), http.StatusNoContent, "")
	if rec := e.filter("203.0.113.7", "/"); rec.Code != http.StatusNoContent {
		t.Fatalf("lifted jail must stop blocking immediately, code=%d", rec.Code)
	}
	j, err := e.q.DefenseGetJail(t.Context(), js[0].ID)
	if err != nil || !j.LiftedAt.Valid || j.LiftedBy.String != "admin" {
		t.Fatalf("jail = %+v err=%v", j, err)
	}
	if !slices.Contains(e.audit.Actions(), audit.ActionJailLift) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	wantStatus(t, e.call(t, http.MethodDelete, path, nil, callerIP), http.StatusNotFound, httpx.CodeNotFound)
	wantStatus(t, e.call(t, http.MethodDelete, "/security/jails/999", nil, callerIP), http.StatusNotFound, httpx.CodeNotFound)
	wantStatus(t, e.call(t, http.MethodDelete, "/security/jails/abc", nil, callerIP), http.StatusNotFound, httpx.CodeNotFound)
}
```

- [ ] **Step 4: Run** `go test ./internal/defense/ -run 'Jail'` → FAIL (`undefined: NewHandler`).

- [ ] **Step 5: Implementation `internal/defense/handler.go`**

```go
package defense

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/store"
)

// Handler serves the security-center API (/api/security/...).
type Handler struct{ s *Service }

func NewHandler(s *Service) *Handler { return &Handler{s: s} }

func pathID(w http.ResponseWriter, r *http.Request, what string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, what+" not found")
		return 0, false
	}
	return id, true
}

func validation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func conflict(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, msg)
}

// isUniqueViolation detects SQLite UNIQUE constraint errors (modernc.org/sqlite message text).
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func callerName(ctx context.Context) string {
	if p, ok := principal.From(ctx); ok {
		return p.Username
	}
	return ""
}

// record writes an audit entry after the change has been committed; failures are logged.
func (h *Handler) record(ctx context.Context, e audit.Entry) {
	if e.Result == "" {
		e.Result = audit.ResultSuccess
	}
	if err := h.s.audit.Record(ctx, e); err != nil {
		h.s.log.Error("defense: audit", "action", e.Action, "err", err)
	}
}

type jailResponse struct {
	ID             int64   `json:"id"`
	IPKey          string  `json:"ip_key"`
	Reason         string  `json:"reason"`
	Level          int64   `json:"level"`
	Manual         bool    `json:"manual"`
	CreatedAt      int64   `json:"created_at"`
	ExpiresAt      int64   `json:"expires_at"`
	LiftedAt       *int64  `json:"lifted_at"`
	LiftedBy       *string `json:"lifted_by"`
	FirewallSynced bool    `json:"firewall_synced"`
}

func firewallSynced(j store.Jail, now time.Time) bool {
	if j.LiftedAt.Valid {
		return !j.FwBannedAt.Valid || j.FwUnbannedAt.Valid
	}
	if j.ExpiresAt <= now.UnixMilli() {
		return true
	}
	return j.FwBannedAt.Valid
}

func toJailResponse(j store.Jail, now time.Time) jailResponse {
	resp := jailResponse{
		ID: j.ID, IPKey: j.IpKey, Reason: j.Reason, Level: j.Level, Manual: j.Manual != 0,
		CreatedAt: j.CreatedAt, ExpiresAt: j.ExpiresAt, LiftedAt: nullInt64Ptr(j.LiftedAt),
		FirewallSynced: firewallSynced(j, now),
	}
	if j.LiftedBy.Valid {
		by := j.LiftedBy.String
		resp.LiftedBy = &by
	}
	return resp
}
```

- [ ] **Step 6: Implementation `internal/defense/jails_api.go`**

```go
package defense

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

const (
	maxManualJailMinutes = 10080
	maxJailReasonLen     = 200
	jailListLimit        = 200
)

var errAlreadyJailed = errors.New("defense: already jailed")

// createManualJail inserts a manual jail unless the key is already jailed.
func (s *Service) createManualJail(ctx context.Context, key netip.Prefix, reason string, d time.Duration, now time.Time) (store.Jail, error) {
	s.jailMu.Lock()
	defer s.jailMu.Unlock()
	if _, ok := s.snap.Load().jailedUntil(key, now); ok {
		return store.Jail{}, errAlreadyJailed
	}
	prior, err := s.q.DefenseCountJailsSince(ctx, store.DefenseCountJailsSinceParams{
		IpKey: key.String(), Since: now.Add(-RepeatWindow).UnixMilli(),
	})
	if err != nil {
		return store.Jail{}, fmt.Errorf("defense: count jails: %w", err)
	}
	expires := now.Add(d)
	j, err := s.q.DefenseInsertJail(ctx, store.DefenseInsertJailParams{
		IpKey: key.String(), Reason: reason, Level: prior + 1, Manual: 1,
		CreatedAt: now.UnixMilli(), ExpiresAt: expires.UnixMilli(),
	})
	if err != nil {
		return store.Jail{}, fmt.Errorf("defense: insert jail: %w", err)
	}
	s.addJail(key, expires)
	return j, nil
}

func (h *Handler) listJails(w http.ResponseWriter, r *http.Request) {
	active := true
	if v := r.URL.Query().Get("active"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "active must be true or false")
			return
		}
		active = b
	}
	now := h.s.clock.Now()
	var rows []store.Jail
	var err error
	if active {
		rows, err = h.s.q.DefenseListActiveJails(r.Context(), now.UnixMilli())
	} else {
		rows, err = h.s.q.DefenseListJails(r.Context(), jailListLimit)
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]jailResponse, 0, len(rows))
	for _, j := range rows {
		out = append(out, toJailResponse(j, now))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createJailRequest struct {
	IP              string `json:"ip"`
	DurationMinutes int64  `json:"duration_minutes"`
	Reason          string `json:"reason"`
}

func (h *Handler) createJail(w http.ResponseWriter, r *http.Request) {
	var req createJailRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(req.IP))
	if err != nil {
		validation(w, r, "ip must be a single IP address")
		return
	}
	ip = normalizeIP(ip)
	if ip.IsLoopback() || ip.IsUnspecified() {
		validation(w, r, "loopback and unspecified addresses cannot be jailed")
		return
	}
	if req.DurationMinutes < 1 || req.DurationMinutes > maxManualJailMinutes {
		validation(w, r, "duration_minutes must be between 1 and 10080")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if utf8.RuneCountInString(reason) > maxJailReasonLen {
		validation(w, r, "reason must be at most 200 characters")
		return
	}
	if reason == "" {
		reason = "manual"
	}
	ctx := r.Context()
	now := h.s.clock.Now()
	key := httpx.IPKey(ip)
	if h.s.snap.Load().allowOverlaps(key, now) {
		conflict(w, r, "the address is covered by an allow rule")
		return
	}
	if caller := normalizeIP(httpx.ClientIP(ctx)); caller.IsValid() && httpx.IPKey(caller) == key {
		conflict(w, r, "refusing to jail your own address")
		return
	}
	j, err := h.s.createManualJail(ctx, key, reason, time.Duration(req.DurationMinutes)*time.Minute, now)
	if errors.Is(err, errAlreadyJailed) {
		conflict(w, r, "the address is already jailed")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.s.kickSync()
	h.record(ctx, audit.Entry{
		Action: audit.ActionJailCreate, Target: key.String(),
		Details: map[string]any{"jail_id": j.ID, "duration_minutes": req.DurationMinutes, "reason": reason},
	})
	httpx.WriteJSON(w, http.StatusCreated, toJailResponse(j, now))
}

func (h *Handler) liftJail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "jail")
	if !ok {
		return
	}
	ctx := r.Context()
	j, err := h.s.q.DefenseGetJail(ctx, id)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && j.LiftedAt.Valid) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "jail not found or already lifted")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.s.clock.Now()
	n, err := h.s.q.DefenseLiftJail(ctx, store.DefenseLiftJailParams{
		LiftedAt: msNull(now), LiftedBy: sql.NullString{String: callerName(ctx), Valid: true}, ID: id,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "jail not found or already lifted")
		return
	}
	h.s.reloadOrLog(ctx)
	h.s.kickSync()
	h.record(ctx, audit.Entry{Action: audit.ActionJailLift, Target: j.IpKey, Details: map[string]any{"jail_id": id}})
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 7: `internal/defense/routes.go` (Stand nach Task 8)**

```go
package defense

import "github.com/Bernotese/mc-panel/internal/rbac"

// Routes registers the security-center endpoints with full paths relative to /api.
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/security/jails", rbac.Admin(), h.listJails)
	api.Post("/security/jails", rbac.Admin().WithSudo(), h.createJail)
	api.Delete("/security/jails/{id}", rbac.Admin().WithSudo(), h.liftJail)
}
```

- [ ] **Step 8: Run** `go test ./internal/defense/` → PASS. (`t.Context()` erfordert Go ≥ 1.24; die Toolchain aus Phase 0 ist aktueller.)

- [ ] **Step 9: Commit**

```bash
git add internal/store/queries/defense.sql internal/store/defense.sql.go internal/defense/handler.go internal/defense/routes.go internal/defense/jails_api.go internal/defense/handler_test.go internal/defense/jails_api_test.go
git commit -m "feat(defense): add jail list, manual jail and lift endpoints" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: IP-Regel-Endpunkte und Schutz vor Selbstaussperrung

**Files:**
- Modify: `internal/store/queries/defense.sql` (anhängen), regeneriert `internal/store/defense.sql.go`; `internal/defense/routes.go` (ersetzen)
- Create: `internal/defense/lockout.go`, `internal/defense/rules_api.go`
- Test: `internal/defense/rules_api_test.go`

**Interfaces:**
- Consumes: Task 2 (`snapshot`, `clone`, `ruleEntry`), Task 5 (`blockReason`), Task 8 (Handler-Helfer), `audit.ActionIPRuleCreate`, `audit.ActionIPRuleDelete`.
- Produces:
```go
func (s *snapshot) locksOut(ip netip.Addr, now time.Time, geo GeoResolver) string   // "" | "deny_rule" | "country:XX" | "admin_allowlist"
func (s *snapshot) withRule(kind string, e ruleEntry) *snapshot
func (s *snapshot) withoutRule(id int64) *snapshot
func (s *snapshot) withSettings(st store.SecuritySettings) *snapshot
func lockoutMessage(reason string) string
func parseRuleCIDR(s string) (netip.Prefix, error)   // CIDR or bare IP (→ /32, /128), masked; rejects zones and IPv4-mapped
type ipRuleResponse struct{ ID int64; CIDR, Kind, Comment string; ExpiresAt *int64; CreatedBy string; CreatedAt int64 }  // = OpenAPI IPRule
// sqlc
func (q *Queries) DefenseGetIPRule(ctx context.Context, id int64) (IpRule, error)
func (q *Queries) DefenseDeleteIPRule(ctx context.Context, id int64) (int64, error)
```
Endpunkte: `GET /security/ip-rules` (alle, inkl. abgelaufene), `POST /security/ip-rules` (201; Duplikat `cidr+kind` → 409; Selbstaussperrung → 409), `DELETE /security/ip-rules/{id}` (204; unbekannt → 404; Selbstaussperrung → 409).

- [ ] **Step 1: Queries an `internal/store/queries/defense.sql` anhängen**

```sql
-- name: DefenseGetIPRule :one
SELECT * FROM ip_rules WHERE id = sqlc.arg(id);

-- name: DefenseDeleteIPRule :execrows
DELETE FROM ip_rules WHERE id = sqlc.arg(id);
```

```bash
sqlc generate && go build ./internal/store/
```

- [ ] **Step 2: Failing test `internal/defense/rules_api_test.go`**

```go
package defense

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

func ruleBody(cidr, kind string) map[string]any {
	return map[string]any{"cidr": cidr, "kind": kind, "comment": ""}
}

func TestCreateAndListIPRules(t *testing.T) {
	e := newEnv(t)
	rec := e.call(t, http.MethodPost, "/security/ip-rules",
		map[string]any{"cidr": "203.0.113.77/24", "kind": "deny", "comment": " bots ", "expires_at": nil}, callerIP)
	wantStatus(t, rec, http.StatusCreated, "")
	r := decode[map[string]any](t, rec)
	if r["cidr"] != "203.0.113.0/24" || r["kind"] != "deny" || r["comment"] != "bots" || r["created_by"] != "admin" ||
		r["expires_at"] != nil || r["created_at"] != float64(t0.UnixMilli()) {
		t.Fatalf("rule = %v", r)
	}
	rec = e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("2001:db8::1", "allow"), callerIP)
	wantStatus(t, rec, http.StatusCreated, "")
	if got := decode[map[string]any](t, rec)["cidr"]; got != "2001:db8::1/128" {
		t.Fatalf("bare IPv6 must become /128, got %v", got)
	}
	exp := t0.Add(time.Hour).UnixMilli()
	rec = e.call(t, http.MethodPost, "/security/ip-rules",
		map[string]any{"cidr": "192.0.2.0/24", "kind": "allow", "comment": "", "expires_at": exp}, callerIP)
	wantStatus(t, rec, http.StatusCreated, "")
	if got := decode[map[string]any](t, rec)["expires_at"]; got != float64(exp) {
		t.Fatalf("expires_at = %v", got)
	}
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("203.0.113.0/24", "deny"), callerIP),
		http.StatusConflict, httpx.CodeConflict)
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("203.0.113.0/24", "allow"), callerIP),
		http.StatusCreated, "")
	items := decode[[]map[string]any](t, e.call(t, http.MethodGet, "/security/ip-rules", nil, callerIP))
	if len(items) != 4 {
		t.Fatalf("rules = %v", items)
	}
	if n := strings.Count(strings.Join(e.audit.Actions(), ","), audit.ActionIPRuleCreate); n != 4 {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestCreateIPRuleValidation(t *testing.T) {
	e := newEnv(t)
	past := t0.Add(-time.Second).UnixMilli()
	cases := []map[string]any{
		ruleBody("nope", "deny"),
		ruleBody("::ffff:203.0.113.7/128", "deny"),
		ruleBody("fe80::1%en0", "deny"),
		ruleBody("203.0.113.0/33", "deny"),
		ruleBody("203.0.113.0/24", "block"),
		{"cidr": "203.0.113.0/24", "kind": "deny", "comment": strings.Repeat("x", 201)},
		{"cidr": "203.0.113.0/24", "kind": "deny", "comment": "", "expires_at": past},
	}
	for i, body := range cases {
		rec := e.call(t, http.MethodPost, "/security/ip-rules", body, callerIP)
		if rec.Code != http.StatusBadRequest || errCode(rec) != httpx.CodeValidation {
			t.Errorf("case %d: %d %s", i, rec.Code, rec.Body)
		}
	}
}

func TestCreatedDenyRuleBlocksImmediately(t *testing.T) {
	e := newEnv(t)
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("203.0.113.0/24", "deny"), callerIP), http.StatusCreated, "")
	if rec := e.filter("203.0.113.7", "/"); rec.Code != http.StatusForbidden || errCode(rec) != httpx.CodeIPBlocked {
		t.Fatalf("code=%d err=%q", rec.Code, errCode(rec))
	}
}

func TestCreateIPRuleRejectsSelfLockout(t *testing.T) {
	e := newEnv(t)
	for _, cidr := range []string{"198.51.100.0/24", "0.0.0.0/0", callerIP} {
		wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody(cidr, "deny"), callerIP),
			http.StatusConflict, httpx.CodeConflict)
	}
	// an IPv6 deny does not affect an IPv4 caller
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("::/0", "deny"), callerIP), http.StatusCreated, "")
	// once the caller is allowlisted, a covering deny rule is fine
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody(callerIP, "allow"), callerIP), http.StatusCreated, "")
	wantStatus(t, e.call(t, http.MethodPost, "/security/ip-rules", ruleBody("198.51.100.0/24", "deny"), callerIP), http.StatusCreated, "")
}

func TestDeleteIPRuleRejectsSelfLockout(t *testing.T) {
	e := newEnv(t)
	own := e.addRule(t, callerIP+"/32", "allow", nil)
	other := e.addRule(t, "203.0.113.0/24", "allow", nil)
	e.saveSettings(t, store.SecuritySettings{AdminAllowlistOnly: true, AllowedCountries: []string{}})
	wantStatus(t, e.call(t, http.MethodDelete, fmt.Sprintf("/security/ip-rules/%d", own), nil, callerIP),
		http.StatusConflict, httpx.CodeConflict)
	wantStatus(t, e.call(t, http.MethodDelete, fmt.Sprintf("/security/ip-rules/%d", other), nil, callerIP),
		http.StatusNoContent, "")
	if !slices.Contains(e.audit.Actions(), audit.ActionIPRuleDelete) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	wantStatus(t, e.call(t, http.MethodDelete, "/security/ip-rules/999", nil, callerIP), http.StatusNotFound, httpx.CodeNotFound)
	// from loopback the admin can always remove the rule
	wantStatus(t, e.call(t, http.MethodDelete, fmt.Sprintf("/security/ip-rules/%d", own), nil, "127.0.0.1"),
		http.StatusNoContent, "")
	if rules, _ := e.q.DefenseListIPRules(t.Context()); len(rules) != 0 {
		t.Fatalf("rules left: %+v", rules)
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/defense/ -run 'IPRule|DenyRule'` → FAIL (404 für alle Requests bzw. `undefined: parseRuleCIDR` nach Anlegen der Datei).

- [ ] **Step 4: Implementation `internal/defense/lockout.go`**

```go
package defense

import (
	"net/netip"
	"slices"
	"time"

	"github.com/Bernotese/mc-panel/internal/store"
)

// locksOut reports why ip would be locked out of the panel under this snapshot.
func (s *snapshot) locksOut(ip netip.Addr, now time.Time, geo GeoResolver) string {
	ip = normalizeIP(ip)
	if !ip.IsValid() {
		return ""
	}
	if r := s.blockReason(ip, now, geo); r != "" {
		return r
	}
	if s.settings.AdminAllowlistOnly && !ip.IsLoopback() && !s.allowed(ip, now) {
		return "admin_allowlist"
	}
	return ""
}

func (s *snapshot) withRule(kind string, e ruleEntry) *snapshot {
	c := s.clone()
	if kind == "allow" {
		c.allow = append(c.allow, e)
	} else {
		c.deny = append(c.deny, e)
	}
	return c
}

func (s *snapshot) withoutRule(id int64) *snapshot {
	c := s.clone()
	match := func(r ruleEntry) bool { return r.id == id }
	c.allow = slices.DeleteFunc(c.allow, match)
	c.deny = slices.DeleteFunc(c.deny, match)
	return c
}

func (s *snapshot) withSettings(st store.SecuritySettings) *snapshot {
	c := s.clone()
	c.settings = st
	c.settings.AllowedCountries = slices.Clone(st.AllowedCountries)
	return c
}

func lockoutMessage(reason string) string {
	return "this change would block your current IP address (" + reason + "); use `panel admin unlock` on the server if you are locked out"
}
```

- [ ] **Step 5: Implementation `internal/defense/rules_api.go`**

```go
package defense

import (
	"database/sql"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

const maxRuleCommentLen = 200

var errBadCIDR = errors.New("cidr must be an IPv4 or IPv6 address or network, e.g. 203.0.113.0/24")

func parseRuleCIDR(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	var p netip.Prefix
	if strings.Contains(s, "/") {
		var err error
		if p, err = netip.ParsePrefix(s); err != nil {
			return netip.Prefix{}, errBadCIDR
		}
	} else {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return netip.Prefix{}, errBadCIDR
		}
		p = netip.PrefixFrom(a, a.BitLen())
	}
	if p.Addr().Is4In6() {
		return netip.Prefix{}, errors.New("use plain IPv4 notation instead of IPv4-mapped IPv6")
	}
	return p.Masked(), nil
}

type ipRuleResponse struct {
	ID        int64  `json:"id"`
	CIDR      string `json:"cidr"`
	Kind      string `json:"kind"`
	Comment   string `json:"comment"`
	ExpiresAt *int64 `json:"expires_at"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
}

func toIPRuleResponse(r store.IpRule) ipRuleResponse {
	return ipRuleResponse{
		ID: r.ID, CIDR: r.Cidr, Kind: r.Kind, Comment: r.Comment, ExpiresAt: nullInt64Ptr(r.ExpiresAt),
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
	}
}

func (h *Handler) listIPRules(w http.ResponseWriter, r *http.Request) {
	rows, err := h.s.q.DefenseListIPRules(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]ipRuleResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toIPRuleResponse(row))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createIPRuleRequest struct {
	CIDR      string `json:"cidr"`
	Kind      string `json:"kind"`
	Comment   string `json:"comment"`
	ExpiresAt *int64 `json:"expires_at"`
}

func (h *Handler) createIPRule(w http.ResponseWriter, r *http.Request) {
	var req createIPRuleRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	p, err := parseRuleCIDR(req.CIDR)
	if err != nil {
		validation(w, r, err.Error())
		return
	}
	if req.Kind != "allow" && req.Kind != "deny" {
		validation(w, r, "kind must be allow or deny")
		return
	}
	comment := strings.TrimSpace(req.Comment)
	if utf8.RuneCountInString(comment) > maxRuleCommentLen {
		validation(w, r, "comment must be at most 200 characters")
		return
	}
	ctx := r.Context()
	now := h.s.clock.Now()
	entry := ruleEntry{prefix: p}
	var exp sql.NullInt64
	if req.ExpiresAt != nil {
		if *req.ExpiresAt <= now.UnixMilli() {
			validation(w, r, "expires_at must be in the future")
			return
		}
		exp = sql.NullInt64{Int64: *req.ExpiresAt, Valid: true}
		entry.expires = time.UnixMilli(*req.ExpiresAt)
	}
	if reason := h.s.snap.Load().withRule(req.Kind, entry).locksOut(httpx.ClientIP(ctx), now, h.s.geo); reason != "" {
		conflict(w, r, lockoutMessage(reason))
		return
	}
	row, err := h.s.q.DefenseInsertIPRule(ctx, store.DefenseInsertIPRuleParams{
		Cidr: p.String(), Kind: req.Kind, Comment: comment, ExpiresAt: exp,
		CreatedBy: callerName(ctx), CreatedAt: now.UnixMilli(),
	})
	if isUniqueViolation(err) {
		conflict(w, r, "a rule for this network and kind already exists")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.s.reloadOrLog(ctx)
	h.record(ctx, audit.Entry{
		Action: audit.ActionIPRuleCreate, Target: row.Cidr,
		Details: map[string]any{"rule_id": row.ID, "kind": row.Kind, "comment": row.Comment, "expires_at": nullInt64Ptr(row.ExpiresAt)},
	})
	httpx.WriteJSON(w, http.StatusCreated, toIPRuleResponse(row))
}

func (h *Handler) deleteIPRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "ip rule")
	if !ok {
		return
	}
	ctx := r.Context()
	row, err := h.s.q.DefenseGetIPRule(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "ip rule not found")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.s.clock.Now()
	if reason := h.s.snap.Load().withoutRule(id).locksOut(httpx.ClientIP(ctx), now, h.s.geo); reason != "" {
		conflict(w, r, lockoutMessage(reason))
		return
	}
	n, err := h.s.q.DefenseDeleteIPRule(ctx, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "ip rule not found")
		return
	}
	h.s.reloadOrLog(ctx)
	h.record(ctx, audit.Entry{
		Action: audit.ActionIPRuleDelete, Target: row.Cidr,
		Details: map[string]any{"rule_id": row.ID, "kind": row.Kind},
	})
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 6: `internal/defense/routes.go` ersetzen (Stand nach Task 9)**

```go
package defense

import "github.com/Bernotese/mc-panel/internal/rbac"

// Routes registers the security-center endpoints with full paths relative to /api.
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/security/jails", rbac.Admin(), h.listJails)
	api.Post("/security/jails", rbac.Admin().WithSudo(), h.createJail)
	api.Delete("/security/jails/{id}", rbac.Admin().WithSudo(), h.liftJail)
	api.Get("/security/ip-rules", rbac.Admin(), h.listIPRules)
	api.Post("/security/ip-rules", rbac.Admin().WithSudo(), h.createIPRule)
	api.Delete("/security/ip-rules/{id}", rbac.Admin().WithSudo(), h.deleteIPRule)
}
```

- [ ] **Step 7: Run** `go test ./internal/defense/` → PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/store/queries/defense.sql internal/store/defense.sql.go internal/defense/lockout.go internal/defense/rules_api.go internal/defense/rules_api_test.go internal/defense/routes.go
git commit -m "feat(defense): add IP rule endpoints with self-lockout protection" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Sicherheitseinstellungen (`GET`/`PUT /security/settings`)

**Files:**
- Modify: `internal/defense/routes.go` (ersetzen)
- Create: `internal/defense/settings_api.go`
- Test: `internal/defense/settings_api_test.go`

**Interfaces:**
- Consumes: `store.SecuritySettings`, `store.LoadSecuritySettings`, `store.SaveSecuritySettings`, `audit.ActionSettingsUpdate`, Task 6 (`countryCodeRE`), Task 9 (`withSettings`, `locksOut`, `lockoutMessage`), `Service.GeoAvailable()`.
- Produces:
```go
type settingsResponse struct{ Enforce2FAForUsers, AdminAllowlistOnly bool; AllowedCountries []string; GeoIPAvailable bool }  // = SecuritySettingsResponse
func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request)
func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request)
```
Regeln: Alle drei Felder Pflicht (`null`/fehlend → `400 validation_failed`); Ländercodes `^[A-Z]{2}$`, Duplikate werden entfernt (Reihenfolge bleibt), max. 300 Einträge. Länderliste wird auch ohne GeoIP-DB gespeichert (wirkt dann nicht; `geoip_available=false` informiert das UI).

- [ ] **Step 1: Failing test `internal/defense/settings_api_test.go`**

```go
package defense

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

func settingsBody(enforce, allowlistOnly bool, countries []string) map[string]any {
	return map[string]any{"enforce_2fa_for_users": enforce, "admin_allowlist_only": allowlistOnly, "allowed_countries": countries}
}

func TestGetSettings(t *testing.T) {
	e := newEnv(t)
	got := decode[map[string]any](t, e.call(t, http.MethodGet, "/security/settings", nil, callerIP))
	if got["enforce_2fa_for_users"] != false || got["admin_allowlist_only"] != false || got["geoip_available"] != true {
		t.Fatalf("settings = %v", got)
	}
	if c, ok := got["allowed_countries"].([]any); !ok || len(c) != 0 {
		t.Fatalf("allowed_countries must be an empty array, got %#v", got["allowed_countries"])
	}
	noGeo := newEnv(t, func(d *Deps) { d.GeoAvailable = false })
	if got := decode[map[string]any](t, noGeo.call(t, http.MethodGet, "/security/settings", nil, callerIP)); got["geoip_available"] != false {
		t.Fatalf("geoip_available = %v", got["geoip_available"])
	}
}

func TestPutSettings(t *testing.T) {
	e := newEnv(t)
	e.geo["203.0.113.7"] = "US"
	rec := e.call(t, http.MethodPut, "/security/settings", settingsBody(true, false, []string{"DE", "AT", "DE"}), callerIP)
	wantStatus(t, rec, http.StatusOK, "")
	got := decode[map[string]any](t, rec)
	if got["enforce_2fa_for_users"] != true || len(got["allowed_countries"].([]any)) != 2 || got["geoip_available"] != true {
		t.Fatalf("response = %v", got)
	}
	stored, err := store.LoadSecuritySettings(context.Background(), e.q)
	if err != nil || !stored.Enforce2FAForUsers || !slices.Equal(stored.AllowedCountries, []string{"DE", "AT"}) {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if !slices.Contains(e.audit.Actions(), audit.ActionSettingsUpdate) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
	if rec := e.filter("203.0.113.7", "/"); rec.Code != http.StatusForbidden {
		t.Fatalf("country filter must apply immediately, code=%d", rec.Code)
	}
}

func TestPutSettingsValidation(t *testing.T) {
	e := newEnv(t)
	cases := []any{
		`{"enforce_2fa_for_users":true,"admin_allowlist_only":false}`,
		`{"enforce_2fa_for_users":true,"admin_allowlist_only":false,"allowed_countries":null}`,
		`{"admin_allowlist_only":false,"allowed_countries":[]}`,
		settingsBody(false, false, []string{"de"}),
		settingsBody(false, false, []string{"DEU"}),
		settingsBody(false, false, []string{""}),
	}
	for i, body := range cases {
		rec := e.call(t, http.MethodPut, "/security/settings", body, callerIP)
		if rec.Code != http.StatusBadRequest || errCode(rec) != httpx.CodeValidation {
			t.Errorf("case %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	wantStatus(t, e.call(t, http.MethodPut, "/security/settings",
		`{"enforce_2fa_for_users":true,"admin_allowlist_only":false,"allowed_countries":[],"x":1}`, callerIP),
		http.StatusBadRequest, httpx.CodeBadRequest)
}

func TestPutSettingsRejectsSelfLockout(t *testing.T) {
	t.Run("admin allowlist without own rule", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, true, []string{}), callerIP),
			http.StatusConflict, httpx.CodeConflict)
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, true, []string{}), "127.0.0.1"),
			http.StatusOK, "")
	})
	t.Run("admin allowlist with own rule", func(t *testing.T) {
		e := newEnv(t)
		e.addRule(t, "198.51.100.0/24", "allow", nil)
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, true, []string{}), callerIP),
			http.StatusOK, "")
	})
	t.Run("country filter excluding caller", func(t *testing.T) {
		e := newEnv(t)
		e.geo[callerIP] = "US"
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, false, []string{"DE"}), callerIP),
			http.StatusConflict, httpx.CodeConflict)
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, false, []string{"DE", "US"}), callerIP),
			http.StatusOK, "")
		if got, _ := store.LoadSecuritySettings(context.Background(), e.q); len(got.AllowedCountries) != 2 {
			t.Fatalf("stored = %+v", got)
		}
	})
	t.Run("unknown caller country is not locked out", func(t *testing.T) {
		e := newEnv(t)
		wantStatus(t, e.call(t, http.MethodPut, "/security/settings", settingsBody(false, false, []string{"DE"}), callerIP),
			http.StatusOK, "")
	})
}
```

- [ ] **Step 2: Run** `go test ./internal/defense/ -run 'Settings'` → FAIL (404, Route fehlt).

- [ ] **Step 3: Implementation `internal/defense/settings_api.go`**

```go
package defense

import (
	"fmt"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

const maxAllowedCountries = 300

type settingsRequest struct {
	Enforce2FAForUsers *bool    `json:"enforce_2fa_for_users"`
	AdminAllowlistOnly *bool    `json:"admin_allowlist_only"`
	AllowedCountries   []string `json:"allowed_countries"`
}

type settingsResponse struct {
	Enforce2FAForUsers bool     `json:"enforce_2fa_for_users"`
	AdminAllowlistOnly bool     `json:"admin_allowlist_only"`
	AllowedCountries   []string `json:"allowed_countries"`
	GeoIPAvailable     bool     `json:"geoip_available"`
}

func (h *Handler) toSettingsResponse(st store.SecuritySettings) settingsResponse {
	countries := st.AllowedCountries
	if countries == nil {
		countries = []string{}
	}
	return settingsResponse{
		Enforce2FAForUsers: st.Enforce2FAForUsers, AdminAllowlistOnly: st.AdminAllowlistOnly,
		AllowedCountries: countries, GeoIPAvailable: h.s.GeoAvailable(),
	}
}

func settingsDetails(st store.SecuritySettings) map[string]any {
	return map[string]any{
		"enforce_2fa_for_users": st.Enforce2FAForUsers,
		"admin_allowlist_only":  st.AdminAllowlistOnly,
		"allowed_countries":     st.AllowedCountries,
	}
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := store.LoadSecuritySettings(r.Context(), h.s.q)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.toSettingsResponse(st))
}

func (h *Handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if req.Enforce2FAForUsers == nil || req.AdminAllowlistOnly == nil || req.AllowedCountries == nil {
		validation(w, r, "enforce_2fa_for_users, admin_allowlist_only and allowed_countries are required")
		return
	}
	if len(req.AllowedCountries) > maxAllowedCountries {
		validation(w, r, "too many allowed countries")
		return
	}
	countries := make([]string, 0, len(req.AllowedCountries))
	seen := make(map[string]bool, len(req.AllowedCountries))
	for _, c := range req.AllowedCountries {
		if !countryCodeRE.MatchString(c) {
			validation(w, r, fmt.Sprintf("invalid country code %q (expected ISO 3166-1 alpha-2 in upper case)", c))
			return
		}
		if !seen[c] {
			seen[c] = true
			countries = append(countries, c)
		}
	}
	st := store.SecuritySettings{
		Enforce2FAForUsers: *req.Enforce2FAForUsers,
		AdminAllowlistOnly: *req.AdminAllowlistOnly,
		AllowedCountries:   countries,
	}
	ctx := r.Context()
	now := h.s.clock.Now()
	if reason := h.s.snap.Load().withSettings(st).locksOut(httpx.ClientIP(ctx), now, h.s.geo); reason != "" {
		conflict(w, r, lockoutMessage(reason))
		return
	}
	before, err := store.LoadSecuritySettings(ctx, h.s.q)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if err := store.SaveSecuritySettings(ctx, h.s.q, st, now); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.s.reloadOrLog(ctx)
	h.record(ctx, audit.Entry{
		Action: audit.ActionSettingsUpdate, Target: "security",
		Details: map[string]any{"before": settingsDetails(before), "after": settingsDetails(st)},
	})
	httpx.WriteJSON(w, http.StatusOK, h.toSettingsResponse(st))
}
```

- [ ] **Step 4: `internal/defense/routes.go` ersetzen (Stand nach Task 10)**

```go
package defense

import "github.com/Bernotese/mc-panel/internal/rbac"

// Routes registers the security-center endpoints with full paths relative to /api.
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/security/jails", rbac.Admin(), h.listJails)
	api.Post("/security/jails", rbac.Admin().WithSudo(), h.createJail)
	api.Delete("/security/jails/{id}", rbac.Admin().WithSudo(), h.liftJail)
	api.Get("/security/ip-rules", rbac.Admin(), h.listIPRules)
	api.Post("/security/ip-rules", rbac.Admin().WithSudo(), h.createIPRule)
	api.Delete("/security/ip-rules/{id}", rbac.Admin().WithSudo(), h.deleteIPRule)
	api.Get("/security/settings", rbac.Admin(), h.getSettings)
	api.Put("/security/settings", rbac.Admin().WithSudo(), h.putSettings)
}
```

- [ ] **Step 5: Run** `go test ./internal/defense/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/defense/settings_api.go internal/defense/settings_api_test.go internal/defense/routes.go
git commit -m "feat(defense): add security settings endpoints with lockout check" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: Übersicht, Login-Versuche, Agent-Status und vollständige Routen-Tabelle

**Files:**
- Modify: `internal/store/queries/defense.sql` (anhängen), regeneriert `internal/store/defense.sql.go`; `internal/defense/routes.go` (ersetzen, Endstand)
- Create: `internal/defense/stats_api.go`
- Test: `internal/defense/stats_api_test.go`, `internal/defense/routes_test.go`

**Interfaces:**
- Consumes: `agentapi.Client.Health`, `agentapi.HealthResponse{Status, Version, Firewall}`, `(*rbac.Router).Routes() []rbac.RouteInfo`, `rbac.Rule.String()`, Task 7 (`DefenseCountPendingSync`, `fakeAgent`, `withAgent`), Task 8 (Helfer).
- Produces:
```go
func (h *Handler) overview(w http.ResponseWriter, r *http.Request)     // GET /security/overview?range=24h|7d|30d
func (h *Handler) attempts(w http.ResponseWriter, r *http.Request)     // GET /security/attempts?success=&ip=&cursor=&limit=
func (h *Handler) agentStatus(w http.ResponseWriter, r *http.Request)  // GET /security/agent
func seriesWindow(now time.Time, total, bucket time.Duration) (start, n, bucketMs int64)
func normalizeIPFilter(v string) (string, bool)
func (h *Handler) Routes(api *rbac.Router)   // final: 11 routes
// sqlc
func (q *Queries) DefenseAttemptSeries(ctx context.Context, arg DefenseAttemptSeriesParams) ([]DefenseAttemptSeriesRow, error)
func (q *Queries) DefenseTopFailingIPs(ctx context.Context, since int64) ([]DefenseTopFailingIPsRow, error)
func (q *Queries) DefenseTopFailingCountries(ctx context.Context, since int64) ([]DefenseTopFailingCountriesRow, error)
func (q *Queries) DefenseCountFailuresTotal(ctx context.Context, since int64) (int64, error)
func (q *Queries) DefenseCountActiveJails(ctx context.Context, now int64) (int64, error)
func (q *Queries) DefenseNewDeviceLogins(ctx context.Context, since int64) ([]DefenseNewDeviceLoginsRow, error)
func (q *Queries) DefenseListAttempts(ctx context.Context, arg DefenseListAttemptsParams) ([]LoginAttempt, error)
```
Definitionen (exakt):
- **Zeitraum/Buckets:** `24h` → 24 Buckets à 1 h; `7d` → 28 Buckets à 6 h; `30d` → 30 Buckets à 24 h. Buckets sind an Vielfachen der Bucket-Länge seit Unix-Epoche (UTC) ausgerichtet; der letzte Bucket enthält `now`. `start = floor(now/bucket)·bucket − (n−1)·bucket`; alle Kennzahlen zählen Versuche mit `ts >= start`. Fehlende Buckets werden mit 0 aufgefüllt; `t` = Bucket-Beginn (Unix-ms). Die Serie zählt alle Stufen (`password`, `totp`, `webauthn`, `recovery`, `sudo`).
- **Top-IPs/-Länder:** je 10, nach Fehlversuchen absteigend, bei Gleichstand nach `ip_key` bzw. Land aufsteigend; Länder ohne Code werden bei Top-Ländern ausgelassen.
- **`active_jails`:** nicht aufgehoben und `expires_at > now`.
- **`new_device_logins`:** Zeilen mit `success = 1 AND reason = 'complete'` (vollständige Anmeldung laut Stream-A-Vertrag) und `user_id` im Zeitraum, für die es keine frühere (kleinere `id`) solche Zeile mit gleichem `user_id` und `ip_key` gibt; neueste zuerst, max. 50.
- **Attempts-Cursor:** opak = Dezimal-`id` des letzten Elements; Seite = `id < cursor` absteigend; `next_cursor` nur, wenn weitere Elemente existieren (es wird `limit+1` gelesen). `limit` 1–200 (Standard 50). `ip`-Filter: einzelne IP (normalisiert, exakter Treffer auf `ip`) oder Präfix (maskiert, Treffer auf `ip_key` bzw. `ip`). Ungültige Query-Parameter → `400 bad_request`.
- **Agent-Status:** `Health` mit `2 s` Timeout; Fehler → `reachable=false`, `firewall=""`, `version=""`; `pending_sync` = `DefenseCountPendingSync(now)`. Immer `200`.

- [ ] **Step 1: Queries an `internal/store/queries/defense.sql` anhängen**

```sql
-- name: DefenseAttemptSeries :many
SELECT CAST((ts - sqlc.arg(start)) / sqlc.arg(bucket_ms) AS INTEGER) AS bucket,
       count(CASE WHEN success = 1 THEN 1 END) AS successes,
       count(CASE WHEN success = 0 THEN 1 END) AS failures
FROM login_attempts
WHERE ts >= sqlc.arg(since)
GROUP BY bucket
ORDER BY bucket;

-- name: DefenseTopFailingIPs :many
SELECT ip_key, CAST(MAX(country) AS TEXT) AS country, count(*) AS failures
FROM login_attempts
WHERE success = 0 AND ts >= sqlc.arg(since)
GROUP BY ip_key
ORDER BY failures DESC, ip_key
LIMIT 10;

-- name: DefenseTopFailingCountries :many
SELECT country, count(*) AS failures
FROM login_attempts
WHERE success = 0 AND ts >= sqlc.arg(since) AND country <> ''
GROUP BY country
ORDER BY failures DESC, country
LIMIT 10;

-- name: DefenseCountFailuresTotal :one
SELECT count(*) FROM login_attempts WHERE success = 0 AND ts >= sqlc.arg(since);

-- name: DefenseCountActiveJails :one
SELECT count(*) FROM jails WHERE lifted_at IS NULL AND expires_at > sqlc.arg(now);

-- name: DefenseNewDeviceLogins :many
SELECT a.ts, a.username, a.ip, a.country, a.user_agent
FROM login_attempts a
WHERE a.success = 1 AND a.reason = 'complete' AND a.user_id IS NOT NULL
  AND a.ts >= sqlc.arg(since)
  AND NOT EXISTS (
      SELECT 1 FROM login_attempts b
      WHERE b.user_id = a.user_id AND b.ip_key = a.ip_key AND b.success = 1
        AND b.reason = 'complete' AND b.id < a.id)
ORDER BY a.ts DESC, a.id DESC
LIMIT 50;

-- name: DefenseListAttempts :many
SELECT * FROM login_attempts
WHERE id < sqlc.arg(before_id)
  AND (sqlc.arg(any_success) = 1 OR success = sqlc.arg(success))
  AND (sqlc.arg(any_ip) = 1 OR ip = sqlc.arg(ip) OR ip_key = sqlc.arg(ip_key))
ORDER BY id DESC
LIMIT sqlc.arg(lim);
```

```bash
sqlc generate && go build ./internal/store/ && grep -n "type DefenseAttemptSeriesRow\|type DefenseListAttemptsParams" -A 8 internal/store/defense.sql.go
```
Expected: `DefenseAttemptSeriesRow{Bucket int64; Successes int64; Failures int64}`, `DefenseListAttemptsParams{BeforeID int64; AnySuccess …; Success int64; AnyIp …; Ip string; IpKey string; Lim int64}` (`AnySuccess`/`AnyIp` ggf. `interface{}`).

- [ ] **Step 2: Failing test `internal/defense/stats_api_test.go`**

```go
package defense

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

type overviewJSON struct {
	Range  string `json:"range"`
	Series []struct {
		T       int64 `json:"t"`
		Success int64 `json:"success"`
		Failure int64 `json:"failure"`
	} `json:"series"`
	TopIPs []struct {
		IPKey    string `json:"ip_key"`
		Country  string `json:"country"`
		Failures int64  `json:"failures"`
	} `json:"top_ips"`
	TopCountries []struct {
		Country  string `json:"country"`
		Failures int64  `json:"failures"`
	} `json:"top_countries"`
	ActiveJails     int64 `json:"active_jails"`
	FailuresTotal   int64 `json:"failures_total"`
	NewDeviceLogins []struct {
		Ts        int64  `json:"ts"`
		Username  string `json:"username"`
		IP        string `json:"ip"`
		Country   string `json:"country"`
		UserAgent string `json:"user_agent"`
	} `json:"new_device_logins"`
}

func TestOverview24h(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.geo["203.0.113.7"] = "US"
	e.geo["198.51.100.1"] = "DE"
	uid := storetest.CreateUser(t, e.db, "alice", "user")

	e.clk.Set(t0.Add(-25 * time.Hour))
	e.fail(t, "203.0.113.9", 1) // outside the range
	e.clk.Set(t0.Add(-30 * time.Minute))
	e.fail(t, "203.0.113.7", 3)
	e.fail(t, "198.51.100.1", 1)
	e.clk.Set(t0)
	for i := 0; i < 2; i++ {
		if err := e.svc.RecordAttempt(ctx, Attempt{IP: addr("198.51.100.1"), Username: "alice", UserID: &uid,
			Stage: StageTOTP, Success: true, Reason: ReasonComplete, UserAgent: "Firefox"}); err != nil {
			t.Fatal(err)
		}
	}
	e.insertJail(t, "192.0.2.1/32", t0, t0.Add(time.Hour))

	rec := e.call(t, http.MethodGet, "/security/overview", nil, callerIP)
	wantStatus(t, rec, http.StatusOK, "")
	type point struct{ T, Success, Failure int64 }
	got := decode[overviewJSON](t, rec)
	start := t0.Add(-23 * time.Hour).UnixMilli()
	if got.Range != "24h" || len(got.Series) != 24 || got.Series[0].T != start ||
		got.Series[23].T != t0.UnixMilli() {
		t.Fatalf("range/series = %q len=%d first=%d", got.Range, len(got.Series), got.Series[0].T)
	}
	if p := got.Series[22]; (point{p.T, p.Success, p.Failure}) != (point{t0.Add(-time.Hour).UnixMilli(), 0, 4}) {
		t.Fatalf("series[22] = %+v", p)
	}
	if p := got.Series[23]; p.Success != 2 || p.Failure != 0 {
		t.Fatalf("series[23] = %+v", p)
	}
	if got.FailuresTotal != 4 || got.ActiveJails != 1 {
		t.Fatalf("failures_total=%d active_jails=%d", got.FailuresTotal, got.ActiveJails)
	}
	if len(got.TopIPs) != 2 || got.TopIPs[0].IPKey != "203.0.113.7/32" || got.TopIPs[0].Country != "US" ||
		got.TopIPs[0].Failures != 3 || got.TopIPs[1].IPKey != "198.51.100.1/32" {
		t.Fatalf("top_ips = %+v", got.TopIPs)
	}
	if len(got.TopCountries) != 2 || got.TopCountries[0].Country != "US" || got.TopCountries[1].Failures != 1 {
		t.Fatalf("top_countries = %+v", got.TopCountries)
	}
	if len(got.NewDeviceLogins) != 1 || got.NewDeviceLogins[0].Username != "alice" ||
		got.NewDeviceLogins[0].IP != "198.51.100.1" || got.NewDeviceLogins[0].Country != "DE" ||
		got.NewDeviceLogins[0].UserAgent != "Firefox" {
		t.Fatalf("new_device_logins = %+v", got.NewDeviceLogins)
	}
}

func TestOverviewRanges(t *testing.T) {
	e := newEnv(t)
	for rng, n := range map[string]int{"7d": 28, "30d": 30} {
		got := decode[map[string]any](t, e.call(t, http.MethodGet, "/security/overview?range="+rng, nil, callerIP))
		series, _ := got["series"].([]any)
		if got["range"] != rng || len(series) != n {
			t.Errorf("%s: range=%v len=%d", rng, got["range"], len(series))
		}
		for _, k := range []string{"top_ips", "top_countries", "new_device_logins"} {
			if arr, ok := got[k].([]any); !ok || arr == nil {
				t.Errorf("%s: %s must be an array, got %#v", rng, k, got[k])
			}
		}
	}
	wantStatus(t, e.call(t, http.MethodGet, "/security/overview?range=1y", nil, callerIP), http.StatusBadRequest, httpx.CodeBadRequest)
}

func TestSeriesWindow(t *testing.T) {
	now := time.Date(2026, 10, 8, 14, 37, 0, 0, time.UTC)
	start, n, bucket := seriesWindow(now, 7*24*time.Hour, 6*time.Hour)
	wantStart := time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC).UnixMilli()
	if n != 28 || bucket != (6*time.Hour).Milliseconds() || start != wantStart {
		t.Fatalf("start=%v n=%d bucket=%d", time.UnixMilli(start).UTC(), n, bucket)
	}
}

func TestListAttempts(t *testing.T) {
	e := newEnv(t)
	e.fail(t, "203.0.113.7", 3)     // ids 1-3
	e.fail(t, "2001:db8:1:2::1", 2) // ids 4-5
	if err := e.svc.RecordAttempt(context.Background(), Attempt{IP: addr("198.51.100.1"), Username: "bob",
		Stage: StagePassword, Success: true}); err != nil { // id 6
		t.Fatal(err)
	}
	type page struct {
		Items []struct {
			ID      int64  `json:"id"`
			IP      string `json:"ip"`
			Stage   string `json:"stage"`
			Success bool   `json:"success"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	ids := func(p page) []int64 {
		out := []int64{}
		for _, it := range p.Items {
			out = append(out, it.ID)
		}
		return out
	}
	p1 := decode[page](t, e.call(t, http.MethodGet, "/security/attempts?limit=4", nil, callerIP))
	if got := ids(p1); len(got) != 4 || got[0] != 6 || got[3] != 3 || p1.NextCursor == nil || *p1.NextCursor != "3" {
		t.Fatalf("page 1 = %v next=%v", got, p1.NextCursor)
	}
	if !p1.Items[0].Success || p1.Items[0].Stage != "password" || p1.Items[0].IP != "198.51.100.1" {
		t.Fatalf("item = %+v", p1.Items[0])
	}
	p2 := decode[page](t, e.call(t, http.MethodGet, "/security/attempts?limit=4&cursor=3", nil, callerIP))
	if got := ids(p2); len(got) != 2 || got[0] != 2 || got[1] != 1 || p2.NextCursor != nil {
		t.Fatalf("page 2 = %v next=%v", got, p2.NextCursor)
	}
	for q, want := range map[string]int{
		"success=true":                 1,
		"success=false":                5,
		"ip=203.0.113.7":               3,
		"ip=::ffff:203.0.113.7":        3,
		"ip=2001:db8:1:2::/64":         2,
		"ip=2001:db8:1:2::1&success=0": 2,
	} {
		if got := decode[page](t, e.call(t, http.MethodGet, "/security/attempts?"+q, nil, callerIP)); len(got.Items) != want {
			t.Errorf("%s: %d items, want %d", q, len(got.Items), want)
		}
	}
	for _, q := range []string{"limit=0", "limit=201", "cursor=abc", "cursor=0", "success=maybe", "ip=nope"} {
		wantStatus(t, e.call(t, http.MethodGet, "/security/attempts?"+q, nil, callerIP), http.StatusBadRequest, httpx.CodeBadRequest)
	}
}

func TestAgentStatus(t *testing.T) {
	t.Run("no agent configured", func(t *testing.T) {
		e := newEnv(t)
		e.insertJail(t, "203.0.113.7/32", t0, t0.Add(time.Hour))
		got := decode[map[string]any](t, e.call(t, http.MethodGet, "/security/agent", nil, callerIP))
		if got["reachable"] != false || got["firewall"] != "" || got["version"] != "" || got["pending_sync"] != float64(1) {
			t.Fatalf("status = %v", got)
		}
	})
	t.Run("healthy agent", func(t *testing.T) {
		fa := newFakeAgent()
		e := newEnv(t, withAgent(fa))
		got := decode[map[string]any](t, e.call(t, http.MethodGet, "/security/agent", nil, callerIP))
		if got["reachable"] != true || got["firewall"] != "noop" || got["version"] != "test" || got["pending_sync"] != float64(0) {
			t.Fatalf("status = %v", got)
		}
	})
	t.Run("agent down", func(t *testing.T) {
		fa := newFakeAgent()
		fa.setErr(errAgentDown)
		e := newEnv(t, withAgent(fa))
		rec := e.call(t, http.MethodGet, "/security/agent", nil, callerIP)
		wantStatus(t, rec, http.StatusOK, "")
		if got := decode[map[string]any](t, rec); got["reachable"] != false {
			t.Fatalf("status = %v", got)
		}
	})
}
```

- [ ] **Step 3: Failing test `internal/defense/routes_test.go`**

```go
package defense

import (
	"maps"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/rbac"
)

// TestRoutesDeclareRules pins every security route to the rule from the OpenAPI table.
func TestRoutesDeclareRules(t *testing.T) {
	e := newEnv(t)
	rt := rbac.NewRouter(chi.NewRouter(), denyChecker{}, e.clk)
	NewHandler(e.svc).Routes(rt)
	const admin, adminSudo = "admin[full]", "admin+sudo[full]"
	want := map[string]string{
		"GET /security/overview":         admin,
		"GET /security/attempts":         admin,
		"GET /security/jails":            admin,
		"POST /security/jails":           adminSudo,
		"DELETE /security/jails/{id}":    adminSudo,
		"GET /security/ip-rules":         admin,
		"POST /security/ip-rules":        adminSudo,
		"DELETE /security/ip-rules/{id}": adminSudo,
		"GET /security/settings":         admin,
		"PUT /security/settings":         adminSudo,
		"GET /security/agent":            admin,
	}
	got := map[string]string{}
	for _, ri := range rt.Routes() {
		got[ri.Method+" "+ri.Pattern] = ri.Rule.String()
	}
	if !maps.Equal(got, want) {
		t.Fatalf("routes =\n%v\nwant\n%v", got, want)
	}
}
```

- [ ] **Step 4: Run** `go test ./internal/defense/ -run 'Overview|SeriesWindow|ListAttempts|AgentStatus|RoutesDeclareRules'` → FAIL (`undefined: seriesWindow`, Routen fehlen).

- [ ] **Step 5: Implementation `internal/defense/stats_api.go`**

```go
package defense

import (
	"context"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/store"
)

type rangeSpec struct{ total, bucket time.Duration }

var overviewRanges = map[string]rangeSpec{
	"24h": {24 * time.Hour, time.Hour},
	"7d":  {7 * 24 * time.Hour, 6 * time.Hour},
	"30d": {30 * 24 * time.Hour, 24 * time.Hour},
}

const (
	defaultAttemptLimit = 50
	maxAttemptLimit     = 200
)

type seriesPoint struct {
	T       int64 `json:"t"`
	Success int64 `json:"success"`
	Failure int64 `json:"failure"`
}

type ipCount struct {
	IPKey    string `json:"ip_key"`
	Country  string `json:"country"`
	Failures int64  `json:"failures"`
}

type countryCount struct {
	Country  string `json:"country"`
	Failures int64  `json:"failures"`
}

type newDeviceLogin struct {
	Ts        int64  `json:"ts"`
	Username  string `json:"username"`
	IP        string `json:"ip"`
	Country   string `json:"country"`
	UserAgent string `json:"user_agent"`
}

type overviewResponse struct {
	Range           string           `json:"range"`
	Series          []seriesPoint    `json:"series"`
	TopIPs          []ipCount        `json:"top_ips"`
	TopCountries    []countryCount   `json:"top_countries"`
	ActiveJails     int64            `json:"active_jails"`
	FailuresTotal   int64            `json:"failures_total"`
	NewDeviceLogins []newDeviceLogin `json:"new_device_logins"`
}

// seriesWindow returns the first bucket start (Unix ms), the number of buckets
// and the bucket length (ms); buckets are aligned to the Unix epoch and the last one contains now.
func seriesWindow(now time.Time, total, bucket time.Duration) (start, n, bucketMs int64) {
	bucketMs = bucket.Milliseconds()
	n = int64(total / bucket)
	start = (now.UnixMilli()/bucketMs)*bucketMs - (n-1)*bucketMs
	return start, n, bucketMs
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "24h"
	}
	spec, ok := overviewRanges[rng]
	if !ok {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "range must be 24h, 7d or 30d")
		return
	}
	resp, err := h.buildOverview(r.Context(), rng, spec)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) buildOverview(ctx context.Context, rng string, spec rangeSpec) (overviewResponse, error) {
	now := h.s.clock.Now()
	start, n, bucketMs := seriesWindow(now, spec.total, spec.bucket)
	resp := overviewResponse{
		Range: rng, Series: make([]seriesPoint, n), TopIPs: []ipCount{},
		TopCountries: []countryCount{}, NewDeviceLogins: []newDeviceLogin{},
	}
	for i := range resp.Series {
		resp.Series[i].T = start + int64(i)*bucketMs
	}
	rows, err := h.s.q.DefenseAttemptSeries(ctx, store.DefenseAttemptSeriesParams{Start: start, BucketMs: bucketMs, Since: start})
	if err != nil {
		return resp, err
	}
	for _, row := range rows {
		if row.Bucket >= 0 && row.Bucket < n {
			resp.Series[row.Bucket].Success = row.Successes
			resp.Series[row.Bucket].Failure = row.Failures
		}
	}
	ips, err := h.s.q.DefenseTopFailingIPs(ctx, start)
	if err != nil {
		return resp, err
	}
	for _, row := range ips {
		resp.TopIPs = append(resp.TopIPs, ipCount{IPKey: row.IpKey, Country: row.Country, Failures: row.Failures})
	}
	countries, err := h.s.q.DefenseTopFailingCountries(ctx, start)
	if err != nil {
		return resp, err
	}
	for _, row := range countries {
		resp.TopCountries = append(resp.TopCountries, countryCount{Country: row.Country, Failures: row.Failures})
	}
	if resp.FailuresTotal, err = h.s.q.DefenseCountFailuresTotal(ctx, start); err != nil {
		return resp, err
	}
	if resp.ActiveJails, err = h.s.q.DefenseCountActiveJails(ctx, now.UnixMilli()); err != nil {
		return resp, err
	}
	devices, err := h.s.q.DefenseNewDeviceLogins(ctx, start)
	if err != nil {
		return resp, err
	}
	for _, row := range devices {
		resp.NewDeviceLogins = append(resp.NewDeviceLogins, newDeviceLogin{
			Ts: row.Ts, Username: row.Username, IP: row.Ip, Country: row.Country, UserAgent: row.UserAgent,
		})
	}
	return resp, nil
}

type attemptResponse struct {
	ID        int64  `json:"id"`
	Ts        int64  `json:"ts"`
	IP        string `json:"ip"`
	Username  string `json:"username"`
	Stage     string `json:"stage"`
	Success   bool   `json:"success"`
	Reason    string `json:"reason"`
	Country   string `json:"country"`
	UserAgent string `json:"user_agent"`
}

type attemptPage struct {
	Items      []attemptResponse `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

// normalizeIPFilter accepts a single IP (→ unmapped string) or a prefix (→ masked string).
func normalizeIPFilter(v string) (string, bool) {
	if a, err := netip.ParseAddr(v); err == nil {
		return normalizeIP(a).String(), true
	}
	if p, err := netip.ParsePrefix(v); err == nil && !p.Addr().Is4In6() {
		return p.Masked().String(), true
	}
	return "", false
}

func (h *Handler) attempts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	bad := func(msg string) { httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, msg) }
	limit := int64(defaultAttemptLimit)
	if v := q.Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > maxAttemptLimit {
			bad("limit must be between 1 and 200")
			return
		}
		limit = n
	}
	before := int64(math.MaxInt64)
	if v := q.Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			bad("invalid cursor")
			return
		}
		before = n
	}
	anySuccess, success := int64(1), int64(0)
	if v := q.Get("success"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			bad("success must be true or false")
			return
		}
		anySuccess, success = 0, boolInt(b)
	}
	anyIP, ipFilter := int64(1), ""
	if v := strings.TrimSpace(q.Get("ip")); v != "" {
		f, ok := normalizeIPFilter(v)
		if !ok {
			bad("ip must be an IP address or prefix")
			return
		}
		anyIP, ipFilter = 0, f
	}
	rows, err := h.s.q.DefenseListAttempts(r.Context(), store.DefenseListAttemptsParams{
		BeforeID: before, AnySuccess: anySuccess, Success: success,
		AnyIp: anyIP, Ip: ipFilter, IpKey: ipFilter, Lim: limit + 1,
	})
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	page := attemptPage{Items: make([]attemptResponse, 0, len(rows))}
	if int64(len(rows)) > limit {
		rows = rows[:limit]
		c := strconv.FormatInt(rows[len(rows)-1].ID, 10)
		page.NextCursor = &c
	}
	for _, a := range rows {
		page.Items = append(page.Items, attemptResponse{
			ID: a.ID, Ts: a.Ts, IP: a.Ip, Username: a.Username, Stage: a.Stage, Success: a.Success != 0,
			Reason: a.Reason, Country: a.Country, UserAgent: a.UserAgent,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

type agentStatusResponse struct {
	Reachable   bool   `json:"reachable"`
	Firewall    string `json:"firewall"`
	Version     string `json:"version"`
	PendingSync int64  `json:"pending_sync"`
}

func (h *Handler) agentStatus(w http.ResponseWriter, r *http.Request) {
	var resp agentStatusResponse
	if h.s.agent != nil {
		ctx, cancel := context.WithTimeout(r.Context(), AgentStatusTimeout)
		hr, err := h.s.agent.Health(ctx)
		cancel()
		if err == nil {
			resp.Reachable, resp.Firewall, resp.Version = true, hr.Firewall, hr.Version
		}
	}
	n, err := h.s.q.DefenseCountPendingSync(r.Context(), h.s.clock.Now().UnixMilli())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	resp.PendingSync = n
	httpx.WriteJSON(w, http.StatusOK, resp)
}
```

- [ ] **Step 6: `internal/defense/routes.go` ersetzen (Endstand)**

```go
package defense

import "github.com/Bernotese/mc-panel/internal/rbac"

// Routes registers the security-center endpoints with full paths relative to /api.
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/security/overview", rbac.Admin(), h.overview)
	api.Get("/security/attempts", rbac.Admin(), h.attempts)
	api.Get("/security/jails", rbac.Admin(), h.listJails)
	api.Post("/security/jails", rbac.Admin().WithSudo(), h.createJail)
	api.Delete("/security/jails/{id}", rbac.Admin().WithSudo(), h.liftJail)
	api.Get("/security/ip-rules", rbac.Admin(), h.listIPRules)
	api.Post("/security/ip-rules", rbac.Admin().WithSudo(), h.createIPRule)
	api.Delete("/security/ip-rules/{id}", rbac.Admin().WithSudo(), h.deleteIPRule)
	api.Get("/security/settings", rbac.Admin(), h.getSettings)
	api.Put("/security/settings", rbac.Admin().WithSudo(), h.putSettings)
	api.Get("/security/agent", rbac.Admin(), h.agentStatus)
}
```

- [ ] **Step 7: Run** `go test ./internal/defense/` → PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/store/queries/defense.sql internal/store/defense.sql.go internal/defense/stats_api.go internal/defense/stats_api_test.go internal/defense/routes.go internal/defense/routes_test.go
git commit -m "feat(defense): add security overview, login attempts and agent status endpoints" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: Notfall-Entsperrung `Unlock` (CLI `panel admin unlock`)

**Files:**
- Modify: `internal/store/queries/defense.sql` (anhängen), regeneriert `internal/store/defense.sql.go`
- Create: `internal/defense/unlock.go`
- Test: `internal/defense/unlock_test.go`

**Interfaces:**
- Consumes: `store.LoadSecuritySettings`, `store.SaveSecuritySettings`, `audit.Recorder`, `audit.ActorCLI`, `audit.ActionJailLift`, `audit.ActionSettingsUpdate`, `clock.Clock`, Task 7 (`msNull`), Task 10 (`settingsDetails`).
- Produces:
```go
type UnlockResult struct{ JailsLifted int64; AdminAllowlistDisabled, CountryFilterCleared bool }
func Unlock(ctx context.Context, db *sql.DB, rec audit.Recorder, c clock.Clock, out io.Writer) (UnlockResult, error)
// sqlc
func (q *Queries) DefenseLiftAllActiveJails(ctx context.Context, arg DefenseLiftAllActiveJailsParams) (int64, error)
```
Wirkung: hebt alle aktiven Jails auf (`lifted_by = "cli"`), setzt `admin_allowlist_only = false` und leert `allowed_countries`; `enforce_2fa_for_users` und IP-Regeln bleiben unverändert. Audit (nach den Schreibvorgängen) mit `ActorType: audit.ActorCLI`, `ActorName: "cli"`: `ActionJailLift` (Target `all`, nur wenn ≥ 1 Jail) und `ActionSettingsUpdate` (nur wenn sich etwas geändert hat). Ein laufendes panel übernimmt die Änderungen beim nächsten Snapshot-Refresh (≤ 30 s); der Firewall-Sync entfernt die Bans.

- [ ] **Step 1: Query an `internal/store/queries/defense.sql` anhängen**

```sql
-- name: DefenseLiftAllActiveJails :execrows
UPDATE jails SET lifted_at = sqlc.arg(lifted_at), lifted_by = sqlc.arg(lifted_by)
WHERE lifted_at IS NULL AND expires_at > sqlc.arg(now);
```

```bash
sqlc generate && go build ./internal/store/
```
Expected: `DefenseLiftAllActiveJailsParams{LiftedAt sql.NullInt64; LiftedBy sql.NullString; Now int64}`.

- [ ] **Step 2: Failing test `internal/defense/unlock_test.go`**

```go
package defense

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store"
)

func TestUnlock(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.insertJail(t, "203.0.113.1/32", t0, t0.Add(time.Hour))
	e.insertJail(t, "2001:db8:1:2::/64", t0, t0.Add(time.Hour))
	e.insertJail(t, "203.0.113.2/32", t0.Add(-2*time.Hour), t0.Add(-time.Hour))
	e.saveSettings(t, store.SecuritySettings{Enforce2FAForUsers: true, AdminAllowlistOnly: true, AllowedCountries: []string{"DE", "AT"}})
	rec := &audit.MemoryRecorder{}
	var out bytes.Buffer

	res, err := Unlock(ctx, e.db, rec, e.clk, &out)
	if err != nil {
		t.Fatal(err)
	}
	if res != (UnlockResult{JailsLifted: 2, AdminAllowlistDisabled: true, CountryFilterCleared: true}) {
		t.Fatalf("result = %+v", res)
	}
	if js := e.activeJails(t); len(js) != 0 {
		t.Fatalf("active jails left: %+v", js)
	}
	var liftedBy string
	if err := e.db.QueryRow("SELECT lifted_by FROM jails WHERE ip_key = '203.0.113.1/32'").Scan(&liftedBy); err != nil || liftedBy != "cli" {
		t.Fatalf("lifted_by = %q, %v", liftedBy, err)
	}
	st, err := store.LoadSecuritySettings(ctx, e.q)
	if err != nil || st.AdminAllowlistOnly || len(st.AllowedCountries) != 0 || !st.Enforce2FAForUsers {
		t.Fatalf("settings = %+v, %v", st, err)
	}
	if len(rec.Entries) != 2 {
		t.Fatalf("audit = %+v", rec.Entries)
	}
	for _, en := range rec.Entries {
		if en.ActorType != audit.ActorCLI || en.ActorName != "cli" || en.Result != audit.ResultSuccess {
			t.Fatalf("audit entry = %+v", en)
		}
	}
	if rec.Entries[0].Action != audit.ActionJailLift || rec.Entries[1].Action != audit.ActionSettingsUpdate {
		t.Fatalf("actions = %v", rec.Actions())
	}
	for _, want := range []string{"Lifted 2 active jail(s).", "Disabled", "Cleared country filter (was: DE, AT).", "Deny rules were not changed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q missing %q", out.String(), want)
		}
	}
}

func TestUnlockNothingToDo(t *testing.T) {
	e := newEnv(t)
	rec := &audit.MemoryRecorder{}
	var out bytes.Buffer
	res, err := Unlock(context.Background(), e.db, rec, e.clk, &out)
	if err != nil || res != (UnlockResult{}) {
		t.Fatalf("res = %+v, %v", res, err)
	}
	if len(rec.Entries) != 0 {
		t.Fatalf("no audit entries expected: %+v", rec.Entries)
	}
	if !strings.Contains(out.String(), "Lifted 0 active jail(s).") {
		t.Fatalf("output = %q", out.String())
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/defense/ -run Unlock` → FAIL (`undefined: Unlock`).

- [ ] **Step 4: Implementation `internal/defense/unlock.go`**

```go
package defense

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/store"
)

// UnlockResult summarizes what Unlock changed.
type UnlockResult struct {
	JailsLifted            int64
	AdminAllowlistDisabled bool
	CountryFilterCleared   bool
}

// Unlock is the emergency escape hatch behind `panel admin unlock`: it lifts all
// active jails, disables "admin login only from allowlist" and clears the
// country filter. It works directly on the database (the panel may be stopped).
func Unlock(ctx context.Context, db *sql.DB, rec audit.Recorder, c clock.Clock, out io.Writer) (UnlockResult, error) {
	q := store.New(db)
	now := c.Now()
	var res UnlockResult
	n, err := q.DefenseLiftAllActiveJails(ctx, store.DefenseLiftAllActiveJailsParams{
		LiftedAt: msNull(now), LiftedBy: sql.NullString{String: "cli", Valid: true}, Now: now.UnixMilli(),
	})
	if err != nil {
		return res, fmt.Errorf("unlock: lift jails: %w", err)
	}
	res.JailsLifted = n

	st, err := store.LoadSecuritySettings(ctx, q)
	if err != nil {
		return res, fmt.Errorf("unlock: load settings: %w", err)
	}
	before := st
	before.AllowedCountries = slices.Clone(st.AllowedCountries)
	res.AdminAllowlistDisabled = st.AdminAllowlistOnly
	res.CountryFilterCleared = len(st.AllowedCountries) > 0
	changed := res.AdminAllowlistDisabled || res.CountryFilterCleared
	if changed {
		st.AdminAllowlistOnly = false
		st.AllowedCountries = []string{}
		if err := store.SaveSecuritySettings(ctx, q, st, now); err != nil {
			return res, fmt.Errorf("unlock: save settings: %w", err)
		}
	}

	entry := func(action, target string, details map[string]any) audit.Entry {
		return audit.Entry{
			Action: action, Target: target, Result: audit.ResultSuccess,
			ActorType: audit.ActorCLI, ActorName: "cli", Details: details,
		}
	}
	if n > 0 {
		if err := rec.Record(ctx, entry(audit.ActionJailLift, "all", map[string]any{"count": n})); err != nil {
			return res, fmt.Errorf("unlock: audit: %w", err)
		}
	}
	if changed {
		details := map[string]any{"before": settingsDetails(before), "after": settingsDetails(st)}
		if err := rec.Record(ctx, entry(audit.ActionSettingsUpdate, "security", details)); err != nil {
			return res, fmt.Errorf("unlock: audit: %w", err)
		}
	}

	fmt.Fprintf(out, "Lifted %d active jail(s).\n", n)
	if res.AdminAllowlistDisabled {
		fmt.Fprintln(out, `Disabled "admin login only from allowlist".`)
	} else {
		fmt.Fprintln(out, `"Admin login only from allowlist" was already disabled.`)
	}
	if res.CountryFilterCleared {
		fmt.Fprintf(out, "Cleared country filter (was: %s).\n", strings.Join(before.AllowedCountries, ", "))
	} else {
		fmt.Fprintln(out, "Country filter was already empty.")
	}
	fmt.Fprintln(out, "Deny rules were not changed. A running panel applies these changes within 30 seconds; firewall bans are removed by the next sync.")
	return res, nil
}
```

- [ ] **Step 5: Run** `go test ./internal/defense/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/queries/defense.sql internal/store/defense.sql.go internal/defense/unlock.go internal/defense/unlock_test.go
git commit -m "feat(defense): add emergency unlock for panel admin unlock" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13: Notifier-Konfiguration – Validierung, Maskierung, Verschlüsselung

**Files:**
- Create: `internal/notify/config.go`
- Test: `internal/notify/config_test.go`

**Interfaces:**
- Consumes: `secretbox.New([]byte) (*secretbox.Box, error)`, `(*secretbox.Box).Seal(plaintext, aad []byte) ([]byte, error)`, `(*secretbox.Box).Open(ciphertext, aad []byte) ([]byte, error)`, `secretbox.ErrInvalid`, `secretbox.KeySize`; AAD-Konvention `"notifier:" + kind`.
- Produces:
```go
type Kind string
const KindDiscord Kind = "discord"; const KindNtfy Kind = "ntfy"
func (k Kind) Valid() bool
type Config struct{ URL string `json:"url"`; Topic string `json:"topic,omitempty"`; Token string `json:"token,omitempty"` }
func ValidateConfig(kind Kind, c Config, dev bool) error     // error message is safe to show to admins (no secrets)
func MaskTarget(kind Kind, c Config) string
func SealConfig(box *secretbox.Box, kind Kind, c Config) ([]byte, error)
func OpenConfig(box *secretbox.Box, kind Kind, enc []byte) (Config, error)
```
Maskierung (exakt): Discord `scheme://host/api/webhooks/<erste 4 Zeichen der ID>…/****`; ntfy `scheme://host[/pfad]/<erste min(3, len/2) Zeichen des Topics>****` plus ` (token)`, falls ein Token gesetzt ist.

- [ ] **Step 1: Failing test `internal/notify/config_test.go`**

```go
package notify

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/secretbox"
)

const testWebhook = "https://discord.com/api/webhooks/123456789012345678/SuperSecretToken_abc-XYZ"

func TestValidateConfig(t *testing.T) {
	cases := []struct {
		name  string
		kind  Kind
		cfg   Config
		dev   bool
		valid bool
	}{
		{"discord ok", KindDiscord, Config{URL: testWebhook}, false, true},
		{"discordapp ok", KindDiscord, Config{URL: "https://discordapp.com/api/webhooks/1/abc"}, false, true},
		{"discord wrong host", KindDiscord, Config{URL: "https://evil.example/api/webhooks/1/abc"}, false, false},
		{"discord subdomain", KindDiscord, Config{URL: "https://discord.com.evil.example/api/webhooks/1/abc"}, false, false},
		{"discord wrong path", KindDiscord, Config{URL: "https://discord.com/api/other/1/abc"}, false, false},
		{"discord non-numeric id", KindDiscord, Config{URL: "https://discord.com/api/webhooks/abc/def"}, false, false},
		{"discord missing token", KindDiscord, Config{URL: "https://discord.com/api/webhooks/1"}, false, false},
		{"discord with port", KindDiscord, Config{URL: "https://discord.com:8443/api/webhooks/1/abc"}, false, false},
		{"discord with topic", KindDiscord, Config{URL: testWebhook, Topic: "x"}, false, false},
		{"discord http in prod", KindDiscord, Config{URL: "http://discord.com/api/webhooks/1/abc"}, false, false},
		{"query not allowed", KindDiscord, Config{URL: testWebhook + "?wait=true"}, false, false},
		{"fragment not allowed", KindDiscord, Config{URL: testWebhook + "#x"}, false, false},
		{"credentials not allowed", KindNtfy, Config{URL: "https://user:pw@ntfy.sh", Topic: "alerts"}, false, false},
		{"ntfy ok", KindNtfy, Config{URL: "https://ntfy.sh", Topic: "mc-alerts_1", Token: "tk_abc"}, false, true},
		{"ntfy with path", KindNtfy, Config{URL: "https://example.org/ntfy", Topic: "alerts"}, false, true},
		{"ntfy missing topic", KindNtfy, Config{URL: "https://ntfy.sh"}, false, false},
		{"ntfy bad topic", KindNtfy, Config{URL: "https://ntfy.sh", Topic: "a/b"}, false, false},
		{"ntfy token with space", KindNtfy, Config{URL: "https://ntfy.sh", Topic: "a", Token: "a b"}, false, false},
		{"ntfy token too long", KindNtfy, Config{URL: "https://ntfy.sh", Topic: "a", Token: strings.Repeat("t", 257)}, false, false},
		{"ntfy localhost in prod", KindNtfy, Config{URL: "https://localhost:8080", Topic: "a"}, false, false},
		{"ntfy private ip in prod", KindNtfy, Config{URL: "https://10.0.0.5", Topic: "a"}, false, false},
		{"ntfy loopback v6 in prod", KindNtfy, Config{URL: "https://[::1]:2586", Topic: "a"}, false, false},
		{"ntfy http localhost in dev", KindNtfy, Config{URL: "http://127.0.0.1:2586", Topic: "a"}, true, true},
		{"ntfy http in prod", KindNtfy, Config{URL: "http://ntfy.sh", Topic: "a"}, false, false},
		{"relative url", KindNtfy, Config{URL: "/ntfy", Topic: "a"}, false, false},
		{"ftp scheme", KindNtfy, Config{URL: "ftp://ntfy.sh", Topic: "a"}, true, false},
		{"too long", KindNtfy, Config{URL: "https://ntfy.sh/" + strings.Repeat("a", 2048), Topic: "a"}, false, false},
		{"unknown kind", Kind("slack"), Config{URL: "https://hooks.slack.com/x"}, false, false},
	}
	for _, tc := range cases {
		err := ValidateConfig(tc.kind, tc.cfg, tc.dev)
		if (err == nil) != tc.valid {
			t.Errorf("%s: err = %v, valid want %v", tc.name, err, tc.valid)
		}
		if err != nil && (strings.Contains(err.Error(), "SuperSecretToken") || strings.Contains(err.Error(), "tk_abc")) {
			t.Errorf("%s: error leaks secret: %v", tc.name, err)
		}
	}
}

func TestMaskTargetHidesSecrets(t *testing.T) {
	d := MaskTarget(KindDiscord, Config{URL: testWebhook})
	if d != "https://discord.com/api/webhooks/1234…/****" || strings.Contains(d, "SuperSecret") {
		t.Fatalf("discord mask = %q", d)
	}
	n := MaskTarget(KindNtfy, Config{URL: "https://ntfy.sh", Topic: "mcpanel-alerts-x7", Token: "tk_secret"})
	if n != "https://ntfy.sh/mcp**** (token)" || strings.Contains(n, "alerts") || strings.Contains(n, "tk_secret") {
		t.Fatalf("ntfy mask = %q", n)
	}
	short := MaskTarget(KindNtfy, Config{URL: "https://example.org/ntfy/", Topic: "abcd"})
	if short != "https://example.org/ntfy/ab****" {
		t.Fatalf("short topic mask = %q", short)
	}
	if got := MaskTarget(KindNtfy, Config{URL: "::"}); got != "****" {
		t.Fatalf("unparsable URL mask = %q", got)
	}
}

func TestSealOpenConfig(t *testing.T) {
	box, err := secretbox.New(bytes.Repeat([]byte{3}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{URL: "https://ntfy.sh", Topic: "alerts", Token: "tk_secret"}
	enc, err := SealConfig(box, KindNtfy, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, []byte("tk_secret")) {
		t.Fatal("ciphertext contains plaintext token")
	}
	got, err := OpenConfig(box, KindNtfy, enc)
	if err != nil || got != cfg {
		t.Fatalf("OpenConfig = %+v, %v", got, err)
	}
	if _, err := OpenConfig(box, KindDiscord, enc); !errors.Is(err, secretbox.ErrInvalid) {
		t.Fatalf("wrong kind (AAD) must fail with ErrInvalid, got %v", err)
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/notify/ -run 'ValidateConfig|MaskTarget|SealOpen'` → FAIL (`undefined: ValidateConfig`).

- [ ] **Step 3: Implementation `internal/notify/config.go`**

```go
package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/Bernotese/mc-panel/internal/secretbox"
)

type Kind string

const (
	KindDiscord Kind = "discord"
	KindNtfy    Kind = "ntfy"
)

func (k Kind) Valid() bool { return k == KindDiscord || k == KindNtfy }

// Config is stored as encrypted JSON in notifiers.config_enc.
type Config struct {
	URL   string `json:"url"`
	Topic string `json:"topic,omitempty"`
	Token string `json:"token,omitempty"`
}

const (
	maxURLLen   = 2048
	maxTokenLen = 256
)

var (
	topicRE        = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	digitsRE       = regexp.MustCompile(`^[0-9]+$`)
	webhookTokenRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// ValidateConfig checks a notifier target. Error messages never contain secrets.
func ValidateConfig(kind Kind, c Config, dev bool) error {
	if !kind.Valid() {
		return errors.New("kind must be discord or ntfy")
	}
	if c.URL == "" {
		return errors.New("url is required")
	}
	if len(c.URL) > maxURLLen {
		return errors.New("url is too long")
	}
	u, err := url.Parse(c.URL)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return errors.New("url must be an absolute URL")
	}
	if u.User != nil {
		return errors.New("url must not contain credentials")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("url must not contain a query or fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !dev {
			return errors.New("url must use https")
		}
	default:
		return errors.New("url must use https")
	}
	host := strings.ToLower(u.Hostname())
	switch kind {
	case KindDiscord:
		if host != "discord.com" && host != "discordapp.com" {
			return errors.New("discord webhook host must be discord.com or discordapp.com")
		}
		if u.Port() != "" {
			return errors.New("discord webhook URL must not contain a port")
		}
		parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
		if len(parts) != 4 || parts[0] != "api" || parts[1] != "webhooks" ||
			!digitsRE.MatchString(parts[2]) || !webhookTokenRE.MatchString(parts[3]) {
			return errors.New("discord webhook URL must look like https://discord.com/api/webhooks/<id>/<token>")
		}
		if c.Topic != "" || c.Token != "" {
			return errors.New("topic and token are only valid for ntfy")
		}
	case KindNtfy:
		if !dev && isInternalHost(host) {
			return errors.New("ntfy server must not be localhost or a loopback, private or link-local address")
		}
		if !topicRE.MatchString(c.Topic) {
			return errors.New("ntfy topic must match ^[A-Za-z0-9_-]{1,64}$")
		}
		bad := func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }
		if len(c.Token) > maxTokenLen || strings.IndexFunc(c.Token, bad) >= 0 {
			return errors.New("ntfy token must be at most 256 characters without whitespace")
		}
	}
	return nil
}

func isInternalHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsUnspecified()
}

// MaskTarget renders a notifier target without secrets (webhook token, topic, access token).
func MaskTarget(kind Kind, c Config) string {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" {
		return "****"
	}
	base := u.Scheme + "://" + u.Host
	switch kind {
	case KindDiscord:
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		id := ""
		if len(parts) >= 3 {
			id = parts[2]
		}
		if len(id) > 4 {
			id = id[:4] + "…"
		}
		return base + "/api/webhooks/" + id + "/****"
	case KindNtfy:
		topic := c.Topic[:min(3, len(c.Topic)/2)]
		s := strings.TrimRight(base+u.Path, "/") + "/" + topic + "****"
		if c.Token != "" {
			s += " (token)"
		}
		return s
	}
	return "****"
}

func configAAD(kind Kind) []byte { return []byte("notifier:" + string(kind)) }

func SealConfig(box *secretbox.Box, kind Kind, c Config) ([]byte, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	return box.Seal(b, configAAD(kind))
}

func OpenConfig(box *secretbox.Box, kind Kind, enc []byte) (Config, error) {
	pt, err := box.Open(enc, configAAD(kind))
	if err != nil {
		return Config{}, fmt.Errorf("notify: decrypt config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(pt, &c); err != nil {
		return Config{}, fmt.Errorf("notify: decode config: %w", err)
	}
	return c, nil
}
```

- [ ] **Step 4: Run** `go test ./internal/notify/ -run 'ValidateConfig|MaskTarget|SealOpen'` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/notify/config.go internal/notify/config_test.go
git commit -m "feat(notify): add notifier config validation, masking and encryption" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Sender für Discord und ntfy

**Files:**
- Create: `internal/notify/sender.go`, `internal/notify/discord.go`, `internal/notify/ntfy.go`
- Test: `internal/notify/sender_test.go`

**Interfaces:**
- Consumes: `notify.Event`, `notify.EventType` + Konstanten (Phase 0), Task 13 (`Kind`, `Config`).
- Produces:
```go
type Sender interface{ Send(ctx context.Context, e Event) error }
type PermanentError struct{ Err error }      // not retried (4xx except 429, redirects, invalid request)
func (e *PermanentError) Error() string
func (e *PermanentError) Unwrap() error
func NewHTTPClient() *http.Client            // 5s timeout, never follows redirects
func NewSender(kind Kind, c Config, client *http.Client) (Sender, error)
func render(e Event) (title, body string)
```
Discord: `POST <webhook>` mit JSON `{"content": "**<title>**\n<body>", "allowed_mentions": {"parse": []}}` (verhindert `@everyone`-Injektion über Benutzernamen), `content` max. 2000 Runen. ntfy: `POST <url>/<topic>` (`url.JoinPath`), Body = Text, Header `Title` (Nicht-ASCII als RFC-2047-B-Wort, das ntfy dekodiert), `Priority: high` für `security.jailed` und `audit.chain_broken`, optional `Authorization: Bearer <token>`. Fehlermeldungen enthalten nie die URL (`*url.Error` wird ausgepackt).

- [ ] **Step 1: Failing test `internal/notify/sender_test.go`**

```go
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

type capturedReq struct {
	method, path, contentType, title, auth, priority string
	body                                             []byte
}

// hookServer records requests and answers with the given status codes in order
// (the last one repeats).
type hookServer struct {
	mu       sync.Mutex
	reqs     []capturedReq
	statuses []int
	srv      *httptest.Server
}

func newHookServer(t *testing.T, statuses ...int) *hookServer {
	t.Helper()
	h := &hookServer{statuses: statuses}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.reqs = append(h.reqs, capturedReq{
			method: r.Method, path: r.URL.Path, contentType: r.Header.Get("Content-Type"),
			title: r.Header.Get("Title"), auth: r.Header.Get("Authorization"), priority: r.Header.Get("Priority"), body: b,
		})
		status := h.statuses[min(len(h.reqs), len(h.statuses))-1]
		h.mu.Unlock()
		if status == http.StatusFound {
			w.Header().Set("Location", "/redirect-target")
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hookServer) requests() []capturedReq {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]capturedReq(nil), h.reqs...)
}

var evTime = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func TestDiscordSender(t *testing.T) {
	h := newHookServer(t, http.StatusNoContent)
	s, err := NewSender(KindDiscord, Config{URL: h.srv.URL + "/api/webhooks/1/tok"}, NewHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	err = s.Send(context.Background(), Event{Type: EventJailed, Title: "IP jailed", Message: "jailed for 15m",
		IP: "203.0.113.7/32", Username: "@everyone", Time: evTime})
	if err != nil {
		t.Fatal(err)
	}
	reqs := h.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPost || reqs[0].path != "/api/webhooks/1/tok" ||
		reqs[0].contentType != "application/json" {
		t.Fatalf("requests = %+v", reqs)
	}
	var payload struct {
		Content         string `json:"content"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	if err := json.Unmarshal(reqs[0].body, &payload); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"**IP jailed**", "jailed for 15m", "IP: 203.0.113.7/32", "User: @everyone", "Time: 2026-10-08T12:00:00Z"} {
		if !strings.Contains(payload.Content, want) {
			t.Errorf("content %q missing %q", payload.Content, want)
		}
	}
	if payload.AllowedMentions.Parse == nil || len(payload.AllowedMentions.Parse) != 0 {
		t.Fatalf("allowed_mentions.parse must be an empty array: %s", reqs[0].body)
	}
}

func TestDiscordContentIsTruncated(t *testing.T) {
	h := newHookServer(t, http.StatusNoContent)
	s, _ := NewSender(KindDiscord, Config{URL: h.srv.URL + "/api/webhooks/1/tok"}, NewHTTPClient())
	if err := s.Send(context.Background(), Event{Type: EventTest, Message: strings.Repeat("ä", 3000)}); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(h.requests()[0].body, &payload)
	if n := utf8.RuneCountInString(payload.Content); n > 2000 {
		t.Fatalf("content has %d runes", n)
	}
}

func TestNtfySender(t *testing.T) {
	h := newHookServer(t, http.StatusOK)
	s, err := NewSender(KindNtfy, Config{URL: h.srv.URL + "/", Topic: "alerts", Token: "tk_secret"}, NewHTTPClient())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), Event{Type: EventJailed, Title: "IP jailed", Message: "jailed", IP: "203.0.113.7/32"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(context.Background(), Event{Type: EventNewDeviceLogin, Title: "Login von neuem Gerät", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	reqs := h.requests()
	if len(reqs) != 2 || reqs[0].path != "/alerts" || reqs[0].title != "IP jailed" || reqs[0].auth != "Bearer tk_secret" ||
		reqs[0].priority != "high" || !strings.HasPrefix(reqs[0].contentType, "text/plain") ||
		!strings.Contains(string(reqs[0].body), "IP: 203.0.113.7/32") {
		t.Fatalf("first request = %+v", reqs[0])
	}
	if reqs[1].priority != "" {
		t.Fatalf("new-device events use default priority, got %q", reqs[1].priority)
	}
	decoded, err := new(mime.WordDecoder).DecodeHeader(reqs[1].title)
	if err != nil || decoded != "Login von neuem Gerät" || !strings.HasPrefix(reqs[1].title, "=?utf-8?b?") {
		t.Fatalf("title header = %q (decoded %q, %v)", reqs[1].title, decoded, err)
	}
}

func TestSenderStatusClassification(t *testing.T) {
	cases := map[int]bool{ // status -> permanent?
		http.StatusBadRequest:          true,
		http.StatusNotFound:            true,
		http.StatusFound:               true,
		http.StatusTooManyRequests:     false,
		http.StatusServiceUnavailable:  false,
		http.StatusInternalServerError: false,
	}
	for status, permanent := range cases {
		h := newHookServer(t, status)
		s, _ := NewSender(KindNtfy, Config{URL: h.srv.URL, Topic: "alerts"}, NewHTTPClient())
		err := s.Send(context.Background(), Event{Type: EventTest})
		var pe *PermanentError
		if err == nil || errors.As(err, &pe) != permanent {
			t.Errorf("status %d: err = %v, permanent want %v", status, err, permanent)
		}
		if n := len(h.requests()); n != 1 {
			t.Errorf("status %d: %d requests (redirects must not be followed)", status, n)
		}
	}
}

func TestSenderErrorsDoNotLeakURL(t *testing.T) {
	h := newHookServer(t, http.StatusOK)
	base := h.srv.URL
	h.srv.Close()
	senders := map[string]Config{
		"discord": {URL: base + "/api/webhooks/1/SuperSecretToken"},
		"ntfy":    {URL: base, Topic: "secrettopic", Token: "tk_secret"},
	}
	for kind, cfg := range senders {
		s, _ := NewSender(Kind(kind), cfg, NewHTTPClient())
		err := s.Send(context.Background(), Event{Type: EventTest})
		if err == nil {
			t.Fatalf("%s: expected error", kind)
		}
		for _, secret := range []string{"SuperSecretToken", "secrettopic", "tk_secret"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("%s: error %q leaks %q", kind, err, secret)
			}
		}
	}
}

func TestRenderDefaults(t *testing.T) {
	title, body := render(Event{Type: EventAuditChainBroken})
	if title != "Audit log chain is broken" || body != "" {
		t.Fatalf("render = %q / %q", title, body)
	}
	if title, _ := render(Event{Type: EventType("x.custom")}); title != "mc-panel: x.custom" {
		t.Fatalf("fallback title = %q", title)
	}
}

func TestNewSenderUnknownKind(t *testing.T) {
	if _, err := NewSender(Kind("slack"), Config{}, NewHTTPClient()); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/notify/ -run 'Sender|Render'` → FAIL (`undefined: NewSender`).

- [ ] **Step 3: Implementation `internal/notify/sender.go`**

```go
package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Sender delivers one event to one target.
type Sender interface {
	Send(ctx context.Context, e Event) error
}

// PermanentError marks failures that retrying cannot fix.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

const httpTimeout = 5 * time.Second

// NewHTTPClient returns the client used for notifications: 5 s timeout and no redirects
// (a redirect could leak the webhook token to another host).
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       httpTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func NewSender(kind Kind, c Config, client *http.Client) (Sender, error) {
	switch kind {
	case KindDiscord:
		return &discordSender{url: c.URL, client: client}, nil
	case KindNtfy:
		return &ntfySender{base: c.URL, topic: c.Topic, token: c.Token, client: client}, nil
	}
	return nil, fmt.Errorf("notify: unknown kind %q", kind)
}

// requestError removes the request URL (which may contain secrets) from transport errors.
func requestError(kind Kind, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return fmt.Errorf("notify: %s request failed: %w", kind, err)
}

func checkStatus(kind Kind, resp *http.Response) error {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	err := fmt.Errorf("notify: %s returned status %d", kind, resp.StatusCode)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return err
	}
	return &PermanentError{Err: err}
}

var defaultTitles = map[EventType]string{
	EventNewDeviceLogin:     "Login from a new device",
	EventJailed:             "IP jailed",
	EventPermissionsChanged: "Roles or permissions changed",
	EventUserCreated:        "User created",
	EventUserDeleted:        "User deleted",
	Event2FAChanged:         "Two-factor authentication changed",
	EventAuditChainBroken:   "Audit log chain is broken",
	EventTest:               "mc-panel test notification",
}

func render(e Event) (title, body string) {
	title = e.Title
	if title == "" {
		title = defaultTitles[e.Type]
	}
	if title == "" {
		title = "mc-panel: " + string(e.Type)
	}
	var b strings.Builder
	if e.Message != "" {
		b.WriteString(e.Message)
		b.WriteString("\n")
	}
	if e.Username != "" {
		fmt.Fprintf(&b, "User: %s\n", e.Username)
	}
	if e.IP != "" {
		fmt.Fprintf(&b, "IP: %s\n", e.IP)
	}
	if !e.Time.IsZero() {
		fmt.Fprintf(&b, "Time: %s\n", e.Time.UTC().Format(time.RFC3339))
	}
	return title, strings.TrimRight(b.String(), "\n")
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
```

- [ ] **Step 4: Implementation `internal/notify/discord.go`**

```go
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

const discordMaxContent = 2000

type discordSender struct {
	url    string
	client *http.Client
}

func (d *discordSender) Send(ctx context.Context, e Event) error {
	title, body := render(e)
	content := truncateRunes("**"+title+"**\n"+body, discordMaxContent)
	payload, err := json.Marshal(map[string]any{
		"content":          content,
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
	if err != nil {
		return &PermanentError{Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.url, bytes.NewReader(payload))
	if err != nil {
		return &PermanentError{Err: errors.New("notify: discord: invalid webhook URL")}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mc-panel")
	resp, err := d.client.Do(req)
	if err != nil {
		return requestError(KindDiscord, err)
	}
	defer resp.Body.Close()
	return checkStatus(KindDiscord, resp)
}
```

- [ ] **Step 5: Implementation `internal/notify/ntfy.go`**

```go
package notify

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ntfySender struct {
	base, topic, token string
	client             *http.Client
}

// headerValue removes control characters and RFC-2047-encodes non-ASCII text.
func headerValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return mime.BEncoding.Encode("utf-8", s)
		}
	}
	return s
}

func (n *ntfySender) Send(ctx context.Context, e Event) error {
	title, body := render(e)
	target, err := url.JoinPath(n.base, n.topic)
	if err != nil {
		return &PermanentError{Err: errors.New("notify: ntfy: invalid server URL")}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		return &PermanentError{Err: errors.New("notify: ntfy: invalid server URL")}
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("User-Agent", "mc-panel")
	req.Header.Set("Title", headerValue(title))
	if e.Type == EventJailed || e.Type == EventAuditChainBroken {
		req.Header.Set("Priority", "high")
	}
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return requestError(KindNtfy, err)
	}
	defer resp.Body.Close()
	return checkStatus(KindNtfy, resp)
}
```

- [ ] **Step 6: Run** `go test ./internal/notify/` → PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/notify/sender.go internal/notify/discord.go internal/notify/ntfy.go internal/notify/sender_test.go
git commit -m "feat(notify): add Discord and ntfy senders" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15: Dispatcher (asynchron, Dedup, Retry, Testversand)

**Files:**
- Modify: `internal/store/queries/defense.sql` (anhängen), regeneriert `internal/store/defense.sql.go`
- Create: `internal/notify/dispatcher.go`
- Test: `internal/notify/dispatcher_test.go`

**Interfaces:**
- Consumes: `notify.Notifier` (Phase 0), `secretbox.Box`, `clock.Clock`, `storetest.Open`, Task 13 (`SealConfig`, `OpenConfig`), Task 14 (`Sender`, `NewSender`, `NewHTTPClient`, `PermanentError`, `hookServer` aus `sender_test.go`).
- Produces:
```go
const DedupWindow = 5 * time.Minute
var DefaultBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
var ErrNotFound, ErrConfig error
type DispatcherOptions struct{ QueueSize int; Backoff []time.Duration; Client *http.Client; Logger *slog.Logger }
func NewDispatcher(db *sql.DB, box *secretbox.Box, c clock.Clock, opts DispatcherOptions) *Dispatcher
func (d *Dispatcher) Notify(ctx context.Context, e Event)        // implements Notifier; never blocks
func (d *Dispatcher) Run(ctx context.Context)                     // Phase 2: go disp.Run(ctx)
func (d *Dispatcher) SendTest(ctx context.Context, id int64) error   // ErrNotFound | ErrConfig | send error (sanitized)
// sqlc
func (q *Queries) DefenseListEnabledNotifiers(ctx context.Context) ([]Notifier, error)
func (q *Queries) DefenseGetNotifier(ctx context.Context, id int64) (Notifier, error)
func (q *Queries) DefenseInsertNotifier(ctx context.Context, arg DefenseInsertNotifierParams) (Notifier, error)
```
Semantik: `Notify` setzt `Time` (falls leer) auf `clock.Now()`, dedupliziert (`Type|IP`, nur bei nicht leerer IP und nicht `EventTest`) und reiht nicht blockierend ein; volle Queue → `slog.Warn` + verwerfen. `Run` stellt nacheinander an alle aktivierten Notifier zu (Konfiguration bei jedem Event frisch aus der DB). Pro Notifier: 1 Versuch + `len(Backoff)` Wiederholungen; `PermanentError` beendet sofort. Fehler nur ins Log (ohne URL). `Backoff: nil` → `DefaultBackoff`, leerer Slice → keine Wiederholung.

- [ ] **Step 1: Queries an `internal/store/queries/defense.sql` anhängen**

```sql
-- name: DefenseListEnabledNotifiers :many
SELECT * FROM notifiers WHERE enabled = 1 ORDER BY id;

-- name: DefenseGetNotifier :one
SELECT * FROM notifiers WHERE id = sqlc.arg(id);

-- name: DefenseInsertNotifier :one
INSERT INTO notifiers (kind, name, config_enc, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;
```

```bash
sqlc generate && go build ./internal/store/
```
Expected: `DefenseInsertNotifierParams{Kind string; Name string; ConfigEnc []byte; Enabled int64; CreatedAt int64; UpdatedAt int64}`.

- [ ] **Step 2: Failing test `internal/notify/dispatcher_test.go`**

```go
package notify

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/store"
	"github.com/Bernotese/mc-panel/internal/store/storetest"
)

var dT0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

type dispEnv struct {
	db  *sql.DB
	q   *store.Queries
	box *secretbox.Box
	clk *clock.Fake
	d   *Dispatcher
}

func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	b, err := secretbox.New(bytes.Repeat([]byte{9}, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newDispEnv(t *testing.T, opts DispatcherOptions) *dispEnv {
	t.Helper()
	db := storetest.Open(t)
	e := &dispEnv{db: db, q: store.New(db), box: testBox(t), clk: clock.NewFake(dT0)}
	if opts.Backoff == nil {
		opts.Backoff = []time.Duration{0, 0, 0}
	}
	e.d = NewDispatcher(db, e.box, e.clk, opts)
	return e
}

func (e *dispEnv) addNotifier(t *testing.T, kind Kind, name string, cfg Config, enabled bool) int64 {
	t.Helper()
	enc, err := SealConfig(e.box, kind, cfg)
	if err != nil {
		t.Fatal(err)
	}
	en := int64(0)
	if enabled {
		en = 1
	}
	row, err := e.q.DefenseInsertNotifier(context.Background(), store.DefenseInsertNotifierParams{
		Kind: string(kind), Name: name, ConfigEnc: enc, Enabled: en, CreatedAt: dT0.UnixMilli(), UpdatedAt: dT0.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func TestNotifyDedup(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{QueueSize: 20})
	ctx := context.Background()
	jailed := func(ip string) Event { return Event{Type: EventJailed, IP: ip} }
	e.d.Notify(ctx, jailed("203.0.113.7/32"))
	e.d.Notify(ctx, jailed("203.0.113.7/32"))
	if n := len(e.d.ch); n != 1 {
		t.Fatalf("duplicate within 5 min must be dropped, queued %d", n)
	}
	e.d.Notify(ctx, jailed("203.0.113.8/32"))
	e.d.Notify(ctx, Event{Type: EventNewDeviceLogin, IP: "203.0.113.7/32"})
	e.clk.Advance(DedupWindow)
	e.d.Notify(ctx, jailed("203.0.113.7/32"))
	if n := len(e.d.ch); n != 4 {
		t.Fatalf("queued %d, want 4", n)
	}
	e.d.Notify(ctx, Event{Type: EventUserCreated, Username: "a"})
	e.d.Notify(ctx, Event{Type: EventUserCreated, Username: "b"})
	e.d.Notify(ctx, Event{Type: EventTest, IP: "x"})
	e.d.Notify(ctx, Event{Type: EventTest, IP: "x"})
	if n := len(e.d.ch); n != 8 {
		t.Fatalf("events without IP and test events are never deduplicated, queued %d", n)
	}
}

func TestNotifyNeverBlocks(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{QueueSize: 1})
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			e.d.Notify(context.Background(), Event{Type: EventUserCreated})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Notify blocked on a full queue")
	}
	if n := len(e.d.ch); n != 1 {
		t.Fatalf("queued %d, want 1", n)
	}
	ev := <-e.d.ch
	if !ev.Time.Equal(dT0) {
		t.Fatalf("event time = %v, want clock time", ev.Time)
	}
}

func TestDeliverOnlyToEnabledNotifiers(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{})
	a := newHookServer(t, http.StatusNoContent)
	b := newHookServer(t, http.StatusOK)
	e.addNotifier(t, KindDiscord, "discord", Config{URL: a.srv.URL + "/api/webhooks/1/tok"}, true)
	e.addNotifier(t, KindNtfy, "ntfy-off", Config{URL: b.srv.URL, Topic: "alerts"}, false)
	e.d.deliver(context.Background(), Event{Type: EventJailed, Title: "IP jailed"})
	if len(a.requests()) != 1 || len(b.requests()) != 0 {
		t.Fatalf("a=%d b=%d", len(a.requests()), len(b.requests()))
	}
}

func TestDeliverRetries(t *testing.T) {
	cases := []struct {
		name     string
		statuses []int
		want     int
	}{
		{"transient then ok", []int{500, 503, 204}, 3},
		{"permanent", []int{400}, 1},
		{"always failing", []int{500}, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newDispEnv(t, DispatcherOptions{})
			h := newHookServer(t, tc.statuses...)
			e.addNotifier(t, KindNtfy, "n", Config{URL: h.srv.URL, Topic: "alerts"}, true)
			e.d.deliver(context.Background(), Event{Type: EventTest})
			if n := len(h.requests()); n != tc.want {
				t.Fatalf("requests = %d, want %d", n, tc.want)
			}
		})
	}
}

func TestDeliverSkipsUndecryptableNotifier(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{})
	h := newHookServer(t, http.StatusOK)
	if _, err := e.q.DefenseInsertNotifier(context.Background(), store.DefenseInsertNotifierParams{
		Kind: "ntfy", Name: "broken", ConfigEnc: []byte("garbage"), Enabled: 1, CreatedAt: 0, UpdatedAt: 0,
	}); err != nil {
		t.Fatal(err)
	}
	e.addNotifier(t, KindNtfy, "ok", Config{URL: h.srv.URL, Topic: "alerts"}, true)
	e.d.deliver(context.Background(), Event{Type: EventTest})
	if len(h.requests()) != 1 {
		t.Fatal("a broken notifier must not stop delivery to the others")
	}
}

func TestRunDeliversQueuedEvents(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{})
	h := newHookServer(t, http.StatusOK)
	e.addNotifier(t, KindNtfy, "n", Config{URL: h.srv.URL, Topic: "alerts"}, true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { e.d.Run(ctx); close(done) }()
	e.d.Notify(ctx, Event{Type: EventJailed, Title: "IP jailed", IP: "203.0.113.7/32"})
	deadline := time.Now().Add(2 * time.Second)
	for len(h.requests()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("event was not delivered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestSendTest(t *testing.T) {
	e := newDispEnv(t, DispatcherOptions{})
	ctx := context.Background()
	if err := e.d.SendTest(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	ok := newHookServer(t, http.StatusOK)
	id := e.addNotifier(t, KindNtfy, "ok", Config{URL: ok.srv.URL, Topic: "alerts"}, false)
	if err := e.d.SendTest(ctx, id); err != nil {
		t.Fatalf("SendTest: %v", err)
	}
	reqs := ok.requests()
	if len(reqs) != 1 || reqs[0].title != "mc-panel test notification" || !strings.Contains(string(reqs[0].body), "notifications work") {
		t.Fatalf("test request = %+v", reqs)
	}
	bad := newHookServer(t, http.StatusNotFound)
	badID := e.addNotifier(t, KindNtfy, "bad", Config{URL: bad.srv.URL, Topic: "alerts"}, true)
	var pe *PermanentError
	if err := e.d.SendTest(ctx, badID); !errors.As(err, &pe) || len(bad.requests()) != 1 {
		t.Fatalf("SendTest = %v (requests %d), want single permanent failure", err, len(bad.requests()))
	}
	row, err := e.q.DefenseInsertNotifier(ctx, store.DefenseInsertNotifierParams{
		Kind: "ntfy", Name: "broken", ConfigEnc: []byte("garbage"), Enabled: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.d.SendTest(ctx, row.ID); !errors.Is(err, ErrConfig) {
		t.Fatalf("broken config: %v", err)
	}
}
```

- [ ] **Step 3: Run** `go test ./internal/notify/ -run 'Notify|Deliver|RunDelivers|SendTest'` → FAIL (`undefined: NewDispatcher`).

- [ ] **Step 4: Implementation `internal/notify/dispatcher.go`**

```go
package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/store"
)

const (
	DedupWindow      = 5 * time.Minute
	defaultQueueSize = 100
	maxDedupEntries  = 1024
)

// DefaultBackoff are the delays between delivery attempts (1 attempt + 3 retries).
var DefaultBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

var (
	ErrNotFound = errors.New("notify: notifier not found")
	ErrConfig   = errors.New("notify: notifier configuration is unreadable")
)

type DispatcherOptions struct {
	QueueSize int             // default 100
	Backoff   []time.Duration // nil → DefaultBackoff; empty → no retries
	Client    *http.Client    // default NewHTTPClient()
	Logger    *slog.Logger    // default slog.Default()
}

// Dispatcher implements Notifier: events are queued without blocking and
// delivered asynchronously by Run to all enabled notifiers.
type Dispatcher struct {
	q       *store.Queries
	box     *secretbox.Box
	clock   clock.Clock
	client  *http.Client
	backoff []time.Duration
	log     *slog.Logger
	ch      chan Event

	mu   sync.Mutex
	last map[string]time.Time
}

var _ Notifier = (*Dispatcher)(nil)

func NewDispatcher(db *sql.DB, box *secretbox.Box, c clock.Clock, opts DispatcherOptions) *Dispatcher {
	if opts.QueueSize <= 0 {
		opts.QueueSize = defaultQueueSize
	}
	if opts.Backoff == nil {
		opts.Backoff = DefaultBackoff
	}
	if opts.Client == nil {
		opts.Client = NewHTTPClient()
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Dispatcher{
		q: store.New(db), box: box, clock: c, client: opts.Client, backoff: opts.Backoff, log: opts.Logger,
		ch: make(chan Event, opts.QueueSize), last: make(map[string]time.Time),
	}
}

func (d *Dispatcher) Notify(_ context.Context, e Event) {
	now := d.clock.Now()
	if e.Time.IsZero() {
		e.Time = now
	}
	if e.Type != EventTest && e.IP != "" && d.isDuplicate(string(e.Type)+"|"+e.IP, now) {
		return
	}
	select {
	case d.ch <- e:
	default:
		d.log.Warn("notify: queue full, dropping event", "type", e.Type)
	}
}

func (d *Dispatcher) isDuplicate(key string, now time.Time) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.last[key]; ok && now.Sub(t) < DedupWindow {
		return true
	}
	d.last[key] = now
	if len(d.last) > maxDedupEntries {
		for k, t := range d.last {
			if now.Sub(t) >= DedupWindow {
				delete(d.last, k)
			}
		}
	}
	return false
}

// Run delivers queued events until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-d.ch:
			d.deliver(ctx, e)
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, e Event) {
	rows, err := d.q.DefenseListEnabledNotifiers(ctx)
	if err != nil {
		d.log.Error("notify: load notifiers", "err", err)
		return
	}
	for _, n := range rows {
		s, err := d.senderFor(n)
		if err != nil {
			d.log.Error("notify: skipping notifier", "notifier", n.Name, "err", err)
			continue
		}
		if err := d.sendWithRetry(ctx, s, e); err != nil {
			d.log.Warn("notify: delivery failed", "notifier", n.Name, "type", e.Type, "err", err)
		}
	}
}

func (d *Dispatcher) senderFor(n store.Notifier) (Sender, error) {
	cfg, err := OpenConfig(d.box, Kind(n.Kind), n.ConfigEnc)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	return NewSender(Kind(n.Kind), cfg, d.client)
}

func (d *Dispatcher) sendWithRetry(ctx context.Context, s Sender, e Event) error {
	for attempt := 0; ; attempt++ {
		err := s.Send(ctx, e)
		if err == nil {
			return nil
		}
		var pe *PermanentError
		if errors.As(err, &pe) || attempt >= len(d.backoff) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.backoff[attempt]):
		}
	}
}

// SendTest synchronously sends EventTest to one notifier (single attempt, also if disabled).
func (d *Dispatcher) SendTest(ctx context.Context, id int64) error {
	n, err := d.q.DefenseGetNotifier(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	s, err := d.senderFor(n)
	if err != nil {
		return err
	}
	return s.Send(ctx, Event{
		Type:    EventTest,
		Title:   "mc-panel test notification",
		Message: "If you can read this, notifications work.",
		Time:    d.clock.Now(),
	})
}
```

- [ ] **Step 5: Run** `go test ./internal/notify/` → PASS; zusätzlich `CGO_ENABLED=1 go test -race ./internal/notify/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/queries/defense.sql internal/store/defense.sql.go internal/notify/dispatcher.go internal/notify/dispatcher_test.go
git commit -m "feat(notify): add async dispatcher with dedup and retries" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 16: Notifier-API (CRUD + Testversand)

**Files:**
- Modify: `internal/store/queries/defense.sql` (anhängen), regeneriert `internal/store/defense.sql.go`
- Create: `internal/notify/api.go`
- Test: `internal/notify/api_test.go`

**Interfaces:**
- Consumes: `rbac.Router`, `rbac.Admin()`, `rbac.Rule.WithSudo()`, `rbac.NewRouter`, `chi.URLParam`, `httpx.DecodeJSON`, `httpx.WriteJSON`, `httpx.WriteError`, `httpx.Internal`, `principal.With`, `audit.Recorder`, `audit.ActionNotifierCreate`, `audit.ActionNotifierUpdate`, `audit.ActionNotifierDelete`, Task 13–15.
- Produces:
```go
type HandlerDeps struct{ DB *sql.DB; Box *secretbox.Box; Dispatcher *Dispatcher; Audit audit.Recorder; Clock clock.Clock; Dev bool; Logger *slog.Logger }
func NewHandler(d HandlerDeps) *Handler
func (h *Handler) Routes(api *rbac.Router)    // 5 routes under /security/notifiers
// sqlc
func (q *Queries) DefenseListNotifiers(ctx context.Context) ([]Notifier, error)
func (q *Queries) DefenseUpdateNotifier(ctx context.Context, arg DefenseUpdateNotifierParams) (Notifier, error)
func (q *Queries) DefenseDeleteNotifier(ctx context.Context, id int64) (int64, error)
```
Endpunkte und Regeln (exakt laut OpenAPI-Tabelle): `GET /security/notifiers` Admin; `POST /security/notifiers` Admin+Sudo (201; Name doppelt → 409); `PATCH /security/notifiers/{id}` Admin+Sudo (200; 404; Name doppelt → `400 validation_failed`, da der Vertrag hier kein 409 vorsieht); `DELETE /security/notifiers/{id}` Admin+Sudo (204; 404); `POST /security/notifiers/{id}/test` Admin (204; 404; Zustellfehler → `400 validation_failed` mit bereinigter Meldung, siehe Global Constraints). PATCH: nur gesetzte Felder ändern; `token: ""` entfernt das Token; die zusammengeführte Konfiguration wird vollständig neu validiert. Antworten enthalten nie URL/Topic/Token im Klartext, nur `target_masked`.

- [ ] **Step 1: Queries an `internal/store/queries/defense.sql` anhängen**

```sql
-- name: DefenseListNotifiers :many
SELECT * FROM notifiers ORDER BY name;

-- name: DefenseUpdateNotifier :one
UPDATE notifiers
SET name = sqlc.arg(name), config_enc = sqlc.arg(config_enc), enabled = sqlc.arg(enabled), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DefenseDeleteNotifier :execrows
DELETE FROM notifiers WHERE id = sqlc.arg(id);
```

```bash
sqlc generate && go build ./internal/store/
```
Expected: `DefenseUpdateNotifierParams{Name string; ConfigEnc []byte; Enabled int64; UpdatedAt int64; ID int64}`.

- [ ] **Step 2: Failing test `internal/notify/api_test.go`**

```go
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/principal"
	"github.com/Bernotese/mc-panel/internal/rbac"
)

type noPerms struct{}

func (noPerms) HasServerPermission(context.Context, int64, int64, rbac.Permission) (bool, error) {
	return false, nil
}

type apiEnv struct {
	*dispEnv
	audit *audit.MemoryRecorder
	rt    *rbac.Router
	mux   http.Handler
}

func newAPIEnv(t *testing.T, dev bool) *apiEnv {
	t.Helper()
	de := newDispEnv(t, DispatcherOptions{Backoff: []time.Duration{}})
	rec := &audit.MemoryRecorder{}
	mux := chi.NewRouter()
	rt := rbac.NewRouter(mux, noPerms{}, de.clk)
	NewHandler(HandlerDeps{DB: de.db, Box: de.box, Dispatcher: de.d, Audit: rec, Clock: de.clk, Dev: dev}).Routes(rt)
	return &apiEnv{dispEnv: de, audit: rec, rt: rt, mux: mux}
}

func (e *apiEnv) call(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rdr)
	ctx := httpx.WithClientIP(req.Context(), netip.MustParseAddr("198.51.100.10"))
	ctx = principal.With(ctx, principal.Principal{UserID: 1, Username: "admin", Role: principal.RoleAdmin,
		Stage: principal.StageFull, SessionID: 1, SudoUntil: e.clk.Now().Add(5 * time.Minute)})
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func apiErr(rec *httptest.ResponseRecorder) httpx.ErrorBody {
	var b httpx.ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	return b
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, code int, errCode string) {
	t.Helper()
	if rec.Code != code || (errCode != "" && apiErr(rec).Error != errCode) {
		t.Fatalf("status %d %s, want %d %q", rec.Code, rec.Body, code, errCode)
	}
}

func discordBody(name string) map[string]any {
	return map[string]any{"kind": "discord", "name": name, "enabled": true, "url": testWebhook}
}

func TestNotifierRoutesDeclareRules(t *testing.T) {
	e := newAPIEnv(t, false)
	want := map[string]string{
		"GET /security/notifiers":            "admin[full]",
		"POST /security/notifiers":           "admin+sudo[full]",
		"PATCH /security/notifiers/{id}":     "admin+sudo[full]",
		"DELETE /security/notifiers/{id}":    "admin+sudo[full]",
		"POST /security/notifiers/{id}/test": "admin[full]",
	}
	got := map[string]string{}
	for _, ri := range e.rt.Routes() {
		got[ri.Method+" "+ri.Pattern] = ri.Rule.String()
	}
	if !maps.Equal(got, want) {
		t.Fatalf("routes = %v", got)
	}
}

func TestCreateAndListNotifiers(t *testing.T) {
	e := newAPIEnv(t, false)
	rec := e.call(t, http.MethodPost, "/security/notifiers", discordBody(" Ops "))
	expect(t, rec, http.StatusCreated, "")
	var n map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &n)
	if n["name"] != "Ops" || n["kind"] != "discord" || n["enabled"] != true ||
		n["target_masked"] != "https://discord.com/api/webhooks/1234…/****" || n["created_at"] != float64(dT0.UnixMilli()) {
		t.Fatalf("notifier = %v", n)
	}
	for _, k := range []string{"url", "topic", "token"} {
		if _, ok := n[k]; ok {
			t.Errorf("response must not contain %q", k)
		}
	}
	expect(t, e.call(t, http.MethodPost, "/security/notifiers", discordBody("Ops")), http.StatusConflict, httpx.CodeConflict)
	invalid := []any{
		map[string]any{"kind": "discord", "name": "x", "enabled": true, "url": "https://evil.example/api/webhooks/1/a"},
		map[string]any{"kind": "slack", "name": "x", "enabled": true, "url": testWebhook},
		map[string]any{"kind": "discord", "name": "", "enabled": true, "url": testWebhook},
		map[string]any{"kind": "discord", "name": strings.Repeat("n", 65), "enabled": true, "url": testWebhook},
		map[string]any{"kind": "discord", "name": "x", "url": testWebhook},
		map[string]any{"kind": "ntfy", "name": "x", "enabled": true, "url": "https://ntfy.sh"},
	}
	for i, body := range invalid {
		if rec := e.call(t, http.MethodPost, "/security/notifiers", body); rec.Code != http.StatusBadRequest || apiErr(rec).Error != httpx.CodeValidation {
			t.Errorf("case %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	var list []map[string]any
	_ = json.Unmarshal(e.call(t, http.MethodGet, "/security/notifiers", nil).Body.Bytes(), &list)
	if len(list) != 1 || list[0]["name"] != "Ops" {
		t.Fatalf("list = %v", list)
	}
	if !slices.Equal(e.audit.Actions(), []string{audit.ActionNotifierCreate}) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestNotifierAPINeverReturnsSecrets(t *testing.T) {
	e := newAPIEnv(t, false)
	var bodies []string
	rec := e.call(t, http.MethodPost, "/security/notifiers",
		map[string]any{"kind": "ntfy", "name": "phone", "enabled": true, "url": "https://ntfy.sh", "topic": "secrettopicname", "token": "tk_secret"})
	expect(t, rec, http.StatusCreated, "")
	bodies = append(bodies, rec.Body.String())
	bodies = append(bodies, e.call(t, http.MethodPost, "/security/notifiers", discordBody("discord")).Body.String())
	var created map[string]any
	_ = json.Unmarshal([]byte(bodies[0]), &created)
	id := int64(created["id"].(float64))
	rec = e.call(t, http.MethodPatch, fmt.Sprintf("/security/notifiers/%d", id), map[string]any{"name": "phone2"})
	expect(t, rec, http.StatusOK, "")
	bodies = append(bodies, rec.Body.String(), e.call(t, http.MethodGet, "/security/notifiers", nil).Body.String())
	for _, en := range e.audit.Entries {
		bodies = append(bodies, fmt.Sprintf("%s %v", en.Target, en.Details))
	}
	for _, b := range bodies {
		for _, secret := range []string{"SuperSecretToken", "secrettopicname", "tk_secret", "123456789012345678"} {
			if strings.Contains(b, secret) {
				t.Errorf("%q leaks %q", b, secret)
			}
		}
	}
	if created["target_masked"] != "https://ntfy.sh/sec**** (token)" {
		t.Fatalf("target_masked = %v", created["target_masked"])
	}
}

func TestUpdateNotifier(t *testing.T) {
	e := newAPIEnv(t, false)
	ctx := context.Background()
	rec := e.call(t, http.MethodPost, "/security/notifiers",
		map[string]any{"kind": "ntfy", "name": "phone", "enabled": true, "url": "https://ntfy.sh", "topic": "alerts", "token": "tk_secret"})
	expect(t, rec, http.StatusCreated, "")
	expect(t, e.call(t, http.MethodPost, "/security/notifiers", discordBody("other")), http.StatusCreated, "")
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id := int64(created["id"].(float64))
	path := fmt.Sprintf("/security/notifiers/%d", id)

	rec = e.call(t, http.MethodPatch, path, map[string]any{"enabled": false})
	expect(t, rec, http.StatusOK, "")
	row, err := e.q.DefenseGetNotifier(ctx, id)
	if err != nil || row.Enabled != 0 {
		t.Fatalf("row = %+v, %v", row, err)
	}
	cfg, err := OpenConfig(e.box, KindNtfy, row.ConfigEnc)
	if err != nil || cfg.Token != "tk_secret" || cfg.Topic != "alerts" {
		t.Fatalf("config must be preserved: %+v, %v", cfg, err)
	}
	rec = e.call(t, http.MethodPatch, path, map[string]any{"token": ""})
	expect(t, rec, http.StatusOK, "")
	var upd map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &upd)
	if strings.Contains(upd["target_masked"].(string), "(token)") {
		t.Fatalf("token must be removed: %v", upd)
	}
	expect(t, e.call(t, http.MethodPatch, path, map[string]any{"url": "http://ntfy.sh"}), http.StatusBadRequest, httpx.CodeValidation)
	expect(t, e.call(t, http.MethodPatch, path, map[string]any{"name": "other"}), http.StatusBadRequest, httpx.CodeValidation)
	expect(t, e.call(t, http.MethodPatch, path, map[string]any{"name": ""}), http.StatusBadRequest, httpx.CodeValidation)
	expect(t, e.call(t, http.MethodPatch, "/security/notifiers/999", map[string]any{"enabled": true}), http.StatusNotFound, httpx.CodeNotFound)
	if n := strings.Count(strings.Join(e.audit.Actions(), ","), audit.ActionNotifierUpdate); n != 2 {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestDeleteNotifier(t *testing.T) {
	e := newAPIEnv(t, false)
	rec := e.call(t, http.MethodPost, "/security/notifiers", discordBody("ops"))
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	path := fmt.Sprintf("/security/notifiers/%d", int64(created["id"].(float64)))
	expect(t, e.call(t, http.MethodDelete, path, nil), http.StatusNoContent, "")
	expect(t, e.call(t, http.MethodDelete, path, nil), http.StatusNotFound, httpx.CodeNotFound)
	expect(t, e.call(t, http.MethodDelete, "/security/notifiers/abc", nil), http.StatusNotFound, httpx.CodeNotFound)
	if !slices.Contains(e.audit.Actions(), audit.ActionNotifierDelete) {
		t.Fatalf("audit = %v", e.audit.Actions())
	}
}

func TestTestNotifierEndpoint(t *testing.T) {
	e := newAPIEnv(t, true) // dev: http and 127.0.0.1 targets allowed
	ok := newHookServer(t, http.StatusOK)
	rec := e.call(t, http.MethodPost, "/security/notifiers",
		map[string]any{"kind": "ntfy", "name": "ok", "enabled": false, "url": ok.srv.URL, "topic": "alerts"})
	expect(t, rec, http.StatusCreated, "")
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	expect(t, e.call(t, http.MethodPost, fmt.Sprintf("/security/notifiers/%d/test", int64(created["id"].(float64))), nil),
		http.StatusNoContent, "")
	if len(ok.requests()) != 1 {
		t.Fatalf("requests = %d", len(ok.requests()))
	}
	bad := newHookServer(t, http.StatusNotFound)
	rec = e.call(t, http.MethodPost, "/security/notifiers",
		map[string]any{"kind": "ntfy", "name": "bad", "enabled": true, "url": bad.srv.URL, "topic": "secrettopicname"})
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	rec = e.call(t, http.MethodPost, fmt.Sprintf("/security/notifiers/%d/test", int64(created["id"].(float64))), nil)
	expect(t, rec, http.StatusBadRequest, httpx.CodeValidation)
	if msg := apiErr(rec).Message; !strings.Contains(msg, "status 404") || strings.Contains(msg, "secrettopicname") || strings.Contains(msg, bad.srv.URL) {
		t.Fatalf("message = %q", msg)
	}
	expect(t, e.call(t, http.MethodPost, "/security/notifiers/999/test", nil), http.StatusNotFound, httpx.CodeNotFound)
}
```

- [ ] **Step 3: Run** `go test ./internal/notify/ -run 'Notifier|TestNotifierEndpoint'` → FAIL (`undefined: NewHandler`).

- [ ] **Step 4: Implementation `internal/notify/api.go`**

```go
package notify

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/secretbox"
	"github.com/Bernotese/mc-panel/internal/store"
)

const maxNotifierNameLen = 64

type HandlerDeps struct {
	DB         *sql.DB
	Box        *secretbox.Box
	Dispatcher *Dispatcher
	Audit      audit.Recorder
	Clock      clock.Clock
	Dev        bool
	Logger     *slog.Logger
}

// Handler serves /api/security/notifiers.
type Handler struct {
	q     *store.Queries
	box   *secretbox.Box
	disp  *Dispatcher
	audit audit.Recorder
	clock clock.Clock
	dev   bool
	log   *slog.Logger
}

func NewHandler(d HandlerDeps) *Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Handler{q: store.New(d.DB), box: d.Box, disp: d.Dispatcher, audit: d.Audit, clock: d.Clock, dev: d.Dev, log: d.Logger}
}

// Routes registers the notifier endpoints with full paths relative to /api.
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/security/notifiers", rbac.Admin(), h.list)
	api.Post("/security/notifiers", rbac.Admin().WithSudo(), h.create)
	api.Patch("/security/notifiers/{id}", rbac.Admin().WithSudo(), h.update)
	api.Delete("/security/notifiers/{id}", rbac.Admin().WithSudo(), h.delete)
	api.Post("/security/notifiers/{id}/test", rbac.Admin(), h.test)
}

type notifierResponse struct {
	ID           int64  `json:"id"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	TargetMasked string `json:"target_masked"`
	CreatedAt    int64  `json:"created_at"`
}

func (h *Handler) toResponse(n store.Notifier) notifierResponse {
	masked := "(unreadable)"
	if cfg, err := OpenConfig(h.box, Kind(n.Kind), n.ConfigEnc); err == nil {
		masked = MaskTarget(Kind(n.Kind), cfg)
	} else {
		h.log.Error("notify: cannot decrypt notifier config", "notifier", n.Name, "err", err)
	}
	return notifierResponse{ID: n.ID, Kind: n.Kind, Name: n.Name, Enabled: n.Enabled != 0, TargetMasked: masked, CreatedAt: n.CreatedAt}
}

func validation(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "notifier not found")
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		notFound(w, r)
		return 0, false
	}
	return id, true
}

func cleanName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	return name, n >= 1 && n <= maxNotifierNameLen
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// record writes the audit entry after the change was committed. Details never contain secrets.
func (h *Handler) record(ctx context.Context, action, target string, details map[string]any) {
	if err := h.audit.Record(ctx, audit.Entry{Action: action, Target: target, Result: audit.ResultSuccess, Details: details}); err != nil {
		h.log.Error("notify: audit", "action", action, "err", err)
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.DefenseListNotifiers(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]notifierResponse, 0, len(rows))
	for _, n := range rows {
		out = append(out, h.toResponse(n))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type createRequest struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled"`
	URL     string `json:"url"`
	Topic   string `json:"topic"`
	Token   string `json:"token"`
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	kind := Kind(req.Kind)
	if !kind.Valid() {
		validation(w, r, "kind must be discord or ntfy")
		return
	}
	name, ok := cleanName(req.Name)
	if !ok {
		validation(w, r, "name must be 1 to 64 characters")
		return
	}
	if req.Enabled == nil {
		validation(w, r, "enabled is required")
		return
	}
	cfg := Config{URL: strings.TrimSpace(req.URL), Topic: strings.TrimSpace(req.Topic), Token: strings.TrimSpace(req.Token)}
	if err := ValidateConfig(kind, cfg, h.dev); err != nil {
		validation(w, r, err.Error())
		return
	}
	enc, err := SealConfig(h.box, kind, cfg)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	now := h.clock.Now().UnixMilli()
	row, err := h.q.DefenseInsertNotifier(r.Context(), store.DefenseInsertNotifierParams{
		Kind: string(kind), Name: name, ConfigEnc: enc, Enabled: boolInt(*req.Enabled), CreatedAt: now, UpdatedAt: now,
	})
	if isUniqueViolation(err) {
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "a notifier with this name already exists")
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(r.Context(), audit.ActionNotifierCreate, row.Name,
		map[string]any{"notifier_id": row.ID, "kind": row.Kind, "enabled": row.Enabled != 0})
	httpx.WriteJSON(w, http.StatusCreated, h.toResponse(row))
}

type updateRequest struct {
	Name    *string `json:"name"`
	Enabled *bool   `json:"enabled"`
	URL     *string `json:"url"`
	Topic   *string `json:"topic"`
	Token   *string `json:"token"`
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req updateRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	row, err := h.q.DefenseGetNotifier(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, r)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	kind := Kind(row.Kind)
	cfg, err := OpenConfig(h.box, kind, row.ConfigEnc)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	name, enabled := row.Name, row.Enabled
	var fields []string
	if req.Name != nil {
		n, ok := cleanName(*req.Name)
		if !ok {
			validation(w, r, "name must be 1 to 64 characters")
			return
		}
		name = n
		fields = append(fields, "name")
	}
	if req.Enabled != nil {
		enabled = boolInt(*req.Enabled)
		fields = append(fields, "enabled")
	}
	if req.URL != nil {
		cfg.URL = strings.TrimSpace(*req.URL)
		fields = append(fields, "url")
	}
	if req.Topic != nil {
		cfg.Topic = strings.TrimSpace(*req.Topic)
		fields = append(fields, "topic")
	}
	if req.Token != nil {
		cfg.Token = strings.TrimSpace(*req.Token)
		fields = append(fields, "token")
	}
	if err := ValidateConfig(kind, cfg, h.dev); err != nil {
		validation(w, r, err.Error())
		return
	}
	enc, err := SealConfig(h.box, kind, cfg)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	updated, err := h.q.DefenseUpdateNotifier(ctx, store.DefenseUpdateNotifierParams{
		Name: name, ConfigEnc: enc, Enabled: enabled, UpdatedAt: h.clock.Now().UnixMilli(), ID: id,
	})
	if isUniqueViolation(err) {
		validation(w, r, "a notifier with this name already exists")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, r)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	h.record(ctx, audit.ActionNotifierUpdate, updated.Name, map[string]any{"notifier_id": id, "fields": fields})
	httpx.WriteJSON(w, http.StatusOK, h.toResponse(updated))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	row, err := h.q.DefenseGetNotifier(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, r)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	n, err := h.q.DefenseDeleteNotifier(ctx, id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if n == 0 {
		notFound(w, r)
		return
	}
	h.record(ctx, audit.ActionNotifierDelete, row.Name, map[string]any{"notifier_id": id, "kind": row.Kind})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) test(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := h.disp.SendTest(r.Context(), id)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrNotFound):
		notFound(w, r)
	case errors.Is(err, ErrConfig):
		httpx.Internal(w, r, err)
	default:
		// Sender errors are sanitized (no URL, topic or token).
		validation(w, r, "test notification failed: "+err.Error())
	}
}
```

- [ ] **Step 5: Run** `go test ./internal/notify/` → PASS. Danach das gesamte Stream-Paket: `go vet ./internal/defense/ ./internal/notify/ && go test ./internal/... && make lint` → grün.

- [ ] **Step 6: Commit**

```bash
git add internal/store/queries/defense.sql internal/store/defense.sql.go internal/notify/api.go internal/notify/api_test.go
git commit -m "feat(notify): add notifier CRUD and test endpoints" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-Review

Spec-Abdeckung (Stream B, Spec §7, §3.4, §9, §10, OpenAPI-Zeilen „B“):

| Anforderung | Task |
|---|---|
| §7.1 Token-Bucket Login pro IP (10/min, Burst 5), pro Benutzername (5/min), 2FA pro Session+IP (5/min) | 1, 3 |
| §7.1 API 300/min pro Session, `429` + `Retry-After` | 1, 5 |
| §7.2 5 Fehlversuche / 10 min → 15 min; Verdopplung bei erneuter Sperre in 30 d, max. 7 d | 4 (`TestJailAfterFiveFailures`, `TestJailEscalation`, `TestJailDuration`) |
| §7.2 Gesperrte IPs: `403` für alle Requests vor dem Routing | 5 (`TestIPFilterJailed`, `TestJailBlocksImmediatelyWithoutReload`) |
| §7.2 nftables-Eintrag über agent mit Timeout = Restdauer; agent-Ausfall → App-Sperre bleibt, Sync wird nachgeholt; Abgleich nach agent-Neustart | 7 |
| §7.2 Konten werden nie gesperrt (nur IP-Keys) | 4 (keine Benutzer-Sperre; Benutzername nur rate-limitiert, Task 3) |
| §7.2 Allowlist-IPs nie gejailt | 4 (`TestAllowlistedAndLoopbackNeverJailed`), 8 (manuell → 409) |
| §7.2 Admin hebt Jails auf (Sudo) / sperrt manuell | 8 |
| §7.3 Allow-/Denylist CIDR v4/v6, Kommentar, Ablaufdatum | 2, 9 |
| §7.3 „Admin-Zugang nur aus Allowlist“ | 3 (`CheckAdminNetwork`), 10 |
| §7.3 Länderfilter über lokale DB-IP-Lite-MMDB; ohne DB deaktiviert, Länder leer | 5, 6, 10 (`geoip_available`) |
| §7.3 Schutz vor Selbstaussperrung | 8, 9, 10 |
| §7.3 Notfall `panel admin unlock` | 12 |
| §7.4 Diagramm Erfolg/Fehler 24 h / 7 d / 30 d; Top-IPs/-Länder; aktive Jails; neue Geräte | 11, 8 |
| §7.4 Konfiguration IP-Regeln, Länderfilter, Notifier | 9, 10, 16 |
| §7.4 Aktive Sessions aller Benutzer, Audit-Log | nicht Stream B (A bzw. D) |
| §7.5 Notifier-Interface, Discord-Webhook, ntfy (URL + Topic + optional Token) | 14, 15 |
| §7.5 Events „neues Gerät“ und „Jail ausgelöst“ (B), übrige Events über `notify.Notifier` von A/D | 4, 15 |
| §7.5 Asynchron, Retry, Dedup (Typ + IP, 5 min), Fehler nur ins Log | 15 |
| §7.5 „Test senden“ | 15, 16 |
| §3.4 Secrets in der DB, im UI nur maskiert | 13, 16 |
| §9 agent-Fehler: App-Funktionen laufen weiter; Agent-Status im Security Center | 7, 11 |
| §10 Unit-Tests Rate-Limiter, Jail-Eskalation, CIDR-Regeln, Notifier-Dedup | 1, 3, 4, 9, 15 |
| Aufbewahrung `login_attempts` 90 d | 7 |
| OpenAPI: 11 Security-Endpunkte mit exakten Regeln | 8–11 (`TestRoutesDeclareRules`) |
| OpenAPI: 5 Notifier-Endpunkte mit exakten Regeln | 16 (`TestNotifierRoutesDeclareRules`) |
| Review Focus 1 (IPv6-/64, IPv4-mapped) | 3, 4 |
| Review Focus 4 (agent nicht erreichbar) | 7 |
| Stream-A-Vertrag: `reason = 'complete'` = vollständige Anmeldung; `admin_network`-Ablehnungen zählen fürs Jail | 4, 11 |

Typ-/Signatur-Konsistenz geprüft: `keyedLimiter.take` (1) → 3, 5; `snapshot`/`addJail`/`Reload` (2) → 3–12; `msNull` (7) → 8, 12; `settingsDetails` (10) → 12; `locksOut`/`withRule`/`withoutRule`/`withSettings` (9) → 9, 10; `fail`/`activeJails` (4), `filter`/`errCode` (5), `fakeAgent`/`withAgent`/`errAgentDown` (7), `call`/`decode`/`wantStatus`/`denyChecker`/`callerIP` (8) werden nur in späteren Tasks desselben Pakets genutzt; `hookServer`/`testWebhook`/`newDispEnv` (13–15) → 15, 16. Alle sqlc-Funktionen werden in dem Task angelegt, der sie zuerst braucht.

## Wiring-Vertrag

**Konstruktoren und Hintergrund-Goroutinen (Phase 2, `cmd/panel serve`):**

```go
key, err := secretbox.LoadOrCreateKey(cfg.SecretKeyPath())
box, err := secretbox.New(key)

geo, geoOK, closeGeo := defense.LoadGeo(cfg.GeoIPPath, slog.Default()) // (defense.GeoResolver, bool, func() error)
defer closeGeo()

disp := notify.NewDispatcher(db, box, clock.Real{}, notify.DispatcherOptions{}) // *notify.Dispatcher, implements notify.Notifier

svc, err := defense.NewService(ctx, defense.Deps{
	DB: db, Clock: clock.Real{}, Geo: geo, GeoAvailable: geoOK,
	Audit: auditRecorder, // Stream D audit.Recorder
	Notifier: disp,
	Agent: agentClient,   // Stream C agentapi.Client (nil → kein Firewall-Sync, Agent "nicht erreichbar")
}) // *defense.Service, implements defense.LoginGuard

var wg sync.WaitGroup
wg.Add(2)
go func() { defer wg.Done(); disp.Run(ctx) }() // blockiert bis ctx.Done()
go func() { defer wg.Done(); svc.Run(ctx) }()  // Firewall-Sync (10 s + Kick), Regel-Refresh (30 s), Retention (1 h), Limiter-Cleanup (1 min)
// beim Shutdown: ctx abbrechen, wg.Wait(), erst danach db.Close()
```

**Middleware (Reihenfolge aus Phase 0, Positionen 5 und 7):**

```go
mux.Use(
	httpx.RequestID,
	httpx.RealIP(cfg.TrustedPrefixes()),
	httpx.SecurityHeaders,
	httpx.BodyLimit(1 << 20),
	svc.IPFilter,     // func(http.Handler) http.Handler – Jail/Deny/Land, für alle Pfade
	/* Stream A: Session-Loader, CSRF */
	svc.APIRateLimit, // func(http.Handler) http.Handler – nur /api/, 300/min pro Session bzw. IP-Key
)
```

**Routen (innerhalb von `rt.Route("/api", func(api *rbac.Router) { … })`):**

```go
defense.NewHandler(svc).Routes(api) // 11 Routen /security/{overview,attempts,jails,ip-rules,settings,agent}
notify.NewHandler(notify.HandlerDeps{
	DB: db, Box: box, Dispatcher: disp, Audit: auditRecorder, Clock: clock.Real{}, Dev: cfg.Dev,
}).Routes(api) // 5 Routen /security/notifiers…
```

**Übergaben an andere Streams:**
- Stream A erhält `svc` als `defense.LoginGuard`, `geo` als `defense.GeoResolver` (Land für Sessions) und `disp` als `notify.Notifier`. Vertrag mit A: `CheckLogin` vor der Passwortprüfung, `CheckAdminNetwork` nach erfolgreichem Passwort für Admins, `CheckSecondFactor(ctx, ip, strconv.FormatInt(sessionID, 10))` vor jeder Code-/Assertion-Prüfung, `RecordAttempt` für jeden Versuch mit den Stream-A-`reason`-Werten (`complete` = vollständige Anmeldung). Fehlerabbildung in A: `defense.ErrJailed` → `403 jailed`; `*defense.RateLimitError` → `429 rate_limited` mit `Retry-After: defense.RetryAfterSeconds(err.RetryAfter)`. A sendet `EventNewDeviceLogin` nicht selbst (macht `RecordAttempt`).
- Stream D erhält `disp` als `notify.Notifier` (Events `rbac.changed`, `user.created`, `user.deleted`, `audit.chain_broken`).

**CLI `panel admin unlock` (Phase 2):**

```go
// nach store.Open + store.Migrate; auditRecorder = Stream-D-SQL-Recorder
res, err := defense.Unlock(ctx, db, auditRecorder, clock.Real{}, stdout) // (defense.UnlockResult, error)
```
Exit-Code 1 bei `err != nil`; die Funktion gibt selbst aus, was geändert wurde.

**Änderungswunsch an den Lead (OpenAPI, Phase 0 schreibgeschützt):** In `api/openapi.yaml` bei `testNotifier` (`POST /security/notifiers/{id}/test`) `'400': { $ref: '#/components/responses/BadRequest' }` ergänzen (Zustellfehler → `400 validation_failed`, siehe Global Constraints). Stream E sollte die Meldung im UI anzeigen.
