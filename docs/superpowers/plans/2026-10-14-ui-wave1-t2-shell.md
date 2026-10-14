# UI-Redesign – Welle 1 / T2 Shell (Implementation Plan)

> **For agentic workers:** Dieser Plan wird von **Teammate T2** eines Claude-Code-Agent-Teams umgesetzt (CLAUDE.md „Implementation with agent teams“). Steps use checkbox (`- [ ]`) syntax for tracking. Arbeite nur in deinem Worktree und nur an den Dateien aus „File Structure“. Brauchst du eine Änderung an einer fremden Datei, melde sie dem Lead (SendMessage) statt sie selbst zu machen.

**Goal:** Den neuen App-Rahmen (Icon-Leiste, Server-Baum, Topbar mit Brotkrume, responsiv) und die Server-Bereiche als echte Routen mit festem Server-Kopf bauen (Spec §4).

**Architecture:** `AppLayout` wird zu einem Raster aus Icon-Leiste (`IconRail`), Server-Baum (`ServerTree`) und Inhalt mit dünner Topbar. Die Serverseite wird eine Layout-Route `/_app/servers/$id` (lädt den Server, zeigt `ServerHeader`, stellt `ServerContext` bereit) mit Kind-Routen je Bereich. Welche Bereiche erlaubt sind, entscheidet allein die reine Funktion `serverSections()`; Baum, Mobilmenü und Routen-Guards nutzen sie.

**Tech Stack:** React 19, TanStack Router (file-based, `useBlocker`, `Navigate`), TanStack Query, Tailwind v4, Vitest + Testing Library + MSW, Playwright.

**Spec:** `docs/superpowers/specs/2026-10-14-mc-panel-ui-redesign-design.md` (§4, §6.2, §6.3) – baut auf Welle 0 (`docs/superpowers/plans/2026-10-14-ui-wave0-foundation.md`) auf.

**Branch / Worktree:**

```bash
cd /Users/jannes/Workspace/mc-panel
git fetch origin
git worktree add ../mc-panel-wt/feat-ui-shell -b feat/ui-shell origin/feat/ui-foundation
cd ../mc-panel-wt/feat-ui-shell/web && npm ci
```

## Global Constraints

- Nur Frontend (`web/`); keine API-, Go-, OpenAPI- oder Mock-Änderung.
- Keine externen Ressourcen (CSP); Icons nur aus `lucide-react` oder `PixelIcon`.
- Gemeinsame Bausteine aus Welle 0 nutzen, nicht nachbauen: `PageHeader`, `Section`, `StatSlot`, `PixelIcon` (`@/components/*`); Utilities `bevel`, `bevel-primary`, `bevel-inset`, `glow`, `pixel-heading`; Farben `bg-rail` gibt es **nicht** als Klasse → `bg-[var(--rail)]`.
- `web/src/index.css`, `web/package.json`, `web/src/components/**`, `web/src/test/**` gehören dir nicht (Lead bzw. T1).
- i18n: neue Schlüssel nur in den Namensräumen `nav.*` und `servers.header.*` / `servers.detail.*`; bestehende Schlüssel nicht löschen/umbenennen. Ausnahme (dein Namensraum): Wert von `nav.dashboard` wird „Server“ / „Servers“.
- Barrierefreiheit: jedes Icon-Element hat `aria-label`; aktiver Eintrag `aria-current="page"`; Skip-Link bleibt; sichtbarer Fokus.
- Fehlende Werte als „–“ (StatSlot macht das bei `null`), nie 0.
- Local storage-Schlüssel `mcpanel.tree` (Werte `"open"`/`"closed"`), Zugriff immer in `try/catch`, Standard offen.
- Breakpoints: `lg` = 1024 px (Leiste + Baum), `md` = 768 px (nur Leiste, Baum als Flyout), darunter Topbar + Drawer.
- Mac: nur `make check`. Tests: `make test-vm SUITE=web`, `SUITE=e2e`, Screenshots `SUITE=shots`.
- Commits: Conventional Commits mit den Attribution-Zeilen der Session.

## Review Focus

- **Recht wird entzogen, während ein Bereich offen ist:** Server-Query liefert nach Refetch weniger `permissions` → der Bereich muss zur Übersicht weiterleiten (`replace`), nicht leer oder kaputt stehen bleiben (Test in Task 3).
- **Serverwechsel im Baum mit nicht erlaubtem Bereich:** auf `survival/files` stehend zu `creative` (ohne `server.files`) springen → Ziel ist `creative`-Übersicht, nicht `creative/files` (Test in Task 2, `sectionHref`).
- **Doppelte Link-Namen in E2E (strict mode):** Der Baum enthält Links „survival“ und „Übersicht“, die Startseite auch → alle E2E-Klicks auf Servernamen/Bereiche über die neuen Helfer im Baum scopen (Task 6).
- **Server ohne Instanz / Server gelöscht während offen / 403:** Layout zeigt Fehlermeldung statt Kopf + Outlet, Kind-Routen rendern nichts (Tests aus der bisherigen Detailseite übernommen, Task 3).
- **`localStorage` wirft (Private Mode):** Baum startet offen und lässt sich trotzdem umschalten (Test in Task 4).

---

## File Structure

| Datei | Verantwortung |
|---|---|
| `web/src/features/servers/sections.ts` (neu) | `ServerSection`, `serverSections()`, `sectionHref()`, Label-Schlüssel |
| `web/src/features/servers/sections.test.ts` (neu) | alle Rechte-Kombinationen |
| `web/src/features/servers/server-context.tsx` (neu) | `ServerContext`, `useServerContext()` |
| `web/src/features/servers/server-layout.tsx` (neu, ersetzt `server-detail-page.tsx`) | Laden, Fehlerzustände, `ServerHeader`, `<Outlet/>` |
| `web/src/features/servers/section-guard.tsx` (neu) | Weiterleitung bei nicht erlaubtem Bereich |
| `web/src/features/servers/server-header.tsx` (neu) | Name, Status, Adresse, Power, 4 `StatSlot`s, Banner |
| `web/src/features/servers/server-overview.tsx` (ändern) | nur noch Details + Online-Spieler |
| `web/src/features/servers/status-badge.tsx`, `address-line.tsx`, `power-buttons.tsx` (ändern) | M2-Stil (Icon-Variante für Power) |
| `web/src/features/servers/server-detail-page.tsx` (löschen) | ersetzt durch Layout |
| `web/src/features/servers/server-detail-page.test.tsx` → `server-layout.test.tsx` (umbenennen + umbauen) | Routen-basierte Tests |
| `web/src/features/servers/test-utils.tsx` (neu) | `renderApp(path, me)` mit echter Route-Tree |
| `web/src/features/layout/app-layout.tsx` (ändern) | Raster, Topbar, Drawer, Flyout |
| `web/src/features/layout/icon-rail.tsx` (neu) | Icon-Leiste |
| `web/src/features/layout/server-tree.tsx` (neu) | Server-Baum |
| `web/src/features/layout/tree-state.ts` (neu, + Test) | `mcpanel.tree` lesen/schreiben |
| `web/src/features/layout/breadcrumb.tsx` (neu) | Brotkrume aus Route |
| `web/src/features/layout/nav.ts` (ändern) | Gruppierung oben/unten |
| `web/src/features/layout/app-layout.test.tsx` (ändern) | neue Struktur |
| `web/src/routes/_app/servers/$id.tsx` (ändern) | Layout-Route |
| `web/src/routes/_app/servers/$id/{index,monitoring,console,players,files,plugins,backups,settings}.tsx` (neu) | Bereiche |
| `web/src/routeTree.gen.ts` (regeneriert) | |
| `web/src/i18n/de.ts`, `en.ts` (nur `nav.*`, `servers.header.*`) | Texte |
| `web/e2e/helpers.ts`, `web/e2e/{servers,plugins,backups,monitoring}.spec.ts` | Navigation Tab → Link |
| `web/e2e/shots/shell.shots.ts` (neu) | Screenshots |

`format.ts`, `status.ts`, `use-power.ts`, `api.ts` gehören dir, bleiben aber inhaltlich unverändert (andere Teammates importieren sie).

---

### Task 1: `serverSections` und `sectionHref`

**Files:**
- Create: `web/src/features/servers/sections.ts`, `web/src/features/servers/sections.test.ts`

**Interfaces:**
- Consumes: `Server` (`@/api/types`), `pluginKindOf` (`@/features/plugins/model`, unverändert von T4).
- Produces:

```ts
export type ServerSection = 'overview' | 'monitoring' | 'console' | 'players' | 'files' | 'plugins' | 'backups' | 'settings'
export const SECTION_ORDER: readonly ServerSection[]
export function serverSections(server: Pick<Server, 'permissions' | 'instance'>, isAdmin: boolean): ServerSection[]
export function sectionLabelKey(section: ServerSection, server: Pick<Server, 'instance'>): string // i18n key, 'servers.detail.tabs.mods' for mod servers
export function sectionHref(section: ServerSection): '/servers/$id' | `/servers/$id/${Exclude<ServerSection, 'overview'>}`
/** Section to open on `target` when switching from `current`: keeps it if allowed, else overview. */
export function sectionForSwitch(current: ServerSection, target: Pick<Server, 'permissions' | 'instance'>, isAdmin: boolean): ServerSection
```

- [ ] **Step 1: Failing test**

