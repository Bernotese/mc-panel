# mc-panel Teilprojekt 3c – Phase B3: Frontend Plugins und Mods (Aufgaben 13–16)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der Tab „Plugins“ (paper/purpur) bzw. „Mods“ (fabric/neoforge) auf der Server-Detailseite: Installiert-Liste mit Icon, Namens-Fallback, Version, Quelle, Schalter aktiv/inaktiv, Menü (Projektseite, Version wechseln, Aktualisieren, Entfernen), Hinweisen und Neustart-Banner; Entdecken mit verzögerter Suche, Quellen-Umschalter, „Mehr laden“ und Fehlerzustand; Installieren-Dialog mit Versionsliste und Auflösungs-Vorschau; Job-Fortschritt mit 2-s-Abfrage und Sperren; Updates; Hinweis beim Vergeben von `server.plugins`; Playwright gegen den Mock.

**Architecture:** Alles Neue liegt in `web/src/features/plugins/`. Reine Logik (Art pro Servertyp, Namens-/Versions-Fallback, Standardversion, Sicherheitsfilter für Icon- und Link-URLs, Job-Fortschritt) steht in `model.ts` und ist per Vitest getestet; `api.ts` hält Query-Keys, Query-Options und Aufrufe über den vorhandenen Client (`@/api/client`, `unwrap`/`unwrapVoid`). Inventar, Updates und Job hängen unter `['servers', id, 'plugins', …]` (Power-Aktionen frischen `restart_needed` mit auf), Upstream-Abfragen (Suche, Versionen, Vorschau) bewusst unter `['plugin-upstream', id, …]`, damit eine Power-Aktion nicht alle geladenen Suchseiten erneut gegen Modrinth/Hangar und das 60/min-Limit schickt. Der Job wird wie der Entpack-Job in 3b über `setQueryData` gestartet und alle 2 s abgefragt, solange er läuft; sein Ende lädt Inventar, Updates, Suche und Vorschau neu. Die Detailseite zeigt den Tab nur bei `server.plugins` und einer Art mit Plugins/Mods.

**Tech Stack:** React 19, TypeScript, TanStack Query (`useQuery`, `useInfiniteQuery`, `useMutation`), shadcn/ui (Tabs, Dialog, DropdownMenu, Switch, Badge, Button, Input, Label), `DataList`, `ConfirmDialog`, `SectionCard`, react-i18next, MSW 2, Vitest + Testing Library, Playwright. Keine neuen Pakete.

**Spec:** `docs/superpowers/specs/2026-10-12-mc-panel-plugins-design.md` (§6.6, §7) · **Übersicht und Verträge:** `docs/superpowers/plans/2026-10-12-plugins-00-overview.md` (V3, V5; „Ausführung“, „Global Constraints“ und Dateieigentum B3 gelten vollständig).

## Ausführung dieser Phase

- Teammate „frontend“, nach dem Merge von Phase A: Worktree `git worktree add ../mc-panel-wt/feat-plugins-frontend -b feat/plugins-frontend origin/main`, dort einmal `(cd web && npm ci)`.
- Lokal nur `make check` (gofmt, vet, Go-Build, `tsc`). Vitest, ESLint und Playwright laufen nur in der CI (Job „Web (lint, typecheck, test, build, audit)“ bzw. „E2E“). Neue Dateien vor `make check` mit `git add -N <datei>` vormerken.
- „Run: `npx vitest run …`“ in den Schritten heißt: **nicht lokal**, sondern Commit, Push, `gh pr checks <pr> --watch`, Ergebnis im CI-Log (`gh run view <id> --log-failed`).
- Ein Draft-PR für die ganze Phase (Titel `feat(web): plugins and mods tab`), ein Commit pro Aufgabe; Mergen nur durch den Nutzer.
- Dateieigentum B3: `web/src/features/plugins/**` (neu), `web/src/features/servers/**`, `web/src/features/users/**`, `web/src/i18n/**`, `web/e2e/**`; dazu nach Freigabe durch den Lead die eine Zeile in `web/playwright.config.ts` (siehe „Abweichungen“). **Nicht** ändern: `web/src/api/**`, `web/src/mocks/**` (Phase A) – fehlt dort etwas oder verhält sich der Mock anders als unten angenommen, den Lead fragen (Änderung per eigenem PR auf `main`, danach `git merge origin/main`).

## Was Phase A liefert (Annahmen dieses Plans)

- `web/src/api/schema.d.ts` enthält die elf Operationen aus V3; `web/src/api/types.ts` exportiert die Aliase `PluginSource`, `PluginKind`, `PluginChannel`, `PluginProjectRef`, `PluginVersionRef`, `PluginJarMeta`, `InstalledJar`, `PluginInventory`, `PluginSearchHit`, `PluginSearchResult`, `PluginVersion`, `PluginVersionList`, `PluginPlanEntry`, `PluginAlready`, `PluginExternal`, `PluginIncompatible`, `PluginResolution`, `InstallPluginRequest`, `PluginUpdate`, `PluginUpdates`, `UpdatePluginsRequest`, `TogglePluginRequest`, `PluginFileRequest`, `PluginJobStep`, `PluginJob`, `PluginJobResponse` sowie die benannten Enums `PluginJarSource` (`modrinth|hangar|manual`), `PluginIncompatibleReason`, `PluginJobKind`, `PluginJobState`, `PluginJobStepState`, `PluginStepErrorCode` (`error_code` ist `PluginStepErrorCode | null`). Dieser Plan importiert sie von `@/api/types`.
- `web/src/api/errors.ts` (`KNOWN_CODES`) kennt `plugins_unsupported`, `plugin_source_unavailable`, `plugin_not_downloadable`, `plugin_incompatible`; `de.ts`/`en.ts` haben dafür `errors.<code>`-Texte und `i18n.test.ts` hat sie in `ERROR_CODES` (Phase A, Aufgabe 2). Alles unter `plugins.*` schreibt dieser Plan.
- MSW-Mocks (`web/src/mocks/handlers/plugins.ts`, V5): Server 1 „survival“ (paper 1.21.11, läuft) mit `LuckPerms-Bukkit-5.5.71.jar` (Modrinth, Titel „LuckPerms“, Update auf Version `5.5.72`, das schon `GET …/updates` ohne `refresh` liefert), `ViaVersion-5.5.1.jar` (Hangar, Titel „ViaVersion“), `OldTool.jar` (manuell, `meta.name` „OldTool“, `meta.version` „1.0“), `Disabled.jar.disabled` (Modrinth, aus); Server 2 „creative“ (vanilla) → `409 plugins_unsupported`; Server 3 „lobby“ (ohne Instanz) → `404`; **neu** Server 4 „modded“ (fabric 1.21.11, gestoppt, `kind: mod`, `dir: mods`, `sources: ['modrinth']`, `fabric-api-0.110.0.jar`). `alice` hat auf Server 1 nur `server.view` + `server.power` (Plugin-Routen → `403`) und keine Rechte auf Server 4. Katalog: Hangar `ViaBackwards` (Pflicht-Abhängigkeit ViaVersion → Vorschau „Bereits vorhanden: ViaVersion“, `install: [ViaBackwards]`), Hangar `Essentials` (nur extern, resolve/install `409 plugin_not_downloadable`), Modrinth `chunkyborder` (braucht `chunky` → `install: [Chunky (required_by „ChunkyBorder“), ChunkyBorder]`). Suche nach Teilstring (case-insensitiv), 20 pro Seite. Ein Job läuft ~4 s mit Schritten `pending → downloading → done`, ein zweiter → `409 conflict`; `toggle` benennt um (meta bleibt), `delete` entfernt.
- Der Updatecheck mit Umgehung des Caches wird als `?refresh=true` gesendet (openapi-fetch serialisiert den Boolean so); Mock und B2 akzeptieren `true` (mit plan-contracts und plan-panel abgestimmt).
- `project` in `versions`/`resolve`/`install` ist `project_id` aus dem Suchtreffer bzw. `project.id` aus dem Inventar.

## Abweichungen

1. **Tab-Integration in Aufgabe 13 statt 15:** Die Tests der Aufgaben 13–15 öffnen den Tab wie in 3b über `ServerDetailPage` (`test-utils.tsx`). Deshalb verdrahtet Aufgabe 13 den Tab samt Titel nach Art und Sichtbarkeitsregeln und passt `server-detail-page.test.tsx` an; Aufgabe 15 behält Job, Updates, Sperren und Rechte-Hinweis.
2. **Alle Texte in Aufgabe 13:** Der komplette `plugins.*`-Block (plus `servers.detail.tabs.plugins/mods`, `permissions.plugins_warning`) kommt in einem Schritt, damit spätere Aufgaben `de.ts`/`en.ts` nicht stückweise anfassen (wie files-04).
3. **`web/playwright.config.ts`:** Das Projekt `mock` nimmt nur `(totp|smoke|servers)\.spec\.ts`; ohne Anpassung liefe `web/e2e/plugins.spec.ts` nie. Aufgabe 16 ergänzt `plugins` in diesem Regex – eine Zeile außerhalb des B3-Eigentums, der Lead gibt sie frei (sonst hängt der Teammate die Tests an `web/e2e/servers.spec.ts` an).
4. **Rechte-Bezeichnung:** `permission.server.plugins` heißt jetzt „Plugins/Mods“ (en „Plugins/mods“), passend zum Hinweistext aus Spec §7.2. Kein vorhandener Test nutzt die alte Bezeichnung.
5. **Playwright-Helfer:** `signIn` wandert aus `web/e2e/servers.spec.ts` nach `web/e2e/helpers.ts`, damit `plugins.spec.ts` ihn importieren kann (eine Spec-Datei zu importieren würde ihre Tests doppelt registrieren).

## Sicherheit im Frontend (CSP, Upstream-Inhalte)

- Keine Inline-Skripte, kein `dangerouslySetInnerHTML`, keine Markdown-/HTML-Darstellung: Titel, Autor, Kurzbeschreibung, Dateinamen und Fehlertexte sind React-Textknoten (Test in Aufgabe 14 mit `<b>`-Beschreibung).
- Bilder nur über den Icon-Proxy: `iconSrc` lässt ausschließlich `/api/servers/<id>/plugins/icon?url=…` durch, alles andere wird zum Platzhalter (CSP `img-src 'self'` bleibt unverändert).
- Externe Links (`page_url`, `external_url`, `external[].url`) nur bei `http:`/`https:` (`externalHref`; eine `javascript:`-URL aus einer Upstream-Antwort wird nie ein Link), immer mit `target="_blank"` und `rel="noopener noreferrer"`.
- Upstream-Abfragen ohne automatische Wiederholung (`retry: false`): ein `502`/`429` soll nicht dreifach gegen Modrinth und das Konto-Limit laufen; der Nutzer wiederholt mit „Erneut versuchen“.

## Review Focus (aus der Übersicht, für B3 relevant)

- **Feindliche Upstream-Antworten (Focus 1):** Beschreibung mit HTML/Markdown bleibt Text, fremde Bild-URLs und `javascript:`-Links werden verworfen. → Aufgaben 13 (`model.test.ts`, Projektseite), 14 (Discover-Test).
- **Gleichzeitige Änderungen (Focus 3):** Während ein Job läuft, sind Installieren, Aktualisieren, Schalter und Entfernen gesperrt; `409 conflict` bekommt einen eigenen Text; nach Job-Ende, Schalter und Entfernen wird das Inventar neu geladen (die Platte ist Quelle der Wahrheit). → Aufgaben 13–15.
- **Upstream nicht erreichbar (Focus 4):** `source_errors` → Hinweis, Installiert bleibt bedienbar; Entdecken zeigt Fehlerzustand mit „Erneut versuchen“; Schritt-Fehler `source_unavailable` wird übersetzt. → Aufgaben 13–15.

## Dateistruktur (B3)

```
web/src/features/plugins/model.ts (+test)          (13) Art pro Typ, Namens-/Versions-Fallback, Standardversion, URL-Filter, Fortschritt
web/src/features/plugins/api.ts                    (13) Konstanten, Query-Keys, Query-Options, Aufrufe, startJob, refreshPluginState
web/src/features/plugins/messages.ts               (13) Fehlertexte für 409/429, Toast-Hook
web/src/features/plugins/plugin-icon.tsx           (13) Icon über den Proxy, Platzhalter
web/src/features/plugins/installed-list.tsx        (13) DataList (Tabelle/Karten) mit Schalter und Menü
web/src/features/plugins/plugins-tab.tsx (+test)   (13–15) Tab: Banner, Job, Ansichten Installiert/Entdecken, Dialoge
web/src/features/plugins/test-utils.tsx            (13) Fixtures und openPlugins/openDiscover
web/src/features/plugins/use-debounced-value.ts    (14) verzögerter Suchbegriff
web/src/features/plugins/discover.tsx (+test)      (14) Suche, Quellen-Umschalter, Treffer, „Mehr laden“
web/src/features/plugins/install-dialog.tsx (+test)(14) Versionsliste, Vorschau, Installieren
web/src/features/plugins/use-plugin-job.ts         (15) Job-Abfrage (2 s solange running), Ende → Neuladen + Toast
web/src/features/plugins/job-progress.tsx          (15) Fortschritt pro Jar
web/src/features/plugins/plugins-jobs.test.tsx     (15) Job, Updates, Sperren
web/src/features/servers/server-detail-page.tsx (+test) (13) Tab „Plugins“/„Mods“
web/src/features/users/permissions-page.tsx (+test)     (15) Hinweis bei server.plugins
web/src/i18n/de.ts, en.ts, i18n.test.ts            (13) alle Texte von 3c
web/e2e/helpers.ts, servers.spec.ts, plugins.spec.ts (16) Playwright
web/playwright.config.ts                           (16) eine Zeile (nach Freigabe)
```

---

### Task 13: Grundlagen, Installiert-Liste und Tab

**Files:**
- Create: `web/src/features/plugins/model.ts`, `model.test.ts`, `api.ts`, `messages.ts`, `plugin-icon.tsx`, `installed-list.tsx`, `plugins-tab.tsx`, `plugins-tab.test.tsx`, `test-utils.tsx`
- Modify: `web/src/features/servers/server-detail-page.tsx`, `web/src/features/servers/server-detail-page.test.tsx`
- Modify: `web/src/i18n/de.ts`, `web/src/i18n/en.ts`, `web/src/i18n/i18n.test.ts`

**Interfaces:**
- Consumes: `api` (`@/api/client`), `unwrap`/`unwrapVoid`/`isApiError`/`isSudoCancelled` (`@/api/errors`), Plugin-Typen aus `@/api/types` (Phase A), `DataList`, `ConfirmDialog`, `SectionCard`, `usePower` (`@/features/servers/use-power`), `errorMessage` (`@/lib/error-message`), `formatBytes` (`@/features/files/format`, ab Aufgabe 14).
- Produces (von Aufgaben 14–16 genutzt):
  - `model.ts`: `pluginKindOf(type): PluginKind | null`, `sourcesFor(kind)`, `jarName(jar)`, `jarVersion(jar)`, `defaultVersion(versions, preferred?)`, `isJobRunning(data)`, `updatesByFile(updates)`, `canInstall(resolution)`, `jobProgress(job)`, `uniqueHits(hits)`, `iconSrc(url)`, `externalHref(url)`, `type InstallTarget`.
  - `api.ts`: `JOB_POLL_MS`, `SEARCH_DEBOUNCE_MS`, `MAX_QUERY_LENGTH`, `MAX_SEARCH_OFFSET`, `inventoryKey`, `updatesKey`, `pluginJobKey`, `searchesKey`, `searchKey`, `versionsKey`, `resolvesKey`, `resolveKey`, `inventoryQueryOptions`, `updatesQueryOptions`, `checkUpdates`, `nextOffset`, `searchQueryOptions`, `versionsQueryOptions`, `resolveQueryOptions`, `installPlugin`, `updatePlugins`, `togglePlugin`, `deletePlugin`, `pluginJobQueryOptions`, `startJob`, `refreshPluginState`.
  - `messages.ts`: `pluginErrorMessage(err, t)`, `jobStartErrorMessage(err, t)`, `installErrorMessage(err, t)`, `usePluginErrorToast()`.
  - `installed-list.tsx`: `InstalledList` (Props für Updates, Sperre, Versionswechsel und Einzel-Update schon vorhanden, optional).
  - `plugins-tab.tsx`: `PluginsTab({ server, kind })`.
  - `test-utils.tsx`: `jar`, `modrinthJar`, `hangarJar`, `inventory`, `mockInventory`, `hit`, `version`, `planEntry`, `step`, `job`, `update`, `openPlugins`, `openDiscover`.
  - i18n: kompletter Block `plugins.*`, `servers.detail.tabs.plugins`/`mods`, `permission.server.plugins` („Plugins/Mods“), `permissions.plugins_warning`.

- [ ] **Step 1: Tests für die reine Logik schreiben (rot)**

