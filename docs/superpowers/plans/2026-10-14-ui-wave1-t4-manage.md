# UI-Redesign – Welle 1 / T4: Server-Verwaltung (Implementation Plan)

> **For agentic workers:** Dieser Plan wird vom **Teammate T4** umgesetzt (Agent-Team, siehe CLAUDE.md „Implementation with agent teams“). Arbeite in deinem eigenen Worktree auf deinem eigenen Branch, ändere **nur** die unten gelisteten Dateien, führe vor „fertig“ `make check` und die genannten `make test-vm`-Suites aus und melde dem Lead. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Dateien, Plugins/Mods, Backups, Server-Formular und Server-Einstellungen (inkl. Gefahrenzone „Server löschen“) im neuen M2/Clean-Stil, mit einem Dateien-Bereich, der sich selbst gegen Datenverlust schützt und ab `xl` Liste und Editor nebeneinander zeigt.

**Architecture:** Die Bereichs-Komponenten nehmen nur noch `{ server }` (Plugins zusätzlich `kind`, Einstellungen zusätzlich `instance`) und hängen nicht mehr von der Tab-Seite ab: `FilesTab` schützt ungespeicherte Änderungen und laufende Uploads ausschließlich über `useBlocker` (Route-Wechsel, Reload) und meldet nichts mehr nach oben; Verweise auf die Einstellungen sind echte Links (`/servers/$id/settings`), die über den Router navigieren und damit vom Blocker erfasst werden. Tests rendern die Bereichs-Komponenten direkt (Hilfsfunktion `renderServerTab`) statt über `ServerDetailPage`, damit T2 die Detailseite parallel umbauen kann. Optisch werden `SectionCard` durch `Section` ersetzt und die Listen/Formulare an die Spacing-Skala angepasst.

**Tech Stack:** React 19, TanStack Router 1.170 (`useBlocker`, `useNavigate({ href })`), TanStack Query, Tailwind v4 mit den Welle-0-Utilities, Vitest + Testing Library + MSW, Playwright (Screenshots).

**Spec:** `docs/superpowers/specs/2026-10-14-mc-panel-ui-redesign-design.md` (§3.4, §3.6, §4.3, §5.2, §6.2, §6.3). Welle-0-Plan: `docs/superpowers/plans/2026-10-14-ui-wave0-foundation.md` (Bausteine und Utilities).

**Branch / Worktree:**

```bash
git fetch origin
git worktree add ../mc-panel-wt/feat-ui-manage -b feat/ui-manage origin/feat/ui-foundation
cd ../mc-panel-wt/feat-ui-manage && (cd web && npm ci)
```

## Global Constraints

- Nur Dateien aus „File Structure“ ändern oder anlegen. Nicht anfassen: `web/src/index.css`, `web/package.json`, Welle-0-Bausteine (`web/src/components/{page-header,section,stat-slot,ping-bars,pixel-icon,mc-text}.tsx`), `web/src/components/ui/*` (T1), `web/src/routes/**`, `web/src/features/servers/{server-detail-page,server-overview,sections,server-header,status-badge,address-line,power-buttons,use-power,status,api,format}.*` (T2), `web/e2e/*.spec.ts` (T2). Bedarf dort → dem Lead melden.
- Keine API-, Go-, OpenAPI- oder Mock-Änderungen (`web/src/mocks/**` nicht ändern; Test-spezifische Antworten nur per `server.use(...)` im Test).
- i18n: neue Schlüssel nur unter `files.*`, `plugins.*`, `backups.*` und `servers.form.*`; bestehende Schlüssel nicht umbenennen oder löschen. Immer `de.ts` **und** `en.ts`.
- `SectionCard` in eigenen Dateien durch `Section` (`@/components/section`) ersetzen; `features/profile/section-card.tsx` **nicht** löschen (macht der Lead).
- Spacing (§3.4): zwischen Sections `gap-6`, innerhalb `gap-4`, Formularfelder `gap-3`; Formulare `max-w-3xl`.
- Keine Farbwerte im Code (`text-green-…`, Hex): nur Tokens (`text-primary`, `text-status-*`, `border-destructive` …).
- Tests: Rollen, Namen und Texte der bestehenden Tests bleiben, außer wo dieser Plan sie ausdrücklich ändert (Tab → Router-Navigation, Button → Link „Zu den Einstellungen“).
- Mac: nur `make check`. Tests: `make test-vm SUITE=web`; Screenshots: `make test-vm SUITE=shots`. Kein `SUITE=all` (macht der Lead).
- Commits: Conventional Commits mit den Attribution-Zeilen aus der Session.

## Review Focus

- **Datei wechseln im Zwei-Spalten-Modus mit ungespeicherten Änderungen:** Ab `xl` bleibt die Liste neben dem Editor klickbar; ein Klick auf eine andere Datei darf den Entwurf nicht still verwerfen → Bestätigung „Ungespeicherte Änderungen verwerfen?“ (Test in Task 3).
- **Wegnavigieren statt Tab-Wechsel:** Ohne Detailseiten-Tabs greift nur noch `useBlocker`; Navigation weg (anderer Bereich, anderer Server, Startseite) bei laufenden Uploads oder ungespeichertem Editor muss fragen, „Abbrechen“ bleibt auf der URL, „Verlassen“/„Verwerfen“ navigiert (Tests in Task 2).
- **„Zu den Einstellungen“ für Nicht-Admins:** Der Link darf nur für Admins erscheinen (die Route ist Admin-only); ohne Admin-Rolle kein toter Link (Test in Task 2).
- **Server ohne Instanz in den Einstellungen:** `instance === null` darf nicht crashen; dann nur Hinweis + Gefahrenzone (Test in Task 6).
- **Lange Plugin-Titel/Beschreibungen im Kachel-Raster:** Kacheln brechen um und bleiben gleich hoch ausgerichtet, Aktionen unten; keine horizontale Scrollleiste bei 390 px (Screenshot-Prüfung in Task 7, `break-words` + `min-w-0` im Code in Task 4).

---

## File Structure