`sections.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import type { InstanceConfig, Permission, Server } from '@/api/types'
import { sectionForSwitch, sectionHref, sectionLabelKey, serverSections } from './sections'

function instance(type: InstanceConfig['type']): InstanceConfig {
  return { type, version: '1.21.11', loader_version: null, modpack: null, java: 'java21', memory_mib: 4096, properties: {} as InstanceConfig['properties'] }
}
function srv(perms: Permission[], type: InstanceConfig['type'] | null = 'paper'): Pick<Server, 'permissions' | 'instance'> {
  return { permissions: perms, instance: type ? instance(type) : null }
}

describe('serverSections', () => {
  it('gives view-only users overview and monitoring', () => {
    expect(serverSections(srv(['server.view']), false)).toEqual(['overview', 'monitoring'])
  })
  it('adds console and players with server.console', () => {
    expect(serverSections(srv(['server.view', 'server.console']), false)).toEqual(['overview', 'monitoring', 'console', 'players'])
  })
  it('adds files with server.files', () => {
    expect(serverSections(srv(['server.view', 'server.files']), false)).toEqual(['overview', 'monitoring', 'files'])
  })
  it('adds plugins only for plugin/mod types with server.plugins', () => {
    expect(serverSections(srv(['server.view', 'server.plugins'], 'paper'), false)).toContain('plugins')
    expect(serverSections(srv(['server.view', 'server.plugins'], 'fabric'), false)).toContain('plugins')
    expect(serverSections(srv(['server.view', 'server.plugins'], 'vanilla'), false)).not.toContain('plugins')
    expect(serverSections(srv(['server.view'], 'paper'), false)).not.toContain('plugins')
  })
  it('adds backups on every type with server.backups', () => {
    expect(serverSections(srv(['server.view', 'server.backups'], 'vanilla'), false)).toEqual(['overview', 'monitoring', 'backups'])
  })
  it('adds settings for admins only, in fixed order', () => {
    const all: Permission[] = ['server.view', 'server.power', 'server.console', 'server.files', 'server.plugins', 'server.backups']
    expect(serverSections(srv(all), true)).toEqual(['overview', 'monitoring', 'console', 'players', 'files', 'plugins', 'backups', 'settings'])
    expect(serverSections(srv(all), false)).not.toContain('settings')
  })
  it('gives a server without instance only the overview', () => {
    expect(serverSections(srv(['server.view', 'server.console', 'server.files'], null), true)).toEqual(['overview'])
  })
})

describe('sectionLabelKey', () => {
  it('names plugins "Mods" on mod servers', () => {
    expect(sectionLabelKey('plugins', srv([], 'neoforge'))).toBe('servers.detail.tabs.mods')
    expect(sectionLabelKey('plugins', srv([], 'purpur'))).toBe('servers.detail.tabs.plugins')
    expect(sectionLabelKey('console', srv([]))).toBe('servers.detail.tabs.console')
  })
})

describe('sectionHref / sectionForSwitch', () => {
  it('maps sections to route paths', () => {
    expect(sectionHref('overview')).toBe('/servers/$id')
    expect(sectionHref('files')).toBe('/servers/$id/files')
  })
  it('keeps an allowed section and falls back to overview otherwise', () => {
    expect(sectionForSwitch('files', srv(['server.view', 'server.files']), false)).toBe('files')
    expect(sectionForSwitch('files', srv(['server.view']), false)).toBe('overview')
    expect(sectionForSwitch('plugins', srv(['server.view', 'server.plugins'], 'vanilla'), true)).toBe('overview')
  })
})
```

Vor dem Schreiben `Permission`-Werte und Pflichtfelder von `ServerProperties` in `web/src/api/types.ts` prüfen; der Cast `{} as …` ist nur im Test erlaubt.

- [ ] **Step 2: Fehlschlag prüfen** – `make test-vm SUITE=web` → FAIL (Modul `./sections` fehlt).

- [ ] **Step 3: Implementieren**

```ts
import type { Server } from '@/api/types'
import { pluginKindOf } from '@/features/plugins/model'

export type ServerSection = 'overview' | 'monitoring' | 'console' | 'players' | 'files' | 'plugins' | 'backups' | 'settings'

export const SECTION_ORDER: readonly ServerSection[] = ['overview', 'monitoring', 'console', 'players', 'files', 'plugins', 'backups', 'settings']

type SectionServer = Pick<Server, 'permissions' | 'instance'>

/** Spec §4.3: the only place that decides which areas of a server a user sees (tree, drawer and route guards). */
export function serverSections(server: SectionServer, isAdmin: boolean): ServerSection[] {
  if (server.instance === null) return ['overview']
  const has = (p: string) => server.permissions.includes(p as Server['permissions'][number])
  const allowed = new Set<ServerSection>(['overview', 'monitoring'])
  if (has('server.console')) {
    allowed.add('console')
    allowed.add('players')
  }
  if (has('server.files')) allowed.add('files')
  if (has('server.plugins') && pluginKindOf(server.instance.type) !== null) allowed.add('plugins')
  if (has('server.backups')) allowed.add('backups')
  if (isAdmin) allowed.add('settings')
  return SECTION_ORDER.filter((s) => allowed.has(s))
}

export function sectionLabelKey(section: ServerSection, server: Pick<Server, 'instance'>): string {
  if (section === 'plugins' && server.instance && pluginKindOf(server.instance.type) === 'mod') return 'servers.detail.tabs.mods'
  return `servers.detail.tabs.${section}`
}

export function sectionHref(section: ServerSection) {
  return section === 'overview' ? ('/servers/$id' as const) : (`/servers/$id/${section}` as const)
}

export function sectionForSwitch(current: ServerSection, target: SectionServer, isAdmin: boolean): ServerSection {
  return serverSections(target, isAdmin).includes(current) ? current : 'overview'
}
```

- [ ] **Step 4:** `make check && make test-vm SUITE=web` → PASS.
- [ ] **Step 5: Commit** – `git add web/src/features/servers/sections.ts web/src/features/servers/sections.test.ts && git commit -m "feat(web): derive server sections from permissions in one place"`

---

### Task 2: Routen, `ServerContext`, Layout und Guard

**Files:**
- Create: `web/src/features/servers/server-context.tsx`, `server-layout.tsx`, `section-guard.tsx`, `web/src/routes/_app/servers/$id/{index,monitoring,console,players,files,plugins,backups,settings}.tsx`
- Modify: `web/src/routes/_app/servers/$id.tsx`, `web/src/routes/_app/servers/new.tsx` (nur `navigate` bleibt gleich – prüfen), `web/src/routeTree.gen.ts`
- Delete: `web/src/features/servers/server-detail-page.tsx`

**Interfaces:**
- Consumes: Task 1; `serverQueryOptions`, `detailPollInterval` (unverändert); `useFileJob`, `isExtracting`, `leaveTexts`, `FilesBusy` (T4, API unverändert); `ConfirmDialog` (T1, API unverändert).
- Produces:

```ts
// server-context.tsx
export interface ServerContextValue { server: Server; isAdmin: boolean; extracting: boolean; sections: ServerSection[] }
export function ServerContextProvider(props: { value: ServerContextValue; children: ReactNode }): JSX.Element
export function useServerContext(): ServerContextValue   // wirft außerhalb des Providers
// section-guard.tsx
export function SectionGuard(props: { section: ServerSection; children: ReactNode }): JSX.Element
// server-layout.tsx
export function ServerLayout(props: { serverId: number }): JSX.Element
```

**Übergangsregeln bis T3/T4 integriert sind** (alle Props existieren heute und sind optional – geprüft in `files-tab.tsx:33-38`, `players-tab.tsx:19`):
- `files`-Route übergibt `onBusyChange` und hält selbst einen `useBlocker` (siehe unten). **Integration (Lead):** sobald T4 `onBusyChange` entfernt, schlägt `tsc` hier fehl → Route auf `<FilesTab server={server} />` reduzieren und den Blocker hier löschen.
- `files`/`players`-Routen übergeben `onOpenSettings` für Admins als Navigation zu `/servers/$id/settings`. **Integration:** entfernen, sobald T3/T4 das Prop entfernen (tsc-Fehler).
- `settings`-Route rendert `<ServerSettingsTab/>` **und darunter** `<DeleteServerSection/>`. **Integration:** sobald T4 die Gefahrenzone in `ServerSettingsTab` rendert, `<DeleteServerSection/>` hier entfernen (kein tsc-Fehler – Lead muss es aktiv tun; Test „renders exactly one delete form“ in Task 3 fängt Doppelungen).

- [ ] **Step 1: `server-context.tsx`**

```tsx
import { createContext, useContext, type ReactNode } from 'react'
import type { Server } from '@/api/types'
import type { ServerSection } from './sections'

export interface ServerContextValue {
  server: Server
  isAdmin: boolean
  extracting: boolean
  sections: ServerSection[]
}

const Ctx = createContext<ServerContextValue | null>(null)

export function ServerContextProvider({ value, children }: { value: ServerContextValue; children: ReactNode }) {
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useServerContext(): ServerContextValue {
  const value = useContext(Ctx)
  if (!value) throw new Error('useServerContext outside of ServerLayout')
  return value
}
```

- [ ] **Step 2: `section-guard.tsx`**

```tsx
import { Navigate } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import type { ServerSection } from './sections'
import { useServerContext } from './server-context'

/** A section that is not (or no longer) allowed falls back to the overview (spec §4.3), also after a revoked permission. */
export function SectionGuard({ section, children }: { section: ServerSection; children: ReactNode }) {
  const { server, sections } = useServerContext()
  if (!sections.includes(section)) return <Navigate to="/servers/$id" params={{ id: String(server.id) }} replace />
  return <>{children}</>
}
```

