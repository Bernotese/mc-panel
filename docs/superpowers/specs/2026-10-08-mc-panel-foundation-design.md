# mc-panel – Teilprojekt 1: Fundament + Security (Design-Spec)

- **Datum:** 2026-10-08
- **Status:** Entwurf, wartet auf Review
- **Autor:** Jannes (mit Claude)

## 1. Ziel und Kontext

mc-panel ist ein selbst gebautes Web-Panel (Lern-/Hobbyprojekt) zur Verwaltung eigener Minecraft-Server auf einem öffentlich erreichbaren Linux-VPS. Weil das Panel aus dem Internet erreichbar ist, hat Sicherheit oberste Priorität und soll in der App selbst sichtbar und auswertbar sein.

Entwickelt wird lokal auf macOS; das fertige Produkt wird später per Installer auf dem VPS deployed.

### 1.1 Gesamt-Roadmap

| # | Teilprojekt | Inhalt (Kurz) |
|---|---|---|
| **1** | **Fundament + Security** | **Repo, panel/agent, Auth, 2FA, Sessions, Rechte, Audit, Abwehr, Security Center** |
| 2 | Instanzen-Lifecycle | Docker + `itzg/minecraft-server`, Versionen/Engines, Ressourcenlimits, Ports/Firewall, Config-Editor, Auto-Restart |
| 3 | Live-Betrieb | Konsole (xterm.js/WebSocket), Logs, Dateibrowser, Plugins/Mods (Modrinth, Hangar), Spieler-Verwaltung |
| 4 | Backups | Zeitpläne, Retention, Offsite via restic, konsistente Snapshots, Restore |
| 5 | Monitoring + Alerts | Spieler, TPS/MSPT, Ressourcen, Historie, Alerts (Discord, ntfy) |
| 6 | Installer-CLI | `install`/`update`/`doctor`/`uninstall`, Caddy + Let's Encrypt, VPS-Härtung |

Jedes Teilprojekt hat einen eigenen Zyklus Spec → Plan → Umsetzung. **Dieses Dokument spezifiziert nur Teilprojekt 1.**

### 1.2 Nicht-Ziele (gesamt)

Multi-Node-Betrieb, Billing, Mandanten/Kundenkonten, öffentliche Registrierung.

### 1.3 Nicht-Ziele (Teilprojekt 1)

Docker/Minecraft-Instanzen, Installer, Caddy-Konfiguration, Backups, Monitoring. Server existieren nur als Stub-Datensätze (für Rechte-Tests).

## 2. Technologie-Entscheidungen

| Bereich | Wahl | Begründung |
|---|---|---|
| Backend | Go | Statische Binaries, gute Docker-SDK, ressourcenschonend |
| HTTP | `chi` | Schlank, idiomatisch, Middleware-Ökosystem |
| DB | SQLite via `modernc.org/sqlite` | Kein CGO → einfaches Cross-Compile; ein VPS braucht keine DB-Server |
| Queries/Migrationen | `sqlc`, `goose` | Typsichere SQL, versionierte Migrationen |
| Logging | `log/slog` (JSON in Prod) | Standardbibliothek |
| 2FA | `pquerna/otp`, `go-webauthn/webauthn` | Etablierte Bibliotheken |
| Passwort | `golang.org/x/crypto/argon2` (argon2id) | Aktueller Standard |
| Frontend | React + TypeScript + Vite | Größtes Ökosystem (xterm.js, Monaco, Charts später) |
| Frontend-Libs | TanStack Router/Query, Tailwind, shadcn/ui, react-i18next | Typsicheres Routing, Daten-Caching, schnelle UI, de/en |
| API-Vertrag | OpenAPI 3.1 (`api/openapi.yaml`), TS-Typen via `openapi-typescript` | Frontend und Backend bleiben synchron |
| Release | GoReleaser | Cross-Compile, Checksums, GitHub Releases |
| Reverse Proxy (Prod, TP 6) | Caddy | Automatisches Let's Encrypt, HSTS, HTTP/3 |

## 3. Architektur

```
Internet ─443─▶ Caddy ─▶ /run/mc-panel/panel.sock  panel  (User mcpanel, SQLite, React via go:embed)
                                            │  Unix-Socket (0660, Gruppe mcpanel)
                                            │  + Peer-UID-Prüfung
                                            ▼
                                          agent  (root bzw. docker-Gruppe)
                                            • Health
                                            • firewall.ban / unban / list (nftables-Set)
                                            • (TP 2+: Docker-Operationen)
```

