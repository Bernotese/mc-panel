import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { db } from '@/mocks/db'
import { server as mockApi } from '@/mocks/node'
import { renderServerTab } from '@/test/render-section'
import { ServerSettingsTab } from './server-settings-tab'

const SECTION = 'Weboberflächen'
const ROOT_SWITCH = 'Unter der Hauptadresse veröffentlichen'
const ROOT_PORT = 'Port im Container für die Hauptadresse'
const ADD = 'Port hinzufügen'
const SAVE = 'Weboberflächen speichern'

function settingsTab(serverId = 2, username = 'admin') {
  return renderServerTab((s) => <ServerSettingsTab server={s} instance={s.instance} />, { serverId, username, sudo: true, path: `/servers/${serverId}/settings` })
}

/** Records the bodies of PATCH /servers/<id>; returning nothing hands the request on to the mock handler. */
function capturePatches(serverId = 2): unknown[] {
  const bodies: unknown[] = []
  mockApi.use(
    http.patch(`*/api/servers/${serverId}`, async ({ request }) => {
      bodies.push(await request.clone().json())
    }),
  )
  return bodies
}

function setRoutes(serverId: number, routes: { path: string; port: number }[], extra: Record<string, unknown> = {}) {
  const s = db.servers.find((x) => x.id === serverId)
  if (s) Object.assign(s, { webRoutes: routes, webState: 'active', ...extra })
}

async function section() {
  return screen.findByRole('region', { name: SECTION })
}