- [ ] **Step 3: `server-layout.tsx`** (übernimmt Laden/Fehler aus `server-detail-page.tsx`)

```tsx
import { useQuery } from '@tanstack/react-query'
import { Link, Outlet } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { isApiError } from '@/api/errors'
import { useMe } from '@/features/auth/me'
import { isExtracting, useFileJob } from '@/features/files/use-file-job'
import { errorMessage } from '@/lib/error-message'
import { serverQueryOptions } from './api'
import { serverSections } from './sections'
import { ServerContextProvider } from './server-context'
import { ServerHeader } from './server-header'
import { detailPollInterval } from './status'

/** Keyed by id in the route: switching servers must reset the header and section state. */
export function ServerLayout({ serverId }: { serverId: number }) {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const query = useQuery({
    ...serverQueryOptions(serverId),
    refetchInterval: (q) => detailPollInterval(q.state.data, q.state.error),
  })
  const filesAllowed = query.data?.permissions.includes('server.files') === true && query.data.instance !== null
  const fileJob = useFileJob(serverId, filesAllowed)

  if (query.isPending) return <p className="text-muted-foreground">{t('common.loading')}</p>
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
  const value = { server, isAdmin, extracting: isExtracting(fileJob.data), sections: serverSections(server, isAdmin) }
  return (
    <ServerContextProvider value={value}>
      <div className="flex min-w-0 flex-col gap-6">
        <ServerHeader />
        <Outlet />
      </div>
    </ServerContextProvider>
  )
}
```

`ServerHeader` entsteht in Task 3; für diesen Task vorübergehend `export function ServerHeader() { const { server } = useServerContext(); return <h1 className="pixel-heading text-xl">{server.name}</h1> }` in `server-header.tsx` anlegen.

- [ ] **Step 4: Routen**

`routes/_app/servers/$id.tsx`:

```tsx
import { createFileRoute, notFound } from '@tanstack/react-router'
import { ServerLayout } from '@/features/servers/server-layout'

export const Route = createFileRoute('/_app/servers/$id')({
  beforeLoad: ({ params }) => {
    // `/servers/abc` or `/servers/0` are no server ids – render the 404 page instead of firing requests.
    if (!/^[1-9]\d*$/.test(params.id)) throw notFound()
  },
  component: ServerRoute,
})

function ServerRoute() {
  const { id } = Route.useParams()
  return <ServerLayout key={id} serverId={Number(id)} />
}
```

`routes/_app/servers/$id/index.tsx`:

```tsx
import { createFileRoute } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { DeleteServerSection } from '@/features/servers/delete-server-section'
import { useServerContext } from '@/features/servers/server-context'
import { ServerOverview } from '@/features/servers/server-overview'

export const Route = createFileRoute('/_app/servers/$id/')({ component: OverviewRoute })

function OverviewRoute() {
  const { t } = useTranslation()
  const { server, isAdmin, extracting } = useServerContext()
  if (server.instance === null) {
    // Pre-instance servers can only be deleted (spec §4.3: their only section is the overview).
    return (
      <div className="max-w-3xl space-y-4">
        <p className="text-muted-foreground">{t('servers.detail.not_configured')}</p>
        {isAdmin ? <DeleteServerSection server={server} /> : null}
      </div>
    )
  }
  return <ServerOverview server={server} instance={server.instance} extracting={extracting} />
}
```

`routes/_app/servers/$id/monitoring.tsx` (Muster für `console`, `backups`):

```tsx
import { createFileRoute } from '@tanstack/react-router'
import { MonitoringTab } from '@/features/monitoring/monitoring-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/monitoring')({ component: MonitoringRoute })

function MonitoringRoute() {
  const { server } = useServerContext()
  return (
    <SectionGuard section="monitoring">
      <MonitoringTab server={server} />
    </SectionGuard>
  )
}
```

`console.tsx`: gleiches Muster mit `ConsoleTab` (`@/features/console/console-tab`), `section="console"`, Route `'/_app/servers/$id/console'`.
`backups.tsx`: gleiches Muster mit `BackupsTab` (`@/features/backups/backups-tab`), `section="backups"`, Route `'/_app/servers/$id/backups'`.

`players.tsx`:

```tsx
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { PlayersTab } from '@/features/players/players-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/players')({ component: PlayersRoute })

function PlayersRoute() {
  const { server, isAdmin } = useServerContext()
  const navigate = useNavigate()
  // Transitional (until T3 links itself): see plan Task 2 "Übergangsregeln".
  const openSettings = isAdmin ? () => void navigate({ to: '/servers/$id/settings', params: { id: String(server.id) } }) : undefined
  return (
    <SectionGuard section="players">
      <PlayersTab server={server} onOpenSettings={openSettings} />
    </SectionGuard>
  )
}
```

`plugins.tsx`:

```tsx
import { createFileRoute } from '@tanstack/react-router'
import { pluginKindOf } from '@/features/plugins/model'
import { PluginsTab } from '@/features/plugins/plugins-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/plugins')({ component: PluginsRoute })

function PluginsRoute() {
  const { server } = useServerContext()
  const kind = server.instance ? pluginKindOf(server.instance.type) : null
  return <SectionGuard section="plugins">{kind ? <PluginsTab server={server} kind={kind} /> : null}</SectionGuard>
}
```

`files.tsx` (Übergangs-Blocker, siehe oben):

```tsx
import { createFileRoute, useBlocker, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { leaveTexts, type FilesBusy } from '@/features/files/busy'
import { FilesTab } from '@/features/files/files-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/files')({ component: FilesRoute })

function FilesRoute() {
  const { t } = useTranslation()
  const { server, isAdmin } = useServerContext()
  const navigate = useNavigate()
  const [busy, setBusy] = useState<FilesBusy | null>(null)
  // Transitional (until T4 moves this into FilesTab): leaving with unsaved edits or running uploads asks first – on any navigation.
  const blocker = useBlocker({ shouldBlockFn: () => busy !== null, enableBeforeUnload: () => busy !== null, withResolver: true })
  const leave = leaveTexts(busy ?? { unsaved: true, uploads: false }, t)
  const openSettings = isAdmin ? () => void navigate({ to: '/servers/$id/settings', params: { id: String(server.id) } }) : undefined
  return (
    <SectionGuard section="files">
      <FilesTab server={server} onBusyChange={setBusy} onOpenSettings={openSettings} />
      <ConfirmDialog
        open={blocker.status === 'blocked'}
        onOpenChange={(open) => (open ? undefined : blocker.reset?.())}
        title={leave.title}
        description={leave.description}
        confirmLabel={leave.confirm}
        destructive
        onConfirm={() => blocker.proceed?.()}
      />
    </SectionGuard>
  )
}
```

Vor dem Schreiben `busy.ts` (`FilesBusy`-Felder) und die Signatur von `ConfirmDialog` lesen und Prop-Namen angleichen.

`settings.tsx`:

```tsx
import { createFileRoute } from '@tanstack/react-router'
import { DeleteServerSection } from '@/features/servers/delete-server-section'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'
import { ServerSettingsTab } from '@/features/servers/server-settings-tab'

export const Route = createFileRoute('/_app/servers/$id/settings')({ component: SettingsRoute })

function SettingsRoute() {
  const { server } = useServerContext()
  return (
    <SectionGuard section="settings">
      {server.instance ? (
        <div className="flex flex-col gap-6">
          <ServerSettingsTab server={server} instance={server.instance} />
          {/* Transitional: T4 moves the danger zone into ServerSettingsTab – then remove this line (plan Task 2). */}
          <DeleteServerSection server={server} />
        </div>
      ) : null}
    </SectionGuard>
  )
}
```

`server-detail-page.tsx` löschen.

- [ ] **Step 5: Route-Tree regenerieren**

```bash
cd web && node --input-type=module -e "
import { Generator, getConfig } from '@tanstack/router-generator'
const root = process.cwd()
const config = getConfig({ target: 'react', autoCodeSplitting: true, routeFileIgnorePattern: '\\\\.test\\\\.' }, root)
await new Generator({ config, root }).run()
"
git diff --stat src/routeTree.gen.ts
```

Expected: `routeTree.gen.ts` enthält `/_app/servers/$id/console` usw. Falls die Generator-API abweicht: `npx vite --port 5199` starten, warten bis „Generated route tree“ erscheint, mit Ctrl-C beenden.

- [ ] **Step 6:** `cd .. && make check` → grün (Route-Typen inkl. `'/servers/$id/settings'` gültig).
- [ ] **Step 7: Commit** – `git add -A web/src/routes web/src/routeTree.gen.ts web/src/features/servers && git commit -m "feat(web): turn server tabs into routes under a server layout"`

---

### Task 3: Server-Kopf, Übersicht und Layout-Tests

**Files:**
- Modify: `web/src/features/servers/server-header.tsx` (komplett), `server-overview.tsx`, `status-badge.tsx`, `address-line.tsx`, `power-buttons.tsx`
- Create: `web/src/features/servers/test-utils.tsx`
- Move: `server-detail-page.test.tsx` → `server-layout.test.tsx` (umbauen)
- Modify: `web/src/i18n/de.ts`, `en.ts` (`servers.header.*`)

