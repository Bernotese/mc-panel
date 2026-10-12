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
