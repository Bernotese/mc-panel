# UI-Redesign – Welle 1, T3: Startseite + Live (Implementation Plan)

> **For agentic workers:** Dieser Plan wird vom **Teammate T3** eines Agent-Teams umgesetzt (CLAUDE.md „Implementation with agent teams“). Worktree: `git worktree add ../mc-panel-wt/feat-ui-live -b feat/ui-live origin/feat/ui-foundation`. Steps use checkbox (`- [ ]`) syntax for tracking. Vor „fertig“: `make check` und `make test-vm SUITE=web` grün, Screenshots angesehen. Nur die Dateien unter „Besitz“ ändern; alles andere als Anfrage an den Lead melden.

**Goal:** Startseite als Minecraft-Mehrspieler-Liste mit Host-Slots, Konsole im Terminal-Stil mit voller Höhe, Spieler- und Monitoring-Bereich im neuen Design – und alle Tests dieser Bereiche unabhängig von der alten Tab-Seite machen.

**Architecture:** Die Startseite rendert pro Server eine `ServerRow` (Icon, Name, Adresse, Spieler + `PingBars`, MOTD-Zeile, Icon-Power-Buttons, Sparkline ab `lg`); der Host wird zu einer `Section` mit `StatSlot`s, deren Balken weiter der vorhandene `Meter` ist (Tests prüfen `aria-valuenow` in Bytes). Konsole, Spieler und Monitoring behalten ihre Datenlogik und bekommen nur Layout und Stil; ihre Tests rendern die Bereichs-Komponente direkt mit einem aus dem Mock geladenen `Server` statt über `ServerDetailPage` (die T2 umbaut).

**Tech Stack:** React 19, TanStack Query/Router, Tailwind v4 mit den Welle-0-Tokens, Vitest + Testing Library + MSW, Playwright (nur Screenshots).

**Spec:** `docs/superpowers/specs/2026-10-14-mc-panel-ui-redesign-design.md` (§3.4, §3.6, §5.1, §5.2, §6.2, §6.3); Welle 0: `docs/superpowers/plans/2026-10-14-ui-wave0-foundation.md`

## Besitz (nur diese Dateien ändern)

- `web/src/features/dashboard/*`
- `web/src/features/console/*` (inkl. `testing/`)
- `web/src/features/players/*`
- `web/src/features/monitoring/*`
- `web/e2e/shots/live.shots.ts` (neu)
- i18n: nur neue Schlüssel unter `dashboard.*` in `web/src/i18n/de.ts` und `en.ts`; keine bestehenden Schlüssel ändern oder löschen.

Nur lesen: `features/servers/*` (T2/T4), `components/*` (T1/Lead), `features/profile/section-card.tsx` (nicht mehr importieren, nicht löschen).

## Global Constraints

- Keine API-, Mock- oder OpenAPI-Änderung; keine neuen npm-Pakete.
- Bausteine aus Welle 0 mit genau diesen Props: `Section({title?, description?, actions?, tone?, headingLevel?, className?, children})`, `StatSlot({label, value, hint?, bar?, tone?, className?})`, `PingBars({level, label, className?})`, `PixelIcon({kind, state?, size?, className?})`, `McText({text, className?})`, `PageHeader({title, description?, actions?, breadcrumb?})`; Utilities `console-scope`, `bevel-inset`, `pixel-heading`, `font-pixel`, Farben `text-mc-*`, `text-status-*`, `bg-console`, `border-console-border`; `mcColorClass(color: LogColor)` aus `@/lib/mc-colors`.
- Bereichs-Komponenten nehmen nur `{ server: Server }`: `ConsoleTab`, `PlayersTab`, `MonitoringTab` (Spec §6.2). `OnlinePlayersCard({ serverId })` bleibt unverändert in der Signatur (T2 nutzt sie in der Übersicht).
- Fehlende Werte zeigen `t('monitoring.missing')` („–“), nie 0.
- Spacing: Sections untereinander `gap-6`/`space-y-6`, innerhalb `gap-4`, Formularfelder `gap-3`, dichte Listenzeilen `py-1.5`.
- Silkscreen (`pixel-heading`/`font-heading`) nur für Überschriften/Labels ≥ 12 px; Pixelify (`font-pixel`) nur für Zahlen und MOTD-Zeile.
- Konsole in beiden Modi dunkel (`console-scope`).
- Icon-only-Buttons haben immer `aria-label` und `title` (gleicher Text).
- Mac: nur `make check`; Tests nur `make test-vm SUITE=web` (bzw. `SUITE=shots`). Kein `SUITE=all` (VM-Lock ist geteilt).
- Commits: Conventional Commits, Scope `web`, mit den Attribution-Zeilen der Session.

## Review Focus

- **Server ohne Instanz und ohne Monitoring-Werte in der Liste:** `lobby` (nicht eingerichtet) zeigt Badge + Hinweistext, keine Power-Gruppe, keine Adresse, keine MOTD-Werte, kein Crash bei `metrics === undefined` (Test in Task 2 – vorhandene Dashboard-Tests).
- **Monitoring-Übersicht schlägt fehl oder liefert `null`-Werte:** Zeile zeigt „–“ für Spieler/TPS/CPU als eigene Textknoten, nie „NaN“ oder „0“ (vorhandener Test „marks an unreachable server“; zusätzlicher Test für `players: null, tps: null` bei laufendem Server in Task 2).
- **Konsole bei kleinem Fenster/Handy:** Die Höhe fällt nie unter 320 px und wird bei Größenänderung neu berechnet (Unit-Test für `fillHeight` in Task 3).
- **Recht `server.power` fehlt:** keine Icon-Buttons, stattdessen der Text „Keine Berechtigung für Start/Stopp.“ (vorhandener Test, Task 2).
- **Admin vs. Nutzer im Spieler-Bereich:** Link „Zu den Einstellungen“ nur für Admins, mit `href="/servers/1/settings"` (Test in Task 1).

---

## File Structure