**Interfaces:**
- Consumes: `StatSlot`, `PageHeader` nicht (Kopf ist eigen), `monitoringOverviewQueryOptions`, `OVERVIEW_POLL_MS` (`@/features/monitoring/api`), `formatDecimal` (`@/features/monitoring/model`), `formatGiB`, `formatCpu`, `describeInstance`, `javaLabel` (`./format`), `OnlinePlayersCard` (T3, `{ serverId }` unverändert).
- Produces: `ServerHeader()` (liest Context, keine Props); `renderApp(path: string, me: Me | null): Promise<{ router; queryClient }>` in `test-utils.tsx`; `PowerButtons` bekommt `compact?: boolean` (Icon-Buttons mit `aria-label`, sonst wie bisher).

- [ ] **Step 1: i18n**

`de.ts` unter `servers`:

```ts
    header: {
      players: 'Spieler',
      tps: 'TPS',
      memory: 'RAM',
      cpu: 'CPU',
      memory_value: '{{used}} / {{limit}} GiB',
      players_value: '{{players}}/{{max}}',
    },
```

`en.ts` analog: `players: 'Players', tps: 'TPS', memory: 'RAM', cpu: 'CPU', memory_value: '{{used}} / {{limit}} GiB', players_value: '{{players}}/{{max}}'`. `servers.overview.memory_assigned: 'Zugewiesen'` / `'Assigned'` ergänzen.

- [ ] **Step 2: `test-utils.tsx`**

```tsx
import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render } from '@testing-library/react'
import type { Me } from '@/api/types'
import { routerNavigator, wireApi } from '@/app/wire-api'
import { applyMe } from '@/features/auth/me'
import { createAppRouter } from '@/router'
import { createTestQueryClient } from '@/test/render'

/** Full app (real route tree, guards and AppLayout) at `path` – for the server layout, its sections and the tree. */
export async function renderApp(path: string, me: Me | null, queryClient = createTestQueryClient()) {
  if (me) applyMe(queryClient, me)
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  wireApi(queryClient, routerNavigator(router))
  await router.load()
  const result = render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { ...result, router, queryClient }
}
```

- [ ] **Step 3: Failing tests (`server-layout.test.tsx`)**

Datei per `git mv web/src/features/servers/server-detail-page.test.tsx web/src/features/servers/server-layout.test.tsx` umziehen, dann umbauen:

1. `renderWithProviders(<ServerDetailPage serverId={N} />, { path, me })` → `renderApp('/servers/N', me)`; für Bereiche direkt `renderApp('/servers/1/settings', …)`.
2. Tab-Klicks → Link im Baum: Helfer am Dateianfang

```ts
function sectionLinks(name: string) {
  return within(screen.getByRole('list', { name: `Bereiche von ${name}` })).getAllByRole('link')
}
async function openSection(name: string, label: string) {
  const user = userEvent.setup()
  await user.click(within(await screen.findByRole('list', { name: `Bereiche von ${name}` })).getByRole('link', { name: label, exact: true }))
}
```

3. `expect(screen.getAllByRole('tab').map(…)).toEqual([...9])` → `expect(sectionLinks('survival').map((l) => l.textContent)).toEqual(['Übersicht', 'Monitoring', 'Konsole', 'Spieler', 'Dateien', 'Plugins', 'Backups', 'Einstellungen'])` (kein „Löschen“ mehr).
4. Die Prüfungen auf `region 'Zustand'` für Status und Adresse wandern in den Kopf: `const header = screen.getByRole('region', { name: 'survival' })` → `within(header).getByText('Läuft')`, `…getByText('survival.mc.example.com')`, `…getByText('2 / 5 GiB')` (RAM-Slot), `…getByText('12,5 %')` (CPU-Slot). In `region 'Zustand'` bleiben `Paper 1.21.11`, `Java 21`, `vor 5 Minuten`.
5. Test „lets the tab bar scroll sideways“ ersatzlos streichen (keine Tab-Leiste mehr).
6. „deletes the server…“ / „deletes the backups too…“ / „explains a 409…“ / „links the purge hint…“: statt Tab „Löschen“ direkt `renderApp('/servers/1/settings', loginAs('admin', { sudo: true }))`.
7. „saves settings…“: `renderApp('/servers/1/settings', …)`, am Ende `await openSection('survival', 'Übersicht')`.
8. `queryRole('tab', { name: X })` → `expect(within(screen.getByRole('list', { name: 'Bereiche von survival' })).queryByRole('link', { name: X, exact: true })).not.toBeInTheDocument()`.

Zusätzliche neue Tests:

```tsx
  it('redirects a section the user may not open to the overview', async () => {
    const { router } = await renderApp('/servers/1/files', loginAs('alice'))
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/1'))
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
  })

  it('leaves a section whose permission is revoked while it is open', async () => {
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.console'] }])
    const { router, queryClient } = await renderApp('/servers/1/console', loginAs('alice'))
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/1/console'))
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view'] }])
    await queryClient.refetchQueries({ queryKey: ['servers', 1] })
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/1'))
  })

  it('keeps the section after a reload', async () => {
    const { router } = await renderApp('/servers/1/backups', loginAs('admin'))
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/servers/1/backups')
  })

  it('renders exactly one delete form in the settings', async () => {
    await renderApp('/servers/1/settings', loginAs('admin', { sudo: true }))
    expect(await screen.findAllByRole('button', { name: 'Server löschen' })).toHaveLength(1)
  })

  it('shows header slots with a dash for missing monitoring values', async () => {
    server.use(http.get('*/api/monitoring/overview', () => HttpResponse.json({ servers: [], host: null })))
    await renderApp('/servers/2', loginAs('admin'))
    const header = await screen.findByRole('region', { name: 'creative' })
    expect(within(header).getByText('Spieler').parentElement).toHaveTextContent('–')
    expect(within(header).getByText('TPS').parentElement).toHaveTextContent('–')
  })
```

Prüfen, ob `alice` im Mock ein Grant auf Server 1 ohne `server.files` hat (`grep -n "grants" web/src/mocks/db.ts`); sonst den ersten Test mit `db.grants.set(2, [{ server_id: 1, permissions: ['server.view'] }])` beginnen.

- [ ] **Step 4: Fehlschlag prüfen** – `make test-vm SUITE=web` → FAIL (Kopf ohne Slots/Region).

- [ ] **Step 5: `server-header.tsx`**

```tsx
import { useQuery } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { StatSlot } from '@/components/stat-slot'
import { Button } from '@/components/ui/button'
import { monitoringOverviewQueryOptions, OVERVIEW_POLL_MS } from '@/features/monitoring/api'
import { formatDecimal } from '@/features/monitoring/model'
import { useFormat } from '@/lib/format'
import { AddressLine } from './address-line'
import { formatCpu, formatGiB } from './format'
import { PowerButtons } from './power-buttons'
import { useServerContext } from './server-context'
import { StatusBadge } from './status-badge'
import { usePower } from './use-power'

/** Spec §4.4: always visible above every section of a server. */
export function ServerHeader() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const { server, isAdmin, extracting } = useServerContext()
  const { status, instance } = server
  const overview = useQuery({ ...monitoringOverviewQueryOptions, refetchInterval: OVERVIEW_POLL_MS, enabled: instance !== null })
  const metrics = overview.data?.servers.find((s) => s.id === server.id)
  const canPower = server.permissions.includes('server.power')
  const running = status.state === 'running' && status.memory_limit_bytes > 0

  const players = metrics && metrics.players !== null ? t('servers.header.players_value', { players: metrics.players, max: metrics.players_max ?? '–' }) : null
  const tps = metrics && metrics.tps !== null ? formatDecimal(metrics.tps, fmt.locale) : null
  const memory = running
    ? t('servers.header.memory_value', { used: formatGiB(status.memory_bytes, fmt.locale), limit: formatGiB(status.memory_limit_bytes, fmt.locale) })
    : null
  const cpu = running ? t('servers.cpu_value', { value: formatCpu(status.cpu_percent, fmt.locale) }) : null

  return (
    <section aria-labelledby={id} className="flex flex-col gap-4">
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          <h1 id={id} className="pixel-heading text-xl break-words md:text-2xl">
            {server.name}
          </h1>
          <StatusBadge state={status.state} />
          {instance ? <AddressLine address={server.address} /> : null}
        </div>
        {instance && canPower ? <PowerButtons server={server} extracting={extracting} /> : null}
      </div>
      {instance ? (
        <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
          <StatSlot label={t('servers.header.players')} value={players} />
          <StatSlot label={t('servers.header.tps')} value={tps} />
          <StatSlot label={t('servers.header.memory')} value={memory} bar={running ? status.memory_bytes / status.memory_limit_bytes : null} />
          <StatSlot label={t('servers.header.cpu')} value={cpu} />
        </div>
      ) : null}
      {status.restart_required ? <RestartBanner canPower={canPower} /> : null}
      {status.last_error && isAdmin ? (
        <div role="alert" className="border-destructive/60 bg-card space-y-1 border p-3 text-sm">
          <p className="font-medium">{t('servers.overview.last_error')}</p>
          <p className="font-mono break-words">{status.last_error}</p>
        </div>
      ) : status.state === 'error' ? (
        <div role="alert" className="border-destructive/60 bg-card border p-3 text-sm">
          <p>{t('servers.overview.error_hidden')}</p>
        </div>
      ) : null}
    </section>
  )
}

function RestartBanner({ canPower }: { canPower: boolean }) {
  const { t } = useTranslation()
  const { server, extracting } = useServerContext()
  const power = usePower(server)
  return (
    <div role="status" className="bevel-inset flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="space-y-1">
        <p className="text-status-starting font-medium">{t('servers.restart_required')}</p>
        <p className="text-muted-foreground text-sm">{t('servers.restart.description')}</p>
      </div>
      {canPower ? (
        <Button type="button" disabled={power.isPending || extracting} onClick={() => power.mutate('restart')}>
          {t('servers.restart.now')}
        </Button>
      ) : null}
    </div>
  )
}
```