| Datei | Verantwortung |
|---|---|
| `web/src/features/servers/tab-test-utils.tsx` (neu) | `renderServerTab()` – rendert eine Bereichs-Komponente mit geladenem `Server` im Test-Router |
| `web/src/features/files/test-utils.tsx` (ändern) | `openFiles()` über `renderServerTab` statt Detailseite |
| `web/src/features/plugins/test-utils.tsx` (ändern) | `openPlugins()`/`openDiscover()` über `renderServerTab` |
| `web/src/features/backups/test-utils.tsx` (ändern) | `openBackups()` über `renderServerTab` |
| `web/src/features/files/settings-link.tsx` (neu) | `ServerSettingsLink` – Router-Link auf `/servers/$id/settings` ohne typisierte Route |
| `web/src/features/files/files-tab.tsx` (ändern) | Blocker als einziger Schutz, Zwei-Spalten ab `xl`, Datei-Wechsel-Bestätigung, `Section` |
| `web/src/features/files/file-editor.tsx` (ändern) | Settings-Link (Admin), kein `onOpenSettings` |
| `web/src/features/files/upload-list.tsx` (ändern) | `Section` |
| `web/src/features/files/files-tab.test.tsx`, `file-editor.test.tsx` (ändern) | Router-Navigation statt Tabs, Link statt Button, Zwei-Spalten-Test |
| `web/src/features/plugins/plugins-tab.tsx`, `discover.tsx`, `installed-list.tsx` (ändern) | `Section`, Kachel-Raster, Neustart-Hinweis |
| `web/src/features/backups/backups-tab.tsx`, `schedule-card.tsx` (ändern) | `Section` |
| `web/src/features/servers/server-settings-tab.tsx` (ändern) | Form + Gefahrenzone, `instance` nullable |
| `web/src/features/servers/server-settings-tab.test.tsx` (neu) | Gefahrenzone, Server ohne Instanz |
| `web/src/features/servers/delete-server-section.tsx` (ändern) | `Section tone="danger"` |
| `web/src/features/servers/server-form.tsx`, `collapsible-section.tsx` (ändern) | Formular in Sections |
| `web/src/features/servers/create-server-page.tsx` (ändern) | Basisdomain-Hinweis im neuen Stil |
| `web/src/i18n/de.ts`, `web/src/i18n/en.ts` (ändern) | `servers.form.section.*` |
| `web/e2e/shots/manage.shots.ts` (neu) | Screenshots Dateien/Plugins/Backups/Einstellungen/Anlegen |

Nicht geändert, aber genutzt: `features/servers/{field-row,properties-fields,form-model,validation,java}.*` (Besitz T4, keine Änderung nötig außer wenn Lint/Style es verlangt).

---

### Task 1: Test-Harness unabhängig von der Detailseite

**Files:**
- Create: `web/src/features/servers/tab-test-utils.tsx`
- Modify: `web/src/features/files/test-utils.tsx`, `web/src/features/plugins/test-utils.tsx`, `web/src/features/backups/test-utils.tsx`

**Interfaces:**
- Consumes: `serverQueryOptions(id)` aus `@/features/servers/api` (T2-Datei, nur lesend), `renderWithProviders` aus `@/test/render`, `loginAs` aus `@/mocks/db`.
- Produces:

```ts
export async function renderServerTab(
  render: (server: Server) => ReactNode,
  opts?: { serverId?: number; username?: string; sudo?: boolean; path?: string },
): Promise<{ user: UserEvent; router: TestRouter; queryClient: QueryClient }>
// Standard: serverId 1, username 'admin', path `/servers/<id>/<letzter Pfadteil>` = vom Aufrufer; Default `/servers/<id>`.
```

- `openFiles(serverId = 1, username = 'admin')` → `{ user, region, router, queryClient }` (region = `region` „Dateien“).
- `openPlugins(serverId, username, tab)` und `openDiscover(...)` – gleiche Rückgaben wie heute (`{ user, region, queryClient }`), `tab` steuert nur noch das erwartete Regions-Label und `kind`.
- `openBackups(serverId, username, opts)` → `{ user, queryClient }` wie heute.

- [ ] **Step 1: `tab-test-utils.tsx` anlegen**

```tsx
import { useQuery, type QueryClient } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import type { Server } from '@/api/types'
import { loginAs } from '@/mocks/db'
import { renderWithProviders, type TestRouter } from '@/test/render'
import { serverQueryOptions } from './api'

function WithServer({ serverId, render }: { serverId: number; render: (server: Server) => ReactNode }) {
  const query = useQuery(serverQueryOptions(serverId))
  if (!query.data) return null
  return <>{render(query.data)}</>
}

/**
 * Renders one server area (files, plugins, backups, settings …) with its loaded server, independent of the detail page
 * and its navigation (T2). The test router matches every path, so navigation can be asserted via router.state.location.
 */
export async function renderServerTab(
  render: (server: Server) => ReactNode,
  { serverId = 1, username = 'admin', sudo, path }: { serverId?: number; username?: string; sudo?: boolean; path?: string } = {},
): Promise<{ user: ReturnType<typeof userEvent.setup>; router: TestRouter; queryClient: QueryClient }> {
  const user = userEvent.setup()
  const { router, queryClient } = await renderWithProviders(<WithServer serverId={serverId} render={render} />, {
    me: loginAs(username, sudo === undefined ? {} : { sudo }),
    path: path ?? `/servers/${serverId}`,
  })
  return { user, router, queryClient }
}
```

Prüfen, dass `TestRouter` aus `@/test/render` exportiert ist (`grep -n "export type TestRouter" web/src/test/render.tsx` – ja, Zeile ~32).

- [ ] **Step 2: `files/test-utils.tsx` umstellen**

```tsx
import { screen } from '@testing-library/react'
import { renderServerTab } from '@/features/servers/tab-test-utils'
import { FilesTab } from './files-tab'

export async function openFiles(serverId = 1, username = 'admin') {
  const { user, router, queryClient } = await renderServerTab((server) => <FilesTab server={server} />, {
    serverId,
    username,
    path: `/servers/${serverId}/files`,
  })
  const region = await screen.findByRole('region', { name: 'Dateien' })
  return { user, region, router, queryClient }
}
```

- [ ] **Step 3: `plugins/test-utils.tsx` – nur `openPlugins`/`openDiscover` ersetzen** (alle Fixture-Helfer bleiben unverändert; Import von `ServerDetailPage`, `loginAs`, `renderWithProviders`, `userEvent` entfernen, falls danach ungenutzt)

