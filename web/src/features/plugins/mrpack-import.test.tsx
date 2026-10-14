import { act, screen, waitFor, within } from '@testing-library/react'
import { strToU8, zipSync } from 'fflate'
import { http } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { db } from '@/mocks/db'
import { MRPACK_MOCK_HASHES, PLUGIN_JOB_MS } from '@/mocks/handlers/plugins'
import { server } from '@/mocks/node'
import { renderServerTab } from '@/test/render-section'
import { JOB_POLL_MS } from './api'
import { pluginKindOf } from './model'
import { PluginsTab } from './plugins-tab'
import { openPlugins } from './test-utils'

const H = MRPACK_MOCK_HASHES

function index(over: Record<string, unknown> = {}) {
  return {
    formatVersion: 1,
    game: 'minecraft',
    versionId: '2.0.0',
    name: 'Test Pack',
    files: [
      { path: 'mods/lithium-fabric-0.15.0.jar', hashes: { sha512: H.lithium, sha1: 'a'.repeat(40) }, downloads: ['https://cdn.modrinth.com/x.jar'], fileSize: 123 },
      { path: 'mods/fabric-api-0.111.0-beta.jar', hashes: { sha512: H.fabricApiBeta }, downloads: ['https://github.com/x.jar'], fileSize: 456 },
      { path: 'mods/servercore-fabric-1.5.8.jar', hashes: { sha512: H.servercore }, env: { client: 'optional', server: 'optional' } },
      { path: 'mods/sodium-extra-0.6.12.jar', hashes: { sha512: H.sodiumExtra }, env: { client: 'required', server: 'unsupported' } },
      { path: 'mods/custom-tweaks.jar', hashes: { sha512: 'c'.repeat(128) } },
      { path: 'resourcepacks/faithful.zip', hashes: { sha512: 'd'.repeat(128) } },
    ],
    dependencies: { minecraft: '1.21.11', 'fabric-loader': '0.19.5' },
    ...over,
  }
}

function mrpack(idx: unknown, name = 'test.mrpack'): File {
  const bytes = zipSync({ 'modrinth.index.json': strToU8(JSON.stringify(idx)), 'overrides/config/x.json': strToU8('{}') })
  return new File([bytes as Uint8Array<ArrayBuffer>], name)
}

/** The mods area of the fabric server "modded", once its inventory (and with it the import button) is there. */
async function openMods(sudo?: boolean) {
  const opened = await openModsArea(sudo)
  await within(opened.region).findByRole('button', { name: 'Modpack importieren (.mrpack)' })
  return opened
}

async function openModsArea(sudo?: boolean) {
  if (sudo === undefined) return openPlugins(4, 'admin', 'Mods')
  const { user, queryClient } = await renderServerTab((s) => <PluginsTab server={s} kind={(s.instance && pluginKindOf(s.instance.type)) ?? 'mod'} />, {
    serverId: 4,
    sudo,
    path: '/servers/4/plugins',
  })
  const region = await screen.findByRole('region', { name: 'Installierte Mods' })
  return { user, region, queryClient }
}

async function choose(user: Awaited<ReturnType<typeof openMods>>['user'], file: File) {
  await user.upload(screen.getByLabelText('Modpack-Datei (.mrpack)'), file)
  return screen.findByRole('dialog', { name: 'Modpack importieren' })
}

const group = (dialog: HTMLElement, name: RegExp) => within(dialog).getByRole('region', { name })

afterEach(() => {
  vi.useRealTimers()
})

