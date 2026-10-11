# mc-panel Teilprojekt 3b – Phase B3: Frontend Dateibrowser (Aufgaben 13–17)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der Tab „Dateien“ auf der Server-Detailseite: Ordnerliste mit Brotkrumen, Sortierung und Karten auf dem Handy; Anlegen, Umbenennen/Verschieben, Löschen; Upload-Warteschlange mit Fortschritt; Download (auch Ordner als Zip); Editor (CodeMirror 6, lazy) mit Konflikt-Erkennung; Entpacken mit Fortschritt und Power-Sperre; Hinweis beim Vergeben von `server.files`.

**Architecture:** Alles Neue liegt in `web/src/features/files/`. Reine Logik (Pfadregeln, Formatierung, Sortierung, Upload-Warteschlange) ist von React getrennt und per Vitest getestet. Die Komponenten nutzen den vorhandenen API-Client (`@/api/client`) und TanStack Query; nur der Upload läuft per `XMLHttpRequest` (Fortschritt), Downloads sind normale Links (der Browser streamt, kein Blob im Speicher). Der Editor ist eine lazy geladene Komponente (`React.lazy`), damit CodeMirror nur im Editor-Chunk landet. Die Detailseite fragt den Entpack-Job ab und sperrt Start/Neustart, solange er läuft.

**Tech Stack:** React 19, TypeScript, TanStack Query/Router (`useBlocker`), shadcn/ui (Dialog, DropdownMenu, Tabs), react-i18next, MSW 2, Vitest + Testing Library, Playwright; neu: CodeMirror 6 (`codemirror`, `@codemirror/state`, `@codemirror/view`, `@codemirror/language`, `@codemirror/lang-yaml`, `@codemirror/lang-json`, `@codemirror/legacy-modes`).

**Spec:** `docs/superpowers/specs/2026-10-11-mc-panel-files-design.md` (§6) · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-11-files-00-overview.md` (V3, V5; „Ausführung“, „Global Constraints“ und Dateieigentum B3 gelten vollständig).

## Ausführung dieser Phase

- Teammate „frontend“, Worktree `git worktree add ../mc-panel-wt/feat-files-frontend -b feat/files-frontend origin/main` (nach dem Merge von Phase A), dort einmal `(cd web && npm ci)`.
- Lokal nur `make check` (gofmt, vet, Go-Build, `tsc`). Vitest, ESLint und Playwright laufen nur in der CI (Job „Web (lint, typecheck, test, build, audit)“ bzw. „E2E“). Neue Dateien vor `make check` mit `git add -N <datei>` vormerken.
- „Run: `npx vitest run …`“ in den Schritten heißt: **nicht lokal**, sondern Commit, Push, `gh pr checks <pr> --watch`, Ergebnis im CI-Log (`gh run view <id> --log-failed`).
- Ein Draft-PR für die ganze Phase (Titel `feat(web): file browser, editor, uploads and extract`), ein Commit pro Aufgabe; Mergen nur durch den Nutzer.
- Dateieigentum B3: `web/src/features/files/**` (neu), `web/src/features/servers/**`, `web/src/features/users/**`, `web/src/i18n/**`, `web/e2e/**`, `web/package.json`, `web/package-lock.json`. **Nicht** ändern: `web/src/api/**`, `web/src/mocks/**` (Phase A) – fehlt dort etwas, den Lead fragen.

## Was Phase A liefert (Annahmen dieses Plans)

- `web/src/api/schema.d.ts` enthält die zehn Operationen und Schemas aus V3; `web/src/api/types.ts` exportiert (Phase A, Aufgabe 3, Step 7) `FileEntryType`, `FileEntry`, `FileList`, `FileContent`, `WriteFileRequest`, `FileWriteResult`, `UploadResult`, `FilePathRequest`, `RenameFileRequest`, `ExtractRequest`, `FileJobState`, `FileJob`, `FileJobResponse`. Dieser Plan importiert sie von dort (`@/api/types`).
- `web/src/api/errors.ts` kennt die sieben neuen Codes (`files_unavailable`, `file_exists`, `file_modified`, `not_text`, `not_regular`, `instance_running`, `insufficient_storage`); `de.ts`/`en.ts` haben dafür `errors.<code>`-Texte und `i18n.test.ts` hat sie in `ERROR_CODES` (Phase A, Aufgabe 3, Step 7). Dieser Plan ergänzt in `i18n.test.ts` nur `DYNAMIC_KEYS`.
- MSW-Mocks (`web/src/mocks/handlers/files.ts`, `db.files` aus `mockTree(now, running)`): Server 1 „survival“ (läuft) mit `server.properties` (`rcon.password=********`), `ops.json`, `world/` (`level.dat`), `logs/latest.log`, `world-backup.zip` (30 MiB), `bukkit.yml`, `plugins/` (`EssentialsX.jar` 2 MiB, `Essentials/config.yml` mit `ops-name-color: "4"`), Symlink `current`; `.rcon-cli.env` existiert, wird aber nie gelistet. Server 2 „creative“ (gestoppt) hat denselben Baum ohne `bukkit.yml`, `plugins/` und `current`. Upload legt die Datei an (`409 file_exists` ohne `overwrite=true`), Binärdateien (ohne Text-Inhalt) liefern beim Lesen `415 not_text`, Umbenennen auf ein vorhandenes Ziel `409 file_exists`, Entpacken bei nicht gestopptem Server `409 instance_running`, ein Entpack-Job läuft 6 s (`JOB_MS`) und legt dann den Zielordner an. Rechte über `serverPermissions` (`server.files`): `admin` hat alle, `alice` hat auf Server 1 nur `server.view` + `server.power`.
- Abfrage-Parameter `overwrite` des Uploads ist laut OpenAPI ein `boolean` (Default `false`); dieser Plan sendet `overwrite=true|false` wie der Mock (`=== 'true'`). B2 muss genau das parsen (zum Dienst geht es dann als `0|1`).

## CSP

Die CSP des panels (`internal/httpx/middleware.go`) ist `script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; …`. CodeMirror 6 braucht kein `eval` und keine Worker; seine Styles setzt `style-mod` über `adoptedStyleSheets` bzw. ein `<style>`-Element, dazu Inline-`style`-Attribute – beides deckt `style-src 'unsafe-inline'` ab. Eine CSP-Änderung ist **nicht** nötig. (Würde `'unsafe-inline'` später entfernt, bräuchte der Editor die Facette `EditorView.cspNonce` mit einem Nonce aus dem Server – nicht Teil von 3b.) Downloads sind Same-Origin-Links (`default-src 'self'` genügt), der Upload ist ein Same-Origin-XHR (`connect-src 'self'`).

## Review Focus (aus der Übersicht, für B3 relevant)

- **RCON-Geheimnis (Focus 4):** Die UI zeigt nie mehr als die API liefert; `.rcon-cli.*` und `.mcpanel-*` gelten als reserviert (`hidden` in den Pfadregeln) – Anlegen/Umbenennen auf solche Namen wird schon im Browser abgelehnt. → Aufgabe 13 (`path.test.ts`).
- **Durchreichen ohne Puffern (Focus 3):** Upload übergibt das `File`-Objekt direkt an `XMLHttpRequest.send` (der Browser streamt), Download ist ein Link mit `download`-Attribut – nie `fetch().blob()`. → Aufgabe 14 (Test prüft, dass `send` das `File` bekommt), Aufgabe 13 (Link-`href`).
- **Entpacken bei laufendem Server (Focus 5):** Entpacken nur bei `stopped` anbietbar, Start/Neustart gesperrt, solange ein Job läuft. → Aufgabe 16.

## Dateistruktur (B3)

```
web/src/features/files/path.ts (+test)        (13) Pfadregeln wie filesapi.CleanPath, Join/Parent/Base/Brotkrumen
web/src/features/files/format.ts (+test)      (13) Größen, Sortierung, Editor-Sprache, Grenzen
web/src/features/files/api.ts                 (13) Query-Keys, Query-Options, Aufrufe, Download-URL
web/src/features/files/use-file-job.ts        (13) Job-Abfrage (2-s-Polling während "running")
web/src/features/files/name-dialog.tsx        (13) Dialog für Neuer Ordner / Neue Datei / Umbenennen
web/src/features/files/file-list.tsx          (13) DataList mit Namen, Größe, Datum, Aktionsmenü
web/src/features/files/files-tab.tsx (+test)  (13–16) Tab: Brotkrumen, Werkzeugleiste, Liste, Dialoge
web/src/features/files/upload.ts (+test)      (14) uploadFile per XMLHttpRequest
web/src/features/files/upload-queue.ts (+test)(14) Reducer + Hook useUploadQueue
web/src/features/files/upload-list.tsx        (14) Anzeige der Warteschlange
web/src/features/files/code-editor.tsx        (15) CodeMirror (default export, lazy)
web/src/features/files/file-editor.tsx (+test)(15) Laden, Speichern, Konflikt, Verlassen-Warnung
web/src/features/files/extract-dialog.tsx     (16) Entpack-Dialog
web/src/features/files/job-status.tsx         (16) Fortschritt des Entpack-Jobs
web/src/features/servers/server-detail-page.tsx (+test) (13, 15, 16) Tab, Dirty-Wechsel, Job-Sperre
web/src/features/servers/server-overview.tsx, power-buttons.tsx (16) Power-Sperre
web/src/features/users/permission-matrix.ts (+test), permissions-page.tsx (+test) (16) Rechte-Hinweis
web/src/i18n/de.ts, en.ts, i18n.test.ts       (13) alle Texte von 3b
web/e2e/servers.spec.ts                       (17) Playwright
web/package.json, package-lock.json           (15) CodeMirror-Pakete
```

---

### Task 13: Grundlagen und Dateiliste

**Files:**
- Create: `web/src/features/files/path.ts`, `path.test.ts`, `format.ts`, `format.test.ts`, `api.ts`, `use-file-job.ts`, `name-dialog.tsx`, `file-list.tsx`, `files-tab.tsx`, `files-tab.test.tsx`
- Modify: `web/src/features/servers/server-detail-page.tsx`, `web/src/features/servers/server-detail-page.test.tsx`
- Modify: `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts`

**Interfaces:**
- Consumes: `api` (`@/api/client`), `unwrap`/`unwrapVoid`/`isApiError` (`@/api/errors`), Datei-Typen aus `@/api/types` (Phase A), `DataList`, `ConfirmDialog`, `SectionCard`, `useFormat`, `useErrorToast`, `errorMessage`.
- Produces (von Aufgaben 14–16 genutzt):
  - `path.ts`: `MAX_PATH_BYTES`, `MAX_COMPONENT_BYTES`, `type PathProblem`, `cleanPath(input): { path: string } | { problem: PathProblem }`, `pathProblem(input): PathProblem | null`, `componentProblem(name, dir?): PathProblem | null`, `targetProblem(target): PathProblem | null`, `isHiddenPath(path): boolean`, `joinPath(dir, name)`, `parentPath(path)`, `baseName(path)`, `breadcrumbs(path): { name: string; path: string }[]`, `stripZip(name)`.
  - `format.ts`: `EDITOR_MAX_BYTES`, `MAX_UPLOAD_BYTES`, `formatBytes(bytes, locale)`, `type EditorLanguage`, `editorLanguage(name)`, `type SortKey`, `sortEntries(entries, key, descending)`, `isZip(name)`.
  - `api.ts`: `JOB_POLL_MS`, `filesKey(id)`, `fileListsKey(id)`, `fileListKey(id, path)`, `fileContentKey(id, path)`, `fileJobKey(id)`, `fileListQueryOptions(id, path)`, `readFile(id, path)`, `writeFile(id, body)`, `makeDirectory(id, path)`, `renameFile(id, from, to)`, `deleteFile(id, path)`, `extractArchive(id, path, dest)`, `fileJobQueryOptions(id)`, `downloadUrl(id, path)`.
  - `use-file-job.ts`: `useFileJob(serverId, enabled)`, `isExtracting(data)`.
  - `files-tab.tsx`: `FilesTab({ server, onOpenSettings?, onDirtyChange? })`.
  - `file-list.tsx`: `FileList` mit optionalen `onOpenFile`, `onExtract` (Aufgaben 15/16 schalten sie ein).
  - i18n: kompletter Block `files.*` für 3b sowie `servers.detail.tabs.files`, `servers.extracting_lock`, `permissions.files_warning`.

- [ ] **Step 1: Tests für Pfadregeln und Formatierung schreiben (rot)**

`web/src/features/files/path.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { baseName, breadcrumbs, cleanPath, componentProblem, isHiddenPath, joinPath, parentPath, pathProblem, stripZip, targetProblem } from './path'

describe('cleanPath (mirrors filesapi.CleanPath)', () => {
  it.each([
    ['', ''],
    ['.', ''],
    ['server.properties', 'server.properties'],
    ['plugins/Essentials/config.yml', 'plugins/Essentials/config.yml'],
    ['Welt mit Leerzeichen/level.dat', 'Welt mit Leerzeichen/level.dat'],
    ['ünïcödé/日本', 'ünïcödé/日本'],
    ['plugins/Essentials/', 'plugins/Essentials'], // one trailing slash is fine (as in filesapi)
    ['.hidden', '.hidden'],
    ['a..b', 'a..b'],
  ])('accepts %j', (input, path) => {
    expect(cleanPath(input)).toEqual({ path })
  })

  it.each([
    ['/etc/passwd', 'invalid'],
    ['plugins\\x', 'invalid'],
    ['a//b', 'invalid'],
    ['a/b//', 'invalid'],
    ['a/./b', 'invalid'],
    ['./a', 'invalid'],
    ['..', 'dot_dot'],
    ['plugins/../../etc', 'dot_dot'],
    ['a\nb', 'control'],
    ['a\u0000b', 'control'],
    ['a\u007fb', 'control'],
    ['bad\ud800', 'invalid'],
    [`${'a/'.repeat(2048)}b`, 'too_long'],
    ['x'.repeat(256), 'component_too_long'],
    [`${'ä'.repeat(128)}`, 'component_too_long'], // 256 Byte UTF-8
  ])('rejects %j as %s', (input, problem) => {
    expect(cleanPath(input)).toEqual({ problem })
    expect(pathProblem(input)).toBe(problem)
  })

  it('allows exactly 255 bytes per component', () => {
    expect(pathProblem('x'.repeat(255))).toBeNull()
  })
})

describe('hidden names', () => {
  it.each(['.rcon-cli.env', '.rcon-cli.yaml', '.mcpanel-tmp-1', 'world/.mcpanel-tmp-x/a'])('hides %j', (p) => {
    expect(isHiddenPath(p)).toBe(true)
  })
  it.each(['plugins/.rcon-cli.env', 'rcon-cli.env', '.mcpanelx', 'server.properties'])('shows %j', (p) => {
    expect(isHiddenPath(p)).toBe(false)
  })
})

describe('componentProblem', () => {
  it.each([
    ['', 'empty'],
    ['   ', 'empty'],
    ['a/b', 'slash'],
    ['..', 'dot_dot'],
    ['.', 'invalid'],
    ['a\tb', 'control'],
    ['.mcpanel-x', 'hidden'],
    ['x'.repeat(256), 'component_too_long'],
  ])('rejects %j as %s', (name, problem) => {
    expect(componentProblem(name)).toBe(problem)
  })
  it('accepts a normal name', () => {
    expect(componentProblem('config.yml')).toBeNull()
  })
  it('rejects the top-level RCON files only at the top level', () => {
    expect(componentProblem('.rcon-cli.env', '')).toBe('hidden')
    expect(componentProblem('.rcon-cli.env', 'plugins')).toBeNull()
  })
})

describe('targetProblem', () => {
  it.each([
    ['', 'empty'],
    ['plugins/../x', 'dot_dot'],
    ['.rcon-cli.env', 'hidden'],
    ['world/.mcpanel-tmp', 'hidden'],
  ])('rejects %j as %s', (target, problem) => {
    expect(targetProblem(target)).toBe(problem)
  })
  it('accepts a nested target', () => {
    expect(targetProblem('plugins/alt/config.yml')).toBeNull()
  })
})

describe('path helpers', () => {
  it('joins, splits and names paths', () => {
    expect(joinPath('', 'a')).toBe('a')
    expect(joinPath('a/b', 'c')).toBe('a/b/c')
    expect(parentPath('a/b/c')).toBe('a/b')
    expect(parentPath('a')).toBe('')
    expect(parentPath('')).toBe('')
    expect(baseName('a/b/c.yml')).toBe('c.yml')
    expect(baseName('')).toBe('')
    expect(stripZip('world-backup.zip')).toBe('world-backup')
    expect(stripZip('WORLD.ZIP')).toBe('WORLD')
    expect(stripZip('notes.txt')).toBe('notes.txt')
  })

  it('builds breadcrumbs from the root', () => {
    expect(breadcrumbs('')).toEqual([])
    expect(breadcrumbs('plugins/Essentials')).toEqual([
      { name: 'plugins', path: 'plugins' },
      { name: 'Essentials', path: 'plugins/Essentials' },
    ])
  })
})
```

`web/src/features/files/format.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { editorLanguage, formatBytes, isZip, sortEntries } from './format'
import type { FileEntry } from '@/api/types'

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [1023, '1.023 B'],
    [1024, '1 KiB'],
    [1536, '1,5 KiB'],
    [2 * 1024 * 1024, '2 MiB'],
    [4 * 1024 ** 3, '4 GiB'],
  ])('%d bytes → %s', (bytes, text) => {
    expect(formatBytes(bytes, 'de')).toBe(text)
  })
  it('uses the locale', () => {
    expect(formatBytes(1536, 'en')).toBe('1.5 KiB')
  })
})

describe('editorLanguage', () => {
  it.each([
    ['config.yml', 'yaml'],
    ['paper-global.YAML', 'yaml'],
    ['ops.json', 'json'],
    ['pack.mcmeta', 'json'],
    ['fabric.toml', 'toml'],
    ['server.properties', 'properties'],
    ['settings.ini', 'properties'],
    ['latest.log', 'text'],
    ['Dockerfile', 'text'],
    ['.gitignore', 'text'],
  ])('%s → %s', (name, language) => {
    expect(editorLanguage(name)).toBe(language)
  })
})

describe('sortEntries', () => {
  const entries: FileEntry[] = [
    { name: 'b.txt', type: 'file', size: 10, mtime: 3 },
    { name: 'world', type: 'dir', size: 0, mtime: 1 },
    { name: 'a10.txt', type: 'file', size: 30, mtime: 2 },
    { name: 'a9.txt', type: 'file', size: 20, mtime: 4 },
    { name: 'plugins', type: 'dir', size: 0, mtime: 5 },
  ]
  const names = (list: FileEntry[]) => list.map((e) => e.name)

  it('keeps folders first and sorts names naturally', () => {
    expect(names(sortEntries(entries, 'name', false))).toEqual(['plugins', 'world', 'a9.txt', 'a10.txt', 'b.txt'])
    expect(names(sortEntries(entries, 'name', true))).toEqual(['world', 'plugins', 'b.txt', 'a10.txt', 'a9.txt'])
  })
  it('sorts by size and date', () => {
    expect(names(sortEntries(entries, 'size', true)).slice(2)).toEqual(['a10.txt', 'a9.txt', 'b.txt'])
    expect(names(sortEntries(entries, 'mtime', false))).toEqual(['world', 'plugins', 'a10.txt', 'b.txt', 'a9.txt'])
  })
  it('does not change the input', () => {
    const copy = [...entries]
    sortEntries(entries, 'size', false)
    expect(entries).toEqual(copy)
  })
})

it('recognises zip files', () => {
  expect(isZip('world.zip')).toBe(true)
  expect(isZip('WORLD.ZIP')).toBe(true)
  expect(isZip('world.zip.txt')).toBe(false)
})
```

Run: `make check` → Expected: FAIL (`./path` und `./format` fehlen).

- [ ] **Step 2: Pfadregeln und Formatierung implementieren**

`web/src/features/files/path.ts`:

```ts
/** Same rules as filesapi.CleanPath (spec §3.4). The service checks again; this only spares a round trip. */
export const MAX_PATH_BYTES = 4096
export const MAX_COMPONENT_BYTES = 255

