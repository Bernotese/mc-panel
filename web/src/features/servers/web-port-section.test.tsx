import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { db } from '@/mocks/db'
import { server as mockApi } from '@/mocks/node'
import { renderServerTab } from '@/test/render-section'
import { ServerSettingsTab } from './server-settings-tab'

const SETTINGS = '/servers/2/settings'

function settingsTab(serverId = 2, username = 'admin') {
  return renderServerTab((s) => <ServerSettingsTab server={s} instance={s.instance} />, { serverId, username, sudo: true, path: SETTINGS })
}

/** Records the bodies of PATCH /servers/2; returning nothing hands the request on to the mock handler. */
function capturePatches(): unknown[] {
  const bodies: unknown[] = []
  mockApi.use(
    http.patch('*/api/servers/2', async ({ request }) => {
      bodies.push(await request.clone().json())
    }),
  )
  return bodies
}

describe('WebPortSection', () => {
  it('is shown to admins, before the danger zone, with the default port 8100', async () => {
    await settingsTab()
    const section = await screen.findByRole('region', { name: 'Weboberfläche' })
    expect(within(section).getByRole('switch', { name: 'Web-Port veröffentlichen' })).not.toBeChecked()
    expect(within(section).getByLabelText('Port im Container')).toHaveValue(8100)
    expect(within(section).getByText('Aus')).toBeInTheDocument()
    expect(within(section).getByText(/accept-download: true/)).toBeInTheDocument()
    const danger = screen.getByRole('region', { name: 'Server löschen' })
    expect(section.compareDocumentPosition(danger) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('is never shown to non-admins', async () => {
    await settingsTab(2, 'alice')
    expect(await screen.findByRole('region', { name: 'Server löschen' })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Weboberfläche' })).not.toBeInTheDocument()
  })

  it('rejects 25565 and out-of-range ports on the client', async () => {
    const bodies = capturePatches()
    const { user } = await settingsTab()
    const section = await screen.findByRole('region', { name: 'Weboberfläche' })
    await user.click(within(section).getByRole('switch', { name: 'Web-Port veröffentlichen' }))
    const port = within(section).getByLabelText('Port im Container')
    await user.clear(port)
    await user.type(port, '25565')
    await user.click(within(section).getByRole('button', { name: 'Weboberfläche speichern' }))
    expect(within(section).getByText(/Die Ports 25565 \(Minecraft\) und 25575 \(RCON\)/)).toBeInTheDocument()
    expect(port).toHaveAttribute('aria-invalid', 'true')
    await user.clear(port)
    await user.type(port, '80')
    await user.click(within(section).getByRole('button', { name: 'Weboberfläche speichern' }))
    expect(within(section).getByText('Gib einen Port von 1024 bis 65535 ein.')).toBeInTheDocument()
    expect(bodies).toEqual([])
  })

  it('publishes {web_port: 8100} and shows the status and the URL', async () => {
    const bodies = capturePatches()
    const { user } = await settingsTab()
    const section = await screen.findByRole('region', { name: 'Weboberfläche' })
    await user.click(within(section).getByRole('switch', { name: 'Web-Port veröffentlichen' }))
    await user.click(within(section).getByRole('button', { name: 'Weboberfläche speichern' }))
    await waitFor(() => expect(bodies).toEqual([{ web_port: 8100 }]))
    // the section is re-initialised with the saved port
    const saved = await screen.findByRole('region', { name: 'Weboberfläche' })
    expect(await within(saved).findByText('Ausstehend')).toBeInTheDocument()
    expect(within(saved).getByRole('switch', { name: 'Web-Port veröffentlichen' })).toBeChecked()
    const link = within(saved).getByRole('link', { name: 'https://creative.mc.example.com' })
    expect(link).toHaveAttribute('href', 'https://creative.mc.example.com')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(db.servers.find((s) => s.id === 2)?.webPort).toBe(8100)
  })

  it('sends null when the switch is turned off', async () => {
    const creative = db.servers.find((s) => s.id === 2)
    if (creative) Object.assign(creative, { webPort: 8123, webState: 'error', webError: 'server network missing' })
    const bodies = capturePatches()
    const { user } = await settingsTab()
    const section = await screen.findByRole('region', { name: 'Weboberfläche' })
    expect(within(section).getByLabelText('Port im Container')).toHaveValue(8123)
    expect(within(section).getByText('Fehler')).toBeInTheDocument()
    expect(within(section).getByText('server network missing')).toBeInTheDocument()
    await user.click(within(section).getByRole('switch', { name: 'Web-Port veröffentlichen' }))
    await user.click(within(section).getByRole('button', { name: 'Weboberfläche speichern' }))
    await waitFor(() => expect(bodies).toEqual([{ web_port: null }]))
    const saved = await screen.findByRole('region', { name: 'Weboberfläche' })
    expect(await within(saved).findByText('Aus')).toBeInTheDocument()
    expect(within(saved).queryByRole('link', { name: /^https:/ })).not.toBeInTheDocument()
  })

  it('explains a missing base domain and disables the switch', async () => {
    db.serverSettings.base_domain = null
    await settingsTab()
    const section = await screen.findByRole('region', { name: 'Weboberfläche' })
    expect(within(section).getByText(/braucht eine Basisdomain/)).toBeInTheDocument()
    expect(within(section).getByRole('link', { name: 'Zu den Einstellungen' })).toHaveAttribute('href', '/settings')
    expect(within(section).getByRole('switch', { name: 'Web-Port veröffentlichen' })).toBeDisabled()
  })

  it('shows the same explanation when the panel answers 409 base domain required', async () => {
    mockApi.use(http.patch('*/api/servers/2', () => HttpResponse.json({ error: 'conflict', message: 'base domain required' }, { status: 409 })))
    const { user } = await settingsTab()
    const section = await screen.findByRole('region', { name: 'Weboberfläche' })
    await user.click(within(section).getByRole('switch', { name: 'Web-Port veröffentlichen' }))
    await user.click(within(section).getByRole('button', { name: 'Weboberfläche speichern' }))
    expect(await within(section).findByText(/braucht eine Basisdomain/)).toBeInTheDocument()
    expect(within(section).getByRole('switch', { name: 'Web-Port veröffentlichen' })).toBeDisabled()
  })
})
