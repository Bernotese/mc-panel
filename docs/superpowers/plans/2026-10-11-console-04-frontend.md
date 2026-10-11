# Teilprojekt 3a – Frontend (Aufgaben 14–18) – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Konsole (Live-Log per SSE, Befehle mit Antwort-Blöcken), Spieler-Tab (Online, Whitelist, Ops, Bans mit Aktionen) und die Karte „Online-Spieler“ auf der Übersicht der Server-Detailseite – vollständig gegen den MSW-Mock (V5) getestet, bereit für das echte panel aus Aufgaben 9–13.

**Architecture:** Zwei neue Feature-Module. `web/src/features/console/` trennt reine Logik (Parser für ANSI-SGR und Minecraft-`§`-Codes → Segmente, Log-Reducer mit Ringpuffer und Duplikat-Schutz über die SSE-Event-ID, Befehlsverlauf, Befehlsprüfung) von React: der Hook `useConsoleStream` kapselt `EventSource`, Bündelung der Zeilen und Neuverbindung (bei endgültig geschlossenem Stream fragt er den Status einmal per `fetch` ab, weil `EventSource` keine Statuscodes liefert); `LogView` rendert virtualisiert (`@tanstack/react-virtual`). `web/src/features/players/` enthält Query-Optionen, Validierung, die Aktions-Mutation, den Spieler-Tab und die Online-Karte. Die Detailseite (`web/src/features/servers/server-detail-page.tsx`) bekommt gesteuerte Tabs „Konsole“ und „Spieler“ (nur mit `server.console`), die Übersicht die Online-Karte.