### 3.1 Prozesse

- **panel**: öffentliche Web-App + API. Läuft unprivilegiert, lauscht in Prod auf dem Unix-Socket `/run/mc-panel/panel.sock` (0660, nur Caddy erreicht ihn; `listen = "unix:…"`, Dev: `127.0.0.1:8080`). Kennt den Docker-Socket nicht.
- **agent**: kleine, privilegierte Komponente mit enger, strikt validierter JSON-API über Unix-Socket. Akzeptiert nur Verbindungen von der konfigurierten panel-UID (Peer-Credentials). Jede Anfrage wird gegen ein Schema validiert; unbekannte Felder werden abgelehnt.
- Ziel: Eine Schwachstelle im panel führt nicht direkt zu Root auf dem Host.

### 3.2 Repo-Struktur

```
cmd/panel/            main: HTTP-Server + CLI-Subcommands (serve, admin create, admin unlock, audit verify)
cmd/agent/            main: Agent-Daemon
internal/config/      Konfiguration (Datei + Env), Dev-/Prod-Profil
internal/store/       DB-Zugriff (sqlc-generiert) + Store-Interfaces
internal/auth/        Passwort, TOTP, WebAuthn, Recovery, Sessions, CSRF, Sudo
internal/rbac/        Rollen, Rechte pro Server, Middleware
internal/users/       Benutzerverwaltung
internal/audit/       Audit-Log mit Hash-Kette
internal/defense/     Rate-Limits, Jail, IP-Regeln, GeoIP
internal/notify/      Notifier-Interface, Discord, ntfy
internal/agent/       Agent-Server, Agent-Client, Firewall-Backends, Peer-Cred
internal/httpx/       Gemeinsame HTTP-Helfer (Fehlerformat, Client-IP, Security-Header)
internal/clock/       Clock-Interface (testbare Zeit)
migrations/           goose-Migrationen
api/openapi.yaml      API-Vertrag
web/                  React-App (Vite)
```

Konvention: Jedes Feature-Paket exportiert `Routes(r chi.Router, deps …)`; nur `cmd/panel` verdrahtet die Pakete.

### 3.3 Plattform-Abstraktion

Linux-spezifisches liegt hinter Interfaces, Implementierungen per Build-Tag:

| Interface | Linux | macOS (Dev) |
|---|---|---|
| `Firewall` | nftables (Sets `mcpanel_jail4` und `mcpanel_jail6` in eigener Tabelle `inet mcpanel`) | No-op, loggt nur |
| Peer-Credentials | `SO_PEERCRED` | `LOCAL_PEERCRED` (`getpeereid`) |

### 3.4 Konfiguration

- Prod: `/etc/mc-panel/panel.toml`, `/etc/mc-panel/agent.toml`, Daten unter `/var/lib/mc-panel/`, Socket `/run/mc-panel/agent.sock`.
- Dev (`--dev`): alles unter `./.dev/`, Socket `./.dev/agent.sock`, Logs als Text.
- Secrets (z. B. Webhook-URLs) werden in der DB gespeichert, im UI nur maskiert angezeigt.
- `trusted_proxies`: standardmäßig nur `127.0.0.1/32` und `::1/128`; nur von dort wird `X-Forwarded-For` übernommen. Verbindungen über den Unix-Socket gelten immer als vom Proxy kommend (X-Forwarded-For, rechtester nicht vertrauenswürdiger Eintrag; ohne verwertbares X-Forwarded-For wird der Request abgewiesen). Loopback ist nur im Dev-Modus von der Admin-Allowlist ausgenommen.

### 3.5 Entwicklung und Release

- `make dev`: startet agent + panel mit `--dev` und den Vite-Dev-Server (Proxy `/api` → panel). Browser behandeln `localhost` als sicheren Kontext, daher funktionieren `Secure`/`__Host-`-Cookies.
- Linux-VM (OrbStack/Lima, Ubuntu LTS) für produktionsnahe Tests (nftables, systemd).
- `make release`: Frontend-Build → `go:embed` → GoReleaser für `linux/amd64` und `linux/arm64` mit SHA256-Checksums.

## 4. Authentifizierung und Sessions

### 4.1 Passwörter