| Datei | Verantwortung |
|---|---|
| `features/console/testing/render-section.tsx` (neu) | Test-Helfer: Server aus dem Mock laden und einen Bereich rendern |
| `features/console/console-tab.test.tsx`, `features/players/players-tab.test.tsx`, `features/monitoring/monitoring-tab.test.tsx` (ändern) | ohne `ServerDetailPage` |
| `features/players/players-tab.tsx` (ändern) | `onOpenSettings` → Link; Section; Icon-Buttons; Raster |
| `features/players/online-players-card.tsx` (ändern) | Section statt SectionCard |
| `features/dashboard/server-row.tsx` (neu) | eine Zeile der Mehrspieler-Liste |
| `features/dashboard/power-icon-buttons.tsx` (neu) | Start/Stopp/Neustart als Icon-Buttons mit Bestätigung |
| `features/dashboard/server-row.test.tsx` (neu) | Zusatz-Tests für die Zeile |
| `features/dashboard/dashboard-page.tsx` (ändern) | Liste statt Kachel-Raster |
| `features/dashboard/server-card.tsx` (löschen) | ersetzt durch `server-row.tsx` |
| `features/monitoring/server-metrics.tsx` (löschen) | Logik wandert in `server-row.tsx` |
| `features/monitoring/host-card.tsx` (ändern) | Section mit StatSlots |
| `features/monitoring/meter.tsx` (ändern) | eckig, Token-Farben |
| `features/monitoring/live-header.tsx`, `monitoring-tab.tsx`, `range-picker.tsx`, `down-alerts-switch.tsx`, `metric-charts.tsx` (ändern) | StatSlots, Sections, Spacing |
| `features/console/fill-height.ts` (+ Test) (neu) | Höhe „bis zum Fensterende“ |
| `features/console/log-text.tsx`, `log-view.tsx`, `command-input.tsx`, `console-tab.tsx` (ändern) | Terminal-Stil |
| `web/e2e/shots/live.shots.ts` (neu) | Screenshots Startseite, Konsole, Spieler, Monitoring |

---

### Task 1: Tests von der Tab-Seite lösen, Spieler-Link auf die Einstellungen

Dieser Task zuerst: Danach hängen die T3-Tests nicht mehr an `ServerDetailPage`, und T2 kann die Seite unabhängig umbauen.

**Files:**
- Create: `web/src/features/console/testing/render-section.tsx`
- Modify: `web/src/features/console/console-tab.test.tsx`, `web/src/features/players/players-tab.test.tsx`, `web/src/features/monitoring/monitoring-tab.test.tsx`, `web/src/features/players/players-tab.tsx`

**Interfaces:**
- Consumes: `serverQueryOptions(id)` aus `@/features/servers/api` (nur lesen), `renderWithProviders(ui, { me, queryClient? })` aus `@/test/render`, `useMe()` aus `@/features/auth/me`.
- Produces: `renderSection(serverId: number, section: (server: Server) => ReactNode, options: RenderOptions): ReturnType<typeof renderWithProviders>`; `PlayersTab({ server })`; `onOpenSettings` bleibt bis zur Integration optional und wird ignoriert.

- [ ] **Step 1: Helfer schreiben**

`web/src/features/console/testing/render-section.tsx`:

```tsx
import { useQuery } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { Server } from '@/api/types'
import { serverQueryOptions } from '@/features/servers/api'
import { renderWithProviders, type RenderOptions } from '@/test/render'

function WithServer({ serverId, children }: { serverId: number; children: (server: Server) => ReactNode }) {
  const query = useQuery(serverQueryOptions(serverId))
  return query.data ? <>{children(query.data)}</> : null
}

/** Renders one server section the way the server route does: with the server loaded from the (mock) API. */
export function renderSection(serverId: number, section: (server: Server) => ReactNode, options: RenderOptions = {}) {
  return renderWithProviders(<WithServer serverId={serverId}>{section}</WithServer>, options)
}
```

- [ ] **Step 2: Konsolen-Tests umstellen**

In `console-tab.test.tsx` den Import von `ServerDetailPage` entfernen, `ConsoleTab` und `renderSection` importieren und `openConsole` ersetzen:

```tsx
import { ConsoleTab } from './console-tab'
import { renderSection } from './testing/render-section'

async function openConsole(serverId = 1) {
  const user = userEvent.setup()
  await renderSection(serverId, (s) => <ConsoleTab server={s} />, { me: loginAs('admin') })
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
  const es = FakeEventSource.latest()
  act(() => es.open())
  return { user, es, log: screen.getByRole('log', { name: 'Server-Log' }) }
}
```

Den Test „refreshes the player data after a command“ (ab Zeile ~166) entsprechend anpassen:

```tsx
  it('refreshes the player data after a command', async () => {
    const user = userEvent.setup()
    const { queryClient } = await renderSection(1, (s) => <ConsoleTab server={s} />, { me: loginAs('admin') })
    queryClient.setQueryData(playerListsKey(1), { ops: [], whitelist: [], bans: [], errors: [] })
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
    act(() => FakeEventSource.latest().open())
    expect(queryClient.getQueryState(playerListsKey(1))?.isInvalidated).toBe(false)
    await user.type(screen.getByRole('textbox', { name: 'Befehl' }), 'op Steve{Enter}')
    await waitFor(() => expect(queryClient.getQueryState(playerListsKey(1))?.isInvalidated).toBe(true))
  })
```

Alle weiteren Stellen mit `getByRole('tab', …)` in der Datei suchen (`grep -n "'tab'" web/src/features/console/console-tab.test.tsx`) und ebenso auf `renderSection` umstellen.

- [ ] **Step 3: Monitoring-Tests umstellen**

In `monitoring-tab.test.tsx`:

```tsx
import { renderSection } from '@/features/console/testing/render-section'
import { MonitoringTab } from './monitoring-tab'

async function openMonitoring(serverId = 1, username = 'admin') {
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  const rendered = await renderSection(serverId, (s) => <MonitoringTab server={s} />, { me: loginAs(username, { sudo: true }) })
  return { user, ...rendered }
}
```

Im Test „polls the live values every 5 s only while the tab is open“ das Verlassen des Tabs durch Unmount ersetzen:

```tsx
    const { unmount } = await openMonitoring()
    await screen.findByText('1/20')
    await act(() => vi.advanceTimersByTimeAsync(LIVE_POLL_MS + 100))
    expect(await screen.findByText('2/20')).toBeInTheDocument()
    unmount()
    const calls = live.calls
    await act(() => vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 3))
    expect(live.calls).toBe(calls)
```