Hinweis: Heute zeigt die API `last_error` nur Admins; die Bedingung `isAdmin` ist Absicherung. Den Test „shows other users a generic error“ beibehalten.

- [ ] **Step 6: `server-overview.tsx`** – `RestartBanner`, Status-, Adress-, Power- und Fehlerzeilen entfernen (jetzt im Kopf). Neu:

```tsx
export function ServerOverview({ server, instance }: { server: Server; instance: InstanceConfig; extracting?: boolean }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const { status } = server
  const running = status.state === 'running'
  return (
    <div className="grid gap-6 xl:grid-cols-2">
      {status.state === 'unknown' ? <p className="text-muted-foreground text-sm xl:col-span-2">{t('servers.overview.unknown_hint')}</p> : null}
      <Section title={t('servers.overview.title')}>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-4 gap-y-2 text-sm">
          <dt className="text-muted-foreground">{t('servers.overview.type')}</dt>
          <dd>{describeInstance(instance, t)}</dd>
          <dt className="text-muted-foreground">{t('servers.overview.java')}</dt>
          <dd>{javaLabel(instance.java)}</dd>
          <dt className="text-muted-foreground">{t('servers.overview.memory_assigned')}</dt>
          <dd className="tabular-nums">{formatGiB(instance.memory_mib * 1024 ** 2, fmt.locale)} GiB</dd>
          <dt className="text-muted-foreground">{t('servers.overview.started')}</dt>
          <dd>{running && status.started_at > 0 ? fmt.relative(status.started_at) : '–'}</dd>
          {status.operation ? (
            <>
              <dt className="text-muted-foreground">{t('servers.overview.status')}</dt>
              <dd>{t(`servers.operation.${status.operation}`)}</dd>
            </>
          ) : null}
        </dl>
      </Section>
      <OnlinePlayersCard serverId={server.id} />
      {!server.permissions.includes('server.power') ? <p className="text-muted-foreground text-sm">{t('servers.no_power')}</p> : null}
    </div>
  )
}
```

Imports: `Section` aus `@/components/section` statt `SectionCard`. Der Hinweis „Keine Berechtigung für Start/Stopp.“ bleibt in der Übersicht (Test „restricted user“).

- [ ] **Step 7: Stil der kleinen Bausteine**
  - `status-badge.tsx`: Badge bekommt einen Status-Punkt: `<Badge variant={…}><span aria-hidden className={cn('size-1.5', dotClass(state))} />{label}</Badge>` mit `dotClass`: running → `bg-status-online`, error → `bg-status-error`, stopped/not_configured → `bg-status-offline`, sonst `bg-status-starting`.
  - `address-line.tsx`: Adresse als `font-mono text-sm text-primary bg-surface-2 px-1.5 py-0.5`.
  - `power-buttons.tsx`: Prop `compact?: boolean`; ohne `compact` wie bisher, aber `restart` → `variant="default"`, `start` → `variant="default"`, `stop` → `variant="outline"`; mit `compact` `size="icon-sm"`, Icons `Play`/`Square`/`RotateCw` aus lucide, `aria-label={t(`servers.power.${action}`)}` und `title` gleich. Gruppe und Texte unverändert (Tests in T3 nutzen „Starten“, „Stoppen“, „Neu starten“ als Namen).

- [ ] **Step 8:** `make check && make test-vm SUITE=web` → PASS.
- [ ] **Step 9: Commit** – `git add -A web/src/features/servers web/src/i18n && git commit -m "feat(web): add server header with stat slots above every section"`

---

### Task 4: App-Rahmen – Icon-Leiste, Server-Baum, Topbar

**Files:**
- Create: `web/src/features/layout/{icon-rail,server-tree,breadcrumb}.tsx`, `tree-state.ts`, `tree-state.test.ts`
- Modify: `web/src/features/layout/{app-layout.tsx,nav.ts,app-layout.test.tsx}`, `web/src/i18n/{de,en}.ts` (`nav.*`)

**Interfaces:**
- Consumes: `serversQueryOptions`, `pollInterval`, `stateLabelKey` (eigene), `serverSections`, `sectionHref`, `sectionLabelKey`, `sectionForSwitch` (Task 1), `PixelIcon` (Welle 0), `ThemeToggle` (T1, API unverändert), `useLogout`, `useMe`.
- Produces:

```ts
// tree-state.ts
export const TREE_STORAGE_KEY = 'mcpanel.tree'
export function readTreeOpen(): boolean          // Standard true, Fehler → true
export function writeTreeOpen(open: boolean): void // Fehler ignoriert
// nav.ts
export interface NavItem { to: NavPath; labelKey: string; icon: LucideIcon; adminOnly: boolean; placement: 'top' | 'bottom' }
export function navItemsFor(role: Role, placement?: 'top' | 'bottom'): NavItem[]
// server-tree.tsx
export function ServerTree(props: { onNavigate?: () => void }): JSX.Element  // <nav aria-label="Server">
// icon-rail.tsx
export function IconRail(props: { onToggleTree?: () => void; treeOpen?: boolean }): JSX.Element // <nav aria-label="Hauptnavigation">
// breadcrumb.tsx
export function Breadcrumb(): JSX.Element
```

- [ ] **Step 1: i18n (`nav.*`)**

`de.ts`:

```ts
  nav: {
    label: 'Hauptnavigation',
    dashboard: 'Server',
    users: 'Benutzer',
    security: 'Security Center',
    audit: 'Audit-Log',
    settings: 'Einstellungen',
    profile: 'Profil',
    open_menu: 'Menü öffnen',
    menu_description: 'Navigation zu allen Bereichen des Panels',
    skip: 'Zum Inhalt springen',
    signed_in_as: 'Angemeldet als {{username}}',
    servers: 'Server',
    server_sections: 'Bereiche von {{name}}',
    new_server: 'Neuer Server',
    tree_collapse: 'Serverliste einklappen',
    tree_expand: 'Serverliste ausklappen',
    open_servers: 'Serverliste öffnen',
    breadcrumb: 'Brotkrume',
    home: 'Startseite',
    no_servers: 'Noch keine Server.',
  },
```

`en.ts` analog (`dashboard: 'Servers'`, `servers: 'Servers'`, `server_sections: 'Sections of {{name}}'`, `new_server: 'New server'`, `tree_collapse: 'Collapse server list'`, `tree_expand: 'Expand server list'`, `open_servers: 'Open server list'`, `breadcrumb: 'Breadcrumb'`, `home: 'Home'`, `no_servers: 'No servers yet.'`).

- [ ] **Step 2: Failing tests**

`tree-state.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from 'vitest'
import { readTreeOpen, TREE_STORAGE_KEY, writeTreeOpen } from './tree-state'

afterEach(() => {
  vi.restoreAllMocks()
  localStorage.clear()
})

describe('tree state', () => {
  it('defaults to open and round-trips', () => {
    expect(readTreeOpen()).toBe(true)
    writeTreeOpen(false)
    expect(localStorage.getItem(TREE_STORAGE_KEY)).toBe('closed')
    expect(readTreeOpen()).toBe(false)
  })
  it('stays usable when storage throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    expect(readTreeOpen()).toBe(true)
    expect(() => writeTreeOpen(false)).not.toThrow()
  })
})
```

`app-layout.test.tsx` neu (nutzt `renderApp` aus Task 3, damit Baum und Kind-Routen echt sind):