export type PathProblem = 'empty' | 'slash' | 'invalid' | 'dot_dot' | 'control' | 'too_long' | 'component_too_long' | 'hidden'

const encoder = new TextEncoder()
const byteLength = (s: string) => encoder.encode(s).length
/** A lone surrogate is not valid UTF-8 (a paired one forms a single code point and does not match). */
const LONE_SURROGATE = /\p{Cs}/u
const HIDDEN_TOP = new Set(['.rcon-cli.env', '.rcon-cli.yaml'])
const HIDDEN_PREFIX = '.mcpanel-'

function hasControl(s: string): boolean {
  for (const ch of s) {
    const code = ch.codePointAt(0) ?? 0
    if (code < 0x20 || code === 0x7f) return true
  }
  return false
}

export function cleanPath(input: string): { path: string } | { problem: PathProblem } {
  if (input === '' || input === '.') return { path: '' }
  if (hasControl(input)) return { problem: 'control' }
  if (LONE_SURROGATE.test(input) || input.startsWith('/') || input.includes('\\')) return { problem: 'invalid' }
  if (byteLength(input) > MAX_PATH_BYTES) return { problem: 'too_long' }
  // Like filesapi.CleanPath: one trailing slash (a folder path) is dropped, a second one is an empty component.
  const parts = (input.endsWith('/') ? input.slice(0, -1) : input).split('/')
  for (const part of parts) {
    if (part === '..') return { problem: 'dot_dot' }
    if (part === '' || part === '.') return { problem: 'invalid' }
    if (byteLength(part) > MAX_COMPONENT_BYTES) return { problem: 'component_too_long' }
  }
  return { path: parts.join('/') }
}

export function pathProblem(input: string): PathProblem | null {
  const result = cleanPath(input)
  return 'problem' in result ? result.problem : null
}

/** True for the top-level RCON files and anything named .mcpanel-* at any depth (spec §3.4). */
export function isHiddenPath(path: string): boolean {
  if (path === '') return false
  const parts = path.split('/')
  if (parts.length === 1 && HIDDEN_TOP.has(parts[0])) return true
  return parts.some((p) => p.startsWith(HIDDEN_PREFIX))
}

/** Checks a single new name (folder, file, upload) that will be created in dir. */
export function componentProblem(name: string, dir = ''): PathProblem | null {
  if (name.trim() === '') return 'empty'
  if (name.includes('/')) return 'slash'
  const problem = pathProblem(name)
  if (problem) return problem
  if (name === '.') return 'invalid'
  return isHiddenPath(joinPath(dir, name)) ? 'hidden' : null
}

/** A rename/move target: a valid, non-root path that names nothing hidden. */
export function targetProblem(target: string): PathProblem | null {
  if (target.trim() === '') return 'empty'
  return pathProblem(target) ?? (isHiddenPath(target) ? 'hidden' : null)
}

export function joinPath(dir: string, name: string): string {
  return dir === '' ? name : `${dir}/${name}`
}

export function parentPath(path: string): string {
  const i = path.lastIndexOf('/')
  return i < 0 ? '' : path.slice(0, i)
}

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1)
}

export function breadcrumbs(path: string): { name: string; path: string }[] {
  if (path === '') return []
  const parts = path.split('/')
  return parts.map((name, i) => ({ name, path: parts.slice(0, i + 1).join('/') }))
}

export function stripZip(name: string): string {
  return name.toLowerCase().endsWith('.zip') ? name.slice(0, -4) : name
}
```

`web/src/features/files/format.ts`:

```ts
import type { FileEntry } from '@/api/types'

/** Defaults of files.toml (spec §3.1); the service is authoritative, the UI only avoids pointless requests. */
export const EDITOR_MAX_BYTES = 5 * 1024 * 1024
export const MAX_UPLOAD_BYTES = 4 * 1024 ** 3

const UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'] as const

export function formatBytes(bytes: number, locale: string): string {
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < UNITS.length - 1) {
    value /= 1024
    unit += 1
  }
  const text = new Intl.NumberFormat(locale, { maximumFractionDigits: unit === 0 ? 0 : 1 }).format(value)
  return `${text} ${UNITS[unit]}`
}

export type EditorLanguage = 'yaml' | 'json' | 'toml' | 'properties' | 'text'

const LANGUAGES: Record<string, EditorLanguage> = {
  yml: 'yaml',
  yaml: 'yaml',
  json: 'json',
  json5: 'json',
  mcmeta: 'json',
  toml: 'toml',
  properties: 'properties',
  ini: 'properties',
  cfg: 'properties',
  conf: 'properties',
}

export function editorLanguage(name: string): EditorLanguage {
  const dot = name.lastIndexOf('.')
  if (dot <= 0) return 'text' // no extension, or a dotfile such as .gitignore
  return LANGUAGES[name.slice(dot + 1).toLowerCase()] ?? 'text'
}

export type SortKey = 'name' | 'size' | 'mtime'

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' })

/** Folders always come first; ties fall back to the name. Returns a new array. */
export function sortEntries(entries: readonly FileEntry[], key: SortKey, descending: boolean): FileEntry[] {
  const factor = descending ? -1 : 1
  return [...entries].sort((a, b) => {
    const folderA = a.type === 'dir' ? 0 : 1
    const folderB = b.type === 'dir' ? 0 : 1
    if (folderA !== folderB) return folderA - folderB
    let cmp = 0
    if (key === 'size') cmp = a.size - b.size
    if (key === 'mtime') cmp = a.mtime - b.mtime
    if (cmp === 0) cmp = collator.compare(a.name, b.name)
    return cmp * factor
  })
}

export function isZip(name: string): boolean {
  return name.toLowerCase().endsWith('.zip')
}
```

Hinweis zum Test `formatBytes(1023, 'de')`: `Intl` gruppiert in `de` ab vier Stellen mit Punkt (`1.023 B`); stimmt das im CI-Node nicht, ist der Test (nicht der Code) an die tatsächliche Ausgabe von `Intl.NumberFormat('de')` anzupassen.

- [ ] **Step 3: API-Modul und Job-Hook**

`web/src/features/files/api.ts`:

```ts
import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { FileContent, FileJob, FileJobResponse, FileList, FileWriteResult, WriteFileRequest } from '@/api/types'

/** The job is polled every 2 s while it runs (spec §6.3). */
export const JOB_POLL_MS = 2_000

/** Below serverKey(id): invalidating the server also refreshes listings. */
export const filesKey = (id: number) => ['servers', id, 'files'] as const
export const fileListsKey = (id: number) => ['servers', id, 'files', 'list'] as const
export const fileListKey = (id: number, path: string) => ['servers', id, 'files', 'list', path] as const
export const fileJobKey = (id: number) => ['servers', id, 'files', 'job'] as const
/**
 * Deliberately NOT below ['servers', …]: power actions invalidate every server query, and a refetched file
 * would replace the mtime the editor compares against (spec §6.2: conflicts must be detected, not hidden).
 */
export const fileContentKey = (id: number, path: string) => ['file-content', id, path] as const

export function fileListQueryOptions(id: number, path: string) {
  return queryOptions<FileList>({
    queryKey: fileListKey(id, path),
    queryFn: () => unwrap(api.GET('/servers/{id}/files', { params: { path: { id }, query: { path } } })),
  })
}

export function readFile(id: number, path: string): Promise<FileContent> {
  return unwrap(api.GET('/servers/{id}/files/content', { params: { path: { id }, query: { path } } }))
}

export function writeFile(id: number, body: WriteFileRequest): Promise<FileWriteResult> {
  return unwrap(api.PUT('/servers/{id}/files/content', { params: { path: { id } }, body }))
}

export function makeDirectory(id: number, path: string): Promise<void> {
  return unwrapVoid(api.POST('/servers/{id}/files/mkdir', { params: { path: { id } }, body: { path } }))
}

export function renameFile(id: number, from: string, to: string): Promise<void> {
  return unwrapVoid(api.POST('/servers/{id}/files/rename', { params: { path: { id } }, body: { from, to } }))
}

export function deleteFile(id: number, path: string): Promise<void> {
  return unwrapVoid(api.POST('/servers/{id}/files/delete', { params: { path: { id } }, body: { path } }))
}

export function extractArchive(id: number, path: string, dest: string): Promise<FileJob> {
  return unwrap(api.POST('/servers/{id}/files/extract', { params: { path: { id } }, body: { path, dest } }))
}

export function fileJobQueryOptions(id: number) {
  return queryOptions<FileJobResponse>({
    queryKey: fileJobKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/files/jobs', { params: { path: { id } } })),
  })
}

/**
 * A plain same-origin link: the browser streams the file (or the folder zip) to disk itself, so even a 4 GiB
 * world never passes through JavaScript memory. The session cookie authenticates it like the console stream.
 */
export function downloadUrl(id: number, path: string): string {
  return `/api/servers/${id}/files/download?path=${encodeURIComponent(path)}`
}
```

`web/src/features/files/use-file-job.ts`:

```ts
import { useQuery } from '@tanstack/react-query'
import { JOB_POLL_MS, fileJobQueryOptions } from './api'
import type { FileJobResponse } from '@/api/types'

export function isExtracting(data: FileJobResponse | undefined): boolean {
  return data?.job?.state === 'running'
}

/** Shared by the files tab and the detail page (same query key): polls only while a job runs. */
export function useFileJob(serverId: number, enabled: boolean) {
  return useQuery({
    ...fileJobQueryOptions(serverId),
    enabled,
    refetchInterval: (query) => (isExtracting(query.state.data) ? JOB_POLL_MS : false),
  })
}
```

- [ ] **Step 4: i18n – alle Texte für 3b**

In `web/src/i18n/de.ts`: in `servers.detail.tabs` nach `players: 'Spieler',` die Zeile `files: 'Dateien',` einfügen; in `servers` nach `no_power: 'Keine Berechtigung für Start/Stopp.',` die Zeile

```ts
    extracting_lock: 'Entpacken läuft – Starten ist erst danach möglich.',
```

in `permissions` nach `unsaved: 'Ungespeicherte Änderungen',`:

```ts
    files_warning: '„Dateien“ erlaubt Code-Ausführung im Minecraft-Server (z. B. über Plugins). Vergib es nur an Personen, denen du voll vertraust.',
