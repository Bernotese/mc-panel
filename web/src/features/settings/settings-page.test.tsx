import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { ServerSettings } from '@/api/types'
import { db, loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { SettingsPage } from './settings-page'

describe('SettingsPage', () => {
  it('saves a new base domain after sudo and shows the DNS record to create', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin') })
    expect(await screen.findByRole('heading', { name: 'Minecraft-Server', level: 2 })).toBeInTheDocument()
    // The h2 renders right away; the cards follow once the settings have loaded.
    const input = await screen.findByLabelText('Basisdomain')
    expect(input).toHaveValue('mc.example.com')
    expect(screen.getByText('*.mc.example.com')).toBeInTheDocument()
    const save = screen.getByRole('button', { name: 'Basisdomain speichern' })
    expect(save).toBeDisabled()

    await user.clear(input)
    await user.type(input, 'Play.Example.org')
    expect(screen.getByText('*.play.example.org')).toBeInTheDocument()
    await user.click(save)
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))

    expect(await screen.findByText('Basisdomain gespeichert.')).toBeInTheDocument()
    expect(db.serverSettings.base_domain).toBe('play.example.org')
    expect(screen.getByLabelText('Basisdomain')).toHaveValue('play.example.org')
  })

  it('rejects an invalid base domain before sending', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin', { sudo: true }) })
    const input = await screen.findByLabelText('Basisdomain')
    await user.clear(input)
    await user.type(input, 'localhost')
    expect(
      screen.getByText('Ungültige Domain. Beispiel: mc.example.com (mindestens zwei Teile, nur Kleinbuchstaben, Ziffern und Bindestrich).'),
    ).toBeInTheDocument()
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('button', { name: 'Basisdomain speichern' })).toBeDisabled()
    expect(db.serverSettings.base_domain).toBe('mc.example.com')
  })

  it('stores and removes the CurseForge key without ever showing it', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin', { sudo: true }) })
    expect(await screen.findByText('Nicht hinterlegt')).toBeInTheDocument()
    const key = screen.getByLabelText('API-Key')
    expect(key).toHaveAttribute('type', 'password')
    // A password manager must not offer the admin's login password here (it would end up in a container env).
    expect(key).toHaveAttribute('autocomplete', 'new-password')
    await user.type(key, '$2a$10$abcdef')
    await user.click(screen.getByRole('button', { name: 'Key speichern' }))

    expect(await screen.findByText('CurseForge-Key gespeichert.')).toBeInTheDocument()
    expect(screen.getByText('Hinterlegt')).toBeInTheDocument()
    expect(db.serverSettings.curseforge_api_key_set).toBe(true)
    expect(screen.getByLabelText('Neuer API-Key')).toHaveValue('')
    expect(document.body).not.toHaveTextContent('$2a$10$abcdef')

    await user.click(screen.getByRole('button', { name: 'Key entfernen' }))
    const confirm = screen.getByRole('dialog', { name: 'CurseForge-Key entfernen?' })
    await user.click(within(confirm).getByRole('button', { name: 'Key entfernen' }))
    expect(await screen.findByText('CurseForge-Key entfernt.')).toBeInTheDocument()
    expect(screen.getByText('Nicht hinterlegt')).toBeInTheDocument()
    expect(db.serverSettings.curseforge_api_key_set).toBe(false)
  })

  it('shows the docker status and unknown containers', async () => {
    server.use(
      http.get('*/api/servers/settings', () =>
        HttpResponse.json<ServerSettings>({
          base_domain: 'mc.example.com',
          curseforge_api_key_set: false,
          backup_timezone: 'Europe/Berlin',
          docker: 'unavailable',
          unknown_containers: [{ id: 7, name: 'oldworld' }],
        }),
      ),
    )
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin') })
    const status = await screen.findByRole('region', { name: 'Status' })
    expect(within(status).getByText('Nicht erreichbar')).toBeInTheDocument()
    expect(
      within(status).getByText('Der Agent erreicht Docker nicht. Prüfe auf dem Host, ob Docker läuft (systemctl status docker).'),
    ).toBeInTheDocument()
    expect(within(status).getByText('oldworld (ID 7)')).toBeInTheDocument()
  })

  it('keeps h1 → h2 section → h3 card and limits the width', async () => {
    const { container } = await renderWithProviders(<SettingsPage />, { me: loginAs('admin') })
    expect(screen.getByRole('heading', { level: 1, name: 'Einstellungen' })).toBeInTheDocument()
    const status = await screen.findByRole('region', { name: 'Status' })
    expect(within(status).getByRole('heading', { level: 3, name: 'Status' })).toBeInTheDocument()
    expect(screen.getAllByRole('heading', { level: 2 }).length).toBeGreaterThanOrEqual(2)
    expect(container.querySelector('.max-w-3xl')).not.toBeNull()
  })

  it('reports an empty list of unknown containers', async () => {
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin') })
    const status = await screen.findByRole('region', { name: 'Status' })
    expect(within(status).getAllByText('Erreichbar')).toHaveLength(2)
    expect(within(status).getByText('Keine.')).toBeInTheDocument()
  })

  it('saves the time zone of the backup schedules', async () => {
    const user = userEvent.setup()
    let sent: unknown = null
    server.use(
      http.put('*/api/servers/settings', async ({ request }) => {
        sent = await request.json()
        return HttpResponse.json<ServerSettings>({ ...db.serverSettings, unknown_containers: [], backup_timezone: 'UTC' })
      }),
    )
    await renderWithProviders(<SettingsPage />, { me: loginAs('admin', { sudo: true }) })
    const card = await screen.findByRole('region', { name: 'Zeitzone für Backups' })
    const select = within(card).getByRole('combobox', { name: 'Zeitzone' })
    expect(select).toHaveValue('Europe/Berlin')
    const save = within(card).getByRole('button', { name: 'Zeitzone speichern' })
    expect(save).toBeDisabled()
    await user.selectOptions(select, 'UTC')
    await user.click(save)
    await waitFor(() => expect(sent).toEqual({ backup_timezone: 'UTC' }))
    expect(await screen.findByText('Zeitzone gespeichert.')).toBeInTheDocument()
  })
})