**Tech Stack:** React 19 + TypeScript, TanStack Query v5, `@tanstack/react-virtual` (einzige neue Abhängigkeit), shadcn/ui (vorhandene Komponenten), react-i18next, sonner, lucide-react, openapi-fetch (`parseAs: 'stream'`), MSW 2, Vitest + Testing Library + user-event, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-console-players-design.md` (§7 Frontend, §6.1–6.5 API, §3.3 RCON-Rauschen)

**Übersicht/Verträge:** `docs/superpowers/plans/2026-10-11-console-00-overview.md` (V3 panel-API, V5 MSW-Mock, Abschnitt „Ausführung“) und `docs/superpowers/plans/2026-10-11-console-01-contracts.md` (Phase A: Aufgabe 1 entfernt Ops/Whitelist aus Formular und Texten, Aufgabe 3 liefert TS-Typen, `KNOWN_CODES`, Fehlertexte und die MSW-Mocks – inklusive der dort beschriebenen Abweichung von V5).

## Global Constraints

Es gelten alle Global Constraints aus der Übersicht (und damit TP1/TP2 sowie `CLAUDE.md`). Frontend-spezifisch zusätzlich:

- **Eigentum (B3):** nur `web/src/features/servers/**`, `web/src/features/console/**` (neu), `web/src/features/players/**` (neu), `web/src/i18n/**`, `web/e2e/**`, `web/package.json`, `web/package-lock.json`. **Nicht** anfassen: `web/src/api/**`, `web/src/mocks/**`, `web/src/test/**`, `web/playwright.config.ts`, Go-Code, `api/openapi.yaml`. Braucht es dort etwas → Lead fragen (er bringt es auf `main`, danach `git rebase origin/main`).
- **Lokal nur `make check`** (Format, Vet, Build, `tsc`). `npm run lint`, `npm test`, `npx vitest`, `npm run e2e`, `make test` laufen **nur in der CI**. Ablauf jeder Aufgabe: Test schreiben → `make check` (rot = Kompilierfehler, weil Modul/Export fehlt) → implementieren → `make check` grün → Commit → Push → `gh pr checks <pr> --watch`; bei Fehlern `gh run view <run-id> --log-failed`. Eine Aufgabe ist fertig, wenn die CI ihres Commits grün ist.
- **Neue Abhängigkeit:** nur `@tanstack/react-virtual` (Aufgabe 15, `cd web && npm install @tanstack/react-virtual`; `package.json` bekommt die von npm gewählte Caret-Version, `package-lock.json` wird mit committet). Keine weiteren npm-Pakete, keine neuen shadcn-Komponenten.
- **Kein `dangerouslySetInnerHTML`**, kein `innerHTML`; Log-Text wird nur als React-Text in `<span>`s gerendert. Keine Inline-Skripte, keine CDNs. Inline-`style` (für die Virtualisierung) ist erlaubt (CSP `style-src 'self' 'unsafe-inline'`).
- **Rechte:** Tabs „Konsole“ und „Spieler“ nur, wenn `server.permissions` `server.console` enthält (Admins bekommen vom panel alle Rechte). Die Online-Karte erscheint für jeden, der die Detailseite sieht (`server.view`).
- **Zahlen aus der Spec:** Ringpuffer `MAX_ENTRIES = 5000`; Befehlsverlauf `MAX_HISTORY = 100`; Befehl 1–1000 Byte UTF-8 nach Entfernen genau eines führenden `/`, keine Zeichen `< 0x20` und kein `0x7f`, nicht nur Leerzeichen; Spielername `^[A-Za-z0-9_]{3,16}$`; Grund ≤ 200 Zeichen (Codepoints), druckbar, nur bei `kick`/`ban`; Online-Spieler alle 15 s (`ONLINE_POLL_MS`), nur bei sichtbarem Dokument (TanStack-Default `refetchIntervalInBackground: false`).
- **Stream-Timing (Hook):** Zeilen werden gebündelt (`flushMs = 50`); nach endgültigem Abbruch bei Status 200 neu verbinden nach `reconnectMs = 3000`; bei 429 nach `Retry-After` (mindestens `reconnectMs`), bei 5xx/Netzfehler nach `retryMs = 10000`; bei 401/403/404 keine Neuverbindung.
- **Darstellung:** Log-Fläche immer dunkel (`bg-zinc-950 text-zinc-100`, Terminal-Optik in beiden Farbschemata), Monospace. Farbklassen nur als statische Tailwind-Strings.
- **i18n:** alle Texte in `de` (Standard) und `en` (Typ `Messages` erzwingt gleiche Schlüssel). Neue Namensräume `console.*`, `players.*`; neue Schlüssel `servers.detail.tabs.console|players`. Zur Laufzeit gebaute Schlüssel in `DYNAMIC_KEYS` (`web/src/i18n/i18n.test.ts`). `errors.instance_not_running|rcon_unavailable` und ihre `ERROR_CODES`-Einträge legt Phase A an (nicht doppelt anlegen – doppelte Objektschlüssel brechen `tsc`).
- **Lint-Regeln, die hier greifen:** `react-hooks` v7 (keine synchronen `setState`-Aufrufe im Effekt-Körper, keine `ref.current`-Zugriffe beim Rendern), `react-refresh/only-export-components` (`.tsx` exportiert nur Komponenten; Konstanten/Hooks in `.ts`), `no-control-regex` (Steuerzeichen per Codepoint prüfen, nicht per Regex), `verbatimModuleSyntax` (`import type`).
- **Commits:** Conventional Commits, Trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`, nur die Pfade der Aufgabe.

## Annahmen über Phase A (Stand beim Start)

Maßgeblich ist `docs/superpowers/plans/2026-10-11-console-01-contracts.md`. Fehlt etwas davon auf `main`, **nicht selbst ergänzen**, sondern den Lead fragen.

- `web/src/api/schema.d.ts` enthält V3 (Operationen `streamConsole`, `sendConsoleCommand`, `getOnlinePlayers`, `getPlayerLists`, `playerAction`; `ServerProperties` ohne `whitelist`/`ops`; Fehler-Enum mit `instance_not_running`, `rcon_unavailable`).
- `web/src/api/types.ts` exportiert `ConsoleCommandRequest`, `ConsoleCommandResponse`, `OnlinePlayer`, `OnlinePlayers`, `PlayerOp`, `PlayerEntry`, `PlayerBan`, `PlayerLists`, `PlayerListFile` (= `PlayerLists['errors'][number]`), `PlayerAction`, `PlayerActionRequest` sowie
  ```ts
  export interface ConsoleLineEvent { ts: number; stream: 'out' | 'err'; line: string }
  export interface ConsoleStateEvent { running: boolean }
  ```
- `web/src/api/errors.ts`: `KNOWN_CODES` enthält `instance_not_running` und `rcon_unavailable`. `de.ts`/`en.ts` haben `errors.instance_not_running` (`Der Server läuft nicht.`) und `errors.rcon_unavailable` (`Die Server-Konsole antwortet gerade nicht – startet der Server noch?`), `i18n.test.ts` hat beide in `ERROR_CODES`.
- Aufgabe 1 (Phase A) hat aus Formular, Mock und Texten `whitelist`/`ops` entfernt (`servers.props.whitelist|whitelist_hint|ops|ops_hint`, `servers.validation.players_invalid|players_too_many` gelöscht, `servers.props.lists_sync_hint` → `servers.props.lists_hint` mit Verweis auf den Tab „Spieler“). `server-detail-page.test.tsx` enthält `LISTS_HINT` mit dem neuen Text.
- MSW-Mock (`web/src/mocks/handlers/console.ts`, `MockServer.players`): `survival` (id 1, läuft) hat online `Alex` und `Steve` (beide mit UUID), Whitelist `Alex`, `Steve`, einen Op, einen Ban `Griefer123` (Grund `Griefing`, `expires: 'forever'`); `creative` (id 2, gestoppt) und `lobby` haben leere Listen. `sendConsoleCommand`: `list` → `There are 2 of a max of 20 players online: Alex, Steve`, `say …` → `""`, sonst `Unknown or incomplete command…`; gestoppt → `409 instance_not_running`. `playerAction` verändert den Zustand (kick → `Kicked <name>: …` und aus `online` entfernt; ban → Ban **mit dem übergebenen Grund**, aus `online` entfernt; `whitelist_add` → Eintrag mit dem Namen; `op` → Level 4).
- **Abweichung von V5:** `streamConsole` ist ein `http.get` mit `ReadableStream` (`text/event-stream`), nicht `sse()`; er sendet `state` und zwölf Startzeilen (u. a. ANSI, `§a`, `WARN`, `ERROR` auf stderr, zwei RCON-Rauschzeilen), dann alle 2 s `[HH:MM:SS INFO]: Tick <n>`. Im Browser (Playwright, `make preview`) liefert der Service Worker ihn an die echte `EventSource`. In Vitest gibt es keine `EventSource`; die Tests ersetzen sie durch `FakeEventSource` (Aufgabe 15) und nutzen den Mock-Stream nicht.

## Review Focus

1. **Doppelte Zeilen nach Neuverbindung:** Die native Neuverbindung von `EventSource` schickt `Last-Event-ID`, eine vom Hook neu erzeugte `EventSource` nicht (der Server liefert dann wieder `tail` 500). Erwartet: Zeilen mit einer Event-ID ≤ der zuletzt gesehenen werden verworfen; ein Container-Neustart zeigt keine Dopplungen. → Aufgabe 14 (`log-model.test.ts`: `drops lines it has already seen`), Aufgabe 15 (`use-console-stream.test.ts`: `reconnects after the stream was closed and skips lines it already has`).
2. **Stilles Aufgeben bei 401/403:** `EventSource` meldet nur `error` mit `readyState = CLOSED`. Erwartet: einmalige Statusabfrage; 401 → Abmeldung über die API-Middleware, 403 → „Keine Berechtigung“ ohne Endlosschleife, 503 → „Konsole nicht erreichbar“ und späterer Neuversuch. → Aufgabe 15 (Hook-Tests `gives up after a 403`, `retries later while the agent is unavailable`).
3. **Feindlicher Log-Inhalt:** Ein Plugin schreibt `<img src=x onerror=alert(1)>`, ANSI-Sequenzen ohne Abschluss, `§`-Codes oder Steuerzeichen ins Log. Erwartet: alles als Text, kein HTML-Element, kaputte Sequenzen verschwinden. → Aufgabe 14 (`ansi.test.ts`), Aufgabe 15 (`renders markup in log lines as text`).
4. **Zeilenflut:** 500 Zeilen `tail` beim Verbinden, danach schnelle Ausgabe. Erwartet: Bündelung (ein Render pro 50 ms), Puffer bleibt bei 5000 Einträgen, Liste virtualisiert. → Aufgabe 14 (`keeps at most MAX_ENTRIES entries`), Aufgabe 15 (Hook bündelt; `LogView` mit Virtualisierung).
5. **Aktionen auf gestopptem Server und ungültige Namen:** Erwartet: alle Änderungs-Knöpfe gesperrt mit Hinweis; Name `a b` oder ein Grund mit Zeilenumbruch wird vor dem Senden abgelehnt. → Aufgabe 16 (`locks every change while the server is stopped`, `adds a name to the whitelist after checking it`, `validation.test.ts`), Aufgabe 15 (`locks the command line while the server is stopped`).

## Dateistruktur

```
web/
  package.json, package-lock.json               (15) @tanstack/react-virtual
  e2e/servers.spec.ts                           (18) Konsole + Spieler (Admin), Online-Karte ohne Konsole (alice)
  src/
    i18n/de.ts, en.ts, i18n.test.ts             (15–17) console.*, players.*, Tabs, Fehlercodes, DYNAMIC_KEYS
    features/
      console/
        ansi.ts                                 (14) parseFormatted, plainText, Segment, LogColor
        log-model.ts                            (14) LogEntry, logReducer, visibleEntries, parseLineEvent, …
        history.ts                              (14) Befehlsverlauf
        command.ts                              (14) normalizeCommand, commandProblem
        api.ts                                  (15) consoleStreamUrl, probeConsoleStream, sendConsoleCommand
        use-console-stream.ts                   (15) Hook (EventSource, Bündelung, Neuverbindung)
        log-text.tsx                            (15) Segmente → <span>s
        log-view.tsx                            (15) virtualisierte Liste, Auto-Scroll, „Neue Zeilen“
        command-input.tsx                       (15) Befehlszeile mit Verlauf
        console-tab.tsx                         (15) Tab „Konsole“
        testing/fake-event-source.ts            (15) EventSource-Ersatz für jsdom
        testing/layout.ts                       (15) Viewport-Stub für die Virtualisierung
      players/
        api.ts                                  (16) Keys, Query-Optionen, playerAction, ONLINE_POLL_MS
        validation.ts                           (16) Name/Grund
        use-player-action.ts                    (16) Mutation + Toast + Invalidierung
        name-form.tsx                           (16) Name hinzufügen (Whitelist, Ops)
        ban-dialog.tsx                          (16) Ban mit optionalem Grund
        players-tab.tsx                         (16) Tab „Spieler“
        online-players-card.tsx                 (17) Karte „Online-Spieler“
      servers/
        server-detail-page.tsx (+ .test.tsx)    (15–16) gesteuerte Tabs, Konsole, Spieler
        server-overview.tsx                     (17) Online-Karte
```

---

### Task 14: Reine Konsolen-Logik – Formatierung, Log-Modell, Verlauf, Befehlsprüfung

**Files:**
- Create: `web/src/features/console/ansi.ts`, `web/src/features/console/log-model.ts`, `web/src/features/console/history.ts`, `web/src/features/console/command.ts`
- Test: `web/src/features/console/ansi.test.ts`, `web/src/features/console/log-model.test.ts`, `web/src/features/console/history.test.ts`, `web/src/features/console/command.test.ts`

**Interfaces:**
- Consumes: `ConsoleLineEvent`, `ConsoleStateEvent` aus `@/api/types` (Phase A)
- Produces:
```ts
// ansi.ts
export type LogColor = 'black' | 'red' | 'green' | 'yellow' | 'blue' | 'magenta' | 'cyan' | 'white'
  | 'bright-black' | 'bright-red' | 'bright-green' | 'bright-yellow' | 'bright-blue' | 'bright-magenta' | 'bright-cyan' | 'bright-white'
export interface Segment { text: string; color?: LogColor; bold?: true; italic?: true; underline?: true; strike?: true; obfuscated?: true }
export function parseFormatted(input: string): Segment[]
export function plainText(input: string): string

// log-model.ts
export const MAX_ENTRIES = 5000
export type LogLevel = 'info' | 'warn' | 'error'
export type LogStreamName = ConsoleLineEvent['stream']
export interface IncomingLine extends ConsoleLineEvent { id: string }
export interface LineEntry { kind: 'line'; key: string; ts: number; stream: LogStreamName; text: string; segments: Segment[]; level: LogLevel; rcon: boolean }
export type CommandStatus = 'pending' | 'ok' | 'error'
export interface CommandEntry { kind: 'command'; key: string; id: number; command: string; status: CommandStatus; output: string; segments: Segment[] }
export type LogEntry = LineEntry | CommandEntry
export interface LogState { entries: LogEntry[]; nextKey: number; lastId: bigint | null }
export type LogAction =
  | { type: 'lines'; lines: readonly IncomingLine[] }
  | { type: 'command'; id: number; command: string }
  | { type: 'command_done'; id: number; ok: boolean; output: string }
export function initialLogState(): LogState
export function logReducer(state: LogState, action: LogAction): LogState
export function isRconNoise(text: string): boolean
export function detectLevel(text: string, stream: LogStreamName): LogLevel
export function eventId(id: string): bigint | null
export function parseLineEvent(data: string, lastEventId: string): IncomingLine | null
export function parseStateEvent(data: string): ConsoleStateEvent | null
export interface LogFilter { query: string; hideRcon: boolean }
export function visibleEntries(entries: readonly LogEntry[], filter: LogFilter): readonly LogEntry[]
export function entryText(entry: LogEntry): string

// history.ts
export const MAX_HISTORY = 100
export interface HistoryState { readonly items: readonly string[]; readonly index: number | null; readonly draft: string }
export const emptyHistory: HistoryState
export function pushHistory(h: HistoryState, command: string): HistoryState
export function historyUp(h: HistoryState, current: string): [HistoryState, string]
export function historyDown(h: HistoryState, current: string): [HistoryState, string]

// command.ts
export const MAX_COMMAND_BYTES = 1000
export type CommandProblem = 'empty' | 'too_long' | 'control'
export function normalizeCommand(input: string): string
export function commandProblem(input: string): CommandProblem | null
```

- [ ] **Step 1: Worktree, Abhängigkeiten, Draft-PR vorbereiten**

```bash
git fetch origin
git worktree add ../mc-panel-wt/feat/console-frontend -b feat/console-frontend origin/main
cd ../mc-panel-wt/feat/console-frontend/web && npm ci
```

Prüfen, dass Phase A da ist (sonst Lead fragen): `grep -n "ConsoleLineEvent\|PlayerActionRequest" src/api/types.ts` und `grep -n "instance_not_running" src/api/errors.ts` liefern Treffer.

- [ ] **Step 2: Tests für den Formatierungs-Parser schreiben**

`web/src/features/console/ansi.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { parseFormatted, plainText } from './ansi'

const ESC = '\u001b'

describe('parseFormatted', () => {
  it('returns plain text as one segment', () => {
    expect(parseFormatted('[10:00:00 INFO]: Done')).toEqual([{ text: '[10:00:00 INFO]: Done' }])
  })

  it('returns no segments for an empty line', () => {
    expect(parseFormatted('')).toEqual([])
  })

  it('applies ANSI foreground colours and resets', () => {
    expect(parseFormatted(`${ESC}[33mWARN${ESC}[0m done`)).toEqual([{ text: 'WARN', color: 'yellow' }, { text: ' done' }])
  })

  it('combines bold with bright colours and clears them separately', () => {
    expect(parseFormatted(`${ESC}[1;91mERR${ESC}[22m x${ESC}[39my`)).toEqual([
      { text: 'ERR', color: 'bright-red', bold: true },
      { text: ' x', color: 'bright-red' },
      { text: 'y' },
    ])
  })

  it('treats an empty SGR parameter list as reset', () => {
    expect(parseFormatted(`${ESC}[32mok${ESC}[m.`)).toEqual([{ text: 'ok', color: 'green' }, { text: '.' }])
  })

  it('skips 256-colour and true-colour parameters without applying them', () => {
    expect(parseFormatted(`${ESC}[38;5;208mX${ESC}[38;2;1;2;3mY${ESC}[39mZ`)).toEqual([{ text: 'XYZ' }])
  })

  it('drops other CSI sequences, lone escapes and unterminated sequences', () => {
    expect(parseFormatted(`a${ESC}[2Kb${ESC}c${ESC}[12`)).toEqual([{ text: 'abc' }])
  })

  it('applies Minecraft colour and format codes', () => {
    expect(parseFormatted('§aGrün §lfett§r normal')).toEqual([
      { text: 'Grün ', color: 'bright-green' },
      { text: 'fett', color: 'bright-green', bold: true },
      { text: ' normal' },
    ])
  })

  it('lets a Minecraft colour code reset the formats like the game does', () => {
    expect(parseFormatted('§l§cX')).toEqual([{ text: 'X', color: 'bright-red' }])
  })

  it('accepts upper-case codes and maps all format codes', () => {
    expect(parseFormatted('§A§K1§r§M2§r§N3§r§O4')).toEqual([
      { text: '1', color: 'bright-green', obfuscated: true },
      { text: '2', strike: true },
      { text: '3', underline: true },
      { text: '4', italic: true },
    ])
  })

  it('drops unknown codes and a trailing section sign', () => {
    expect(parseFormatted('§zX§')).toEqual([{ text: 'X' }])
  })

  it('removes control characters but keeps tabs', () => {
    expect(parseFormatted('a\u0007b\tc\u007f\u0000d')).toEqual([{ text: 'ab\tcd' }])
  })

  it('keeps markup as plain text', () => {
    expect(parseFormatted('<img src=x onerror=alert(1)>')).toEqual([{ text: '<img src=x onerror=alert(1)>' }])
  })
})

describe('plainText', () => {
  it('strips all formatting', () => {
    expect(plainText(`§6There are ${ESC}[1m2${ESC}[0m players`)).toBe('There are 2 players')
  })
})
```

- [ ] **Step 3: Tests für das Log-Modell schreiben**

`web/src/features/console/log-model.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import {
  detectLevel,
  entryText,
  eventId,
  initialLogState,
  isRconNoise,
  logReducer,
  MAX_ENTRIES,
  parseLineEvent,
  parseStateEvent,
  visibleEntries,
  type IncomingLine,
  type LogEntry,
  type LogState,
} from './log-model'

function line(id: string, text: string, stream: 'out' | 'err' = 'out'): IncomingLine {
  return { id, ts: 1_760_000_000_000, stream, line: text }
}

function texts(state: LogState): string[] {
  return state.entries.map((e) => (e.kind === 'line' ? e.text : `> ${e.command}`))
}

describe('logReducer', () => {
  it('appends lines with stable keys, plain text, level and RCON flag', () => {
    const s = logReducer(initialLogState(), {
      type: 'lines',
      lines: [line('1000', '§a[10:00:00 INFO]: Done'), line('1001', '[10:00:01 WARN]: Can\'t keep up!'), line('1002', 'Thread RCON Client /127.0.0.1 started')],
    })
    expect(s.entries.map((e) => e.key)).toEqual(['l0', 'l1', 'l2'])
    const [done, warn, rcon] = s.entries as Extract<LogEntry, { kind: 'line' }>[]
    expect(done.text).toBe('[10:00:00 INFO]: Done')
    expect(done.segments).toEqual([{ text: '[10:00:00 INFO]: Done', color: 'bright-green' }])
    expect(done.level).toBe('info')
    expect(warn.level).toBe('warn')
    expect(rcon.rcon).toBe(true)
    expect(s.lastId).toBe(1002n)
  })

  it('drops lines it has already seen', () => {
    let s = logReducer(initialLogState(), { type: 'lines', lines: [line('1000', 'a'), line('1001', 'b')] })
    s = logReducer(s, { type: 'lines', lines: [line('999', 'old'), line('1000', 'a'), line('1001', 'b'), line('1002', 'c')] })
    expect(texts(s)).toEqual(['a', 'b', 'c'])
  })

  it('accepts lines without a usable event id', () => {
    const s = logReducer(initialLogState(), { type: 'lines', lines: [line('', 'a'), line('x', 'b')] })
    expect(texts(s)).toEqual(['a', 'b'])
    expect(s.lastId).toBeNull()
  })

  it('returns the same state when nothing new arrived', () => {
    const s = logReducer(initialLogState(), { type: 'lines', lines: [line('5', 'a')] })
    expect(logReducer(s, { type: 'lines', lines: [line('5', 'a')] })).toBe(s)
  })

  it('keeps at most MAX_ENTRIES entries', () => {
    const lines = Array.from({ length: MAX_ENTRIES + 3 }, (_, i) => line(String(i + 1), `line ${i + 1}`))
    const s = logReducer(initialLogState(), { type: 'lines', lines })
    expect(s.entries).toHaveLength(MAX_ENTRIES)
    expect(texts(s)[0]).toBe('line 4')
    expect(texts(s).at(-1)).toBe(`line ${MAX_ENTRIES + 3}`)
  })

  it('tracks a command from pending to its answer', () => {
    let s = logReducer(initialLogState(), { type: 'command', id: 1, command: 'list' })
    expect(s.entries).toEqual([{ kind: 'command', key: 'c1', id: 1, command: 'list', status: 'pending', output: '', segments: [] }])
    s = logReducer(s, { type: 'command_done', id: 1, ok: true, output: '§6There are 0 of a max of 20 players online: ' })
    expect(s.entries[0]).toMatchObject({ status: 'ok', segments: [{ text: 'There are 0 of a max of 20 players online: ', color: 'yellow' }] })
  })

  it('stores a failed command as plain error text', () => {
    let s = logReducer(initialLogState(), { type: 'command', id: 7, command: 'list' })
    s = logReducer(s, { type: 'command_done', id: 7, ok: false, output: '§cnot formatted' })
    expect(s.entries[0]).toMatchObject({ status: 'error', output: '§cnot formatted', segments: [{ text: '§cnot formatted' }] })
  })

  it('ignores the answer of a command that left the buffer', () => {
    const s = logReducer(initialLogState(), { type: 'lines', lines: [line('1', 'a')] })
    expect(logReducer(s, { type: 'command_done', id: 99, ok: true, output: 'x' }).entries).toEqual(s.entries)
  })
})

describe('classification', () => {
  it('detects RCON connection noise', () => {
    expect(isRconNoise('[09:32:51 INFO]: Thread RCON Client /0:0:0:0:0:0:0:1 started')).toBe(true)
    expect(isRconNoise('[09:32:51 INFO]: Thread RCON Client /127.0.0.1 shutting down')).toBe(true)
    expect(isRconNoise('[09:32:51 INFO]: RCON running on 0.0.0.0:25575')).toBe(false)
  })

  it('detects warning and error levels of Paper and Vanilla log formats', () => {
    expect(detectLevel('[10:00:00 WARN]: x', 'out')).toBe('warn')
    expect(detectLevel('[10:00:00] [Server thread/WARN]: x', 'out')).toBe('warn')
    expect(detectLevel('[10:00:00 ERROR]: x', 'out')).toBe('error')
    expect(detectLevel('[10:00:00] [Server thread/FATAL]: x', 'out')).toBe('error')
    expect(detectLevel('[10:00:00 INFO]: WARN in text is fine', 'out')).toBe('info')
    expect(detectLevel('java.lang.Exception', 'err')).toBe('warn')
    expect(detectLevel('[10:00:00 ERROR]: x', 'err')).toBe('error')
  })
})

describe('events', () => {
  it('parses event ids as nanosecond numbers', () => {
    expect(eventId('1760000000123456789')).toBe(1760000000123456789n)
    expect(eventId('')).toBeNull()
    expect(eventId('12a')).toBeNull()
    expect(eventId('123456789012345678901')).toBeNull()
  })

  it('parses line events and rejects malformed data', () => {
    expect(parseLineEvent('{"ts":1,"stream":"err","line":"x"}', '42')).toEqual({ id: '42', ts: 1, stream: 'err', line: 'x' })
    expect(parseLineEvent('not json', '1')).toBeNull()
    expect(parseLineEvent('null', '1')).toBeNull()
    expect(parseLineEvent('{"ts":"1","stream":"out","line":"x"}', '1')).toBeNull()
    expect(parseLineEvent('{"ts":1,"stream":"stdout","line":"x"}', '1')).toBeNull()
    expect(parseLineEvent('{"ts":1,"stream":"out","line":5}', '1')).toBeNull()
  })

  it('parses state events', () => {
    expect(parseStateEvent('{"running":false}')).toEqual({ running: false })
    expect(parseStateEvent('{"running":"yes"}')).toBeNull()
    expect(parseStateEvent('[')).toBeNull()
  })
})

describe('visibleEntries', () => {
  const base = logReducer(
    logReducer(initialLogState(), {
      type: 'lines',
      lines: [line('1', 'Done (3.2s)!'), line('2', 'Thread RCON Client /127.0.0.1 started'), line('3', "Can't keep up!")],
    }),
    { type: 'command', id: 1, command: 'list' },
  )
  const state = logReducer(base, { type: 'command_done', id: 1, ok: true, output: 'There are 2 players' })

  it('hides RCON noise on request', () => {
    expect(visibleEntries(state.entries, { query: '', hideRcon: true })).toHaveLength(3)
    expect(visibleEntries(state.entries, { query: '', hideRcon: false })).toHaveLength(4)
  })

  it('filters lines and command blocks case-insensitively', () => {
    expect(visibleEntries(state.entries, { query: 'KEEP', hideRcon: true }).map(entryText)).toEqual(["Can't keep up!"])
    expect(visibleEntries(state.entries, { query: '2 players', hideRcon: true }).map((e) => e.key)).toEqual(['c1'])
    expect(visibleEntries(state.entries, { query: '  ', hideRcon: false })).toHaveLength(4)
  })

  it('renders entries as copyable text', () => {
    expect(state.entries.map(entryText)).toEqual(['Done (3.2s)!', 'Thread RCON Client /127.0.0.1 started', "Can't keep up!", '› list\nThere are 2 players'])
  })
})
```

- [ ] **Step 4: Tests für Verlauf und Befehlsprüfung schreiben**

`web/src/features/console/history.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { emptyHistory, historyDown, historyUp, MAX_HISTORY, pushHistory } from './history'

describe('command history', () => {
  it('walks back and forth and restores the draft', () => {
    let h = pushHistory(pushHistory(emptyHistory, 'list'), 'say hi')
    let value: string
    ;[h, value] = historyUp(h, 'dra')
    expect(value).toBe('say hi')
    ;[h, value] = historyUp(h, value)
    expect(value).toBe('list')
    ;[h, value] = historyUp(h, value)
    expect(value).toBe('list')
    ;[h, value] = historyDown(h, value)
    expect(value).toBe('say hi')
    ;[h, value] = historyDown(h, value)
    expect(value).toBe('dra')
    expect(h.index).toBeNull()
    ;[h, value] = historyDown(h, 'dra')
    expect(value).toBe('dra')
  })

  it('does nothing without entries', () => {
    expect(historyUp(emptyHistory, 'x')).toEqual([emptyHistory, 'x'])
  })

  it('skips empty commands and direct repetitions', () => {
    const h = pushHistory(pushHistory(pushHistory(emptyHistory, 'list'), 'list'), '')
    expect(h.items).toEqual(['list'])
  })

  it('keeps the last MAX_HISTORY commands', () => {
    let h = emptyHistory
    for (let i = 1; i <= MAX_HISTORY + 5; i++) h = pushHistory(h, `cmd ${i}`)
    expect(h.items).toHaveLength(MAX_HISTORY)
    expect(h.items[0]).toBe('cmd 6')
  })

  it('resets the position after a new command', () => {
    let h = pushHistory(emptyHistory, 'a')
    ;[h] = historyUp(h, '')
    h = pushHistory(h, 'b')
    expect(h).toEqual({ items: ['a', 'b'], index: null, draft: '' })
  })
})
```

`web/src/features/console/command.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { commandProblem, MAX_COMMAND_BYTES, normalizeCommand } from './command'

describe('normalizeCommand', () => {
  it('removes exactly one leading slash', () => {
    expect(normalizeCommand('/list')).toBe('list')
    expect(normalizeCommand('//list')).toBe('/list')
    expect(normalizeCommand('list')).toBe('list')
  })
})

describe('commandProblem', () => {
  it('accepts normal commands with and without slash', () => {
    expect(commandProblem('list')).toBeNull()
    expect(commandProblem('/say Grüße an alle')).toBeNull()
  })

  it('rejects empty commands and a lone slash', () => {
    expect(commandProblem('')).toBe('empty')
    expect(commandProblem('   ')).toBe('empty')
    expect(commandProblem('/')).toBe('empty')
  })

  it('rejects control characters', () => {
    expect(commandProblem('say a\nop me')).toBe('control')
    expect(commandProblem('say a\tb')).toBe('control')
    expect(commandProblem('say \u0000')).toBe('control')
    expect(commandProblem('say \u007f')).toBe('control')
  })

  it('counts UTF-8 bytes after removing the slash', () => {
    expect(commandProblem('/' + 'a'.repeat(MAX_COMMAND_BYTES))).toBeNull()
    expect(commandProblem('a'.repeat(MAX_COMMAND_BYTES + 1))).toBe('too_long')
    expect(commandProblem('ä'.repeat(MAX_COMMAND_BYTES / 2))).toBeNull()
    expect(commandProblem('ä'.repeat(MAX_COMMAND_BYTES / 2) + 'a')).toBe('too_long')
  })
})
```

- [ ] **Step 5: Rot prüfen**

Run: `make check`
Expected: FAIL – `tsc` meldet fehlende Module `./ansi`, `./log-model`, `./history`, `./command`.

- [ ] **Step 6: Parser implementieren**

`web/src/features/console/ansi.ts`:

```ts
/** Colours of ANSI SGR 30–37/90–97; Minecraft § colours are mapped onto the same 16 names. */
export type LogColor =
  | 'black'
  | 'red'
  | 'green'
  | 'yellow'
  | 'blue'
  | 'magenta'
  | 'cyan'
  | 'white'
  | 'bright-black'
  | 'bright-red'
  | 'bright-green'
  | 'bright-yellow'
  | 'bright-blue'
  | 'bright-magenta'
  | 'bright-cyan'
  | 'bright-white'