(`const { user } = …` an dieser Stelle entfällt; ESLint meldet sonst eine ungenutzte Variable.)

- [ ] **Step 4: Spieler-Tests umstellen und Link-Test schreiben**

In `players-tab.test.tsx`:

```tsx
import { renderSection } from '@/features/console/testing/render-section'
import { PlayersTab } from './players-tab'

async function openPlayers(serverId = 1, username = 'admin') {
  const user = userEvent.setup()
  await renderSection(serverId, (s) => <PlayersTab server={s} />, { me: loginAs(username) })
  return user
}
```

Test „leads admins to the whitelist setting“ ersetzen:

```tsx
  it('links admins to the whitelist setting', async () => {
    await openPlayers()
    expect(await screen.findByRole('link', { name: 'Zu den Einstellungen' })).toHaveAttribute('href', '/servers/1/settings')
  })
```

Im Test „tells other users …“ `queryByRole('button', …)` durch `queryByRole('link', { name: 'Zu den Einstellungen' })` ersetzen.

- [ ] **Step 5: Test laufen lassen (Spieler-Test soll fehlschlagen)**

Run: `make check && make test-vm SUITE=web`
Expected: Konsole/Monitoring grün; `links admins to the whitelist setting` FAIL (noch ein Button, kein Link).

- [ ] **Step 6: `PlayersTab` auf Link umstellen**

In `players-tab.tsx`:

```tsx
import { Link, type LinkProps } from '@tanstack/react-router'
import { useMe } from '@/features/auth/me'

// The settings route is added by T2 (feat/ui-shell); the cast keeps tsc green on feat/ui-live until both are integrated.
// The lead removes the cast when merging (see "Requests to lead").
function settingsLink(serverId: number): LinkProps {
  return { to: '/servers/$id/settings', params: { id: String(serverId) } } as unknown as LinkProps
}

/** `onOpenSettings` is ignored and goes away once T2's routes land (spec §6.2); see the note below. */
export function PlayersTab({ server }: { server: Server; onOpenSettings?: () => void }) {
  const { t } = useTranslation()
  const isAdmin = useMe()?.role === 'admin'
  …
```

Und in der Whitelist-Section:

```tsx
          {whitelistOn ? (
            <Muted>{t('players.whitelist.active')}</Muted>
          ) : isAdmin ? (
            <div className="flex flex-wrap items-center gap-2">
              <Muted>{t('players.whitelist.inactive')}</Muted>
              <Link {...settingsLink(server.id)} className="text-primary text-sm underline underline-offset-4">
                {t('players.whitelist.open_settings')}
              </Link>
            </div>
          ) : (
            <Muted>{t('players.whitelist.inactive_user')}</Muted>
          )}
```

Prüfen, ob der Test-Router den Pfad trotz fehlender Route zu `/servers/1/settings` auflöst (er matcht `$`); falls `href` leer bleibt, `href` stattdessen über `router.buildLocation` nicht anfassen, sondern im Test `toHaveAttribute('href', expect.stringContaining('/servers/1/settings'))` verwenden und das im Bericht an den Lead nennen.

Hinweis: `ServerDetailPage` (T2) übergibt auf `feat/ui-foundation` noch `onOpenSettings`. Damit `make check` auf `feat/ui-live` grün bleibt, bleibt die Prop optional und ungenutzt in der Signatur (siehe oben); T2 entfernt den Aufruf (Vertrag §6.2), der Lead entfernt die Prop bei der Integration.