`web/src/features/plugins/model.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { InstalledJar, PluginJob, PluginSearchHit, PluginVersion } from '@/api/types'
import { nextOffset } from './api'
import {
  canInstall,
  defaultVersion,
  externalHref,
  iconSrc,
  isJobRunning,
  jarName,
  jarVersion,
  jobProgress,
  pluginKindOf,
  sourcesFor,
  uniqueHits,
  updatesByFile,
} from './model'

const base: InstalledJar = {
  file: 'Foo-1.0.jar',
  enabled: true,
  size: 10,
  mtime: 0,
  source: 'manual',
  project: null,
  version: null,
  meta: null,
  installed_by_panel: false,
}

function v(id: string, channel: PluginVersion['channel'] = 'release', downloadable = true): PluginVersion {
  return { id, name: id, channel, published_at: 0, downloadable, external_url: downloadable ? null : 'https://example.org/x.jar' }
}

describe('pluginKindOf', () => {
  it.each([
    ['paper', 'plugin'],
    ['purpur', 'plugin'],
    ['fabric', 'mod'],
    ['neoforge', 'mod'],
    ['vanilla', null],
    ['modrinth', null],
    ['curseforge', null],
  ] as const)('%s → %s', (type, kind) => {
    expect(pluginKindOf(type)).toBe(kind)
  })

  it('offers Hangar only for plugins', () => {
    expect(sourcesFor('plugin')).toEqual(['modrinth', 'hangar'])
    expect(sourcesFor('mod')).toEqual(['modrinth'])
  })
})

describe('jarName and jarVersion', () => {
  it('prefers the project title, then the jar metadata, then the file name', () => {
    const project = { id: 'p', slug: 'foo', title: 'Foo Plugin', page_url: 'https://modrinth.com/plugin/foo', icon_url: null }
    const meta = { kind: 'plugin' as const, id: '', name: 'FooMeta', version: '0.9' }
    expect(jarName({ ...base, project, meta })).toBe('Foo Plugin')
    expect(jarName({ ...base, project: { ...project, title: '  ' }, meta })).toBe('FooMeta')
    expect(jarName({ ...base, meta })).toBe('FooMeta')
    expect(jarName({ ...base, meta: { ...meta, name: '' } })).toBe('Foo-1.0.jar')
    expect(jarName(base)).toBe('Foo-1.0.jar')
  })

  it('prefers the matched version over the jar metadata', () => {
    const meta = { kind: 'plugin' as const, id: '', name: 'Foo', version: '0.9' }
    expect(jarVersion({ ...base, meta, version: { id: 'v1', name: '1.0', channel: 'release' } })).toBe('1.0')
    expect(jarVersion({ ...base, meta })).toBe('0.9')
    expect(jarVersion(base)).toBeNull()
  })
})

describe('defaultVersion', () => {
  it('takes the newest downloadable release', () => {
    expect(defaultVersion([v('3-beta', 'beta'), v('2'), v('1')])).toBe('2')
    expect(defaultVersion([v('3', 'release', false), v('2')])).toBe('2')
  })
  it('falls back to the newest downloadable pre-release', () => {
    expect(defaultVersion([v('3-alpha', 'alpha'), v('2-beta', 'beta')])).toBe('3-alpha')
  })
  it('keeps a preferred version only if it can be downloaded', () => {
    expect(defaultVersion([v('3-beta', 'beta'), v('2')], '3-beta')).toBe('3-beta')
    expect(defaultVersion([v('3', 'release', false), v('2')], '3')).toBe('2')
    expect(defaultVersion([v('2')], 'gone')).toBe('2')
  })
  it('returns null without a downloadable version', () => {
    expect(defaultVersion([v('1', 'release', false)])).toBeNull()
    expect(defaultVersion([])).toBeNull()
  })
})

describe('jobs and plans', () => {
  const job: PluginJob = {
    id: 'j',
    kind: 'install',
    state: 'running',
    started_at: 1,
    finished_at: 0,
    steps: [
      { title: 'A', file: 'a.jar', state: 'done', error_code: null, error: '' },
      { title: 'B', file: 'b.jar', state: 'failed', error_code: 'hash_mismatch', error: 'x' },
      { title: 'C', file: 'c.jar', state: 'skipped', error_code: null, error: '' },
      { title: 'D', file: 'd.jar', state: 'downloading', error_code: null, error: '' },
      { title: 'E', file: 'e.jar', state: 'pending', error_code: null, error: '' },
    ],
  }

  it('counts finished steps', () => {
    expect(jobProgress(job)).toEqual({ finished: 3, total: 5 })
  })
  it('knows a running job', () => {
    expect(isJobRunning({ job })).toBe(true)
    expect(isJobRunning({ job: { ...job, state: 'done' } })).toBe(false)
    expect(isJobRunning({ job: null })).toBe(false)
    expect(isJobRunning(undefined)).toBe(false)
  })
  it('allows installing only a non-empty plan without incompatibilities', () => {
    const entry = {
      source: 'modrinth' as const,
      project_id: 'p',
      slug: 'p',
      title: 'P',
      version_id: 'v',
      version_name: '1',
      channel: 'release' as const,
      filename: 'p.jar',
      size: 1,
      required_by: null,
    }
    expect(canInstall({ install: [entry], already: [], external: [], incompatible: [] })).toBe(true)
    expect(canInstall({ install: [], already: [], external: [], incompatible: [] })).toBe(false)
    expect(canInstall({ install: [entry], already: [], external: [], incompatible: [{ title: 'X', reason: 'client_only' }] })).toBe(false)
  })
  it('indexes updates by file', () => {
    const update = { file: 'a.jar', from: { id: '1', name: '1', channel: 'release' as const }, to: { id: '2', name: '2', channel: 'release' as const } }
    expect(updatesByFile([update]).get('a.jar')).toBe(update)
    expect(updatesByFile(undefined).size).toBe(0)
  })
})

describe('search pages', () => {
  const hit = (project_id: string): PluginSearchHit => ({
    source: 'modrinth',
    project_id,
    slug: project_id,
    title: project_id,
    description: '',
    author: 'a',
    downloads: 0,
    icon_url: null,
    page_url: `https://modrinth.com/plugin/${project_id}`,
    installed: false,
  })

  it('computes the next offset and stops at the end or at 1000', () => {
    expect(nextOffset({ hits: Array.from({ length: 20 }, (_, i) => hit(`p${i}`)), total: 45, offset: 0 })).toBe(20)
    expect(nextOffset({ hits: [hit('a')], total: 21, offset: 20 })).toBeUndefined()
    expect(nextOffset({ hits: [], total: 50, offset: 40 })).toBeUndefined()
    expect(nextOffset({ hits: Array.from({ length: 20 }, (_, i) => hit(`p${i}`)), total: 5000, offset: 980 })).toBe(1000)
    expect(nextOffset({ hits: Array.from({ length: 20 }, (_, i) => hit(`p${i}`)), total: 5000, offset: 1000 })).toBeUndefined()
  })

  it('drops hits a later page repeats', () => {
    expect(uniqueHits([hit('a'), hit('b'), hit('a')]).map((h) => h.project_id)).toEqual(['a', 'b'])
  })
})

describe('URL filters', () => {
  it('only lets proxied icons through', () => {
    expect(iconSrc('/api/servers/1/plugins/icon?url=https%3A%2F%2Fcdn.modrinth.com%2Fa.png')).toBe(
      '/api/servers/1/plugins/icon?url=https%3A%2F%2Fcdn.modrinth.com%2Fa.png',
    )
    expect(iconSrc('https://cdn.modrinth.com/a.png')).toBeNull()
    expect(iconSrc('//evil.example/a.png')).toBeNull()
    expect(iconSrc('/api/servers/1/files/download?path=x')).toBeNull()
    expect(iconSrc(null)).toBeNull()
  })

  it('only links http(s) URLs', () => {
    expect(externalHref('https://modrinth.com/plugin/luckperms')).toBe('https://modrinth.com/plugin/luckperms')
    expect(externalHref('http://example.org/x.jar')).toBe('http://example.org/x.jar')
    expect(externalHref('javascript:alert(1)')).toBeNull()
    expect(externalHref('data:text/html,hi')).toBeNull()
    expect(externalHref('not a url')).toBeNull()
    expect(externalHref(null)).toBeNull()
  })
})
```

Run: `make check` → Expected: FAIL (`./model`, `./api` fehlen).

- [ ] **Step 2: `model.ts` und `api.ts` implementieren**

`web/src/features/plugins/model.ts`:

```ts
import type {
  InstalledJar,
  InstanceType,
  PluginJob,
  PluginJobResponse,
  PluginKind,
  PluginResolution,
  PluginSearchHit,
  PluginSource,
  PluginUpdate,
  PluginVersion,
} from '@/api/types'

/** What the install dialog is opened for: a search hit or an installed jar ("Version wechseln"). */
export interface InstallTarget {
  source: PluginSource
  /** project_id of the hit resp. project.id of the jar (V3 `project`). */
  project: string
  title: string
  /** Preselected version id; the dialog falls back to the newest release. */
  version?: string | null
}

const KINDS: Partial<Record<InstanceType, PluginKind>> = { paper: 'plugin', purpur: 'plugin', fabric: 'mod', neoforge: 'mod' }

/** Spec §3.1: only these four types get a "Plugins"/"Mods" tab; vanilla and modpacks never (no 409 in the UI). */
export function pluginKindOf(type: InstanceType): PluginKind | null {
  return KINDS[type] ?? null
}

/** Spec §3.1: Hangar has no mods. Used until the inventory names the sources itself. */
export function sourcesFor(kind: PluginKind): PluginSource[] {
  return kind === 'plugin' ? ['modrinth', 'hangar'] : ['modrinth']
}

/** Spec §7.1: project title, else the name from plugin.yml/fabric.mod.json/neoforge.mods.toml, else the file name. */
export function jarName(jar: InstalledJar): string {
  return jar.project?.title.trim() || jar.meta?.name.trim() || jar.file
}

export function jarVersion(jar: InstalledJar): string | null {
  return jar.version?.name || jar.meta?.version || null
}

/** Versions come newest first (V3). Spec §7.1: default is the newest release; external-only ones cannot be chosen. */
export function defaultVersion(versions: readonly PluginVersion[], preferred?: string | null): string | null {
  const downloadable = versions.filter((v) => v.downloadable)
  if (preferred && downloadable.some((v) => v.id === preferred)) return preferred
  return (downloadable.find((v) => v.channel === 'release') ?? downloadable[0])?.id ?? null
}

export function isJobRunning(data: PluginJobResponse | undefined): boolean {
  return data?.job?.state === 'running'
}

export function updatesByFile(updates: readonly PluginUpdate[] | undefined): Map<string, PluginUpdate> {
  return new Map((updates ?? []).map((u) => [u.file, u]))
}

/** Spec §6.3: an incompatible dependency makes the plan unusable; an empty plan has nothing to do. */
export function canInstall(plan: PluginResolution): boolean {
  return plan.incompatible.length === 0 && plan.install.length > 0
}

export function jobProgress(job: PluginJob): { finished: number; total: number } {
  const finished = job.steps.filter((s) => s.state === 'done' || s.state === 'failed' || s.state === 'skipped').length
  return { finished, total: job.steps.length }
}