```tsx
import { pluginKindOf } from './model'
import { PluginsTab } from './plugins-tab'
import { renderServerTab } from '@/features/servers/tab-test-utils'

export async function openPlugins(serverId = 1, username = 'admin', tab: 'Plugins' | 'Mods' = 'Plugins') {
  const { user, queryClient } = await renderServerTab(
    (server) => <PluginsTab server={server} kind={(server.instance && pluginKindOf(server.instance.type)) ?? (tab === 'Mods' ? 'mod' : 'plugin')} />,
    { serverId, username, path: `/servers/${serverId}/plugins` },
  )
  const region = await screen.findByRole('region', { name: tab === 'Plugins' ? 'Installierte Plugins' : 'Installierte Mods' })
  return { user, region, queryClient }
}
```

`openDiscover` bleibt textlich gleich (ruft `openPlugins`, klickt den inneren Tab „Entdecken“). Vorher prüfen: `grep -n "export function pluginKindOf" web/src/features/plugins/model.ts` (Rückgabe `PluginKind | null`).

- [ ] **Step 4: `backups/test-utils.tsx` – nur `openBackups` ersetzen**

```tsx
import { BackupsTab } from './backups-tab'
import { renderServerTab } from '@/features/servers/tab-test-utils'

export async function openBackups(serverId = 1, username = 'admin', opts: { sudo?: boolean } = { sudo: true }) {
  const { user, queryClient } = await renderServerTab((server) => <BackupsTab server={server} />, {
    serverId,
    username,
    sudo: opts.sudo,
    path: `/servers/${serverId}/backups`,
  })
  return { user, queryClient }
}
```

- [ ] **Step 5: Bestehende Tests laufen lassen**

Run: `make check && make test-vm SUITE=web`
Expected: Alle Plugins-/Backups-Tests grün. In `files-tab.test.tsx` und `file-editor.test.tsx` schlagen genau die Tests fehl, die Detailseiten-Tabs anklicken („asks before leaving the tab while uploads run“, „asks before switching tabs with unsaved changes“, „asks before "Zu den Einstellungen" drops unsaved changes“) – die werden in Task 2 ersetzt. Schlagen andere fehl (z. B. weil ein Test eine Detailseiten-Region wie „Zustand“ erwartet), im Test durch die Router-Prüfung aus Task 2 ersetzen und notieren.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/servers/tab-test-utils.tsx web/src/features/{files,plugins,backups}/test-utils.tsx
git commit -m "test(web): render server areas directly instead of through the detail page tabs"
```

---

### Task 2: Dateien schützen sich selbst; Link zu den Einstellungen

**Files:**
- Create: `web/src/features/files/settings-link.tsx`
- Modify: `web/src/features/files/files-tab.tsx`, `web/src/features/files/file-editor.tsx`, `web/src/features/files/files-tab.test.tsx`, `web/src/features/files/file-editor.test.tsx`

**Interfaces:**
- Produces:

```ts
// files-tab.tsx – finaler Vertrag für T2
export interface FilesTabProps {
  server: Server
  /** @deprecated Ignored; the files area guards itself with useBlocker. Removed by the lead after T2 is integrated. */
  onBusyChange?: (busy: FilesBusy | null) => void
  /** @deprecated Ignored; the editor links to /servers/$id/settings itself. Removed by the lead after T2 is integrated. */
  onOpenSettings?: () => void
}
export function FilesTab({ server }: FilesTabProps): JSX.Element