describe('mrpack import', () => {
  it('offers the import for mods', async () => {
    const { region } = await openMods()
    expect(within(region).getByRole('button', { name: 'Modpack importieren (.mrpack)' })).toBeInTheDocument()
  })

  it('offers no import for plugins', async () => {
    const { region } = await openPlugins(1)
    await within(region).findByText('LuckPerms')
    expect(within(region).queryByRole('button', { name: 'Modpack importieren (.mrpack)' })).not.toBeInTheDocument()
  })

  it('shows the diff, sends the index without download links and starts the import', async () => {
    let sent: Record<string, unknown> | null = null
    server.use(
      http.post('*/api/servers/:id/plugins/mrpack/import', async ({ request }) => {
        sent = (await request.clone().json()) as Record<string, unknown>
        return undefined // fall through to the mock
      }),
    )
    const { user } = await openMods()
    const dialog = await choose(user, mrpack(index()))
    expect(await within(dialog).findByText(/Test Pack \(2\.0\.0\)/)).toBeInTheDocument()

    const added = group(dialog, /^Neu/)
    expect(within(added).getByText('Lithium')).toBeInTheDocument()
    expect(within(added).getByText('ServerCore')).toBeInTheDocument()
    const updated = group(dialog, /^Aktualisiert/)
    expect(within(updated).getByText('Fabric API')).toBeInTheDocument()
    expect(within(updated).getByText('fabric-api-0.110.0.jar')).toBeInTheDocument()
    expect(within(updated).getByText('fabric-api-0.111.0-beta.jar')).toBeInTheDocument()
    const skipped = group(dialog, /^Übersprungen/)
    expect(within(skipped).getByText('Nur Client – läuft nicht auf dem Server.')).toBeInTheDocument()
    expect(within(skipped).getByText('custom-tweaks.jar')).toBeInTheDocument()
    expect(within(skipped).getByText(/Nicht auf Modrinth – bitte manuell/)).toBeInTheDocument()
    expect(within(dialog).getByText('Dateien außerhalb von mods/ werden ignoriert: 1')).toBeInTheDocument()
    expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument()

    const optional = within(dialog).getByRole('checkbox', { name: 'ServerCore importieren' })
    expect(optional).toBeChecked()
    await user.click(optional)
    await user.click(within(dialog).getByRole('button', { name: 'Importieren (2)' }))

    expect(await screen.findByText('Modpack-Import läuft …')).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(sent).not.toBeNull()
    const body = sent as unknown as { index: { files: Record<string, unknown>[] }; exclude: string[]; accept_mismatch: boolean }
    expect(body.exclude).toEqual([H.servercore])
    expect(body.accept_mismatch).toBe(false)
    expect(body.index.files.every((f) => !('downloads' in f) && !('fileSize' in f))).toBe(true)
    expect(body.index.files[0]).toEqual({ path: 'mods/lithium-fabric-0.15.0.jar', hashes: { sha512: H.lithium, sha1: 'a'.repeat(40) } })
  })

  it('needs "Trotzdem importieren" for another Minecraft version and lets admins switch the server', async () => {
    let patched: unknown = null
    server.use(
      http.patch('*/api/servers/:id', async ({ request }) => {
        patched = await request.clone().json()
        return undefined
      }),
    )
    const { user } = await openMods(true)
    const dialog = await choose(user, mrpack(index({ dependencies: { minecraft: '26.3', 'fabric-loader': '0.19.5' } })))
    const alert = await within(dialog).findByRole('alert')
    expect(alert).toHaveTextContent('Pack: Minecraft 26.3 · Fabric Loader 0.19.5')
    expect(alert).toHaveTextContent('Server: Minecraft 1.21.11 · Fabric Loader neueste')
    const submit = within(dialog).getByRole('button', { name: 'Importieren (3)' })
    expect(submit).toBeDisabled()
    await user.click(within(alert).getByRole('checkbox', { name: /Trotzdem importieren/ }))
    expect(submit).toBeEnabled()

    await user.click(within(alert).getByRole('button', { name: 'Server-Version auf Minecraft 26.3 umstellen' }))
    await waitFor(() => expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument())
    expect(patched).toEqual({ version: '26.3', java: 'java25' })
    expect(db.servers.find((s) => s.id === 4)?.instance?.version).toBe('26.3')
    expect(within(dialog).getByRole('button', { name: 'Importieren (3)' })).toBeEnabled()
  })

  it('shows no version switch to users who are not admins', async () => {
    const { user, queryClient } = await openMods()
    queryClient.setQueryData(['me'], (me: Record<string, unknown> | undefined) => (me ? { ...me, role: 'user' } : me))
    const dialog = await choose(user, mrpack(index({ dependencies: { minecraft: '26.3', 'fabric-loader': '0.19.5' } })))
    await within(dialog).findByRole('alert')
    expect(within(dialog).queryByRole('button', { name: /Server-Version/ })).not.toBeInTheDocument()
  })

  it('explains files it cannot read and packs for another loader', async () => {
    const { user } = await openMods()
    let dialog = await choose(user, new File(['just text, no zip archive here'], 'broken.mrpack'))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Das ist keine gültige .mrpack-Datei')
    await user.click(within(dialog).getByRole('button', { name: 'Abbrechen' }))

    dialog = await choose(user, mrpack(index({ dependencies: { minecraft: '1.21.11', 'quilt-loader': '0.27.0' } })))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Das Modpack ist für einen anderen Mod-Loader als dieser Server.')
    expect(within(dialog).getByRole('button', { name: 'Importieren (0)' })).toBeDisabled()
  })

  it('reports a failed step when the import ends', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const { user } = await openMods()
    const files = [
      { path: 'mods/lithium-fabric-0.15.1-beta.jar', hashes: { sha512: H.lithiumBroken } },
      { path: 'mods/servercore-fabric-1.5.8.jar', hashes: { sha512: H.servercore } },
    ]
    const dialog = await choose(user, mrpack(index({ files })))
    await user.click(await within(dialog).findByRole('button', { name: 'Importieren (2)' }))
    expect(await screen.findByText('Modpack-Import läuft …')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(PLUGIN_JOB_MS + JOB_POLL_MS))
    expect(await screen.findByText('Modpack-Import mit Fehlern beendet')).toBeInTheDocument()
    expect(screen.getByText('Fehler: Download fehlgeschlagen.')).toBeInTheDocument()
    const installed = screen.getByRole('region', { name: 'Installierte Mods' })
    expect(await within(installed).findByText('ServerCore')).toBeInTheDocument()
    expect(within(installed).queryByText('Lithium')).not.toBeInTheDocument()
  })
})