- argon2id (m=64 MiB, t=3, p=2; Parameter im Hash-String gespeichert, Re-Hash bei Parameteränderung beim nächsten Login).
- Mindestlänge 12, Maximallänge 256, Abgleich mit eingebetteter Liste häufiger Passwörter (z. B. Top-100k).
- Unbekannter Benutzer: Dummy-Hash wird trotzdem berechnet; Fehlermeldung immer „Benutzername oder Passwort falsch“.

### 4.2 Zweiter Faktor

- **TOTP** (RFC 6238, 30 s, 6 Stellen, ±1 Schritt Toleranz, Replay-Schutz: zuletzt genutzter Zeitschritt wird gespeichert). Secret verschlüsselt in der DB (AES-GCM, Schlüssel aus Server-Secret-Datei).
- **WebAuthn/Passkeys**: mehrere Credentials pro Benutzer, benennbar, löschbar. RP-ID = konfigurierte Domain (Dev: `localhost`).
- **Recovery-Codes**: 10 Stück, je 10 Zeichen, nur gehasht gespeichert, einmalig verwendbar, neu generierbar (Sudo).
- **Pflicht**: Admins müssen 2FA einrichten, bevor sie irgendetwas anderes tun können (erzwungener Setup-Flow). Für `user` per globaler Einstellung erzwingbar.

### 4.3 Login-Ablauf

1. `POST /api/auth/login` (Benutzername, Passwort) → bei Erfolg Pre-Auth-Session (nur für 2FA-Schritt gültig, 5 min).
2. `POST /api/auth/2fa/totp` | `/api/auth/2fa/webauthn/*` | `/api/auth/2fa/recovery` → volle Session.
3. Neue Session-ID nach jedem Privilegienwechsel (Schutz vor Session Fixation).

Jeder Versuch (Erfolg/Fehler, Schritt, IP, User-Agent, Benutzername) wird in `login_attempts` protokolliert.

### 4.4 Sessions

- Serverseitig in SQLite; Cookie enthält 256-Bit-Zufallstoken, DB speichert nur SHA-256 davon.
- Cookie: `__Host-sid`, `HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/`.
- Idle-Timeout 30 min, absolute Laufzeit 12 h (konfigurierbar).
- Gespeichert: Benutzer, erstellt, zuletzt aktiv, IP, User-Agent, Land (GeoIP), Sudo-bis.
- Profilseite: Liste aktiver Sessions, einzelne oder alle anderen widerrufen. Passwortänderung widerruft alle anderen Sessions.

### 4.5 Sudo-Modus

Sensible Aktionen verlangen eine 2FA-Bestätigung, die höchstens 10 min alt ist. Dazu gehören: Benutzer anlegen/löschen, Rollen und Rechte ändern, 2FA-Methoden ändern, Recovery-Codes neu generieren, Jails aufheben, IP-Regeln und Notifier ändern (später: Dateien schreiben, Plugins installieren). API antwortet sonst mit `403 {"error":"sudo_required"}`, das Frontend zeigt den Bestätigungsdialog.

### 4.6 CSRF und Header