export interface Segment {
  text: string
  color?: LogColor
  bold?: true
  italic?: true
  underline?: true
  strike?: true
  obfuscated?: true
}

type Style = Omit<Segment, 'text'>

const BASE: readonly LogColor[] = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white']
const BRIGHT: readonly LogColor[] = [
  'bright-black',
  'bright-red',
  'bright-green',
  'bright-yellow',
  'bright-blue',
  'bright-magenta',
  'bright-cyan',
  'bright-white',
]

const MC_COLORS: Readonly<Record<string, LogColor>> = {
  '0': 'black',
  '1': 'blue',
  '2': 'green',
  '3': 'cyan',
  '4': 'red',
  '5': 'magenta',
  '6': 'yellow',
  '7': 'white',
  '8': 'bright-black',
  '9': 'bright-blue',
  a: 'bright-green',
  b: 'bright-cyan',
  c: 'bright-red',
  d: 'bright-magenta',
  e: 'bright-yellow',
  f: 'bright-white',
}

function sameStyle(a: Style, b: Style): boolean {
  return (
    a.color === b.color &&
    a.bold === b.bold &&
    a.italic === b.italic &&
    a.underline === b.underline &&
    a.strike === b.strike &&
    a.obfuscated === b.obfuscated
  )
}

function withMinecraftFormat(style: Style, code: string): Style | null {
  switch (code) {
    case 'k':
      return { ...style, obfuscated: true }
    case 'l':
      return { ...style, bold: true }
    case 'm':
      return { ...style, strike: true }
    case 'n':
      return { ...style, underline: true }
    case 'o':
      return { ...style, italic: true }
    default:
      return null
  }
}

/** Applies one SGR parameter list ("1;31"); unsupported parameters (backgrounds, 256/true colour) are skipped. */
function applySgr(style: Style, params: string): Style {
  const codes = params === '' ? [0] : params.split(';').map((p) => (p === '' ? 0 : Number(p)))
  let next: Style = { ...style }
  for (let k = 0; k < codes.length; k++) {
    const c = codes[k]
    if (c === 0) next = {}
    else if (c === 1) next.bold = true
    else if (c === 3) next.italic = true
    else if (c === 4) next.underline = true
    else if (c === 9) next.strike = true
    else if (c === 22) delete next.bold
    else if (c === 23) delete next.italic
    else if (c === 24) delete next.underline
    else if (c === 29) delete next.strike
    else if (c >= 30 && c <= 37) next.color = BASE[c - 30]
    else if (c === 39) delete next.color
    else if (c >= 90 && c <= 97) next.color = BRIGHT[c - 90]
    else if (c === 38 || c === 48) k += codes[k + 1] === 5 ? 2 : codes[k + 1] === 2 ? 4 : 0
  }
  return next
}

/**
 * Splits a log line into styled segments: ANSI SGR sequences and Minecraft § codes set the style, every other
 * escape sequence and control character (except tab) is dropped. The result is rendered as text only.
 */
export function parseFormatted(input: string): Segment[] {
  const out: Segment[] = []
  let style: Style = {}
  let text = ''

  const flush = () => {
    if (!text) return
    const last = out.at(-1)
    if (last && sameStyle(last, style)) last.text += text
    else out.push({ ...style, text })
    text = ''
  }
  const setStyle = (next: Style) => {
    flush()
    style = next
  }

  for (let i = 0; i < input.length; i++) {
    const ch = input[i]
    if (ch === '\u001b') {
      if (input[i + 1] !== '[') continue // lone ESC: drop it
      let j = i + 2
      while (j < input.length) {
        const code = input.charCodeAt(j)
        if (code >= 0x40 && code <= 0x7e) break // final byte of the CSI sequence
        j++
      }
      if (j >= input.length) break // unterminated sequence: drop the rest
      if (input[j] === 'm') setStyle(applySgr(style, input.slice(i + 2, j)))
      i = j
      continue
    }
    if (ch === '§') {
      const code = input[i + 1]?.toLowerCase()
      if (code === undefined) continue
      i += 1
      if (Object.hasOwn(MC_COLORS, code)) setStyle({ color: MC_COLORS[code] })
      else if (code === 'r') setStyle({})
      else {
        const formatted = withMinecraftFormat(style, code)
        if (formatted) setStyle(formatted)
      }
      continue
    }
    const c = input.charCodeAt(i)
    if ((c < 0x20 && ch !== '\t') || c === 0x7f) continue
    text += ch
  }
  flush()
  return out
}

export function plainText(input: string): string {
  return parseFormatted(input)
    .map((s) => s.text)
    .join('')
}
```

- [ ] **Step 7: Log-Modell implementieren**

`web/src/features/console/log-model.ts`:

```ts
import type { ConsoleLineEvent, ConsoleStateEvent } from '@/api/types'
import { parseFormatted, plainText, type Segment } from './ansi'

/** Ring buffer size of the console (spec §7.2). */
export const MAX_ENTRIES = 5000

export type LogLevel = 'info' | 'warn' | 'error'
export type LogStreamName = ConsoleLineEvent['stream']

/** A `line` event of the console stream plus its SSE id (Docker timestamp in ns, as text). */
export interface IncomingLine extends ConsoleLineEvent {
  id: string
}

export interface LineEntry {
  kind: 'line'
  key: string
  ts: number
  stream: LogStreamName
  text: string
  segments: Segment[]
  level: LogLevel
  rcon: boolean
}

export type CommandStatus = 'pending' | 'ok' | 'error'

export interface CommandEntry {
  kind: 'command'
  key: string
  id: number
  command: string
  status: CommandStatus
  output: string
  segments: Segment[]
}

export type LogEntry = LineEntry | CommandEntry

export interface LogState {
  entries: LogEntry[]
  nextKey: number
  /** Highest event id seen so far; lines with a lower or equal id are duplicates of a reconnect. */
  lastId: bigint | null
}

export type LogAction =
  | { type: 'lines'; lines: readonly IncomingLine[] }
  | { type: 'command'; id: number; command: string }
  | { type: 'command_done'; id: number; ok: boolean; output: string }

const RCON_NOISE = /\bThread RCON Client \S+ (?:started|shutting down)\b/
const ERROR_MARK = /[\s/[](?:ERROR|FATAL|SEVERE)\]/
const WARN_MARK = /[\s/[](?:WARN|WARNING)\]/
const EVENT_ID = /^\d{1,20}$/

export function initialLogState(): LogState {
  return { entries: [], nextKey: 0, lastId: null }
}

/** The two lines every RCON call leaves in the server log (spec §3.3). */
export function isRconNoise(text: string): boolean {
  return RCON_NOISE.test(text)
}

export function detectLevel(text: string, stream: LogStreamName): LogLevel {
  if (ERROR_MARK.test(text)) return 'error'
  if (WARN_MARK.test(text) || stream === 'err') return 'warn'
  return 'info'
}

export function eventId(id: string): bigint | null {
  return EVENT_ID.test(id) ? BigInt(id) : null
}

function parseJson(data: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(data)
    return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : null
  } catch {
    return null
  }
}

export function parseLineEvent(data: string, lastEventId: string): IncomingLine | null {
  const value = parseJson(data)
  if (!value) return null
  const { ts, stream, line } = value
  if (typeof ts !== 'number' || typeof line !== 'string' || (stream !== 'out' && stream !== 'err')) return null
  return { id: lastEventId, ts, stream, line }
}

export function parseStateEvent(data: string): ConsoleStateEvent | null {
  const value = parseJson(data)
  return value && typeof value.running === 'boolean' ? { running: value.running } : null
}

function makeLine(incoming: IncomingLine, key: string): LineEntry {
  const segments = parseFormatted(incoming.line)
  const text = segments.map((s) => s.text).join('')
  return {
    kind: 'line',
    key,
    ts: incoming.ts,
    stream: incoming.stream,
    text,
    segments,
    level: detectLevel(text, incoming.stream),
    rcon: isRconNoise(text),
  }
}

function trim(entries: LogEntry[]): LogEntry[] {
  return entries.length > MAX_ENTRIES ? entries.slice(entries.length - MAX_ENTRIES) : entries
}

export function logReducer(state: LogState, action: LogAction): LogState {
  switch (action.type) {
    case 'lines': {
      let { nextKey, lastId } = state
      const added: LogEntry[] = []
      for (const incoming of action.lines) {
        const id = eventId(incoming.id)
        if (id !== null && lastId !== null && id <= lastId) continue
        if (id !== null) lastId = id
        added.push(makeLine(incoming, `l${nextKey}`))
        nextKey += 1
      }
      if (added.length === 0) return state
      return { entries: trim([...state.entries, ...added]), nextKey, lastId }
    }
    case 'command': {
      const entry: CommandEntry = {
        kind: 'command',
        key: `c${action.id}`,
        id: action.id,
        command: action.command,
        status: 'pending',
        output: '',
        segments: [],
      }
      return { ...state, entries: trim([...state.entries, entry]) }
    }
    case 'command_done':
      return {
        ...state,
        entries: state.entries.map((e) =>
          e.kind === 'command' && e.id === action.id
            ? {
                ...e,
                status: action.ok ? 'ok' : 'error',
                output: action.output,
                // Error texts come from the UI (translated), never from the game: no formatting codes to apply.
                segments: action.ok ? parseFormatted(action.output) : [{ text: action.output }],
              }
            : e,
        ),
      }
  }
}

export interface LogFilter {
  query: string
  hideRcon: boolean
}

export function visibleEntries(entries: readonly LogEntry[], filter: LogFilter): readonly LogEntry[] {
  const query = filter.query.trim().toLowerCase()
  if (!query && !filter.hideRcon) return entries
  return entries.filter((e) => {
    if (e.kind === 'line') {
      if (filter.hideRcon && e.rcon) return false
      return !query || e.text.toLowerCase().includes(query)
    }
    return !query || e.command.toLowerCase().includes(query) || plainText(e.output).toLowerCase().includes(query)
  })
}

export function entryText(entry: LogEntry): string {
  if (entry.kind === 'line') return entry.text
  const output = entry.status === 'error' ? entry.output : plainText(entry.output)
  return output ? `› ${entry.command}\n${output}` : `› ${entry.command}`
}
```

- [ ] **Step 8: Verlauf und Befehlsprüfung implementieren**

`web/src/features/console/history.ts`:

```ts
export const MAX_HISTORY = 100

/** index = position while browsing with ↑/↓ (null = editing the draft). */
export interface HistoryState {
  readonly items: readonly string[]
  readonly index: number | null
  readonly draft: string
}

export const emptyHistory: HistoryState = { items: [], index: null, draft: '' }

export function pushHistory(h: HistoryState, command: string): HistoryState {
  if (!command) return { ...h, index: null, draft: '' }
  const items = h.items.at(-1) === command ? h.items : [...h.items, command].slice(-MAX_HISTORY)
  return { items, index: null, draft: '' }
}

export function historyUp(h: HistoryState, current: string): [HistoryState, string] {
  if (h.items.length === 0) return [h, current]
  if (h.index === null) {
    const index = h.items.length - 1
    return [{ ...h, index, draft: current }, h.items[index]]
  }
  const index = Math.max(0, h.index - 1)
  return [{ ...h, index }, h.items[index]]
}

export function historyDown(h: HistoryState, current: string): [HistoryState, string] {
  if (h.index === null) return [h, current]
  if (h.index >= h.items.length - 1) return [{ ...h, index: null, draft: '' }, h.draft]
  const index = h.index + 1
  return [{ ...h, index }, h.items[index]]
}
```

`web/src/features/console/command.ts`:

```ts
/** Same rules as agentapi.ValidateCommand (spec §5.3); the panel strips one leading "/" first. */
export const MAX_COMMAND_BYTES = 1000

export type CommandProblem = 'empty' | 'too_long' | 'control'

export function normalizeCommand(input: string): string {
  return input.startsWith('/') ? input.slice(1) : input
}

export function commandProblem(input: string): CommandProblem | null {
  const command = normalizeCommand(input)
  if (command.trim() === '') return 'empty'
  for (const ch of command) {
    const code = ch.codePointAt(0) ?? 0
    if (code < 0x20 || code === 0x7f) return 'control'
  }
  if (new TextEncoder().encode(command).length > MAX_COMMAND_BYTES) return 'too_long'
  return null
}
```

- [ ] **Step 9: Grün prüfen, committen, Draft-PR anlegen**

Run: `make check`
Expected: PASS (kein `tsc`-Fehler).

```bash
git add web/src/features/console/ansi.ts web/src/features/console/ansi.test.ts \
  web/src/features/console/log-model.ts web/src/features/console/log-model.test.ts \
  web/src/features/console/history.ts web/src/features/console/history.test.ts \
  web/src/features/console/command.ts web/src/features/console/command.test.ts
git commit -m "feat(web): console log model, formatting parser, history and command checks" \
  -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push -u origin feat/console-frontend
gh pr create --draft --base main --title "feat(web): console and players (TP3a, phase B3)" --body "$(cat <<'EOF'
Frontend of sub-project 3a: console tab (live log via SSE, commands), players tab and the online players card.

Plan: docs/superpowers/plans/2026-10-11-console-04-frontend.md (tasks 14–18)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
gh pr checks --watch
```

Expected: alle Checks grün (die neuen Vitest-Dateien laufen im Job „web“).

---

### Task 15: Konsole-Tab – Stream-Hook, virtualisierte Log-Ansicht, Befehlszeile

**Files:**
- Modify: `web/package.json`, `web/package-lock.json` (`@tanstack/react-virtual`)
- Create: `web/src/features/console/api.ts`, `web/src/features/console/use-console-stream.ts`, `web/src/features/console/log-text.tsx`, `web/src/features/console/log-view.tsx`, `web/src/features/console/command-input.tsx`, `web/src/features/console/console-tab.tsx`, `web/src/features/console/testing/fake-event-source.ts`, `web/src/features/console/testing/layout.ts`
- Modify: `web/src/features/servers/server-detail-page.tsx`, `web/src/features/servers/server-detail-page.test.tsx`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts`
- Test: `web/src/features/console/use-console-stream.test.ts`, `web/src/features/console/console-tab.test.tsx`