/** Upstream rankings shift between pages; a repeated project would show twice (and collide as React key). */
export function uniqueHits(hits: readonly PluginSearchHit[]): PluginSearchHit[] {
  const seen = new Set<string>()
  return hits.filter((h) => {
    const key = `${h.source}:${h.project_id}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

const ICON_PROXY = /^\/api\/servers\/\d+\/plugins\/icon\?url=/

/** Spec §6.8: icons only through the panel's proxy (CSP img-src 'self'); any other URL becomes the placeholder. */
export function iconSrc(url: string | null | undefined): string | null {
  return url && ICON_PROXY.test(url) ? url : null
}

/** Links from upstream answers only for http(s); a javascript: or data: URL never becomes an href. */
export function externalHref(url: string | null | undefined): string | null {
  if (!url) return null
  try {
    const { protocol } = new URL(url)
    return protocol === 'https:' || protocol === 'http:' ? url : null
  } catch {
    return null
  }
}
```

`web/src/features/plugins/api.ts`:

```ts
import { infiniteQueryOptions, queryOptions, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type {
  InstallPluginRequest,
  PluginInventory,
  PluginJob,
  PluginJobResponse,
  PluginResolution,
  PluginSearchResult,
  PluginSource,
  PluginUpdates,
  PluginVersionList,
} from '@/api/types'

/** The job is polled every 2 s while it runs (spec §7.1). */
export const JOB_POLL_MS = 2_000
/** The search waits for a typing pause (spec §7.1 "Suchfeld verzögert"). */
export const SEARCH_DEBOUNCE_MS = 400
/** Contract limits of GET …/plugins/search (V3). */
export const MAX_QUERY_LENGTH = 100
export const MAX_SEARCH_OFFSET = 1000
/** Like the panel caches (search 5 min, versions 15 min): switching views does not ask again. */
const SEARCH_STALE_MS = 5 * 60_000
const VERSIONS_STALE_MS = 15 * 60_000

/** Below serverKey(id): a power action refreshes restart_needed together with the server. */
export const inventoryKey = (id: number) => ['servers', id, 'plugins', 'inventory'] as const
export const updatesKey = (id: number) => ['servers', id, 'plugins', 'updates'] as const
export const pluginJobKey = (id: number) => ['servers', id, 'plugins', 'job'] as const
/**
 * Deliberately NOT below ['servers', …]: every power action invalidates the server queries, and re-running all loaded
 * search pages, version lists and previews would cost upstream calls and the 60/min limit (spec §6.6).
 */
export const searchesKey = (id: number) => ['plugin-upstream', id, 'search'] as const
export const searchKey = (id: number, source: PluginSource, q: string) => ['plugin-upstream', id, 'search', source, q] as const
export const versionsKey = (id: number, source: PluginSource, project: string) => ['plugin-upstream', id, 'versions', source, project] as const
export const resolvesKey = (id: number) => ['plugin-upstream', id, 'resolve'] as const
export const resolveKey = (id: number, source: PluginSource, project: string, version: string) =>
  ['plugin-upstream', id, 'resolve', source, project, version] as const

export function inventoryQueryOptions(id: number) {
  return queryOptions<PluginInventory>({
    queryKey: inventoryKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/plugins', { params: { path: { id } } })),
  })
}

/** Served from the panel's 1-h cache; no upstream call per tab visit. */
export function updatesQueryOptions(id: number) {
  return queryOptions<PluginUpdates>({
    queryKey: updatesKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/plugins/updates', { params: { path: { id } } })),
    retry: false,
  })
}

/** "Nach Updates suchen": bypasses the cache and counts against the 60/min limit (spec §6.6). */
export function checkUpdates(id: number): Promise<PluginUpdates> {
  return unwrap(api.GET('/servers/{id}/plugins/updates', { params: { path: { id }, query: { refresh: true } } }))
}

export function nextOffset(last: PluginSearchResult): number | undefined {
  const next = last.offset + last.hits.length
  return last.hits.length > 0 && next < last.total && next <= MAX_SEARCH_OFFSET ? next : undefined
}

/** Empty q = the most popular matching projects (spec §7.1); 20 hits per page, "Mehr laden" via offset. */
export function searchQueryOptions(id: number, source: PluginSource, q: string) {
  return infiniteQueryOptions({
    queryKey: searchKey(id, source, q),
    initialPageParam: 0,
    queryFn: ({ pageParam }): Promise<PluginSearchResult> =>
      unwrap(
        api.GET('/servers/{id}/plugins/search', {
          params: { path: { id }, query: { source, q: q === '' ? undefined : q, offset: pageParam === 0 ? undefined : pageParam } },
        }),
      ),
    getNextPageParam: (last) => nextOffset(last),
    staleTime: SEARCH_STALE_MS,
    retry: false,
  })
}

export function versionsQueryOptions(id: number, source: PluginSource, project: string) {
  return queryOptions<PluginVersionList>({
    queryKey: versionsKey(id, source, project),
    queryFn: () => unwrap(api.GET('/servers/{id}/plugins/versions', { params: { path: { id }, query: { source, project } } })),
    staleTime: VERSIONS_STALE_MS,
    retry: false,
  })
}

/** Depends on the inventory ("Bereits vorhanden"): refreshPluginState invalidates it after every change. */
export function resolveQueryOptions(id: number, source: PluginSource, project: string, version: string) {
  return queryOptions<PluginResolution>({
    queryKey: resolveKey(id, source, project, version),
    queryFn: () => unwrap(api.GET('/servers/{id}/plugins/resolve', { params: { path: { id }, query: { source, project, version } } })),
    retry: false,
  })
}

export function installPlugin(id: number, body: InstallPluginRequest): Promise<PluginJob> {
  return unwrap(api.POST('/servers/{id}/plugins/install', { params: { path: { id } }, body }))
}

/** files = [] updates every jar with an update (V3). */
export function updatePlugins(id: number, files: string[]): Promise<PluginJob> {
  return unwrap(api.POST('/servers/{id}/plugins/update', { params: { path: { id } }, body: { files } }))
}

export function togglePlugin(id: number, file: string, enabled: boolean): Promise<void> {
  return unwrapVoid(api.POST('/servers/{id}/plugins/toggle', { params: { path: { id } }, body: { file, enabled } }))
}

export function deletePlugin(id: number, file: string): Promise<void> {
  return unwrapVoid(api.POST('/servers/{id}/plugins/delete', { params: { path: { id } }, body: { file } }))
}

export function pluginJobQueryOptions(id: number) {
  return queryOptions<PluginJobResponse>({
    queryKey: pluginJobKey(id),
    queryFn: () => unwrap(api.GET('/servers/{id}/plugins/jobs', { params: { path: { id } } })),
  })
}

/** The 202 answer becomes the job query's data, which starts the 2-s polling (same pattern as extracting in 3b). */
export function startJob(queryClient: QueryClient, id: number, job: PluginJob): void {
  queryClient.setQueryData<PluginJobResponse>(pluginJobKey(id), { job })
}

/** After a job, a toggle or a removal the disk changed: inventory, updates, "installiert" badges and previews are stale. */
export function refreshPluginState(queryClient: QueryClient, id: number): Promise<unknown> {
  return Promise.all([
    queryClient.invalidateQueries({ queryKey: inventoryKey(id) }),
    queryClient.invalidateQueries({ queryKey: updatesKey(id) }),
    queryClient.invalidateQueries({ queryKey: searchesKey(id) }),
    queryClient.invalidateQueries({ queryKey: resolvesKey(id) }),
  ])
}
```

Hinweis: Meldet `tsc`, dass `refresh: true` nicht zum generierten Query-Typ passt (falls Phase A `refresh` anders typisiert hat), den Lead fragen statt zu casten – der Vertrag sagt `boolean`.

- [ ] **Step 3: Fehlertexte und Icon**

`web/src/features/plugins/messages.ts`:

```ts
import type { TFunction } from 'i18next'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError, isSudoCancelled } from '@/api/errors'
import { errorMessage } from '@/lib/error-message'

/** Plugin-specific texts for the 409 answers (spec §6.6/§6.7); everything else as everywhere else. */
export function pluginErrorMessage(err: unknown, t: TFunction): string {
  if (isApiError(err, 'conflict')) return t('plugins.error.job_running')
  if (isApiError(err, 'plugin_incompatible')) return t('plugins.error.incompatible')
  if (isApiError(err, 'plugin_not_downloadable')) return t('plugins.error.not_downloadable')
  return errorMessage(err, t)
}

/** Starting a job: 429 means the panel's four job slots are taken (spec §6.4), not the per-account limit. */
export function jobStartErrorMessage(err: unknown, t: TFunction): string {
  if (isApiError(err, 'rate_limited')) return t('plugins.error.too_many_jobs')
  return pluginErrorMessage(err, t)
}

/** Starting an install: 409 conflict is a running job or a project installed meanwhile (plan 03, deviation 5). */
export function installErrorMessage(err: unknown, t: TFunction): string {
  if (isApiError(err, 'conflict')) return t('plugins.error.install_conflict')
  return jobStartErrorMessage(err, t)
}

export function usePluginErrorToast(): (err: unknown) => void {
  const { t } = useTranslation()
  return useCallback(
    (err: unknown) => {
      if (isSudoCancelled(err)) return
      toast.error(pluginErrorMessage(err, t))
    },
    [t],
  )
}
```

`web/src/features/plugins/plugin-icon.tsx`:

```tsx
import { Puzzle } from 'lucide-react'
import { useState } from 'react'
import { iconSrc } from './model'

/** 32-px icon through the panel's icon proxy; a placeholder when there is none or it fails to load. */
export function PluginIcon({ url }: { url: string | null | undefined }) {
  const src = iconSrc(url)
  const [failed, setFailed] = useState<string | null>(null)
  if (!src || failed === src) {
    return (
      <span aria-hidden="true" className="bg-muted text-muted-foreground flex size-8 shrink-0 items-center justify-center rounded-md">
        <Puzzle className="size-4" />
      </span>
    )
  }
  return (
    <img
      src={src}
      alt=""
      width={32}
      height={32}
      loading="lazy"
      decoding="async"
      className="size-8 shrink-0 rounded-md object-cover"
      onError={() => setFailed(src)}
    />
  )
}
```

- [ ] **Step 4: i18n – alle Texte für 3c**

In `web/src/i18n/de.ts`:
- `permission.server.plugins: 'Plugins',` → `plugins: 'Plugins/Mods',`
- in `servers.detail.tabs` nach `files: 'Dateien',`: `plugins: 'Plugins',` und `mods: 'Mods',`
- in `permissions` nach `files_warning: …,`:

```ts
    plugins_warning: '„Plugins/Mods“ erlaubt Code-Ausführung im Minecraft-Server. Vergib es nur an Personen, denen du voll vertraust.',
```

- auf oberster Ebene nach dem Block `files: { … },` (vor `settings: {`):

```ts
  plugins: {
    unsupported: 'Dieser Servertyp unterstützt keine Plugins oder Mods.',
    views: 'Ansicht',
    view: { installed: 'Installiert', discover: 'Entdecken' },
    restart: {
      title: 'Neustart nötig, damit die Änderungen wirken',
      description: 'Plugins und Mods werden nur beim Start des Servers geladen.',
      now: 'Jetzt neu starten',
    },
    source: { modrinth: 'Modrinth', hangar: 'Hangar', manual: 'Manuell' },
    channel: { release: 'Release', beta: 'Beta', alpha: 'Alpha' },
    installed: {
      title_plugin: 'Installierte Plugins',
      title_mod: 'Installierte Mods',
      caption: 'Inhalt von {{dir}}/',
      empty: 'In {{dir}}/ liegen noch keine Jars.',
      truncated: 'Es werden nur die ersten {{count}} Jars angezeigt.',
      source_errors: 'Modrinth gerade nicht erreichbar – Zuordnung unvollständig.',
      unmatched: 'Nicht zugeordnet – Updates nicht möglich',
      col: { name: 'Name', version: 'Version', source: 'Quelle', enabled: 'Aktiv', actions: 'Aktionen' },
      enabled_label: '{{name}} aktiv',
      actions_label: 'Aktionen für {{name}}',
      open_page: 'Projektseite öffnen',
      change_version: 'Version wechseln …',
      update_to: 'Auf {{version}} aktualisieren',
      update_badge: 'Update: {{version}}',
      remove: 'Entfernen',
      enabled_done: '„{{name}}“ aktiviert.',
      disabled_done: '„{{name}}“ deaktiviert.',
      remove_title: '„{{name}}“ entfernen?',
      remove_description: '{{file}} wird aus {{dir}}/ gelöscht. Konfigurationsdateien bleiben erhalten.',
      remove_submit: 'Entfernen',
      removed: '„{{name}}“ entfernt.',
    },
    updates: {
      check: 'Nach Updates suchen',
      checking: 'Sucht …',
      update_all: 'Alle aktualisieren ({{count}})',
      checked_at: 'Updates zuletzt geprüft {{when}}.',
      found: 'Updates gefunden: {{count}}',
      none: 'Alles aktuell.',
      failed: 'Updates konnten gerade nicht geprüft werden.',
      started: 'Aktualisierung gestartet.',
    },
    discover: {
      title: 'Entdecken',
      search: 'Suchen',
      search_placeholder: 'Name oder Stichwort',
      source: 'Quelle',
      popular: 'Beliebt auf {{source}}',
      results: 'Treffer auf {{source}}',
      empty: 'Keine passenden Treffer.',
      meta: 'von {{author}} · {{downloads}} Downloads',
      installed: 'Installiert',
      install: 'Installieren',
      install_label: '{{title}} installieren',
      open_page: 'Projektseite',
      open_page_label: 'Projektseite von {{title}} (neues Fenster)',
      failed: 'Die Suche ist gerade nicht möglich.',
    },
    install: {
      title: '{{title}} installieren',
      description: 'Wähle eine Version. Pflicht-Abhängigkeiten werden mitinstalliert.',
      versions: 'Version',
      versions_loading: 'Versionen werden geladen …',
      no_versions: 'Für diese Spielversion gibt es keine passende Version.',
      published: 'veröffentlicht {{date}}',
      external_only: 'Nur extern erhältlich',
      external_link: 'Extern herunterladen',
      external_hint: 'Lade die Datei selbst herunter und lade sie im Tab „Dateien“ nach {{dir}}/ hoch.',
      none_downloadable: 'Keine Version lässt sich direkt installieren.',
      preview_loading: 'Abhängigkeiten werden geprüft …',
      will_install: 'Wird installiert',
      will_install_dep: 'Wird mitinstalliert',
      required_by: 'benötigt von {{title}}',
      already: 'Bereits vorhanden',
      external: 'Manuell nötig',
      incompatible: 'Inkompatibel',
      incompatible_entry: '{{title}}: {{reason}}',
      incompatible_hint: 'Solange etwas inkompatibel ist, lässt sich nichts installieren.',
      nothing: 'Es gibt nichts zu installieren – alles ist bereits vorhanden.',
      submit: 'Installieren',
      started: 'Installation von {{title}} gestartet.',
    },
    incompatible_reason: {
      no_compatible_version: 'keine Version für diese Spielversion und Plattform',
      client_only: 'nur für den Client',
      not_downloadable: 'nur extern erhältlich',
      too_deep: 'zu tief verschachtelte Abhängigkeiten',
      too_many: 'zu viele Abhängigkeiten',
    },
    error: {
      job_running: 'Für diesen Server läuft gerade schon ein Auftrag. Warte, bis er fertig ist.',
      install_conflict: 'Für diesen Server läuft gerade schon ein Auftrag, oder das Projekt ist inzwischen installiert. Prüfe die Liste „Installiert“.',
      too_many_jobs: 'Das panel bearbeitet gerade zu viele Aufträge. Versuche es gleich noch einmal.',
      incompatible: 'Für diese Spielversion gibt es keine passende Version, oder einer Pflicht-Abhängigkeit fehlt sie.',
      not_downloadable: 'Diese Version gibt es nur extern. Lade sie selbst herunter und lade sie im Tab „Dateien“ hoch.',
    },
    job: {
      install_running: 'Installation läuft …',
      install_done: 'Installation abgeschlossen',
      install_failed: 'Installation fehlgeschlagen',
      update_running: 'Aktualisierung läuft …',
      update_done: 'Aktualisierung abgeschlossen',
      update_failed: 'Aktualisierung fehlgeschlagen',
      progress: 'Fortschritt des Auftrags',
      counts: '{{finished}} von {{total}} Jars',
      dismiss: 'Ausblenden',
      toast_done: 'Fertig. Starte den Server neu, damit die Änderungen wirken.',
      toast_failed: 'Nicht alles hat geklappt – Details stehen beim Auftrag.',
    },
    step: {
      state: { pending: 'Wartet', downloading: 'Lädt …', done: 'Fertig', failed: 'Fehler', skipped: 'Übersprungen' },
      failed_reason: 'Fehler: {{reason}}',
      error: {
        download_failed: 'Download fehlgeschlagen.',
        hash_mismatch: 'Prüfsumme stimmt nicht – die Datei wurde verworfen.',
        file_exists: 'Eine andere Datei trägt schon diesen Namen.',
        insufficient_storage: 'Nicht genug freier Speicherplatz.',
        files_unavailable: 'Der Dateidienst ist nicht erreichbar.',
        source_unavailable: 'Modrinth bzw. Hangar ist gerade nicht erreichbar.',
      },
    },
  },
```

In `web/src/i18n/en.ts` an denselben Stellen (`en: Messages` erzwingt dieselben Schlüssel): `permission.server.plugins: 'Plugins/mods',`; `plugins: 'Plugins',` und `mods: 'Mods',` in `servers.detail.tabs`; in `permissions`:

```ts
    plugins_warning: '“Plugins/mods” allows running code inside the Minecraft server. Grant it only to people you fully trust.',
```

und der Block:

```ts
  plugins: {
    unsupported: 'This server type does not support plugins or mods.',
    views: 'View',
    view: { installed: 'Installed', discover: 'Discover' },
    restart: {
      title: 'Restart required for the changes to take effect',
      description: 'Plugins and mods are only loaded when the server starts.',
      now: 'Restart now',
    },
    source: { modrinth: 'Modrinth', hangar: 'Hangar', manual: 'Manual' },
    channel: { release: 'Release', beta: 'Beta', alpha: 'Alpha' },
    installed: {
      title_plugin: 'Installed plugins',
      title_mod: 'Installed mods',
      caption: 'Contents of {{dir}}/',
      empty: 'There are no jars in {{dir}}/ yet.',
      truncated: 'Only the first {{count}} jars are shown.',
      source_errors: 'Modrinth is unreachable right now – matching is incomplete.',
      unmatched: 'Not matched – updates are not possible',
      col: { name: 'Name', version: 'Version', source: 'Source', enabled: 'Enabled', actions: 'Actions' },
      enabled_label: '{{name}} enabled',
      actions_label: 'Actions for {{name}}',
      open_page: 'Open project page',
      change_version: 'Change version …',
      update_to: 'Update to {{version}}',
      update_badge: 'Update: {{version}}',
      remove: 'Remove',
      enabled_done: '“{{name}}” enabled.',
      disabled_done: '“{{name}}” disabled.',
      remove_title: 'Remove “{{name}}”?',
      remove_description: '{{file}} is deleted from {{dir}}/. Configuration files are kept.',
      remove_submit: 'Remove',
      removed: '“{{name}}” removed.',
    },
    updates: {
      check: 'Check for updates',
      checking: 'Checking …',
      update_all: 'Update all ({{count}})',
      checked_at: 'Updates last checked {{when}}.',
      found: 'Updates found: {{count}}',
      none: 'Everything is up to date.',
      failed: 'Updates could not be checked right now.',
      started: 'Update started.',
    },
    discover: {
      title: 'Discover',
      search: 'Search',
      search_placeholder: 'Name or keyword',
      source: 'Source',
      popular: 'Popular on {{source}}',
      results: 'Results on {{source}}',
      empty: 'No matching results.',
      meta: 'by {{author}} · {{downloads}} downloads',
      installed: 'Installed',
      install: 'Install',
      install_label: 'Install {{title}}',
      open_page: 'Project page',
      open_page_label: 'Project page of {{title}} (new window)',
      failed: 'Searching is not possible right now.',
    },
    install: {
      title: 'Install {{title}}',
      description: 'Choose a version. Required dependencies are installed as well.',
      versions: 'Version',
      versions_loading: 'Loading versions …',
      no_versions: 'There is no matching version for this game version.',
      published: 'published {{date}}',
      external_only: 'Only available externally',
      external_link: 'Download externally',
      external_hint: 'Download the file yourself and upload it to {{dir}}/ in the “Files” tab.',
      none_downloadable: 'No version can be installed directly.',
      preview_loading: 'Checking dependencies …',
      will_install: 'Will be installed',
      will_install_dep: 'Installed as dependency',
      required_by: 'required by {{title}}',
      already: 'Already present',
      external: 'Needs manual download',
      incompatible: 'Incompatible',
      incompatible_entry: '{{title}}: {{reason}}',
      incompatible_hint: 'Nothing can be installed while something is incompatible.',
      nothing: 'There is nothing to install – everything is already present.',
      submit: 'Install',
      started: 'Installation of {{title}} started.',
    },
    incompatible_reason: {
      no_compatible_version: 'no version for this game version and platform',
      client_only: 'client only',
      not_downloadable: 'only available externally',
      too_deep: 'dependencies nested too deeply',
      too_many: 'too many dependencies',
    },
    error: {
      job_running: 'A job is already running for this server. Wait until it has finished.',
      install_conflict: 'A job is already running for this server, or the project has been installed meanwhile. Check the “Installed” list.',
      too_many_jobs: 'The panel is processing too many jobs right now. Try again in a moment.',
      incompatible: 'There is no matching version for this game version, or a required dependency has none.',
      not_downloadable: 'This version is only available externally. Download it yourself and upload it in the “Files” tab.',
    },
    job: {
      install_running: 'Installing …',
      install_done: 'Installation finished',
      install_failed: 'Installation failed',
      update_running: 'Updating …',
      update_done: 'Update finished',
      update_failed: 'Update failed',
      progress: 'Job progress',
      counts: '{{finished}} of {{total}} jars',
      dismiss: 'Hide',
      toast_done: 'Done. Restart the server for the changes to take effect.',
      toast_failed: 'Not everything worked – see the job for details.',
    },
    step: {
      state: { pending: 'Waiting', downloading: 'Downloading …', done: 'Done', failed: 'Failed', skipped: 'Skipped' },
      failed_reason: 'Failed: {{reason}}',
      error: {
        download_failed: 'Download failed.',
        hash_mismatch: 'Checksum mismatch – the file was discarded.',
        file_exists: 'Another file already has this name.',
        insufficient_storage: 'Not enough free disk space.',
        files_unavailable: 'The file service is unreachable.',
        source_unavailable: 'Modrinth or Hangar is unreachable right now.',
      },
    },
  },
```

In `web/src/i18n/i18n.test.ts` in `DYNAMIC_KEYS` (vor dem `...['not_configured', …`-Eintrag) ergänzen:

```ts
  ...['modrinth', 'hangar', 'manual'].map((s) => `plugins.source.${s}`),
  ...['release', 'beta', 'alpha'].map((c) => `plugins.channel.${c}`),
  ...['pending', 'downloading', 'done', 'failed', 'skipped'].map((s) => `plugins.step.state.${s}`),
  ...['download_failed', 'hash_mismatch', 'file_exists', 'insufficient_storage', 'files_unavailable', 'source_unavailable'].map(
    (c) => `plugins.step.error.${c}`,
  ),
  ...['no_compatible_version', 'client_only', 'not_downloadable', 'too_deep', 'too_many'].map((r) => `plugins.incompatible_reason.${r}`),
```

(Job-Titel, Banner und Sektionstitel werden mit statischen Schlüsseln gewählt – `t(cond ? … )` vermeiden, damit der Test „defines every static t() key“ sie prüft.)

- [ ] **Step 5: Test-Hilfen und Tests für Liste und Tab schreiben (rot)**

`web/src/features/plugins/test-utils.tsx`:

```tsx
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import type {
  InstalledJar,
  PluginChannel,
  PluginInventory,
  PluginJob,
  PluginJobStep,
  PluginPlanEntry,
  PluginSearchHit,
  PluginUpdate,
  PluginVersion,
} from '@/api/types'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'

const slug = (title: string) => title.toLowerCase().replace(/[^a-z0-9]+/g, '-')

export function jar(file: string, over: Partial<InstalledJar> = {}): InstalledJar {
  return {
    file,
    enabled: !file.toLowerCase().endsWith('.disabled'),
    size: 1024,
    mtime: Date.now() - 60_000,
    source: 'manual',
    project: null,
    version: null,
    meta: null,
    installed_by_panel: false,
    ...over,
  }
}

export function modrinthJar(file: string, title: string, version: string, over: Partial<InstalledJar> = {}): InstalledJar {
  return jar(file, {
    source: 'modrinth',
    project: { id: `p-${slug(title)}`, slug: slug(title), title, page_url: `https://modrinth.com/plugin/${slug(title)}`, icon_url: null },
    version: { id: `v-${version}`, name: version, channel: 'release' },
    installed_by_panel: true,
    ...over,
  })
}

export function hangarJar(file: string, title: string, version: string, channel: PluginChannel = 'release'): InstalledJar {
  return jar(file, {
    source: 'hangar',
    project: { id: '42', slug: title, title, page_url: `https://hangar.papermc.io/ViaVersion/${title}`, icon_url: null },
    version: { id: version, name: version, channel },
    installed_by_panel: true,
  })
}

export function inventory(over: Partial<PluginInventory> = {}): PluginInventory {
  return {
    kind: 'plugin',
    dir: 'plugins',
    game_version: '1.21.11',
    sources: ['modrinth', 'hangar'],
    jars: [],
    truncated: false,
    restart_needed: false,
    source_errors: [],
    ...over,
  }
}

/** Replaces the mock inventory for one test; the update list is empty unless a test overrides it afterwards. */
export function mockInventory(inv: PluginInventory) {
  server.use(
    http.get('*/api/servers/:id/plugins', () => HttpResponse.json(inv)),
    http.get('*/api/servers/:id/plugins/updates', () => HttpResponse.json({ updates: [], checked_at: Date.now() })),
  )
}

export function hit(title: string, over: Partial<PluginSearchHit> = {}): PluginSearchHit {
  return {
    source: 'modrinth',
    project_id: `id-${slug(title)}`,
    slug: slug(title),
    title,
    description: `${title} description`,
    author: 'someone',
    downloads: 1234,
    icon_url: null,
    page_url: `https://modrinth.com/plugin/${slug(title)}`,
    installed: false,
    ...over,
  }
}

export function version(id: string, name: string, channel: PluginChannel = 'release', over: Partial<PluginVersion> = {}): PluginVersion {
  return { id, name, channel, published_at: Date.UTC(2026, 9, 1), downloadable: true, external_url: null, ...over }
}

export function planEntry(title: string, versionName: string, requiredBy: string | null = null): PluginPlanEntry {
  return {
    source: 'modrinth',
    project_id: `id-${slug(title)}`,
    slug: slug(title),
    title,
    version_id: `v-${versionName}`,
    version_name: versionName,
    channel: 'release',
    filename: `${title}-${versionName}.jar`,
    size: 100 * 1024,
    required_by: requiredBy,
  }
}

export function step(title: string, file: string, state: PluginJobStep['state'], errorCode: PluginJobStep['error_code'] = null): PluginJobStep {
  return { title, file, state, error_code: errorCode, error: errorCode ? 'details from the panel' : '' }
}

export function job(kind: PluginJob['kind'], state: PluginJob['state'], steps: PluginJobStep[]): PluginJob {
  return { id: 'job-1', kind, state, steps, started_at: Date.now() - 1000, finished_at: state === 'running' ? 0 : Date.now() }
}

export function update(file: string, from: string, to: string): PluginUpdate {
  return { file, from: { id: `v-${from}`, name: from, channel: 'release' }, to: { id: `v-${to}`, name: to, channel: 'release' } }
}

export async function openPlugins(serverId = 1, username = 'admin', tab: 'Plugins' | 'Mods' = 'Plugins') {
  const user = userEvent.setup()
  await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs(username) })
  await user.click(await screen.findByRole('tab', { name: tab }))
  const region = await screen.findByRole('region', { name: tab === 'Plugins' ? 'Installierte Plugins' : 'Installierte Mods' })
  return { user, region }
}

/** Task 14: the "Entdecken" view of the tab. */
export async function openDiscover(serverId = 1, username = 'admin', tab: 'Plugins' | 'Mods' = 'Plugins') {
  const { user } = await openPlugins(serverId, username, tab)
  await user.click(screen.getByRole('tab', { name: 'Entdecken' }))
  const region = await screen.findByRole('region', { name: 'Entdecken' })
  return { user, region }
}
```

`web/src/features/plugins/plugins-tab.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { db } from '@/mocks/db'
import { server } from '@/mocks/node'
import { setViewport } from '@/test/media'
import { hangarJar, inventory, jar, mockInventory, modrinthJar, openPlugins } from './test-utils'

const OLDTOOL_META = { kind: 'plugin' as const, id: '', name: 'OldTool', version: '1.0' }