describe('WebRoutesSection', () => {
  it('is shown to admins, before the danger zone, with the root off and 8100 prefilled', async () => {
    await settingsTab()
    const s = await section()
    expect(within(s).getByRole('switch', { name: ROOT_SWITCH })).not.toBeChecked()
    expect(within(s).getByRole('spinbutton', { name: ROOT_PORT })).toHaveValue(8100)
    expect(within(s).getByRole('spinbutton', { name: ROOT_PORT })).toBeDisabled()
    expect(within(s).getByText('Aus')).toBeInTheDocument()
    expect(within(s).getByText(/accept-download: true/)).toBeInTheDocument()
    expect(within(s).getByText(/Unter einem Pfad wird das Präfix entfernt, bevor die Anfrage den Mod erreicht/)).toBeInTheDocument()
    expect(within(s).getByText(/Apps, die ihre Dateien absolut verlinken, funktionieren nur unter der Hauptadresse/)).toBeInTheDocument()
    expect(within(s).getByRole('button', { name: SAVE })).toBeDisabled()
    const danger = screen.getByRole('region', { name: 'Server löschen' })
    expect(s.compareDocumentPosition(danger) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('is never shown to non-admins', async () => {
    await settingsTab(2, 'alice')
    expect(await screen.findByRole('region', { name: 'Server löschen' })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: SECTION })).not.toBeInTheDocument()
  })

  it('publishes the root with the prefilled 8100 and shows the status and the URL', async () => {
    const bodies = capturePatches()
    const { user } = await settingsTab()
    const s = await section()
    await user.click(within(s).getByRole('switch', { name: ROOT_SWITCH }))
    expect(within(s).getByRole('spinbutton', { name: ROOT_PORT })).toHaveValue(8100)
    expect(within(s).getByText('https://creative.mc.example.com/')).toBeInTheDocument()
    await user.click(within(s).getByRole('button', { name: SAVE }))
    await waitFor(() => expect(bodies).toEqual([{ web_routes: [{ path: '', port: 8100 }] }]))
    // the section is re-initialised with the saved routes
    const saved = await section()
    expect(await within(saved).findByText('Ausstehend')).toBeInTheDocument()
    expect(within(saved).getByRole('switch', { name: ROOT_SWITCH })).toBeChecked()
    const link = within(saved).getByRole('link', { name: 'https://creative.mc.example.com/' })
    expect(link).toHaveAttribute('href', 'https://creative.mc.example.com/')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(db.servers.find((x) => x.id === 2)?.webRoutes).toEqual([{ path: '', port: 8100 }])
  })

  it('prefills 8100 again when the root is switched on with an empty port', async () => {
    const { user } = await settingsTab()
    const s = await section()
    const root = within(s).getByRole('switch', { name: ROOT_SWITCH })
    await user.click(root)
    await user.clear(within(s).getByRole('spinbutton', { name: ROOT_PORT }))
    await user.click(root)
    await user.click(root)
    expect(within(s).getByRole('spinbutton', { name: ROOT_PORT })).toHaveValue(8100)
  })

  it('rejects 25565 and out-of-range root ports on the client', async () => {
    const bodies = capturePatches()
    const { user } = await settingsTab()
    const s = await section()
    await user.click(within(s).getByRole('switch', { name: ROOT_SWITCH }))
    const port = within(s).getByRole('spinbutton', { name: ROOT_PORT })
    await user.clear(port)
    await user.type(port, '25565')
    expect(within(s).getByText(/Die Ports 25565 \(Minecraft\) und 25575 \(RCON\)/)).toBeInTheDocument()
    expect(port).toHaveAttribute('aria-invalid', 'true')
    expect(within(s).getByRole('button', { name: SAVE })).toBeDisabled()
    await user.clear(port)
    await user.type(port, '80')
    expect(within(s).getByText('Gib einen Port von 1024 bis 65535 ein.')).toBeInTheDocument()
    expect(within(s).getByRole('button', { name: SAVE })).toBeDisabled()
    expect(bodies).toEqual([])
  })

  it('adds a path row and sends both routes, root first, and shows both URLs', async () => {
    const bodies = capturePatches()
    const { user } = await settingsTab()
    const s = await section()
    await user.click(within(s).getByRole('switch', { name: ROOT_SWITCH }))
    await user.click(within(s).getByRole('button', { name: ADD }))
    await user.type(within(s).getByRole('textbox', { name: 'Pfad 1' }), 'afk-smoke')
    await user.type(within(s).getByRole('spinbutton', { name: 'Port im Container 1' }), '8200')
    expect(within(s).getByText('https://creative.mc.example.com/afk-smoke/')).toBeInTheDocument()
    await user.click(within(s).getByRole('button', { name: SAVE }))
    await waitFor(() =>
      expect(bodies).toEqual([
        {
          web_routes: [
            { path: '', port: 8100 },
            { path: 'afk-smoke', port: 8200 },
          ],
        },
      ]),
    )
    const saved = await section()
    expect(await within(saved).findByRole('link', { name: 'https://creative.mc.example.com/' })).toBeInTheDocument()
    expect(within(saved).getByRole('link', { name: 'https://creative.mc.example.com/afk-smoke/' })).toHaveAttribute('href', 'https://creative.mc.example.com/afk-smoke/')
    expect(within(saved).getByRole('textbox', { name: 'Pfad 1' })).toHaveValue('afk-smoke')
  })

  it('validates paths and ports of the rows live and disables Save', async () => {
    const { user } = await settingsTab()
    const s = await section()
    await user.click(within(s).getByRole('switch', { name: ROOT_SWITCH }))
    await user.click(within(s).getByRole('button', { name: ADD }))
    // an untouched empty row shows no error yet, but cannot be saved
    expect(within(s).queryByText(/Kleinbuchstaben/)).not.toBeInTheDocument()
    expect(within(s).getByRole('button', { name: SAVE })).toBeDisabled()
    const path = within(s).getByRole('textbox', { name: 'Pfad 1' })
    await user.type(path, 'Afk')
    expect(within(s).getByText('1–32 Kleinbuchstaben, Ziffern oder Bindestriche, am Anfang und Ende kein Bindestrich.')).toBeInTheDocument()
    expect(path).toHaveAttribute('aria-invalid', 'true')
    expect(within(s).queryByText(/creative\.mc\.example\.com\/Afk/)).not.toBeInTheDocument()
    expect(within(s).getByRole('button', { name: SAVE })).toBeDisabled()
    await user.clear(path)
    await user.type(path, 'map')
    await user.type(within(s).getByRole('spinbutton', { name: 'Port im Container 1' }), '8100')
    expect(within(s).getAllByText('Dieser Port ist schon vergeben.')).toHaveLength(2)
    expect(within(s).getByRole('button', { name: SAVE })).toBeDisabled()
    await user.click(within(s).getByRole('button', { name: ADD }))
    await user.type(within(s).getByRole('textbox', { name: 'Pfad 2' }), 'map')
    expect(within(s).getAllByText('Diesen Pfad gibt es schon.')).toHaveLength(2)
    await user.click(within(s).getByRole('button', { name: 'Pfad 2 entfernen' }))
    await user.clear(within(s).getByRole('spinbutton', { name: 'Port im Container 1' }))
    await user.type(within(s).getByRole('spinbutton', { name: 'Port im Container 1' }), '8200')
    expect(within(s).queryByText(/schon/)).not.toBeInTheDocument()
    expect(within(s).getByRole('button', { name: SAVE })).toBeEnabled()
  })

  it('counts the root against the limit of 8 only while it is switched on', async () => {
    setRoutes(2, [{ path: '', port: 8100 }, ...Array.from({ length: 7 }, (_, i) => ({ path: `p${i}`, port: 8201 + i }))])
    const { user } = await settingsTab()
    const s = await section()
    expect(within(s).getByRole('button', { name: ADD })).toBeDisabled()
    expect(within(s).getByText('Höchstens 8 Weboberflächen pro Server.')).toBeInTheDocument()
    await user.click(within(s).getByRole('switch', { name: ROOT_SWITCH }))
    expect(within(s).getByRole('button', { name: ADD })).toBeEnabled()
    await user.click(within(s).getByRole('button', { name: ADD }))
    expect(within(s).getByRole('button', { name: ADD })).toBeDisabled()
  })

  it('lets the admin switch the root on for stored routes without one', async () => {
    setRoutes(2, [{ path: 'afk-smoke', port: 8200 }])
    const bodies = capturePatches()
    const { user } = await settingsTab()
    const s = await section()
    expect(within(s).getByRole('switch', { name: ROOT_SWITCH })).not.toBeChecked()
    expect(within(s).getByRole('textbox', { name: 'Pfad 1' })).toHaveValue('afk-smoke')
    expect(within(s).getByRole('link', { name: 'https://creative.mc.example.com/afk-smoke/' })).toBeInTheDocument()
    expect(within(s).getByText('Aktiv')).toBeInTheDocument()
    await user.click(within(s).getByRole('switch', { name: ROOT_SWITCH }))
    await user.click(within(s).getByRole('button', { name: SAVE }))
    await waitFor(() =>
      expect(bodies).toEqual([
        {
          web_routes: [
            { path: '', port: 8100 },
            { path: 'afk-smoke', port: 8200 },
          ],
        },
      ]),
    )
  })

  it('sends [] when the root is switched off and the path rows are removed', async () => {
    setRoutes(
      2,
      [
        { path: '', port: 8123 },
        { path: 'afk-smoke', port: 8200 },
      ],
      { webState: 'error', webError: 'server network missing' },
    )
    const bodies = capturePatches()
    const { user } = await settingsTab()
    const s = await section()
    expect(within(s).getByRole('spinbutton', { name: ROOT_PORT })).toHaveValue(8123)
    expect(within(s).getByText('Fehler')).toBeInTheDocument()
    expect(within(s).getByText('server network missing')).toBeInTheDocument()
    await user.click(within(s).getByRole('button', { name: 'Pfad 1 entfernen' }))
    await user.click(within(s).getByRole('switch', { name: ROOT_SWITCH }))
    await user.click(within(s).getByRole('button', { name: SAVE }))
    await waitFor(() => expect(bodies).toEqual([{ web_routes: [] }]))
    const saved = await section()
    expect(await within(saved).findByText('Aus')).toBeInTheDocument()
    expect(within(saved).queryByRole('link', { name: /^https:/ })).not.toBeInTheDocument()
    expect(within(saved).queryByRole('textbox', { name: 'Pfad 1' })).not.toBeInTheDocument()
  })

  it('explains a missing base domain and disables the controls', async () => {
    db.serverSettings.base_domain = null
    await settingsTab()
    const s = await section()
    expect(within(s).getByText(/brauchen eine Basisdomain/)).toBeInTheDocument()
    expect(within(s).getByRole('link', { name: 'Zu den Einstellungen' })).toHaveAttribute('href', '/settings')
    expect(within(s).getByRole('switch', { name: ROOT_SWITCH })).toBeDisabled()
    expect(within(s).getByRole('button', { name: ADD })).toBeDisabled()
  })

  it('keeps showing the stored routes while the base domain is missing', async () => {
    db.serverSettings.base_domain = null
    await settingsTab(1)
    const s = await section()
    expect(within(s).getByRole('switch', { name: ROOT_SWITCH })).toBeChecked()
    expect(within(s).getByRole('spinbutton', { name: ROOT_PORT })).toHaveValue(8100)
    expect(within(s).getByText('Aus')).toBeInTheDocument()
    expect(within(s).getByText(/brauchen eine Basisdomain/)).toBeInTheDocument()
  })

  it('shows the same explanation when the panel answers 409 base domain required', async () => {
    mockApi.use(http.patch('*/api/servers/2', () => HttpResponse.json({ error: 'conflict', message: 'base domain required' }, { status: 409 })))
    const { user } = await settingsTab()
    const s = await section()
    await user.click(within(s).getByRole('switch', { name: ROOT_SWITCH }))
    await user.click(within(s).getByRole('button', { name: SAVE }))
    expect(await within(s).findByText(/brauchen eine Basisdomain/)).toBeInTheDocument()
    expect(within(s).getByRole('switch', { name: ROOT_SWITCH })).toBeDisabled()
  })
})