```

und vor `settings: {` (oberste Ebene, nach dem Block `players`) den Block:

```ts
  files: {
    title: 'Dateien',
    root: 'Server-Daten',
    breadcrumbs: 'Pfad',
    caption: 'Inhalt von {{dir}}',
    running_hint: 'Der Server läuft. Änderungen wirken meist erst nach einem Neustart.',
    never_started: 'Der Server wurde noch nie gestartet – es gibt noch keine Daten.',
    dir_gone: 'Diesen Ordner gibt es nicht mehr.',
    back_to_root: 'Zur obersten Ebene',
    empty: 'Dieser Ordner ist leer.',
    truncated: 'Es werden nur die ersten {{count}} Einträge angezeigt.',
    col: { name: 'Name', size: 'Größe', modified: 'Geändert', actions: 'Aktionen' },
    sort: {
      label: 'Sortieren nach',
      name: 'Name',
      size: 'Größe',
      mtime: 'Änderungsdatum',
      ascending: 'Aufsteigend sortiert – umkehren',
      descending: 'Absteigend sortiert – umkehren',
    },
    type: { file: 'Datei', dir: 'Ordner', symlink: 'Verknüpfung', other: 'Sonderdatei' },
    actions_label: 'Aktionen für {{name}}',
    open: 'Öffnen',
    edit: 'Bearbeiten',
    download: 'Herunterladen',
    download_zip: 'Als Zip herunterladen',
    rename: 'Umbenennen oder verschieben',
    delete: 'Löschen',
    toolbar: {
      new_folder: 'Neuer Ordner',
      new_file: 'Neue Datei',
      upload: 'Hochladen',
      upload_input: 'Dateien hochladen',
      drop: 'Dateien hier ablegen, um sie nach {{dir}} hochzuladen',
    },
    dialog: {
      name: 'Name',
      target: 'Neuer Pfad',
      target_hint: 'Pfad relativ zu den Server-Daten, z. B. plugins/alt/config.yml.',
      new_folder_title: 'Neuer Ordner in {{dir}}',
      new_file_title: 'Neue Datei in {{dir}}',
      rename_title: '„{{name}}“ umbenennen oder verschieben',
      create: 'Anlegen',
      rename_submit: 'Umbenennen',
      delete_title: '„{{name}}“ löschen?',
      delete_file: 'Die Datei wird endgültig gelöscht.',
      delete_dir: 'Der Ordner wird inklusive Inhalt endgültig gelöscht.',
      delete_submit: 'Endgültig löschen',
    },
    done: {
      mkdir: 'Ordner „{{name}}“ angelegt.',
      newfile: 'Datei „{{name}}“ angelegt.',
      rename: '„{{from}}“ heißt jetzt „{{to}}“.',
      delete: '„{{name}}“ gelöscht.',
    },
    validation: {
      empty: 'Gib einen Namen ein.',
      slash: 'Ein Name darf kein „/“ enthalten.',
      invalid: 'Ungültiger Pfad: kein „/“ am Anfang, kein „\\“, keine leeren Teile und kein „.“.',
      dot_dot: '„..“ ist nicht erlaubt.',
      control: 'Steuerzeichen sind nicht erlaubt.',
      too_long: 'Der Pfad ist zu lang (höchstens 4096 Byte).',
      component_too_long: 'Ein Name ist zu lang (höchstens 255 Byte).',
      hidden: 'Dieser Name ist für das panel reserviert.',
      too_large: 'Die Datei ist größer als 4 GiB.',
    },
    uploads: {
      title: 'Uploads',
      progress: 'Fortschritt von {{name}}',
      status: {
        queued: 'Wartet',
        uploading: 'Lädt hoch …',
        conflict: 'Existiert bereits',
        done: 'Hochgeladen',
        failed: 'Fehlgeschlagen',
        cancelled: 'Abgebrochen',
        skipped: 'Übersprungen',
      },
      cancel: '{{name}} abbrechen',
      clear: 'Erledigte entfernen',
      conflict_title: '„{{name}}“ existiert bereits',
      conflict_description: 'Soll die vorhandene Datei überschrieben werden?',
      overwrite: 'Überschreiben',
      skip: 'Überspringen',
    },
    editor: {
      label: 'Inhalt von {{name}}',
      back: 'Zurück zur Liste',
      saving: 'Speichert …',
      saved: 'Gespeichert.',
      unsaved: 'Ungespeicherte Änderungen',
      not_text: 'Diese Datei ist zu groß für den Editor oder keine Textdatei.',
      download_instead: 'Stattdessen herunterladen',
      properties_hint:
        'server.properties wird beim Start aus den panel-Einstellungen überschrieben. rcon.password wird geschwärzt angezeigt und bleibt beim Speichern erhalten.',
      open_settings: 'Zu den Einstellungen',
      conflict_title: 'Datei wurde geändert',
      conflict_description:
        'Die Datei wurde gespeichert, seit du sie geöffnet hast. „Neu laden“ verwirft deine Änderungen, „Überschreiben“ ersetzt die anderen.',
      reload: 'Neu laden',
      overwrite: 'Überschreiben',
      discard_title: 'Ungespeicherte Änderungen verwerfen?',
      discard_description: 'Deine Änderungen an {{name}} gehen verloren.',
      discard_tab: 'Deine ungespeicherten Änderungen im Editor gehen verloren.',
      discard: 'Verwerfen',
    },
    extract: {
      action: 'Entpacken …',
      title: '„{{name}}“ entpacken',
      dest: 'Zielordner',
      dest_hint: 'Der Ordner muss neu oder leer sein.',
      submit: 'Entpacken',
      stopped_only: 'Entpacken geht nur bei gestopptem Server.',
      running: 'Entpacken von „{{name}}“ läuft …',
      progress: 'Fortschritt beim Entpacken',
      counts: '{{entries}} von {{total_entries}} Einträgen · {{bytes}} von {{total_bytes}}',
      done: '„{{name}}“ wurde nach {{dest}} entpackt.',
      failed: 'Entpacken von „{{name}}“ fehlgeschlagen: {{error}}',
    },
  },
```

In `web/src/i18n/en.ts` an denselben Stellen (`en: Messages` erzwingt dieselben Schlüssel): `files: 'Files',` in `servers.detail.tabs`; `extracting_lock: 'Extracting – starting is possible once it has finished.',` in `servers`; in `permissions`:

```ts
    files_warning: '“Files” allows running code inside the Minecraft server (e.g. via plugins). Grant it only to people you fully trust.',
```

und der Block:

```ts
  files: {
    title: 'Files',
    root: 'Server data',
    breadcrumbs: 'Path',
    caption: 'Contents of {{dir}}',
    running_hint: 'The server is running. Changes usually take effect after a restart.',
    never_started: 'The server has never been started – there is no data yet.',
    dir_gone: 'This folder no longer exists.',
    back_to_root: 'Back to the top level',
    empty: 'This folder is empty.',
    truncated: 'Only the first {{count}} entries are shown.',
    col: { name: 'Name', size: 'Size', modified: 'Modified', actions: 'Actions' },
    sort: {
      label: 'Sort by',
      name: 'Name',
      size: 'Size',
      mtime: 'Date modified',
      ascending: 'Sorted ascending – reverse',
      descending: 'Sorted descending – reverse',
    },
    type: { file: 'File', dir: 'Folder', symlink: 'Link', other: 'Special file' },
    actions_label: 'Actions for {{name}}',
    open: 'Open',
    edit: 'Edit',
    download: 'Download',
    download_zip: 'Download as zip',
    rename: 'Rename or move',
    delete: 'Delete',
    toolbar: {
      new_folder: 'New folder',
      new_file: 'New file',
      upload: 'Upload',
      upload_input: 'Upload files',
      drop: 'Drop files here to upload them to {{dir}}',
    },
    dialog: {
      name: 'Name',
      target: 'New path',
      target_hint: 'Path relative to the server data, e.g. plugins/old/config.yml.',
      new_folder_title: 'New folder in {{dir}}',
      new_file_title: 'New file in {{dir}}',
      rename_title: 'Rename or move “{{name}}”',
      create: 'Create',
      rename_submit: 'Rename',
      delete_title: 'Delete “{{name}}”?',
      delete_file: 'The file is deleted permanently.',
      delete_dir: 'The folder and everything in it is deleted permanently.',
      delete_submit: 'Delete permanently',
    },
    done: {
      mkdir: 'Folder “{{name}}” created.',
      newfile: 'File “{{name}}” created.',
      rename: '“{{from}}” is now “{{to}}”.',
      delete: '“{{name}}” deleted.',
    },
    validation: {
      empty: 'Enter a name.',
      slash: 'A name must not contain “/”.',
      invalid: 'Invalid path: no “/” at the start, no “\\”, no empty parts and no “.”.',
      dot_dot: '“..” is not allowed.',
      control: 'Control characters are not allowed.',
      too_long: 'The path is too long (at most 4096 bytes).',
      component_too_long: 'A name is too long (at most 255 bytes).',
      hidden: 'This name is reserved for the panel.',
      too_large: 'The file is larger than 4 GiB.',
    },
    uploads: {
      title: 'Uploads',
      progress: 'Progress of {{name}}',
      status: {
        queued: 'Waiting',
        uploading: 'Uploading …',
        conflict: 'Already exists',
        done: 'Uploaded',
        failed: 'Failed',
        cancelled: 'Cancelled',
        skipped: 'Skipped',
      },
      cancel: 'Cancel {{name}}',
      clear: 'Remove finished',
      conflict_title: '“{{name}}” already exists',
      conflict_description: 'Overwrite the existing file?',
      overwrite: 'Overwrite',
      skip: 'Skip',
    },
    editor: {
      label: 'Contents of {{name}}',
      back: 'Back to the list',
      saving: 'Saving …',
      saved: 'Saved.',
      unsaved: 'Unsaved changes',
      not_text: 'This file is too large for the editor or not a text file.',
      download_instead: 'Download instead',
      properties_hint:
        'server.properties is overwritten from the panel settings on start. rcon.password is shown redacted and is kept when you save.',
      open_settings: 'Open settings',
      conflict_title: 'File was changed',
      conflict_description: 'The file was saved since you opened it. “Reload” discards your changes, “Overwrite” replaces the other ones.',
      reload: 'Reload',
      overwrite: 'Overwrite',
      discard_title: 'Discard unsaved changes?',
      discard_description: 'Your changes to {{name}} will be lost.',
      discard_tab: 'Your unsaved changes in the editor will be lost.',
      discard: 'Discard',
    },
    extract: {
      action: 'Extract …',
      title: 'Extract “{{name}}”',
      dest: 'Target folder',
      dest_hint: 'The folder must be new or empty.',
      submit: 'Extract',
      stopped_only: 'Extracting is only possible while the server is stopped.',
      running: 'Extracting “{{name}}” …',
      progress: 'Extraction progress',
      counts: '{{entries}} of {{total_entries}} entries · {{bytes}} of {{total_bytes}}',
      done: '“{{name}}” was extracted to {{dest}}.',
      failed: 'Extracting “{{name}}” failed: {{error}}',
    },
  },
```

In `web/src/i18n/i18n.test.ts` in `DYNAMIC_KEYS` (vor dem `...['not_configured', …`-Eintrag) ergänzen:

```ts
  ...['file', 'dir', 'symlink', 'other'].map((k) => `files.type.${k}`),
  ...['name', 'size', 'mtime'].map((k) => `files.sort.${k}`),
  ...['empty', 'slash', 'invalid', 'dot_dot', 'control', 'too_long', 'component_too_long', 'hidden', 'too_large'].map(
    (p) => `files.validation.${p}`,
  ),
  ...['queued', 'uploading', 'conflict', 'done', 'failed', 'cancelled', 'skipped'].map((s) => `files.uploads.status.${s}`),
```

- [ ] **Step 5: Tests für Tab und Liste schreiben (rot)**

`web/src/features/files/files-tab.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'

export async function openFiles(serverId = 1, username = 'admin') {
  const user = userEvent.setup()
  await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs(username) })
  await user.click(await screen.findByRole('tab', { name: 'Dateien' }))
  const region = await screen.findByRole('region', { name: 'Dateien' })
  return { user, region }
}

describe('FilesTab', () => {
  it('lists the top level with folders, files and links', async () => {
    const { region } = await openFiles()
    expect(await within(region).findByRole('button', { name: 'plugins' })).toBeInTheDocument()
    expect(within(region).getByText('server.properties')).toBeInTheDocument()
    expect(within(region).getByText('current')).toBeInTheDocument()
    expect(within(region).getByText('Verknüpfung')).toBeInTheDocument()
    expect(screen.getByText('Der Server läuft. Änderungen wirken meist erst nach einem Neustart.')).toBeInTheDocument()
  })

  it('opens folders and walks back via the breadcrumbs', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'plugins' }))
    expect(await within(region).findByRole('button', { name: 'Essentials' })).toBeInTheDocument()
    const crumbs = screen.getByRole('navigation', { name: 'Pfad' })
    expect(within(crumbs).getByText('plugins')).toHaveAttribute('aria-current', 'page')
    await user.click(within(crumbs).getByRole('button', { name: 'Server-Daten' }))
    expect(await within(region).findByText('server.properties')).toBeInTheDocument()
  })

  it('links downloads straight to the API', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für server.properties' }))
    expect(screen.getByRole('menuitem', { name: 'Herunterladen' })).toHaveAttribute(
      'href',
      '/api/servers/1/files/download?path=server.properties',
    )
    await user.keyboard('{Escape}')
    await user.click(within(region).getByRole('button', { name: 'Aktionen für plugins' }))
    expect(screen.getByRole('menuitem', { name: 'Als Zip herunterladen' })).toHaveAttribute('href', '/api/servers/1/files/download?path=plugins')
  })

  it('creates a folder after checking the name', async () => {
    const { user, region } = await openFiles()
    await user.click(await screen.findByRole('button', { name: 'Neuer Ordner' }))
    const dialog = await screen.findByRole('dialog', { name: 'Neuer Ordner in Server-Daten' })
    const input = within(dialog).getByLabelText('Name')
    await user.type(input, 'a/b')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    expect(within(dialog).getByText('Ein Name darf kein „/“ enthalten.')).toBeInTheDocument()
    await user.clear(input)
    await user.type(input, 'backups')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    expect(await within(region).findByRole('button', { name: 'backups' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('refuses reserved names before sending', async () => {
    const { user } = await openFiles()
    await user.click(await screen.findByRole('button', { name: 'Neue Datei' }))
    const dialog = await screen.findByRole('dialog', { name: 'Neue Datei in Server-Daten' })
    await user.type(within(dialog).getByLabelText('Name'), '.rcon-cli.env')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    expect(within(dialog).getByText('Dieser Name ist für das panel reserviert.')).toBeInTheDocument()
  })

  it('renames a file and shows a conflict inline', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für bukkit.yml' }))
    await user.click(screen.getByRole('menuitem', { name: 'Umbenennen oder verschieben' }))
    const dialog = await screen.findByRole('dialog', { name: '„bukkit.yml“ umbenennen oder verschieben' })
    const input = within(dialog).getByLabelText('Neuer Pfad')
    expect(input).toHaveValue('bukkit.yml')
    await user.clear(input)
    await user.type(input, 'server.properties')
    await user.click(within(dialog).getByRole('button', { name: 'Umbenennen' }))
    expect(await within(dialog).findByRole('alert')).toBeInTheDocument() // 409 file_exists
    await user.clear(input)
    await user.type(input, 'bukkit-alt.yml')
    await user.click(within(dialog).getByRole('button', { name: 'Umbenennen' }))
    expect(await within(region).findByText('bukkit-alt.yml')).toBeInTheDocument()
    expect(within(region).queryByText('bukkit.yml')).not.toBeInTheDocument()
  })

  it('deletes a folder after confirmation', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für logs' }))
    await user.click(screen.getByRole('menuitem', { name: 'Löschen' }))
    const dialog = await screen.findByRole('dialog', { name: '„logs“ löschen?' })
    expect(within(dialog).getByText('Der Ordner wird inklusive Inhalt endgültig gelöscht.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Endgültig löschen' }))
    await waitFor(() => expect(within(region).queryByRole('button', { name: 'logs' })).not.toBeInTheDocument())
  })

  it('explains a server that was never started', async () => {
    server.use(http.get('*/api/servers/:id/files', () => HttpResponse.json({ error: 'not_found', message: 'not found' }, { status: 404 })))
    const { region } = await openFiles()
    expect(await within(region).findByText('Der Server wurde noch nie gestartet – es gibt noch keine Daten.')).toBeInTheDocument()
  })

  it('sorts by size with folders first', async () => {
    const { user, region } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    const names = () =>
      within(region)
        .getAllByRole('row')
        .slice(1)
        .map((row) => within(row).getAllByRole('cell')[0].textContent ?? '')
    await user.selectOptions(screen.getByLabelText('Sortieren nach'), 'size')
    expect(names().slice(0, 3)).toEqual(['logs', 'plugins', 'world'])
    expect(names()[3]).toMatch(/^current/) // the link (size 0) is the smallest non-folder
    await user.click(screen.getByRole('button', { name: 'Aufsteigend sortiert – umkehren' }))
    expect(names().slice(0, 4)).toEqual(['world', 'plugins', 'logs', 'world-backup.zip'])
    expect(screen.getByRole('button', { name: 'Absteigend sortiert – umkehren' })).toBeInTheDocument()
  })
})
```

In `web/src/features/servers/server-detail-page.test.tsx` die Tab-Erwartung (Test „shows status, address, resources and power buttons on the overview“) auf

```ts
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Übersicht', 'Konsole', 'Spieler', 'Dateien', 'Einstellungen', 'Löschen'])
```

und im Test „lets the tab bar scroll sideways …“ `toHaveLength(5)` auf `toHaveLength(6)` ändern; dazu einen Test anhängen:

```tsx
  it('shows the files tab only with the files permission', async () => {
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    await screen.findByRole('heading', { name: 'survival', level: 1 })
    expect(screen.queryByRole('tab', { name: 'Dateien' })).not.toBeInTheDocument()
  })