- [ ] **Step 7: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web/src/features/console/testing/render-section.tsx web/src/features/console/console-tab.test.tsx web/src/features/monitoring/monitoring-tab.test.tsx web/src/features/players/players-tab.test.tsx web/src/features/players/players-tab.tsx
git commit -m "test(web): render console, players and monitoring sections without the tab page"
```

---

### Task 2: Startseite als Mehrspieler-Liste

**Files:**
- Create: `web/src/features/dashboard/server-row.tsx`, `web/src/features/dashboard/power-icon-buttons.tsx`, `web/src/features/dashboard/server-row.test.tsx`
- Modify: `web/src/features/dashboard/dashboard-page.tsx`, `web/src/features/monitoring/host-card.tsx`, `web/src/features/monitoring/meter.tsx`, `web/src/i18n/de.ts`, `web/src/i18n/en.ts`
- Delete: `web/src/features/dashboard/server-card.tsx`, `web/src/features/monitoring/server-metrics.tsx`

**Interfaces:**
- Consumes: `AddressLine({ address })`, `StatusBadge({ state })`, `describeInstance(instance, t)`, `formatGiB(bytes, locale)`, `formatCpu(pct, locale)` aus `@/features/servers/*`; `POWER_ACTIONS`, `powerAllowed(state, action)` aus `@/features/servers/status`; `usePower(server)` aus `@/features/servers/use-power`; `ConfirmDialog` aus `@/components/confirm-dialog`; `Sparkline` aus `@/components/charts/sparkline`; `reachability`, `alertKey`, `formatDecimal` aus `./model`.
- Produces: `ServerRow({ server, metrics }: { server: Server; metrics?: OverviewServer })`, `PowerIconButtons({ server }: { server: Pick<Server, 'id' | 'name' | 'status'> })`, `HostCard({ host })` (Name bleibt, Darstellung neu), neue i18n-Schlüssel `dashboard.list_label`.

Alle Assertions in `dashboard-page.test.tsx` bleiben unverändert gültig. Sie verlangen u. a.: `article` mit Namen des Servers; Gruppe „Steuerung für survival“ mit Buttons „Starten“/„Stoppen“/„Neu starten“; Text „Keine Berechtigung für Start/Stopp.“; „Läuft“/„Gestoppt“/„Nicht eingerichtet“; „Paper 1.21.11“; Adresse als eigener Text; „RAM: 2 / 5 GiB“ als eigener Text; „Neustart nötig“; Link `survival` → `/servers/1`; „3/20“, „19,8“, „12,5 %“, „Erreichbar“, „Nicht erreichbar“, „Server down“ und mindestens zwei „–“ jeweils als **eigene Textknoten**; Sparkline-`img`; Region „Host“ mit „5 GiB frei von 100 GiB“, Warnungstext, `meter` „Platte“ mit `aria-valuenow` in Bytes, `meter` „CPU“ mit `23.4`, „0,52“ und Link „Monitoring-Einstellungen“ → `/settings`.

- [ ] **Step 1: Zusatz-Test schreiben**

`web/src/features/dashboard/server-row.test.tsx`:

```tsx
import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { mockOverview, overviewServer } from '@/features/monitoring/test-utils'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { DashboardPage } from './dashboard-page'

describe('ServerRow', () => {
  it('shows dashes, not zeros, when a running server has no values yet', async () => {
    mockOverview({ servers: [overviewServer(1, { players: null, tps: null, cpu_percent: null })], host: null })
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const row = await screen.findByRole('article', { name: 'survival' })
    expect(await within(row).findAllByText('–')).toHaveLength(3)
    expect(within(row).queryByText('0')).not.toBeInTheDocument()
    expect(within(row).queryByText(/NaN/)).not.toBeInTheDocument()
  })

  it('lists the servers in a labelled list with icon power buttons that keep their names', async () => {
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const list = await screen.findByRole('list', { name: 'Server-Liste' })
    const row = within(list).getByRole('article', { name: 'survival' })
    const group = within(row).getByRole('group', { name: 'Steuerung für survival' })
    for (const name of ['Starten', 'Stoppen', 'Neu starten']) {
      const button = within(group).getByRole('button', { name })
      expect(button).toHaveAttribute('title', name)
    }
  })

  it('shows reachability as ping bars with a label', async () => {
    mockOverview({ servers: [overviewServer(1, { up: true })], host: null })
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const row = await screen.findByRole('article', { name: 'survival' })
    expect(await within(row).findByRole('img', { name: 'Erreichbar' })).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Test laufen lassen**

Run: `make test-vm SUITE=web`
Expected: FAIL – keine Liste „Server-Liste“, kein `title`, kein `img` „Erreichbar“.

- [ ] **Step 3: i18n**

`de.ts` im Block `dashboard`: `list_label: 'Server-Liste',`; `en.ts`: `list_label: 'Server list',`.

- [ ] **Step 4: `PowerIconButtons`**

`web/src/features/dashboard/power-icon-buttons.tsx`:

```tsx
import { Play, RotateCw, Square } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PowerAction, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { POWER_ACTIONS, powerAllowed } from '@/features/servers/status'
import { usePower } from '@/features/servers/use-power'

const ICON: Record<PowerAction, typeof Play> = { start: Play, stop: Square, restart: RotateCw }

/** Icon-only power buttons for the server list; same rules as PowerButtons (stop asks first). */
export function PowerIconButtons({ server }: { server: Pick<Server, 'id' | 'name' | 'status'> }) {
  const { t } = useTranslation()
  const [confirmStop, setConfirmStop] = useState(false)
  const power = usePower(server)
  return (
    <>
      <div role="group" aria-label={t('servers.power_group', { name: server.name })} className="flex gap-1.5">
        {POWER_ACTIONS.map((action) => {
          const Icon = ICON[action]
          const label = t(`servers.power.${action}`)
          return (
            <Button
              key={action}
              type="button"
              size="icon-sm"
              variant={action === 'start' ? 'default' : 'secondary'}
              aria-label={label}
              title={label}
              disabled={power.isPending || !powerAllowed(server.status.state, action)}
              onClick={() => (action === 'stop' ? setConfirmStop(true) : power.mutate(action))}
            >
              <Icon aria-hidden="true" />
            </Button>
          )
        })}
      </div>
      <ConfirmDialog
        open={confirmStop}
        onOpenChange={setConfirmStop}
        title={t('servers.stop_title', { name: server.name })}
        description={t('servers.stop_description')}
        confirmLabel={t('servers.power.stop')}
        destructive
        pending={power.isPending}
        onConfirm={() => power.mutate('stop', { onSettled: () => setConfirmStop(false) })}
      />
    </>
  )
}
```

- [ ] **Step 5: `ServerRow`**

`web/src/features/dashboard/server-row.tsx`:

```tsx
import { Link } from '@tanstack/react-router'
import { useId, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { OverviewServer, Server, ServerState } from '@/api/types'
import { Sparkline } from '@/components/charts/sparkline'
import { PingBars } from '@/components/ping-bars'
import { PixelIcon } from '@/components/pixel-icon'
import { alertKey, formatDecimal, reachability } from '@/features/monitoring/model'
import { AddressLine } from '@/features/servers/address-line'
import { describeInstance, formatCpu, formatGiB } from '@/features/servers/format'
import { StatusBadge } from '@/features/servers/status-badge'
import { useFormat } from '@/lib/format'
import { cn } from '@/lib/utils'
import { PowerIconButtons } from './power-icon-buttons'

const TRANSITIONAL: ReadonlySet<ServerState> = new Set<ServerState>(['creating', 'starting', 'stopping', 'restarting', 'unknown'])

function iconState(state: ServerState): 'online' | 'offline' | 'unknown' {
  if (state === 'running') return 'online'
  return TRANSITIONAL.has(state) ? 'unknown' : 'offline'
}

/** One "MOTD" fact; each value is its own text node so that "–" and numbers stay findable. */
function Fact({ label, children, className }: { label?: string; children: ReactNode; className?: string }) {
  return (
    <span className={cn('inline-flex items-baseline gap-1', className)}>
      {label ? <span className="text-muted-foreground font-sans text-xs">{label}</span> : null}
      <span>{children}</span>
    </span>
  )
}

/** One server in the multiplayer-style list (spec §5.1); every missing value is "–", never 0. */
export function ServerRow({ server, metrics }: { server: Server; metrics?: OverviewServer }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const headingId = useId()
  const { instance, status } = server
  const canPower = server.permissions.includes('server.power')
  const running = status.state === 'running'
  const showMemory = running && status.memory_limit_bytes > 0
  const missing = t('monitoring.missing')
  const reach = reachability(server, metrics)
  const spark = (metrics?.sparkline ?? []).map(([at, v]) => [at, v] as const)
  const sparkMax = spark.reduce((m, [, v]) => Math.max(m, v), 0)

  return (
    <article aria-labelledby={headingId} className="bg-card hover:bg-accent/40 flex flex-col gap-3 border p-3 transition-colors sm:flex-row sm:items-center">
      <div className="flex min-w-0 flex-1 gap-3">
        <PixelIcon kind="server" state={iconState(status.state)} size={40} className="mt-0.5" />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <h2 id={headingId} className="min-w-0 font-semibold break-words">
              <Link
                to="/servers/$id"
                params={{ id: String(server.id) }}
                className="focus-visible:ring-ring outline-none hover:underline focus-visible:ring-2"
              >
                {server.name}
              </Link>
            </h2>
            <StatusBadge state={status.state} />
            {instance ? <AddressLine address={server.address} /> : null}
            {metrics && instance ? (
              <span className="flex items-center gap-2 sm:ml-auto">
                <span className="font-pixel tabular-nums">
                  {metrics.players === null ? missing : t('monitoring.players_value', { players: metrics.players, max: metrics.players_max ?? missing })}
                </span>
                {reach ? (
                  <>
                    <PingBars level={reach === 'reachable' ? 5 : 0} label={t(`monitoring.reachability.${reach}`)} />
                    <span aria-hidden="true" className={cn('text-xs', reach === 'unreachable' ? 'text-destructive' : 'text-muted-foreground')}>
                      {t(`monitoring.reachability.${reach}`)}
                    </span>
                  </>
                ) : null}
              </span>
            ) : null}
          </div>
          <p className="font-pixel text-muted-foreground flex flex-wrap items-baseline gap-x-3 gap-y-1 text-sm tabular-nums">
            {instance ? <Fact className="text-foreground">{describeInstance(instance, t)}</Fact> : <Fact>{t('servers.not_configured_hint')}</Fact>}
            {instance && metrics ? (
              <>
                <Fact label={t('monitoring.tps')} className="text-mc-b">
                  {metrics.tps === null ? missing : formatDecimal(metrics.tps, fmt.locale)}
                </Fact>
                <Fact label={t('monitoring.cpu')}>
                  {metrics.cpu_percent === null ? missing : t('monitoring.percent', { value: formatCpu(metrics.cpu_percent, fmt.locale) })}
                </Fact>
              </>
            ) : null}
            {showMemory ? (
              <Fact>{t('servers.memory_usage', { used: formatGiB(status.memory_bytes, fmt.locale), limit: formatGiB(status.memory_limit_bytes, fmt.locale) })}</Fact>
            ) : null}
            {status.restart_required ? <Fact className="text-mc-6">{t('servers.restart_required')}</Fact> : null}
            {metrics?.alerts.map((kind) => (
              <Fact key={kind} className="text-destructive">
                {t(alertKey(kind))}
              </Fact>
            ))}
          </p>
          {instance && !canPower ? <p className="text-muted-foreground text-xs">{t('servers.no_power')}</p> : null}
        </div>
      </div>
      {spark.length >= 2 ? (
        <div className="hidden w-40 shrink-0 lg:block">
          <Sparkline points={spark} spanMs={3_600_000} label={t('monitoring.sparkline', { current: spark[spark.length - 1][1], max: sparkMax })} />
        </div>
      ) : null}
      {instance && canPower ? (
        <div className="shrink-0 self-end sm:self-center">
          <PowerIconButtons server={server} />
        </div>
      ) : null}
    </article>
  )
}
```

Hinweis zur Sparkline ab `lg`: Der vorhandene Test sucht das `img` mit `getByRole`; `hidden`-Elemente (`display:none`) sind in jsdom für `getByRole` unsichtbar, **aber** jsdom wertet Tailwind-Klassen nicht aus – die Klasse `hidden` setzt kein Inline-Style, der Test bleibt also grün. Läuft er dennoch rot, die Sparkline-Hülle mit `max-lg:hidden` statt `hidden lg:block` schreiben (gleiches Ergebnis im Browser).

- [ ] **Step 6: Dashboard auf die Liste umstellen**

In `dashboard-page.tsx` `ServerCard` durch `ServerRow` ersetzen und das Raster durch eine Liste:

```tsx
import { ServerRow } from './server-row'
…
    content = (
      <ul aria-label={t('dashboard.list_label')} className="space-y-2">
        {servers.data.map((server) => (
          <li key={server.id}>
            <ServerRow server={server} metrics={metricsById.get(server.id)} />
          </li>
        ))}
      </ul>
    )
```

Den Basisdomain-Hinweis als `Section` (aus `@/components/section`) mit `title={t('dashboard.base_domain_missing_title')}` statt der handgebauten `div` (Rolle `status` behalten: `<div role="status">` innerhalb der Section um Text und Link). Reihenfolge: PageHeader → Hinweis → Host → Liste, Abstände über einen Wrapper `<div className="space-y-6">`.

`server-card.tsx` und `features/monitoring/server-metrics.tsx` löschen (`git rm`); vorher `grep -rn "server-card\|server-metrics" web/src` – es dürfen keine Importe übrig sein.

- [ ] **Step 7: `Meter` und `HostCard`**

`meter.tsx`: `rounded-full` überall entfernen, Hintergrund `bg-background`, Höhe `h-1.5`; sonst unverändert (Rolle und `aria-*` bleiben, die Tests prüfen `aria-valuenow`).

`host-card.tsx` neu (Texte, Labels und Meter-Werte unverändert):

```tsx
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import type { HostSnapshot } from '@/api/types'
import { Section } from '@/components/section'
import { StatSlot } from '@/components/stat-slot'
import { formatCpu, formatGiB } from '@/features/servers/format'
import { useFormat } from '@/lib/format'
import { Meter } from './meter'
import { formatDecimal } from './model'

/** Admin-only host values on the dashboard (spec §5.1); bars stay Meter (aria-valuenow in bytes). */
export function HostCard({ host }: { host: HostSnapshot }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const low = host.alerts.includes('host.disk_low')
  const gib = (n: number) => formatGiB(n, fmt.locale)
  const cpu = host.cpu_percent === null ? null : t('monitoring.percent', { value: formatCpu(host.cpu_percent, fmt.locale) })
  const mem = host.memory_used !== null && host.memory_total !== null ? t('monitoring.host.memory_value', { used: gib(host.memory_used), total: gib(host.memory_total) }) : null
  const disk = host.disk_free !== null && host.disk_total !== null ? t('monitoring.host.disk_value', { free: gib(host.disk_free), total: gib(host.disk_total) }) : null
  return (
    <Section
      title={t('monitoring.host.title')}
      description={t('monitoring.host.description')}
      actions={
        <Link to="/settings" className="text-primary text-sm underline underline-offset-4">
          {t('monitoring.host.settings_link')}
        </Link>
      }
    >
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <StatSlot
          label={t('monitoring.host.cpu')}
          value={cpu}
          hint={host.cpu_percent !== null && cpu ? <Meter label={t('monitoring.host.cpu')} value={Math.min(100, Math.max(0, host.cpu_percent))} max={100} valueText={cpu} /> : undefined}
        />
        <StatSlot label={t('monitoring.host.load')} value={host.load1 === null ? null : formatDecimal(host.load1, fmt.locale, 2)} />
        <StatSlot
          label={t('monitoring.host.memory')}
          value={mem}
          hint={host.memory_used !== null && host.memory_total !== null && mem ? <Meter label={t('monitoring.host.memory')} value={host.memory_used} max={host.memory_total} valueText={mem} /> : undefined}
        />
        <StatSlot
          label={t('monitoring.host.disk')}
          value={disk}
          tone={low ? 'danger' : 'default'}
          hint={
            host.disk_free !== null && host.disk_total !== null && disk ? (
              <Meter label={t('monitoring.host.disk')} value={host.disk_total - host.disk_free} max={host.disk_total} danger={low} valueText={disk} />
            ) : undefined
          }
        />
      </div>
      {low ? (
        <p role="status" className="text-destructive mt-3 text-sm">
          {t('monitoring.host.disk_low')}
        </p>
      ) : null}
    </Section>
  )
}
```

`StatSlot` setzt um `hint` ein `truncate`-`div`; der `Meter` ist ein Block und wird dadurch nicht abgeschnitten. Falls der Balken optisch zu nah am Wert klebt, dem `Meter` `className`-frei über einen Wrapper `<div className="pt-1">` Luft geben.

- [ ] **Step 8: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS für `dashboard-page.test.tsx` (unverändert) und `server-row.test.tsx`.

- [ ] **Step 9: Commit**

```bash
git add -A web/src/features/dashboard web/src/features/monitoring/host-card.tsx web/src/features/monitoring/meter.tsx web/src/features/monitoring/server-metrics.tsx web/src/i18n/de.ts web/src/i18n/en.ts
git commit -m "feat(web): show servers as a multiplayer-style list with host slots"
```

---

### Task 3: Konsole im Terminal-Stil mit voller Höhe

**Files:**
- Create: `web/src/features/console/fill-height.ts`, `web/src/features/console/fill-height.test.ts`
- Modify: `web/src/features/console/log-text.tsx`, `log-view.tsx`, `command-input.tsx`, `console-tab.tsx`

**Interfaces:**
- Consumes: `mcColorClass` (`@/lib/mc-colors`), Utility `console-scope`.
- Produces: `fillHeight(viewport: number, top: number, bottomGap: number, min: number): number`; `useFillHeight<T extends HTMLElement>(min?: number, bottomGap?: number): { ref: RefObject<T | null>; height: number | null }`.

- [ ] **Step 1: Failing test**

`fill-height.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { fillHeight } from './fill-height'

describe('fillHeight', () => {
  it('fills the space from the element to the bottom of the window', () => {
    expect(fillHeight(900, 300, 24, 320)).toBe(576)
  })
  it('never goes below the minimum', () => {
    expect(fillHeight(600, 500, 24, 320)).toBe(320)
    expect(fillHeight(0, 0, 0, 320)).toBe(320)
  })
  it('rounds down to whole pixels', () => {
    expect(fillHeight(900.7, 300.2, 24, 320)).toBe(576)
  })
})
```

- [ ] **Step 2: Implementieren**

`fill-height.ts`:

```ts
import { useLayoutEffect, useRef, useState } from 'react'

/** Height from `top` (document coordinates) to the window's bottom edge minus `bottomGap`, at least `min`. */
export function fillHeight(viewport: number, top: number, bottomGap: number, min: number): number {
  return Math.max(min, Math.floor(viewport - top - bottomGap))
}

/** Lets an element reach the bottom of the window; recomputed on resize and when the page above it changes height. */
export function useFillHeight<T extends HTMLElement>(min = 320, bottomGap = 24) {
  const ref = useRef<T>(null)
  const [height, setHeight] = useState<number | null>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const update = () => setHeight(fillHeight(window.innerHeight, el.getBoundingClientRect().top + window.scrollY, bottomGap, min))
    update()
    window.addEventListener('resize', update)
    const observer = new ResizeObserver(update)
    observer.observe(document.body)
    return () => {
      window.removeEventListener('resize', update)
      observer.disconnect()
    }
  }, [min, bottomGap])
  return { ref, height }
}
```

- [ ] **Step 3: Farben und Fläche**

`log-text.tsx`: `COLOR_CLASS` entfernen, `mcColorClass(s.color)` verwenden (Import `@/lib/mc-colors`); `obfuscated` bleibt `blur-[2px]`.

`log-view.tsx`:
- Äußere Hülle `<div className="relative flex min-h-0 flex-1 flex-col">`.
- Log-Container-Klassen: `console-scope min-h-0 flex-1 overflow-y-auto border border-console-border p-2 font-mono text-xs leading-5 sm:text-sm` (statt `h-[60vh] min-h-64 … rounded-md bg-zinc-950 … text-zinc-100`).
- Leer-Text: `text-mc-8` statt `text-zinc-400`.
- `LineRow`: warn → `border-l-2 border-mc-6 bg-mc-6/10`, error → `border-l-2 border-mc-c bg-mc-c/10`.
- `CommandRow`: Hülle `my-1 border border-console-border bg-white/5 px-2 py-1`; Befehl `text-mc-b` mit Präfix `> ` statt `› `; pending/no-output `text-mc-8`; Fehler `text-mc-c`.
- „Neue Zeilen“-Button unverändert (nur `shadow` entfernen; Button bringt `bevel` mit).

- [ ] **Step 4: Eingabe im Terminal-Stil**

`command-input.tsx` – Formular und Zeile ersetzen (Logik unverändert):

```tsx
    <form onSubmit={submit} className="space-y-1">
      <div className="flex gap-2">
        <Label htmlFor={id} className="sr-only">
          {t('console.command')}
        </Label>
        <div className="console-scope border-console-border focus-within:ring-ring flex min-w-0 flex-1 items-center gap-2 border px-3 focus-within:ring-2">
          <span aria-hidden="true" className="text-mc-a font-mono">
            &gt;
          </span>
          <Input
            id={id}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={onKeyDown}
            disabled={disabled}
            autoComplete="off"
            autoCapitalize="off"
            spellCheck={false}
            placeholder={disabled ? (lockedText ?? t('console.stopped_placeholder')) : t('console.command_placeholder')}
            aria-invalid={problem ? true : undefined}
            aria-describedby={problem ? `${errorId} ${hintId}` : hintId}
            className="text-console-foreground placeholder:text-mc-8 h-9 border-0 bg-transparent! px-0 font-mono shadow-none! focus-visible:ring-0"
          />
        </div>
        <Button type="submit" disabled={disabled}>
          <SendHorizontal aria-hidden="true" />
          {t('console.send')}
        </Button>
      </div>
