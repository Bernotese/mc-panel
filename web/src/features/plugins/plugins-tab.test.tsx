import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { db, enableTotp } from '@/mocks/db'
import { server } from '@/mocks/node'
import { setViewport } from '@/test/media'
import { searchKey } from './api'
import { hangarJar, hit, inventory, jar, mockInventory, modrinthJar, openPlugins, planEntry, version } from './test-utils'

const OLDTOOL_META = { kind: 'plugin' as const, id: '', name: 'OldTool', version: '1.0' }

describe('PluginsTab – installed', () => {
  it('names jars by project title, jar metadata or file name', async () => {
    mockInventory(
      inventory({
        jars: [
          modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71'),
          hangarJar('ViaVersion-5.6.0-SNAPSHOT.jar', 'ViaVersion', '5.6.0-SNAPSHOT', 'beta'),
          modrinthJar('Disabled.jar.disabled', 'Disabled', '2.0', { enabled: false }),
          jar('OldTool.jar', { meta: OLDTOOL_META }),
          jar('mystery.jar'),
        ],
      }),
    )
    const { region } = await openPlugins()
    expect(await within(region).findByText('LuckPerms')).toBeInTheDocument()
    const row = (name: RegExp) => within(region).getByRole('row', { name })
    expect(within(row(/LuckPerms/)).getByText('5.5.71')).toBeInTheDocument()
    expect(within(row(/LuckPerms/)).getByText('Modrinth')).toBeInTheDocument()
    expect(within(row(/LuckPerms/)).getByText('LuckPerms-Bukkit-5.5.71.jar')).toBeInTheDocument()
    expect(within(row(/ViaVersion/)).getByText('Hangar')).toBeInTheDocument()
    expect(within(row(/ViaVersion/)).getByText('Beta')).toBeInTheDocument()
    expect(within(row(/OldTool/)).getByText('1.0')).toBeInTheDocument()
    expect(within(row(/OldTool/)).getByText('Manuell')).toBeInTheDocument()
    expect(within(row(/mystery/)).getByText('mystery.jar')).toBeInTheDocument()
    expect(within(row(/mystery/)).getByText('—')).toBeInTheDocument()
    expect(within(region).getAllByText('Nicht zugeordnet – Updates nicht möglich')).toHaveLength(2)
    expect(within(region).getByRole('switch', { name: 'LuckPerms aktiv' })).toBeChecked()
    expect(within(region).getByRole('switch', { name: 'Disabled aktiv' })).not.toBeChecked()
  })

  it('switches a jar off and reloads the list', async () => {
    const { user, region } = await openPlugins() // mock inventory V5
    const toggle = await within(region).findByRole('switch', { name: 'OldTool aktiv' })
    expect(toggle).toBeChecked()
    await user.click(toggle)
    expect(await screen.findByText('„OldTool“ deaktiviert.')).toBeInTheDocument()
    await waitFor(() => expect(within(region).getByRole('switch', { name: 'OldTool aktiv' })).not.toBeChecked())
  })

  it('reports a name clash when switching', async () => {
    mockInventory(inventory({ jars: [jar('X.jar.disabled', { meta: { kind: 'plugin', id: '', name: 'X', version: '1' } })] }))
    server.use(http.post('*/api/servers/:id/plugins/toggle', () => HttpResponse.json({ error: 'file_exists', message: 'X.jar exists' }, { status: 409 })))
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('switch', { name: 'X aktiv' }))
    expect(await screen.findByText('Dort existiert bereits eine Datei oder ein Ordner mit diesem Namen.')).toBeInTheDocument()
  })

  it('removes a jar after confirmation', async () => {
    const { user, region } = await openPlugins() // mock inventory V5
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für OldTool' }))
    await user.click(screen.getByRole('menuitem', { name: 'Entfernen' }))
    const dialog = await screen.findByRole('dialog', { name: '„OldTool“ entfernen?' })
    expect(within(dialog).getByText('OldTool.jar wird aus plugins/ gelöscht. Konfigurationsdateien bleiben erhalten.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Entfernen' }))
    expect(await screen.findByText('„OldTool“ entfernt.')).toBeInTheDocument()
    await waitFor(() => expect(within(region).queryByRole('switch', { name: 'OldTool aktiv' })).not.toBeInTheDocument())
  })

  it('shows format characters in jar names, labels and the removal dialog (SEC-38)', async () => {
    mockInventory(inventory({ jars: [jar('evil\u202Erab.jar')] }))
    const { user, region } = await openPlugins()
    expect(await within(region).findByText('evil⟨U+202E⟩rab.jar')).toBeInTheDocument()
    expect(within(region).getByRole('switch', { name: 'evil⟨U+202E⟩rab.jar aktiv' })).toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Aktionen für evil⟨U+202E⟩rab.jar' }))
    await user.click(screen.getByRole('menuitem', { name: 'Entfernen' }))
    const dialog = await screen.findByRole('dialog', { name: '„evil⟨U+202E⟩rab.jar“ entfernen?' })
    expect(within(dialog).getByText(/^evil⟨U\+202E⟩rab\.jar wird aus plugins\/ gelöscht/)).toBeInTheDocument()
  })

  it('opens project pages in a new window without referrer', async () => {
    mockInventory(inventory({ jars: [modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71')] }))
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für LuckPerms' }))
    const link = screen.getByRole('menuitem', { name: 'Projektseite öffnen' })
    expect(link).toHaveAttribute('href', 'https://modrinth.com/plugin/luckperms')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('links no project page for unmatched jars or non-http URLs', async () => {
    mockInventory(
      inventory({
        jars: [
          jar('OldTool.jar', { meta: OLDTOOL_META }),
          modrinthJar('evil.jar', 'Evil', '1', {
            project: { id: 'e', slug: 'evil', title: 'Evil', page_url: 'javascript:alert(1)', icon_url: 'https://evil.example/x.png' },
          }),
        ],
      }),
    )
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für OldTool' }))
    expect(screen.queryByRole('menuitem', { name: 'Projektseite öffnen' })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')
    await user.click(within(region).getByRole('button', { name: 'Aktionen für Evil' }))
    expect(screen.queryByRole('menuitem', { name: 'Projektseite öffnen' })).not.toBeInTheDocument()
    expect(region.querySelector('img')).toBeNull() // the icon is not proxied, so only the placeholder is shown
  })

  it('shows the restart banner and restarts for users with the power permission', async () => {
    mockInventory(inventory({ restart_needed: true }))
    const { user } = await openPlugins()
    expect(await screen.findByText('Neustart nötig, damit die Änderungen wirken')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Jetzt neu starten' }))
    expect(await screen.findByText('Neustart für „survival“ angefordert.')).toBeInTheDocument()
  })

  it('shows no restart button without the power permission', async () => {
    enableTotp('alice') // full-trust permissions require 2FA (SEC-10)
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.plugins'] }])
    mockInventory(inventory({ restart_needed: true }))
    await openPlugins(1, 'alice')
    expect(await screen.findByText('Neustart nötig, damit die Änderungen wirken')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Jetzt neu starten' })).not.toBeInTheDocument()
  })

  it('says when Modrinth could not be asked and when the folder is empty', async () => {
    mockInventory(inventory({ source_errors: ['modrinth'] }))
    const { region } = await openPlugins()
    expect(await within(region).findByText('Modrinth gerade nicht erreichbar – Zuordnung unvollständig.')).toBeInTheDocument()
    expect(within(region).getByText('In plugins/ liegen noch keine Jars.')).toBeInTheDocument()
  })

  it('shows cards instead of a table on a phone', async () => {
    setViewport('mobile')
    mockInventory(inventory({ jars: [modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71')] }))
    const { region } = await openPlugins()
    expect(await within(region).findByRole('list', { name: 'Inhalt von plugins/' })).toBeInTheDocument()
    expect(within(region).queryByRole('table')).not.toBeInTheDocument()
    expect(within(region).getByRole('switch', { name: 'LuckPerms aktiv' })).toBeInTheDocument()
  })

  it('reports an unreachable file service', async () => {
    server.use(http.get('*/api/servers/:id/plugins', () => HttpResponse.json({ error: 'files_unavailable', message: 'down' }, { status: 503 })))
    const { region } = await openPlugins()
    expect(await within(region).findByRole('alert')).toHaveTextContent('Der Dateidienst ist nicht erreichbar.')
  })

  it('never shows the plugins_unsupported error code to the user', async () => {
    server.use(http.get('*/api/servers/:id/plugins', () => HttpResponse.json({ error: 'plugins_unsupported', message: 'vanilla' }, { status: 409 })))
    const { region } = await openPlugins()
    expect(await within(region).findByText('Dieser Servertyp unterstützt keine Plugins oder Mods.')).toBeInTheDocument()
    expect(within(region).queryByRole('alert')).not.toBeInTheDocument()
  })
})

describe('PluginsTab – views', () => {
  it('changes the version of an installed jar through the install dialog', async () => {
    mockInventory(inventory({ jars: [modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71')] }))
    server.use(
      http.get('*/api/servers/:id/plugins/versions', () => HttpResponse.json({ versions: [version('v-5.5.72', '5.5.72'), version('v-5.5.71', '5.5.71')] })),
      http.get('*/api/servers/:id/plugins/resolve', () =>
        HttpResponse.json({ install: [planEntry('LuckPerms', '5.5.72')], already: [], external: [], incompatible: [] }),
      ),
    )
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für LuckPerms' }))
    await user.click(screen.getByRole('menuitem', { name: 'Version wechseln …' }))
    const dialog = await screen.findByRole('dialog', { name: 'LuckPerms installieren' })
    expect(await within(dialog).findByRole('radio', { name: '5.5.71' })).toBeChecked()
  })

  it('offers no version change for unmatched jars', async () => {
    mockInventory(inventory({ jars: [jar('OldTool.jar', { meta: OLDTOOL_META })] }))
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für OldTool' }))
    expect(screen.queryByRole('menuitem', { name: 'Version wechseln …' })).not.toBeInTheDocument()
  })

  it('keeps the installed list usable while the search fails', async () => {
    server.use(
      http.get('*/api/servers/:id/plugins/search', () => HttpResponse.json({ error: 'plugin_source_unavailable', message: 'down' }, { status: 502 })),
    )
    const { user, region } = await openPlugins() // mock inventory V5
    await within(region).findByRole('switch', { name: 'OldTool aktiv' })
    await user.click(screen.getByRole('tab', { name: 'Entdecken' }))
    expect(await screen.findByText('Die Suche ist gerade nicht möglich.')).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Installiert' }))
    expect(await screen.findByRole('switch', { name: 'OldTool aktiv' })).toBeEnabled()
  })
})

describe('PluginsTab – after switching', () => {
  const X_META = { kind: 'plugin' as const, id: '', name: 'X', version: '1' }

  it('keeps the switch locked until the reloaded list arrives', async () => {
    let disabled = false
    let release = () => {}
    const reloaded = new Promise<void>((resolve) => {
      release = resolve
    })
    server.use(
      http.get('*/api/servers/:id/plugins', async () => {
        if (!disabled) return HttpResponse.json(inventory({ jars: [jar('X.jar', { meta: X_META })] }))
        await reloaded
        return HttpResponse.json(inventory({ jars: [jar('X.jar.disabled', { meta: X_META })] }))
      }),
      http.get('*/api/servers/:id/plugins/updates', () => HttpResponse.json({ updates: [], checked_at: Date.now() })),
      http.post('*/api/servers/:id/plugins/toggle', () => {
        disabled = true
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('switch', { name: 'X aktiv' }))
    expect(await screen.findByText('„X“ deaktiviert.')).toBeInTheDocument()
    // The old list is still shown: the switch must not be usable (or flip back) before the new one arrives.
    expect(within(region).getByRole('switch', { name: 'X aktiv' })).toBeDisabled()
    release()
    await waitFor(() => expect(within(region).getByRole('switch', { name: 'X aktiv' })).not.toBeChecked())
    expect(within(region).getByRole('switch', { name: 'X aktiv' })).toBeEnabled()
  })

  it('reloads the list when switching fails because the jar is gone', async () => {
    let reads = 0
    server.use(
      http.get('*/api/servers/:id/plugins', () => {
        reads += 1
        return HttpResponse.json(inventory({ jars: reads === 1 ? [jar('X.jar', { meta: X_META })] : [] }))
      }),
      http.get('*/api/servers/:id/plugins/updates', () => HttpResponse.json({ updates: [], checked_at: Date.now() })),
      http.post('*/api/servers/:id/plugins/toggle', () => HttpResponse.json({ error: 'not_found', message: 'jar not found' }, { status: 404 })),
    )
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('switch', { name: 'X aktiv' }))
    await waitFor(() => expect(within(region).queryByRole('switch', { name: 'X aktiv' })).not.toBeInTheDocument())
    expect(within(region).getByText('In plugins/ liegen noch keine Jars.')).toBeInTheDocument()
  })

  it('leaves the search results alone when a jar is switched', async () => {
    server.use(http.get('*/api/servers/:id/plugins/search', () => HttpResponse.json({ hits: [hit('Chunky')], total: 1, offset: 0 })))
    const { user, queryClient } = await openPlugins() // mock inventory V5
    await user.click(screen.getByRole('tab', { name: 'Entdecken' }))
    expect(await screen.findByRole('heading', { name: 'Chunky' })).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Installiert' }))
    await user.click(await screen.findByRole('switch', { name: 'OldTool aktiv' }))
    await waitFor(() => expect(screen.getByRole('switch', { name: 'OldTool aktiv' })).not.toBeChecked())
    // Disabled jars still count as installed (spec §6.3): searching again would only cost upstream calls.
    expect(queryClient.getQueryState(searchKey(1, 'modrinth', ''))?.isInvalidated).toBe(false)
  })
})