```tsx
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { renderApp } from '@/features/servers/test-utils'
import { db, loginAs } from '@/mocks/db'
import { setViewport } from '@/test/media'

describe('AppLayout', () => {
  it('shows the full rail to admins', async () => {
    await renderApp('/', loginAs('admin'))
    const nav = await screen.findByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).getAllByRole('link').map((l) => l.getAttribute('aria-label'))).toEqual([
      'Startseite',
      'Server',
      'Benutzer',
      'Security Center',
      'Audit-Log',
      'Einstellungen',
      'Profil',
    ])
    expect(screen.getByRole('link', { name: 'Zum Inhalt springen' })).toHaveAttribute('href', '#main')
  })

  it('hides admin areas from regular users', async () => {
    await renderApp('/', loginAs('alice'))
    const nav = await screen.findByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).getAllByRole('link').map((l) => l.getAttribute('aria-label'))).toEqual(['Startseite', 'Server', 'Profil'])
  })

  it('marks the current page', async () => {
    await renderApp('/profile', loginAs('alice'))
    const nav = await screen.findByRole('navigation', { name: 'Hauptnavigation' })
    expect(within(nav).getByRole('link', { name: 'Profil' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Server' })).not.toHaveAttribute('aria-current')
  })

  it('lists permitted servers in the tree and expands the active one', async () => {
    await renderApp('/servers/1', loginAs('alice'))
    const tree = await screen.findByRole('navigation', { name: 'Server' })
    expect(within(tree).getByRole('link', { name: 'survival', exact: true })).toHaveAttribute('aria-current', 'page')
    expect(within(tree).getByRole('link', { name: 'creative', exact: true })).toBeInTheDocument()
    expect(within(tree).queryByRole('link', { name: 'lobby', exact: true })).not.toBeInTheDocument()
    expect(within(tree).getByRole('list', { name: 'Bereiche von survival' })).toBeInTheDocument()
    expect(within(tree).queryByRole('list', { name: 'Bereiche von creative' })).not.toBeInTheDocument()
  })

  it('keeps the section when switching servers if it is allowed, else opens the overview', async () => {
    const user = userEvent.setup()
    const { router } = await renderApp('/servers/1/files', loginAs('admin'))
    const tree = await screen.findByRole('navigation', { name: 'Server' })
    expect(within(tree).getByRole('link', { name: 'creative', exact: true })).toHaveAttribute('href', '/servers/2/files')
    await user.click(within(tree).getByRole('link', { name: 'creative', exact: true }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/2/files'))
    // modded (id 4) is a mod server: plugins allowed; vanilla creative has no plugins section
    await user.click(within(tree).getByRole('link', { name: 'Backups', exact: true }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/2/backups'))
  })

  it('collapses the tree and remembers it', async () => {
    const user = userEvent.setup()
    await renderApp('/', loginAs('admin'))
    await user.click(await screen.findByRole('button', { name: 'Serverliste einklappen' }))
    expect(screen.queryByRole('navigation', { name: 'Server' })).not.toBeInTheDocument()
    expect(localStorage.getItem('mcpanel.tree')).toBe('closed')
    await user.click(screen.getByRole('button', { name: 'Serverliste ausklappen' }))
    expect(screen.getByRole('navigation', { name: 'Server' })).toBeInTheDocument()
  })

  it('offers admins a new-server link in the tree', async () => {
    await renderApp('/', loginAs('admin'))
    const tree = await screen.findByRole('navigation', { name: 'Server' })
    expect(within(tree).getByRole('link', { name: 'Neuer Server' })).toHaveAttribute('href', '/servers/new')
  })

  it('shows a breadcrumb for server sections', async () => {
    await renderApp('/servers/1/console', loginAs('admin'))
    const crumb = await screen.findByRole('navigation', { name: 'Brotkrume' })
    await waitFor(() => expect(crumb).toHaveTextContent('survival/Konsole'))
  })

  it('uses a drawer with rail and tree on phones', async () => {
    setViewport('mobile')
    const user = userEvent.setup()
    const { router } = await renderApp('/', loginAs('admin'))
    expect(screen.queryByRole('navigation', { name: 'Hauptnavigation' })).not.toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: 'Menü öffnen' }))
    const sheet = await screen.findByRole('dialog', { name: 'mc-panel' })
    expect(within(sheet).getByRole('navigation', { name: 'Server' })).toBeInTheDocument()
    await user.click(within(sheet).getByRole('link', { name: 'Profil' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/profile'))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('signs out', async () => {
    const user = userEvent.setup()
    const { router } = await renderApp('/', loginAs('admin'))
    await user.click(await screen.findByRole('button', { name: 'Abmelden' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(db.currentSessionId).toBeNull()
  })
})
```

Mock-Daten vorher prüfen (`grep -n "name: '" web/src/mocks/db.ts | head`): Server-IDs/Namen (1 survival, 2 creative, 3 lobby, 4 modded) und ob `creative` ein `files`-Recht für admin hat (Admins haben alle Rechte). Erwartungen ggf. an die echten Mock-Werte anpassen. Setzt `test/setup.ts` das Viewport nach jedem Test zurück? (`grep -n setViewport web/src/test/setup.ts`) – sonst `afterEach(() => setViewport('desktop'))` ergänzen. Tablet (md, Flyout) ist mit `test/media.ts` nicht abbildbar (nur `desktop`/`mobile`); siehe „Requests to lead“ – bis dahin nur per Screenshot geprüft.

- [ ] **Step 3: Fehlschlag prüfen** – `make test-vm SUITE=web` → FAIL.

- [ ] **Step 4: `tree-state.ts`**

```ts
export const TREE_STORAGE_KEY = 'mcpanel.tree'

export function readTreeOpen(): boolean {
  try {
    return localStorage.getItem(TREE_STORAGE_KEY) !== 'closed'
  } catch {
    return true
  }
}

export function writeTreeOpen(open: boolean): void {
  try {
    localStorage.setItem(TREE_STORAGE_KEY, open ? 'open' : 'closed')
  } catch {
    // storage unavailable (private mode) – the choice only lasts for this page view
  }
}
```

- [ ] **Step 5: `nav.ts`**

```ts
import { LayoutGrid, ScrollText, Settings, ShieldCheck, UserRound, Users, type LucideIcon } from 'lucide-react'
import type { Role } from '@/api/types'

export type NavPath = '/' | '/users' | '/security' | '/audit' | '/settings' | '/profile'

export interface NavItem {
  to: NavPath
  labelKey: string
  icon: LucideIcon
  adminOnly: boolean
  placement: 'top' | 'bottom'
}

export const NAV_ITEMS: readonly NavItem[] = [
  { to: '/', labelKey: 'nav.dashboard', icon: LayoutGrid, adminOnly: false, placement: 'top' },
  { to: '/users', labelKey: 'nav.users', icon: Users, adminOnly: true, placement: 'top' },
  { to: '/security', labelKey: 'nav.security', icon: ShieldCheck, adminOnly: true, placement: 'top' },
  { to: '/audit', labelKey: 'nav.audit', icon: ScrollText, adminOnly: true, placement: 'top' },
  { to: '/settings', labelKey: 'nav.settings', icon: Settings, adminOnly: true, placement: 'top' },
  { to: '/profile', labelKey: 'nav.profile', icon: UserRound, adminOnly: false, placement: 'bottom' },
]

export function navItemsFor(role: Role, placement?: 'top' | 'bottom'): NavItem[] {
  return NAV_ITEMS.filter((item) => (!item.adminOnly || role === 'admin') && (!placement || item.placement === placement))
}
```

- [ ] **Step 6: `icon-rail.tsx`**

```tsx
import { Link } from '@tanstack/react-router'
import { LogOut, PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { PixelIcon } from '@/components/pixel-icon'
import { ThemeToggle } from '@/components/theme-toggle'
import { Button } from '@/components/ui/button'
import { useMe } from '@/features/auth/me'
import { useLogout } from '@/features/auth/use-logout'
import { cn } from '@/lib/utils'
import { navItemsFor, type NavItem } from './nav'

const ITEM =
  'flex size-10 items-center justify-center text-muted-foreground outline-none hover:bg-accent hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring'

function RailLink({ item, onNavigate }: { item: NavItem; onNavigate?: () => void }) {
  const { t } = useTranslation()
  const Icon = item.icon
  const label = t(item.labelKey)
  return (
    <li>
      <Link
        to={item.to}
        aria-label={label}
        title={label}
        activeOptions={{ exact: item.to === '/', includeSearch: false }}
        activeProps={{ 'aria-current': 'page', className: 'bg-primary text-primary-foreground bevel-primary hover:bg-primary hover:text-primary-foreground' }}
        onClick={onNavigate}
        className={ITEM}
      >
        <Icon className="size-5" aria-hidden="true" />
      </Link>
    </li>
  )
}

/** Spec §4.1: 56 px icon rail; labels are aria-labels and tooltips (the e2e suite clicks them by name). */
export function IconRail({ treeOpen, onToggleTree, onNavigate }: { treeOpen?: boolean; onToggleTree?: () => void; onNavigate?: () => void }) {
  const { t } = useTranslation()
  const me = useMe()
  const logout = useLogout()
  const role = me?.role ?? 'user'
  return (
    <div className="flex h-full w-14 flex-col items-center gap-2 border-r-2 border-[var(--sidebar-border)] bg-[var(--rail)] py-3">
      <nav aria-label={t('nav.label')} className="flex flex-1 flex-col items-center">
        <ul className="flex flex-col items-center gap-2">
          <li>
            <Link to="/" aria-label={t('nav.home')} title={t('nav.home')} onClick={onNavigate} className="mb-2 block p-1 outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <PixelIcon kind="grass" size={28} />
            </Link>
          </li>
          {navItemsFor(role, 'top').map((item) => (
            <RailLink key={item.to} item={item} onNavigate={onNavigate} />
          ))}
        </ul>
        <ul className="mt-auto flex flex-col items-center gap-2">
          {navItemsFor(role, 'bottom').map((item) => (
            <RailLink key={item.to} item={item} onNavigate={onNavigate} />
          ))}
        </ul>
      </nav>
      {onToggleTree ? (
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={treeOpen ? t('nav.tree_collapse') : t('nav.tree_expand')}
          title={treeOpen ? t('nav.tree_collapse') : t('nav.tree_expand')}
          onClick={onToggleTree}
        >
          {treeOpen ? <PanelLeftClose aria-hidden="true" /> : <PanelLeftOpen aria-hidden="true" />}
        </Button>
      ) : null}
      <ThemeToggle />
      <Button
        type="button"
        variant="ghost"
        size="icon"
        aria-label={t('auth.logout')}
        title={t('auth.logout')}
        disabled={logout.isPending}
        onClick={() => logout.mutate()}
        className={cn('text-muted-foreground')}
      >
        <LogOut aria-hidden="true" />
      </Button>
    </div>
  )
}
```