```

Run: `make check` → Expected: FAIL (`./files-tab`, `FilesTab` fehlen).

- [ ] **Step 6: Namens-Dialog, Liste und Tab implementieren**

`web/src/features/files/name-dialog.tsx`:

```tsx
import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'

export interface NameDialogProps {
  title: string
  label: string
  initial: string
  hint?: string
  submitLabel: string
  /** Returns an i18n key or null. */
  validate(value: string): string | null
  onClose(): void
  /** Throws on failure; the error is shown inside the dialog. */
  onSubmit(value: string): Promise<void>
}

/** One field, local validation first, server errors (e.g. file_exists) inline instead of a toast. */
export function NameDialog({ title, label, initial, hint, submitLabel, validate, onClose, onSubmit }: NameDialogProps) {
  const { t } = useTranslation()
  const id = useId()
  const messageId = `${id}-message`
  const hintId = `${id}-hint`
  const [value, setValue] = useState(initial)
  const [problem, setProblem] = useState<string | null>(null)
  const [failure, setFailure] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (pending) return
    const found = validate(value)
    setProblem(found)
    setFailure(null)
    if (found) return
    setPending(true)
    try {
      await onSubmit(value)
    } catch (err) {
      setFailure(errorMessage(err, t))
    } finally {
      setPending(false)
    }
  }

  const message = problem ? t(problem) : failure
  const describedBy = [hint ? hintId : null, message ? messageId : null].filter(Boolean).join(' ') || undefined

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={(event) => void submit(event)} className="space-y-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            {hint ? <DialogDescription id={hintId}>{hint}</DialogDescription> : null}
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor={id}>{label}</Label>
            <Input
              id={id}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              aria-invalid={message ? true : undefined}
              aria-describedby={describedBy}
            />
            {message ? (
              <p id={messageId} role="alert" className="text-destructive text-sm">
                {message}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={pending}>
              {submitLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

`web/src/features/files/file-list.tsx`:

```tsx
import { File, FileText, Folder, Link2, MoreHorizontal } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { DataList, type Column } from '@/components/data-list'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useFormat } from '@/lib/format'
import { downloadUrl } from './api'
import { EDITOR_MAX_BYTES, formatBytes, isZip } from './format'
import { joinPath } from './path'
import type { FileEntry } from '@/api/types'

export interface FileListProps {
  serverId: number
  dir: string
  entries: FileEntry[]
  onOpenDir(name: string): void
  onRename(entry: FileEntry): void
  onDelete(entry: FileEntry): void
  /** Task 15: opens text files in the editor. Without it, files are not clickable. */
  onOpenFile?(entry: FileEntry): void
  /** Task 16: offers "Extract …" for zip files. */
  onExtract?(entry: FileEntry): void
}

function editable(entry: FileEntry): boolean {
  return entry.type === 'file' && entry.size <= EDITOR_MAX_BYTES
}

export function FileList({ serverId, dir, entries, onOpenDir, onRename, onDelete, onOpenFile, onExtract }: FileListProps) {
  const { t } = useTranslation()
  const fmt = useFormat()

  function nameCell(entry: FileEntry) {
    if (entry.type === 'dir') {
      return (
        <Button type="button" variant="link" className="h-auto max-w-full justify-start p-0" onClick={() => onOpenDir(entry.name)}>
          <Folder aria-hidden="true" />
          <span className="truncate">{entry.name}</span>
        </Button>
      )
    }
    if (onOpenFile && editable(entry)) {
      return (
        <Button type="button" variant="link" className="h-auto max-w-full justify-start p-0" onClick={() => onOpenFile(entry)}>
          <FileText aria-hidden="true" />
          <span className="truncate">{entry.name}</span>
        </Button>
      )
    }
    const Icon = entry.type === 'symlink' ? Link2 : File
    return (
      <span className="flex min-w-0 items-center gap-1.5">
        <Icon aria-hidden="true" className="size-4 shrink-0" />
        <span className="truncate">{entry.name}</span>
        {entry.type === 'symlink' || entry.type === 'other' ? <Badge variant="outline">{t(`files.type.${entry.type}`)}</Badge> : null}
      </span>
    )
  }

  function menu(entry: FileEntry) {
    const path = joinPath(dir, entry.name)
    const downloadable = entry.type === 'file' || entry.type === 'dir'
    return (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button type="button" variant="ghost" size="icon-sm" aria-label={t('files.actions_label', { name: entry.name })}>
            <MoreHorizontal aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {entry.type === 'dir' ? <DropdownMenuItem onSelect={() => onOpenDir(entry.name)}>{t('files.open')}</DropdownMenuItem> : null}
          {onOpenFile && editable(entry) ? <DropdownMenuItem onSelect={() => onOpenFile(entry)}>{t('files.edit')}</DropdownMenuItem> : null}
          {downloadable ? (
            <DropdownMenuItem asChild>
              <a href={downloadUrl(serverId, path)} download>
                {entry.type === 'dir' ? t('files.download_zip') : t('files.download')}
              </a>
            </DropdownMenuItem>
          ) : null}
          {onExtract && entry.type === 'file' && isZip(entry.name) ? (
            <DropdownMenuItem onSelect={() => onExtract(entry)}>{t('files.extract.action')}</DropdownMenuItem>
          ) : null}
          {entry.type !== 'other' ? <DropdownMenuItem onSelect={() => onRename(entry)}>{t('files.rename')}</DropdownMenuItem> : null}
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" onSelect={() => onDelete(entry)}>
            {t('files.delete')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    )
  }

  const columns: Column<FileEntry>[] = [
    { id: 'name', header: t('files.col.name'), cell: nameCell, className: 'max-w-0 w-full' },
    {
      id: 'size',
      header: t('files.col.size'),
      className: 'whitespace-nowrap text-right tabular-nums',
      cell: (e) => (e.type === 'file' ? formatBytes(e.size, fmt.locale) : '—'),
    },
    { id: 'mtime', header: t('files.col.modified'), className: 'whitespace-nowrap', cell: (e) => fmt.dateTime(e.mtime) },
    { id: 'actions', header: t('files.col.actions'), className: 'w-0 text-right', cell: menu },
  ]

  return (
    <DataList
      items={entries}
      columns={columns}
      rowKey={(e) => e.name}
      caption={t('files.caption', { dir: dir || t('files.root') })}
      empty={t('files.empty')}
    />
  )
}
```

`web/src/features/files/files-tab.tsx`:

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowDownWideNarrow, ArrowUpNarrowWide, FilePlus, FolderPlus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { FileEntry, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { deleteFile, fileListQueryOptions, fileListsKey, makeDirectory, renameFile, writeFile } from './api'
import { FileList } from './file-list'
import { sortEntries, type SortKey } from './format'
import { NameDialog } from './name-dialog'
import { breadcrumbs, componentProblem, joinPath, targetProblem } from './path'

type DialogState = { kind: 'mkdir' } | { kind: 'newfile' } | { kind: 'rename'; entry: FileEntry } | { kind: 'delete'; entry: FileEntry }

const SORT_KEYS: SortKey[] = ['name', 'size', 'mtime']

export interface FilesTabProps {
  server: Server
  /** Admins: link from the server.properties hint to the settings tab (Task 15). */
  onOpenSettings?: () => void
  /** Reports unsaved editor changes to the detail page (Task 15). */
  onDirtyChange?: (dirty: boolean) => void
}

export function FilesTab({ server }: FilesTabProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [dir, setDir] = useState('')
  const [sortKey, setSortKey] = useState<SortKey>('name')
  const [descending, setDescending] = useState(false)
  const [dialog, setDialog] = useState<DialogState | null>(null)
  const list = useQuery(fileListQueryOptions(server.id, dir))
  const running = server.status.state === 'running'
  const dirLabel = dir || t('files.root')

  const refresh = () => queryClient.invalidateQueries({ queryKey: fileListsKey(server.id) })

  const remove = useMutation({
    mutationFn: (entry: FileEntry) => deleteFile(server.id, joinPath(dir, entry.name)),
    onSuccess: (_data, entry) => {
      toast.success(t('files.done.delete', { name: entry.name }))
      setDialog(null)
      void refresh()
    },
    onError: notifyError,
  })

  async function createFolder(name: string) {
    await makeDirectory(server.id, joinPath(dir, name))
    toast.success(t('files.done.mkdir', { name }))
    setDialog(null)
    await refresh()
  }

  async function createFile(name: string) {
    await writeFile(server.id, { path: joinPath(dir, name), content: '', expected_mtime: null })
    toast.success(t('files.done.newfile', { name }))
    setDialog(null)
    await refresh()
  }

  async function rename(entry: FileEntry, to: string) {
    const from = joinPath(dir, entry.name)
    await renameFile(server.id, from, to)
    toast.success(t('files.done.rename', { from, to }))
    setDialog(null)
    await refresh()
  }

  function body() {
    if (list.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
    if (list.isError) {
      if (isApiError(list.error, 'not_found') && dir === '') return <p className="text-muted-foreground text-sm">{t('files.never_started')}</p>
      if (isApiError(list.error, 'not_found')) {
        return (
          <div className="space-y-2">
            <p className="text-muted-foreground text-sm">{t('files.dir_gone')}</p>
            <Button type="button" variant="outline" size="sm" onClick={() => setDir('')}>
              {t('files.back_to_root')}
            </Button>
          </div>
        )
      }
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(list.error, t)}
        </p>
      )
    }
    return (
      <div className="space-y-2">
        <FileList
          serverId={server.id}
          dir={dir}
          entries={sortEntries(list.data.entries, sortKey, descending)}
          onOpenDir={(name) => setDir(joinPath(dir, name))}
          onRename={(entry) => setDialog({ kind: 'rename', entry })}
          onDelete={(entry) => setDialog({ kind: 'delete', entry })}
        />
        {list.data.truncated ? (
          <p className="text-muted-foreground text-sm">{t('files.truncated', { count: list.data.entries.length })}</p>
        ) : null}
      </div>
    )
  }

  const toolbar = (
    <>
      <Button type="button" variant="outline" size="sm" onClick={() => setDialog({ kind: 'mkdir' })}>
        <FolderPlus aria-hidden="true" />
        {t('files.toolbar.new_folder')}
      </Button>
      <Button type="button" variant="outline" size="sm" onClick={() => setDialog({ kind: 'newfile' })}>
        <FilePlus aria-hidden="true" />
        {t('files.toolbar.new_file')}
      </Button>
    </>
  )

  return (
    <div className="space-y-4">
      {running ? (
        <p role="status" className="rounded-md border p-3 text-sm">
          {t('files.running_hint')}
        </p>
      ) : null}

      <SectionCard title={t('files.title')} actions={toolbar}>
        <div className="space-y-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <nav aria-label={t('files.breadcrumbs')} className="min-w-0">
              <ol className="flex flex-wrap items-center gap-1 text-sm">
                <li>
                  {dir === '' ? (
                    <span aria-current="page" className="font-medium">
                      {t('files.root')}
                    </span>
                  ) : (
                    <Button type="button" variant="link" className="h-auto p-0" onClick={() => setDir('')}>
                      {t('files.root')}
                    </Button>
                  )}
                </li>
                {breadcrumbs(dir).map((crumb, i, all) => (
                  <li key={crumb.path} className="flex items-center gap-1">
                    <span aria-hidden="true" className="text-muted-foreground">
                      /
                    </span>
                    {i === all.length - 1 ? (
                      <span aria-current="page" className="font-medium break-all">
                        {crumb.name}
                      </span>
                    ) : (
                      <Button type="button" variant="link" className="h-auto p-0" onClick={() => setDir(crumb.path)}>
                        {crumb.name}
                      </Button>
                    )}
                  </li>
                ))}
              </ol>
            </nav>
            <div className="flex items-center gap-2">
              <label className="sr-only" htmlFor={`files-sort-${server.id}`}>
                {t('files.sort.label')}
              </label>
              <NativeSelect id={`files-sort-${server.id}`} value={sortKey} onChange={(e) => setSortKey(e.target.value as SortKey)}>
                {SORT_KEYS.map((key) => (
                  <option key={key} value={key}>
                    {t(`files.sort.${key}`)}
                  </option>
                ))}
              </NativeSelect>
              <Button
                type="button"
                variant="outline"
                size="icon-sm"
                aria-label={descending ? t('files.sort.descending') : t('files.sort.ascending')}
                onClick={() => setDescending((d) => !d)}
              >
                {descending ? <ArrowDownWideNarrow aria-hidden="true" /> : <ArrowUpNarrowWide aria-hidden="true" />}
              </Button>
            </div>
          </div>
          {body()}
        </div>
      </SectionCard>

      {dialog?.kind === 'mkdir' ? (
        <NameDialog
          title={t('files.dialog.new_folder_title', { dir: dirLabel })}
          label={t('files.dialog.name')}
          initial=""
          submitLabel={t('files.dialog.create')}
          validate={(name) => {
            const p = componentProblem(name, dir)
            return p ? `files.validation.${p}` : null
          }}
          onClose={() => setDialog(null)}
          onSubmit={createFolder}
        />
      ) : null}
      {dialog?.kind === 'newfile' ? (
        <NameDialog
          title={t('files.dialog.new_file_title', { dir: dirLabel })}
          label={t('files.dialog.name')}
          initial=""
          submitLabel={t('files.dialog.create')}
          validate={(name) => {
            const p = componentProblem(name, dir)
            return p ? `files.validation.${p}` : null
          }}
          onClose={() => setDialog(null)}
          onSubmit={createFile}
        />
      ) : null}
      {dialog?.kind === 'rename' ? (
        <NameDialog
          title={t('files.dialog.rename_title', { name: dialog.entry.name })}
          label={t('files.dialog.target')}
          hint={t('files.dialog.target_hint')}
          initial={joinPath(dir, dialog.entry.name)}
          submitLabel={t('files.dialog.rename_submit')}
          validate={(to) => {
            const p = targetProblem(to)
            return p ? `files.validation.${p}` : null
          }}
          onClose={() => setDialog(null)}
          onSubmit={(to) => rename(dialog.entry, to)}
        />
      ) : null}
      {dialog?.kind === 'delete' ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => (open ? undefined : setDialog(null))}
          title={t('files.dialog.delete_title', { name: dialog.entry.name })}
          description={dialog.entry.type === 'dir' ? t('files.dialog.delete_dir') : t('files.dialog.delete_file')}
          confirmLabel={t('files.dialog.delete_submit')}
          destructive
          pending={remove.isPending}
          onConfirm={() => remove.mutate(dialog.entry)}
        />
      ) : null}
    </div>
  )
}
```

- [ ] **Step 7: Tab in der Detailseite**

In `web/src/features/servers/server-detail-page.tsx`:

```tsx
import { FilesTab } from '@/features/files/files-tab'
```

```tsx
type DetailTab = 'overview' | 'console' | 'players' | 'files' | 'settings' | 'delete'
```

nach `const canConsole = …`:

```tsx
  const canFiles = server.permissions.includes('server.files')
```

`available` erweitern (nach der Konsole-Zeile):

```tsx
  if (canFiles) available.push('files')
```

in der `TabsList` nach dem Spieler-Trigger:

```tsx
          {canFiles ? <TabsTrigger value="files">{t('servers.detail.tabs.files')}</TabsTrigger> : null}
```

und nach dem Spieler-`TabsContent`:

```tsx
        {canFiles ? (
          <TabsContent value="files" className="pt-4">
            <FilesTab server={server} onOpenSettings={isAdmin ? () => setTab('settings') : undefined} />
          </TabsContent>
        ) : null}
```

- [ ] **Step 8: Prüfen und committen**

Run: `make check` → Expected: PASS. Push, CI: `npx vitest run src/features/files src/features/servers/server-detail-page.test.tsx src/i18n` → Expected: PASS.

```bash
git add web/src/features/files web/src/features/servers/server-detail-page.tsx web/src/features/servers/server-detail-page.test.tsx web/src/i18n
git commit -m "feat(web): files tab with listing, breadcrumbs, sorting and file management

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 14: Upload-Warteschlange

**Files:**
- Create: `web/src/features/files/upload.ts`, `upload.test.ts`, `upload-queue.ts`, `upload-queue.test.ts`, `upload-list.tsx`
- Modify: `web/src/features/files/files-tab.tsx`, `web/src/features/files/files-tab.test.tsx`

**Interfaces:**
- Consumes: `getCsrfToken`/`setCsrfToken` (`@/api/csrf`), `apiHandlers` (`@/api/handlers`), `api` (`@/api/client`, für `/auth/me` bei `csrf_invalid`), `toApiError`/`isApiError`, `componentProblem`, `joinPath`, `MAX_UPLOAD_BYTES`, `fileListsKey`.
- Produces:
  - `upload.ts`: `interface UploadOptions { overwrite: boolean; signal?: AbortSignal; onProgress?(loaded: number, total: number): void }`, `uploadFile(serverId, path, file: Blob, options): Promise<UploadResult>`, `type UploadTransport = typeof uploadFile`.
  - `upload-queue.ts`: `type UploadStatus`, `interface UploadItem`, `uploadReducer(items, action)`, `nextToStart(items)`, `useUploadQueue(serverId, { onUploaded, transport? })` → `{ items, add(files, dir), cancel(id), overwrite(id), skip(id), clear() }`.
  - `upload-list.tsx`: `UploadList({ items, onCancel, onClear })`.

- [ ] **Step 1: Tests schreiben (rot)**

`web/src/features/files/upload-queue.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { nextToStart, uploadReducer, type UploadItem } from './upload-queue'

const item = (id: number, status: UploadItem['status'] = 'queued'): UploadItem => ({
  id,
  name: `f${id}`,
  path: `f${id}`,
  size: 100,
  loaded: 0,
  status,
  overwrite: false,
  error: null,
})

describe('uploadReducer', () => {
  it('adds, starts, tracks progress and finishes', () => {
    let items = uploadReducer([], { type: 'add', items: [item(1), item(2)] })
    items = uploadReducer(items, { type: 'start', id: 1 })
    items = uploadReducer(items, { type: 'progress', id: 1, loaded: 40 })
    expect(items[0]).toMatchObject({ status: 'uploading', loaded: 40 })
    items = uploadReducer(items, { type: 'done', id: 1 })
    expect(items[0]).toMatchObject({ status: 'done', loaded: 100 })
  })

  it('pauses on a conflict until the user decides', () => {
    let items = uploadReducer([item(1, 'uploading'), item(2)], { type: 'conflict', id: 1 })
    expect(nextToStart(items)).toBeUndefined()
    items = uploadReducer(items, { type: 'overwrite', id: 1 })
    expect(items[0]).toMatchObject({ status: 'queued', overwrite: true, loaded: 0 })
    expect(nextToStart(items)?.id).toBe(1)
    items = uploadReducer([item(1, 'conflict'), item(2)], { type: 'skip', id: 1 })
    expect(items[0].status).toBe('skipped')
    expect(nextToStart(items)?.id).toBe(2)
  })

  it('runs one upload at a time', () => {
    expect(nextToStart([item(1, 'uploading'), item(2)])).toBeUndefined()
    expect(nextToStart([item(1, 'done'), item(2)])?.id).toBe(2)
    expect(nextToStart([item(1, 'failed'), item(2, 'cancelled')])).toBeUndefined()
  })

  it('cancels only unfinished items and clears finished ones', () => {
    let items = uploadReducer([item(1, 'done'), item(2, 'queued'), item(3, 'uploading')], { type: 'cancel', id: 2 })
    items = uploadReducer(items, { type: 'cancel', id: 1 })
    expect(items.map((i) => i.status)).toEqual(['done', 'cancelled', 'uploading'])
    items = uploadReducer(items, { type: 'clear' })
    expect(items.map((i) => i.id)).toEqual([3])
  })

  it('records failures', () => {
    const items = uploadReducer([item(1, 'uploading')], { type: 'failed', id: 1, error: 'boom' })
    expect(items[0]).toMatchObject({ status: 'failed', error: 'boom' })
  })
})
```

`web/src/features/files/upload.test.ts`:

```ts
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it } from 'vitest'
import { setCsrfToken } from '@/api/csrf'
import { ApiError } from '@/api/errors'
import { server } from '@/mocks/node'
import { uploadFile } from './upload'

afterEach(() => setCsrfToken(''))

describe('uploadFile', () => {
  it('posts the raw file with token, path and overwrite flag', async () => {
    setCsrfToken('tok-1')
    let seen: { url: string; token: string | null; type: string | null; body: string } | null = null
    server.use(
      http.post('*/api/servers/:id/files/upload', async ({ request }) => {
        seen = {
          url: request.url,
          token: request.headers.get('X-CSRF-Token'),
          type: request.headers.get('Content-Type'),
          body: await request.text(),
        }
        return HttpResponse.json({ size: 5 }, { status: 201 })
      }),
    )
    const result = await uploadFile(1, 'plugins/a b.txt', new File(['hello'], 'a b.txt'), { overwrite: true })
    expect(result).toEqual({ size: 5 })
    expect(seen).not.toBeNull()
    const url = new URL(seen!.url)
    expect(url.pathname).toBe('/api/servers/1/files/upload')
    expect(url.searchParams.get('path')).toBe('plugins/a b.txt')
    expect(url.searchParams.get('overwrite')).toBe('true')
    expect(seen!.token).toBe('tok-1')
    expect(seen!.type).toBe('application/octet-stream')
    expect(seen!.body).toBe('hello')
  })

  it('turns error answers into ApiErrors', async () => {
    server.use(
      http.post('*/api/servers/:id/files/upload', () =>
        HttpResponse.json({ error: 'file_exists', message: 'exists' }, { status: 409 }),
      ),
    )
    const err = await uploadFile(1, 'a', new File(['x'], 'a'), { overwrite: false }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).code).toBe('file_exists')
  })

  it('rejects with AbortError when cancelled before sending', async () => {
    const controller = new AbortController()
    controller.abort()
    const err = await uploadFile(1, 'a', new File(['x'], 'a'), { overwrite: false, signal: controller.signal }).catch((e: unknown) => e)
    expect((err as DOMException).name).toBe('AbortError')
  })

  it('fetches a fresh token once after csrf_invalid', async () => {
    let calls = 0
    server.use(
      http.post('*/api/servers/:id/files/upload', ({ request }) => {
        calls += 1
        return request.headers.get('X-CSRF-Token') === 'fresh'
          ? HttpResponse.json({ size: 1 }, { status: 201 })
          : HttpResponse.json({ error: 'csrf_invalid', message: 'csrf' }, { status: 403 })
      }),
      http.get('*/api/auth/me', () => HttpResponse.json({ csrf_token: 'fresh', stage: 'full' })),
    )
    await expect(uploadFile(1, 'a', new File(['x'], 'a'), { overwrite: false })).resolves.toEqual({ size: 1 })
    expect(calls).toBe(2)
  })
})
```

(Der `/auth/me`-Mock oben liefert absichtlich nur die zwei Felder, die `captureMe` prüft; die App-weite `onMe`-Verarbeitung ist in Unit-Tests über `resetApiHandlers` ein No-op.)

In `web/src/features/files/files-tab.test.tsx` ergänzen:

```tsx
  it('uploads files into the open folder', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'plugins' }))
    await within(region).findByRole('button', { name: 'Essentials' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File(['jar'], 'WorldEdit.jar'))
    const uploads = await screen.findByRole('region', { name: 'Uploads' })
    expect(await within(uploads).findByText('Hochgeladen')).toBeInTheDocument()
    expect(await within(region).findByText('WorldEdit.jar')).toBeInTheDocument()
  })

  it('asks before overwriting an existing file and can skip it', async () => {
    const { user, region } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File(['x'], 'server.properties'))
    const dialog = await screen.findByRole('dialog', { name: '„server.properties“ existiert bereits' })
    await user.click(within(dialog).getByRole('button', { name: 'Überspringen' }))
    const uploads = screen.getByRole('region', { name: 'Uploads' })
    expect(await within(uploads).findByText('Übersprungen')).toBeInTheDocument()
  })

  it('rejects reserved names without uploading', async () => {
    const { user, region } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File(['x'], '.rcon-cli.env'))
    const uploads = await screen.findByRole('region', { name: 'Uploads' })
    expect(within(uploads).getByText('Dieser Name ist für das panel reserviert.')).toBeInTheDocument()
  })
```

Run: `make check` → Expected: FAIL (Module fehlen).

- [ ] **Step 2: `uploadFile` implementieren**

`web/src/features/files/upload.ts`:

```ts
import { api } from '@/api/client'
import { getCsrfToken, setCsrfToken } from '@/api/csrf'
import { toApiError } from '@/api/errors'
import { apiHandlers } from '@/api/handlers'
import type { UploadResult } from '@/api/types'

export interface UploadOptions {
  overwrite: boolean
  signal?: AbortSignal
  onProgress?(loaded: number, total: number): void
}

function parse(text: string): unknown {
  try {
    return text ? (JSON.parse(text) as unknown) : null
  } catch {
    return text
  }
}

/**
 * XMLHttpRequest instead of fetch: only XHR reports upload progress. The File is handed to send() as is, so the
 * browser streams it from disk (no copy in memory, spec §5.1). Mirrors the fetch middleware: X-CSRF-Token,
 * 401 → login, one retry with a fresh token after csrf_invalid.
 */
export function uploadFile(serverId: number, path: string, file: Blob, options: UploadOptions): Promise<UploadResult> {
  return send(serverId, path, file, options, true)
}

export type UploadTransport = typeof uploadFile

function send(serverId: number, path: string, file: Blob, options: UploadOptions, mayRetry: boolean): Promise<UploadResult> {
  return new Promise<UploadResult>((resolve, reject) => {
    if (options.signal?.aborted) {
      reject(new DOMException('Upload cancelled', 'AbortError'))
      return
    }
    const query = new URLSearchParams({ path, overwrite: options.overwrite ? 'true' : 'false' })
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `${window.location.origin}/api/servers/${serverId}/files/upload?${query.toString()}`)
    xhr.setRequestHeader('Content-Type', 'application/octet-stream')
    xhr.setRequestHeader('Accept', 'application/json')
    const token = getCsrfToken()
    if (token) xhr.setRequestHeader('X-CSRF-Token', token)

    const onAbort = () => xhr.abort()
    options.signal?.addEventListener('abort', onAbort, { once: true })
    const done = () => options.signal?.removeEventListener('abort', onAbort)

    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) options.onProgress?.(event.loaded, event.total)
    }
    xhr.onabort = () => {
      done()
      reject(new DOMException('Upload cancelled', 'AbortError'))
    }
    xhr.onerror = () => {
      done()
      reject(new TypeError('Network error'))
    }
    xhr.onload = () => {
      done()
      const body = parse(xhr.responseText)
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(body as UploadResult)
        return
      }
      const headers = new Headers()
      const retryAfter = xhr.getResponseHeader('Retry-After')
      if (retryAfter) headers.set('Retry-After', retryAfter)
      const error = toApiError(new Response(null, { status: xhr.status, headers }), body)
      if (xhr.status === 401 && error.code === 'unauthorized') {
        setCsrfToken('')
        apiHandlers().onUnauthorized()
      }
      if (mayRetry && xhr.status === 403 && error.code === 'csrf_invalid') {
        // /auth/me is in ME_PATHS: the API middleware stores the fresh token before the retry reads it.
        api
          .GET('/auth/me')
          .then(() => send(serverId, path, file, options, false))
          .then(resolve, reject)
        return
      }
      reject(error)
    }
    xhr.send(file)
  })
}
```

- [ ] **Step 3: Warteschlange implementieren**

`web/src/features/files/upload-queue.ts`:

```ts
import { useCallback, useEffect, useReducer, useRef } from 'react'
import { isApiError } from '@/api/errors'
import { MAX_UPLOAD_BYTES } from './format'
import { componentProblem, joinPath } from './path'
import { uploadFile, type UploadTransport } from './upload'

export type UploadStatus = 'queued' | 'uploading' | 'conflict' | 'done' | 'failed' | 'cancelled' | 'skipped'

export interface UploadItem {
  id: number
  name: string
  path: string
  size: number
  loaded: number
  status: UploadStatus
  overwrite: boolean
  /** i18n key (local check) or the error thrown by the transport. */
  error: unknown
}

export type UploadAction =
  | { type: 'add'; items: UploadItem[] }
  | { type: 'start'; id: number }
  | { type: 'progress'; id: number; loaded: number }
  | { type: 'done'; id: number }
  | { type: 'failed'; id: number; error: unknown }
  | { type: 'conflict'; id: number }
  | { type: 'overwrite'; id: number }
  | { type: 'skip'; id: number }
  | { type: 'cancel'; id: number }
  | { type: 'clear' }

const FINISHED: ReadonlySet<UploadStatus> = new Set<UploadStatus>(['done', 'failed', 'cancelled', 'skipped'])

function update(items: UploadItem[], id: number, change: (item: UploadItem) => UploadItem): UploadItem[] {
  return items.map((item) => (item.id === id ? change(item) : item))
}

export function uploadReducer(items: UploadItem[], action: UploadAction): UploadItem[] {
  switch (action.type) {
    case 'add':
      return [...items, ...action.items]
    case 'start':
      return update(items, action.id, (i) => ({ ...i, status: 'uploading', loaded: 0, error: null }))
    case 'progress':
      return update(items, action.id, (i) => ({ ...i, loaded: action.loaded }))
    case 'done':
      return update(items, action.id, (i) => ({ ...i, status: 'done', loaded: i.size }))
    case 'failed':
      return update(items, action.id, (i) => ({ ...i, status: 'failed', error: action.error }))
    case 'conflict':
      return update(items, action.id, (i) => ({ ...i, status: 'conflict' }))
    case 'overwrite':
      return update(items, action.id, (i) => ({ ...i, status: 'queued', overwrite: true, loaded: 0 }))
    case 'skip':
      return update(items, action.id, (i) => ({ ...i, status: 'skipped' }))
    case 'cancel':
      return update(items, action.id, (i) => (FINISHED.has(i.status) ? i : { ...i, status: 'cancelled' }))
    case 'clear':
      return items.filter((i) => !FINISHED.has(i.status))
  }
}

/** One upload at a time (the panel allows two per user, spec §5.1); a pending conflict pauses the queue. */
export function nextToStart(items: UploadItem[]): UploadItem | undefined {
  if (items.some((i) => i.status === 'uploading' || i.status === 'conflict')) return undefined
  return items.find((i) => i.status === 'queued')
}

export interface UploadQueueOptions {
  onUploaded(): void
  transport?: UploadTransport
}

export function useUploadQueue(serverId: number, { onUploaded, transport = uploadFile }: UploadQueueOptions) {
  const [items, dispatch] = useReducer(uploadReducer, [])
  const files = useRef(new Map<number, File>())
  const controllers = useRef(new Map<number, AbortController>())
  const seq = useRef(0)
  const latest = useRef({ onUploaded, transport })
  useEffect(() => {
    latest.current = { onUploaded, transport }
  })

  const next = nextToStart(items)
  const nextId = next?.id
  const nextPath = next?.path
  const nextOverwrite = next?.overwrite ?? false

  useEffect(() => {
    if (nextId === undefined || nextPath === undefined) return
    const file = files.current.get(nextId)
    if (!file) return
    const controller = new AbortController()
    controllers.current.set(nextId, controller)
    dispatch({ type: 'start', id: nextId })
    latest.current
      .transport(serverId, nextPath, file, {
        overwrite: nextOverwrite,
        signal: controller.signal,
        onProgress: (loaded) => dispatch({ type: 'progress', id: nextId, loaded }),
      })
      .then(() => {
        dispatch({ type: 'done', id: nextId })
        files.current.delete(nextId)
        latest.current.onUploaded()
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === 'AbortError') dispatch({ type: 'cancel', id: nextId })
        else if (isApiError(err, 'file_exists') && !nextOverwrite) dispatch({ type: 'conflict', id: nextId })
        else dispatch({ type: 'failed', id: nextId, error: err })
      })
      .finally(() => controllers.current.delete(nextId))
  }, [serverId, nextId, nextPath, nextOverwrite])

  // Leaving the tab cancels running uploads; the service discards the temp file (spec §4).
  useEffect(() => {
    const running = controllers.current
    return () => {
      for (const controller of running.values()) controller.abort()
    }
  }, [])

  const add = useCallback((list: File[], dir: string) => {
    const added: UploadItem[] = list.map((file) => {
      seq.current += 1
      const id = seq.current
      files.current.set(id, file)
      const problem = componentProblem(file.name, dir)
      const tooLarge = file.size > MAX_UPLOAD_BYTES
      return {
        id,
        name: file.name,
        path: joinPath(dir, file.name),
        size: file.size,
        loaded: 0,
        status: problem || tooLarge ? 'failed' : 'queued',
        overwrite: false,
        error: problem ? `files.validation.${problem}` : tooLarge ? 'files.validation.too_large' : null,
      }
    })
    dispatch({ type: 'add', items: added })
  }, [])

  const cancel = useCallback((id: number) => {
    controllers.current.get(id)?.abort()
    dispatch({ type: 'cancel', id })
  }, [])
  const overwrite = useCallback((id: number) => dispatch({ type: 'overwrite', id }), [])
  const skip = useCallback((id: number) => dispatch({ type: 'skip', id }), [])
  const clear = useCallback(() => dispatch({ type: 'clear' }), [])

  return { items, add, cancel, overwrite, skip, clear }
}
```

`web/src/features/files/upload-list.tsx`:

```tsx
import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { formatBytes } from './format'
import type { UploadItem } from './upload-queue'

export function UploadList({ items, onCancel, onClear }: { items: UploadItem[]; onCancel(id: number): void; onClear(): void }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  if (items.length === 0) return null
  const anyFinished = items.some((i) => ['done', 'failed', 'cancelled', 'skipped'].includes(i.status))

  function errorText(item: UploadItem): string | null {
    if (item.status !== 'failed') return null
    return typeof item.error === 'string' && item.error.startsWith('files.') ? t(item.error) : errorMessage(item.error, t)
  }

  return (
    <SectionCard
      title={t('files.uploads.title')}
      actions={
        anyFinished ? (
          <Button type="button" variant="outline" size="sm" onClick={onClear}>
            {t('files.uploads.clear')}
          </Button>
        ) : null
      }
    >
      <ul className="divide-y">
        {items.map((item) => {
          const error = errorText(item)
          const active = item.status === 'queued' || item.status === 'uploading'
          return (
            <li key={item.id} className="space-y-1 py-2">
              <div className="flex items-center justify-between gap-2">
                <span className="min-w-0 truncate font-medium" title={item.path}>
                  {item.name}
                </span>
                <span className="flex shrink-0 items-center gap-2 text-sm">
                  <span className="text-muted-foreground tabular-nums">{formatBytes(item.size, fmt.locale)}</span>
                  <span>{t(`files.uploads.status.${item.status}`)}</span>
                  {active ? (
                    <Button type="button" variant="ghost" size="icon-xs" aria-label={t('files.uploads.cancel', { name: item.name })} onClick={() => onCancel(item.id)}>
                      <X aria-hidden="true" />
                    </Button>
                  ) : null}
                </span>
              </div>
              {item.status === 'uploading' ? (
                <progress className="h-1.5 w-full" value={item.loaded} max={item.size || 1} aria-label={t('files.uploads.progress', { name: item.name })} />
              ) : null}
              {error ? (
                <p role="alert" className="text-destructive text-sm">
                  {error}
                </p>
              ) : null}
            </li>
          )
        })}
      </ul>
    </SectionCard>
  )
}
```

- [ ] **Step 4: In den Tab einbauen (Button, Drag & Drop, Konflikt-Dialog)**

In `web/src/features/files/files-tab.tsx`:

Importe ergänzen:

```tsx
import { Upload } from 'lucide-react'
import { useRef, type DragEvent } from 'react'
import { UploadList } from './upload-list'
import { useUploadQueue } from './upload-queue'
```

(`useRef` zum bestehenden `react`-Import hinzufügen, `Upload` zum `lucide-react`-Import.)

Im Komponentenrumpf nach `const refresh = …`:

```tsx
  const uploads = useUploadQueue(server.id, { onUploaded: () => void refresh() })
  const fileInput = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)
  const conflict = uploads.items.find((i) => i.status === 'conflict')

  function onDrop(event: DragEvent<HTMLDivElement>) {
    event.preventDefault()
    setDragging(false)
    const dropped = Array.from(event.dataTransfer.files)
    if (dropped.length > 0) uploads.add(dropped, dir)
  }
```

`toolbar` um den Upload-Button und das versteckte Eingabefeld erweitern (am Ende des Fragments):

```tsx
      <Button type="button" size="sm" onClick={() => fileInput.current?.click()}>
        <Upload aria-hidden="true" />
        {t('files.toolbar.upload')}
      </Button>
      <input
        ref={fileInput}
        type="file"
        multiple
        className="sr-only"
        tabIndex={-1}
        aria-label={t('files.toolbar.upload_input')}
        onChange={(e) => {
          uploads.add(Array.from(e.target.files ?? []), dir)
          e.target.value = '' // the same file can be chosen again
        }}
      />
```

Den Inhalt der `SectionCard` (das `<div className="space-y-3">`) in eine Ablagefläche einpacken:

```tsx
        <div
          className={dragging ? 'ring-primary relative space-y-3 rounded-md ring-2' : 'relative space-y-3'}
          onDragOver={(event) => {
            if (!event.dataTransfer.types.includes('Files')) return
            event.preventDefault()
            setDragging(true)
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={onDrop}
        >
          {dragging ? (
            <p className="bg-background/90 absolute inset-0 z-10 flex items-center justify-center rounded-md text-sm font-medium">
              {t('files.toolbar.drop', { dir: dirLabel })}
            </p>
          ) : null}
          {/* … bisheriger Inhalt: Brotkrumen/Sortierung und body() … */}
        </div>
```

Nach der `SectionCard` die Warteschlange, und bei den Dialogen den Konflikt-Dialog:

```tsx
      <UploadList items={uploads.items} onCancel={uploads.cancel} onClear={uploads.clear} />
```

```tsx
Für den Konflikt ein eigener kleiner Dialog mit zwei klar beschrifteten Knöpfen (`files.uploads.skip`, `files.uploads.overwrite`); Schließen per Escape/X überspringt ebenfalls:

```tsx
      {conflict ? (
        <Dialog open onOpenChange={(open) => (open ? undefined : uploads.skip(conflict.id))}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle>{t('files.uploads.conflict_title', { name: conflict.name })}</DialogTitle>
              <DialogDescription>{t('files.uploads.conflict_description')}</DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => uploads.skip(conflict.id)}>
                {t('files.uploads.skip')}
              </Button>
              <Button type="button" variant="destructive" onClick={() => uploads.overwrite(conflict.id)}>
                {t('files.uploads.overwrite')}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}
```

(Import `Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle` aus `@/components/ui/dialog`.)

- [ ] **Step 5: Prüfen und committen**

Run: `make check` → Expected: PASS. CI: `npx vitest run src/features/files` → Expected: PASS. Falls der MSW-XHR-Interceptor in jsdom den `File`-Body nicht als Text liefert (`seen.body` leer), im ersten `upload.test.ts`-Fall statt des Inhalts die Länge prüfen (`request.headers.get('Content-Length') === '5'`) – Inhalt und Fortschritt deckt Playwright (Aufgabe 17) im echten Browser ab.

```bash
git add web/src/features/files
git commit -m "feat(web): upload queue with progress, cancel, drag and drop and overwrite prompt

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 15: Editor (CodeMirror 6, lazy)

**Files:**
- Modify: `web/package.json`, `web/package-lock.json` (per `npm install`)
- Create: `web/src/features/files/code-editor.tsx`, `file-editor.tsx`, `file-editor.test.tsx`
- Modify: `web/src/features/files/files-tab.tsx`, `web/src/features/servers/server-detail-page.tsx`, `web/src/features/servers/server-detail-page.test.tsx`

**Interfaces:**
- Consumes: `readFile`, `writeFile`, `fileContentKey`, `fileListsKey`, `downloadUrl`, `editorLanguage`, `baseName`, `useBlocker` (`@tanstack/react-router`).
- Produces: `code-editor.tsx` (default export `CodeEditor({ value, language, label, onChange, onSave })`), `FileEditor({ server, path, onClose, onDirtyChange?, onOpenSettings? })`; `FilesTab` reicht `onDirtyChange`/`onOpenSettings` durch; die Detailseite fragt vor einem Tab-Wechsel mit ungespeicherten Änderungen nach.

- [ ] **Step 1: Abhängigkeiten installieren**

```bash
cd web && npm install codemirror @codemirror/state @codemirror/view @codemirror/language @codemirror/lang-yaml @codemirror/lang-json @codemirror/legacy-modes
```

Expected: `package.json` (dependencies) und `package-lock.json` geändert; `npm audit` im CI-Job bleibt grün (sonst Lead fragen).

- [ ] **Step 2: Tests schreiben (rot)**

`web/src/features/files/file-editor.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { server } from '@/mocks/node'
import { openFiles } from './files-tab.test'

// jsdom cannot lay out CodeMirror; the page logic is tested against a textarea with the same contract.
vi.mock('./code-editor', () => ({
  default: ({
    value,
    label,
    onChange,
    onSave,
  }: {
    value: string
    label: string
    onChange(v: string): void
    onSave(): void
  }) => (
    <textarea
      aria-label={label}
      defaultValue={value}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        if ((e.ctrlKey || e.metaKey) && e.key === 's') {
          e.preventDefault()
          onSave()
        }
      }}
    />
  ),
}))

async function openConfig() {
  const { user, region } = await openFiles()
  await user.click(await within(region).findByRole('button', { name: 'plugins' }))
  await user.click(await within(region).findByRole('button', { name: 'Essentials' }))
  await user.click(await within(region).findByRole('button', { name: 'config.yml' }))
  const editor = await screen.findByRole('textbox', { name: 'Inhalt von config.yml' })
  return { user, region, editor }
}

describe('FileEditor', () => {
  it('saves with Ctrl+S and clears the unsaved marker', async () => {
    const { user, editor } = await openConfig()
    await user.type(editor, '\n# geändert')
    expect(screen.getByText('Ungespeicherte Änderungen')).toBeInTheDocument()
    await user.keyboard('{Control>}s{/Control}')
    expect(await screen.findByText('Gespeichert.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('Ungespeicherte Änderungen')).not.toBeInTheDocument())
  })

  it('offers reload or overwrite after a conflicting save', async () => {
    server.use(
      http.put('*/api/servers/:id/files/content', () => HttpResponse.json({ error: 'file_modified', message: 'modified' }, { status: 409 })),
    )
    const { user, editor } = await openConfig()
    await user.type(editor, 'x')
    await user.click(screen.getByRole('button', { name: 'Speichern' }))
    const dialog = await screen.findByRole('dialog', { name: 'Datei wurde geändert' })
    await user.click(within(dialog).getByRole('button', { name: 'Neu laden' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.queryByText('Ungespeicherte Änderungen')).not.toBeInTheDocument()
  })

  it('asks before leaving with unsaved changes', async () => {
    const { user, region, editor } = await openConfig()
    await user.type(editor, 'x')
    await user.click(screen.getByRole('button', { name: 'Zurück zur Liste' }))
    const dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
    await user.click(within(dialog).getByRole('button', { name: 'Verwerfen' }))
    expect(await within(region).findByRole('button', { name: 'config.yml' })).toBeInTheDocument()
  })

  it('asks before switching tabs with unsaved changes', async () => {
    const { user, editor } = await openConfig()
    await user.type(editor, 'x')
    await user.click(screen.getByRole('tab', { name: 'Übersicht' }))
    const dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
    await user.click(within(dialog).getByRole('button', { name: 'Verwerfen' }))
    expect(await screen.findByRole('region', { name: 'Zustand' })).toBeInTheDocument()
  })

  it('offers a download for files the editor cannot open', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'plugins' }))
    await user.click(await within(region).findByRole('button', { name: 'EssentialsX.jar' }))
    expect(await screen.findByText('Diese Datei ist zu groß für den Editor oder keine Textdatei.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Stattdessen herunterladen' })).toHaveAttribute(
      'href',
      '/api/servers/1/files/download?path=plugins%2FEssentialsX.jar',
    )
  })

  it('explains server.properties and shows the redacted password', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'server.properties' }))
    const editor = await screen.findByRole('textbox', { name: 'Inhalt von server.properties' })
    expect((editor as HTMLTextAreaElement).value).toContain('rcon.password=********')
    expect(screen.getByText(/wird beim Start aus den panel-Einstellungen überschrieben/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Zu den Einstellungen' })).toBeInTheDocument()
  })
})
```

(`openFiles` wird aus `files-tab.test.tsx` importiert; dort ist es bereits exportiert. Vitest führt die importierte Datei nicht als zweite Suite aus.)

Run: `make check` → Expected: FAIL (`./code-editor`, `FileEditor` fehlen).

- [ ] **Step 3: CodeMirror-Komponente**

`web/src/features/files/code-editor.tsx`:

```tsx
import { json } from '@codemirror/lang-json'
import { yaml } from '@codemirror/lang-yaml'
import { StreamLanguage } from '@codemirror/language'
import { properties } from '@codemirror/legacy-modes/mode/properties'
import { toml } from '@codemirror/legacy-modes/mode/toml'
import { EditorState, Prec, type Extension } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { basicSetup } from 'codemirror'
import { useEffect, useRef } from 'react'
import type { EditorLanguage } from './format'

export interface CodeEditorProps {
  value: string
  language: EditorLanguage
  label: string
  onChange(value: string): void
  onSave(): void
}

function languageExtension(language: EditorLanguage): Extension {
  switch (language) {
    case 'yaml':
      return yaml()
    case 'json':
      return json()
    case 'toml':
      return StreamLanguage.define(toml)
    case 'properties':
      return StreamLanguage.define(properties)
    case 'text':
      return []
  }
}

/** Colours from the app's CSS variables, so light and dark mode follow the theme toggle. */
const theme = EditorView.theme({
  '&': { height: '60vh', backgroundColor: 'var(--background)', color: 'var(--foreground)' },
  '&.cm-focused': { outline: '2px solid var(--ring)' },
  '.cm-scroller': { fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace' },
  '.cm-gutters': { backgroundColor: 'var(--muted)', color: 'var(--muted-foreground)', border: 'none' },
  '.cm-activeLine, .cm-activeLineGutter': { backgroundColor: 'color-mix(in oklch, var(--muted), transparent 40%)' },
})

/**
 * Uncontrolled: the document is set once per mount. FileEditor remounts it (key) after "reload".
 * Default export for React.lazy – CodeMirror ends up in its own chunk.
 */
export default function CodeEditor({ value, language, label, onChange, onSave }: CodeEditorProps) {
  const host = useRef<HTMLDivElement>(null)
  const callbacks = useRef({ onChange, onSave })
  useEffect(() => {
    callbacks.current = { onChange, onSave }
  })

  useEffect(() => {
    if (!host.current) return
    const view = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: value,
        extensions: [
          basicSetup,
          languageExtension(language),
          theme,
          EditorView.contentAttributes.of({ 'aria-label': label }),
          Prec.highest(
            keymap.of([
              {
                key: 'Mod-s',
                preventDefault: true,
                run: () => {
                  callbacks.current.onSave()
                  return true
                },
              },
            ]),
          ),
          EditorView.updateListener.of((update) => {
            if (update.docChanged) callbacks.current.onChange(update.state.doc.toString())
          }),
        ],
      }),
    })
    return () => view.destroy()
    // value/language/label belong to the mount; changing them means remounting (key in FileEditor).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return <div ref={host} className="overflow-hidden rounded-md border text-sm" />
}
```

- [ ] **Step 4: `FileEditor`**

`web/src/features/files/file-editor.tsx`:

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useBlocker } from '@tanstack/react-router'
import { ArrowLeft } from 'lucide-react'
import { lazy, Suspense, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { FileContent, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { downloadUrl, fileContentKey, fileListsKey, readFile, writeFile } from './api'
import { editorLanguage } from './format'
import { baseName } from './path'

const CodeEditor = lazy(() => import('./code-editor'))

export interface FileEditorProps {
  server: Server
  path: string
  onClose(): void
  onDirtyChange?(dirty: boolean): void
  onOpenSettings?(): void
}

export function FileEditor({ server, path, onClose, onDirtyChange, onOpenSettings }: FileEditorProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const contentKey = fileContentKey(server.id, path)
  const name = baseName(path)
  const file = useQuery({
    queryKey: contentKey,
    queryFn: () => readFile(server.id, path),
    // The loaded mtime is the base for conflict detection: never refresh it behind the user's back.
    staleTime: Infinity,
    gcTime: 0,
    refetchOnWindowFocus: false,
    retry: false,
  })
  const [draft, setDraft] = useState<string | null>(null)
  const [editorKey, setEditorKey] = useState(0)
  const [conflict, setConflict] = useState(false)
  const [confirmClose, setConfirmClose] = useState(false)
  const dirty = draft !== null && file.data !== undefined && draft !== file.data.content

  useEffect(() => {
    onDirtyChange?.(dirty)
  }, [dirty, onDirtyChange])
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange])

  // Route changes and reloads; tab switches are asked by the detail page (onDirtyChange).
  const blocker = useBlocker({ shouldBlockFn: () => dirty, enableBeforeUnload: () => dirty, withResolver: true })

  const save = useMutation({
    mutationFn: ({ content, mtime }: { content: string; mtime: number }) => writeFile(server.id, { path, content, expected_mtime: mtime }),
    onSuccess: (result, { content }) => {
      queryClient.setQueryData<FileContent>(contentKey, { path, content, mtime: result.mtime, size: result.size })
      // Text typed while the request ran stays a draft.
      setDraft((current) => (current === content ? null : current))
      toast.success(t('files.editor.saved'))
      void queryClient.invalidateQueries({ queryKey: fileListsKey(server.id) })
    },
    onError: (err) => (isApiError(err, 'file_modified') ? setConflict(true) : notifyError(err)),
  })

  function doSave() {
    if (!file.data || draft === null || !dirty || save.isPending) return
    save.mutate({ content: draft, mtime: file.data.mtime })
  }

  async function overwrite() {
    setConflict(false)
    if (draft === null) return
    try {
      const current = await readFile(server.id, path)
      save.mutate({ content: draft, mtime: current.mtime })
    } catch (err) {
      notifyError(err)
    }
  }

  async function reload() {
    setConflict(false)
    setDraft(null)
    await queryClient.refetchQueries({ queryKey: contentKey })
    setEditorKey((k) => k + 1)
  }

  function body() {
    if (file.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
    if (file.isError) {
      if (isApiError(file.error, 'not_text')) {
        return (
          <div className="space-y-2">
            <p className="text-sm">{t('files.editor.not_text')}</p>
            <Button asChild variant="outline" size="sm">
              <a href={downloadUrl(server.id, path)} download>
                {t('files.editor.download_instead')}
              </a>
            </Button>
          </div>
        )
      }
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(file.error, t)}
        </p>
      )
    }
    return (
      <Suspense fallback={<p className="text-muted-foreground text-sm">{t('common.loading')}</p>}>
        <CodeEditor
          key={editorKey}
          value={file.data.content}
          language={editorLanguage(name)}
          label={t('files.editor.label', { name })}
          onChange={setDraft}
          onSave={doSave}
        />
      </Suspense>
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => (dirty ? setConfirmClose(true) : onClose())}>
          <ArrowLeft aria-hidden="true" />
          {t('files.editor.back')}
        </Button>
        <p className="min-w-0 flex-1 truncate font-mono text-sm" title={path}>
          {path}
        </p>
        {dirty ? <Badge variant="outline">{t('files.editor.unsaved')}</Badge> : null}
        <Button type="button" size="sm" disabled={!dirty || save.isPending} onClick={doSave}>
          {save.isPending ? t('files.editor.saving') : t('common.save')}
        </Button>
      </div>
      {path === 'server.properties' ? (
        <div role="note" className="flex flex-wrap items-center gap-2 rounded-md border p-3 text-sm">
          <span>{t('files.editor.properties_hint')}</span>
          {onOpenSettings ? (
            <Button type="button" variant="link" className="h-auto p-0" onClick={onOpenSettings}>
              {t('files.editor.open_settings')}
            </Button>
          ) : null}
        </div>
      ) : null}
      {body()}

      <Dialog open={conflict} onOpenChange={setConflict}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t('files.editor.conflict_title')}</DialogTitle>
            <DialogDescription>{t('files.editor.conflict_description')}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setConflict(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="button" variant="outline" onClick={() => void reload()}>
              {t('files.editor.reload')}
            </Button>
            <Button type="button" variant="destructive" onClick={() => void overwrite()}>
              {t('files.editor.overwrite')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={confirmClose}
        onOpenChange={setConfirmClose}
        title={t('files.editor.discard_title')}
        description={t('files.editor.discard_description', { name })}
        confirmLabel={t('files.editor.discard')}
        destructive
        onConfirm={() => {
          setConfirmClose(false)
          setDraft(null)
          onClose()
        }}
      />
      <ConfirmDialog
        open={blocker.status === 'blocked'}
        onOpenChange={(open) => (open ? undefined : blocker.reset?.())}
        title={t('files.editor.discard_title')}
        description={t('files.editor.discard_description', { name })}
        confirmLabel={t('files.editor.discard')}
        destructive
        onConfirm={() => blocker.proceed?.()}
      />
    </div>
  )
}
```

Hinweis `useBlocker`: In `@tanstack/react-router` ≥ 1.80 liefert `useBlocker({ …, withResolver: true })` `{ status: 'idle' | 'blocked', proceed, reset }`; `proceed`/`reset` sind nur bei `blocked` gesetzt (daher `?.()`). Meldet `tsc` eine abweichende Signatur der installierten Version, an die Typdefinition in `node_modules/@tanstack/react-router/dist/esm/useBlocker.d.ts` anpassen – Verhalten wie beschrieben.

- [ ] **Step 5: Editor im Tab, Tab-Wechsel auf der Detailseite**

In `web/src/features/files/files-tab.tsx`:

- Import `import { FileEditor } from './file-editor'`.
- Signatur: `export function FilesTab({ server, onOpenSettings, onDirtyChange }: FilesTabProps) {`
- Zustand: `const [editing, setEditing] = useState<string | null>(null)`
- In `FileList` die Prop `onOpenFile={(entry) => setEditing(joinPath(dir, entry.name))}` setzen.
- `createFile` öffnet die neue Datei direkt: nach `await refresh()` die Zeile `setEditing(joinPath(dir, name))`.
- Im JSX den Inhalt der `SectionCard` umschalten: steht `editing !== null`, rendert die Karte statt Brotkrumen/Liste

```tsx
          <FileEditor
            server={server}
            path={editing}
            onClose={() => setEditing(null)}
            onDirtyChange={onDirtyChange}
            onOpenSettings={onOpenSettings}
          />
```

  (die Werkzeugleiste `actions` bleibt nur in der Listenansicht: `actions={editing === null ? toolbar : undefined}`).

In `web/src/features/servers/server-detail-page.tsx`:

```tsx
import { ConfirmDialog } from '@/components/confirm-dialog'
```

nach `const [tab, setTab] = useState<DetailTab>('overview')`:

```tsx
  const [filesDirty, setFilesDirty] = useState(false)
  const [pendingTab, setPendingTab] = useState<DetailTab | null>(null)
```

`Tabs` wechselt nur noch über eine Funktion (im Rumpf nach `const active = …`):

```tsx
  function changeTab(next: DetailTab) {
    // Radix unmounts inactive tabs: leaving "files" with unsaved editor changes would silently lose them.
    if (active === 'files' && next !== 'files' && filesDirty) setPendingTab(next)
    else setTab(next)
  }
```

`<Tabs value={active} onValueChange={(value) => changeTab(value as DetailTab)}>`, `FilesTab` bekommt `onDirtyChange={setFilesDirty}`, und nach `</Tabs>`:

```tsx
      <ConfirmDialog
        open={pendingTab !== null}
        onOpenChange={(open) => (open ? undefined : setPendingTab(null))}
        title={t('files.editor.discard_title')}
        description={t('files.editor.discard_tab')}
        confirmLabel={t('files.editor.discard')}
        destructive
        onConfirm={() => {
          setFilesDirty(false)
          if (pendingTab) setTab(pendingTab)
          setPendingTab(null)
        }}
      />
```

`onDirtyChange={setFilesDirty}` ist stabil (State-Setter), daher läuft der Effekt im Editor nicht in Schleife.

- [ ] **Step 6: Prüfen und committen**

Run: `make check` → Expected: PASS. CI: `npx vitest run src/features/files src/features/servers` → Expected: PASS. Prüfe im CI-Build-Log (`vite build`), dass CodeMirror in einem eigenen Chunk (`code-editor-*.js`) landet und das Haupt-Bundle nicht wächst.

```bash
git add web/package.json web/package-lock.json web/src/features/files web/src/features/servers
git commit -m "feat(web): lazy CodeMirror editor with save, conflict dialog and unsaved-change guards

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 16: Entpacken, Power-Sperre, Rechte-Hinweis

**Files:**
- Create: `web/src/features/files/extract-dialog.tsx`, `web/src/features/files/job-status.tsx`
- Modify: `web/src/features/files/files-tab.tsx`, `web/src/features/files/files-tab.test.tsx`
- Modify: `web/src/features/servers/server-detail-page.tsx`, `server-overview.tsx`, `power-buttons.tsx`, `server-detail-page.test.tsx`
- Modify: `web/src/features/users/permission-matrix.ts`, `permission-matrix.test.ts`, `permissions-page.tsx`, `permissions-page.test.tsx`

**Interfaces:**
- Consumes: `extractArchive`, `fileJobKey`, `fileListsKey`, `useFileJob`, `isExtracting`, `stripZip`, `parentPath`, `baseName`, `targetProblem`.
- Produces: `ExtractDialog({ server, path, onClose })`, `JobStatus({ job })`, `PowerButtons({ server, extracting? })`, `ServerOverview({ server, instance, extracting? })`, `newlyGranted(draft, saved, permission): number[]`.

- [ ] **Step 1: Tests schreiben (rot)**

In `web/src/features/files/files-tab.test.tsx` (Imports `act` aus `@testing-library/react`, `db` aus `@/mocks/db` ergänzen, falls benötigt):

```tsx
  it('extracts a zip of a stopped server and reports progress', async () => {
    let polls = 0
    server.use(
      http.get('*/api/servers/:id/files', () =>
        HttpResponse.json({
          path: '',
          truncated: false,
          entries: [{ name: 'neue-welt.zip', type: 'file', size: 1024, mtime: Date.now() }],
        }),
      ),
      http.post('*/api/servers/:id/files/extract', async ({ request }) => {
        const body = (await request.json()) as { path: string; dest: string }
        expect(body).toEqual({ path: 'neue-welt.zip', dest: 'neue-welt' })
        return HttpResponse.json(job('running', 10), { status: 202 })
      }),
      http.get('*/api/servers/:id/files/jobs', () => {
        polls += 1
        return HttpResponse.json({ job: polls < 2 ? job('running', 50) : job('done', 100) })
      }),
    )
    const { user, region } = await openFiles(2)
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für neue-welt.zip' }))
    await user.click(screen.getByRole('menuitem', { name: 'Entpacken …' }))
    const dialog = await screen.findByRole('dialog', { name: '„neue-welt.zip“ entpacken' })
    expect(within(dialog).getByLabelText('Zielordner')).toHaveValue('neue-welt')
    await user.click(within(dialog).getByRole('button', { name: 'Entpacken' }))
    expect(await screen.findByRole('progressbar', { name: 'Fortschritt beim Entpacken' })).toBeInTheDocument()
    expect(await screen.findByText('„neue-welt.zip“ wurde nach neue-welt entpackt.', {}, { timeout: 6000 })).toBeInTheDocument()
  })

  it('does not offer extracting while the server runs', async () => {
    server.use(
      http.get('*/api/servers/:id/files', () =>
        HttpResponse.json({ path: '', truncated: false, entries: [{ name: 'w.zip', type: 'file', size: 1, mtime: 0 }] }),
      ),
    )
    const { user, region } = await openFiles(1)
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für w.zip' }))
    await user.click(screen.getByRole('menuitem', { name: 'Entpacken …' }))
    const dialog = await screen.findByRole('dialog', { name: '„w.zip“ entpacken' })
    expect(within(dialog).getByText('Entpacken geht nur bei gestopptem Server.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Entpacken' })).toBeDisabled()
  })
```

mit der Hilfsfunktion oben in der Datei:

```tsx
function job(state: 'running' | 'done' | 'failed', percent: number) {
  return {
    id: 'j1',
    state,
    path: 'neue-welt.zip',
    dest: 'neue-welt',
    bytes: percent * 10,
    entries: percent,
    total_bytes: 1000,
    total_entries: 100,
    error: '',
    started_at: Date.now() - 1000,
    finished_at: state === 'running' ? 0 : Date.now(),
  }
}
```

In `web/src/features/servers/server-detail-page.test.tsx`:

```tsx
  it('blocks starting while an extraction runs', async () => {
    server.use(
      http.get('*/api/servers/:id/files/jobs', () =>
        HttpResponse.json({
          job: { id: 'j', state: 'running', path: 'w.zip', dest: 'w', bytes: 1, entries: 1, total_bytes: 10, total_entries: 10, error: '', started_at: Date.now(), finished_at: 0 },
        }),
      ),
    )
    await renderWithProviders(<ServerDetailPage serverId={2} />, { me: loginAs('admin') })
    expect(await screen.findByText('Entpacken läuft – Starten ist erst danach möglich.')).toBeInTheDocument()
    const group = screen.getByRole('group', { name: 'Steuerung für creative' })
    expect(within(group).getByRole('button', { name: 'Starten' })).toBeDisabled()
  })
```

(Die Datei importiert bisher weder `msw` noch `@/mocks/node`: `import { http, HttpResponse } from 'msw'` und `import { server } from '@/mocks/node'` ergänzen. „Starten“ ist `servers.power.start`.)

`web/src/features/users/permission-matrix.test.ts` ergänzen:

```ts
describe('newlyGranted', () => {
  it('lists servers where a permission was added', () => {
    const saved = { 1: ['server.view' as const], 2: ['server.view' as const, 'server.files' as const] }
    const draft = { 1: ['server.view' as const, 'server.files' as const], 2: ['server.view' as const, 'server.files' as const], 3: ['server.view' as const] }
    expect(newlyGranted(draft, saved, 'server.files')).toEqual([1])
    expect(newlyGranted(saved, saved, 'server.files')).toEqual([])
  })
})
```

(Import `newlyGranted` ergänzen.) `web/src/features/users/permissions-page.test.tsx` ergänzen:

```tsx
  it('warns before granting file access', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UserPermissionsPage userId={2} />, { me: loginAs('admin', { sudo: true }) })
    await screen.findByRole('heading', { name: 'Rechte von alice' })
    expect(screen.queryByText(/erlaubt Code-Ausführung/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: 'survival: Dateien' }))
    expect(screen.getByText(/„Dateien“ erlaubt Code-Ausführung im Minecraft-Server/)).toBeInTheDocument()
  })
```

Run: `make check` → Expected: FAIL.

- [ ] **Step 2: Entpack-Dialog und Job-Anzeige**

`web/src/features/files/extract-dialog.tsx`:

```tsx
import { useQueryClient } from '@tanstack/react-query'
import { useId, useState, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import type { FileJobResponse, Server } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { extractArchive, fileJobKey } from './api'
import { baseName, joinPath, parentPath, stripZip, targetProblem } from './path'

/** Extracting only while the server is stopped (spec §6.3); the panel checks again (409 instance_running). */
export function ExtractDialog({ server, path, onClose }: { server: Server; path: string; onClose(): void }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const id = useId()
  const name = baseName(path)
  const [dest, setDest] = useState(joinPath(parentPath(path), stripZip(name)))
  const [message, setMessage] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const stopped = server.status.state === 'stopped'

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!stopped || pending) return
    const problem = targetProblem(dest)
    if (problem) {
      setMessage(t(`files.validation.${problem}`))
      return
    }
    setMessage(null)
    setPending(true)
    try {
      const job = await extractArchive(server.id, path, dest)
      queryClient.setQueryData<FileJobResponse>(fileJobKey(server.id), { job }) // starts the 2-s polling
      onClose()
    } catch (err) {
      setMessage(errorMessage(err, t))
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={(event) => void submit(event)} className="space-y-4">
          <DialogHeader>
            <DialogTitle>{t('files.extract.title', { name })}</DialogTitle>
            <DialogDescription>{t('files.extract.dest_hint')}</DialogDescription>
          </DialogHeader>
          {!stopped ? (
            <p role="status" className="rounded-md border p-3 text-sm">
              {t('files.extract.stopped_only')}
            </p>
          ) : null}
          <div className="space-y-2">
            <Label htmlFor={id}>{t('files.extract.dest')}</Label>
            <Input id={id} value={dest} onChange={(e) => setDest(e.target.value)} autoComplete="off" spellCheck={false} disabled={!stopped} />
            {message ? (
              <p role="alert" className="text-destructive text-sm">
                {message}
              </p>
            ) : null}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" disabled={!stopped || pending}>
              {t('files.extract.submit')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

`web/src/features/files/job-status.tsx`:

```tsx
import { useTranslation } from 'react-i18next'
import { useFormat } from '@/lib/format'
import { formatBytes } from './format'
import { baseName } from './path'
import type { FileJob } from '@/api/types'

export function JobStatus({ job }: { job: FileJob }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  return (
    <div role="status" className="space-y-2 rounded-md border p-3 text-sm">
      <p className="font-medium">{t('files.extract.running', { name: baseName(job.path) })}</p>
      <progress
        className="h-1.5 w-full"
        value={job.bytes}
        max={job.total_bytes || 1}
        aria-label={t('files.extract.progress')}
      />
      <p className="text-muted-foreground tabular-nums">
        {t('files.extract.counts', {
          entries: fmt.number(job.entries),
          total_entries: fmt.number(job.total_entries),
          bytes: formatBytes(job.bytes, fmt.locale),
          total_bytes: formatBytes(job.total_bytes, fmt.locale),
        })}
      </p>
    </div>
  )
}
```

- [ ] **Step 3: Im Tab einbauen**

In `web/src/features/files/files-tab.tsx`:

- Importe: `useCallback`, `useEffect` (react), `ExtractDialog`, `JobStatus`, `useFileJob`, `baseName` (aus `./path`).
- `DialogState` um `| { kind: 'extract'; path: string }` erweitern; `FileList` bekommt `onExtract={(entry) => setDialog({ kind: 'extract', path: joinPath(dir, entry.name) })}`.
- Job abfragen und Ende melden (nach `const uploads = …`):

```tsx
  const jobQuery = useFileJob(server.id, true)
  const job = jobQuery.data?.job ?? null
  const lastState = useRef(job?.state)
  useEffect(() => {
    const previous = lastState.current
    lastState.current = job?.state
    if (previous !== 'running' || !job || job.state === 'running') return
    if (job.state === 'done') toast.success(t('files.extract.done', { name: baseName(job.path), dest: job.dest }))
    else toast.error(t('files.extract.failed', { name: baseName(job.path), error: job.error }))
    void refresh()
  }, [job, t]) // refresh only invalidates queries; the eslint rule may ask to wrap it in useCallback – do so if it does
```

- Vor der `SectionCard`: `{job?.state === 'running' ? <JobStatus job={job} /> : null}`
- Bei den Dialogen: `{dialog?.kind === 'extract' ? <ExtractDialog server={server} path={dialog.path} onClose={() => setDialog(null)} /> : null}`

`refresh` als `useCallback` definieren, damit der Effekt eine stabile Abhängigkeit hat:

```tsx
  const refresh = useCallback(() => queryClient.invalidateQueries({ queryKey: fileListsKey(server.id) }), [queryClient, server.id])
```

und `refresh` in die Abhängigkeiten des Effekts aufnehmen (`[job, refresh, t]`).

- [ ] **Step 4: Power-Sperre**

`web/src/features/servers/power-buttons.tsx`:

```tsx
/** Start, stop (after confirmation) and restart; actions that make no sense in the current state are disabled. */
export function PowerButtons({ server, extracting = false }: { server: Pick<Server, 'id' | 'name' | 'status'>; extracting?: boolean }) {
```

im Button: `disabled={power.isPending || !powerAllowed(server.status.state, action) || (extracting && action !== 'stop')}`; nach dem `</div>` der Gruppe:

```tsx
      {extracting ? <p className="text-muted-foreground text-sm">{t('servers.extracting_lock')}</p> : null}
```

`web/src/features/servers/server-overview.tsx`: Signatur `export function ServerOverview({ server, instance, extracting = false }: { server: Server; instance: InstanceConfig; extracting?: boolean })`; `<PowerButtons server={server} extracting={extracting} />`; `RestartBanner` bekommt `extracting` und setzt am „Jetzt neu starten“-Knopf `disabled={power.isPending || extracting}`.

`web/src/features/servers/server-detail-page.tsx`: Import `import { isExtracting, useFileJob } from '@/features/files/use-file-job'`; Hooks **vor** den frühen `return`s (nach `const [tab, setTab] = …`):

```tsx
  // Users with server.files see the lock; everyone else gets the panel's 409 "extracting" on start.
  const filesAllowed = query.data?.permissions.includes('server.files') === true && query.data.instance !== null
  const fileJob = useFileJob(serverId, filesAllowed)
  const extracting = isExtracting(fileJob.data)
```

und `<ServerOverview server={server} instance={instance} extracting={extracting} />`.

- [ ] **Step 5: Rechte-Hinweis**

`web/src/features/users/permission-matrix.ts` ergänzen:

```ts
/** Server ids where permission is in draft but was not saved yet (for the files warning, spec §6.4). */
export function newlyGranted(draft: Matrix, saved: Matrix, permission: Permission): number[] {
  return Object.entries(draft)
    .filter(([id, perms]) => perms.includes(permission) && !(saved[Number(id)] ?? []).includes(permission))
    .map(([id]) => Number(id))
    .sort((a, b) => a - b)
}
```

`web/src/features/users/permissions-page.tsx`: Import `newlyGranted`; vor `return (`:

```tsx
  const filesWarning = !targetIsAdmin && saved !== null && newlyGranted(current, saved, 'server.files').length > 0
```

und nach der `dirty`-Badge:

```tsx
      {filesWarning ? (
        <p role="status" className="border-destructive/50 mb-4 rounded-md border p-3 text-sm">
          {t('permissions.files_warning')}
        </p>
      ) : null}
```

- [ ] **Step 6: Prüfen und committen**

Run: `make check` → Expected: PASS. CI: `npx vitest run src/features/files src/features/servers src/features/users` → Expected: PASS.

```bash
git add web/src/features/files web/src/features/servers web/src/features/users
git commit -m "feat(web): extract archives with progress, lock power while extracting, warn on file access

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 17: Playwright und Abschlussprüfung der Texte

**Files:**
- Modify: `web/e2e/servers.spec.ts`
- Prüfen: `web/src/i18n/de.ts`, `en.ts` (alle in den Aufgaben 13–16 genutzten Schlüssel vorhanden; `en: Messages` und `i18n.test.ts` erzwingen Vollständigkeit)

**Interfaces:**
- Consumes: Mock-Verhalten nach V5 (Upload legt Datei an, Entpacken legt den Zielordner nach ~6 s an), `signIn` aus `servers.spec.ts`.

- [ ] **Step 1: Playwright-Tests anhängen**

Am Ende von `web/e2e/servers.spec.ts`:

```ts
test('admin edits a config, uploads, renames and deletes files', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await page.getByRole('tab', { name: 'Dateien' }).click()
  const files = page.getByRole('region', { name: 'Dateien' })

  await files.getByRole('button', { name: 'plugins' }).click()
  await files.getByRole('button', { name: 'Essentials' }).click()
  await files.getByRole('button', { name: 'config.yml' }).click()
  const editor = page.getByRole('textbox', { name: 'Inhalt von config.yml' })
  await expect(editor).toBeVisible()
  await editor.click()
  await page.keyboard.press('ControlOrMeta+End')
  await page.keyboard.type('\n# geändert im Test')
  await expect(page.getByText('Ungespeicherte Änderungen')).toBeVisible()
  await page.keyboard.press('ControlOrMeta+s')
  await expect(page.getByText('Gespeichert.')).toBeVisible()
  await page.getByRole('button', { name: 'Zurück zur Liste' }).click()

  await page.getByLabel('Dateien hochladen').setInputFiles({ name: 'hallo.txt', mimeType: 'text/plain', buffer: Buffer.from('hallo welt') })
  const uploads = page.getByRole('region', { name: 'Uploads' })
  await expect(uploads.getByText('Hochgeladen')).toBeVisible()
  await expect(files.getByRole('button', { name: 'hallo.txt' })).toBeVisible()

  await files.getByRole('button', { name: 'Aktionen für hallo.txt' }).click()
  await page.getByRole('menuitem', { name: 'Umbenennen oder verschieben' }).click()
  const rename = page.getByRole('dialog', { name: '„hallo.txt“ umbenennen oder verschieben' })
  await rename.getByLabel('Neuer Pfad').fill('plugins/Essentials/servus.txt')
  await rename.getByRole('button', { name: 'Umbenennen' }).click()
  await expect(files.getByRole('button', { name: 'servus.txt' })).toBeVisible()

  await files.getByRole('button', { name: 'Aktionen für servus.txt' }).click()
  await page.getByRole('menuitem', { name: 'Löschen' }).click()
  await page.getByRole('dialog', { name: '„servus.txt“ löschen?' }).getByRole('button', { name: 'Endgültig löschen' }).click()
  await expect(files.getByRole('button', { name: 'servus.txt' })).toHaveCount(0)
})

test('admin uploads a world zip to a stopped server and extracts it', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'creative', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/2$/)
  await page.getByRole('tab', { name: 'Dateien' }).click()
  const files = page.getByRole('region', { name: 'Dateien' })

  await page.getByLabel('Dateien hochladen').setInputFiles({
    name: 'neue-welt.zip',
    mimeType: 'application/zip',
    buffer: Buffer.from('PK\u0005\u0006' + '\u0000'.repeat(18), 'binary'),
  })
  await expect(page.getByRole('region', { name: 'Uploads' }).getByText('Hochgeladen')).toBeVisible()

  await files.getByRole('button', { name: 'Aktionen für neue-welt.zip' }).click()
  await page.getByRole('menuitem', { name: 'Entpacken …' }).click()
  const dialog = page.getByRole('dialog', { name: '„neue-welt.zip“ entpacken' })
  await expect(dialog.getByLabel('Zielordner')).toHaveValue('neue-welt')
  await dialog.getByRole('button', { name: 'Entpacken' }).click()
  await expect(page.getByRole('progressbar', { name: 'Fortschritt beim Entpacken' })).toBeVisible()
  await expect(page.getByText('„neue-welt.zip“ wurde nach neue-welt entpackt.')).toBeVisible({ timeout: 15_000 })
  await expect(files.getByRole('button', { name: 'neue-welt' })).toBeVisible()
})

test('a user without file permission sees no files tab', async ({ page }) => {
  await signIn(page, 'alice', false)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await expect(page.getByRole('tab', { name: 'Übersicht' })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Dateien' })).toHaveCount(0)
})
```

Hinweise:
- `<progress>` hat implizit die Rolle `progressbar`; der Name kommt aus `aria-label`.
- Ein Download wird im Mock-Browser nicht geprüft: MSWs Service Worker lässt Navigations-Anfragen (auch `<a download>`) unbehandelt durch. Die `href` prüft Vitest (Aufgabe 13, 15); den echten Download prüft die manuelle Abnahme (`docs/dev/acceptance-tp3b.md`, Kriterium 5).
- Falls ein Upload-Fortschritt im Mock zu schnell vorbei ist, prüft der Test nur den Endzustand „Hochgeladen“ – das ist beabsichtigt.

- [ ] **Step 2: Texte prüfen**

Kein eigener Code: `make check` (`tsc` mit `en: Messages`) und der CI-Test `src/i18n/i18n.test.ts` („defines every static t() key“, `DYNAMIC_KEYS`) sichern Vollständigkeit. Zusätzlich die englischen Texte einmal lesen (Tippfehler, gleiche Bedeutung wie `de`).

- [ ] **Step 3: Prüfen und committen**

Run: `make check` → Expected: PASS. Push; CI-Jobs „Web“ und „E2E“ → Expected: PASS (`npm run e2e` im Job läuft gegen den Mock auf Port 5174).

```bash
git add web/e2e/servers.spec.ts
git commit -m "test(web): end-to-end file browser flows against the mock

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

Danach: Abschlussbericht an den Lead (Commits je Aufgabe, CI-Run, Abweichungen), PR bleibt Draft (der Lead setzt ihn auf „ready“).

## Self-Review (dieser Datei)

- Spec §6.1 (Liste, Brotkrumen, Sortierung, Karten, Menü, Werkzeugleiste, Upload-Warteschlange, Drag & Drop, Überschreiben-Dialog, Hinweis bei laufendem Server, Leerzustand) → Aufgaben 13, 14.
- §6.2 (CodeMirror lazy, Sprachen, Strg/Cmd+S, Verlassen-Warnung, Konflikt-Dialog, `server.properties`-Hinweis mit Link, `not_text` → Download) → Aufgabe 15.
- §6.3 (Entpack-Dialog mit Vorschlag, gesperrt bei laufendem Server, Fortschritt alle 2 s, Power-Buttons gesperrt) → Aufgabe 16.
- §6.4 (Hinweis beim Anhaken von `server.files`) → Aufgabe 16.
- §6.5 (Mocks) → Phase A; dieser Plan nutzt sie und überschreibt Antworten nur in Tests, wo der Ausgang deterministisch sein muss.
- Review Focus 3/4/5 der Übersicht → Aufgaben 13 (Download-Link, reservierte Namen), 14 (`File` direkt an `send`), 16 (Entpacken nur gestoppt, Power-Sperre).
- Typen kommen direkt aus `components['schemas']` (keine Abhängigkeit von Re-Exporten in `api/types.ts`).
```
