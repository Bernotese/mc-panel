import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { ServerDetailPage } from './server-detail-page'

const GIB = 1024 ** 3
const LISTS_HINT = 'Whitelist, Operatoren und Bans verwaltest du im Tab „Spieler“ der Serverseite; Änderungen dort wirken sofort.'

function mockServer(id: number) {
  const server = db.servers.find((s) => s.id === id)
  if (!server) throw new Error(`mock server ${id} missing`)
  return server
}

describe('ServerDetailPage', () => {
  it('shows status, address, resources and power buttons on the overview', async () => {
    const survival = mockServer(1)
    survival.status = {
      ...survival.status,
      memory_bytes: 2 * GIB,
      memory_limit_bytes: 5 * GIB,
      cpu_percent: 12.5,
      started_at: Date.now() - 5 * 60_000,
    }
    await renderWithProviders(<ServerDetailPage serverId={1} />, { path: '/servers/1', me: loginAs('admin') })

    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Übersicht', 'Konsole', 'Spieler', 'Dateien', 'Einstellungen', 'Löschen'])
    const state = screen.getByRole('region', { name: 'Zustand' })
    expect(within(state).getByText('Läuft')).toBeInTheDocument()
    expect(within(state).getByText('survival.mc.example.com')).toBeInTheDocument()
    expect(within(state).getByText('Paper 1.21.11')).toBeInTheDocument()
    expect(within(state).getByText('Java 21')).toBeInTheDocument()
    expect(within(state).getByText('2 / 5 GiB')).toBeInTheDocument()
    expect(within(state).getByText('12,5 %')).toBeInTheDocument()
    expect(within(state).getByText('vor 5 Minuten')).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Steuerung für survival' })).toBeInTheDocument()
    expect(screen.queryByText('Neustart nötig')).not.toBeInTheDocument()
    expect(await within(screen.getByRole('region', { name: 'Online-Spieler' })).findByText('2 / 20')).toBeInTheDocument()
  })

  it('shows admins the last error of a failed server', async () => {
    const survival = mockServer(1)
    survival.status = { ...survival.status, state: 'error', last_error: 'image pull failed: manifest unknown' }
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('admin') })
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Letzter Fehler')
    expect(alert).toHaveTextContent('image pull failed: manifest unknown')
    expect(within(screen.getByRole('region', { name: 'Zustand' })).getByText('Fehler')).toBeInTheDocument()
  })

  it('shows other users a generic error without the details', async () => {
    const survival = mockServer(1)
    survival.status = { ...survival.status, state: 'error', last_error: 'image pull failed: manifest unknown' }
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Der Server meldet einen Fehler. Details sieht ein Admin.')
    expect(screen.queryByText('image pull failed: manifest unknown')).not.toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Zustand' })).getByText('Fehler')).toBeInTheDocument()
  })

  it('offers no restart while the agent is unreachable', async () => {
    const survival = mockServer(1)
    survival.status = { ...survival.status, state: 'unknown' }
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('admin') })
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
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    expect(await screen.findByText('Neustart nötig')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Jetzt neu starten' }))
    expect(await screen.findByText('Neustart für „survival“ angefordert.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('Neustart nötig')).not.toBeInTheDocument())
  })

  it('shows a restricted user only the overview', async () => {
    await renderWithProviders(<ServerDetailPage serverId={2} />, { me: loginAs('alice') })
    expect(await screen.findByRole('heading', { name: 'creative', level: 1 })).toBeInTheDocument()
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Übersicht'])
    expect(screen.queryByRole('group', { name: 'Steuerung für creative' })).not.toBeInTheDocument()
    expect(screen.getByText('Keine Berechtigung für Start/Stopp.')).toBeInTheDocument()
  })

  it('shows the console to a user with the console permission', async () => {
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.console'] }])
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    expect(await screen.findByRole('tab', { name: 'Konsole' })).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Spieler' })).toBeInTheDocument()
    expect(screen.queryByRole('tab', { name: 'Einstellungen' })).not.toBeInTheDocument()
  })

  it('explains a server the user may not see', async () => {
    await renderWithProviders(<ServerDetailPage serverId={3} />, { me: loginAs('alice') })
    expect(await screen.findByText('Du hast keine Berechtigung, diesen Server anzusehen.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Zur Übersicht' })).toHaveAttribute('href', '/')
  })

  it('stops showing a server that was deleted while the page was open', async () => {
    const { queryClient } = await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('admin') })
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
    db.servers = db.servers.filter((s) => s.id !== 1)
    await queryClient.refetchQueries({ queryKey: ['servers', 1] })
    expect(await screen.findByText('Diesen Server gibt es nicht (mehr).')).toBeInTheDocument()
    expect(screen.queryByRole('group', { name: 'Steuerung für survival' })).not.toBeInTheDocument()
  })

  it('stops showing a server after the view permission was revoked', async () => {
    const { queryClient } = await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
    db.grants.set(2, [])
    await queryClient.refetchQueries({ queryKey: ['servers', 1] })
    expect(await screen.findByText('Du hast keine Berechtigung, diesen Server anzusehen.')).toBeInTheDocument()
  })

  it('explains a server that does not exist', async () => {
    await renderWithProviders(<ServerDetailPage serverId={999} />, { me: loginAs('admin') })
    expect(await screen.findByText('Diesen Server gibt es nicht (mehr).')).toBeInTheDocument()
  })

  it('saves settings and points out the pending restart', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('tab', { name: 'Einstellungen' }))
    expect(screen.getByText('Der Server läuft. Änderungen werden erst nach einem Neustart aktiv.')).toBeInTheDocument()
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
    await user.click(screen.getByRole('tab', { name: 'Übersicht' }))
    expect(await screen.findByText('Neustart nötig')).toBeInTheDocument()
  })

  it('deletes the server after typing its name', async () => {
    const user = userEvent.setup()
    const { router } = await renderWithProviders(<ServerDetailPage serverId={1} />, { path: '/servers/1', me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('tab', { name: 'Löschen' }))
    expect(screen.getByText(/deleted\/survival-<Zeitpunkt>/)).toBeInTheDocument()
    const submit = screen.getByRole('button', { name: 'Server löschen' })
    expect(submit).toBeDisabled()
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
    expect(submit).toBeEnabled()
    await user.click(submit)
    expect(await screen.findByText('Server „survival“ gelöscht.')).toBeInTheDocument()
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(db.servers.some((s) => s.id === 1)).toBe(false)
  })

  it('lets admins delete a server that was never set up', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<ServerDetailPage serverId={3} />, { path: '/servers/3', me: loginAs('admin', { sudo: true }) })
    expect(
      await screen.findByText('Dieser Server stammt aus der Zeit vor der Instanzverwaltung und hat noch keine Minecraft-Instanz. Du kannst ihn nur löschen.'),
    ).toBeInTheDocument()
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
    expect(screen.getByText('Der Eintrag und alle Rechte darauf werden entfernt. Serverdaten gibt es keine.')).toBeInTheDocument()
    await user.type(screen.getByLabelText('Zum Bestätigen den Namen „lobby“ eintippen'), 'lobby')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    expect(await screen.findByText('Server „lobby“ gelöscht.')).toBeInTheDocument()
  })

  it('shows no error after the user cancelled the sudo dialog', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<ServerDetailPage serverId={3} />, { me: loginAs('admin') })
    await user.type(await screen.findByLabelText('Zum Bestätigen den Namen „lobby“ eintippen'), 'lobby')
    await user.click(screen.getByRole('button', { name: 'Server löschen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.click(within(sudo).getByRole('button', { name: 'Abbrechen' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Aktion bestätigen' })).not.toBeInTheDocument())
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect(screen.queryByText('Diese Aktion muss bestätigt werden.')).not.toBeInTheDocument()
    expect(db.servers.some((s) => s.id === 3)).toBe(true)
  })

  it('lets the tab bar scroll sideways instead of the page on narrow screens', async () => {
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('admin') })
    const tabs = await screen.findByRole('tablist')
    expect(within(tabs).getAllByRole('tab')).toHaveLength(6)
    expect(tabs).toHaveClass('max-w-full', 'justify-start', 'overflow-x-auto')
  })

  it('shows the files tab only with the files permission', async () => {
    await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('alice') })
    await screen.findByRole('heading', { name: 'survival', level: 1 })
    expect(screen.queryByRole('tab', { name: 'Dateien' })).not.toBeInTheDocument()
  })
})