describe('PluginsTab – installed', () => {
  it('names jars by project title, jar metadata or file name', async () => {
    mockInventory(
      inventory({
        jars: [
          modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71'),
          hangarJar('ViaVersion-5.6.0-SNAPSHOT.jar', 'ViaVersion', '5.6.0-SNAPSHOT', 'beta'),
          modrinthJar('Disabled.jar.disabled', 'Disabled', '2.0', { enabled: false }),
          jar('OldTool.jar', { meta: OLDTOOL_META }),
          jar('mystery.jar'),
        ],
      }),
    )
    const { region } = await openPlugins()
    expect(await within(region).findByText('LuckPerms')).toBeInTheDocument()
    const row = (name: RegExp) => within(region).getByRole('row', { name })
    expect(within(row(/LuckPerms/)).getByText('5.5.71')).toBeInTheDocument()
    expect(within(row(/LuckPerms/)).getByText('Modrinth')).toBeInTheDocument()
    expect(within(row(/LuckPerms/)).getByText('LuckPerms-Bukkit-5.5.71.jar')).toBeInTheDocument()
    expect(within(row(/ViaVersion/)).getByText('Hangar')).toBeInTheDocument()
    expect(within(row(/ViaVersion/)).getByText('Beta')).toBeInTheDocument()
    expect(within(row(/OldTool/)).getByText('1.0')).toBeInTheDocument()
    expect(within(row(/OldTool/)).getByText('Manuell')).toBeInTheDocument()
    expect(within(row(/mystery/)).getByText('mystery.jar')).toBeInTheDocument()
    expect(within(row(/mystery/)).getByText('—')).toBeInTheDocument()
    expect(within(region).getAllByText('Nicht zugeordnet – Updates nicht möglich')).toHaveLength(2)
    expect(within(region).getByRole('switch', { name: 'LuckPerms aktiv' })).toBeChecked()
    expect(within(region).getByRole('switch', { name: 'Disabled aktiv' })).not.toBeChecked()
  })

  it('switches a jar off and reloads the list', async () => {
    const { user, region } = await openPlugins() // mock inventory V5
    const toggle = await within(region).findByRole('switch', { name: 'OldTool aktiv' })
    expect(toggle).toBeChecked()
    await user.click(toggle)
    expect(await screen.findByText('„OldTool“ deaktiviert.')).toBeInTheDocument()
    await waitFor(() => expect(within(region).getByRole('switch', { name: 'OldTool aktiv' })).not.toBeChecked())
  })

  it('reports a name clash when switching', async () => {
    mockInventory(inventory({ jars: [jar('X.jar.disabled', { meta: { kind: 'plugin', id: '', name: 'X', version: '1' } })] }))
    server.use(http.post('*/api/servers/:id/plugins/toggle', () => HttpResponse.json({ error: 'file_exists', message: 'X.jar exists' }, { status: 409 })))
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('switch', { name: 'X aktiv' }))
    expect(await screen.findByText('Dort existiert bereits eine Datei oder ein Ordner mit diesem Namen.')).toBeInTheDocument()
  })

  it('removes a jar after confirmation', async () => {
    const { user, region } = await openPlugins() // mock inventory V5
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für OldTool' }))
    await user.click(screen.getByRole('menuitem', { name: 'Entfernen' }))
    const dialog = await screen.findByRole('dialog', { name: '„OldTool“ entfernen?' })
    expect(within(dialog).getByText('OldTool.jar wird aus plugins/ gelöscht. Konfigurationsdateien bleiben erhalten.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Entfernen' }))
    expect(await screen.findByText('„OldTool“ entfernt.')).toBeInTheDocument()
    await waitFor(() => expect(within(region).queryByRole('switch', { name: 'OldTool aktiv' })).not.toBeInTheDocument())
  })

  it('opens project pages in a new window without referrer', async () => {
    mockInventory(inventory({ jars: [modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71')] }))
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für LuckPerms' }))
    const link = screen.getByRole('menuitem', { name: 'Projektseite öffnen' })
    expect(link).toHaveAttribute('href', 'https://modrinth.com/plugin/luckperms')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('links no project page for unmatched jars or non-http URLs', async () => {
    mockInventory(
      inventory({
        jars: [
          jar('OldTool.jar', { meta: OLDTOOL_META }),
          modrinthJar('evil.jar', 'Evil', '1', {
            project: { id: 'e', slug: 'evil', title: 'Evil', page_url: 'javascript:alert(1)', icon_url: 'https://evil.example/x.png' },
          }),
        ],
      }),
    )
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für OldTool' }))
    expect(screen.queryByRole('menuitem', { name: 'Projektseite öffnen' })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')
    await user.click(within(region).getByRole('button', { name: 'Aktionen für Evil' }))
    expect(screen.queryByRole('menuitem', { name: 'Projektseite öffnen' })).not.toBeInTheDocument()
    expect(region.querySelector('img')).toBeNull() // the icon is not proxied, so only the placeholder is shown
  })

  it('shows the restart banner and restarts for users with the power permission', async () => {
    mockInventory(inventory({ restart_needed: true }))
    const { user } = await openPlugins()
    expect(await screen.findByText('Neustart nötig, damit die Änderungen wirken')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Jetzt neu starten' }))
    expect(await screen.findByText('Neustart für „survival“ angefordert.')).toBeInTheDocument()
  })

  it('shows no restart button without the power permission', async () => {
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.plugins'] }])
    mockInventory(inventory({ restart_needed: true }))
    await openPlugins(1, 'alice')
    expect(await screen.findByText('Neustart nötig, damit die Änderungen wirken')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Jetzt neu starten' })).not.toBeInTheDocument()
  })

  it('says when Modrinth could not be asked and when the folder is empty', async () => {
    mockInventory(inventory({ source_errors: ['modrinth'] }))
    const { region } = await openPlugins()
    expect(await within(region).findByText('Modrinth gerade nicht erreichbar – Zuordnung unvollständig.')).toBeInTheDocument()
    expect(within(region).getByText('In plugins/ liegen noch keine Jars.')).toBeInTheDocument()
  })

  it('shows cards instead of a table on a phone', async () => {
    setViewport('mobile')
    mockInventory(inventory({ jars: [modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71')] }))
    const { region } = await openPlugins()
    expect(await within(region).findByRole('list', { name: 'Inhalt von plugins/' })).toBeInTheDocument()
    expect(within(region).queryByRole('table')).not.toBeInTheDocument()
    expect(within(region).getByRole('switch', { name: 'LuckPerms aktiv' })).toBeInTheDocument()
  })

  it('reports an unreachable file service', async () => {
    server.use(http.get('*/api/servers/:id/plugins', () => HttpResponse.json({ error: 'files_unavailable', message: 'down' }, { status: 503 })))
    const { region } = await openPlugins()
    expect(await within(region).findByRole('alert')).toHaveTextContent('Der Dateidienst ist nicht erreichbar.')
  })

  it('never shows the plugins_unsupported error code to the user', async () => {
    server.use(http.get('*/api/servers/:id/plugins', () => HttpResponse.json({ error: 'plugins_unsupported', message: 'vanilla' }, { status: 409 })))
    const { region } = await openPlugins()
    expect(await within(region).findByText('Dieser Servertyp unterstützt keine Plugins oder Mods.')).toBeInTheDocument()
    expect(within(region).queryByRole('alert')).not.toBeInTheDocument()
  })
})
```

In `web/src/features/servers/server-detail-page.test.tsx` die Tab-Erwartung im Test „shows status, address, resources and power buttons on the overview“ auf

```ts
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Übersicht',
      'Konsole',
      'Spieler',
      'Dateien',
      'Plugins',
      'Einstellungen',
      'Löschen',
    ])
```

und im Test „lets the tab bar scroll sideways …“ `toHaveLength(6)` auf `toHaveLength(7)` ändern; dazu anhängen (innerhalb von `describe('ServerDetailPage', …)`, vor `describe('when the job finishes', …)`):

```tsx
  it('names the plugins tab after the server type', async () => {
    await renderWithProviders(<ServerDetailPage serverId={4} />, { me: loginAs('admin') })
    expect(await screen.findByRole('tab', { name: 'Mods' })).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Plugins' })).not.toBeInTheDocument()
  })

  it('shows no plugins tab on vanilla servers', async () => {
    await renderWithProviders(<ServerDetailPage serverId={2} />, { me: loginAs('admin') })
    expect(await screen.findByRole('heading', { name: 'creative', level: 1 })).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Plugins' })).not.toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Mods' })).not.toBeInTheDocument()
  })

  it('shows the plugins tab only with the plugins permission', async () => {
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    await screen.findByRole('heading', { name: 'survival', level: 1 })
    expect(screen.queryByRole('tab', { name: 'Plugins' })).not.toBeInTheDocument()
  })

  it('shows the plugins tab to a user with the plugins permission', async () => {
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.plugins'] }])
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    expect(await screen.findByRole('tab', { name: 'Plugins' })).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Dateien' })).not.toBeInTheDocument()
  })
```

Run: `make check` → Expected: FAIL (`./installed-list`, `./plugins-tab`, `PluginsTab` fehlen).

- [ ] **Step 6: Liste und Tab implementieren**

`web/src/features/plugins/installed-list.tsx` (endgültige Fassung; die optionalen Props schalten die Aufgaben 14 und 15 ein):

```tsx
import { MoreHorizontal } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { InstalledJar, PluginInventory, PluginUpdate } from '@/api/types'
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
import { Switch } from '@/components/ui/switch'
import { externalHref, jarName, jarVersion } from './model'
import { PluginIcon } from './plugin-icon'

export interface InstalledListProps {
  inventory: PluginInventory
  /** Task 15: available updates by file name (badge and "Auf … aktualisieren"). */
  updates?: ReadonlyMap<string, PluginUpdate>
  /** Task 15: a job runs – switches and file-changing menu actions are locked (spec §7.1). */
  busy?: boolean
  /** The file whose toggle or removal is in flight. */
  pendingFile?: string | null
  onToggle(jar: InstalledJar, enabled: boolean): void
  onRemove(jar: InstalledJar): void
  /** Task 14: "Version wechseln …" opens the install dialog with the installed version preselected. */
  onChangeVersion?(jar: InstalledJar): void
  /** Task 15: single update. */
  onUpdate?(jar: InstalledJar, update: PluginUpdate): void
}

/** Table on desktop, cards on a phone (DataList), spec §7.1. */
export function InstalledList({ inventory, updates, busy = false, pendingFile = null, onToggle, onRemove, onChangeVersion, onUpdate }: InstalledListProps) {
  const { t } = useTranslation()

  function nameCell(jar: InstalledJar) {
    const name = jarName(jar)
    return (
      <div className="flex min-w-0 items-start gap-2">
        <PluginIcon url={jar.project?.icon_url} />
        <div className="min-w-0">
          <p className="font-medium break-words">{name}</p>
          {name !== jar.file ? <p className="text-muted-foreground font-mono text-xs break-all">{jar.file}</p> : null}
          {jar.source === 'manual' ? <p className="text-muted-foreground text-xs">{t('plugins.installed.unmatched')}</p> : null}
        </div>
      </div>
    )
  }

  function versionCell(jar: InstalledJar) {
    const available = updates?.get(jar.file)
    const channel = jar.version?.channel
    return (
      <div className="flex flex-wrap items-center gap-1">
        <span className="break-all">{jarVersion(jar) ?? '—'}</span>
        {channel && channel !== 'release' ? <Badge variant="outline">{t(`plugins.channel.${channel}`)}</Badge> : null}
        {available ? <Badge>{t('plugins.installed.update_badge', { version: available.to.name })}</Badge> : null}
      </div>
    )
  }

  function enabledCell(jar: InstalledJar) {
    return (
      <Switch
        checked={jar.enabled}
        disabled={busy || pendingFile === jar.file}
        aria-label={t('plugins.installed.enabled_label', { name: jarName(jar) })}
        onCheckedChange={(on) => onToggle(jar, on)}
      />
    )
  }

  function menu(jar: InstalledJar) {
    const name = jarName(jar)
    const page = externalHref(jar.project?.page_url)
    const available = updates?.get(jar.file)
    return (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button type="button" variant="ghost" size="icon-sm" aria-label={t('plugins.installed.actions_label', { name })}>
            <MoreHorizontal aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {page ? (
            <DropdownMenuItem asChild>
              <a href={page} target="_blank" rel="noopener noreferrer">
                {t('plugins.installed.open_page')}
              </a>
            </DropdownMenuItem>
          ) : null}
          {onUpdate && available ? (
            <DropdownMenuItem disabled={busy} onSelect={() => onUpdate(jar, available)}>
              {t('plugins.installed.update_to', { version: available.to.name })}
            </DropdownMenuItem>
          ) : null}
          {onChangeVersion && jar.source !== 'manual' && jar.project ? (
            <DropdownMenuItem disabled={busy} onSelect={() => onChangeVersion(jar)}>
              {t('plugins.installed.change_version')}
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" disabled={busy || pendingFile === jar.file} onSelect={() => onRemove(jar)}>
            {t('plugins.installed.remove')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    )
  }

  const columns: Column<InstalledJar>[] = [
    { id: 'name', header: t('plugins.installed.col.name'), cell: nameCell, className: 'max-w-0 w-full' },
    { id: 'version', header: t('plugins.installed.col.version'), cell: versionCell, className: 'whitespace-nowrap' },
    {
      id: 'source',
      header: t('plugins.installed.col.source'),
      cell: (jar) => <Badge variant="outline">{t(`plugins.source.${jar.source}`)}</Badge>,
    },
    { id: 'enabled', header: t('plugins.installed.col.enabled'), cell: enabledCell, className: 'w-0' },
    { id: 'actions', header: t('plugins.installed.col.actions'), cell: menu, className: 'w-0 text-right' },
  ]

  return (
    <DataList
      items={inventory.jars}
      columns={columns}
      rowKey={(jar) => jar.file}
      caption={t('plugins.installed.caption', { dir: inventory.dir })}
      empty={t('plugins.installed.empty', { dir: inventory.dir })}
    />
  )
}
```

`web/src/features/plugins/plugins-tab.tsx` (Fassung von Aufgabe 13; Aufgabe 14 und 15 ersetzen die Datei vollständig):

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { InstalledJar, PluginKind, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { usePower } from '@/features/servers/use-power'
import { errorMessage } from '@/lib/error-message'
import { deletePlugin, inventoryQueryOptions, refreshPluginState, togglePlugin } from './api'
import { InstalledList } from './installed-list'
import { usePluginErrorToast } from './messages'
import { jarName } from './model'

export interface PluginsTabProps {
  server: Server
  /** From pluginKindOf(instance.type); the detail page shows the tab only when it is not null. */
  kind: PluginKind
}

export function PluginsTab({ server, kind }: PluginsTabProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = usePluginErrorToast()
  const power = usePower(server)
  const [removing, setRemoving] = useState<InstalledJar | null>(null)
  const inventory = useQuery(inventoryQueryOptions(server.id))
  const canPower = server.permissions.includes('server.power')
  const dir = inventory.data?.dir ?? (kind === 'plugin' ? 'plugins' : 'mods')

  const toggle = useMutation({
    mutationFn: ({ jar, enabled }: { jar: InstalledJar; enabled: boolean }) => togglePlugin(server.id, jar.file, enabled),
    onSuccess: (_data, { jar, enabled }) => {
      const name = jarName(jar)
      toast.success(enabled ? t('plugins.installed.enabled_done', { name }) : t('plugins.installed.disabled_done', { name }))
      void refreshPluginState(queryClient, server.id)
    },
    onError: notifyError,
  })

  const remove = useMutation({
    mutationFn: (jar: InstalledJar) => deletePlugin(server.id, jar.file),
    onSuccess: (_data, jar) => {
      toast.success(t('plugins.installed.removed', { name: jarName(jar) }))
      setRemoving(null)
      void refreshPluginState(queryClient, server.id)
    },
    onError: notifyError,
  })

  const pendingFile = toggle.isPending ? (toggle.variables?.jar.file ?? null) : remove.isPending ? (remove.variables?.file ?? null) : null

  function installedBody() {
    if (inventory.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
    if (inventory.isError) {
      // The tab only exists for plugin/mod types; a type changed meanwhile gets a neutral hint, never the error code.
      if (isApiError(inventory.error, 'plugins_unsupported')) return <p className="text-muted-foreground text-sm">{t('plugins.unsupported')}</p>
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(inventory.error, t)}
        </p>
      )
    }
    const inv = inventory.data
    return (
      <div className="space-y-3">
        {inv.source_errors.length > 0 ? (
          <p role="status" className="rounded-md border p-3 text-sm">
            {t('plugins.installed.source_errors')}
          </p>
        ) : null}
        <InstalledList
          inventory={inv}
          pendingFile={pendingFile}
          onToggle={(jar, enabled) => toggle.mutate({ jar, enabled })}
          onRemove={setRemoving}
        />
        {inv.truncated ? <p className="text-muted-foreground text-sm">{t('plugins.installed.truncated', { count: inv.jars.length })}</p> : null}
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {inventory.data?.restart_needed ? (
        <div role="status" className="flex flex-col gap-3 rounded-md border p-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="space-y-1">
            <p className="font-medium">{t('plugins.restart.title')}</p>
            <p className="text-muted-foreground text-sm">{t('plugins.restart.description')}</p>
          </div>
          {canPower ? (
            <Button type="button" disabled={power.isPending} onClick={() => power.mutate('restart')}>
              {t('plugins.restart.now')}
            </Button>
          ) : null}
        </div>
      ) : null}

      <SectionCard title={kind === 'plugin' ? t('plugins.installed.title_plugin') : t('plugins.installed.title_mod')}>{installedBody()}</SectionCard>

      {removing ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => (open ? undefined : setRemoving(null))}
          title={t('plugins.installed.remove_title', { name: jarName(removing) })}
          description={t('plugins.installed.remove_description', { file: removing.file, dir })}
          confirmLabel={t('plugins.installed.remove_submit')}
          destructive
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      ) : null}
    </div>
  )
}
```

- [ ] **Step 7: Tab in der Detailseite**

In `web/src/features/servers/server-detail-page.tsx`:

```tsx
import { pluginKindOf } from '@/features/plugins/model'
import { PluginsTab } from '@/features/plugins/plugins-tab'
```

```tsx
type DetailTab = 'overview' | 'console' | 'players' | 'files' | 'plugins' | 'settings' | 'delete'
```

nach `const canFiles = …`:

```tsx
  // Spec §7.1: paper/purpur → "Plugins", fabric/neoforge → "Mods"; vanilla and modpacks get no tab, so the UI never
  // meets 409 plugins_unsupported. null = no tab (type without plugins/mods or no server.plugins).
  const pluginsKind = server.permissions.includes('server.plugins') ? pluginKindOf(instance.type) : null
```

`available` erweitern (nach der Dateien-Zeile):

```tsx
  if (pluginsKind) available.push('plugins')
```

Den Kommentar über der `TabsList` auf `{/* Up to seven tabs are wider than a phone: the bar scrolls, not the page. */}` ändern; in der `TabsList` nach dem Dateien-Trigger:

```tsx
          {pluginsKind ? (
            <TabsTrigger value="plugins">{pluginsKind === 'mod' ? t('servers.detail.tabs.mods') : t('servers.detail.tabs.plugins')}</TabsTrigger>
          ) : null}
```

und nach dem Dateien-`TabsContent`:

```tsx
        {pluginsKind ? (
          <TabsContent value="plugins" className="pt-4">
            <PluginsTab server={server} kind={pluginsKind} />
          </TabsContent>
        ) : null}
```

(Der Wechsel aus „Dateien“ mit ungespeicherten Änderungen fragt weiter über `changeTab` nach; „plugins“ braucht dort nichts Eigenes.)

- [ ] **Step 8: Prüfen und committen**

Run: `make check` → Expected: PASS. Push, CI: `npx vitest run src/features/plugins src/features/servers/server-detail-page.test.tsx src/i18n` → Expected: PASS.

```bash
git add web/src/features/plugins web/src/features/servers/server-detail-page.tsx web/src/features/servers/server-detail-page.test.tsx web/src/i18n
git commit -m "feat(web): plugins tab with the installed jars, toggles and removal

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 14: Entdecken und Installieren-Dialog

**Files:**
- Create: `web/src/features/plugins/use-debounced-value.ts`, `discover.tsx`, `discover.test.tsx`, `install-dialog.tsx`, `install-dialog.test.tsx`
- Modify: `web/src/features/plugins/plugins-tab.tsx` (vollständig ersetzt), `web/src/features/plugins/plugins-tab.test.tsx`

**Interfaces:**
- Consumes: `searchQueryOptions`, `versionsQueryOptions`, `resolveQueryOptions`, `installPlugin`, `startJob`, `SEARCH_DEBOUNCE_MS`, `MAX_QUERY_LENGTH`, `defaultVersion`, `canInstall`, `externalHref`, `uniqueHits`, `sourcesFor`, `type InstallTarget`, `pluginErrorMessage`, `installErrorMessage`, `PluginIcon`, `formatBytes` (`@/features/files/format`), `useFormat`.
- Produces: `useDebouncedValue(value, delayMs)`, `Discover({ serverId, sources, busy, onInstall })`, `InstallDialog({ serverId, dir, target, busy, onClose, onStarted })`; der Tab hat die Ansichten „Installiert“ | „Entdecken“ und „Version wechseln …“.

Abgestimmt mit plan-panel (B2): Fordert `resolve`/`install` für ein schon installiertes Projekt explizit eine **andere** Version an, steht es in `install` (`required_by: null`, neuer Dateiname) und der Job ersetzt das alte Jar; bei gleicher Version steht es in `already`. Der Dialog wählt beim Versionswechsel die installierte Version vor und zeigt dann „Es gibt nichts zu installieren“, bis eine andere gewählt ist.

- [ ] **Step 1: Tests schreiben (rot)**

`web/src/features/plugins/discover.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { PluginSearchHit, PluginSource } from '@/api/types'
import { server } from '@/mocks/node'
import { hit, openDiscover } from './test-utils'

/** Answers searches from a fixed list (20 per page, substring filter like the mock) and records what was asked. */
function searchMock(all: (source: PluginSource) => PluginSearchHit[]) {
  const calls: { source: string | null; q: string | null; offset: string | null }[] = []
  server.use(
    http.get('*/api/servers/:id/plugins/search', ({ request }) => {
      const url = new URL(request.url)
      const source = url.searchParams.get('source') as PluginSource
      const q = url.searchParams.get('q')
      const offset = Number(url.searchParams.get('offset') ?? '0')
      calls.push({ source, q, offset: url.searchParams.get('offset') })
      const list = all(source).filter((h) => !q || h.title.toLowerCase().includes(q.toLowerCase()))
      return HttpResponse.json({ hits: list.slice(offset, offset + 20), total: list.length, offset })
    }),
  )
  return calls
}

describe('Discover', () => {
  it('shows popular projects without a search term and switches the source', async () => {
    const calls = searchMock((source) => [hit(source === 'hangar' ? 'ViaBackwards' : 'LuckPerms', { source })])
    const { user, region } = await openDiscover()
    expect(await within(region).findByRole('heading', { name: 'LuckPerms' })).toBeInTheDocument()
    expect(within(region).getByRole('heading', { name: 'Beliebt auf Modrinth' })).toBeInTheDocument()
    const sources = within(region).getByRole('group', { name: 'Quelle' })
    expect(within(sources).getByRole('button', { name: 'Modrinth' })).toHaveAttribute('aria-pressed', 'true')
    await user.click(within(sources).getByRole('button', { name: 'Hangar' }))
    expect(await within(region).findByRole('heading', { name: 'ViaBackwards' })).toBeInTheDocument()
    expect(within(region).getByRole('heading', { name: 'Beliebt auf Hangar' })).toBeInTheDocument()
    expect(calls.map((c) => [c.source, c.q, c.offset])).toEqual([
      ['modrinth', null, null],
      ['hangar', null, null],
    ])
  })

  it('searches after a typing pause instead of on every key', async () => {
    const calls = searchMock(() => [hit('ViaVersion'), hit('ViaBackwards'), hit('LuckPerms')])
    const { user, region } = await openDiscover()
    await within(region).findByRole('heading', { name: 'LuckPerms' })
    await user.type(within(region).getByLabelText('Suchen'), 'via')
    expect(await within(region).findByRole('heading', { name: 'Treffer auf Modrinth' })).toBeInTheDocument()
    await waitFor(() => expect(within(region).queryByRole('heading', { name: 'LuckPerms' })).not.toBeInTheDocument())
    expect(within(region).getByRole('heading', { name: 'ViaBackwards' })).toBeInTheDocument()
    expect(calls.map((c) => c.q)).toEqual([null, 'via'])
  })

  it('loads more results with the offset', async () => {
    const calls = searchMock(() => Array.from({ length: 25 }, (_, i) => hit(`Plugin ${i + 1}`)))
    const { user, region } = await openDiscover()
    expect(await within(region).findByRole('heading', { name: 'Plugin 20' })).toBeInTheDocument()
    expect(within(region).queryByRole('heading', { name: 'Plugin 21' })).not.toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Mehr laden' }))
    expect(await within(region).findByRole('heading', { name: 'Plugin 25' })).toBeInTheDocument()
    expect(within(region).queryByRole('button', { name: 'Mehr laden' })).not.toBeInTheDocument()
    expect(calls.map((c) => c.offset)).toEqual([null, '20'])
  })

  it('marks installed projects and opens the install dialog for the others', async () => {
    searchMock(() => [hit('LuckPerms', { installed: true }), hit('Chunky')])
    server.use(http.get('*/api/servers/:id/plugins/versions', () => HttpResponse.json({ versions: [] })))
    const { user, region } = await openDiscover()
    const luckperms = (await within(region).findByRole('heading', { name: 'LuckPerms' })).closest('li') as HTMLElement
    expect(within(luckperms).getByText('Installiert')).toBeInTheDocument()
    expect(within(luckperms).queryByRole('button', { name: 'LuckPerms installieren' })).not.toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Chunky installieren' }))
    const dialog = await screen.findByRole('dialog', { name: 'Chunky installieren' })
    expect(await within(dialog).findByText('Für diese Spielversion gibt es keine passende Version.')).toBeInTheDocument()
  })

  it('shows descriptions as plain text and links project pages safely', async () => {
    searchMock(() => [hit('Chunky', { description: '<b>fett</b> **kein Markdown**' })])
    const { region } = await openDiscover()
    expect(await within(region).findByText('<b>fett</b> **kein Markdown**')).toBeInTheDocument()
    expect(region.querySelector('b')).toBeNull()
    const link = within(region).getByRole('link', { name: 'Projektseite von Chunky (neues Fenster)' })
    expect(link).toHaveAttribute('href', 'https://modrinth.com/plugin/chunky')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('offers only Modrinth for mods', async () => {
    searchMock(() => [hit('Lithium', { page_url: 'https://modrinth.com/mod/lithium' })])
    const { region } = await openDiscover(4, 'admin', 'Mods')
    expect(await within(region).findByRole('heading', { name: 'Beliebt auf Modrinth' })).toBeInTheDocument()
    expect(within(region).queryByRole('group', { name: 'Quelle' })).not.toBeInTheDocument()
    expect(within(region).queryByRole('button', { name: 'Hangar' })).not.toBeInTheDocument()
  })

  it('offers a retry when the source is unreachable', async () => {
    let fail = true
    server.use(
      http.get('*/api/servers/:id/plugins/search', () =>
        fail
          ? HttpResponse.json({ error: 'plugin_source_unavailable', message: 'modrinth: 503' }, { status: 502 })
          : HttpResponse.json({ hits: [hit('Chunky')], total: 1, offset: 0 }),
      ),
    )
    const { user, region } = await openDiscover()
    const alert = await within(region).findByRole('alert')
    expect(alert).toHaveTextContent('Die Suche ist gerade nicht möglich.')
    fail = false
    await user.click(within(alert).getByRole('button', { name: 'Erneut versuchen' }))
    expect(await within(region).findByRole('heading', { name: 'Chunky' })).toBeInTheDocument()
  })
})
```

`web/src/features/plugins/install-dialog.test.tsx`:

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { PluginResolution, PluginVersion } from '@/api/types'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { InstallDialog } from './install-dialog'
import type { InstallTarget } from './model'
import { job, planEntry, version } from './test-utils'

const VERSIONS: PluginVersion[] = [
  version('v3', '3.0-beta', 'beta'),
  version('v2', '2.1'),
  version('v1', '2.0', 'release', { downloadable: false, external_url: 'https://example.org/chunkyborder-2.0.jar' }),
]
const TARGET: InstallTarget = { source: 'modrinth', project: 'chunkyborder', title: 'ChunkyBorder' }

function resolution(over: Partial<PluginResolution> = {}): PluginResolution {
  return { install: [planEntry('ChunkyBorder', '2.1')], already: [], external: [], incompatible: [], ...over }
}

/** Versions and resolve for the dialog; returns the versions that were resolved. */
function mockUpstream(answer: PluginResolution | (() => Response), versions: PluginVersion[] = VERSIONS) {
  const resolved: (string | null)[] = []
  server.use(
    http.get('*/api/servers/:id/plugins/versions', () => HttpResponse.json({ versions })),
    http.get('*/api/servers/:id/plugins/resolve', ({ request }) => {
      resolved.push(new URL(request.url).searchParams.get('version'))
      return typeof answer === 'function' ? answer() : HttpResponse.json(answer)
    }),
  )
  return resolved
}

async function openDialog(target: InstallTarget = TARGET, busy = false) {
  const user = userEvent.setup()
  const onStarted = vi.fn()
  const onClose = vi.fn()
  await renderWithProviders(
    <InstallDialog serverId={1} dir="plugins" target={target} busy={busy} onClose={onClose} onStarted={onStarted} />,
    { me: loginAs('admin') },
  )
  const dialog = await screen.findByRole('dialog', { name: `${target.title} installieren` })
  return { user, dialog, onStarted, onClose }
}

describe('InstallDialog', () => {
  it('preselects the newest release, marks pre-releases and greys out external versions', async () => {
    const resolved = mockUpstream(resolution())
    const { dialog } = await openDialog()
    expect(await within(dialog).findByRole('radio', { name: '2.1' })).toBeChecked()
    expect(within(dialog).getByRole('radio', { name: '3.0-beta (Beta)' })).not.toBeChecked()
    expect(within(dialog).getByRole('radio', { name: '2.0' })).toBeDisabled()
    expect(within(dialog).getByText('Nur extern erhältlich')).toBeInTheDocument()
    const link = within(dialog).getByRole('link', { name: 'Extern herunterladen' })
    expect(link).toHaveAttribute('href', 'https://example.org/chunkyborder-2.0.jar')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(within(dialog).getByText('Lade die Datei selbst herunter und lade sie im Tab „Dateien“ nach plugins/ hoch.')).toBeInTheDocument()
    await waitFor(() => expect(resolved).toEqual(['v2']))
  })

  it('previews dependencies, present and manual ones and installs the chosen version', async () => {
    mockUpstream(
      resolution({
        install: [planEntry('Chunky', '1.4.28', 'ChunkyBorder'), planEntry('ChunkyBorder', '2.1')],
        already: [{ source: 'hangar', project_id: '42', title: 'ViaVersion', file: 'ViaVersion-5.5.1.jar' }],
        external: [{ name: 'ProtocolLib', url: 'https://example.org/protocollib' }],
      }),
    )
    let body: unknown = null
    server.use(
      http.post('*/api/servers/:id/plugins/install', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(job('install', 'running', []), { status: 202 })
      }),
    )
    const { user, dialog, onStarted } = await openDialog()
    const deps = await within(dialog).findByRole('region', { name: 'Wird mitinstalliert' })
    expect(within(deps).getByText('Chunky')).toBeInTheDocument()
    expect(within(deps).getByText('benötigt von ChunkyBorder')).toBeInTheDocument()
    expect(within(within(dialog).getByRole('region', { name: 'Wird installiert' })).getByText('ChunkyBorder')).toBeInTheDocument()
    expect(within(within(dialog).getByRole('region', { name: 'Bereits vorhanden' })).getByText('ViaVersion')).toBeInTheDocument()
    const manual = within(dialog).getByRole('region', { name: 'Manuell nötig' })
    expect(within(manual).getByRole('link', { name: 'ProtocolLib' })).toHaveAttribute('href', 'https://example.org/protocollib')
    await user.click(within(dialog).getByRole('button', { name: 'Installieren' }))
    await waitFor(() => expect(onStarted).toHaveBeenCalledWith(expect.objectContaining({ kind: 'install', state: 'running' })))
    expect(body).toEqual({ source: 'modrinth', project: 'chunkyborder', version: 'v2' })
  })

  it('blocks installing while a dependency is incompatible', async () => {
    mockUpstream(resolution({ incompatible: [{ title: 'Sodium Extra', reason: 'client_only' }] }))
    const { dialog } = await openDialog()
    const incompatible = await within(dialog).findByRole('region', { name: 'Inkompatibel' })
    expect(within(incompatible).getByText('Sodium Extra: nur für den Client')).toBeInTheDocument()
    expect(within(dialog).getByText('Solange etwas inkompatibel ist, lässt sich nichts installieren.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Installieren' })).toBeDisabled()
  })

  it('explains a version that is only available externally', async () => {
    mockUpstream(() => HttpResponse.json({ error: 'plugin_not_downloadable', message: 'external only' }, { status: 409 }))
    const { dialog } = await openDialog()
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Diese Version gibt es nur extern.')
    expect(within(dialog).getByRole('button', { name: 'Installieren' })).toBeDisabled()
  })

  it('says when another job is running or the project got installed meanwhile', async () => {
    mockUpstream(resolution())
    server.use(http.post('*/api/servers/:id/plugins/install', () => HttpResponse.json({ error: 'conflict', message: 'job running' }, { status: 409 })))
    const { user, dialog, onStarted } = await openDialog()
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Installieren' })).toBeEnabled())
    await user.click(within(dialog).getByRole('button', { name: 'Installieren' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Für diesen Server läuft gerade schon ein Auftrag, oder das Projekt ist inzwischen installiert.')
    expect(onStarted).not.toHaveBeenCalled()
  })

  it('names the job limit of the panel', async () => {
    mockUpstream(resolution())
    server.use(http.post('*/api/servers/:id/plugins/install', () => HttpResponse.json({ error: 'rate_limited', message: 'jobs' }, { status: 429 })))
    const { user, dialog } = await openDialog()
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Installieren' })).toBeEnabled())
    await user.click(within(dialog).getByRole('button', { name: 'Installieren' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Das panel bearbeitet gerade zu viele Aufträge.')
  })

  it('keeps a preselected version and resolves again for another one', async () => {
    const resolved = mockUpstream(resolution())
    const { user, dialog } = await openDialog({ ...TARGET, version: 'v3' })
    expect(await within(dialog).findByRole('radio', { name: '3.0-beta (Beta)' })).toBeChecked()
    await user.click(within(dialog).getByRole('radio', { name: '2.1' }))
    await waitFor(() => expect(resolved).toEqual(['v3', 'v2']))
  })

  it('locks installing while a job runs', async () => {
    mockUpstream(resolution())
    const { dialog } = await openDialog(TARGET, true)
    await within(dialog).findByRole('region', { name: 'Wird installiert' })
    expect(within(dialog).getByRole('button', { name: 'Installieren' })).toBeDisabled()
  })

  it('explains a project without downloadable versions', async () => {
    const resolved = mockUpstream(resolution(), [
      version('e1', '2.21.0', 'release', { downloadable: false, external_url: 'https://example.org/essentials.jar' }),
    ])
    const { dialog } = await openDialog({ source: 'hangar', project: 'Essentials', title: 'Essentials' })
    expect(await within(dialog).findByText('Keine Version lässt sich direkt installieren.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Installieren' })).toBeDisabled()
    expect(resolved).toEqual([])
  })

  it('says when there is nothing to install', async () => {
    mockUpstream(resolution({ install: [], already: [{ source: 'modrinth', project_id: 'id-chunkyborder', title: 'ChunkyBorder', file: 'ChunkyBorder-2.1.jar' }] }))
    const { dialog } = await openDialog()
    expect(await within(dialog).findByText('Es gibt nichts zu installieren – alles ist bereits vorhanden.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Installieren' })).toBeDisabled()
  })
})
```

In `web/src/features/plugins/plugins-tab.test.tsx` Importe um `version`, `planEntry` (aus `./test-utils`) ergänzen und anhängen:

```tsx
describe('PluginsTab – views', () => {
  it('changes the version of an installed jar through the install dialog', async () => {
    mockInventory(inventory({ jars: [modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71')] }))
    server.use(
      http.get('*/api/servers/:id/plugins/versions', () => HttpResponse.json({ versions: [version('v-5.5.72', '5.5.72'), version('v-5.5.71', '5.5.71')] })),
      http.get('*/api/servers/:id/plugins/resolve', () =>
        HttpResponse.json({ install: [planEntry('LuckPerms', '5.5.72')], already: [], external: [], incompatible: [] }),
      ),
    )
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für LuckPerms' }))
    await user.click(screen.getByRole('menuitem', { name: 'Version wechseln …' }))
    const dialog = await screen.findByRole('dialog', { name: 'LuckPerms installieren' })
    expect(await within(dialog).findByRole('radio', { name: '5.5.71' })).toBeChecked()
  })

  it('offers no version change for unmatched jars', async () => {
    mockInventory(inventory({ jars: [jar('OldTool.jar', { meta: OLDTOOL_META })] }))
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für OldTool' }))
    expect(screen.queryByRole('menuitem', { name: 'Version wechseln …' })).not.toBeInTheDocument()
  })

  it('keeps the installed list usable while the search fails', async () => {
    server.use(
      http.get('*/api/servers/:id/plugins/search', () => HttpResponse.json({ error: 'plugin_source_unavailable', message: 'down' }, { status: 502 })),
    )
    const { user, region } = await openPlugins() // mock inventory V5
    await within(region).findByRole('switch', { name: 'OldTool aktiv' })
    await user.click(screen.getByRole('tab', { name: 'Entdecken' }))
    expect(await screen.findByText('Die Suche ist gerade nicht möglich.')).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Installiert' }))
    expect(await screen.findByRole('switch', { name: 'OldTool aktiv' })).toBeEnabled()
  })
})
```

Run: `make check` → Expected: FAIL (`./discover`, `./install-dialog` fehlen).

- [ ] **Step 2: Verzögerter Suchbegriff**

`web/src/features/plugins/use-debounced-value.ts`:

```ts
import { useEffect, useState } from 'react'

/** The value after delayMs without a change: one search per typing pause instead of one per key (60/min limit). */
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(timer)
  }, [value, delayMs])
  return debounced
}
```

- [ ] **Step 3: Entdecken**

`web/src/features/plugins/discover.tsx`:

```tsx
import { useInfiniteQuery } from '@tanstack/react-query'
import { ExternalLink } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PluginSearchHit, PluginSource } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useFormat } from '@/lib/format'
import { MAX_QUERY_LENGTH, SEARCH_DEBOUNCE_MS, searchQueryOptions } from './api'
import { pluginErrorMessage } from './messages'
import { externalHref, uniqueHits, type InstallTarget } from './model'
import { PluginIcon } from './plugin-icon'
import { useDebouncedValue } from './use-debounced-value'

export interface DiscoverProps {
  serverId: number
  /** From the inventory: Modrinth and Hangar for plugins, only Modrinth for mods (spec §3.1). */
  sources: readonly PluginSource[]
  /** A job runs: "Installieren" is locked (spec §7.1). */
  busy: boolean
  onInstall(target: InstallTarget): void
}

export function Discover({ serverId, sources, busy, onInstall }: DiscoverProps) {
  const { t } = useTranslation()
  const inputId = useId()
  const [chosen, setChosen] = useState<PluginSource>(sources[0] ?? 'modrinth')
  const source = sources.includes(chosen) ? chosen : (sources[0] ?? 'modrinth')
  const [text, setText] = useState('')
  const q = useDebouncedValue(text.trim(), SEARCH_DEBOUNCE_MS)
  const search = useInfiniteQuery(searchQueryOptions(serverId, source, q))
  const hits = uniqueHits(search.data?.pages.flatMap((page) => page.hits) ?? [])
  const sourceName = t(`plugins.source.${source}`)

  function results() {
    if (search.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
    if (search.isError && hits.length === 0) {
      return (
        <div role="alert" className="space-y-2 rounded-md border p-3 text-sm">
          <p className="font-medium">{t('plugins.discover.failed')}</p>
          <p className="text-muted-foreground">{pluginErrorMessage(search.error, t)}</p>
          <Button type="button" variant="outline" size="sm" disabled={search.isFetching} onClick={() => void search.refetch()}>
            {t('common.retry')}
          </Button>
        </div>
      )
    }
    if (hits.length === 0) return <p className="text-muted-foreground text-sm">{t('plugins.discover.empty')}</p>
    return (
      <div className="space-y-3">
        <ul className="space-y-3">
          {hits.map((hit) => (
            <HitCard key={`${hit.source}:${hit.project_id}`} hit={hit} busy={busy} onInstall={onInstall} />
          ))}
        </ul>
        {search.isFetchNextPageError ? (
          <div role="alert" className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-destructive">{pluginErrorMessage(search.error, t)}</span>
            <Button type="button" variant="outline" size="sm" onClick={() => void search.fetchNextPage()}>
              {t('common.retry')}
            </Button>
          </div>
        ) : null}
        {search.hasNextPage && !search.isFetchNextPageError ? (
          <Button type="button" variant="outline" disabled={search.isFetchingNextPage} onClick={() => void search.fetchNextPage()}>
            {t('common.load_more')}
          </Button>
        ) : null}
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
        <div className="min-w-0 flex-1 space-y-2">
          <Label htmlFor={inputId}>{t('plugins.discover.search')}</Label>
          <Input
            id={inputId}
            type="search"
            value={text}
            maxLength={MAX_QUERY_LENGTH}
            placeholder={t('plugins.discover.search_placeholder')}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setText(e.target.value)}
          />
        </div>
        {sources.length > 1 ? (
          <div role="group" aria-label={t('plugins.discover.source')} className="flex gap-1">
            {sources.map((s) => (
              <Button key={s} type="button" size="sm" variant={s === source ? 'secondary' : 'outline'} aria-pressed={s === source} onClick={() => setChosen(s)}>
                {t(`plugins.source.${s}`)}
              </Button>
            ))}
          </div>
        ) : null}
      </div>
      <h3 className="text-sm font-medium">
        {q === '' ? t('plugins.discover.popular', { source: sourceName }) : t('plugins.discover.results', { source: sourceName })}
      </h3>
      {results()}
    </div>
  )
}

function HitCard({ hit, busy, onInstall }: { hit: PluginSearchHit; busy: boolean; onInstall(target: InstallTarget): void }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const page = externalHref(hit.page_url)
  return (
    <li className="flex gap-3 rounded-md border p-3">
      <PluginIcon url={hit.icon_url} />
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <h4 className="font-medium break-words">{hit.title}</h4>
          {hit.installed ? <Badge variant="secondary">{t('plugins.discover.installed')}</Badge> : null}
        </div>
        <p className="text-muted-foreground text-xs">{t('plugins.discover.meta', { author: hit.author, downloads: fmt.number(hit.downloads) })}</p>
        {/* Spec §7.1: only the short description, as plain text – never Markdown or HTML from the upstream. */}
        <p className="line-clamp-3 text-sm break-words">{hit.description}</p>
        <div className="flex flex-wrap items-center gap-3 pt-1">
          {!hit.installed ? (
            <Button
              type="button"
              size="sm"
              disabled={busy}
              aria-label={t('plugins.discover.install_label', { title: hit.title })}
              onClick={() => onInstall({ source: hit.source, project: hit.project_id, title: hit.title })}
            >
              {t('plugins.discover.install')}
            </Button>
          ) : null}
          {page ? (
            <a
              href={page}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={t('plugins.discover.open_page_label', { title: hit.title })}
              className="text-primary inline-flex items-center gap-1 text-sm underline-offset-4 hover:underline"
            >
              {t('plugins.discover.open_page')}
              <ExternalLink aria-hidden="true" className="size-3.5" />
            </a>
          ) : null}
        </div>
      </div>
    </li>
  )
}
```

- [ ] **Step 4: Installieren-Dialog**

`web/src/features/plugins/install-dialog.tsx`:

```tsx
import { useMutation, useQuery } from '@tanstack/react-query'
import { ExternalLink } from 'lucide-react'
import { useId, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { PluginJob, PluginPlanEntry } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { formatBytes } from '@/features/files/format'
import { useFormat } from '@/lib/format'
import { installPlugin, resolveQueryOptions, versionsQueryOptions } from './api'
import { installErrorMessage, pluginErrorMessage } from './messages'
import { canInstall, defaultVersion, externalHref, type InstallTarget } from './model'

export interface InstallDialogProps {
  serverId: number
  /** "plugins" or "mods", for the hint on externally available versions. */
  dir: string
  target: InstallTarget
  /** A job runs on this server: installing is locked (spec §7.1). */
  busy: boolean
  onClose(): void
  /** Called with the 202 job; the caller starts the polling and closes the dialog. */
  onStarted(job: PluginJob): void
}

function PreviewSection({ title, children }: { title: string; children: ReactNode }) {
  const id = useId()
  return (
    <section aria-labelledby={id} className="space-y-1">
      <h3 id={id} className="text-sm font-medium">
        {title}
      </h3>
      <ul className="space-y-1.5 text-sm">{children}</ul>
    </section>
  )
}

/** Spec §7.1: version choice (default newest release) and the preview from resolve; the job re-resolves anyway (§6.4). */
export function InstallDialog({ serverId, dir, target, busy, onClose, onStarted }: InstallDialogProps) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const group = useId()
  const [chosen, setChosen] = useState<string | null>(null)
  const versions = useQuery(versionsQueryOptions(serverId, target.source, target.project))
  const list = versions.data?.versions ?? []
  const selected = chosen ?? defaultVersion(list, target.version)
  const resolve = useQuery({ ...resolveQueryOptions(serverId, target.source, target.project, selected ?? ''), enabled: selected !== null })
  const install = useMutation({
    mutationFn: (version: string) => installPlugin(serverId, { source: target.source, project: target.project, version }),
    onSuccess: (job) => onStarted(job),
  })
  const plan = resolve.data
  const blocked = busy || selected === null || !plan || !canInstall(plan) || install.isPending

  function entry(e: PluginPlanEntry) {
    return (
      <li key={`${e.source}:${e.project_id}`} className="min-w-0">
        <span className="font-medium">{e.title}</span> <span className="text-muted-foreground">{e.version_name}</span>
        {e.channel !== 'release' ? (
          <Badge variant="outline" className="ml-1">
            {t(`plugins.channel.${e.channel}`)}
          </Badge>
        ) : null}
        <span className="text-muted-foreground block font-mono text-xs break-all">
          {e.filename} · {formatBytes(e.size, fmt.locale)}
        </span>
        {e.required_by !== null ? <span className="text-muted-foreground block text-xs">{t('plugins.install.required_by', { title: e.required_by })}</span> : null}
      </li>
    )
  }

  function versionList() {
    if (versions.isPending) return <p className="text-muted-foreground text-sm">{t('plugins.install.versions_loading')}</p>
    if (versions.isError) {
      return (
        <div role="alert" className="space-y-2 text-sm">
          <p className="text-destructive">{pluginErrorMessage(versions.error, t)}</p>
          <Button type="button" variant="outline" size="sm" disabled={versions.isFetching} onClick={() => void versions.refetch()}>
            {t('common.retry')}
          </Button>
        </div>
      )
    }
    if (list.length === 0) return <p className="text-muted-foreground text-sm">{t('plugins.install.no_versions')}</p>
    const external = list.some((v) => !v.downloadable)
    return (
      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">{t('plugins.install.versions')}</legend>
        <ul className="max-h-60 space-y-2 overflow-y-auto rounded-md border p-2">
          {list.map((v) => {
            const href = v.downloadable ? null : externalHref(v.external_url)
            const label = v.channel === 'release' ? v.name : `${v.name} (${t(`plugins.channel.${v.channel}`)})`
            return (
              <li key={v.id} className={v.downloadable ? 'text-sm' : 'text-muted-foreground text-sm'}>
                <label className="flex items-start gap-2">
                  <input
                    type="radio"
                    name={group}
                    value={v.id}
                    className="accent-primary mt-0.5 size-4"
                    aria-label={label}
                    checked={selected === v.id}
                    disabled={!v.downloadable}
                    onChange={() => {
                      install.reset()
                      setChosen(v.id)
                    }}
                  />
                  <span className="min-w-0 flex-1">
                    <span className="font-medium break-all">{v.name}</span>
                    {v.channel !== 'release' ? (
                      <Badge variant="outline" className="ml-2">
                        {t(`plugins.channel.${v.channel}`)}
                      </Badge>
                    ) : null}
                    <span className="block text-xs">{t('plugins.install.published', { date: fmt.date(v.published_at) })}</span>
                    {!v.downloadable ? <span className="block text-xs">{t('plugins.install.external_only')}</span> : null}
                  </span>
                </label>
                {href ? (
                  <a href={href} target="_blank" rel="noopener noreferrer" className="text-primary ml-6 inline-flex items-center gap-1 text-xs underline-offset-4 hover:underline">
                    {t('plugins.install.external_link')}
                    <ExternalLink aria-hidden="true" className="size-3" />
                  </a>
                ) : null}
              </li>
            )
          })}
        </ul>
        {list.every((v) => !v.downloadable) ? <p className="text-sm">{t('plugins.install.none_downloadable')}</p> : null}
        {external ? <p className="text-muted-foreground text-xs">{t('plugins.install.external_hint', { dir })}</p> : null}
      </fieldset>
    )
  }

  function preview() {
    if (selected === null) return null
    if (resolve.isError) {
      return (
        <p role="alert" className="text-destructive text-sm">
          {pluginErrorMessage(resolve.error, t)}
        </p>
      )
    }
    if (!plan) return <p className="text-muted-foreground text-sm">{t('plugins.install.preview_loading')}</p>
    const main = plan.install.filter((e) => e.required_by === null)
    const deps = plan.install.filter((e) => e.required_by !== null)
    return (
      <div className="space-y-3">
        {main.length > 0 ? <PreviewSection title={t('plugins.install.will_install')}>{main.map(entry)}</PreviewSection> : null}
        {deps.length > 0 ? <PreviewSection title={t('plugins.install.will_install_dep')}>{deps.map(entry)}</PreviewSection> : null}
        {plan.already.length > 0 ? (
          <PreviewSection title={t('plugins.install.already')}>
            {plan.already.map((a) => (
              <li key={`${a.source}:${a.project_id}`}>
                <span className="font-medium">{a.title}</span>
                <span className="text-muted-foreground block font-mono text-xs break-all">{a.file}</span>
              </li>
            ))}
          </PreviewSection>
        ) : null}
        {plan.external.length > 0 ? (
          <PreviewSection title={t('plugins.install.external')}>
            {plan.external.map((x) => {
              const href = externalHref(x.url)
              return (
                <li key={`${x.name}:${x.url}`}>
                  {href ? (
                    <a href={href} target="_blank" rel="noopener noreferrer" className="text-primary inline-flex items-center gap-1 underline-offset-4 hover:underline">
                      {x.name}
                      <ExternalLink aria-hidden="true" className="size-3.5" />
                    </a>
                  ) : (
                    <span>{x.name}</span>
                  )}
                </li>
              )
            })}
          </PreviewSection>
        ) : null}
        {plan.incompatible.length > 0 ? (
          <>
            <PreviewSection title={t('plugins.install.incompatible')}>
              {plan.incompatible.map((x) => (
                <li key={`${x.title}:${x.reason}`}>{t('plugins.install.incompatible_entry', { title: x.title, reason: t(`plugins.incompatible_reason.${x.reason}`) })}</li>
              ))}
            </PreviewSection>
            <p role="alert" className="text-destructive text-sm">
              {t('plugins.install.incompatible_hint')}
            </p>
          </>
        ) : null}
        {plan.incompatible.length === 0 && plan.install.length === 0 ? <p className="text-muted-foreground text-sm">{t('plugins.install.nothing')}</p> : null}
      </div>
    )
  }

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('plugins.install.title', { title: target.title })}</DialogTitle>
          <DialogDescription>{t('plugins.install.description')}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          {versionList()}
          {preview()}
          {install.isError ? (
            <p role="alert" className="text-destructive text-sm">
              {installErrorMessage(install.error, t)}
            </p>
          ) : null}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button type="button" disabled={blocked} onClick={() => selected !== null && install.mutate(selected)}>
            {t('plugins.install.submit')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
```

Hinweise: `PreviewSection` ist eine nicht exportierte Komponente (React-Refresh-Regel bleibt erfüllt). Der Radio-Name kommt aus `aria-label` (Name plus Kanal), damit Tests und Screenreader „3.0-beta (Beta)“ hören statt zusammengeklebter Spans.

- [ ] **Step 5: Ansichten im Tab**

`web/src/features/plugins/plugins-tab.tsx` vollständig ersetzen durch:

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { InstalledJar, PluginKind, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SectionCard } from '@/features/profile/section-card'
import { usePower } from '@/features/servers/use-power'
import { errorMessage } from '@/lib/error-message'
import { deletePlugin, inventoryQueryOptions, refreshPluginState, startJob, togglePlugin } from './api'
import { Discover } from './discover'
import { InstallDialog } from './install-dialog'
import { InstalledList } from './installed-list'
import { usePluginErrorToast } from './messages'
import { jarName, sourcesFor, type InstallTarget } from './model'

type View = 'installed' | 'discover'

export interface PluginsTabProps {
  server: Server
  /** From pluginKindOf(instance.type); the detail page shows the tab only when it is not null. */
  kind: PluginKind
}

export function PluginsTab({ server, kind }: PluginsTabProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = usePluginErrorToast()
  const power = usePower(server)
  const [view, setView] = useState<View>('installed')
  const [installing, setInstalling] = useState<InstallTarget | null>(null)
  const [removing, setRemoving] = useState<InstalledJar | null>(null)
  const inventory = useQuery(inventoryQueryOptions(server.id))
  const canPower = server.permissions.includes('server.power')
  const dir = inventory.data?.dir ?? (kind === 'plugin' ? 'plugins' : 'mods')

  const toggle = useMutation({
    mutationFn: ({ jar, enabled }: { jar: InstalledJar; enabled: boolean }) => togglePlugin(server.id, jar.file, enabled),
    onSuccess: (_data, { jar, enabled }) => {
      const name = jarName(jar)
      toast.success(enabled ? t('plugins.installed.enabled_done', { name }) : t('plugins.installed.disabled_done', { name }))
      void refreshPluginState(queryClient, server.id)
    },
    onError: notifyError,
  })

  const remove = useMutation({
    mutationFn: (jar: InstalledJar) => deletePlugin(server.id, jar.file),
    onSuccess: (_data, jar) => {
      toast.success(t('plugins.installed.removed', { name: jarName(jar) }))
      setRemoving(null)
      void refreshPluginState(queryClient, server.id)
    },
    onError: notifyError,
  })

  const pendingFile = toggle.isPending ? (toggle.variables?.jar.file ?? null) : remove.isPending ? (remove.variables?.file ?? null) : null

  function changeVersion(jar: InstalledJar) {
    if (jar.source === 'manual' || jar.project === null) return
    setInstalling({ source: jar.source, project: jar.project.id, title: jarName(jar), version: jar.version?.id ?? null })
  }

  function installedBody() {
    if (inventory.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
    if (inventory.isError) {
      // The tab only exists for plugin/mod types; a type changed meanwhile gets a neutral hint, never the error code.
      if (isApiError(inventory.error, 'plugins_unsupported')) return <p className="text-muted-foreground text-sm">{t('plugins.unsupported')}</p>
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(inventory.error, t)}
        </p>
      )
    }
    const inv = inventory.data
    return (
      <div className="space-y-3">
        {inv.source_errors.length > 0 ? (
          <p role="status" className="rounded-md border p-3 text-sm">
            {t('plugins.installed.source_errors')}
          </p>
        ) : null}
        <InstalledList
          inventory={inv}
          pendingFile={pendingFile}
          onToggle={(jar, enabled) => toggle.mutate({ jar, enabled })}
          onRemove={setRemoving}
          onChangeVersion={changeVersion}
        />
        {inv.truncated ? <p className="text-muted-foreground text-sm">{t('plugins.installed.truncated', { count: inv.jars.length })}</p> : null}
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {inventory.data?.restart_needed ? (
        <div role="status" className="flex flex-col gap-3 rounded-md border p-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="space-y-1">
            <p className="font-medium">{t('plugins.restart.title')}</p>
            <p className="text-muted-foreground text-sm">{t('plugins.restart.description')}</p>
          </div>
          {canPower ? (
            <Button type="button" disabled={power.isPending} onClick={() => power.mutate('restart')}>
              {t('plugins.restart.now')}
            </Button>
          ) : null}
        </div>
      ) : null}

      <Tabs value={view} onValueChange={(value) => setView(value as View)}>
        <TabsList aria-label={t('plugins.views')}>
          <TabsTrigger value="installed">{t('plugins.view.installed')}</TabsTrigger>
          <TabsTrigger value="discover">{t('plugins.view.discover')}</TabsTrigger>
        </TabsList>
        <TabsContent value="installed" className="pt-4">
          <SectionCard title={kind === 'plugin' ? t('plugins.installed.title_plugin') : t('plugins.installed.title_mod')}>{installedBody()}</SectionCard>
        </TabsContent>
        <TabsContent value="discover" className="pt-4">
          <SectionCard title={t('plugins.discover.title')}>
            <Discover serverId={server.id} sources={inventory.data?.sources ?? sourcesFor(kind)} busy={false} onInstall={setInstalling} />
          </SectionCard>
        </TabsContent>
      </Tabs>

      {installing ? (
        <InstallDialog
          serverId={server.id}
          dir={dir}
          target={installing}
          busy={false}
          onClose={() => setInstalling(null)}
          onStarted={(job) => {
            startJob(queryClient, server.id, job)
            toast.success(t('plugins.install.started', { title: installing.title }))
            setInstalling(null)
          }}
        />
      ) : null}
      {removing ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => (open ? undefined : setRemoving(null))}
          title={t('plugins.installed.remove_title', { name: jarName(removing) })}
          description={t('plugins.installed.remove_description', { file: removing.file, dir })}
          confirmLabel={t('plugins.installed.remove_submit')}
          destructive
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      ) : null}
    </div>
  )
}
```

(`busy={false}` bis Aufgabe 15; ein gestarteter Job landet schon jetzt per `startJob` im Cache, angezeigt wird er ab Aufgabe 15.)

- [ ] **Step 6: Prüfen und committen**

Run: `make check` → Expected: PASS. CI: `npx vitest run src/features/plugins` → Expected: PASS.

```bash
git add web/src/features/plugins
git commit -m "feat(web): discover plugins and mods and install them with a dependency preview

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 15: Job-Fortschritt, Updates, Sperren und Rechte-Hinweis

**Files:**
- Create: `web/src/features/plugins/use-plugin-job.ts`, `job-progress.tsx`, `plugins-jobs.test.tsx`
- Modify: `web/src/features/plugins/plugins-tab.tsx` (vollständig ersetzt)
- Modify: `web/src/features/users/permissions-page.tsx`, `web/src/features/users/permissions-page.test.tsx`

**Interfaces:**
- Consumes: `pluginJobQueryOptions`, `JOB_POLL_MS`, `isJobRunning`, `jobProgress`, `refreshPluginState`, `startJob`, `updatesQueryOptions`, `updatesKey`, `checkUpdates`, `updatePlugins`, `updatesByFile`, `jobStartErrorMessage`, `newlyGranted` (`permission-matrix.ts`, vorhanden).
- Produces: `usePluginJob(serverId): { job: PluginJob | null; running: boolean }`, `JobProgress({ job, onDismiss })`; `PluginsTab` in der endgültigen Fassung.

- [ ] **Step 1: Tests schreiben (rot)**

`web/src/features/plugins/plugins-jobs.test.tsx`:

```tsx
import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PluginJob, PluginJobStep } from '@/api/types'
import { server } from '@/mocks/node'
import { JOB_POLL_MS } from './api'
import { hit, inventory, job, mockInventory, modrinthJar, openDiscover, openPlugins, step, update } from './test-utils'

const LP = modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71')
const NEW_FILE = 'LuckPerms-Bukkit-5.5.72.jar'
const steps = (state: PluginJobStep['state']) => [step('LuckPerms', NEW_FILE, state)]

/** GET …/jobs answers null until `started`, then the sequence (the last entry repeats). */
function jobSequence(sequence: PluginJob[]) {
  const state = { started: false, polls: 0 }
  server.use(
    http.get('*/api/servers/:id/plugins/jobs', () => {
      if (!state.started) return HttpResponse.json({ job: null })
      const next = sequence[Math.min(state.polls, sequence.length - 1)]
      state.polls += 1
      return HttpResponse.json({ job: next })
    }),
  )
  return state
}

// Real time keeps flowing (MSW, userEvent); advanceTimersByTimeAsync jumps over the 2-s poll interval.
beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => {
  vi.useRealTimers()
})

describe('plugin jobs and updates', () => {
  it('follows an update job, locks the controls and reloads the inventory afterwards', async () => {
    let inventoryCalls = 0
    let updated = false
    server.use(
      http.get('*/api/servers/:id/plugins', () => {
        inventoryCalls += 1
        return HttpResponse.json(inventory({ jars: [updated ? modrinthJar(NEW_FILE, 'LuckPerms', '5.5.72') : LP] }))
      }),
      http.get('*/api/servers/:id/plugins/updates', () =>
        HttpResponse.json({ updates: updated ? [] : [update(LP.file, '5.5.71', '5.5.72')], checked_at: Date.now() }),
      ),
    )
    const jobs = jobSequence([job('update', 'running', steps('downloading')), job('update', 'done', steps('done'))])
    server.use(
      http.post('*/api/servers/:id/plugins/update', async ({ request }) => {
        expect(await request.json()).toEqual({ files: [] })
        jobs.started = true
        return HttpResponse.json(job('update', 'running', steps('pending')), { status: 202 })
      }),
    )
    const { user, region } = await openPlugins()
    expect(await within(region).findByText('Update: 5.5.72')).toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Alle aktualisieren (1)' }))

    const progress = await screen.findByRole('region', { name: 'Aktualisierung läuft …' })
    expect(within(progress).getByText('Wartet')).toBeInTheDocument()
    expect(within(progress).getByRole('progressbar', { name: 'Fortschritt des Auftrags' })).toBeInTheDocument()
    expect(within(region).getByRole('switch', { name: 'LuckPerms aktiv' })).toBeDisabled()
    expect(within(region).getByRole('button', { name: 'Alle aktualisieren (1)' })).toBeDisabled()

    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await within(progress).findByText('Lädt …')).toBeInTheDocument()
    const before = inventoryCalls
    updated = true
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await screen.findByText('Fertig. Starte den Server neu, damit die Änderungen wirken.')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Aktualisierung abgeschlossen' })).toBeInTheDocument()
    await waitFor(() => expect(inventoryCalls).toBeGreaterThan(before))
    expect(await within(region).findByText('5.5.72')).toBeInTheDocument()
    expect(within(region).getByRole('switch', { name: 'LuckPerms aktiv' })).toBeEnabled()
    expect(within(region).queryByRole('button', { name: /Alle aktualisieren/ })).not.toBeInTheDocument()
    const seen = jobs.polls
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS * 4))
    expect(jobs.polls).toBe(seen)
  })

  it('updates a single jar, translates step errors and hides a finished job on request', async () => {
    mockInventory(inventory({ jars: [LP] }))
    server.use(
      http.get('*/api/servers/:id/plugins/updates', () => HttpResponse.json({ updates: [update(LP.file, '5.5.71', '5.5.72')], checked_at: Date.now() })),
    )
    const jobs = jobSequence([job('update', 'failed', [step('LuckPerms', NEW_FILE, 'failed', 'hash_mismatch')])])
    server.use(
      http.post('*/api/servers/:id/plugins/update', async ({ request }) => {
        expect(await request.json()).toEqual({ files: [LP.file] })
        jobs.started = true
        return HttpResponse.json(job('update', 'running', steps('downloading')), { status: 202 })
      }),
    )
    const { user, region } = await openPlugins()
    await within(region).findByText('Update: 5.5.72')
    await user.click(within(region).getByRole('button', { name: 'Aktionen für LuckPerms' }))
    await user.click(screen.getByRole('menuitem', { name: 'Auf 5.5.72 aktualisieren' }))
    expect(await screen.findByRole('region', { name: 'Aktualisierung läuft …' })).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    const progress = await screen.findByRole('region', { name: 'Aktualisierung fehlgeschlagen' })
    expect(within(progress).getByText('Fehler: Prüfsumme stimmt nicht – die Datei wurde verworfen.')).toBeInTheDocument()
    expect(await screen.findByText('Nicht alles hat geklappt – Details stehen beim Auftrag.')).toBeInTheDocument()
    await user.click(within(progress).getByRole('button', { name: 'Ausblenden' }))
    expect(screen.queryByRole('region', { name: 'Aktualisierung fehlgeschlagen' })).not.toBeInTheDocument()
  })

  it('checks for updates on request', async () => {
    mockInventory(inventory({ jars: [LP] }))
    const refreshes: (string | null)[] = []
    server.use(
      http.get('*/api/servers/:id/plugins/updates', ({ request }) => {
        const refresh = new URL(request.url).searchParams.get('refresh')
        refreshes.push(refresh)
        return HttpResponse.json({ updates: refresh === 'true' ? [update(LP.file, '5.5.71', '5.5.72')] : [], checked_at: Date.now() })
      }),
    )
    const { user, region } = await openPlugins()
    await within(region).findByRole('switch', { name: 'LuckPerms aktiv' })
    expect(within(region).queryByRole('button', { name: /Alle aktualisieren/ })).not.toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Nach Updates suchen' }))
    expect(await within(region).findByText('Update: 5.5.72')).toBeInTheDocument()
    expect(within(region).getByRole('button', { name: 'Alle aktualisieren (1)' })).toBeEnabled()
    expect(await screen.findByText('Updates gefunden: 1')).toBeInTheDocument()
    expect(refreshes).toEqual([null, 'true'])
  })

  it('reports the rate limit of the update check', async () => {
    mockInventory(inventory({ jars: [LP] }))
    server.use(
      http.get('*/api/servers/:id/plugins/updates', ({ request }) =>
        new URL(request.url).searchParams.get('refresh')
          ? HttpResponse.json({ error: 'rate_limited', message: 'slow down' }, { status: 429, headers: { 'Retry-After': '30' } })
          : HttpResponse.json({ updates: [], checked_at: Date.now() }),
      ),
    )
    const { user, region } = await openPlugins()
    await within(region).findByRole('switch', { name: 'LuckPerms aktiv' })
    await user.click(within(region).getByRole('button', { name: 'Nach Updates suchen' }))
    expect(await screen.findByText('Zu viele Versuche. Bitte in 30 s erneut versuchen.')).toBeInTheDocument()
  })

  it('locks installing from the search while a job runs', async () => {
    const jobs = jobSequence([job('install', 'running', [step('Chunky', 'Chunky-1.4.28.jar', 'downloading')])])
    jobs.started = true
    server.use(http.get('*/api/servers/:id/plugins/search', () => HttpResponse.json({ hits: [hit('Chunky')], total: 1, offset: 0 })))
    const { region } = await openDiscover()
    expect(await within(region).findByRole('button', { name: 'Chunky installieren' })).toBeDisabled()
    expect(screen.getByRole('region', { name: 'Installation läuft …' })).toBeInTheDocument()
  })

  it('does not show a job that ended before the tab was opened', async () => {
    const old: PluginJob = { ...job('install', 'done', [step('Chunky', 'Chunky-1.4.28.jar', 'done')]), finished_at: Date.now() - 60_000 }
    const jobs = jobSequence([old])
    jobs.started = true
    const { region } = await openPlugins()
    await within(region).findByRole('switch', { name: 'OldTool aktiv' })
    expect(screen.queryByRole('region', { name: 'Installation abgeschlossen' })).not.toBeInTheDocument()
    expect(screen.queryByText('Fertig. Starte den Server neu, damit die Änderungen wirken.')).not.toBeInTheDocument()
  })
})
```

`web/src/features/users/permissions-page.test.tsx` ergänzen:

```tsx
  it('warns before granting plugin access', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UserPermissionsPage userId={2} />, { me: loginAs('admin', { sudo: true }) })
    await screen.findByRole('heading', { name: 'Rechte von alice' })
    expect(screen.queryByText(/erlaubt Code-Ausführung/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: 'survival: Plugins/Mods' }))
    expect(screen.getByText(/„Plugins\/Mods“ erlaubt Code-Ausführung im Minecraft-Server/)).toBeInTheDocument()
    expect(screen.queryByText(/„Dateien“ erlaubt/)).not.toBeInTheDocument()
  })
```

Run: `make check` → Expected: FAIL (`./use-plugin-job`, `./job-progress` fehlen; der neue Permissions-Test erst in der CI).

- [ ] **Step 2: Job-Hook und Fortschritt**

`web/src/features/plugins/use-plugin-job.ts`:

```ts
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { PluginJob } from '@/api/types'
import { JOB_POLL_MS, pluginJobQueryOptions, refreshPluginState } from './api'
import { isJobRunning } from './model'

/**
 * The server's plugin job (spec §6.4/§7.1): polled every 2 s while it runs, otherwise not at all. When a job this tab
 * saw running ends, inventory, updates, "installiert" badges and previews are reloaded and a toast reports the result.
 */
export function usePluginJob(serverId: number): { job: PluginJob | null; running: boolean } {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    ...pluginJobQueryOptions(serverId),
    refetchInterval: (q) => (isJobRunning(q.state.data) ? JOB_POLL_MS : false),
  })
  const job = query.data?.job ?? null
  const lastState = useRef(job?.state)
  useEffect(() => {
    const previous = lastState.current
    lastState.current = job?.state
    if (previous !== 'running' || !job || job.state === 'running') return
    if (job.state === 'done') toast.success(t('plugins.job.toast_done'))
    else toast.error(t('plugins.job.toast_failed'))
    void refreshPluginState(queryClient, serverId)
  }, [job, queryClient, serverId, t])
  return { job, running: job?.state === 'running' }
}
```

`web/src/features/plugins/job-progress.tsx`:

```tsx
import { CircleCheck, CircleMinus, CircleX, Clock, LoaderCircle } from 'lucide-react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import type { PluginJob, PluginJobStep } from '@/api/types'
import { Button } from '@/components/ui/button'
import { jobProgress } from './model'

function jobTitle(job: PluginJob, t: TFunction): string {
  if (job.kind === 'install') {
    if (job.state === 'running') return t('plugins.job.install_running')
    return job.state === 'done' ? t('plugins.job.install_done') : t('plugins.job.install_failed')
  }
  if (job.state === 'running') return t('plugins.job.update_running')
  return job.state === 'done' ? t('plugins.job.update_done') : t('plugins.job.update_failed')
}

/** Spec §7.1: failed steps name the reason from error_code (translated); the panel's text is only a fallback. */
function stepText(step: PluginJobStep, t: TFunction): string {
  if (step.state !== 'failed') return t(`plugins.step.state.${step.state}`)
  if (step.error_code) return t('plugins.step.failed_reason', { reason: t(`plugins.step.error.${step.error_code}`) })
  return step.error ? t('plugins.step.failed_reason', { reason: step.error }) : t('plugins.step.state.failed')
}

function StepIcon({ state }: { state: PluginJobStep['state'] }) {
  const cls = 'mt-0.5 size-4 shrink-0'
  if (state === 'downloading') return <LoaderCircle aria-hidden="true" className={`${cls} animate-spin`} />
  if (state === 'done') return <CircleCheck aria-hidden="true" className={cls} />
  if (state === 'failed') return <CircleX aria-hidden="true" className={`${cls} text-destructive`} />
  if (state === 'skipped') return <CircleMinus aria-hidden="true" className={cls} />
  return <Clock aria-hidden="true" className={`${cls} text-muted-foreground`} />
}

export function JobProgress({ job, onDismiss }: { job: PluginJob; onDismiss(): void }) {
  const { t } = useTranslation()
  const id = useId()
  const { finished, total } = jobProgress(job)
  return (
    <section aria-labelledby={id} className="space-y-3 rounded-md border p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        {/* Only the title is announced; the per-step lines change on every poll (as in 3b, review F9). */}
        <p id={id} role="status" className="font-medium">
          {jobTitle(job, t)}
        </p>
        {job.state !== 'running' ? (
          <Button type="button" variant="ghost" size="sm" onClick={onDismiss}>
            {t('plugins.job.dismiss')}
          </Button>
        ) : null}
      </div>
      <progress className="h-1.5 w-full" value={finished} max={total || 1} aria-label={t('plugins.job.progress')} />
      <p className="text-muted-foreground text-sm tabular-nums">{t('plugins.job.counts', { finished, total })}</p>
      <ul className="space-y-2 text-sm">
        {job.steps.map((step, i) => (
          <li key={`${i}:${step.file}`} className="flex items-start gap-2">
            <StepIcon state={step.state} />
            <div className="min-w-0 flex-1">
              <p className="break-words">
                <span className="font-medium">{step.title}</span>{' '}
                <span className="text-muted-foreground font-mono text-xs break-all">{step.file}</span>
              </p>
              <p className={step.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'}>{stepText(step, t)}</p>
            </div>
          </li>
        ))}
      </ul>
    </section>
  )
}
```

- [ ] **Step 3: Endgültiger Tab mit Job, Updates und Sperren**

`web/src/features/plugins/plugins-tab.tsx` vollständig ersetzen durch:

```tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'
import type { InstalledJar, PluginKind, PluginUpdates, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { SectionCard } from '@/features/profile/section-card'
import { usePower } from '@/features/servers/use-power'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import {
  checkUpdates,
  deletePlugin,
  inventoryQueryOptions,
  refreshPluginState,
  startJob,
  togglePlugin,
  updatePlugins,
  updatesKey,
  updatesQueryOptions,
} from './api'
import { Discover } from './discover'
import { InstallDialog } from './install-dialog'
import { InstalledList } from './installed-list'
import { JobProgress } from './job-progress'
import { jobStartErrorMessage, usePluginErrorToast } from './messages'
import { jarName, sourcesFor, updatesByFile, type InstallTarget } from './model'
import { usePluginJob } from './use-plugin-job'

type View = 'installed' | 'discover'

export interface PluginsTabProps {
  server: Server
  /** From pluginKindOf(instance.type); the detail page shows the tab only when it is not null. */
  kind: PluginKind
}

export function PluginsTab({ server, kind }: PluginsTabProps) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const queryClient = useQueryClient()
  const notifyError = usePluginErrorToast()
  const power = usePower(server)
  const [view, setView] = useState<View>('installed')
  const [installing, setInstalling] = useState<InstallTarget | null>(null)
  const [removing, setRemoving] = useState<InstalledJar | null>(null)
  const [dismissed, setDismissed] = useState<string | null>(null)
  // The panel keeps the last job result (spec §6.4); one that ended before the tab was opened is not shown again.
  const [openedAt] = useState(() => Date.now())
  const inventory = useQuery(inventoryQueryOptions(server.id))
  const updates = useQuery({ ...updatesQueryOptions(server.id), enabled: inventory.isSuccess })
  const { job, running } = usePluginJob(server.id)
  const canPower = server.permissions.includes('server.power')
  const dir = inventory.data?.dir ?? (kind === 'plugin' ? 'plugins' : 'mods')
  const updateMap = updatesByFile(updates.data?.updates)
  const updateCount = updates.data?.updates.length ?? 0

  const toggle = useMutation({
    mutationFn: ({ jar, enabled }: { jar: InstalledJar; enabled: boolean }) => togglePlugin(server.id, jar.file, enabled),
    onSuccess: (_data, { jar, enabled }) => {
      const name = jarName(jar)
      toast.success(enabled ? t('plugins.installed.enabled_done', { name }) : t('plugins.installed.disabled_done', { name }))
      void refreshPluginState(queryClient, server.id)
    },
    onError: notifyError,
  })

  const remove = useMutation({
    mutationFn: (jar: InstalledJar) => deletePlugin(server.id, jar.file),
    onSuccess: (_data, jar) => {
      toast.success(t('plugins.installed.removed', { name: jarName(jar) }))
      setRemoving(null)
      void refreshPluginState(queryClient, server.id)
    },
    onError: notifyError,
  })

  const check = useMutation({
    mutationFn: () => checkUpdates(server.id),
    onSuccess: (data) => {
      queryClient.setQueryData<PluginUpdates>(updatesKey(server.id), data)
      toast.success(data.updates.length > 0 ? t('plugins.updates.found', { count: data.updates.length }) : t('plugins.updates.none'))
    },
    onError: notifyError,
  })

  const update = useMutation({
    mutationFn: (files: string[]) => updatePlugins(server.id, files),
    onSuccess: (started) => {
      startJob(queryClient, server.id, started)
      toast.success(t('plugins.updates.started'))
    },
    onError: (err) => toast.error(jobStartErrorMessage(err, t)),
  })

  // Spec §7.1: while a job runs, installing, updating, toggling and removing are locked (the panel answers 409 anyway).
  const busy = running || update.isPending
  const pendingFile = toggle.isPending ? (toggle.variables?.jar.file ?? null) : remove.isPending ? (remove.variables?.file ?? null) : null
  const visibleJob = job !== null && job.id !== dismissed && (job.state === 'running' || job.finished_at >= openedAt) ? job : null

  function changeVersion(jar: InstalledJar) {
    if (jar.source === 'manual' || jar.project === null) return
    setInstalling({ source: jar.source, project: jar.project.id, title: jarName(jar), version: jar.version?.id ?? null })
  }

  function installedBody() {
    if (inventory.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
    if (inventory.isError) {
      // The tab only exists for plugin/mod types; a type changed meanwhile gets a neutral hint, never the error code.
      if (isApiError(inventory.error, 'plugins_unsupported')) return <p className="text-muted-foreground text-sm">{t('plugins.unsupported')}</p>
      return (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(inventory.error, t)}
        </p>
      )
    }
    const inv = inventory.data
    return (
      <div className="space-y-3">
        {inv.source_errors.length > 0 ? (
          <p role="status" className="rounded-md border p-3 text-sm">
            {t('plugins.installed.source_errors')}
          </p>
        ) : null}
        <InstalledList
          inventory={inv}
          updates={updateMap}
          busy={busy}
          pendingFile={pendingFile}
          onToggle={(jar, enabled) => toggle.mutate({ jar, enabled })}
          onRemove={setRemoving}
          onChangeVersion={changeVersion}
          onUpdate={(jar) => update.mutate([jar.file])}
        />
        {inv.truncated ? <p className="text-muted-foreground text-sm">{t('plugins.installed.truncated', { count: inv.jars.length })}</p> : null}
        {updates.isError ? <p className="text-muted-foreground text-sm">{t('plugins.updates.failed')}</p> : null}
        {updates.data && updates.data.checked_at > 0 ? (
          <p className="text-muted-foreground text-xs">{t('plugins.updates.checked_at', { when: fmt.relative(updates.data.checked_at) })}</p>
        ) : null}
      </div>
    )
  }

  const updateActions = (
    <>
      <Button type="button" variant="outline" size="sm" disabled={check.isPending || !inventory.isSuccess} onClick={() => check.mutate()}>
        <RefreshCw aria-hidden="true" />
        {check.isPending ? t('plugins.updates.checking') : t('plugins.updates.check')}
      </Button>
      {updateCount > 0 ? (
        <Button type="button" size="sm" disabled={busy} onClick={() => update.mutate([])}>
          {t('plugins.updates.update_all', { count: updateCount })}
        </Button>
      ) : null}
    </>
  )

  return (
    <div className="space-y-4">
      {inventory.data?.restart_needed ? (
        <div role="status" className="flex flex-col gap-3 rounded-md border p-4 sm:flex-row sm:items-center sm:justify-between">
          <div className="space-y-1">
            <p className="font-medium">{t('plugins.restart.title')}</p>
            <p className="text-muted-foreground text-sm">{t('plugins.restart.description')}</p>
          </div>
          {canPower ? (
            <Button type="button" disabled={power.isPending || running} onClick={() => power.mutate('restart')}>
              {t('plugins.restart.now')}
            </Button>
          ) : null}
        </div>
      ) : null}

      {visibleJob ? <JobProgress job={visibleJob} onDismiss={() => setDismissed(visibleJob.id)} /> : null}

      <Tabs value={view} onValueChange={(value) => setView(value as View)}>
        <TabsList aria-label={t('plugins.views')}>
          <TabsTrigger value="installed">{t('plugins.view.installed')}</TabsTrigger>
          <TabsTrigger value="discover">{t('plugins.view.discover')}</TabsTrigger>
        </TabsList>
        <TabsContent value="installed" className="pt-4">
          <SectionCard
            title={kind === 'plugin' ? t('plugins.installed.title_plugin') : t('plugins.installed.title_mod')}
            actions={inventory.isError ? undefined : updateActions}
          >
            {installedBody()}
          </SectionCard>
        </TabsContent>
        <TabsContent value="discover" className="pt-4">
          <SectionCard title={t('plugins.discover.title')}>
            <Discover serverId={server.id} sources={inventory.data?.sources ?? sourcesFor(kind)} busy={busy} onInstall={setInstalling} />
          </SectionCard>
        </TabsContent>
      </Tabs>

      {installing ? (
        <InstallDialog
          serverId={server.id}
          dir={dir}
          target={installing}
          busy={busy}
          onClose={() => setInstalling(null)}
          onStarted={(started) => {
            startJob(queryClient, server.id, started)
            toast.success(t('plugins.install.started', { title: installing.title }))
            setInstalling(null)
          }}
        />
      ) : null}
      {removing ? (
        <ConfirmDialog
          open
          onOpenChange={(open) => (open ? undefined : setRemoving(null))}
          title={t('plugins.installed.remove_title', { name: jarName(removing) })}
          description={t('plugins.installed.remove_description', { file: removing.file, dir })}
          confirmLabel={t('plugins.installed.remove_submit')}
          destructive
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      ) : null}
    </div>
  )
}
```

Hinweise:
- `useState(() => Date.now())` ist der einzige direkte Zeitzugriff (Frontend hat keinen `clock.Clock`); die Fake-Timer der Tests (`shouldAdvanceTime`) und die Mock-Zeitstempel nutzen dieselbe Uhr. Weicht die Server-Uhr in Produktion um Sekunden ab, erscheint im ungünstigsten Fall ein eben beendeter Job einmal mehr – harmlos.
- `refreshPluginState` lädt nach Job-Ende auch `GET …/updates` neu; B2 verwirft dafür den Updates-Cache nach jedem Job, Toggle und Delete (abgestimmt mit plan-panel).
- Ein Neustart über den Banner lädt das Inventar über `usePower` (invalidiert `['servers']`) mit neu, damit `restart_needed` verschwindet.

- [ ] **Step 4: Rechte-Hinweis**

`web/src/features/users/permissions-page.tsx`: nach `const filesWarning = …`:

```tsx
  // Spec §7.2: a plugin runs code in the container – same trust as server.files.
  const pluginsWarning = !targetIsAdmin && saved !== null && newlyGranted(current, saved, 'server.plugins').length > 0
```

und nach dem `filesWarning`-Absatz:

```tsx
      {pluginsWarning ? (
        <p role="status" className="border-destructive/50 mb-4 rounded-md border p-3 text-sm">
          {t('permissions.plugins_warning')}
        </p>
      ) : null}
```

- [ ] **Step 5: Prüfen und committen**

Run: `make check` → Expected: PASS. CI: `npx vitest run src/features/plugins src/features/servers src/features/users src/i18n` → Expected: PASS.

```bash
git add web/src/features/plugins web/src/features/users
git commit -m "feat(web): plugin job progress, updates, locks and the plugins permission warning

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

---

### Task 16: Playwright und Abschlussprüfung der Texte

**Files:**
- Create: `web/e2e/helpers.ts`, `web/e2e/plugins.spec.ts`
- Modify: `web/e2e/servers.spec.ts` (Helfer importieren), `web/playwright.config.ts` (eine Zeile, nach Freigabe durch den Lead – siehe „Abweichungen“ 3)
- Prüfen: `web/src/i18n/de.ts`, `en.ts`

**Interfaces:**
- Consumes: Mock-Verhalten nach V5 (siehe „Was Phase A liefert“): survival mit LuckPerms (Update 5.5.72), ViaVersion, OldTool; Hangar-Suche „ViaBack“ trifft ViaBackwards; Job ~4 s; modded (fabric) mit `fabric-api-0.110.0.jar`; creative (vanilla) ohne Tab.

- [ ] **Step 1: Helfer auslagern**

`web/e2e/helpers.ts`:

```ts
import { expect, type Page } from '@playwright/test'

export const PASSWORD = 'correct-horse-battery'
export const TOTP_CODE = '123456'

/** Signs in via the UI; the mock state lives in the page, so the test must not reload afterwards. */
export async function signIn(page: Page, username: string, withTotp: boolean) {
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
  await page.getByLabel('Benutzername').fill(username)
  await page.getByLabel('Passwort').fill(PASSWORD)
  await page.getByRole('button', { name: 'Anmelden' }).click()
  if (withTotp) {
    await expect(page).toHaveURL(/\/login\/2fa$/)
    await page.getByLabel('Code aus der Authenticator-App').fill(TOTP_CODE)
    await page.getByRole('button', { name: 'Bestätigen' }).click()
  }
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()
}

export async function confirmSudo(page: Page) {
  const sudo = page.getByRole('dialog', { name: 'Aktion bestätigen' })
  await sudo.getByLabel('Code aus der Authenticator-App').fill(TOTP_CODE)
  await sudo.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(sudo).toBeHidden()
}
```

In `web/e2e/servers.spec.ts` die Zeilen 1–26 (Import, `PASSWORD`, `TOTP_CODE`, `signIn`, `confirmSudo`) ersetzen durch:

```ts
import { expect, test } from '@playwright/test'
import { confirmSudo, signIn } from './helpers'
```

`web/playwright.config.ts`, Projekt `mock`:

```ts
      testMatch: /(?<![\w-])(totp|smoke|servers|plugins)\.spec\.ts$/,
```

(`helpers.ts` endet nicht auf `.spec.ts` und wird nicht als Test gesammelt.)

- [ ] **Step 2: Playwright-Tests**

`web/e2e/plugins.spec.ts`:

```ts
import { expect, test } from '@playwright/test'
import { signIn } from './helpers'

test('admin installs ViaBackwards, disables a jar, installs an update and removes a jar', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await page.getByRole('tab', { name: 'Plugins', exact: true }).click()
  const installed = page.getByRole('region', { name: 'Installierte Plugins' })
  await expect(installed.getByText('LuckPerms', { exact: true })).toBeVisible()

  // Discover on Hangar and install with the dependency preview.
  await page.getByRole('tab', { name: 'Entdecken' }).click()
  const discover = page.getByRole('region', { name: 'Entdecken' })
  await discover.getByRole('group', { name: 'Quelle' }).getByRole('button', { name: 'Hangar' }).click()
  await discover.getByLabel('Suchen').fill('ViaBack')
  await discover.getByRole('button', { name: 'ViaBackwards installieren' }).click()
  const dialog = page.getByRole('dialog', { name: 'ViaBackwards installieren' })
  await expect(dialog.getByRole('region', { name: 'Bereits vorhanden' }).getByText('ViaVersion', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('region', { name: 'Wird installiert', exact: true }).getByText('ViaBackwards', { exact: true })).toBeVisible()
  await dialog.getByRole('button', { name: 'Installieren', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText('Installation läuft …')).toBeVisible()
  await expect(page.getByText('Installation abgeschlossen')).toBeVisible({ timeout: 15_000 })
  await page.getByRole('tab', { name: 'Installiert' }).click()
  await expect(installed.getByText('ViaBackwards', { exact: true })).toBeVisible()

  // Disable a jar.
  const oldTool = installed.getByRole('switch', { name: 'OldTool aktiv' })
  await expect(oldTool).toBeChecked()
  await oldTool.click()
  await expect(page.getByText('„OldTool“ deaktiviert.')).toBeVisible()
  await expect(installed.getByRole('switch', { name: 'OldTool aktiv' })).not.toBeChecked()

  // Install the available update of LuckPerms.
  await expect(installed.getByText('Update: 5.5.72')).toBeVisible()
  await installed.getByRole('button', { name: 'Aktionen für LuckPerms' }).click()
  await page.getByRole('menuitem', { name: 'Auf 5.5.72 aktualisieren' }).click()
  await expect(page.getByText('Aktualisierung abgeschlossen')).toBeVisible({ timeout: 15_000 })
  await expect(installed.getByText('Update: 5.5.72')).toHaveCount(0)
  await expect(installed.getByText('5.5.72', { exact: true })).toBeVisible()

  // Remove the jar installed above.
  await installed.getByRole('button', { name: 'Aktionen für ViaBackwards' }).click()
  await page.getByRole('menuitem', { name: 'Entfernen' }).click()
  const confirm = page.getByRole('dialog', { name: '„ViaBackwards“ entfernen?' })
  await confirm.getByRole('button', { name: 'Entfernen' }).click()
  await expect(page.getByText('„ViaBackwards“ entfernt.')).toBeVisible()
  await expect(installed.getByText('ViaBackwards', { exact: true })).toHaveCount(0)
})

test('the mods tab of a fabric server offers only Modrinth', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'modded', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/4$/)
  await expect(page.getByRole('tab', { name: 'Plugins', exact: true })).toHaveCount(0)
  await page.getByRole('tab', { name: 'Mods', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Installierte Mods' }).getByText('fabric-api-0.110.0.jar')).toBeVisible()
  await page.getByRole('tab', { name: 'Entdecken' }).click()
  const discover = page.getByRole('region', { name: 'Entdecken' })
  await expect(discover.getByRole('heading', { name: 'Beliebt auf Modrinth' })).toBeVisible()
  await expect(discover.getByRole('group', { name: 'Quelle' })).toHaveCount(0)
  await expect(discover.getByRole('button', { name: 'Hangar' })).toHaveCount(0)
})

test('vanilla servers and users without the permission see no plugins tab', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'creative', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/2$/)
  await expect(page.getByRole('tab', { name: 'Übersicht' })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Plugins', exact: true })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: 'Mods', exact: true })).toHaveCount(0)
})

test('a user without the plugins permission sees no plugins tab', async ({ page }) => {
  await signIn(page, 'alice', false)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await expect(page.getByRole('tab', { name: 'Übersicht' })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Plugins', exact: true })).toHaveCount(0)
})
```

Hinweise:
- Playwright vergleicht Rollen-Namen ohne `exact` als Teilstring (case-insensitiv). `exact: true` steht deshalb dort, wo ein kurzer Name Teil eines anderen sein könnte („Plugins“, „Mods“, „Installieren“ neben „ViaBackwards installieren“, „Wird installiert“).
- Die Vorschau prüft nur, was V5 festlegt (ViaVersion vorhanden, ViaBackwards wird installiert). Der Fall „Wird mitinstalliert“ (ChunkyBorder → Chunky) ist in Vitest abgedeckt (Aufgabe 14).
- Ob der Mock nach Änderungen `restart_needed` setzt, legt V5 nicht fest; der Banner wird daher nur in Vitest geprüft (Aufgabe 13).
- Weicht der Mock von den Annahmen ab (Titel, Versionsname `5.5.72`, Update schon ohne `refresh`), nicht den Test verbiegen, sondern den Lead fragen (Mock gehört Phase A).

- [ ] **Step 3: Texte prüfen**

Kein eigener Code: `make check` (`tsc` mit `en: Messages`) und der CI-Test `src/i18n/i18n.test.ts` („has identical key sets“, „defines every dynamic key“, „defines every static t() key“) sichern Vollständigkeit. Zusätzlich die englischen Texte einmal lesen (gleiche Bedeutung wie `de`, „panel“ klein wie im übrigen UI).

- [ ] **Step 4: Prüfen und committen**

Run: `make check` → Expected: PASS. Push; CI-Jobs „Web“ und „E2E“ → Expected: PASS (`npm run e2e` läuft gegen den Mock auf Port 5174).

```bash
git add web/e2e web/playwright.config.ts
git commit -m "test(web): end-to-end plugin flows against the mock

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01FujEdLGX6o4icgyMAYek1R"
```

Danach: Abschlussbericht an den Lead (Commits je Aufgabe, CI-Run, Abweichungen, ggf. nötige Mock-Anpassungen), PR bleibt Draft (der Lead setzt ihn auf „ready“).

## Self-Review (dieser Datei)

- Spec §7.1 Installiert (Icon über den Proxy, Name-Fallback Titel → `meta.name` → Datei, Version, Quelle, Schalter, Menü Projektseite/Version wechseln/Entfernen mit Bestätigung, Update-Badge, „Nicht zugeordnet – Updates nicht möglich“, Karten auf dem Handy, `source_errors`-Hinweis) → Aufgaben 13, 14 (Version wechseln), 15 (Badge, Einzel-Update).
- §7.1 Kopfzeile „Nach Updates suchen“ (`refresh=true`) und „Alle aktualisieren (n)“ (`files: []`) → Aufgabe 15.
- §7.1 Entdecken (verzögerte Suche, Modrinth | Hangar, bei Mods nur Modrinth, Treffer mit Icon/Titel/Autor/Downloads/Kurzbeschreibung als Text, Badge „Installiert“, beliebteste ohne Suchbegriff, „Mehr laden“ per `offset`, Fehlerzustand mit „Erneut versuchen“) → Aufgabe 14.
- §7.1 Installieren-Dialog (Standard neueste Release, Beta/Alpha markiert, nur extern ausgegraut mit Link und Dateien-Tab-Hinweis, Vorschau Wird installiert/mitinstalliert, Bereits vorhanden, Manuell nötig mit Links, Inkompatibel sperrt; 409 `plugin_not_downloadable`/`plugin_incompatible`/`conflict` und 429 mit eigenen Texten) → Aufgabe 14.
- §7.1 Job (Fortschritt pro Jar, übersetzte `plugins.step.error.*`, 2-s-Abfrage nur solange `running`, Sperren, Neuladen danach) → Aufgabe 15; Banner `restart_needed` mit Neustart bei `server.power` → Aufgabe 13.
- §7.1 Tab nur bei `server.plugins` und paper/purpur/fabric/neoforge, Titel nach Art; `409 plugins_unsupported` nie sichtbar → Aufgabe 13.
- §7.2 Rechte-Hinweis → Aufgabe 15; §7.3 Mocks → Phase A, dieser Plan überschreibt Antworten nur in Vitest, wo der Ausgang deterministisch sein muss; Playwright nutzt den Mock unverändert (Aufgabe 16).
- Global Constraints Frontend: Texte de/en, kein `dangerouslySetInnerHTML`, keine Markdown-/HTML-Darstellung, Bilder nur über den Proxy (`iconSrc`), externe Links nur http(s) mit `noopener noreferrer` → Aufgaben 13, 14 (Tests).
- Keine neuen Pakete; nur vorhandene shadcn-Komponenten (Tabs, Dialog, DropdownMenu, Switch, Badge, Button, Input, Label) und `DataList`/`ConfirmDialog`/`SectionCard`.
