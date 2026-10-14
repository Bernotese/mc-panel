import { within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { hangarJar, inventory, jar, mockInventory, modrinthJar, openPlugins } from './test-utils'

const JARS = [
  modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71'),
  hangarJar('ViaVersion-5.6.0.jar', 'ViaVersion', '5.6.0'),
  modrinthJar('Chunky.jar.disabled', 'Chunky', '1.4', { enabled: false }),
  jar('OldTool.jar', { meta: { kind: 'plugin', id: '', name: 'OldTool', version: '1.0' } }),
]

describe('InstalledList – filters', () => {
  it('filters by name, status and origin and resets the filters', async () => {
    mockInventory(inventory({ jars: JARS }))
    const { user, region } = await openPlugins()
    await within(region).findByText('LuckPerms')
    const rows = () => within(region).getAllByRole('row').length - 1

    await user.type(within(region).getByLabelText('Name filtern'), 'via')
    expect(rows()).toBe(1)
    expect(within(region).getByText('ViaVersion')).toBeInTheDocument()
    expect(within(region).getByText('1 von 4 Jars')).toBeInTheDocument()
    await user.clear(within(region).getByLabelText('Name filtern'))

    // The file name counts as well.
    await user.type(within(region).getByLabelText('Name filtern'), 'bukkit')
    expect(within(region).getByText('LuckPerms')).toBeInTheDocument()
    expect(rows()).toBe(1)
    await user.clear(within(region).getByLabelText('Name filtern'))

    const status = within(region).getByRole('group', { name: 'Status' })
    await user.click(within(status).getByRole('button', { name: 'Deaktiviert' }))
    expect(within(status).getByRole('button', { name: 'Deaktiviert' })).toHaveAttribute('aria-pressed', 'true')
    expect(rows()).toBe(1)
    expect(within(region).getByText('Chunky')).toBeInTheDocument()
    await user.click(within(status).getByRole('button', { name: 'Aktiv' }))
    expect(rows()).toBe(3)

    const origin = within(region).getByRole('group', { name: 'Herkunft' })
    await user.click(within(origin).getByRole('button', { name: 'Manuell' }))
    expect(rows()).toBe(1)
    expect(within(region).getByText('OldTool')).toBeInTheDocument()
    await user.click(within(origin).getByRole('button', { name: 'Hangar' }))
    expect(within(region).getByText('ViaVersion')).toBeInTheDocument()

    await user.type(within(region).getByLabelText('Name filtern'), 'luck')
    expect(within(region).getByText('Keine Jars passen zu den Filtern.')).toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Filter zurücksetzen' }))
    expect(rows()).toBe(4)
    expect(within(region).getByLabelText('Name filtern')).toHaveValue('')
  })

  it('offers Hangar as origin only where it is a source', async () => {
    mockInventory(inventory({ kind: 'mod', dir: 'mods', sources: ['modrinth'], jars: [modrinthJar('lithium.jar', 'Lithium', '0.14')] }))
    const { region } = await openPlugins(4, 'admin', 'Mods')
    const origin = await within(region).findByRole('group', { name: 'Herkunft' })
    expect(within(origin).getAllByRole('button').map((b) => b.textContent)).toEqual(['Alle', 'Modrinth', 'Manuell'])
  })

  it('shows no filters for an empty folder', async () => {
    mockInventory(inventory())
    const { region } = await openPlugins()
    await within(region).findByText('In plugins/ liegen noch keine Jars.')
    expect(within(region).queryByLabelText('Name filtern')).not.toBeInTheDocument()
  })
})