Hinweis: Der Grasblock-Link heißt „Startseite“, der Eintrag `/` „Server“ – beide führen zur Startseite; `activeOptions.exact` nur beim Eintrag „Server“ setzt `aria-current`, damit nicht zwei Links aktiv sind (der Grasblock-Link bekommt kein `activeProps`).

- [ ] **Step 7: `server-tree.tsx`**

```tsx
import { useQuery } from '@tanstack/react-query'
import { Link, useParams, useRouterState } from '@tanstack/react-router'
import { Plus } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { Server } from '@/api/types'
import { useMe } from '@/features/auth/me'
import { serversQueryOptions } from '@/features/servers/api'
import { sectionForSwitch, sectionHref, sectionLabelKey, serverSections, SECTION_ORDER, type ServerSection } from '@/features/servers/sections'
import { pollInterval, stateLabelKey } from '@/features/servers/status'
import { cn } from '@/lib/utils'

const ROW = 'flex min-w-0 items-center gap-2 px-2 py-1.5 text-sm outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring'
const ACTIVE = 'bg-accent text-accent-foreground shadow-[inset_3px_0_0_var(--primary)]'

function currentSection(pathname: string): ServerSection {
  const last = pathname.split('/')[3] as ServerSection | undefined
  return last && SECTION_ORDER.includes(last) ? last : 'overview'
}

function dotClass(server: Server): string {
  const s = server.status.state
  if (s === 'running') return 'bg-status-online shadow-[0_0_6px_var(--status-online)]'
  if (s === 'error') return 'bg-status-error'
  if (s === 'stopped' || s === 'not_configured') return 'bg-status-offline'
  return 'bg-status-starting'
}

/** Spec §4.1: every visible server; the active one is expanded with its sections. Shares the dashboard's servers query. */
export function ServerTree({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useTranslation()
  const me = useMe()
  const isAdmin = me?.role === 'admin'
  const servers = useQuery({ ...serversQueryOptions, refetchInterval: (q) => pollInterval(q.state.data) })
  const params = useParams({ strict: false }) as { id?: string }
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const activeId = params.id ? Number(params.id) : null
  const section = currentSection(pathname)

  return (
    <nav aria-label={t('nav.servers')} className="flex h-full w-56 flex-col gap-1 overflow-y-auto border-r-2 border-[var(--sidebar-border)] bg-sidebar p-2">
      <p className="pixel-heading text-muted-foreground px-2 pt-1 pb-2 text-xs">{t('nav.servers')}</p>
      {servers.data && servers.data.length === 0 ? <p className="text-muted-foreground px-2 text-sm">{t('nav.no_servers')}</p> : null}
      <ul className="flex flex-col gap-0.5">
        {(servers.data ?? []).map((server) => {
          const active = server.id === activeId
          const target = active ? 'overview' : sectionForSwitch(section, server, isAdmin)
          return (
            <li key={server.id}>
              <Link
                to={sectionHref(target)}
                params={{ id: String(server.id) }}
                aria-current={active && section === 'overview' ? 'page' : undefined}
                onClick={onNavigate}
                className={cn(ROW, active && ACTIVE)}
              >
                {/* Status only as tooltip: the link name must stay exactly the server name (e2e and tests select by it). */}
                <span aria-hidden="true" title={t(stateLabelKey(server.status.state))} className={cn('size-2 shrink-0', dotClass(server))} />
                <span className="truncate">{server.name}</span>
              </Link>
              {active ? (
                <ul aria-label={t('nav.server_sections', { name: server.name })} className="mt-0.5 mb-1 ml-4 flex flex-col gap-0.5 border-l border-border pl-2">
                  {serverSections(server, isAdmin).map((s) => (
                    <li key={s}>
                      <Link
                        to={sectionHref(s)}
                        params={{ id: String(server.id) }}
                        aria-current={s === section ? 'page' : undefined}
                        onClick={onNavigate}
                        className={cn(ROW, 'py-1', s === section ? 'text-foreground font-semibold' : 'text-muted-foreground')}
                      >
                        {t(sectionLabelKey(s, server))}
                      </Link>
                    </li>
                  ))}
                </ul>
              ) : null}
            </li>
          )
        })}
      </ul>
      {isAdmin ? (
        <Link to="/servers/new" onClick={onNavigate} className={cn(ROW, 'text-primary mt-2')}>
          <Plus className="size-4" aria-hidden="true" />
          {t('nav.new_server')}
        </Link>
      ) : null}
    </nav>
  )
}
```

- [ ] **Step 8: `breadcrumb.tsx`**

```tsx
import { useQuery } from '@tanstack/react-query'
import { Link, useParams, useRouterState } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { serverQueryOptions } from '@/features/servers/api'
import { sectionLabelKey, SECTION_ORDER, type ServerSection } from '@/features/servers/sections'
import { NAV_ITEMS } from './nav'

export function Breadcrumb() {
  const { t } = useTranslation()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const params = useParams({ strict: false }) as { id?: string }
  // Same key as the server layout: served from its cache, no extra request.
  const server = useQuery({ ...serverQueryOptions(Number(params.id ?? 0)), enabled: !!params.id }).data
  const parts: { label: string; to?: string; params?: { id: string } }[] = []
  if (params.id) {
    const seg = pathname.split('/')[3] as ServerSection | undefined
    const section: ServerSection = seg && SECTION_ORDER.includes(seg) ? seg : 'overview'
    parts.push({ label: server?.name ?? `#${params.id}`, to: '/servers/$id', params: { id: params.id } })
    if (server) parts.push({ label: t(sectionLabelKey(section, server)) })
  } else {
    const item = NAV_ITEMS.find((i) => i.to !== '/' && pathname.startsWith(i.to))
    parts.push({ label: t(item ? item.labelKey : 'nav.dashboard') })
  }
  return (
    <nav aria-label={t('nav.breadcrumb')} className="min-w-0 truncate font-mono text-xs">
      {parts.map((p, i) => (
        <span key={p.label}>
          {i > 0 ? <span className="text-muted-foreground px-1">/</span> : null}
          {p.to ? (
            <Link to={p.to} params={p.params} className="hover:text-foreground text-muted-foreground">
              {p.label}
            </Link>
          ) : (
            <span className="text-foreground">{p.label}</span>
          )}
        </span>
      ))}
    </nav>
  )
}
```

- [ ] **Step 9: `app-layout.tsx`**

```tsx
import { Outlet } from '@tanstack/react-router'
import { Menu, Server as ServerIcon } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from '@/components/ui/sheet'
import { useMediaQuery } from '@/lib/use-media-query'
import { Breadcrumb } from './breadcrumb'
import { IconRail } from './icon-rail'
import { ServerTree } from './server-tree'
import { readTreeOpen, writeTreeOpen } from './tree-state'

const LG_QUERY = '(min-width: 1024px)'
const MD_QUERY = '(min-width: 768px)'