- `SameSite=Strict` + Prüfung von `Sec-Fetch-Site` (nur `same-origin`/`none`) bzw. `Origin` bei zustandsändernden Requests + Synchronizer-Token (`X-CSRF-Token`, an Session gebunden).
- Security-Header vom panel selbst (unabhängig von Caddy): strikte CSP (`default-src 'self'`, kein Inline-Script), `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `Permissions-Policy` minimal, `frame-ancestors 'none'`.
- Request-Body-Limit (Standard 1 MiB), Timeouts für Header/Read/Write.

## 5. Rechte

- Rollen: `admin` (alle Rechte, global) und `user` (nur explizit vergebene Rechte).
- Rechte pro Server: `server.view`, `server.power`; vorgesehen für spätere Teilprojekte: `server.console`, `server.files`, `server.plugins`, `server.backups`.
- Deny-by-default; neue Benutzer haben keine Rechte.
- Durchsetzung in der Middleware `rbac.Require(perm, serverIDFromPath)` – jede Route deklariert ihr Recht; ein Test stellt sicher, dass keine Route ohne Rechte-Deklaration registriert ist.
- Tabelle `servers` existiert als Stub (id, name), damit Rechte und UI testbar sind.

## 6. Audit-Log

- Append-only Tabelle: id, Zeitpunkt, Akteur (User-ID oder `system`/`cli`), Aktion (z. B. `user.create`), Ziel, IP, Ergebnis, Details (JSON), `prev_hash`, `hash`.
- `hash = SHA-256(prev_hash || kanonisches JSON des Eintrags)`; Schreiben serialisiert in einer Transaktion.
- `panel audit verify` prüft die gesamte Kette und meldet den ersten fehlerhaften Eintrag.
- Kein Update/Delete über die API. Optionale Aufbewahrungsdauer wird in einem späteren Teilprojekt behandelt.

## 7. Abwehr und Security Center

### 7.1 Rate-Limits

Token-Bucket (In-Memory, pro Prozess):

| Ziel | Schlüssel | Limit (Standard) |
|---|---|---|
| Login | IP | 10/min, Burst 5 |
| Login | Benutzername | 5/min |
| 2FA | Pre-Auth-Session + IP | 5/min |
| API allgemein | Session | 300/min |

Überschreitung → `429` mit `Retry-After`.

### 7.2 Jail

- Fehlversuche (Passwort oder 2FA) pro IP: 5 in 10 min → Sperre 15 min.
- Wiederholungstäter: Dauer verdoppelt sich pro erneuter Sperre innerhalb von 30 Tagen, maximal 7 Tage.
- Gesperrte IPs erhalten `403` für alle Requests (Middleware vor Routing) und werden über den agent in das nftables-Set eingetragen (mit Timeout = Sperrdauer). Bei agent-Ausfall bleibt die App-Sperre wirksam; Sync wird nachgeholt.
- Konten werden nie gesperrt (verhindert, dass Angreifer den Admin aussperren).
- Allowlist-IPs werden nie gejailt.
- Admin kann Jails aufheben (Sudo) oder manuell IPs sperren.

### 7.3 IP-Regeln und GeoIP

- Allow-/Denylist mit CIDR (IPv4/IPv6), optional mit Kommentar und Ablaufdatum.
- Optional „Admin-Zugang nur aus Allowlist“: Benutzer mit Rolle `admin` können sich nur aus erlaubten Netzen anmelden.
- Optional Länderfilter (erlaubte Länder) über lokale DB-IP-Lite-Datenbank (MMDB, kein API-Key nötig). Ohne Datenbank: Feature deaktiviert, Länderspalten leer.
- Schutz vor Selbstaussperrung: Änderung wird abgelehnt, wenn die aktuelle IP des Admins dadurch ausgeschlossen würde. Notfall: `panel admin unlock` per CLI.

### 7.4 Security Center (UI)

- Diagramm Login-Versuche (Erfolg/Fehler) über 24 h / 7 d / 30 d.
- Top-IPs und Top-Länder der Fehlversuche.
- Aktive Jails (IP, Grund, seit, bis, Anzahl) mit Aufheben.
- Logins von neuen Geräten/IPs.
- Aktive Sessions aller Benutzer (Admin) mit Widerruf.
- Audit-Log mit Filtern (Akteur, Aktion, Zeitraum) und Kettenstatus.
- Konfiguration: IP-Regeln, Länderfilter, Notifier.

### 7.5 Benachrichtigungen

- Interface `Notifier.Send(ctx, Event)`; Implementierungen: Discord-Webhook, ntfy (URL + Topic + optional Token).
- Events: Login von neuem Gerät/neuer IP, Jail ausgelöst, Rolle/Rechte geändert, Benutzer angelegt/gelöscht, 2FA geändert, Audit-Kette ungültig.
- Versand asynchron mit Retry und Deduplizierung (gleiche Ereignisart + IP höchstens alle 5 min); Fehler landen im Log, nicht beim Nutzer.
- „Test senden“-Button im UI.

## 8. Frontend

- Seiten: Login, 2FA-Challenge, erzwungenes 2FA-Setup (TOTP-QR, Passkey, Recovery-Codes anzeigen + Bestätigung), Dashboard (Platzhalter mit Server-Stubs), Benutzerverwaltung mit Rechte-Matrix (Benutzer × Server × Recht), Security Center, Audit-Log, Profil (Passwort, 2FA-Methoden, Sessions).
- Sudo-Dialog global: reagiert auf `sudo_required` und wiederholt die Aktion nach Bestätigung.
- CSRF-Token wird vom Fetch-Wrapper automatisch mitgesendet.
- Dark Mode (System-Standard + Umschalter), mobil bedienbar, Sprachen de/en.
- Kein Inline-Script, keine externen CDNs (CSP-konform).
- API-Typen generiert aus `api/openapi.yaml`; während Phase 1 laufen Mocks über MSW gegen diesen Vertrag.

## 9. Fehlerbehandlung

- Einheitliches JSON-Fehlerformat: `{"error": "<code>", "message": "<lesbar>"}`; interne Fehler geben keine Details preis, sondern eine Request-ID, die im Log steht.
- agent-Fehler werden im panel als `503 agent_unavailable` gemeldet; sicherheitsrelevante Funktionen der App (Jail in der App) funktionieren weiter.
- DB-Migrationen laufen beim Start; schlägt eine fehl, startet das panel nicht.

## 10. Tests

- **TDD** für alle Go-Pakete.
- Unit: Passwort-Hashing/Policy, TOTP inkl. Replay, Recovery-Codes, Session-Ablauf (mit Fake-Clock), Rate-Limiter, Jail-Eskalation, CIDR-Regeln, Hash-Kette, RBAC-Entscheidungen, Notifier-Dedup.
- Integration (`httptest` + echte SQLite-Datei): kompletter Login inkl. TOTP, CSRF-Ablehnung, `403` ohne Recht, Jail nach 5 Fehlversuchen, Sudo-Pflicht, Session-Widerruf, Route-ohne-Rechte-Check.
- agent: Tests mit Fake-Firewall; nftables-Integrationstest nur unter Linux (Build-Tag, CI/VM).
- Frontend: Vitest + Testing Library für Logik/Komponenten; Playwright-E2E für Login + TOTP-Setup gegen echtes Backend.
- CI (GitHub Actions): `go test ./...`, `golangci-lint`, `govulncheck`, `gosec`, `npm ci && npm test && npm run build`, `npm audit --audit-level=high`.

## 11. Umsetzung mit parallelen Teammates

**Phase 0 – Verträge (sequenziell, Lead):** Repo-Scaffold, `go.mod`, Makefile, alle Migrationen, `api/openapi.yaml` vollständig, Interfaces (`Store`-Teilinterfaces, `Notifier`, `Firewall`, `AgentClient`, `Clock`), `internal/httpx`, Vite-Scaffold. Danach teilen sich die Streams keine Dateien.

**Phase 1 – parallel, je eigener Git-Worktree, TDD:**

| Stream | Inhalt | Pfade |
|---|---|---|
| A Auth | Passwort, TOTP, WebAuthn, Recovery, Sessions, CSRF, Sudo | `internal/auth/` |
| B Abwehr | Rate-Limiter, Jail, IP-Regeln, GeoIP, Notifier | `internal/defense/`, `internal/notify/` |
| C Agent | Socket-Server, Peer-Cred, Firewall nftables/no-op, Client | `cmd/agent/`, `internal/agent/` |
| D Rechte & Audit | Users-CRUD, RBAC-Middleware, Audit-Hash-Kette, CLI-Befehle | `internal/rbac/`, `internal/audit/`, `internal/users/` |
| E Frontend | Alle Seiten gegen OpenAPI mit MSW-Mocks | `web/` |
| F CI/Release | GitHub Actions, Lint-Konfiguration, GoReleaser | `.github/`, `.golangci.yml`, `.goreleaser.yaml` |

**Phase 2 – Integration (Lead):** `cmd/panel` verdrahten, Frontend gegen echte API, Integrations- und E2E-Tests, Security-Review, Code-Review.

## 12. Abnahmekriterien

1. `make test` grün; `golangci-lint`, `govulncheck`, `gosec` ohne Befunde.
2. `make dev` auf macOS → UI unter `http://localhost:5173` erreichbar.
3. `panel admin create` → erzwungenes 2FA-Setup → Login mit Passwort + TOTP und mit Passkey funktioniert.
4. Eingeschränkter Benutzer mit `server.power` für Stub-Server A sieht nur Server A; Zugriff auf Server B → `403`.
5. 5 falsche Logins → Jail im Security Center sichtbar, ntfy-/Discord-Testbenachrichtigung kommt an; in der Linux-VM zeigt `nft list set inet mcpanel mcpanel_jail4` (bzw. `mcpanel_jail6`) die IP.
6. Alle Aktionen stehen im Audit-Log, `panel audit verify` meldet OK; nach manueller Manipulation einer Zeile schlägt `verify` fehl.
7. `make release` erzeugt Linux-Binaries (amd64/arm64) mit eingebettetem Frontend; agent + panel starten in der Linux-VM.