```

Fehler- und Hinweiszeile bleiben wie bisher.

- [ ] **Step 5: `ConsoleTab` füllt die Höhe**

```tsx
import { useFillHeight } from './fill-height'
…
  const fill = useFillHeight<HTMLDivElement>()
…
  return (
    <div ref={fill.ref} className="flex flex-col gap-3" style={fill.height ? { height: fill.height } : undefined}>
      <div className="flex flex-wrap items-center gap-3">
        …Toolbar unverändert, Filter-Input `w-full sm:w-64`…
      </div>
      <LogView entries={visible} />
      <CommandInput … />
    </div>
  )
```

In jsdom ist `height` eine Zahl ≥ 320; die `stubLogViewport`-Helfer der Tests stubben `clientHeight` am Log und bleiben gültig.

- [ ] **Step 6: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS (`fill-height.test.ts`, alle Konsolen-Tests).

- [ ] **Step 7: Commit**

```bash
git add web/src/features/console
git commit -m "feat(web): terminal-style console that fills the window"
```

---

### Task 4: Spieler-Bereich

**Files:**
- Modify: `web/src/features/players/players-tab.tsx`, `web/src/features/players/online-players-card.tsx`, `web/src/features/players/name-form.tsx` (nur Spacing, falls nötig)

**Interfaces:**
- Consumes: `Section`.
- Produces: unverändert `PlayersTab({ server })`, `OnlinePlayersCard({ serverId })`.

- [ ] **Step 1: Bestehende Tests als Sicherheitsnetz**

Run: `make test-vm SUITE=web` – `players-tab.test.tsx` und `online-players-card.test.tsx` grün (Stand nach Task 1). Sie prüfen Regionen „Online“, „Whitelist“, „Operatoren“, „Gebannte Spieler“, „Online-Spieler“ und Buttons über ihre `aria-label`s („Steve kicken“, „Steve bannen“, „Op geben: Steve“, „Op entziehen: Alex“, „Steve entbannen“, „Hinzufügen“).

- [ ] **Step 2: Sections und Raster**

- `SectionCard` → `Section` (Import `@/components/section`), gleiche `title`s.
- Container: `<div className="space-y-6">` mit Hinweisen oben; die Online-Section volle Breite; darunter `<div className="grid gap-6 xl:grid-cols-2">` mit Whitelist und Operatoren; Bans volle Breite.
- Hinweise „läuft nicht“ / „Neustart nötig“: `<p role="status" className="bevel-inset px-3 py-2 text-sm">`.
- Listen: `divide-y` behalten, Zeilen `py-1.5` (dicht), Namen `font-medium`, Op-Badge unverändert.
- Bans: die `dl` als vierspaltiges Raster ab `md` (`md:grid-cols-[auto_minmax(0,1fr)_auto_minmax(0,1fr)]`), sonst zweispaltig wie bisher.

- [ ] **Step 3: Icon-Buttons**

Jeder Aktions-Button wird icon-only mit unverändertem `aria-label` plus `title` (gleicher Text) und `size="icon-sm"`, `variant="secondary"`; sichtbarer Text entfällt. Icons aus `lucide-react`:

| Aktion | Icon |
|---|---|
| kick | `LogOut` |
| ban | `Gavel` |
| op | `ShieldPlus` |
| deop | `ShieldMinus` |
| whitelist_remove | `UserMinus` |
| pardon | `Undo2` |

Beispiel (Kick):

```tsx
<Button
  type="button"
  size="icon-sm"
  variant="secondary"
  disabled={disabled}
  aria-label={t('players.kick_label', { name: player.name })}
  title={t('players.kick_label', { name: player.name })}
  onClick={() => action.mutate({ action: 'kick', name: player.name })}
