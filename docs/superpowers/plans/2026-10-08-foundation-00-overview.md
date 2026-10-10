# mc-panel Fundament + Security – Implementierungsplan (Übersicht)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das Fundament von mc-panel bauen: Go-Monorepo mit `panel` und `agent`, Login mit Passwort + TOTP/Passkeys, Sessions, Rechte pro Server, Audit-Log mit Hash-Kette, Abwehr (Rate-Limits, Jail, IP-Regeln, GeoIP, nftables) und Security Center im React-Frontend.

**Architecture:** `panel` (unprivilegiert, chi + SQLite, React via `go:embed`) spricht über einen Unix-Socket mit `agent` (privilegiert, nftables). Alle Querschnittsverträge (Schema, OpenAPI, Interfaces, Router mit Rechte-Regeln) entstehen in Phase 0; danach arbeiten sechs Streams parallel in eigenen Worktrees und teilen sich keine Dateien. Phase 2 verdrahtet alles in `cmd/panel`.

**Tech Stack:** Go (aktuelle stabile Version via Homebrew), chi v5, modernc.org/sqlite, goose v3, sqlc, x/crypto/argon2, pquerna/otp, go-webauthn/webauthn, oschwald/maxminddb-golang/v2, google/nftables, BurntSushi/toml, x/time/rate · React 19 + TypeScript + Vite, TanStack Router/Query, Tailwind v4, shadcn/ui, react-i18next, openapi-typescript + openapi-fetch, MSW, Vitest, Playwright · GoReleaser, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-08-mc-panel-foundation-design.md`

## Plan-Dateien und Reihenfolge

| Datei | Phase | Wer | Abhängig von |
|---|---|---|---|
| `2026-10-08-foundation-01-phase0-contracts.md` | 0 | Lead, sequenziell | – |
| `2026-10-08-foundation-02-stream-a-auth.md` | 1 | Teammate A | Phase 0 |
| `2026-10-08-foundation-03-stream-b-defense.md` | 1 | Teammate B | Phase 0 |
| `2026-10-08-foundation-04-stream-c-agent.md` | 1 | Teammate C | Phase 0 |
| `2026-10-08-foundation-05-stream-d-rbac-audit.md` | 1 | Teammate D | Phase 0 |
| `2026-10-08-foundation-06-stream-e-frontend.md` | 1 | Teammate E | Phase 0 |
| `2026-10-08-foundation-07-stream-f-ci-release.md` | 1 | Teammate F | Phase 0 |
| `2026-10-08-foundation-08-phase2-integration.md` | 2 | Lead | alle Streams gemerged |

Phase-1-Streams laufen parallel, jeder in eigenem Git-Worktree auf Branch `stream/<x>`, abgezweigt vom Phase-0-Stand auf `main`. Merge-Reihenfolge nach Fertigstellung: F, C, D, B, A, E (geringste zu höchste Kopplung). Jeder Stream-Branch muss vor dem Merge `make test` und `make lint` grün haben.

## Global Constraints

- Go-Modulpfad: `github.com/Bernotese/mc-panel`.
- Kein CGO: `CGO_ENABLED=0` für alle Builds; SQLite nur über `modernc.org/sqlite`.
- Release-Ziele: `linux/amd64`, `linux/arm64`. Entwicklung: `darwin/arm64`.
- Zeitstempel in der DB: `INTEGER` Unix-Millisekunden (`*_at`-Spalten), Konvertierung per `time.UnixMilli` / `t.UnixMilli()`.
- Alle Zeitabhängigkeiten über `clock.Clock` (nie `time.Now()` direkt in Geschäftslogik).
- Schema-Änderungen nur in Phase 0 (`migrations/`). Streams dürfen Migrationen **nicht** ändern oder hinzufügen; Bedarf → Lead fragen.
- Jeder Stream legt SQL-Queries nur in seiner eigenen Datei `internal/store/queries/<stream>.sql` an (`auth.sql`, `defense.sql`, `rbac.sql`, `audit.sql`) und committet die generierte `internal/store/<stream>.sql.go`. `sqlc.yaml` setzt `emit_interface: false`, damit keine geteilte Datei entsteht.
- Phase-0-Dateien sind für Streams schreibgeschützt (Liste in Phase-0-Plan, Abschnitt „Eigentum“). Bedarf an Änderungen → Lead.
- Routen werden mit vollem Pfad relativ zu `/api` auf dem übergebenen `*rbac.Router` registriert; Streams rufen nie `Route()` auf.
- sqlc-Query-Namen tragen ein Präfix: `Auth…` (A), `Defense…` (B), `Rbac…`/`Audit…` (D).
- Keine neuen Go-Abhängigkeiten außer `internal/deps/deps.go`; Go-Tests nur mit der Standardbibliothek.
- Fehlerformat JSON: `{"error":"<code>","message":"<text>"}`, Codes ausschließlich aus `internal/httpx/errors.go`.
- API-Präfix `/api`, JSON in `snake_case`, IDs als `integer` (int64).
- Session-Cookie `__Host-sid`, CSRF-Header `X-CSRF-Token`.
- Passwort: argon2id m=64 MiB, t=3, p=2, Salt 16 Byte, Key 32 Byte; Länge 12–256.
- Session: Idle 30 min, absolut 12 h; Pre-Auth 5 min; Sudo 10 min.
- Jail: 5 Fehlversuche / 10 min → 15 min, Verdopplung bei erneuter Sperre innerhalb 30 Tagen, max. 7 Tage.
- Rate-Limits: Login 10/min Burst 5 pro IP; 5/min pro Benutzername; 2FA 5/min pro Pre-Auth-Session+IP; API 300/min pro Session.
- Body-Limit 1 MiB.
- Notifier-Dedup: gleicher Event-Typ + IP höchstens alle 5 min.
- Recovery-Codes: 10 Stück à 10 Zeichen aus `abcdefghjkmnpqrstuvwxyz23456789`.
- UI-Sprachen: `de` (Standard), `en`.
- Frontend: keine Inline-Skripte, keine externen CDNs.
- Commits: Conventional Commits, Abschluss mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **IPv4-mapped IPv6 und IPv6-Rotation:** `::ffff:203.0.113.7` und `203.0.113.7` müssen als dieselbe IP gelten (`netip.Addr.Unmap()`), und IPv6-Clients werden für Rate-Limits und Jail auf ihr `/64` normalisiert – sonst umgeht ein Angreifer das Jail durch Adresswechsel. → Test in Phase 0 (`httpx.IPKey`) und Stream B (Jail: zwei Adressen aus demselben /64 teilen sich einen Zähler).
2. **Letzter Admin:** Löschen, Herabstufen oder das Entfernen der letzten 2FA-Methode des letzten Admins muss abgelehnt werden (`409 conflict`), sonst sperrt sich der Betreiber aus. → Tests in Stream D (Users) und Stream A (2FA entfernen).
3. **TOTP-Replay über Kontexte:** Derselbe TOTP-Code darf nach Login nicht noch einmal für Sudo gelten (gemeinsamer `totp_last_step`). → Test in Stream A.
4. **Agent nicht erreichbar:** Startet das panel ohne agent oder fällt er aus, müssen Login, Jail in der App und UI weiter funktionieren; Bans werden mit `synced_at IS NULL` nachgetragen, sobald der agent wieder antwortet. → Test in Stream B (Sync-Loop mit Fake-Client, der erst fehlschlägt).
5. **Parallele Audit-Schreibzugriffe:** 50 gleichzeitige `Record`-Aufrufe müssen eine lückenlose, gültige Kette ergeben. → Test in Stream D.