**Interfaces:**
- Consumes: Aufgabe 14 (`logReducer`, `initialLogState`, `parseLineEvent`, `parseStateEvent`, `visibleEntries`, `entryText`, `LogEntry`, `Segment`, `LogColor`, `HistoryState`/`pushHistory`/`historyUp`/`historyDown`, `commandProblem`); `api`, `unwrap`, `toApiError`, `isApiError` (`@/api/client`, `@/api/errors`); `ConsoleCommandResponse`, `Server` (`@/api/types`); `errorMessage` (`@/lib/error-message`)
- Produces:
```ts
// api.ts
export function consoleStreamUrl(id: number): string               // '/api/servers/<id>/console/stream'
export function probeConsoleStream(id: number): Promise<unknown>    // null = stream answered 200, otherwise the error
export function sendConsoleCommand(id: number, command: string): Promise<ConsoleCommandResponse>

// use-console-stream.ts
export type ConnectionState = 'connecting' | 'live' | 'stopped' | 'reconnecting' | 'unavailable' | 'forbidden' | 'not_found' | 'unauthorized'
export interface StreamTiming { flushMs: number; reconnectMs: number; retryMs: number }
export const DEFAULT_TIMING: StreamTiming
export function classifyProbe(err: unknown, timing: StreamTiming): { state: ConnectionState; retryMs: number | null }
export interface ConsoleStream { entries: readonly LogEntry[]; connection: ConnectionState; addCommand(command: string): number; finishCommand(id: number, ok: boolean, output: string): void }
export function useConsoleStream(serverId: number, timing?: StreamTiming): ConsoleStream

// components
export function LogText(props: { segments: readonly Segment[] }): JSX.Element
export function LogView(props: { entries: readonly LogEntry[] }): JSX.Element
export function CommandInput(props: { disabled: boolean; onSubmit(command: string): void }): JSX.Element
export function ConsoleTab(props: { server: Server }): JSX.Element

// testing (nur Tests)
export class FakeEventSource { static instances; static reset(); static latest(); open(); emit(type, data, lastEventId?); fail(closed) }
export function stubLogViewport(): void
```

- [ ] **Step 1: Abhängigkeit installieren**

```bash
cd web && npm install @tanstack/react-virtual && cd ..
git diff --stat web/package.json web/package-lock.json
```

Expected: `package.json` enthält unter `dependencies` `"@tanstack/react-virtual": "^3.…"` (zum Planungszeitpunkt 3.14.14), `package-lock.json` ist aktualisiert.

- [ ] **Step 2: Test-Hilfen anlegen**

`web/src/features/console/testing/fake-event-source.ts`:

```ts
type Listener = (event: Event) => void

/** EventSource stand-in for jsdom (which has none). Tests drive it with open(), emit() and fail(). */
export class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  static instances: FakeEventSource[] = []

  readonly url: string
  readyState: number = FakeEventSource.CONNECTING
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  private readonly listeners = new Map<string, Set<Listener>>()

  constructor(url: string | URL) {
    this.url = String(url)
    FakeEventSource.instances.push(this)
  }

  static reset(): void {
    FakeEventSource.instances = []
  }

  static latest(): FakeEventSource {
    const latest = FakeEventSource.instances.at(-1)
    if (!latest) throw new Error('no EventSource was opened')
    return latest
  }

  addEventListener(type: string, listener: Listener): void {
    let set = this.listeners.get(type)
    if (!set) {
      set = new Set()
      this.listeners.set(type, set)
    }
    set.add(listener)
  }

  removeEventListener(type: string, listener: Listener): void {
    this.listeners.get(type)?.delete(listener)
  }

  close(): void {
    this.readyState = FakeEventSource.CLOSED
  }

  open(): void {
    this.readyState = FakeEventSource.OPEN
    this.onopen?.(new Event('open'))
  }

  emit(type: string, data: unknown, lastEventId = ''): void {
    const event = new MessageEvent(type, { data: JSON.stringify(data), lastEventId })
    for (const listener of this.listeners.get(type) ?? []) listener(event)
  }

  /** closed = true: the browser gave up (non-200 answer); false: it will reconnect by itself. */
  fail(closed: boolean): void {
    this.readyState = closed ? FakeEventSource.CLOSED : FakeEventSource.CONNECTING
    this.onerror?.(new Event('error'))
  }
}
```

`web/src/features/console/testing/layout.ts`:

```ts
import { vi } from 'vitest'

/** jsdom has no layout: give the log viewport (data-console-log) a size so the virtualizer renders rows. */
export function stubLogViewport(): void {
  const original = HTMLElement.prototype.getBoundingClientRect
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
    if (this.hasAttribute('data-console-log')) {
      return { x: 0, y: 0, top: 0, left: 0, right: 800, bottom: 600, width: 800, height: 600, toJSON: () => ({}) } as DOMRect
    }
    return original.call(this)
  })
  HTMLElement.prototype.scrollTo ??= function scrollTo() {}
}
```

- [ ] **Step 3: Hook-Tests schreiben**

`web/src/features/console/use-console-stream.test.ts`:

```ts
import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import type { LogEntry } from './log-model'
import { FakeEventSource } from './testing/fake-event-source'
import { classifyProbe, DEFAULT_TIMING, useConsoleStream, type StreamTiming } from './use-console-stream'
import { ApiError } from '@/api/errors'

const FAST: StreamTiming = { flushMs: 0, reconnectMs: 0, retryMs: 0 }
const STREAM = '*/api/servers/:id/console/stream'

function line(text: string) {
  return { ts: 1_760_000_000_000, stream: 'out', line: text }
}

function texts(entries: readonly LogEntry[]): string[] {
  return entries.map((e) => (e.kind === 'line' ? e.text : `> ${e.command}`))
}

beforeEach(() => {
  FakeEventSource.reset()
  vi.stubGlobal('EventSource', FakeEventSource)
  loginAs('admin')
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('useConsoleStream', () => {
  it('opens the stream of the server and collects lines in order', async () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    const es = FakeEventSource.latest()
    expect(es.url).toBe('/api/servers/1/console/stream')
    expect(result.current.connection).toBe('connecting')
    act(() => {
      es.open()
      es.emit('state', { running: true })
      es.emit('line', line('first'), '1000')
      es.emit('line', line('second'), '1001')
    })
    await waitFor(() => expect(texts(result.current.entries)).toEqual(['first', 'second']))
    expect(result.current.connection).toBe('live')
  })

  it('ignores malformed events', async () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    const es = FakeEventSource.latest()
    act(() => {
      es.open()
      es.emit('line', { ts: 'x', stream: 'out', line: 'bad' }, '1')
      es.emit('state', { running: 'maybe' })
      es.emit('line', line('good'), '2')
    })
    await waitFor(() => expect(texts(result.current.entries)).toEqual(['good']))
    expect(result.current.connection).toBe('live')
  })

  it('reports a stopped server', async () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    const es = FakeEventSource.latest()
    act(() => {
      es.open()
      es.emit('state', { running: false })
    })
    expect(result.current.connection).toBe('stopped')
  })

  it('shows a transient error as reconnecting and leaves the retry to the browser', () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    act(() => FakeEventSource.latest().fail(false))
    expect(result.current.connection).toBe('reconnecting')
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('reconnects after the stream was closed and skips lines it already has', async () => {
    server.use(http.get(STREAM, () => new HttpResponse(null, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })))
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    const first = FakeEventSource.latest()
    act(() => {
      first.open()
      first.emit('line', line('first'), '1000')
      first.emit('line', line('second'), '1001')
    })
    await waitFor(() => expect(result.current.entries).toHaveLength(2))
    act(() => first.fail(true))
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2))
    const second = FakeEventSource.latest()
    act(() => {
      second.open()
      second.emit('line', line('first'), '1000')
      second.emit('line', line('second'), '1001')
      second.emit('line', line('third'), '1002')
    })
    await waitFor(() => expect(texts(result.current.entries)).toEqual(['first', 'second', 'third']))
    expect(result.current.connection).toBe('live')
  })

  it('gives up after a 403', async () => {
    server.use(http.get(STREAM, () => HttpResponse.json({ error: 'forbidden', message: 'no' }, { status: 403 })))
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    act(() => FakeEventSource.latest().fail(true))
    await waitFor(() => expect(result.current.connection).toBe('forbidden'))
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('retries later while the agent is unavailable', async () => {
    server.use(http.get(STREAM, () => HttpResponse.json({ error: 'agent_unavailable', message: 'down' }, { status: 503 })))
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    act(() => FakeEventSource.latest().fail(true))
    await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThanOrEqual(2))
    expect(result.current.connection).toBe('unavailable')
  })

  it('closes the stream when the console is left', () => {
    const { unmount } = renderHook(() => useConsoleStream(1, FAST))
    const es = FakeEventSource.latest()
    unmount()
    expect(es.readyState).toBe(FakeEventSource.CLOSED)
  })

  it('tracks commands next to the log lines', () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    let id = 0
    act(() => {
      id = result.current.addCommand('list')
    })
    expect(result.current.entries).toMatchObject([{ kind: 'command', command: 'list', status: 'pending' }])
    act(() => result.current.finishCommand(id, true, 'There are 0 players'))
    expect(result.current.entries).toMatchObject([{ kind: 'command', status: 'ok', output: 'There are 0 players' }])
  })
})

describe('classifyProbe', () => {
  const err = (status: number, code: 'unauthorized' | 'forbidden' | 'not_found' | 'rate_limited' | 'agent_unavailable', retryAfterSeconds?: number) =>
    new ApiError({ status, code, message: code, retryAfterSeconds })

  it('maps the status of the probe to the next step', () => {
    expect(classifyProbe(null, DEFAULT_TIMING)).toEqual({ state: 'reconnecting', retryMs: 3_000 })
    expect(classifyProbe(err(401, 'unauthorized'), DEFAULT_TIMING)).toEqual({ state: 'unauthorized', retryMs: null })
    expect(classifyProbe(err(403, 'forbidden'), DEFAULT_TIMING)).toEqual({ state: 'forbidden', retryMs: null })
    expect(classifyProbe(err(404, 'not_found'), DEFAULT_TIMING)).toEqual({ state: 'not_found', retryMs: null })
    expect(classifyProbe(err(429, 'rate_limited', 30), DEFAULT_TIMING)).toEqual({ state: 'reconnecting', retryMs: 30_000 })
    expect(classifyProbe(err(429, 'rate_limited'), DEFAULT_TIMING)).toEqual({ state: 'reconnecting', retryMs: 10_000 })
    expect(classifyProbe(err(503, 'agent_unavailable'), DEFAULT_TIMING)).toEqual({ state: 'unavailable', retryMs: 10_000 })
    expect(classifyProbe(new TypeError('Failed to fetch'), DEFAULT_TIMING)).toEqual({ state: 'unavailable', retryMs: 10_000 })
  })
})
```

- [ ] **Step 4: Tests für den Konsole-Tab schreiben**

`web/src/features/console/console-tab.test.tsx`:

```tsx
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { FakeEventSource } from './testing/fake-event-source'
import { stubLogViewport } from './testing/layout'

let eventSeq = 1_000

async function openConsole(serverId = 1) {
  const user = userEvent.setup()
  await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs('admin') })
  await user.click(await screen.findByRole('tab', { name: 'Konsole' }))
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
  const es = FakeEventSource.latest()
  act(() => es.open())
  return { user, es, log: screen.getByRole('log', { name: 'Server-Log' }) }
}

function emitLine(es: FakeEventSource, text: string, stream: 'out' | 'err' = 'out') {
  eventSeq += 1
  act(() => es.emit('line', { ts: Date.now(), stream, line: text }, String(eventSeq)))
}

beforeEach(() => {
  FakeEventSource.reset()
  vi.stubGlobal('EventSource', FakeEventSource)
  stubLogViewport()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('ConsoleTab', () => {
  it('shows log lines, marks warnings and hides RCON noise until asked', async () => {
    const { user, es, log } = await openConsole()
    expect(screen.getByText('Verbunden')).toBeInTheDocument()
    emitLine(es, '[10:00:00 INFO]: Done (3.2s)! For help, type "help"')
    emitLine(es, '[10:00:01 INFO]: Thread RCON Client /127.0.0.1 started')
    emitLine(es, "\u001b[33m[10:00:02 WARN]: Can't keep up!\u001b[0m")
    expect(await within(log).findByText(/Done \(3\.2s\)/)).toBeInTheDocument()
    expect(within(log).getByText("[10:00:02 WARN]: Can't keep up!").closest('[data-level]')).toHaveAttribute('data-level', 'warn')
    expect(within(log).queryByText(/Thread RCON Client/)).not.toBeInTheDocument()

    await user.click(screen.getByRole('switch', { name: 'RCON-Meldungen ausblenden' }))
    expect(await within(log).findByText(/Thread RCON Client/)).toBeInTheDocument()
  })

  it('renders markup in log lines as text', async () => {
    const { es, log } = await openConsole()
    emitLine(es, '<img src=x onerror=alert(1)>')
    expect(await within(log).findByText('<img src=x onerror=alert(1)>')).toBeInTheDocument()
    expect(log.querySelector('img')).toBeNull()
  })

  it('filters the log', async () => {
    const { user, es, log } = await openConsole()
    emitLine(es, 'Done (3.2s)!')
    emitLine(es, "Can't keep up!")
    await within(log).findByText("Can't keep up!")
    await user.type(screen.getByRole('searchbox', { name: 'Log filtern' }), 'keep')
    await waitFor(() => expect(within(log).queryByText('Done (3.2s)!')).not.toBeInTheDocument())
    expect(within(log).getByText("Can't keep up!")).toBeInTheDocument()
  })

  it('sends a command and shows the answer as a block', async () => {
    const { user, log } = await openConsole()
    const input = screen.getByRole('textbox', { name: 'Befehl' })
    await user.type(input, 'list{Enter}')
    expect(await within(log).findByText('› list')).toBeInTheDocument()
    expect(await within(log).findByText(/of a max of 20 players online/)).toBeInTheDocument()
    expect(input).toHaveValue('')
  })

  it('shows why a command failed', async () => {
    server.use(http.post('*/api/servers/:id/console', () => HttpResponse.json({ error: 'rcon_unavailable', message: 'not ready' }, { status: 503 })))
    const { user, log } = await openConsole()
    await user.type(screen.getByRole('textbox', { name: 'Befehl' }), 'list{Enter}')
    expect(await within(log).findByText('Die Server-Konsole antwortet gerade nicht – startet der Server noch?')).toBeInTheDocument()
  })

  it('checks a command before sending it', async () => {
    const { user } = await openConsole()
    await user.type(screen.getByRole('textbox', { name: 'Befehl' }), '/{Enter}')
    expect(await screen.findByRole('alert')).toHaveTextContent('Gib einen Befehl ein.')
  })

  it('recalls earlier commands with the arrow keys', async () => {
    const { user, log } = await openConsole()
    const input = screen.getByRole('textbox', { name: 'Befehl' })
    await user.type(input, 'list{Enter}')
    await user.type(input, 'say hi{Enter}')
    expect(await within(log).findByText('Keine Ausgabe.')).toBeInTheDocument()
    await user.type(input, '{ArrowUp}')
    expect(input).toHaveValue('say hi')
    await user.type(input, '{ArrowUp}')
    expect(input).toHaveValue('list')
    await user.type(input, '{ArrowDown}{ArrowDown}')
    expect(input).toHaveValue('')
  })

  it('locks the command line while the server is stopped', async () => {
    const { es } = await openConsole(2)
    expect(screen.getByRole('textbox', { name: 'Befehl' })).toBeDisabled()
    expect(screen.getByText('Der Server läuft nicht. Befehle sind erst nach dem Start möglich.')).toBeInTheDocument()
    act(() => es.emit('state', { running: false }))
    expect(screen.getByText('Server gestoppt')).toBeInTheDocument()
  })

  it('offers a jump to new lines after scrolling up', async () => {
    const { user, es, log } = await openConsole()
    emitLine(es, 'line one')
    await within(log).findByText('line one')
    Object.defineProperty(log, 'scrollHeight', { configurable: true, value: 2000 })
    Object.defineProperty(log, 'clientHeight', { configurable: true, value: 600 })
    log.scrollTop = 0
    fireEvent.scroll(log)
    expect(screen.queryByRole('button', { name: 'Neue Zeilen' })).not.toBeInTheDocument()
    emitLine(es, 'line two')
    await user.click(await screen.findByRole('button', { name: 'Neue Zeilen' }))
    expect(screen.queryByRole('button', { name: 'Neue Zeilen' })).not.toBeInTheDocument()
  })

  it('copies the visible lines', async () => {
    const { user, es, log } = await openConsole()
    emitLine(es, 'alpha')
    emitLine(es, 'Thread RCON Client /127.0.0.1 started')
    emitLine(es, 'beta')
    await within(log).findByText('beta')
    await user.click(screen.getByRole('button', { name: 'Kopieren' }))
    expect(await screen.findByText('Sichtbare Zeilen kopiert.')).toBeInTheDocument()
    expect(await navigator.clipboard.readText()).toBe('alpha\nbeta')
  })
})
```