>
  <LogOut aria-hidden="true" />
</Button>
```

- [ ] **Step 4: `OnlinePlayersCard`**

`SectionCard` → `Section`; Zahl mit `font-pixel text-2xl tabular-nums`; Namen als `Badge variant="outline"` unverändert.

- [ ] **Step 5: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS. Prüfen, dass kein Test einen sichtbaren Button-Text wie „Kicken“ ohne `aria-label` sucht (`grep -n "name: 'Kicken'\|name: 'Bannen'" web/src/features/players/*.test.tsx`); „Bannen“ im Dialog ist der Bestätigungs-Button des `BanDialog` und bleibt Text.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/players
git commit -m "feat(web): denser players section with icon actions"
```

---

### Task 5: Monitoring-Bereich

**Files:**
- Modify: `web/src/features/monitoring/live-header.tsx`, `monitoring-tab.tsx`, `range-picker.tsx`, `down-alerts-switch.tsx`, `metric-charts.tsx`

**Interfaces:**
- Consumes: `Section`, `StatSlot`.
- Produces: `MonitoringTab({ server })` unverändert; Region „Jetzt“ bleibt (Tests: `getByRole('region', { name: 'Jetzt' })`, Liste „Online-Spieler“, Buttons „1 Stunde“/„7 Tage“, Switch „Down-Alerts für diesen Server“, vier `img`s der Diagramme).

- [ ] **Step 1: Zusatz-Test**

In `monitoring-tab.test.tsx` ergänzen:

```tsx
  it('shows a dash for missing live values', async () => {
    mockLive(liveMetrics({ tps: null, mspt: null }))
    mockMetrics()
    await openMonitoring()
    const now = await screen.findByRole('region', { name: 'Jetzt' })
    expect(await within(now).findAllByText('–')).toHaveLength(2)
  })
```

Vorher in `test-utils.tsx` prüfen, dass `liveMetrics(over)` `tps`/`mspt` überschreiben lässt (Signatur `liveMetrics(over: Partial<LiveMetrics>)`); der Mock-Server 1 läuft und hat eine TPS-Quelle ≠ `none`, sonst zeigt die TPS-Kachel den Quellen-Text – dann `tps_source: 'paper'` im `over` mitgeben.

- [ ] **Step 2: Test laufen lassen**

Run: `make test-vm SUITE=web`
Expected: PASS oder FAIL je nach heutiger Darstellung; nach Step 3 muss er grün sein.

- [ ] **Step 3: `LiveHeader` mit StatSlots**

Die äußere `section` durch `<Section title={t('monitoring.live.title')} headingLevel={3}>` ersetzen; die `dl` durch:

```tsx
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
          {items.map(([label, value]) => (
            <StatSlot key={label} label={label} value={value} />
          ))}
        </div>
```

Die `items` behalten ihre Werte; `missing` bleibt `t('monitoring.missing')` (StatSlot zeigt es als Text). Spielernamen-Liste: `li` mit `bevel-inset px-2 py-0.5 font-pixel` statt `bg-muted rounded`.

- [ ] **Step 4: Rest des Tabs**

- `monitoring-tab.tsx`: Container `space-y-6`; `RangePicker` und `MetricCharts` in eine `Section` ohne Titel mit `actions`-freiem Kopf: `<Section><div className="space-y-4"><RangePicker …/><MetricCharts …/></div></Section>`.
- `range-picker.tsx`: aktive Range `variant="default"`, sonst `variant="secondary"` (falls heute `outline`); Rolle `group` und Namen bleiben.
- `down-alerts-switch.tsx`: `rounded-md border p-4` → `Section` ohne Titel bzw. `bevel-inset p-4`; Switch-Label unverändert.
- `metric-charts.tsx`: Raster `gap-6 lg:grid-cols-2` bleibt; die Diagramm-Farben kommen über die `tone`s aus den Tokens (`stroke-primary`, `stroke-chart-2`, `stroke-muted-foreground` in `components/charts`, Besitz T1) – hier keine Farbklassen setzen.

- [ ] **Step 5: Tests grün**

Run: `make check && make test-vm SUITE=web`
Expected: PASS (alle Monitoring-Tests, inkl. Step 1).

- [ ] **Step 6: Commit**

```bash
git add web/src/features/monitoring
git commit -m "feat(web): monitoring section with stat slots"
```

---

### Task 6: Screenshots und Abschluss

**Files:**
- Create: `web/e2e/shots/live.shots.ts`

**Interfaces:**
- Consumes: `shoot(page, name)`, `signInMock(page, user?)` aus `./shoot` (Welle 0).

- [ ] **Step 1: Shots-Datei**

```ts
import type { Page } from '@playwright/test'
import { test } from '@playwright/test'
import { shoot, signInMock } from './shoot'

/** Works before and after T2's routes: old tabs or new section links. */
async function openSection(page: Page, name: string) {
  await page.getByRole('tab', { name, exact: true }).or(page.getByRole('link', { name, exact: true })).first().click()
}

