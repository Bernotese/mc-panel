import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { mockOverview, overviewServer } from '@/features/monitoring/test-utils'
import { db, enableTotp, loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderApp } from './test-utils'

const GIB = 1024 ** 3
const LISTS_HINT = 'Whitelist, Operatoren und Bans verwaltest du im Tab „Spieler“ der Serverseite; Änderungen dort wirken sofort.'

function mockServer(id: number) {
  const server = db.servers.find((s) => s.id === id)
  if (!server) throw new Error(`mock server ${id} missing`)
  return server
}

/** survival has the seeded dev-survival, which blocks deleting it (409 dev_exists): tests that delete it drop the dev. */
function dropDevs() {
  db.servers = db.servers.filter((s) => !s.dev)
}

function sectionList(name: string) {
  return screen.getByRole('list', { name: `Bereiche von ${name}` })
}
function sectionLinks(name: string) {
  return within(sectionList(name)).getAllByRole('link')
}
function sectionLink(name: string, label: string) {
  return within(sectionList(name)).queryByRole('link', { name: label })
}
async function openSection(name: string, label: string) {
  const user = userEvent.setup()
  await user.click(within(await screen.findByRole('list', { name: `Bereiche von ${name}` })).getByRole('link', { name: label }))
}

describe('ServerLayout', () => {
  it('shows status, address, resources and power buttons in the header and the details in the overview', async () => {
    const survival = mockServer(1)
    survival.status = {
      ...survival.status,
      memory_bytes: 2 * GIB,
      memory_limit_bytes: 5 * GIB,
      cpu_percent: 12.5,
      started_at: Date.now() - 5 * 60_000,
    }
    await renderApp('/servers/1', loginAs('admin'))

    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
    expect(sectionLinks('survival').map((l) => l.textContent)).toEqual(['Übersicht', 'Monitoring', 'Konsole', 'Spieler', 'Dateien', 'Plugins', 'Backups', 'Einstellungen'])
    const header = screen.getByRole('region', { name: 'survival' })
    expect(within(header).getByText('Läuft')).toBeInTheDocument()
    expect(within(header).getByText('survival.mc.example.com')).toBeInTheDocument()
    expect(within(header).getByText('2 / 5 GiB')).toBeInTheDocument()
    expect(within(header).getByText('12,5 %')).toBeInTheDocument()
    expect(within(header).getByRole('group', { name: 'Steuerung für survival' })).toBeInTheDocument()
    const state = screen.getByRole('region', { name: 'Zustand' })
    expect(within(state).getByText('Paper 1.21.11')).toBeInTheDocument()
    expect(within(state).getByText('Java 21')).toBeInTheDocument()
    expect(within(state).getByText('vor 5 Minuten')).toBeInTheDocument()
    expect(screen.queryByText('Neustart nötig')).not.toBeInTheDocument()
    expect(await within(screen.getByRole('region', { name: 'Online-Spieler' })).findByText('2 / 20')).toBeInTheDocument()
  })

  it('links the web map in header and overview only while a root web route is active', async () => {
    await renderApp('/servers/1', loginAs('alice'))
    const header = await screen.findByRole('region', { name: 'survival' })
    const link = within(header).getByRole('link', { name: 'Webkarte öffnen' })
    expect(link).toHaveAttribute('href', 'https://survival.mc.example.com')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(within(screen.getByRole('region', { name: 'Zustand' })).getByRole('link', { name: 'Webkarte öffnen' })).toBeInTheDocument()
  })

  // pending settles to active on the next GET of the mock, so error and off stand for "not active".
  it.each([
    ['error', { webState: 'error' }],
    ['off', { webRoutes: [] }],
    ['only published under a path', { webRoutes: [{ path: 'afk-smoke', port: 8200 }] }],
  ] as const)('shows no web map link while the web interface is %s', async (_, patch) => {
    Object.assign(mockServer(1), patch)
    await renderApp('/servers/1', loginAs('admin'))
    const header = await screen.findByRole('region', { name: 'survival' })
    expect(within(header).getByText('survival.mc.example.com')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Webkarte öffnen' })).not.toBeInTheDocument()
  })

  it('shows admins the last error of a failed server', async () => {
    const survival = mockServer(1)
    survival.status = { ...survival.status, state: 'error', last_error: 'image pull failed: manifest unknown' }
    await renderApp('/servers/1', loginAs('admin'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Letzter Fehler')
    expect(alert).toHaveTextContent('image pull failed: manifest unknown')
    expect(within(screen.getByRole('region', { name: 'survival' })).getByText('Fehler')).toBeInTheDocument()
  })

  it('shows other users a generic error without the details', async () => {
    const survival = mockServer(1)
    survival.status = { ...survival.status, state: 'error', last_error: 'image pull failed: manifest unknown' }
    await renderApp('/servers/1', loginAs('alice'))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Der Server meldet einen Fehler. Details sieht ein Admin.')
    expect(screen.queryByText('image pull failed: manifest unknown')).not.toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'survival' })).getByText('Fehler')).toBeInTheDocument()
  })

  it('offers no restart while the agent is unreachable', async () => {
    const survival = mockServer(1)
    survival.status = { ...survival.status, state: 'unknown' }
    await renderApp('/servers/1', loginAs('admin'))
    const controls = await screen.findByRole('group', { name: 'Steuerung für survival' })
    expect(within(controls).getByRole('button', { name: 'Neu starten' })).toBeDisabled()
    expect(within(controls).getByRole('button', { name: 'Starten' })).toBeEnabled()
    expect(within(controls).getByRole('button', { name: 'Stoppen' })).toBeEnabled()
  })

  it('offers a restart while changes are pending and clears the banner afterwards', async () => {
    const survival = mockServer(1)
    survival.revision += 1
    survival.status = { ...survival.status, restart_required: true }
    const user = userEvent.setup()
    await renderApp('/servers/1', loginAs('alice'))
    expect(await screen.findByText('Neustart nötig')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Jetzt neu starten' }))
    expect(await screen.findByText('Neustart für „survival“ angefordert.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('Neustart nötig')).not.toBeInTheDocument())
  })

  it('shows a restricted user only the overview and monitoring', async () => {
    await renderApp('/servers/2', loginAs('alice'))
    expect(await screen.findByRole('heading', { name: 'creative', level: 1 })).toBeInTheDocument()
    expect(sectionLinks('creative').map((l) => l.textContent)).toEqual(['Übersicht', 'Monitoring'])
    expect(screen.queryByRole('group', { name: 'Steuerung für creative' })).not.toBeInTheDocument()
    expect(screen.getByText('Keine Berechtigung für Start/Stopp.')).toBeInTheDocument()
  })

  it('shows the console to a user with the console permission', async () => {
    enableTotp('alice') // full-trust permissions require 2FA (SEC-10)
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.console'] }])
    await renderApp('/servers/1', loginAs('alice'))
    await screen.findByRole('heading', { name: 'survival', level: 1 })
    expect(sectionLink('survival', 'Konsole')).toBeInTheDocument()
    expect(sectionLink('survival', 'Spieler')).toBeInTheDocument()
    expect(sectionLink('survival', 'Einstellungen')).not.toBeInTheDocument()
  })

  it('explains a server the user may not see', async () => {
    await renderApp('/servers/3', loginAs('alice'))
    expect(await screen.findByText('Du hast keine Berechtigung, diesen Server anzusehen.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Zur Übersicht' })).toHaveAttribute('href', '/')
  })

  it('stops showing a server that was deleted while the page was open', async () => {
    const { queryClient } = await renderApp('/servers/1', loginAs('admin'))
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
    db.servers = db.servers.filter((s) => s.id !== 1)
    await queryClient.refetchQueries({ queryKey: ['servers', 1] })
    expect(await screen.findByText('Diesen Server gibt es nicht (mehr).')).toBeInTheDocument()
    expect(screen.queryByRole('group', { name: 'Steuerung für survival' })).not.toBeInTheDocument()
  })

  it('stops showing a server after the view permission was revoked', async () => {
    const { queryClient } = await renderApp('/servers/1', loginAs('alice'))
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
    db.grants.set(2, [])
    await queryClient.refetchQueries({ queryKey: ['servers', 1] })
    expect(await screen.findByText('Du hast keine Berechtigung, diesen Server anzusehen.')).toBeInTheDocument()
  })

  describe('when a refresh fails (issue #155)', () => {
    /** Answers GET /servers/1 with `failure` while set; otherwise the regular mock answers. */
    function breakDetail(failure: () => Response) {
      const state: { broken: boolean } = { broken: true }
      server.use(http.get('*/api/servers/:id', () => (state.broken ? failure() : undefined)))
      return state
    }

    it('keeps the last data, marks it stale with time and retry, and clears that on recovery', async () => {
      const survival = mockServer(1)
      survival.status = { ...survival.status, memory_bytes: 2 * GIB, memory_limit_bytes: 5 * GIB, cpu_percent: 12.5 }
      const { queryClient } = await renderApp('/servers/1', loginAs('admin'))
      const header = await screen.findByRole('region', { name: 'survival' })
      expect(within(header).getByText('Läuft')).toBeInTheDocument()
      expect(within(header).queryByTestId('stale-notice')).not.toBeInTheDocument()

      const outage = breakDetail(() => HttpResponse.error())
      await queryClient.refetchQueries({ queryKey: ['servers', 1], exact: true })
      const notice = await within(header).findByTestId('stale-notice')
      expect(notice).toHaveRole('status')
      expect(notice).toHaveTextContent('Keine Verbindung zum Panel.')
      expect(notice).toHaveTextContent(/Angezeigt wird der Stand von \d\d:\d\d\./)
      expect(within(header).getByText('Zuletzt: Läuft')).toBeInTheDocument()
      expect(within(header).queryByText('Läuft')).not.toBeInTheDocument()
      // The values stay readable, but muted: they are not current.
      expect(within(header).getByText('2 / 5 GiB').closest('[data-stale]')).not.toBeNull()
      expect(within(header).getByText('12,5 %').closest('[data-stale]')).not.toBeNull()

      outage.broken = false
      await userEvent.setup().click(within(notice).getByRole('button', { name: 'Erneut versuchen' }))
      await waitFor(() => expect(within(header).queryByTestId('stale-notice')).not.toBeInTheDocument())
      expect(within(header).getByText('Läuft')).toBeInTheDocument()
      expect(within(header).getByText('2 / 5 GiB').closest('[data-stale]')).toBeNull()
    })

    it('names a missing agent (503)', async () => {
      const { queryClient } = await renderApp('/servers/1', loginAs('admin'))
      const header = await screen.findByRole('region', { name: 'survival' })
      breakDetail(() => HttpResponse.json({ error: 'agent_unavailable', message: 'no agent' }, { status: 503 }))
      await queryClient.refetchQueries({ queryKey: ['servers', 1], exact: true })
      expect(await within(header).findByTestId('stale-notice')).toHaveTextContent('Der Agent ist nicht erreichbar.')
      expect(within(header).getByText('Zuletzt: Läuft')).toBeInTheDocument()
    })

    it('marks only the measured values stale when the monitoring overview fails', async () => {
      mockOverview({ servers: [overviewServer(1, { players: 3, players_max: 20 })], host: null })
      const { queryClient } = await renderApp('/servers/1', loginAs('admin'))
      const header = await screen.findByRole('region', { name: 'survival' })
      const players = await within(header).findByText('3/20')
      expect(players.closest('[data-stale]')).toBeNull()
      server.use(http.get('*/api/monitoring/overview', () => HttpResponse.json({ error: 'internal', message: 'boom' }, { status: 500 })))
      await queryClient.refetchQueries({ queryKey: ['monitoring', 'overview'] })
      expect(await within(header).findByTestId('stale-notice')).toHaveTextContent('Interner Serverfehler.')
      expect(within(header).getByText('3/20').closest('[data-stale]')).not.toBeNull()
      expect(within(header).getByText('Läuft')).toBeInTheDocument()
    })
  })

  it('explains a server that does not exist', async () => {
    await renderApp('/servers/999', loginAs('admin'))
    expect(await screen.findByText('Diesen Server gibt es nicht (mehr).')).toBeInTheDocument()
  })

  it('redirects a section the user may not open to the overview', async () => {
    const { router } = await renderApp('/servers/1/files', loginAs('alice'))
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/1'))
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
  })

  it('leaves a section whose permission is revoked while it is open', async () => {
    enableTotp('alice') // full-trust permissions require 2FA (SEC-10)
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.console'] }])
    const { router, queryClient } = await renderApp('/servers/1/players', loginAs('alice'))
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/servers/1/players')
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view'] }])
    await queryClient.refetchQueries({ queryKey: ['servers', 1] })
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/1'))
    expect(await screen.findByRole('region', { name: 'Zustand' })).toBeInTheDocument()
  })

  it('keeps the section after a reload', async () => {
    const { router } = await renderApp('/servers/1/backups', loginAs('admin'))
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/servers/1/backups')
    expect(sectionLink('survival', 'Backups')).toHaveAttribute('aria-current', 'page')
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
    expect(within(header).getByText('RAM').parentElement).toHaveTextContent('–')
  })

  it('saves settings and points out the pending restart', async () => {
    const user = userEvent.setup()
    await renderApp('/servers/1/settings', loginAs('admin', { sudo: true }))
    expect(await screen.findByText('Der Server läuft. Änderungen werden erst nach einem Neustart aktiv.')).toBeInTheDocument()
    const save = screen.getByRole('button', { name: 'Änderungen speichern' })
    expect(save).toBeDisabled()
    await screen.findByRole('option', { name: '1.21.11' })
    expect(screen.getByRole('combobox', { name: 'Minecraft-Version' })).toHaveValue('1.21.11')
    expect(screen.queryByLabelText('Name')).not.toBeInTheDocument()
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Eigenschaften' }))
    expect(screen.getByText(LISTS_HINT)).toBeInTheDocument()
    const motd = screen.getByLabelText('Nachricht im Serverbrowser (MOTD)')
    expect(motd).toHaveValue('survival')
    await user.clear(motd)
    await user.type(motd, 'Willkommen')
    expect(save).toBeEnabled()
    await user.click(save)

    expect(await screen.findByText('Änderungen gespeichert. Sie werden beim nächsten Neustart aktiv.')).toBeInTheDocument()
    expect(mockServer(1).instance?.properties.motd).toBe('Willkommen')
    // The header sits above every section: the banner shows up without leaving the settings.
    expect(await screen.findByText('Neustart nötig')).toBeInTheDocument()
    await openSection('survival', 'Übersicht')
    expect(await screen.findByRole('region', { name: 'Zustand' })).toBeInTheDocument()
  })

  it('deletes the server after typing its name', async () => {
    dropDevs()
    const user = userEvent.setup()
    const { router } = await renderApp('/servers/1/settings', loginAs('admin', { sudo: true }))
    expect(await screen.findByText(/deleted\/survival-<Zeitpunkt>/)).toBeInTheDocument()
    const submit = screen.getByRole('button', { name: 'Server löschen' })
    expect(submit).toBeDisabled()
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
    expect(submit).toBeEnabled()
    await user.click(submit)
    expect(await screen.findByText('Server „survival“ gelöscht.')).toBeInTheDocument()
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(db.servers.some((s) => s.id === 1)).toBe(false)
  })

  it('blocks deleting a server with a dev and links the dev', async () => {
    const user = userEvent.setup()
    await renderApp('/servers/1/settings', loginAs('admin', { sudo: true }))
    const danger = await screen.findByRole('region', { name: 'Server löschen' })
    expect(within(danger).getByText('Dieser Server hat eine Dev-Instanz. Lösche zuerst sie.')).toBeInTheDocument()
    expect(within(danger).getByRole('link', { name: 'Zu dev-survival' })).toHaveAttribute('href', '/servers/5/settings')
    await user.type(within(danger).getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
    expect(within(danger).getByRole('button', { name: 'Server löschen' })).toBeDisabled()
  })

  it('lets admins delete a server that was never set up', async () => {
    const user = userEvent.setup()
    await renderApp('/servers/3', loginAs('admin', { sudo: true }))
    expect(
      await screen.findByText('Dieser Server stammt aus der Zeit vor der Instanzverwaltung und hat noch keine Minecraft-Instanz. Du kannst ihn nur löschen.'),
    ).toBeInTheDocument()
    expect(sectionLinks('lobby').map((l) => l.textContent)).toEqual(['Übersicht'])
    expect(screen.getByText('Der Eintrag und alle Rechte darauf werden entfernt. Serverdaten gibt es keine.')).toBeInTheDocument()
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „lobby“ eintippen'), 'lobby')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    expect(await screen.findByText('Server „lobby“ gelöscht.')).toBeInTheDocument()
  })

  it('redirects a section of a server without instance to the overview', async () => {
    const { router } = await renderApp('/servers/3/settings', loginAs('admin'))
    await waitFor(() => expect(router.state.location.pathname).toBe('/servers/3'))
    expect(await screen.findByLabelText('Zum Bestätigen den Namen „lobby“ eintippen')).toBeInTheDocument()
  })

  it('shows no error after the user cancelled the sudo dialog', async () => {
    const user = userEvent.setup()
    await renderApp('/servers/3', loginAs('admin'))
    await user.type(await screen.findByLabelText('Zum Bestätigen den Namen „lobby“ eintippen'), 'lobby')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.click(within(sudo).getByRole('button', { name: 'Abbrechen' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Aktion bestätigen' })).not.toBeInTheDocument())
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect(screen.queryByText('Diese Aktion muss bestätigt werden.')).not.toBeInTheDocument()
    expect(db.servers.some((s) => s.id === 3)).toBe(true)
  })

  it('shows the files section only with the files permission', async () => {
    await renderApp('/servers/1', loginAs('alice'))
    await screen.findByRole('heading', { name: 'survival', level: 1 })
    expect(sectionLink('survival', 'Dateien')).not.toBeInTheDocument()
  })

  it('blocks starting while an extraction runs', async () => {
    server.use(
      http.get('*/api/servers/:id/files/jobs', () =>
        HttpResponse.json({
          job: { id: 'j', state: 'running', path: 'w.zip', dest: 'w', bytes: 1, entries: 1, total_bytes: 10, total_entries: 10, error: '', started_at: Date.now(), finished_at: 0 },
        }),
      ),
    )
    await renderApp('/servers/2', loginAs('admin'))
    expect(await screen.findByText('Entpacken läuft – Starten ist erst danach möglich.')).toBeInTheDocument()
    const group = screen.getByRole('group', { name: 'Steuerung für creative' })
    expect(within(group).getByRole('button', { name: 'Starten' })).toBeDisabled()
  })

  it('shows the data job lock text when the panel answers a start with 409', async () => {
    server.use(http.post('*/api/servers/:id/power', () => HttpResponse.json({ error: 'conflict', message: 'extracting' }, { status: 409 })))
    const user = userEvent.setup()
    await renderApp('/servers/2', loginAs('admin'))
    const group = await screen.findByRole('group', { name: 'Steuerung für creative' })
    await user.click(within(group).getByRole('button', { name: 'Starten' }))
    expect(await screen.findByText('Gerade wird ein Archiv entpackt oder ein Backup wiederhergestellt – Starten ist erst danach möglich.')).toBeInTheDocument()
    expect(screen.queryByText('Konflikt mit dem aktuellen Zustand')).not.toBeInTheDocument()
  })

  it('names the plugins section after the server type', async () => {
    await renderApp('/servers/4', loginAs('admin'))
    await screen.findByRole('heading', { name: 'modded', level: 1 })
    expect(sectionLink('modded', 'Mods')).toBeInTheDocument()
    expect(sectionLink('modded', 'Plugins')).not.toBeInTheDocument()
  })

  it('shows no plugins section on vanilla servers', async () => {
    await renderApp('/servers/2', loginAs('admin'))
    expect(await screen.findByRole('heading', { name: 'creative', level: 1 })).toBeInTheDocument()
    expect(sectionLink('creative', 'Plugins')).not.toBeInTheDocument()
    expect(sectionLink('creative', 'Mods')).not.toBeInTheDocument()
  })

  it('shows the plugins section only with the plugins permission', async () => {
    await renderApp('/servers/1', loginAs('alice'))
    await screen.findByRole('heading', { name: 'survival', level: 1 })
    expect(sectionLink('survival', 'Plugins')).not.toBeInTheDocument()
  })

  it('shows the plugins section to a user with the plugins permission', async () => {
    enableTotp('alice') // full-trust permissions require 2FA (SEC-10)
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.plugins'] }])
    await renderApp('/servers/1', loginAs('alice'))
    await screen.findByRole('heading', { name: 'survival', level: 1 })
    expect(sectionLink('survival', 'Plugins')).toBeInTheDocument()
    expect(sectionLink('survival', 'Dateien')).not.toBeInTheDocument()
  })

  describe('when the job finishes', () => {
    afterEach(() => {
      vi.useRealTimers()
    })

    it('lifts the power lock and stops polling', async () => {
      vi.useFakeTimers({ shouldAdvanceTime: true })
      let polls = 0
      const running = { id: 'j', state: 'running', path: 'w.zip', dest: 'w', bytes: 1, entries: 1, total_bytes: 10, total_entries: 10, error: '', started_at: Date.now(), finished_at: 0 }
      server.use(
        http.get('*/api/servers/:id/files/jobs', () => {
          polls += 1
          return HttpResponse.json({ job: polls === 1 ? running : { ...running, state: 'done', finished_at: Date.now() } })
        }),
      )
      await renderApp('/servers/2', loginAs('admin'))
      const group = await screen.findByRole('group', { name: 'Steuerung für creative' })
      expect((await screen.findAllByText('Entpacken läuft – Starten ist erst danach möglich.')).length).toBeGreaterThan(0)
      expect(within(group).getByRole('button', { name: 'Starten' })).toBeDisabled()
      await act(() => vi.advanceTimersByTimeAsync(2_100))
      await waitFor(() => expect(within(group).getByRole('button', { name: 'Starten' })).toBeEnabled())
      const seen = polls
      await act(() => vi.advanceTimersByTimeAsync(10_000))
      expect(polls).toBe(seen)
    })
  })

  it('shows the backups section only with the backups permission', async () => {
    await renderApp('/servers/1', loginAs('alice'))
    await screen.findByRole('heading', { name: 'survival', level: 1 })
    expect(sectionLink('survival', 'Backups')).not.toBeInTheDocument()
  })

  it('shows the backups section to a user with the backups permission, also on vanilla servers', async () => {
    enableTotp('alice') // full-trust permissions require 2FA (SEC-10)
    db.grants.set(2, [{ server_id: 2, permissions: ['server.view', 'server.backups'] }])
    await renderApp('/servers/2', loginAs('alice'))
    await screen.findByRole('heading', { name: 'creative', level: 1 })
    expect(sectionLink('creative', 'Backups')).toBeInTheDocument()
  })

  it('deletes the backups too when the checkbox is set', async () => {
    dropDevs()
    const user = userEvent.setup()
    let query: string | null = null
    server.use(
      http.delete('*/api/servers/:id', ({ request }) => {
        query = new URL(request.url).search
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await renderApp('/servers/1/settings', loginAs('admin', { sudo: true }))
    expect(await screen.findByText('Ohne diese Option bleiben die Backups des Servers auf dem Host liegen und belegen weiter Platz.')).toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: 'Backups ebenfalls löschen' }))
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    await waitFor(() => expect(query).toBe('?purge_backups=true'))
  })

  it('offers no backup checkbox for a server without instance and sends no parameter by default', async () => {
    const user = userEvent.setup()
    let query: string | null = null
    server.use(
      http.delete('*/api/servers/:id', ({ request }) => {
        query = new URL(request.url).search
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await renderApp('/servers/3', loginAs('admin', { sudo: true }))
    await screen.findByLabelText('Zum Bestätigen den Namen „lobby“ eintippen')
    expect(screen.queryByRole('checkbox', { name: 'Backups ebenfalls löschen' })).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „lobby“ eintippen'), 'lobby')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    await waitFor(() => expect(query).toBe(''))
  })

  it('explains a 409 when the server cannot be deleted while a restore runs', async () => {
    dropDevs()
    const user = userEvent.setup()
    server.use(
      http.delete('*/api/servers/:id', () =>
        HttpResponse.json({ error: 'conflict', message: 'a file or backup job is running for this server; try again when it is done' }, { status: 409 }),
      ),
    )
    await renderApp('/servers/1/settings', loginAs('admin', { sudo: true }))
    await user.type(await screen.findByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    expect(
      await screen.findByText('Für diesen Server wird gerade ein Archiv entpackt oder ein Backup wiederhergestellt. Lösche ihn, wenn der Auftrag fertig ist.'),
    ).toBeInTheDocument()
    expect(screen.queryByText('Konflikt mit dem aktuellen Zustand')).not.toBeInTheDocument()
    expect(db.servers.some((s) => s.id === 1)).toBe(true)
  })

  it('links the purge hint to the checkbox and says that the backups are being deleted', async () => {
    dropDevs()
    const user = userEvent.setup()
    let release: () => void = () => undefined
    server.use(
      http.delete('*/api/servers/:id', async () => {
        await new Promise<void>((resolve) => {
          release = resolve
        })
        return new HttpResponse(null, { status: 204 })
      }),
    )
    await renderApp('/servers/1/settings', loginAs('admin', { sudo: true }))
    const purge = await screen.findByRole('checkbox', { name: 'Backups ebenfalls löschen' })
    expect(purge).toHaveAccessibleDescription('Ohne diese Option bleiben die Backups des Servers auf dem Host liegen und belegen weiter Platz.')
    await user.click(purge)
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    expect(await screen.findByText('Server und Backups werden gelöscht …')).toBeInTheDocument()
    release()
    expect(await screen.findByText('Server „survival“ gelöscht.')).toBeInTheDocument()
  })
})