Zusätzlich in `web/src/features/servers/server-detail-page.test.tsx` die Tab-Erwartung des Admin-Tests anpassen und einen Test für Nutzer mit Konsolenrecht ergänzen (Import `db` ist schon vorhanden):

```tsx
// im Test 'shows status, address, resources and power buttons on the overview':
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Übersicht', 'Konsole', 'Einstellungen', 'Löschen'])
```

```tsx
  it('shows the console to a user with the console permission', async () => {
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.console'] }])
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    expect(await screen.findByRole('tab', { name: 'Konsole' })).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Einstellungen' })).not.toBeInTheDocument()
  })
```

- [ ] **Step 5: Rot prüfen**

Run: `make check`
Expected: FAIL – `tsc` meldet fehlende Module `./use-console-stream`, `./api` usw.

- [ ] **Step 6: API-Funktionen implementieren**

`web/src/features/console/api.ts`:

```ts
import { api } from '@/api/client'
import { toApiError, unwrap } from '@/api/errors'
import type { ConsoleCommandResponse } from '@/api/types'

/** Same origin as the API client; EventSource sends the session cookie by itself. */
export function consoleStreamUrl(id: number): string {
  return `/api/servers/${id}/console/stream`
}

/**
 * EventSource reports a refused stream only as "error" with readyState CLOSED and no status code. This asks once
 * via the API client (whose middleware handles 401 → login) and closes an opened stream right away.
 * Returns null if the stream answered 200, otherwise the error (ApiError or network error).
 */
export async function probeConsoleStream(id: number): Promise<unknown> {
  try {
    const { data, error, response } = await api.GET('/servers/{id}/console/stream', {
      params: { path: { id } },
      parseAs: 'stream',
      headers: { Accept: 'text/event-stream' },
    })
    if (response.ok) {
      await data?.cancel()
      return null
    }
    return toApiError(response, error)
  } catch (err) {
    return err
  }
}

export function sendConsoleCommand(id: number, command: string): Promise<ConsoleCommandResponse> {
  return unwrap(api.POST('/servers/{id}/console', { params: { path: { id } }, body: { command } }))
}
```

- [ ] **Step 7: Hook implementieren**

`web/src/features/console/use-console-stream.ts`:

```ts
import { useCallback, useEffect, useReducer, useRef, useState } from 'react'
import { isApiError } from '@/api/errors'
import { consoleStreamUrl, probeConsoleStream } from './api'
import { initialLogState, logReducer, parseLineEvent, parseStateEvent, type IncomingLine, type LogEntry } from './log-model'

export type ConnectionState = 'connecting' | 'live' | 'stopped' | 'reconnecting' | 'unavailable' | 'forbidden' | 'not_found' | 'unauthorized'

export interface StreamTiming {
  /** Lines arriving within this window are rendered together. */
  flushMs: number
  /** Delay before a new EventSource after the stream answered 200 again. */
  reconnectMs: number
  /** Delay after 5xx, network errors or a 429 without Retry-After. */
  retryMs: number
}

export const DEFAULT_TIMING: StreamTiming = { flushMs: 50, reconnectMs: 3_000, retryMs: 10_000 }

/** EventSource.CLOSED; a constant so that tests can replace the global EventSource. */
const EVENT_SOURCE_CLOSED = 2

export function classifyProbe(err: unknown, timing: StreamTiming): { state: ConnectionState; retryMs: number | null } {
  if (err === null) return { state: 'reconnecting', retryMs: timing.reconnectMs }
  if (isApiError(err)) {
    if (err.status === 401) return { state: 'unauthorized', retryMs: null }
    if (err.status === 403) return { state: 'forbidden', retryMs: null }
    if (err.status === 404) return { state: 'not_found', retryMs: null }
    if (err.status === 429) {
      const retryMs = err.retryAfterSeconds !== undefined ? Math.max(err.retryAfterSeconds * 1000, timing.reconnectMs) : timing.retryMs
      return { state: 'reconnecting', retryMs }
    }
  }
  return { state: 'unavailable', retryMs: timing.retryMs }
}

export interface ConsoleStream {
  entries: readonly LogEntry[]
  connection: ConnectionState
  /** Adds a pending command block and returns its id for finishCommand. */
  addCommand(command: string): number
  finishCommand(id: number, ok: boolean, output: string): void
}

export function useConsoleStream(serverId: number, timing: StreamTiming = DEFAULT_TIMING): ConsoleStream {
  const [log, dispatch] = useReducer(logReducer, undefined, initialLogState)
  const [connection, setConnection] = useState<ConnectionState>('connecting')
  const commandSeq = useRef(0)
  const { flushMs, reconnectMs, retryMs } = timing

  useEffect(() => {
    const current: StreamTiming = { flushMs, reconnectMs, retryMs }
    let disposed = false
    let source: EventSource | null = null
    let retryTimer: ReturnType<typeof setTimeout> | undefined
    let flushTimer: ReturnType<typeof setTimeout> | undefined
    let pending: IncomingLine[] = []

    function flush() {
      flushTimer = undefined
      if (pending.length === 0) return
      const lines = pending
      pending = []
      dispatch({ type: 'lines', lines })
    }

    function connect() {
      if (disposed) return
      const es = new EventSource(consoleStreamUrl(serverId))
      source = es
      es.onopen = () => setConnection((prev) => (prev === 'stopped' ? prev : 'live'))
      es.addEventListener('line', (event: Event) => {
        const message = event as MessageEvent<string>
        const line = parseLineEvent(message.data, message.lastEventId)
        if (!line) return
        pending.push(line)
        flushTimer ??= setTimeout(flush, current.flushMs)
      })
      es.addEventListener('state', (event: Event) => {
        const state = parseStateEvent((event as MessageEvent<string>).data)
        if (state) setConnection(state.running ? 'live' : 'stopped')
      })
      es.onerror = () => {
        if (es.readyState !== EVENT_SOURCE_CLOSED) {
          setConnection('reconnecting') // the browser retries by itself and sends Last-Event-ID
          return
        }
        es.close()
        source = null
        void probeConsoleStream(serverId).then((err) => {
          if (disposed) return
          const next = classifyProbe(err, current)
          setConnection(next.state)
          if (next.retryMs !== null) retryTimer = setTimeout(connect, next.retryMs)
        })
      }
    }

    connect()
    return () => {
      disposed = true
      source?.close()
      clearTimeout(retryTimer)
      clearTimeout(flushTimer)
    }
  }, [serverId, flushMs, reconnectMs, retryMs])

  const addCommand = useCallback((command: string) => {
    commandSeq.current += 1
    const id = commandSeq.current
    dispatch({ type: 'command', id, command })
    return id
  }, [])

  const finishCommand = useCallback((id: number, ok: boolean, output: string) => {
    dispatch({ type: 'command_done', id, ok, output })
  }, [])

  return { entries: log.entries, connection, addCommand, finishCommand }
}
```

- [ ] **Step 8: Darstellungs-Komponenten implementieren**

`web/src/features/console/log-text.tsx`:

```tsx
import { cn } from '@/lib/utils'
import type { LogColor, Segment } from './ansi'

/** Readable on the always-dark console background; static strings so that Tailwind finds them. */
const COLOR_CLASS: Record<LogColor, string> = {
  black: 'text-zinc-500',
  red: 'text-red-400',
  green: 'text-green-400',
  yellow: 'text-yellow-400',
  blue: 'text-blue-400',
  magenta: 'text-fuchsia-400',
  cyan: 'text-cyan-400',
  white: 'text-zinc-200',
  'bright-black': 'text-zinc-400',
  'bright-red': 'text-red-300',
  'bright-green': 'text-green-300',
  'bright-yellow': 'text-yellow-200',
  'bright-blue': 'text-blue-300',
  'bright-magenta': 'text-fuchsia-300',
  'bright-cyan': 'text-cyan-300',
  'bright-white': 'text-white',
}

export function LogText({ segments }: { segments: readonly Segment[] }) {
  return (
    <>
      {segments.map((s, i) => (
        <span
          key={i}
          className={cn(
            s.color && COLOR_CLASS[s.color],
            s.bold && 'font-bold',
            s.italic && 'italic',
            s.underline && 'underline',
            s.strike && 'line-through',
            s.obfuscated && 'blur-[2px]',
          )}
        >
          {s.text}
        </span>
      ))}
    </>
  )
}
```

`web/src/features/console/log-view.tsx`:

```tsx
import { useVirtualizer } from '@tanstack/react-virtual'
import { ArrowDown } from 'lucide-react'
import { useEffect, useRef, useState, type UIEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import type { CommandEntry, LineEntry, LogEntry } from './log-model'
import { LogText } from './log-text'

const ROW_ESTIMATE_PX = 20
/** Distance from the end that still counts as "at the bottom" (keeps auto-scroll on). */
const BOTTOM_SLACK_PX = 32

export function LogView({ entries }: { entries: readonly LogEntry[] }) {
  const { t } = useTranslation()
  const scrollRef = useRef<HTMLDivElement>(null)
  const [atBottom, setAtBottom] = useState(true)
  const [seenKey, setSeenKey] = useState<string | undefined>(undefined)
  const lastKey = entries.at(-1)?.key
  // eslint-disable-next-line react-hooks/incompatible-library -- useVirtualizer returns new functions on purpose; this component is not memoized
  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_ESTIMATE_PX,
    overscan: 20,
    getItemKey: (index) => entries[index].key,
  })

  useEffect(() => {
    if (atBottom && entries.length > 0) virtualizer.scrollToIndex(entries.length - 1, { align: 'end' })
  }, [atBottom, lastKey, entries.length, virtualizer])

  function onScroll(event: UIEvent<HTMLDivElement>) {
    const el = event.currentTarget
    const bottom = el.scrollHeight - el.scrollTop - el.clientHeight <= BOTTOM_SLACK_PX
    if (bottom === atBottom) return
    setAtBottom(bottom)
    if (!bottom) setSeenKey(lastKey)
  }

  const showJump = !atBottom && lastKey !== seenKey

  return (
    <div className="relative">
      <div
        ref={scrollRef}
        data-console-log=""
        role="log"
        aria-live="off"
        aria-label={t('console.log_label')}
        tabIndex={0}
        onScroll={onScroll}
        className="h-[60vh] min-h-64 overflow-y-auto rounded-md bg-zinc-950 p-2 font-mono text-xs leading-5 text-zinc-100 sm:text-sm"
      >
        {entries.length === 0 ? (
          <p className="text-zinc-400">{t('console.empty')}</p>
        ) : (
          <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map((item) => {
              const entry = entries[item.index]
              return (
                <div
                  key={item.key}
                  data-index={item.index}
                  ref={virtualizer.measureElement}
                  className="absolute top-0 left-0 w-full"
                  style={{ transform: `translateY(${item.start}px)` }}
                >
                  {entry.kind === 'line' ? <LineRow entry={entry} /> : <CommandRow entry={entry} />}
                </div>
              )
            })}
          </div>
        )}
      </div>
      {showJump ? (
        <Button type="button" size="sm" className="absolute right-4 bottom-4 shadow" onClick={() => setAtBottom(true)}>
          <ArrowDown aria-hidden="true" />
          {t('console.jump_to_bottom')}
        </Button>
      ) : null}
    </div>
  )
}

function LineRow({ entry }: { entry: LineEntry }) {
  return (
    <div
      data-level={entry.level}
      className={cn(
        'px-1 break-words whitespace-pre-wrap',
        entry.level === 'warn' && 'border-l-2 border-yellow-500 bg-yellow-500/10',
        entry.level === 'error' && 'border-l-2 border-red-500 bg-red-500/10',
      )}
    >
      <LogText segments={entry.segments} />
    </div>
  )
}

function CommandRow({ entry }: { entry: CommandEntry }) {
  const { t } = useTranslation()
  return (
    <div data-command="" className="my-1 rounded-sm border border-zinc-700 bg-zinc-900 px-2 py-1">
      <p className="break-words whitespace-pre-wrap text-sky-300">› {entry.command}</p>
      {entry.status === 'pending' ? (
        <p className="text-zinc-400">{t('console.command_pending')}</p>
      ) : entry.status === 'error' ? (
        <p className="break-words whitespace-pre-wrap text-red-300">{entry.output}</p>
      ) : entry.output ? (
        <p className="break-words whitespace-pre-wrap">
          <LogText segments={entry.segments} />
        </p>
      ) : (
        <p className="text-zinc-400">{t('console.command_no_output')}</p>
      )}
    </div>
  )
}
```

Hinweis: Meldet `npm run lint` in der CI die `eslint-disable`-Zeile als ungenutzt (Regel `react-hooks/incompatible-library` nicht aktiv), die Kommentarzeile entfernen.

`web/src/features/console/command-input.tsx`:

```tsx
import { SendHorizontal } from 'lucide-react'
import { useId, useState, type FormEvent, type KeyboardEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { commandProblem, type CommandProblem } from './command'
import { emptyHistory, historyDown, historyUp, pushHistory, type HistoryState } from './history'

export function CommandInput({ disabled, onSubmit }: { disabled: boolean; onSubmit(command: string): void }) {
  const { t } = useTranslation()
  const id = useId()
  const [value, setValue] = useState('')
  const [history, setHistory] = useState<HistoryState>(emptyHistory)
  const [problem, setProblem] = useState<CommandProblem | null>(null)
  const hintId = `${id}-hint`
  const errorId = `${id}-error`

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const command = value.trim()
    const found = commandProblem(command)
    setProblem(found)
    if (found) return
    setHistory((h) => pushHistory(h, command))
    setValue('')
    onSubmit(command)
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
    event.preventDefault()
    const [next, text] = event.key === 'ArrowUp' ? historyUp(history, value) : historyDown(history, value)
    setHistory(next)
    setValue(text)
  }

  return (
    <form onSubmit={submit} className="bg-background sticky bottom-0 space-y-1 pt-2">
      <div className="flex gap-2">
        <Label htmlFor={id} className="sr-only">
          {t('console.command')}
        </Label>
        <Input
          id={id}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={disabled}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          placeholder={disabled ? t('console.stopped_placeholder') : t('console.command_placeholder')}
          aria-invalid={problem ? true : undefined}
          aria-describedby={problem ? `${errorId} ${hintId}` : hintId}
          className="font-mono"
        />
        <Button type="submit" disabled={disabled}>
          <SendHorizontal aria-hidden="true" />
          {t('console.send')}
        </Button>
      </div>
      {problem ? (
        <p id={errorId} role="alert" className="text-destructive text-sm">
          {t(`console.validation.${problem}`)}
        </p>
      ) : null}
      <p id={hintId} className="text-muted-foreground text-xs">
        {disabled ? t('console.stopped_hint') : t('console.stop_hint')}
      </p>
    </form>
  )
}
```

`web/src/features/console/console-tab.tsx`:

```tsx
import { Copy } from 'lucide-react'
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { errorMessage } from '@/lib/error-message'
import { sendConsoleCommand } from './api'
import { CommandInput } from './command-input'
import { entryText, visibleEntries } from './log-model'
import { LogView } from './log-view'
import { useConsoleStream, type ConnectionState } from './use-console-stream'

const CONNECTION_VARIANT: Record<ConnectionState, 'default' | 'secondary' | 'destructive' | 'outline'> = {
  connecting: 'outline',
  live: 'default',
  stopped: 'secondary',
  reconnecting: 'outline',
  unavailable: 'destructive',
  forbidden: 'destructive',
  not_found: 'destructive',
  unauthorized: 'destructive',
}

export function ConsoleTab({ server }: { server: Server }) {
  const { t } = useTranslation()
  const rconId = useId()
  const stream = useConsoleStream(server.id)
  const [query, setQuery] = useState('')
  const [hideRcon, setHideRcon] = useState(true)
  const visible = useMemo(() => visibleEntries(stream.entries, { query, hideRcon }), [stream.entries, query, hideRcon])
  const running = server.status.state === 'running'

  async function send(command: string) {
    const id = stream.addCommand(command)
    try {
      const response = await sendConsoleCommand(server.id, command)
      stream.finishCommand(id, true, response.output)
    } catch (err) {
      stream.finishCommand(id, false, errorMessage(err, t))
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(visible.map(entryText).join('\n'))
      toast.success(t('console.copied'))
    } catch {
      toast.error(t('console.copy_failed'))
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <Badge variant={CONNECTION_VARIANT[stream.connection]}>{t(`console.connection.${stream.connection}`)}</Badge>
        <Input
          type="search"
          aria-label={t('console.filter')}
          placeholder={t('console.filter')}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="w-full sm:w-64"
        />
        <div className="flex items-center gap-2">
          <Switch id={rconId} checked={hideRcon} onCheckedChange={setHideRcon} />
          <Label htmlFor={rconId}>{t('console.hide_rcon')}</Label>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={() => void copy()}>
          <Copy aria-hidden="true" />
          {t('console.copy')}
        </Button>
      </div>
      <LogView entries={visible} />
      <CommandInput disabled={!running} onSubmit={(command) => void send(command)} />
    </div>
  )
}
```