test('live: dashboard, console, players, monitoring', async ({ page }) => {
  await signInMock(page)
  await shoot(page, 'live-dashboard')
  await page.getByRole('link', { name: 'survival', exact: true }).first().click()
  await openSection(page, 'Konsole')
  await page.getByRole('log', { name: 'Server-Log' }).waitFor()
  await shoot(page, 'live-console')
  await openSection(page, 'Spieler')
  await page.getByRole('region', { name: 'Online' }).waitFor()
  await shoot(page, 'live-players')
  await openSection(page, 'Monitoring')
  await page.getByRole('region', { name: 'Jetzt' }).waitFor()
  await shoot(page, 'live-monitoring')
})
```

- [ ] **Step 2: Screenshots erzeugen und ansehen**

```bash
make test-vm SUITE=shots && ls web/screenshots/live-*
```

Expected: 16 PNGs. Mit dem Read-Tool mindestens ansehen: `live-dashboard-dark-desktop.png`, `live-dashboard-light-mobile.png`, `live-console-light-desktop.png` (Konsole muss dunkel sein), `live-players-dark-desktop.png`. Prüfen: Liste im Mehrspieler-Stil, keine horizontale Scrollleiste bei 390 px, Konsole reicht bis knapp ans Fensterende, Abstände nach der Skala. Auffälligkeiten beheben und wiederholen.

- [ ] **Step 3: Gesamtprüfung**

```bash
make check && make test-vm SUITE=web && make test-vm SUITE=lint
```

Expected: alles grün. (`SUITE=lint` einmal am Ende, wegen des geteilten VM-Locks.)

- [ ] **Step 4: Commit und Push**

```bash
git add web/e2e/shots/live.shots.ts
git commit -m "chore(web): screenshots for dashboard and live sections"
git push -u origin feat/ui-live
```

- [ ] **Step 5: Bericht an den Lead**

Kurz: Commits, Screenshot-Pfade (`web/screenshots/live-*`), offene Punkte (insbesondere die `LinkProps`-Umwandlung in `players-tab.tsx` und die übergangsweise ignorierte Prop `onOpenSettings`).