// settings-link.tsx
export function ServerSettingsLink(props: { serverId: number; children: ReactNode; className?: string }): JSX.Element
// <a href="/servers/<id>/settings">, Klick (ohne Modifier) → navigate({ href }) über den Router (Blocker greift)
```

Die beiden veralteten Props bleiben nur, damit die alte `server-detail-page.tsx` (T2-Datei) auf diesem Branch weiter kompiliert; sie werden nicht ausgewertet.

- [ ] **Step 1: Failing tests – Wegnavigieren und Link**

In `files-tab.test.tsx` den Test „asks before leaving the tab while uploads run“ ersetzen (Imports: `act` aus `@testing-library/react` ergänzen):

```tsx
  // Review F1: leaving the files area cancels running uploads, so any navigation asks first (useBlocker).
  it('asks before navigating away while uploads run', async () => {
    server.use(
      http.post('*/api/servers/:id/files/upload', async () => {
        await delay('infinite')
        return HttpResponse.json({ size: 1 }, { status: 201 })
      }),
    )
    const { user, region, router } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File(['x'], 'big.jar'))
    await screen.findByRole('region', { name: 'Uploads' })

    act(() => void router.navigate({ to: '/' }))
    const dialog = await screen.findByRole('dialog', { name: 'Laufende Uploads abbrechen?' })
    expect(within(dialog).getByText('Laufende Uploads werden abgebrochen.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Abbrechen' }))
    expect(router.state.location.pathname).toBe('/servers/1/files')

    act(() => void router.navigate({ to: '/' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Laufende Uploads abbrechen?' })).getByRole('button', { name: 'Verlassen' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })
```

In `file-editor.test.tsx`:
- `openConfig()` gibt zusätzlich `router` zurück (`const { user, region, router } = await openFiles()` … `return { user, region, editor, router }`).
- „asks before switching tabs with unsaved changes“ ersetzen:

```tsx
  it('asks before navigating away with unsaved changes', async () => {
    const { user, editor, router } = await openConfig()
    await user.type(editor, 'x')
    act(() => void router.navigate({ to: '/' }))
    let dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
    await user.click(within(dialog).getByRole('button', { name: 'Abbrechen' }))
    expect(router.state.location.pathname).toBe('/servers/1/files')
    expect(screen.getByRole('textbox', { name: 'Inhalt von config.yml' })).toBeInTheDocument()

    act(() => void router.navigate({ to: '/' }))
    dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
    await user.click(within(dialog).getByRole('button', { name: 'Verwerfen' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })
```

- In „explains server.properties and shows the redacted password“ die letzte Zeile ersetzen:

```tsx
    expect(screen.getByRole('link', { name: 'Zu den Einstellungen' })).toHaveAttribute('href', '/servers/1/settings')
```

- „asks before "Zu den Einstellungen" drops unsaved changes“ ersetzen:

```tsx
  it('asks before "Zu den Einstellungen" drops unsaved changes and then navigates', async () => {
    const { user, region, router } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'server.properties' }))
    const editor = await screen.findByRole('textbox', { name: 'Inhalt von server.properties' })
    await user.type(editor, 'x')
    await user.click(screen.getByRole('link', { name: 'Zu den Einstellungen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
    expect(router.state.location.pathname).toBe('/servers/1/files')
    await user.click(within(dialog).getByRole('button', { name: 'Verwerfen' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/1/settings'))
  })

  it('shows no settings link to non-admins', async () => {
    server.use(
      http.get('*/api/servers/:id/files/content', () =>
        HttpResponse.json({ path: 'server.properties', content: 'motd=hi\n', size: 8, mtime: 1, editable: true }),
      ),
    )
    await renderServerTab((s) => <FileEditor server={s} path="server.properties" onClose={() => {}} />, {
      username: 'alice',
      path: '/servers/1/files',
    })
    expect(await screen.findByText(/wird beim Start aus den panel-Einstellungen überschrieben/)).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Zu den Einstellungen' })).not.toBeInTheDocument()
  })
```

Imports in `file-editor.test.tsx` ergänzen: `act` (Testing Library), `renderServerTab` aus `@/features/servers/tab-test-utils`, `FileEditor` aus `./file-editor`. Die Form der Content-Antwort vorher mit `grep -n "files/content" -A12 web/src/mocks/handlers/files.ts` abgleichen und die Felder des Mocks übernehmen (nur die dort vorhandenen Felder verwenden).

- [ ] **Step 2: Fehlschlag prüfen**

Run: `make test-vm SUITE=web`
Expected: FAIL – Link „Zu den Einstellungen“ nicht gefunden (heute ein Button, und ohne `onOpenSettings` gar nicht da); Blocker-Tests können schon grün sein (der Blocker existiert bereits), das ist in Ordnung.

- [ ] **Step 3: `settings-link.tsx`**

```tsx
import { useNavigate } from '@tanstack/react-router'
import type { MouseEvent, ReactNode } from 'react'
import { cn } from '@/lib/utils'

/**
 * Link to the server's settings area. It navigates through the router (so the files area's blocker can ask first) via
 * `href`, because the typed route `/servers/$id/settings` only exists once the shell (T2) is integrated; the lead may
 * swap it for a typed <Link> afterwards.
 */
export function ServerSettingsLink({ serverId, children, className }: { serverId: number; children: ReactNode; className?: string }) {
  const navigate = useNavigate()
  const href = `/servers/${serverId}/settings`
  function onClick(event: MouseEvent<HTMLAnchorElement>) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    event.preventDefault()
    void navigate({ href })
  }
  return (
    <a href={href} onClick={onClick} className={cn('text-primary underline-offset-4 hover:underline', className)}>
      {children}
    </a>
  )
}
```

- [ ] **Step 4: `file-editor.tsx`**

- `onOpenSettings` aus `FileEditorProps` und Destrukturierung entfernen.
- Imports: `import { useMe } from '@/features/auth/me'` und `import { ServerSettingsLink } from './settings-link'`.
- Im Komponentenrumpf: `const isAdmin = useMe()?.role === 'admin'`.
- Kommentar `// Route changes, reloads and tab switches are asked by the files tab and the detail page (onDirtyChange).` ersetzen durch `// Route changes and reloads are asked by the files area (useBlocker in FilesTab, fed by onDirtyChange).`
- Den Hinweisblock ersetzen:

```tsx
      {path === 'server.properties' ? (
        <div role="note" className="bevel-inset flex flex-wrap items-center gap-2 px-3 py-2 text-sm">
          <span>{t('files.editor.properties_hint')}</span>
          {isAdmin ? <ServerSettingsLink serverId={server.id}>{t('files.editor.open_settings')}</ServerSettingsLink> : null}
        </div>
      ) : null}
```

- [ ] **Step 5: `files-tab.tsx` – Props und Blocker**

- `FilesTabProps` wie unter „Interfaces“ ersetzen; Signatur `export function FilesTab({ server }: FilesTabProps)`.
- Die beiden `useEffect`s mit `onBusyChange` entfernen.
- Kommentar über `useBlocker` ersetzen: `// The only guard of the files area: any navigation away (other area, other server, logout link) and reloads. Leaving cancels the uploads.`
- `<FileEditor … onOpenSettings={onOpenSettings} />` → ohne `onOpenSettings`.

- [ ] **Step 6: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS. `make check` muss grün sein, obwohl `server-detail-page.tsx` noch `onBusyChange`/`onOpenSettings` übergibt (veraltete optionale Props).

- [ ] **Step 7: Commit**

```bash
git add web/src/features/files/{settings-link,files-tab,file-editor}.tsx web/src/features/files/{files-tab,file-editor}.test.tsx
git commit -m "feat(web): files area guards itself on navigation and links to server settings"
```

---

### Task 3: Dateien – Layout, Zwei-Spalten ab `xl`, Sections

**Files:**
- Modify: `web/src/features/files/files-tab.tsx`, `web/src/features/files/upload-list.tsx`, `web/src/features/files/file-editor.test.tsx`

**Interfaces:**
- Consumes: `Section` (Welle 0), `useMediaQuery` aus `@/lib/use-media-query`, `setViewport` aus `@/test/media`.
- Produces: `export const WIDE_QUERY = '(min-width: 1280px)'` in `files-tab.tsx` (Tailwind `xl`).

Verhalten:
- Unter `xl` wie heute: Liste **oder** Editor in der Section „Dateien“.
- Ab `xl` beim Bearbeiten: Liste (mit Toolbar, Pfad, Sortierung) links (`2fr`), Editor rechts (`3fr`), beide in derselben Section „Dateien“ (Regionsname bleibt).
- Klick auf eine andere Datei, während der Editor ungespeicherte Änderungen hat → `ConfirmDialog` mit `files.editor.discard_title` / `files.editor.discard_description` (`{ name }` = Name der *aktuell* offenen Datei) / `files.editor.discard`; Bestätigen öffnet die neue Datei, Abbrechen behält den Entwurf.
- `FileEditor` bekommt `key={editing}`, damit ein Wechsel den Entwurf sauber zurücksetzt.

- [ ] **Step 1: Failing tests** (in `file-editor.test.tsx`; Imports `afterEach` aus vitest, `setViewport` aus `@/test/media`)

```tsx
describe('two panes from xl', () => {
  afterEach(() => setViewport('desktop'))

  it('keeps the list beside the editor and asks before switching files with unsaved changes', async () => {
    // test/media.ts: "desktop" is 1280px = xl.
    const { user, region, editor } = await openConfig()
    expect(within(region).getByRole('button', { name: 'config.yml' })).toBeInTheDocument()
    await user.type(editor, 'x')
    await user.click(within(region).getByRole('button', { name: 'Server-Daten' }))
    await user.click(await within(region).findByRole('button', { name: 'bukkit.yml' }))
    let dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
    await user.click(within(dialog).getByRole('button', { name: 'Abbrechen' }))
    expect(screen.getByRole('textbox', { name: 'Inhalt von config.yml' })).toBeInTheDocument()

    await user.click(within(region).getByRole('button', { name: 'bukkit.yml' }))
    dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
    await user.click(within(dialog).getByRole('button', { name: 'Verwerfen' }))
    expect(await screen.findByRole('textbox', { name: 'Inhalt von bukkit.yml' })).toBeInTheDocument()
  })

  it('shows either list or editor below xl', async () => {
    setViewport('mobile')
    const { region } = await openConfig()
    expect(within(region).queryByRole('button', { name: 'config.yml' })).not.toBeInTheDocument()
  })
})
```

Falls `bukkit.yml` im Mock nicht als Textdatei lesbar ist (`grep -n "bukkit" web/src/mocks/handlers/files.ts web/src/mocks/db.ts`), eine andere Textdatei des Wurzelordners aus dem Mock nehmen (z. B. `server.properties`) und den Textbox-Namen anpassen.

Hinweis: Bestehende Tests, die nach dem Öffnen des Editors erwarten, dass Listen-Buttons *fehlen*, gibt es nicht (geprüft: `grep -n "queryByRole('button'" web/src/features/files/*.test.tsx`); falls doch, mit `setViewport('mobile')` laufen lassen.

- [ ] **Step 2: Fehlschlag prüfen**

Run: `make test-vm SUITE=web`
Expected: FAIL – im Editor-Modus ist die Liste weg (`config.yml`-Button nicht gefunden).

- [ ] **Step 3: Implementieren – `files-tab.tsx`**

Imports ergänzen: `import { Section } from '@/components/section'`, `import { useMediaQuery } from '@/lib/use-media-query'`, `import { cn } from '@/lib/utils'`; `SectionCard`-Import entfernen.

Im Rumpf (nach `const [editing, setEditing] = …`):

```tsx
  const wide = useMediaQuery(WIDE_QUERY)
  const [pendingOpen, setPendingOpen] = useState<string | null>(null)
  /** Opening another file would drop the editor's draft: ask first (two panes from xl keep the list clickable). */
  function openFile(path: string) {
    if (path === editing) return
    if (unsaved) setPendingOpen(path)
    else setEditing(path)
  }
```

(`unsaved` ist erst weiter unten definiert – `openFile` dort deklarieren, wo `unsaved` schon existiert, also nach `const unsaved = …`.)

Oberhalb der Komponente: `export const WIDE_QUERY = '(min-width: 1280px)'` mit Kommentar `/** Tailwind xl: list and editor side by side. */`.

In `body()`: `onOpenFile={(entry) => openFile(joinPath(dir, entry.name))}`.

Den Listen-Teil (heute der `else`-Zweig mit Drop-Zone) in eine lokale Funktion `browser()` ziehen, die genau das bisherige JSX zurückgibt (Drop-Zone mit Pfad, Sortierung, Notice, `body()`); die Klasse der Drop-Zone ersetzen durch:

```tsx
            className={cn('relative flex flex-col gap-3', dragging && 'ring-primary ring-2')}
```

und das Overlay `rounded-md` entfernen (`… absolute inset-0 z-10 flex items-center justify-center text-sm font-medium`).

Den bisherigen `SectionCard`-Block ersetzen:

```tsx
      <Section title={t('files.title')} actions={(editing === null || wide) && !neverStarted ? toolbar : undefined}>
        {editing === null ? (
          browser()
        ) : wide ? (
          <div className="grid grid-cols-[minmax(0,2fr)_minmax(0,3fr)] gap-6">
            <div className="min-w-0">{browser()}</div>
            <div className="min-w-0 border-l pl-6">
              <FileEditor key={editing} server={server} path={editing} onClose={() => setEditing(null)} onDirtyChange={setEditorDirty} />
            </div>
          </div>
        ) : (
          <FileEditor key={editing} server={server} path={editing} onClose={() => setEditing(null)} onDirtyChange={setEditorDirty} />
        )}
      </Section>
```

Den „Server läuft“-Hinweis und die äußere Hülle anpassen:

```tsx
    <div className="flex flex-col gap-6">
      {running ? (
        <p role="status" className="bevel-inset px-3 py-2 text-sm">
          {t('files.running_hint')}
        </p>
      ) : null}
```

Neben den anderen Dialogen ergänzen:

```tsx
      <ConfirmDialog
        open={pendingOpen !== null}
        onOpenChange={(open) => (open ? undefined : setPendingOpen(null))}
        title={t('files.editor.discard_title')}
        description={t('files.editor.discard_description', { name: editing ? baseName(editing) : '' })}
        confirmLabel={t('files.editor.discard')}
        destructive
        onConfirm={() => {
          setEditing(pendingOpen)
          setEditorDirty(false)
          setPendingOpen(null)
        }}
      />
```

- [ ] **Step 4: `upload-list.tsx`**

`SectionCard` → `Section` (gleiche Props `title`, `actions`); `<ul className="divide-y">` bleibt. Import `import { Section } from '@/components/section'`.

- [ ] **Step 5: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS (alle `files`-Tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/features/files/{files-tab,upload-list}.tsx web/src/features/files/file-editor.test.tsx
git commit -m "feat(web): files area with list and editor side by side from xl"
```

---

### Task 4: Plugins/Mods – Sections, Kachel-Raster, Neustart-Hinweis

**Files:**
- Modify: `web/src/features/plugins/plugins-tab.tsx`, `web/src/features/plugins/discover.tsx`, `web/src/features/plugins/installed-list.tsx` (nur falls Lint/Layout es verlangt)

**Interfaces:**
- Produces (Vertrag für T2, unverändert): `export function PluginsTab({ server, kind }: { server: Server; kind: PluginKind }): JSX.Element`.

- [ ] **Step 1: Failing test** (in `web/src/features/plugins/discover.test.tsx` ergänzen)

```tsx
  it('shows the results as a tile grid with the install action in each tile', async () => {
    const { region } = await openDiscover()
    const list = await within(region).findByRole('list')
    expect(list).toHaveAttribute('data-layout', 'grid')
    const tiles = within(list).getAllByRole('listitem')
    expect(tiles.length).toBeGreaterThan(0)
    expect(within(tiles[0]).getByRole('heading', { level: 4 })).toBeInTheDocument()
  })
```

(Falls `region` „Entdecken“ mehr als eine Liste enthält – z. B. die Quellen-Gruppe ist kein `list` –, `findAllByRole('list')` und die mit `data-layout="grid"` nehmen.)

- [ ] **Step 2: Fehlschlag prüfen**

Run: `make test-vm SUITE=web` – Expected: FAIL (`data-layout` fehlt).

- [ ] **Step 3: `discover.tsx`**

Die Ergebnisliste (heute Zeile ~53, `<ul className="space-y-3">`) ersetzen:

```tsx
        <ul data-layout="grid" className="grid gap-3 sm:grid-cols-2 2xl:grid-cols-3">
```

`HitCard` als Kachel:

```tsx
    <li className="bg-surface-2 flex min-w-0 flex-col gap-3 border p-3">
      <div className="flex min-w-0 items-start gap-3">
        <PluginIcon url={hit.icon_url} />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h4 className="font-medium break-words">{hit.title}</h4>
            {hit.installed ? <Badge variant="secondary">{t('plugins.discover.installed')}</Badge> : null}
          </div>
          <p className="text-muted-foreground text-xs">{t('plugins.discover.meta', { author: hit.author, downloads: fmt.number(hit.downloads) })}</p>
        </div>
      </div>
      {/* Spec §7.1: only the short description, as plain text – never Markdown or HTML from the upstream. */}
      <p className="line-clamp-3 text-sm break-words">{hit.description}</p>
      <div className="mt-auto flex flex-wrap items-center gap-3">
        {/* unchanged: install button and external page link */}
      </div>
    </li>
```

(Der innere Block mit Install-Button und Seitenlink wird 1:1 aus dem bisherigen Code übernommen.) Die Suchzeile: `className="flex flex-col gap-3 sm:flex-row sm:items-end"` bleibt; die Überschrift `<h3 className="text-sm font-medium">` → `<h3 className="pixel-heading text-xs uppercase">`; äußere Hülle `space-y-4` → `flex flex-col gap-4`.

- [ ] **Step 4: `plugins-tab.tsx`**

- `SectionCard` → `Section` (zwei Stellen, gleiche Props). Import anpassen.
- Äußere Hülle `space-y-4` → `flex flex-col gap-6`; `TabsContent … className="pt-4"` bleibt.
- Neustart-Hinweis (`role="status"`, Zeile ~184) Klasse ersetzen: `className="border-status-starting/60 bg-card flex flex-col gap-3 border p-4 sm:flex-row sm:items-center sm:justify-between"`, Titel `<p className="font-medium">` bleibt.
- Der Hinweis Zeile ~144 (`role="status" className="rounded-md border p-3 text-sm"`) → `className="bevel-inset px-3 py-2 text-sm"`.

- [ ] **Step 5: Tests grün**

Run: `make check && make test-vm SUITE=web` – Expected: PASS (alle `plugins`-Tests).

- [ ] **Step 6: Commit**

```bash
git add web/src/features/plugins/{plugins-tab,discover}.tsx web/src/features/plugins/discover.test.tsx
git commit -m "feat(web): plugin discovery as tile grid, plugin areas in sections"
```

---

### Task 5: Backups – Sections

**Files:**
- Modify: `web/src/features/backups/backups-tab.tsx`, `web/src/features/backups/schedule-card.tsx`

**Interfaces:**
- Produces (Vertrag für T2, unverändert): `export function BackupsTab({ server }: { server: Server }): JSX.Element`.

- [ ] **Step 1: Test** (in `backups-tab.test.tsx` ergänzen – sichert die Regionen, die Section jetzt liefern muss)

```tsx
  it('shows snapshots and schedule as labelled sections', async () => {
    mockOverview(overview({ snapshots: [snapshot(1)] }))
    await openBackups()
    expect(await screen.findByRole('region', { name: 'Snapshots' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Zeitplan' })).toBeInTheDocument()
  })
```

Die Namen vorher prüfen: `grep -n "snapshots: {" -A2 web/src/i18n/de.ts` und `grep -n "schedule: {" -A2 web/src/i18n/de.ts` (Schlüssel `backups.snapshots.title`, `backups.schedule.title`) und die tatsächlichen deutschen Titel einsetzen. Dieser Test ist heute schon grün (SectionCard labelt ebenfalls) und muss es nach dem Umbau bleiben.

- [ ] **Step 2: Umbau**

- `backups-tab.tsx`: `SectionCard` → `Section` (gleiche Props); äußere Hülle `space-y-4` → `flex flex-col gap-6`; „nicht verfügbar“-Hinweis `className="rounded-md border p-4 text-sm"` → `className="bevel-inset px-4 py-3 text-sm"`.
- `schedule-card.tsx`: `SectionCard` → `Section` (Props `title`, `description`).

- [ ] **Step 3: Tests grün**

Run: `make check && make test-vm SUITE=web` – Expected: PASS (alle `backups`-Tests).

- [ ] **Step 4: Commit**

```bash
git add web/src/features/backups/{backups-tab,schedule-card}.tsx web/src/features/backups/backups-tab.test.tsx
git commit -m "feat(web): backups area in sections"
```

---

### Task 6: Server-Formular, Einstellungen mit Gefahrenzone, Anlegen-Seite

**Files:**
- Modify: `web/src/features/servers/server-settings-tab.tsx`, `delete-server-section.tsx`, `server-form.tsx`, `collapsible-section.tsx`, `create-server-page.tsx`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`
- Create: `web/src/features/servers/server-settings-tab.test.tsx`

**Interfaces:**
- Produces (Vertrag für T2):

```ts
export function ServerSettingsTab({ server, instance }: { server: Server; instance: InstanceConfig | null }): JSX.Element
// instance === null → Hinweis servers.detail.not_configured + Gefahrenzone; sonst Formular + Gefahrenzone.
// T2 rendert den Bereich nur für Admins und entfernt den eigenen Bereich „Löschen“.
export function DeleteServerSection({ server }: { server: Server }): JSX.Element   // unverändert, jetzt Section tone="danger"
```

- i18n (neu): `servers.form.section.basics` („Grundlagen“ / „Basics“), `servers.form.section.resources` („Ressourcen“ / „Resources“).

- [ ] **Step 1: Failing test** – `server-settings-tab.test.tsx`

```tsx
import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderServerTab } from './tab-test-utils'
import { ServerSettingsTab } from './server-settings-tab'

describe('ServerSettingsTab', () => {
  it('shows the form and the danger zone at the end', async () => {
    await renderServerTab((s) => <ServerSettingsTab server={s} instance={s.instance} />, { path: '/servers/1/settings' })
    expect(await screen.findByRole('button', { name: 'Speichern' })).toBeInTheDocument()
    const danger = screen.getByRole('region', { name: 'Server löschen' })
    expect(danger).toHaveAttribute('data-tone', 'danger')
    expect(within(danger).getByRole('button', { name: /löschen/i })).toBeDisabled()
    // the danger zone is the last section of the area
    const regions = screen.getAllByRole('region')
    expect(regions.at(-1)).toBe(danger)
  })

  it('shows only the hint and the danger zone for a server without instance', async () => {
    await renderServerTab((s) => <ServerSettingsTab server={s} instance={null} />, { path: '/servers/1/settings' })
    expect(await screen.findByRole('region', { name: 'Server löschen' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Speichern' })).not.toBeInTheDocument()
  })
})
```

Vorher die Texte prüfen: `grep -n "settings: {" -A4 web/src/i18n/de.ts | grep -n save` (Text von `servers.settings.save`) und `grep -n "submit:" web/src/i18n/de.ts | grep -i lösch` (Text von `servers.delete.submit`); die Strings im Test entsprechend einsetzen. Der zweite Test nutzt Server 1 mit `instance={null}` – das genügt, weil die Komponente nur das Prop auswertet.

- [ ] **Step 2: Fehlschlag prüfen**

Run: `make test-vm SUITE=web` – Expected: FAIL (keine Region „Server löschen“ im Einstellungs-Bereich; `instance={null}` ist ein Typfehler → `make check` schlägt vorher schon fehl, das ist der erwartete Fehlschlag).

- [ ] **Step 3: `server-settings-tab.tsx`**

```tsx
import { DeleteServerSection } from './delete-server-section'
// … bestehende Imports

export function ServerSettingsTab({ server, instance }: { server: Server; instance: InstanceConfig | null }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const save = useMutation({ /* unverändert */ })

  return (
    <div className="flex max-w-3xl flex-col gap-6">
      {instance ? (
        // A changed instance (after saving or by another admin) re-initialises the form via the key.
        <ServerForm
          key={JSON.stringify(instance)}
          mode="edit"
          initial={formFromInstance(server.name, instance)}
          submitLabel={t('servers.settings.save')}
          pending={save.isPending}
          error={save.error}
          notice={RUNNING_STATES.has(server.status.state) ? t('servers.settings.running_notice') : undefined}
          onSubmit={(result) => save.mutate(result)}
        />
      ) : (
        <p className="text-muted-foreground text-sm">{t('servers.detail.not_configured')}</p>
      )}
      <DeleteServerSection server={server} />
    </div>
  )
}
```

`save` bleibt vor dem bedingten Rendern (Hooks-Regel).

- [ ] **Step 4: `delete-server-section.tsx`**

`SectionCard` → `Section` mit `tone="danger"`, ohne `className="max-w-3xl"` (Breite kommt vom Elternteil); `<form className="space-y-4">` → `className="flex flex-col gap-4"`. Import `import { Section } from '@/components/section'`.

- [ ] **Step 5: `server-form.tsx` in Sections**

Import `import { Section } from '@/components/section'`. Den Formularkopf umbauen:

```tsx
    <form className="flex max-w-3xl flex-col gap-6" noValidate onSubmit={/* unverändert */}>
      <Section title={t('servers.form.section.basics')}>
        <div className="flex flex-col gap-4">
          {mode === 'create' ? (
            <>
              <NameField /* unverändert */ />
              <TypeField /* unverändert */ />
            </>
          ) : (
            <p className="text-sm">
              <span className="font-medium">{t('servers.form.type')}:</span> {t(`servers.type.${value.type}`)}
            </p>
          )}
          <VersionFields /* unverändert */ />
        </div>
      </Section>
      <Section title={t('servers.form.section.resources')}>
        <MemoryField /* unverändert */ />
      </Section>
      {/* CollapsibleSection advanced + properties unverändert */}
      {/* EulaField unverändert */}
      <div className="flex flex-col gap-3">{/* notice, Fehler, Submit unverändert */}</div>
    </form>
```

In `TypeField` die Optionskachel `cn('flex items-start gap-3 rounded-md border p-3', type === option && 'border-primary', …)` → `cn('bg-surface-2 flex items-start gap-3 border p-3', type === option && 'border-primary bevel-inset', disabled && 'opacity-60')`.

- [ ] **Step 6: `collapsible-section.tsx`**

Aussehen an `Section` angleichen, Semantik (h2 mit Button, `aria-expanded`, `aria-controls`) bleibt:

```tsx
    <section className="bg-card border">
      <h2>
        <button
          type="button"
          className="hover:bg-accent focus-visible:ring-ring pixel-heading flex w-full items-center justify-between gap-2 px-4 py-3 text-left text-sm uppercase outline-none focus-visible:ring-2"
          /* aria-* und onClick unverändert */
        >
```

und Inhalt `className="flex flex-col gap-4 border-t p-4"`; Beschreibung `className="text-muted-foreground px-4 pb-3 text-sm"` bleibt.

- [ ] **Step 7: `create-server-page.tsx`**

Basisdomain-Hinweis: `className="mb-6 max-w-3xl rounded-md border p-4 text-sm"` → `className="border-status-starting/60 bg-card mb-6 max-w-3xl border p-4 text-sm"`.

- [ ] **Step 8: i18n**

In `de.ts` unter `servers.form` (Objekt suchen mit `grep -n "    form: {" web/src/i18n/de.ts`):

```ts
      section: { basics: 'Grundlagen', resources: 'Ressourcen' },
```

In `en.ts` entsprechend: `section: { basics: 'Basics', resources: 'Resources' },`.

- [ ] **Step 9: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS, inkl. `create-server-page.test.tsx`, `form-model.test.ts`, `validation.test.ts`. Hinweis: Die alte `server-detail-page.tsx` (T2) zeigt auf diesem Branch die Gefahrenzone doppelt (eigener Tab „Löschen“ + Einstellungen) – erwartet, verschwindet mit T2. Prüft ein Test in `server-detail-page.test.tsx` genau eine Region „Server löschen“ und schlägt deshalb fehl, dem Lead melden (T2-Datei, nicht selbst ändern).

- [ ] **Step 10: Commit**

```bash
git add web/src/features/servers/{server-settings-tab,delete-server-section,server-form,collapsible-section,create-server-page}.tsx web/src/features/servers/server-settings-tab.test.tsx web/src/i18n/de.ts web/src/i18n/en.ts
git commit -m "feat(web): server form in sections, settings with danger zone"
```

---

### Task 7: Screenshots, Lint und Abschluss

**Files:**
- Create: `web/e2e/shots/manage.shots.ts`

**Interfaces:**
- Consumes: `shoot(page, name)`, `signInMock(page)` aus `./shoot` (Welle 0).

Die Navigation zu den Bereichen funktioniert auf diesem Branch noch über die alte Detailseite (Tabs). Nach T2 ändern sich die Wege; die Datei nutzt deshalb einen kleinen lokalen Helfer, der beides kann.

- [ ] **Step 1: `manage.shots.ts`**

```ts
import { test, type Page } from '@playwright/test'
import { shoot, signInMock } from './shoot'

/** Before T2: tabs on the detail page; after T2: links in the server tree. Both are tried. */
async function openArea(page: Page, name: string) {
  const tab = page.getByRole('tab', { name, exact: true })
  if (await tab.count()) await tab.click()
  else await page.getByRole('link', { name, exact: true }).first().click()
}

test('manage: files, plugins, backups, settings, create', async ({ page }) => {
  await signInMock(page)
  await page.getByRole('link', { name: 'survival', exact: true }).first().click()

  await openArea(page, 'Dateien')
  await page.getByRole('region', { name: 'Dateien' }).getByRole('button', { name: 'server.properties' }).click()
  await page.getByRole('textbox', { name: 'Inhalt von server.properties' }).waitFor()
  await shoot(page, 'manage-files-editor')

  await openArea(page, 'Plugins')
  await shoot(page, 'manage-plugins-installed')
  await page.getByRole('tab', { name: 'Entdecken' }).click()
  await page.getByRole('region', { name: 'Entdecken' }).getByRole('listitem').first().waitFor()
  await shoot(page, 'manage-plugins-discover')

  await openArea(page, 'Backups')
  await shoot(page, 'manage-backups')

  await openArea(page, 'Einstellungen')
  await shoot(page, 'manage-settings')

  await page.getByRole('link', { name: 'Server anlegen' }).first().click()
  await shoot(page, 'manage-create')
})
```

`shoot` setzt die Viewports selbst; die Datei-Ansicht bei 1440 px zeigt Liste und Editor nebeneinander, bei 390 px nur den Editor.

- [ ] **Step 2: Screenshots erzeugen und ansehen**

```bash
make test-vm SUITE=shots && ls web/screenshots | grep manage
```

Expected: 24 PNGs (`manage-*-{dark,light}-{desktop,mobile}.png`). Mit dem Read-Tool mindestens ansehen: `manage-files-editor-dark-desktop.png` (zwei Spalten), `manage-plugins-discover-light-desktop.png` (Kachel-Raster, Kacheln gleich hoch, Aktionen unten), `manage-settings-dark-mobile.png` (Gefahrenzone rot umrandet am Ende, keine horizontale Scrollleiste), `manage-create-light-desktop.png` (Sections, `max-w-3xl`). Auffälligkeiten (Abstände nicht nach §3.4, harte Farben, Überlauf) in den betroffenen Dateien beheben und erneut erzeugen.

- [ ] **Step 3: Volle eigene Prüfung**

```bash
make check && make test-vm SUITE=lint && make test-vm SUITE=web
```

Expected: alles grün. `SectionCard` darf in eigenen Dateien nicht mehr vorkommen:

```bash
grep -rn "SectionCard" web/src/features/{files,plugins,backups} web/src/features/servers/{server-settings-tab,delete-server-section,server-form,create-server-page}.tsx
```

Expected: keine Treffer.

- [ ] **Step 4: Commit und Push**

```bash
git add web/e2e/shots/manage.shots.ts
git commit -m "chore(web): screenshots of the server management areas"
git push -u origin feat/ui-manage
```

- [ ] **Step 5: An den Lead melden** – Branch, letzter Commit, die vier Bereichs-Verträge (siehe unten), Pfade der vier angesehenen Screenshots, offene Punkte (veraltete Props `onBusyChange`/`onOpenSettings` in `FilesTab`; `ServerSettingsLink` kann nach T2 auf typisiertes `<Link>` umgestellt werden; `renderServerTab` kann nach `web/src/test/` wandern, falls T3 dasselbe braucht).

## Verträge für T2 (final nach diesem Plan)

```ts
FilesTab({ server }: { server: Server })                              // onBusyChange/onOpenSettings: deprecated, ignoriert
PluginsTab({ server, kind }: { server: Server; kind: PluginKind })
BackupsTab({ server }: { server: Server })
ServerSettingsTab({ server, instance }: { server: Server; instance: InstanceConfig | null })  // enthält die Gefahrenzone
```