- [ ] **Step 9: Detailseite um den Tab „Konsole“ erweitern**

`web/src/features/servers/server-detail-page.tsx` – vollständige neue Fassung:

```tsx
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { PageHeader } from '@/components/page-header'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useMe } from '@/features/auth/me'
import { ConsoleTab } from '@/features/console/console-tab'
import { errorMessage } from '@/lib/error-message'
import { serverQueryOptions } from './api'
import { DeleteServerSection } from './delete-server-section'
import { describeInstance } from './format'
import { ServerOverview } from './server-overview'
import { ServerSettingsTab } from './server-settings-tab'
import { detailPollInterval } from './status'
import { StatusBadge } from './status-badge'

type DetailTab = 'overview' | 'console' | 'settings' | 'delete'

export function ServerDetailPage({ serverId }: { serverId: number }) {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const query = useQuery({
    ...serverQueryOptions(serverId),
    // Fast while the server changes state; no polling at all without data or after 403/404.
    refetchInterval: (q) => detailPollInterval(q.state.data, q.state.error),
  })
  const [tab, setTab] = useState<DetailTab>('overview')

  if (query.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
  // A failed refetch keeps the old data: after 403/404 the server is gone or hidden, so stale data must not look live.
  const gone = isApiError(query.error, 'not_found') || isApiError(query.error, 'forbidden')
  if (!query.data || gone) {
    let message = errorMessage(query.error, t)
    if (isApiError(query.error, 'not_found')) message = t('servers.detail.not_found')
    else if (isApiError(query.error, 'forbidden')) message = t('servers.detail.forbidden')
    return (
      <div className="space-y-3">
        <p role="alert" className="text-destructive">
          {message}
        </p>
        <Link to="/" className="text-primary underline underline-offset-4">
          {t('not_found.home')}
        </Link>
      </div>
    )
  }

  const server = query.data
  const header = (
    <PageHeader
      title={server.name}
      description={server.instance ? describeInstance(server.instance, t) : undefined}
      actions={<StatusBadge state={server.status.state} />}
    />
  )

  if (server.instance === null) {
    return (
      <>
        {header}
        <div className="max-w-3xl space-y-4">
          <p className="text-muted-foreground">{t('servers.detail.not_configured')}</p>
          {isAdmin ? <DeleteServerSection server={server} /> : null}
        </div>
      </>
    )
  }

  const instance = server.instance
  const canConsole = server.permissions.includes('server.console')
  const available: DetailTab[] = ['overview']
  if (canConsole) available.push('console')
  if (isAdmin) available.push('settings', 'delete')
  // A tab whose permission was revoked while it was open falls back to the overview.
  const active = available.includes(tab) ? tab : 'overview'

  return (
    <>
      {header}
      <Tabs value={active} onValueChange={(value) => setTab(value as DetailTab)}>
        <TabsList>
          <TabsTrigger value="overview">{t('servers.detail.tabs.overview')}</TabsTrigger>
          {canConsole ? <TabsTrigger value="console">{t('servers.detail.tabs.console')}</TabsTrigger> : null}
          {isAdmin ? <TabsTrigger value="settings">{t('servers.detail.tabs.settings')}</TabsTrigger> : null}
          {isAdmin ? <TabsTrigger value="delete">{t('servers.detail.tabs.delete')}</TabsTrigger> : null}
        </TabsList>
        <TabsContent value="overview" className="pt-4">
          <ServerOverview server={server} instance={instance} />
        </TabsContent>
        {canConsole ? (
          <TabsContent value="console" className="pt-4">
            <ConsoleTab server={server} />
          </TabsContent>
        ) : null}
        {isAdmin ? (
          <TabsContent value="settings" className="pt-4">
            <ServerSettingsTab server={server} instance={instance} />
          </TabsContent>
        ) : null}
        {isAdmin ? (
          <TabsContent value="delete" className="pt-4">
            <DeleteServerSection server={server} />
          </TabsContent>
        ) : null}
      </Tabs>
    </>
  )
}
```

Radix `TabsContent` hängt inaktive Tabs aus: der Stream ist nur offen, solange „Konsole“ sichtbar ist.

- [ ] **Step 10: Texte ergänzen**

`errors.instance_not_running|rcon_unavailable` gibt es schon (Phase A) – nicht erneut anlegen.

`web/src/i18n/de.ts`:
- in `servers.detail.tabs` nach `overview`:
  ```ts
        console: 'Konsole',
  ```
- neuer Top-Level-Block direkt nach `servers: { … },`:
  ```ts
  console: {
    log_label: 'Server-Log',
    empty: 'Noch keine Ausgaben.',
    filter: 'Log filtern',
    hide_rcon: 'RCON-Meldungen ausblenden',
    copy: 'Kopieren',
    copied: 'Sichtbare Zeilen kopiert.',
    copy_failed: 'Kopieren war nicht möglich.',
    jump_to_bottom: 'Neue Zeilen',
    command: 'Befehl',
    command_placeholder: 'Befehl eingeben, z. B. list',
    stopped_placeholder: 'Server gestoppt',
    send: 'Senden',
    command_pending: 'Wird ausgeführt …',
    command_no_output: 'Keine Ausgabe.',
    stop_hint: '„stop“ beendet den Server, Docker startet ihn sofort neu – zum Stoppen den Power-Button verwenden.',
    stopped_hint: 'Der Server läuft nicht. Befehle sind erst nach dem Start möglich.',
    connection: {
      connecting: 'Verbinde …',
      live: 'Verbunden',
      stopped: 'Server gestoppt',
      reconnecting: 'Verbinde neu …',
      unavailable: 'Konsole nicht erreichbar',
      forbidden: 'Keine Berechtigung',
      not_found: 'Server nicht gefunden',
      unauthorized: 'Abgemeldet',
    },
    validation: {
      empty: 'Gib einen Befehl ein.',
      too_long: 'Der Befehl ist zu lang (höchstens 1000 Byte).',
      control: 'Zeilenumbrüche, Tabulatoren und Steuerzeichen sind nicht erlaubt.',
    },
  },
  ```

`web/src/i18n/en.ts` an denselben Stellen:
- `servers.detail.tabs`: `console: 'Console',`
- Block nach `servers`:
  ```ts
  console: {
    log_label: 'Server log',
    empty: 'No output yet.',
    filter: 'Filter log',
    hide_rcon: 'Hide RCON messages',
    copy: 'Copy',
    copied: 'Visible lines copied.',
    copy_failed: 'Copying was not possible.',
    jump_to_bottom: 'New lines',
    command: 'Command',
    command_placeholder: 'Enter a command, e.g. list',
    stopped_placeholder: 'Server stopped',
    send: 'Send',
    command_pending: 'Running …',
    command_no_output: 'No output.',
    stop_hint: '"stop" ends the server and Docker restarts it right away – use the power button to stop it.',
    stopped_hint: 'The server is not running. Commands work once it has started.',
    connection: {
      connecting: 'Connecting …',
      live: 'Connected',
      stopped: 'Server stopped',
      reconnecting: 'Reconnecting …',
      unavailable: 'Console unavailable',
      forbidden: 'No permission',
      not_found: 'Server not found',
      unauthorized: 'Signed out',
    },
    validation: {
      empty: 'Enter a command.',
      too_long: 'The command is too long (at most 1000 bytes).',
      control: 'Line breaks, tabs and control characters are not allowed.',
    },
  },
  ```

`web/src/i18n/i18n.test.ts` – `DYNAMIC_KEYS` am Ende ergänzen (`ERROR_CODES` hat Phase A schon erweitert):
  ```ts
    ...['connecting', 'live', 'stopped', 'reconnecting', 'unavailable', 'forbidden', 'not_found', 'unauthorized'].map(
      (s) => `console.connection.${s}`,
    ),
    ...['empty', 'too_long', 'control'].map((p) => `console.validation.${p}`),
  ```

- [ ] **Step 11: Prüfen, committen, CI abwarten**

Run: `make check`
Expected: PASS.

```bash
git add web/package.json web/package-lock.json web/src/features/console web/src/features/servers/server-detail-page.tsx \
  web/src/features/servers/server-detail-page.test.tsx web/src/i18n/de.ts web/src/i18n/en.ts web/src/i18n/i18n.test.ts
git commit -m "feat(web): console tab with live log, command line and reconnect handling" \
  -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: grün; insbesondere `use-console-stream.test.ts`, `console-tab.test.tsx`, `i18n.test.ts`, `server-detail-page.test.tsx`, `npm run build` (prüft, dass `@tanstack/react-virtual` gebündelt wird).

---

### Task 16: Spieler-Tab – Online, Whitelist, Ops, Bans mit Aktionen

**Files:**
- Create: `web/src/features/players/api.ts`, `web/src/features/players/validation.ts`, `web/src/features/players/use-player-action.ts`, `web/src/features/players/name-form.tsx`, `web/src/features/players/ban-dialog.tsx`, `web/src/features/players/players-tab.tsx`
- Modify: `web/src/features/servers/server-detail-page.tsx`, `web/src/features/servers/server-detail-page.test.tsx`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts`
- Test: `web/src/features/players/validation.test.ts`, `web/src/features/players/players-tab.test.tsx`

**Interfaces:**
- Consumes: `api`, `unwrap` (`@/api/client`, `@/api/errors`); `ConsoleCommandResponse`, `PlayerListFile`, `PlayerActionRequest`, `Server` (`@/api/types`); `plainText` (Aufgabe 14); `useErrorToast`, `errorMessage` (`@/lib/error-message`); `SectionCard` (`@/features/profile/section-card`)
- Produces:
```ts
// players/api.ts
export const ONLINE_POLL_MS = 15_000
export const playersKey: (id: number) => readonly ['servers', number, 'players']
export const onlinePlayersKey: (id: number) => readonly ['servers', number, 'players', 'online']
export const playerListsKey: (id: number) => readonly ['servers', number, 'players', 'lists']
export function onlinePlayersQueryOptions(id: number)   // GET /servers/{id}/players → OnlinePlayers
export function playerListsQueryOptions(id: number)     // GET /servers/{id}/players/lists → PlayerLists
export function playerAction(id: number, body: PlayerActionRequest): Promise<ConsoleCommandResponse>

// players/validation.ts
export const PLAYER_NAME: RegExp           // /^[A-Za-z0-9_]{3,16}$/
export const MAX_REASON_LENGTH = 200
export function playerNameProblem(name: string): string | null   // i18n key or null
export function reasonProblem(reason: string): string | null

// players/use-player-action.ts
export function usePlayerAction(serverId: number): UseMutationResult<ConsoleCommandResponse, unknown, PlayerActionRequest>

// components
export function NameForm(props: { label: string; disabled: boolean; onAdd(name: string): Promise<unknown> }): JSX.Element
export function BanDialog(props: { name: string; open: boolean; pending: boolean; onOpenChange(open: boolean): void; onConfirm(reason: string | undefined): void }): JSX.Element
export function PlayersTab(props: { server: Server; onOpenSettings?: () => void }): JSX.Element
```

- [ ] **Step 1: Tests für die Validierung schreiben**

`web/src/features/players/validation.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { MAX_REASON_LENGTH, playerNameProblem, reasonProblem } from './validation'

describe('playerNameProblem', () => {
  it('accepts Minecraft names', () => {
    for (const name of ['Steve', 'abc', 'A_b_9', 'x'.repeat(16)]) expect(playerNameProblem(name), name).toBeNull()
  })

  it('rejects everything that could change the command', () => {
    for (const name of ['', 'ab', 'x'.repeat(17), 'a b', 'Steve;op', 'Ste-ve', 'Stève', 'Steve\n']) {
      expect(playerNameProblem(name), JSON.stringify(name)).toBe('players.validation.name_invalid')
    }
  })
})

describe('reasonProblem', () => {
  it('accepts printable text up to the limit', () => {
    expect(reasonProblem('griefing am Spawn')).toBeNull()
    expect(reasonProblem('ä'.repeat(MAX_REASON_LENGTH))).toBeNull()
  })

  it('counts characters, not bytes', () => {
    expect(reasonProblem('😀'.repeat(MAX_REASON_LENGTH))).toBeNull()
    expect(reasonProblem('x'.repeat(MAX_REASON_LENGTH + 1))).toBe('players.validation.reason_too_long')
  })

  it('rejects line breaks, control and invisible characters', () => {
    for (const reason of ['a\nb', 'a\rb', 'a\u0000', 'a\tb', 'a​b', 'a b', 'a b']) {
      expect(reasonProblem(reason), JSON.stringify(reason)).toBe('players.validation.reason_control')
    }
  })
})
```

- [ ] **Step 2: Tests für den Spieler-Tab schreiben**

`web/src/features/players/players-tab.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { db, loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'

async function openPlayers(serverId = 1, username = 'admin') {
  const user = userEvent.setup()
  await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs(username) })
  await user.click(await screen.findByRole('tab', { name: 'Spieler' }))
  return user
}

describe('PlayersTab', () => {
  it('lists online players, whitelist, operators and bans', async () => {
    await openPlayers()
    const online = await screen.findByRole('region', { name: 'Online' })
    expect(await within(online).findByText('Alex')).toBeInTheDocument()
    expect(within(online).getByText('Steve')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Whitelist' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Operatoren' })).toBeInTheDocument()
    const bans = screen.getByRole('region', { name: 'Gebannte Spieler' })
    expect(await within(bans).findAllByRole('button', { name: / entbannen$/ })).toHaveLength(1)
  })

  it('kicks a player and refreshes the online list', async () => {
    const user = await openPlayers()
    const online = await screen.findByRole('region', { name: 'Online' })
    await user.click(await within(online).findByRole('button', { name: 'Steve kicken' }))
    await waitFor(() => expect(within(online).queryByText('Steve')).not.toBeInTheDocument())
    await waitFor(() => expect(document.querySelector('[data-sonner-toast]')).not.toBeNull())
  })

  it('bans a player with a reason', async () => {
    const user = await openPlayers()
    const online = await screen.findByRole('region', { name: 'Online' })
    await user.click(await within(online).findByRole('button', { name: 'Steve bannen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Steve bannen' })
    await user.type(within(dialog).getByLabelText('Grund (optional)'), 'griefing')
    await user.click(within(dialog).getByRole('button', { name: 'Bannen' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    const bans = screen.getByRole('region', { name: 'Gebannte Spieler' })
    expect(await within(bans).findByText('griefing')).toBeInTheDocument()
    expect(within(bans).getByRole('button', { name: 'Steve entbannen' })).toBeInTheDocument()
  })

  it('rejects a reason that is too long before sending', async () => {
    const user = await openPlayers()
    const online = await screen.findByRole('region', { name: 'Online' })
    await user.click(await within(online).findByRole('button', { name: 'Steve bannen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Steve bannen' })
    await user.click(within(dialog).getByLabelText('Grund (optional)'))
    await user.paste('x'.repeat(201))
    await user.click(within(dialog).getByRole('button', { name: 'Bannen' }))
    expect(within(dialog).getByText('Höchstens 200 Zeichen.')).toBeInTheDocument()
    expect(within(online).getByText('Steve')).toBeInTheDocument()
  })

  it('adds a name to the whitelist after checking it', async () => {
    const user = await openPlayers()
    const whitelist = await screen.findByRole('region', { name: 'Whitelist' })
    const input = within(whitelist).getByLabelText('Spieler zur Whitelist hinzufügen')
    await user.type(input, 'a b')
    await user.click(within(whitelist).getByRole('button', { name: 'Hinzufügen' }))
    expect(within(whitelist).getByText('3–16 Buchstaben, Ziffern oder Unterstriche.')).toBeInTheDocument()
    await user.clear(input)
    await user.type(input, 'Notch')
    await user.click(within(whitelist).getByRole('button', { name: 'Hinzufügen' }))
    expect(await within(whitelist).findByText('Notch')).toBeInTheDocument()
    expect(input).toHaveValue('')
  })

  it('reports a list file the server could not read', async () => {
    server.use(http.get('*/api/servers/:id/players/lists', () => HttpResponse.json({ ops: [], whitelist: [], bans: [], errors: ['ops.json'] })))
    await openPlayers()
    const ops = await screen.findByRole('region', { name: 'Operatoren' })
    expect(await within(ops).findByRole('alert')).toHaveTextContent('Die Datei ops.json ist auf dem Server nicht lesbar.')
  })

  it('locks every change while the server is stopped', async () => {
    await openPlayers(2)
    expect(await screen.findByText('Der Server läuft nicht. Die Listen sind lesbar, ändern kannst du sie erst nach dem Start.')).toBeInTheDocument()
    const add = screen.getAllByRole('button', { name: 'Hinzufügen' })
    expect(add).toHaveLength(2)
    for (const button of add) expect(button).toBeDisabled()
  })

  it('leads admins to the whitelist setting', async () => {
    const user = await openPlayers()
    await user.click(await screen.findByRole('button', { name: 'Zu den Einstellungen' }))
    expect(screen.getByRole('tab', { name: 'Einstellungen' })).toHaveAttribute('aria-selected', 'true')
  })

  it('tells other users that an admin switches the whitelist on', async () => {
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.console'] }])
    await openPlayers(1, 'alice')
    expect(await screen.findByText('Die Whitelist ist aus: Einträge wirken erst, wenn ein Admin sie einschaltet.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Zu den Einstellungen' })).not.toBeInTheDocument()
  })
})
```