export function AppLayout() {
  const { t } = useTranslation()
  const lg = useMediaQuery(LG_QUERY)
  const md = useMediaQuery(MD_QUERY)
  const [treeOpen, setTreeOpen] = useState(readTreeOpen)
  const [menuOpen, setMenuOpen] = useState(false)
  const [flyoutOpen, setFlyoutOpen] = useState(false)

  function toggleTree() {
    setTreeOpen((open) => {
      writeTreeOpen(!open)
      return !open
    })
  }

  return (
    <div className="bg-background flex min-h-svh">
      <a href="#main" className="focus:bg-background sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:px-3 focus:py-2">
        {t('nav.skip')}
      </a>
      {md ? (
        <aside className="sticky top-0 flex h-svh shrink-0">
          <IconRail treeOpen={lg ? treeOpen : flyoutOpen} onToggleTree={lg ? toggleTree : () => setFlyoutOpen(true)} />
          {lg && treeOpen ? <ServerTree /> : null}
        </aside>
      ) : null}
      {md && !lg ? (
        <Sheet open={flyoutOpen} onOpenChange={setFlyoutOpen}>
          <SheetContent side="left" className="w-60 p-0">
            <SheetHeader className="sr-only">
              <SheetTitle>{t('nav.servers')}</SheetTitle>
              <SheetDescription>{t('nav.open_servers')}</SheetDescription>
            </SheetHeader>
            <ServerTree onNavigate={() => setFlyoutOpen(false)} />
          </SheetContent>
        </Sheet>
      ) : null}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="bg-background/95 sticky top-0 z-10 flex h-11 items-center gap-2 border-b px-4 backdrop-blur md:px-6">
          {!md ? (
            <Sheet open={menuOpen} onOpenChange={setMenuOpen}>
              <SheetTrigger asChild>
                <Button type="button" variant="ghost" size="icon" aria-label={t('nav.open_menu')}>
                  <Menu className="size-5" aria-hidden="true" />
                </Button>
              </SheetTrigger>
              <SheetContent side="left" className="flex w-[19rem] flex-row p-0">
                <SheetHeader className="sr-only">
                  <SheetTitle>{t('auth.brand')}</SheetTitle>
                  <SheetDescription>{t('nav.menu_description')}</SheetDescription>
                </SheetHeader>
                <IconRail onNavigate={() => setMenuOpen(false)} />
                <ServerTree onNavigate={() => setMenuOpen(false)} />
              </SheetContent>
            </Sheet>
          ) : null}
          {md && !lg ? (
            <Button type="button" variant="ghost" size="icon" aria-label={t('nav.open_servers')} onClick={() => setFlyoutOpen(true)}>
              <ServerIcon className="size-4" aria-hidden="true" />
            </Button>
          ) : null}
          <Breadcrumb />
        </header>
        <main id="main" tabIndex={-1} className="w-full flex-1 p-4 outline-none md:p-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
```

Hinweise:
- Der Drawer-Titel bleibt `auth.brand` („mc-panel“), damit der Dialog-Name im Test stimmt; `SheetHeader` ist `sr-only`.
- `IconRail` im Drawer bekommt `onNavigate` (schließt den Drawer) – Prop in Step 6 ergänzen und an jeden Link weitergeben (ist im Code oben schon vorgesehen).
- `DESKTOP_QUERY` aus `@/lib/use-media-query` nicht mehr importieren (andere Dateien nutzen es weiter).

- [ ] **Step 10:** `make check && make test-vm SUITE=web` → PASS.
- [ ] **Step 11: Commit** – `git add -A web/src/features/layout web/src/i18n && git commit -m "feat(web): add icon rail, server tree and breadcrumb app shell"`

---

### Task 5: Screenshots

**Files:**
- Create: `web/e2e/shots/shell.shots.ts`

- [ ] **Step 1: Datei**

```ts
import { test } from '@playwright/test'
import { serverSectionLink, openServer } from '../helpers'
import { shoot, signInMock } from './shoot'

test('shell: dashboard, server sections, collapsed tree', async ({ page }) => {
  await signInMock(page)
  await shoot(page, 'shell-dashboard')
  await openServer(page, 'survival')
  await shoot(page, 'shell-server-overview')
  await serverSectionLink(page, 'survival', 'Konsole').click()
  await shoot(page, 'shell-server-console')
  await serverSectionLink(page, 'survival', 'Einstellungen').click()
  await shoot(page, 'shell-server-settings')
  await page.getByRole('button', { name: 'Serverliste einklappen' }).click()
  await shoot(page, 'shell-tree-collapsed')
})
```

Hinweis: `shoot` schaltet auf Mobil-Viewport um; der Baum ist dann im Drawer, die Seite bleibt aber dieselbe – das ist gewollt.

- [ ] **Step 2:** Nach Task 6 (Helfer existieren): `make test-vm SUITE=shots`, dann die PNGs `web/screenshots/shell-*` mit dem Read-Tool ansehen. Prüfen: Leiste 56 px, Baum mit Grün-Markierung, Kopf mit 4 Slots (Desktop 1×4, Mobil 2×2), keine horizontale Scrollleiste bei 390 px, Light wie Dark lesbar.
- [ ] **Step 3: Commit** – `git add web/e2e/shots/shell.shots.ts && git commit -m "test(web): add shell screenshots"`

---

### Task 6: E2E-Navigation von Tab auf Link

**Files:**
- Modify: `web/e2e/helpers.ts`, `web/e2e/servers.spec.ts`, `plugins.spec.ts`, `backups.spec.ts`, `monitoring.spec.ts`

**Interfaces:**
- Produces (in `helpers.ts`):

```ts
/** Server tree (spec §4.1); e2e runs at 1280 px, so the tree is always visible. */
export function serverTree(page: Page): Locator
/** Opens a server from the tree; names are unique there (the dashboard has the same link names). */
export async function openServer(page: Page, name: string): Promise<void>
/** Link of a section of the expanded server, e.g. serverSectionLink(page, 'survival', 'Dateien'). */
export function serverSectionLink(page: Page, server: string, label: string): Locator
export async function openServerSection(page: Page, server: string, label: string): Promise<void>
```

- [ ] **Step 1: Helfer**

```ts
import { expect, type Locator, type Page } from '@playwright/test'

export function serverTree(page: Page): Locator {
  return page.getByRole('navigation', { name: 'Server', exact: true })
}

export async function openServer(page: Page, name: string) {
  await serverTree(page).getByRole('link', { name, exact: true }).click()
  await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible()
}

export function serverSectionLink(page: Page, server: string, label: string): Locator {
  return serverTree(page).getByRole('list', { name: `Bereiche von ${server}` }).getByRole('link', { name: label, exact: true })
}

export async function openServerSection(page: Page, server: string, label: string) {
  await serverSectionLink(page, server, label).click()
}
```

(`import`-Zeile von `helpers.ts` um `Locator` erweitern.)

- [ ] **Step 2: Ersetzungen** (Zeilennummern Stand `origin/feat/ui-foundation`)

| Datei:Zeile | alt | neu |
|---|---|---|
| `servers.spec.ts:19-21` | `const state = page.getByRole('region', { name: 'Zustand' })` + `state.getByText('Läuft')` / Adresse | `const header = page.getByRole('region', { name: 'skyblock', exact: true })`; `header.getByText('Läuft')`, `header.getByText('skyblock.mc.example.com')`; Zeile 35 `state.getByText('Läuft')` → `header.getByText('Läuft')` |
| `servers.spec.ts:23` | `getByRole('tab', { name: 'Einstellungen' }).click()` | `openServerSection(page, 'skyblock', 'Einstellungen')` |
| `servers.spec.ts:31` | Tab `Übersicht` klicken | `openServerSection(page, 'skyblock', 'Übersicht')` |
| `servers.spec.ts:50,61,87,96,158` | `page.getByRole('link', { name: 'survival', exact: true }).click()` | `await openServer(page, 'survival')` |
| `servers.spec.ts:134` | dito `creative` | `await openServer(page, 'creative')` |
| `servers.spec.ts:53-55` | `expect(tab Übersicht).toBeVisible()`, `tab Einstellungen/Löschen toHaveCount(0)` | `expect(serverSectionLink(page, 'survival', 'Übersicht')).toBeVisible()`, `expect(serverSectionLink(page, 'survival', 'Einstellungen')).toHaveCount(0)` (Zeile „Löschen“ streichen) |
| `servers.spec.ts:64,77` | Tab `Konsole` / `Spieler` klicken | `openServerSection(page, 'survival', 'Konsole' / 'Spieler')` |
| `servers.spec.ts:90-91` | Tabs `Konsole`/`Spieler` `toHaveCount(0)` | `serverSectionLink(page, 'survival', …)` `toHaveCount(0)` |
| `servers.spec.ts:98,136` | Tab `Dateien` klicken | `openServerSection(page, 'survival' / 'creative', 'Dateien')` |
| `servers.spec.ts:160-161` | Tab `Übersicht` sichtbar / `Dateien` 0 | `serverSectionLink(…)` |
| `plugins.spec.ts:6,54,104` / `81` / `95` | Link `survival` / `modded` / `creative` | `openServer(page, …)` |
| `plugins.spec.ts:8,56` | Tab `Plugins` klicken | `openServerSection(page, 'survival', 'Plugins')` |
| `plugins.spec.ts:83-84` | Tab `Plugins` 0 / Tab `Mods` klicken | `serverSectionLink(page, 'modded', 'Plugins')` 0 / `openServerSection(page, 'modded', 'Mods')` |
| `plugins.spec.ts:97-99,106-107` | Tabs sichtbar/0 | `serverSectionLink(page, 'creative' bzw. 'survival', …)` |
| `backups.spec.ts:6,56` / `69` | Link `survival` / `modded` | `openServer(page, …)` |
| `backups.spec.ts:8,57,70` | Tab `Backups` klicken | `openServerSection(page, <server>, 'Backups')` |
| `monitoring.spec.ts:13` | Tab `Monitoring` klicken | `openServerSection(page, 'survival', 'Monitoring')` (Zeile 12, Klick im `article`, bleibt) |

Jede `await expect(page).toHaveURL(/\/servers\/1$/)` nach `openServer` bleibt gültig (Klick vom Dashboard aus öffnet die Übersicht, da dort kein Bereich aktiv ist). Nicht ersetzen: innere Tabs `Entdecken`, `Installiert`, `Passkey`, `Wiederherstellungscode`, `Jails`, `Login-Versuche`.

Danach prüfen, dass keine Server-Tab-Selektoren übrig sind:

```bash
grep -nE "getByRole\('tab', \{ name: '(Übersicht|Monitoring|Konsole|Spieler|Dateien|Plugins|Mods|Backups|Einstellungen|Löschen)'" web/e2e/*.ts
```

Expected: keine Treffer. Ebenso `grep -n "getByRole('link', { name: '\(survival\|creative\|modded\)', exact: true })" web/e2e/*.ts` → nur noch `monitoring.spec.ts:12` (im `article` gescopt).

- [ ] **Step 3:** `make check && make test-vm SUITE=e2e` → PASS (Mock und echtes Backend).
- [ ] **Step 4: Commit** – `git add web/e2e && git commit -m "test(web): navigate server sections through the tree in e2e"`

---

### Task 7: Abschluss

- [ ] **Step 1:** `make check && make test-vm SUITE=lint && make test-vm SUITE=web && make test-vm SUITE=e2e` – alles grün.
- [ ] **Step 2:** `make test-vm SUITE=shots`, Screenshots `shell-*` ansehen (Task 5 Step 2).
- [ ] **Step 3:** `git push -u origin feat/ui-shell`. **Keinen PR öffnen** – der Lead öffnet PR 2 gestapelt auf `feat/ui-foundation`.
- [ ] **Step 4:** Dem Lead melden: Commit-SHA, Testergebnisse, Pfade der wichtigsten Screenshots und die drei Übergangsstellen aus Task 2 (Files-Blocker in der Route, `onOpenSettings` in Files/Players-Route, `DeleteServerSection` in der Settings-Route), die bei der Integration von T3/T4 zu entfernen sind.