In `web/src/features/servers/server-detail-page.test.tsx` die Admin-Tab-Erwartung (aus Aufgabe 15) ändern auf:

```tsx
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Übersicht', 'Konsole', 'Spieler', 'Einstellungen', 'Löschen'])
```

und im Test `shows the console to a user with the console permission` nach der Konsole-Erwartung ergänzen:

```tsx
    expect(screen.getByRole('tab', { name: 'Spieler' })).toBeInTheDocument()
```

- [ ] **Step 3: Rot prüfen**

Run: `make check`
Expected: FAIL – fehlende Module `./validation`, `@/features/players/players-tab`.

- [ ] **Step 4: API, Validierung und Mutation implementieren**

`web/src/features/players/api.ts`:

```ts
import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { ConsoleCommandResponse, PlayerActionRequest } from '@/api/types'

/** Online players are refreshed every 15 s while the page is visible (TanStack pauses in the background). */
export const ONLINE_POLL_MS = 15_000

/** Below the server key: invalidating serverKey(id) or serversKey refreshes the player data as well. */
export const playersKey = (id: number) => ['servers', id, 'players'] as const
export const onlinePlayersKey = (id: number) => ['servers', id, 'players', 'online'] as const
export const playerListsKey = (id: number) => ['servers', id, 'players', 'lists'] as const

export function onlinePlayersQueryOptions(id: number) {
  return queryOptions({
    queryKey: onlinePlayersKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/players', { params: { path: { id } } })),
  })
}

export function playerListsQueryOptions(id: number) {
  return queryOptions({
    queryKey: playerListsKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/players/lists', { params: { path: { id } } })),
  })
}

export function playerAction(id: number, body: PlayerActionRequest): Promise<ConsoleCommandResponse> {
  return unwrap(api.POST('/servers/{id}/players/actions', { params: { path: { id } }, body }))
}
```

`web/src/features/players/validation.ts`:

```ts
/** Same rules as the panel (spec §6.5); the panel stays authoritative. */
export const PLAYER_NAME = /^[A-Za-z0-9_]{3,16}$/
export const MAX_REASON_LENGTH = 200

/** Mirrors Go's unicode.IsPrint: no control/format/private characters, no separators except the ASCII space. */
const NOT_PRINTABLE = /[\p{C}\p{Zl}\p{Zp}]|(?! )\p{Zs}/u

export function playerNameProblem(name: string): string | null {
  return PLAYER_NAME.test(name) ? null : 'players.validation.name_invalid'
}

export function reasonProblem(reason: string): string | null {
  if ([...reason].length > MAX_REASON_LENGTH) return 'players.validation.reason_too_long'
  if (NOT_PRINTABLE.test(reason)) return 'players.validation.reason_control'
  return null
}
```

`web/src/features/players/use-player-action.ts`:

```ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { PlayerActionRequest } from '@/api/types'
import { plainText } from '@/features/console/ansi'
import { useErrorToast } from '@/lib/error-message'
import { playerAction, playersKey } from './api'

/** Runs a player action, shows the server's answer as a toast and reloads online players and lists. */
export function usePlayerAction(serverId: number) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  return useMutation({
    mutationFn: (body: PlayerActionRequest) => playerAction(serverId, body),
    onSuccess: (response) => {
      toast.success(plainText(response.output).trim() || t('players.done'))
      void queryClient.invalidateQueries({ queryKey: playersKey(serverId) })
    },
    onError: notifyError,
  })
}
```

- [ ] **Step 5: Komponenten implementieren**

`web/src/features/players/name-form.tsx`:

```tsx
import { Plus } from 'lucide-react'
import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { playerNameProblem } from './validation'

export function NameForm({ label, disabled, onAdd }: { label: string; disabled: boolean; onAdd(name: string): Promise<unknown> }) {
  const { t } = useTranslation()
  const id = useId()
  const messageId = `${id}-message`
  const [name, setName] = useState('')
  const [problem, setProblem] = useState<string | null>(null)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const value = name.trim()
    const found = playerNameProblem(value)
    setProblem(found)
    if (found) return
    try {
      await onAdd(value)
      setName('')
    } catch {
      // usePlayerAction already showed the error as a toast; keep the input for a correction.
    }
  }

  return (
    <form onSubmit={(event) => void submit(event)} className="space-y-1">
      <Label htmlFor={id}>{label}</Label>
      <div className="flex gap-2">
        <Input
          id={id}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={t('players.name_placeholder')}
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          disabled={disabled}
          aria-invalid={problem ? true : undefined}
          aria-describedby={problem ? messageId : undefined}
          className="max-w-xs"
        />
        <Button type="submit" variant="outline" disabled={disabled}>
          <Plus aria-hidden="true" />
          {t('players.add')}
        </Button>
      </div>
      {problem ? (
        <p id={messageId} className="text-destructive text-sm">
          {t(problem)}
        </p>
      ) : null}
    </form>
  )
}
```

`web/src/features/players/ban-dialog.tsx`:

```tsx
import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { reasonProblem } from './validation'

export interface BanDialogProps {
  name: string
  open: boolean
  pending: boolean
  onOpenChange(open: boolean): void
  onConfirm(reason: string | undefined): void
}

export function BanDialog({ name, open, pending, onOpenChange, onConfirm }: BanDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const messageId = `${id}-message`
  const [reason, setReason] = useState('')
  const [problem, setProblem] = useState<string | null>(null)

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const value = reason.trim()
    const found = value ? reasonProblem(value) : null
    setProblem(found)
    if (found) return
    onConfirm(value || undefined)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="space-y-4">
          <DialogHeader>
            <DialogTitle>{t('players.ban_dialog.title', { name })}</DialogTitle>
            <DialogDescription>{t('players.ban_dialog.description')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor={id}>{t('players.ban_dialog.reason')}</Label>
            <Input
              id={id}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              autoComplete="off"
              aria-invalid={problem ? true : undefined}
              aria-describedby={problem ? messageId : undefined}
            />
            {problem ? (
              <p id={messageId} className="text-destructive text-sm">
                {t(problem)}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="destructive" disabled={pending}>
              {t('players.ban_dialog.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

`web/src/features/players/players-tab.tsx`:

```tsx
import { useQuery } from '@tanstack/react-query'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { PlayerListFile, Server } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { errorMessage } from '@/lib/error-message'
import { ONLINE_POLL_MS, onlinePlayersQueryOptions, playerListsQueryOptions } from './api'
import { BanDialog } from './ban-dialog'
import { NameForm } from './name-form'
import { usePlayerAction } from './use-player-action'
import { PLAYER_NAME } from './validation'

function Muted({ children }: { children: ReactNode }) {
  return <p className="text-muted-foreground text-sm">{children}</p>
}

export function PlayersTab({ server, onOpenSettings }: { server: Server; onOpenSettings?: () => void }) {
  const { t } = useTranslation()
  const online = useQuery({ ...onlinePlayersQueryOptions(server.id), refetchInterval: ONLINE_POLL_MS })
  const lists = useQuery(playerListsQueryOptions(server.id))
  const action = usePlayerAction(server.id)
  const [banTarget, setBanTarget] = useState<string | null>(null)
  const running = server.status.state === 'running'
  const locked = !running || action.isPending
  const opNames = new Set((lists.data?.ops ?? []).map((o) => o.name.toLowerCase()))
  const whitelistOn = server.instance?.properties.whitelist_enabled === true

  function fileError(file: PlayerListFile) {
    return lists.data?.errors.includes(file) ? (
      <p role="alert" className="text-destructive text-sm">
        {t('players.file_error', { file })}
      </p>
    ) : null
  }

  function listsStatus() {
    if (lists.isPending) return <Muted>{t('common.loading')}</Muted>
    if (lists.isError)
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(lists.error, t)}
        </p>
      )
    return null
  }

  function onlineList() {
    if (online.isPending) return <Muted>{t('common.loading')}</Muted>
    if (online.isError)
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(online.error, t)}
        </p>
      )
    if (!online.data.available) return <Muted>{t('players.online.unavailable')}</Muted>
    if (online.data.online.length === 0) return <Muted>{t('players.online.none')}</Muted>
    return (
      <ul className="divide-y">
        {online.data.online.map((player) => {
          const isOp = opNames.has(player.name.toLowerCase())
          // Offline-mode servers allow names the panel rejects; actions on them would only fail.
          const disabled = locked || !PLAYER_NAME.test(player.name)
          return (
            <li key={player.name} className="flex flex-wrap items-center justify-between gap-2 py-2">
              <span className="flex items-center gap-2 font-medium">
                {player.name}
                {isOp ? <Badge variant="secondary">{t('players.op_badge')}</Badge> : null}
              </span>
              <span className="flex flex-wrap gap-2">
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={disabled}
                  aria-label={t('players.kick_label', { name: player.name })}
                  onClick={() => action.mutate({ action: 'kick', name: player.name })}
                >
                  {t('players.kick')}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  disabled={disabled}
                  aria-label={t('players.ban_label', { name: player.name })}
                  onClick={() => setBanTarget(player.name)}
                >
                  {t('players.ban')}
                </Button>
                {isOp ? (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={disabled}
                    aria-label={t('players.deop_label', { name: player.name })}
                    onClick={() => action.mutate({ action: 'deop', name: player.name })}
                  >
                    {t('players.deop')}
                  </Button>
                ) : (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={disabled}
                    aria-label={t('players.op_label', { name: player.name })}
                    onClick={() => action.mutate({ action: 'op', name: player.name })}
                  >
                    {t('players.op')}
                  </Button>
                )}
              </span>
            </li>
          )
        })}
      </ul>
    )
  }

  return (
    <div className="space-y-4">
      {!running ? (
        <p role="status" className="rounded-md border p-3 text-sm">
          {t('players.not_running')}
        </p>
      ) : null}

      <SectionCard title={t('players.online.title')}>{onlineList()}</SectionCard>

      <SectionCard title={t('players.whitelist.title')}>
        <div className="space-y-3">
          {whitelistOn ? (
            <Muted>{t('players.whitelist.active')}</Muted>
          ) : onOpenSettings ? (
            <div className="flex flex-wrap items-center gap-2">
              <Muted>{t('players.whitelist.inactive')}</Muted>
              <Button type="button" variant="link" className="h-auto p-0" onClick={onOpenSettings}>
                {t('players.whitelist.open_settings')}
              </Button>
            </div>
          ) : (
            <Muted>{t('players.whitelist.inactive_user')}</Muted>
          )}
          {fileError('whitelist.json')}
          <NameForm
            label={t('players.whitelist.add_label')}
            disabled={locked}
            onAdd={(name) => action.mutateAsync({ action: 'whitelist_add', name })}
          />
          {listsStatus()}
          {lists.data && lists.data.whitelist.length === 0 ? <Muted>{t('players.whitelist.empty')}</Muted> : null}
          {lists.data && lists.data.whitelist.length > 0 ? (
            <ul className="divide-y">
              {lists.data.whitelist.map((entry) => (
                <li key={entry.uuid || entry.name} className="flex items-center justify-between gap-2 py-2">
                  <span className="font-medium">{entry.name}</span>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={locked}
                    aria-label={t('players.whitelist_remove_label', { name: entry.name })}
                    onClick={() => action.mutate({ action: 'whitelist_remove', name: entry.name })}
                  >
                    {t('players.whitelist_remove')}
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </SectionCard>

      <SectionCard title={t('players.ops.title')}>
        <div className="space-y-3">
          {fileError('ops.json')}
          <NameForm label={t('players.ops.add_label')} disabled={locked} onAdd={(name) => action.mutateAsync({ action: 'op', name })} />
          {listsStatus()}
          {lists.data && lists.data.ops.length === 0 ? <Muted>{t('players.ops.empty')}</Muted> : null}
          {lists.data && lists.data.ops.length > 0 ? (
            <ul className="divide-y">
              {lists.data.ops.map((op) => (
                <li key={op.uuid || op.name} className="flex items-center justify-between gap-2 py-2">
                  <span className="flex items-center gap-2 font-medium">
                    {op.name}
                    <Badge variant="outline">{t('players.ops.level', { level: op.level })}</Badge>
                  </span>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={locked}
                    aria-label={t('players.deop_label', { name: op.name })}
                    onClick={() => action.mutate({ action: 'deop', name: op.name })}
                  >
                    {t('players.deop')}
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </SectionCard>

      <SectionCard title={t('players.bans.title')}>
        <div className="space-y-3">
          {fileError('banned-players.json')}
          {listsStatus()}
          {lists.data && lists.data.bans.length === 0 ? <Muted>{t('players.bans.empty')}</Muted> : null}
          {lists.data && lists.data.bans.length > 0 ? (
            <ul className="divide-y">
              {lists.data.bans.map((ban) => (
                <li key={ban.uuid || ban.name} className="flex flex-wrap items-start justify-between gap-2 py-2">
                  <div className="min-w-0 space-y-1">
                    <p className="font-medium">{ban.name}</p>
                    <dl className="text-muted-foreground grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 text-sm">
                      <dt>{t('players.bans.reason')}</dt>
                      <dd className="break-words">{ban.reason || '—'}</dd>
                      <dt>{t('players.bans.source')}</dt>
                      <dd className="break-words">{ban.source || '—'}</dd>
                      <dt>{t('players.bans.created')}</dt>
                      <dd>{ban.created || '—'}</dd>
                      <dt>{t('players.bans.expires')}</dt>
                      <dd>{ban.expires === 'forever' || !ban.expires ? t('players.bans.forever') : ban.expires}</dd>
                    </dl>
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={locked}
                    aria-label={t('players.pardon_label', { name: ban.name })}
                    onClick={() => action.mutate({ action: 'pardon', name: ban.name })}
                  >
                    {t('players.pardon')}
                  </Button>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      </SectionCard>

      {banTarget !== null ? (
        <BanDialog
          key={banTarget}
          name={banTarget}
          open
          pending={action.isPending}
          onOpenChange={(open) => {
            if (!open) setBanTarget(null)
          }}
          onConfirm={(reason) =>
            action.mutate(
              { action: 'ban', name: banTarget, ...(reason ? { reason } : {}) },
              { onSuccess: () => setBanTarget(null) },
            )
          }
        />
      ) : null}
    </div>
  )
}
```

- [ ] **Step 6: Detailseite um „Spieler“ erweitern**

`web/src/features/servers/server-detail-page.tsx` – Änderungen gegenüber Aufgabe 15 (alle übrigen Zeilen bleiben):

```tsx
// Import ergänzen (alphabetisch nach ConsoleTab):
import { PlayersTab } from '@/features/players/players-tab'

// Typ ersetzen:
type DetailTab = 'overview' | 'console' | 'players' | 'settings' | 'delete'

// Liste der verfügbaren Tabs ersetzen:
  const available: DetailTab[] = ['overview']
  if (canConsole) available.push('console', 'players')
  if (isAdmin) available.push('settings', 'delete')

// In <TabsList> nach dem Konsole-Trigger:
          {canConsole ? <TabsTrigger value="players">{t('servers.detail.tabs.players')}</TabsTrigger> : null}

// Nach dem Konsole-<TabsContent>:
        {canConsole ? (
          <TabsContent value="players" className="pt-4">
            <PlayersTab server={server} onOpenSettings={isAdmin ? () => setTab('settings') : undefined} />
          </TabsContent>
        ) : null}
```

- [ ] **Step 7: Texte ergänzen**

`web/src/i18n/de.ts`:
- `servers.detail.tabs` nach `console`: `players: 'Spieler',`
- neuer Top-Level-Block direkt nach `console: { … },`:
  ```ts
  players: {
    online: {
      title: 'Online',
      none: 'Gerade ist niemand online.',
      unavailable: 'Nicht verfügbar – der Server läuft nicht oder startet noch.',
    },
    whitelist: {
      title: 'Whitelist',
      empty: 'Niemand steht auf der Whitelist.',
      add_label: 'Spieler zur Whitelist hinzufügen',
      active: 'Die Whitelist ist aktiv: nur eingetragene Spieler kommen auf den Server.',
      inactive: 'Die Whitelist ist aus: Einträge wirken erst, wenn du sie in den Einstellungen einschaltest.',
      inactive_user: 'Die Whitelist ist aus: Einträge wirken erst, wenn ein Admin sie einschaltet.',
      open_settings: 'Zu den Einstellungen',
    },
    ops: {
      title: 'Operatoren',
      empty: 'Es gibt keine Operatoren.',
      add_label: 'Spieler zum Operator machen',
      level: 'Level {{level}}',
    },
    bans: {
      title: 'Gebannte Spieler',
      empty: 'Niemand ist gebannt.',
      reason: 'Grund',
      source: 'Von',
      created: 'Seit',
      expires: 'Bis',
      forever: 'unbegrenzt',
    },
    add: 'Hinzufügen',
    name_placeholder: 'Spielername',
    not_running: 'Der Server läuft nicht. Die Listen sind lesbar, ändern kannst du sie erst nach dem Start.',
    file_error: 'Die Datei {{file}} ist auf dem Server nicht lesbar. Die Liste wird deshalb leer angezeigt.',
    done: 'Erledigt.',
    op_badge: 'Op',
    kick: 'Kicken',
    kick_label: '{{name}} kicken',
    ban: 'Bannen',
    ban_label: '{{name}} bannen',
    pardon: 'Entbannen',
    pardon_label: '{{name}} entbannen',
    op: 'Op geben',
    op_label: '{{name}} zum Operator machen',
    deop: 'Op entziehen',
    deop_label: '{{name}} die Operator-Rechte entziehen',
    whitelist_remove: 'Entfernen',
    whitelist_remove_label: '{{name}} von der Whitelist entfernen',
    ban_dialog: {
      title: '{{name}} bannen',
      description: 'Der Spieler wird sofort getrennt und kann nicht mehr beitreten, bis du ihn entbannst.',
      reason: 'Grund (optional)',
      submit: 'Bannen',
    },
    validation: {
      name_invalid: '3–16 Buchstaben, Ziffern oder Unterstriche.',
      reason_too_long: 'Höchstens 200 Zeichen.',
      reason_control: 'Zeilenumbrüche und Steuerzeichen sind nicht erlaubt.',
    },
  },
  ```

`web/src/i18n/en.ts`:
- `servers.detail.tabs`: `players: 'Players',`
- Block nach `console`:
  ```ts
  players: {
    online: {
      title: 'Online',
      none: 'Nobody is online right now.',
      unavailable: 'Not available – the server is not running or still starting.',
    },
    whitelist: {
      title: 'Whitelist',
      empty: 'Nobody is on the whitelist.',
      add_label: 'Add a player to the whitelist',
      active: 'The whitelist is on: only listed players can join.',
      inactive: 'The whitelist is off: entries take effect once you switch it on in the settings.',
      inactive_user: 'The whitelist is off: entries take effect once an admin switches it on.',
      open_settings: 'Go to settings',
    },
    ops: {
      title: 'Operators',
      empty: 'There are no operators.',
      add_label: 'Make a player an operator',
      level: 'Level {{level}}',
    },
    bans: {
      title: 'Banned players',
      empty: 'Nobody is banned.',
      reason: 'Reason',
      source: 'By',
      created: 'Since',
      expires: 'Until',
      forever: 'forever',
    },
    add: 'Add',
    name_placeholder: 'Player name',
    not_running: 'The server is not running. You can read the lists but change them only after it has started.',
    file_error: 'The file {{file}} is not readable on the server, so the list is shown empty.',
    done: 'Done.',
    op_badge: 'Op',
    kick: 'Kick',
    kick_label: 'Kick {{name}}',
    ban: 'Ban',
    ban_label: 'Ban {{name}}',
    pardon: 'Unban',
    pardon_label: 'Unban {{name}}',
    op: 'Make op',
    op_label: 'Make {{name}} an operator',
    deop: 'Remove op',
    deop_label: 'Remove operator rights from {{name}}',
    whitelist_remove: 'Remove',
    whitelist_remove_label: 'Remove {{name}} from the whitelist',
    ban_dialog: {
      title: 'Ban {{name}}',
      description: 'The player is disconnected right away and cannot join again until you unban them.',
      reason: 'Reason (optional)',
      submit: 'Ban',
    },
    validation: {
      name_invalid: '3–16 letters, digits or underscores.',
      reason_too_long: 'At most 200 characters.',
      reason_control: 'Line breaks and control characters are not allowed.',
    },
  },
  ```

`web/src/i18n/i18n.test.ts` – `DYNAMIC_KEYS` ergänzen:

```ts
  ...['name_invalid', 'reason_too_long', 'reason_control'].map((p) => `players.validation.${p}`),
```

- [ ] **Step 8: Prüfen, committen, CI abwarten**

Run: `make check`
Expected: PASS.

```bash
git add web/src/features/players web/src/features/servers/server-detail-page.tsx web/src/features/servers/server-detail-page.test.tsx \
  web/src/i18n/de.ts web/src/i18n/en.ts web/src/i18n/i18n.test.ts
git commit -m "feat(web): players tab with online list, whitelist, operators and bans" \
  -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: grün (`players-tab.test.tsx`, `validation.test.ts`, `server-detail-page.test.tsx`, `i18n.test.ts`).

---

### Task 17: Online-Karte auf der Übersicht

**Files:**
- Create: `web/src/features/players/online-players-card.tsx`
- Modify: `web/src/features/servers/server-overview.tsx`, `web/src/features/servers/server-detail-page.test.tsx`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`
- Test: `web/src/features/players/online-players-card.test.tsx`

**Interfaces:**
- Consumes: `onlinePlayersQueryOptions`, `ONLINE_POLL_MS` (Aufgabe 16); `SectionCard`; `errorMessage`
- Produces: `export function OnlinePlayersCard(props: { serverId: number }): JSX.Element`

- [ ] **Step 1: Tests schreiben**

`web/src/features/players/online-players-card.test.tsx`:

```tsx
import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { OnlinePlayersCard } from './online-players-card'

describe('OnlinePlayersCard', () => {
  it('shows how many players are online and who', async () => {
    await renderWithProviders(<OnlinePlayersCard serverId={1} />, { me: loginAs('admin') })
    const card = screen.getByRole('region', { name: 'Online-Spieler' })
    expect(await within(card).findByText('2 / 20')).toBeInTheDocument()
    expect(within(card).getByText('Alex')).toBeInTheDocument()
    expect(within(card).getByText('Steve')).toBeInTheDocument()
  })

  it('works with the view permission alone', async () => {
    await renderWithProviders(<OnlinePlayersCard serverId={1} />, { me: loginAs('alice') })
    expect(await screen.findByText('2 / 20')).toBeInTheDocument()
  })

  it('says when nobody is online', async () => {
    server.use(http.get('*/api/servers/:id/players', () => HttpResponse.json({ available: true, online: [], max: 20 })))
    await renderWithProviders(<OnlinePlayersCard serverId={1} />, { me: loginAs('admin') })
    expect(await screen.findByText('0 / 20')).toBeInTheDocument()
    expect(screen.getByText('Gerade ist niemand online.')).toBeInTheDocument()
  })

  it('says when the list is not available', async () => {
    await renderWithProviders(<OnlinePlayersCard serverId={2} />, { me: loginAs('admin') })
    expect(await screen.findByText('Nicht verfügbar – der Server läuft nicht oder startet noch.')).toBeInTheDocument()
  })
})
```

In `web/src/features/servers/server-detail-page.test.tsx` im Test `shows status, address, resources and power buttons on the overview` am Ende ergänzen:

```tsx
    expect(await within(screen.getByRole('region', { name: 'Online-Spieler' })).findByText('2 / 20')).toBeInTheDocument()
```

- [ ] **Step 2: Rot prüfen**

Run: `make check`
Expected: FAIL – Modul `./online-players-card` fehlt.

- [ ] **Step 3: Karte implementieren und einbauen**

`web/src/features/players/online-players-card.tsx`:

```tsx
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { SectionCard } from '@/features/profile/section-card'
import { errorMessage } from '@/lib/error-message'
import { ONLINE_POLL_MS, onlinePlayersQueryOptions } from './api'

export function OnlinePlayersCard({ serverId }: { serverId: number }) {
  const { t } = useTranslation()
  const query = useQuery({ ...onlinePlayersQueryOptions(serverId), refetchInterval: ONLINE_POLL_MS })

  let body
  if (query.isPending) body = <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
  else if (query.isError) body = <p className="text-muted-foreground text-sm">{errorMessage(query.error, t)}</p>
  else if (!query.data.available) body = <p className="text-muted-foreground text-sm">{t('players.online.unavailable')}</p>
  else {
    const { online, max } = query.data
    body = (
      <div className="space-y-2">
        <p className="text-2xl font-semibold tabular-nums">{t('players.card.count', { online: online.length, max: max ?? '—' })}</p>
        {online.length === 0 ? (
          <p className="text-muted-foreground text-sm">{t('players.online.none')}</p>
        ) : (
          <ul className="flex flex-wrap gap-1">
            {online.map((player) => (
              <li key={player.name}>
                <Badge variant="outline">{player.name}</Badge>
              </li>
            ))}
          </ul>
        )}
      </div>
    )
  }

  return <SectionCard title={t('players.card.title')}>{body}</SectionCard>
}
```

`web/src/features/servers/server-overview.tsx`:
- Import ergänzen (nach `import { SectionCard } …`): `import { OnlinePlayersCard } from '@/features/players/online-players-card'`
- Zwischen der Zustand-Karte und der Steuerung-Karte einfügen:

```tsx
      </SectionCard>
      <OnlinePlayersCard serverId={server.id} />
      <SectionCard title={t('servers.overview.controls')}>
```

(d. h. direkt vor `<SectionCard title={t('servers.overview.controls')}>`).

- [ ] **Step 4: Texte ergänzen**

`web/src/i18n/de.ts`, im Block `players` nach `online: { … },`:

```ts
    card: {
      title: 'Online-Spieler',
      count: '{{online}} / {{max}}',
    },
```

`web/src/i18n/en.ts` an derselben Stelle:

```ts
    card: {
      title: 'Online players',
      count: '{{online}} / {{max}}',
    },
```

Die alten TP2-Texte für Ops/Whitelist hat Phase A (Aufgabe 1) bereits entfernt bzw. durch `servers.props.lists_hint` ersetzt; hier ist nichts aufzuräumen.

- [ ] **Step 5: Prüfen, committen, CI abwarten**

Run: `make check`
Expected: PASS.

```bash
git add web/src/features/players/online-players-card.tsx web/src/features/players/online-players-card.test.tsx \
  web/src/features/servers/server-overview.tsx web/src/features/servers/server-detail-page.test.tsx \
  web/src/i18n/de.ts web/src/i18n/en.ts
git commit -m "feat(web): online players card on the server overview" \
  -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: grün.

---

### Task 18: Playwright – Konsole und Spieler gegen den Mock

**Files:**
- Modify: `web/e2e/servers.spec.ts` (gehört zum Projekt `mock` in `playwright.config.ts`; keine Änderung der Konfiguration nötig)

**Interfaces:**
- Consumes: `signIn(page, username, withTotp)` (vorhanden in `web/e2e/servers.spec.ts`); Mock aus Phase A im Browser (MSW-Service-Worker; Stream als `http.get` mit `ReadableStream`)
- Produces: zwei Playwright-Tests

- [ ] **Step 1: Tests am Ende von `web/e2e/servers.spec.ts` anhängen**

```ts
test('admin follows the console, sends a command and kicks a player', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)

  await page.getByRole('tab', { name: 'Konsole' }).click()
  const log = page.getByRole('log', { name: 'Server-Log' })
  await expect(page.getByText('Verbunden')).toBeVisible()
  await expect(log).toContainText('INFO')
  await expect(log).not.toContainText('Thread RCON Client')

  const command = page.getByRole('textbox', { name: 'Befehl' })
  await command.fill('list')
  await command.press('Enter')
  await expect(log).toContainText('› list')
  await expect(log).toContainText('of a max of 20 players online')
  await expect(command).toHaveValue('')

  await page.getByRole('tab', { name: 'Spieler' }).click()
  const online = page.getByRole('region', { name: 'Online' })
  await expect(online.getByText('Steve', { exact: true })).toBeVisible()
  await online.getByRole('button', { name: 'Steve kicken' }).click()
  await expect(page.locator('[data-sonner-toast]').first()).toBeVisible()
  await expect(online.getByText('Steve', { exact: true })).toHaveCount(0)
})

test('a user without console permission sees online players but no console', async ({ page }) => {
  await signIn(page, 'alice', false)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await expect(page.getByRole('region', { name: 'Online-Spieler' }).getByText('Alex')).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Konsole' })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: 'Spieler' })).toHaveCount(0)
})
```

- [ ] **Step 2: Prüfen, committen, CI abwarten**

Run: `make check`
Expected: PASS (Playwright-Dateien werden von `tsc` nicht geprüft; der Test läuft in der CI im Job „e2e“).

```bash
git add web/e2e/servers.spec.ts
git commit -m "test(web): end-to-end console and players flows against the mock" \
  -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
git push
gh pr checks --watch
```

Expected: alle Checks grün, insbesondere der Playwright-Job. Schlägt er fehl: `gh run view <run-id> --log-failed`; das hochgeladene Trace-Artefakt (`trace: 'retain-on-failure'`) zeigt, ob der Stream im Service Worker ankam.

- [ ] **Step 3: PR fertigstellen**

```bash
gh pr ready
gh pr view --web
```

Den Lead informieren (PR-Nummer, grüne CI). Mergen nur durch den Nutzer.

---

## Selbstprüfung (Spec §7 → Aufgaben)

| Spec | Aufgabe |
|---|---|
| §7.1 Tabs nur mit Recht, Reihenfolge Übersicht · Konsole · Spieler · Einstellungen · Löschen | 15, 16 |
| §7.1 Online-Karte auf der Übersicht (`server.view`, 15 s, nur sichtbar, „nicht verfügbar“) | 17 |
| §7.1 Einstellungen ohne Ops/Whitelist, `whitelist_enabled` mit Verweis | Phase A (Felder, `lists_hint`), 16 (Verweis aus dem Spieler-Tab) |
| §7.2 virtualisierte Liste, Ringpuffer 5000 | 14, 15 |
| §7.2 ANSI-SGR und `§`-Codes, kein `dangerouslySetInnerHTML`, WARN/ERROR/stderr hervorgehoben | 14, 15 |
| §7.2 Filter, RCON-Schalter (Standard an), Auto-Scroll + „Neue Zeilen ↓“, Verbindungsanzeige, Kopieren | 15 |
| §7.2 Befehlszeile unten, Verlauf 100, Antwort-Blöcke, gesperrt bei gestopptem Server, Hinweis zu `stop` | 14, 15 |
| §7.2 `EventSource` mit `Last-Event-ID`, Schließen bei 401/403 mit Hinweis | 15 |
| §7.3 Online (Kick, Ban mit Grund, Op/Deop), Whitelist, Ops mit Level, Bans mit Details, `errors`, gesperrt bei gestopptem Server, Toast + Neuladen | 16 |
| §7.4 Mocks | Phase A (genutzt in 15–18) |
| §6.6 Fehlercodes in der UI | Phase A (Texte, `KNOWN_CODES`, `ERROR_CODES`), 15 (Anzeige im Antwort-Block) |
