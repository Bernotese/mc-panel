import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'

export async function openFiles(serverId = 1, username = 'admin') {
  const user = userEvent.setup()
  await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs(username) })
  await user.click(await screen.findByRole('tab', { name: 'Dateien' }))
  const region = await screen.findByRole('region', { name: 'Dateien' })
  return { user, region }
}

describe('FilesTab', () => {
  it('lists the top level with folders, files and links', async () => {
    const { region } = await openFiles()
    expect(await within(region).findByRole('button', { name: 'plugins' })).toBeInTheDocument()
    expect(within(region).getByText('server.properties')).toBeInTheDocument()
    expect(within(region).getByText('current')).toBeInTheDocument()
    expect(within(region).getByText('Verknüpfung')).toBeInTheDocument()
    expect(screen.getByText('Der Server läuft. Änderungen wirken meist erst nach einem Neustart.')).toBeInTheDocument()
  })

  it('opens folders and walks back via the breadcrumbs', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'plugins' }))
    expect(await within(region).findByRole('button', { name: 'Essentials' })).toBeInTheDocument()
    const crumbs = screen.getByRole('navigation', { name: 'Pfad' })
    expect(within(crumbs).getByText('plugins')).toHaveAttribute('aria-current', 'page')
    await user.click(within(crumbs).getByRole('button', { name: 'Server-Daten' }))
    expect(await within(region).findByText('server.properties')).toBeInTheDocument()
  })

  it('links downloads straight to the API', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für server.properties' }))
    expect(screen.getByRole('menuitem', { name: 'Herunterladen' })).toHaveAttribute(
      'href',
      '/api/servers/1/files/download?path=server.properties',
    )
    await user.keyboard('{Escape}')
    await user.click(within(region).getByRole('button', { name: 'Aktionen für plugins' }))
    expect(screen.getByRole('menuitem', { name: 'Als Zip herunterladen' })).toHaveAttribute('href', '/api/servers/1/files/download?path=plugins')
  })

  it('creates a folder after checking the name', async () => {
    const { user, region } = await openFiles()
    await user.click(await screen.findByRole('button', { name: 'Neuer Ordner' }))
    const dialog = await screen.findByRole('dialog', { name: 'Neuer Ordner in Server-Daten' })
    const input = within(dialog).getByLabelText('Name')
    await user.type(input, 'a/b')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    expect(within(dialog).getByText('Ein Name darf kein „/“ enthalten.')).toBeInTheDocument()
    await user.clear(input)
    await user.type(input, 'backups')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    expect(await within(region).findByRole('button', { name: 'backups' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('refuses reserved names before sending', async () => {
    const { user } = await openFiles()
    await user.click(await screen.findByRole('button', { name: 'Neue Datei' }))
    const dialog = await screen.findByRole('dialog', { name: 'Neue Datei in Server-Daten' })
    await user.type(within(dialog).getByLabelText('Name'), '.rcon-cli.env')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    expect(within(dialog).getByText('Dieser Name ist für das panel reserviert.')).toBeInTheDocument()
  })

  it('renames a file and shows a conflict inline', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für bukkit.yml' }))
    await user.click(screen.getByRole('menuitem', { name: 'Umbenennen oder verschieben' }))
    const dialog = await screen.findByRole('dialog', { name: '„bukkit.yml“ umbenennen oder verschieben' })
    const input = within(dialog).getByLabelText('Neuer Pfad')
    expect(input).toHaveValue('bukkit.yml')
    await user.clear(input)
    await user.type(input, 'server.properties')
    await user.click(within(dialog).getByRole('button', { name: 'Umbenennen' }))
    expect(await within(dialog).findByRole('alert')).toBeInTheDocument() // 409 file_exists
    await user.clear(input)
    await user.type(input, 'bukkit-alt.yml')
    await user.click(within(dialog).getByRole('button', { name: 'Umbenennen' }))
    expect(await within(region).findByText('bukkit-alt.yml')).toBeInTheDocument()
    expect(within(region).queryByText('bukkit.yml')).not.toBeInTheDocument()
  })

  it('deletes a folder after confirmation', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für logs' }))
    await user.click(screen.getByRole('menuitem', { name: 'Löschen' }))
    const dialog = await screen.findByRole('dialog', { name: '„logs“ löschen?' })
    expect(within(dialog).getByText('Der Ordner wird inklusive Inhalt endgültig gelöscht.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Endgültig löschen' }))
    await waitFor(() => expect(within(region).queryByRole('button', { name: 'logs' })).not.toBeInTheDocument())
  })

  it('explains a server that was never started', async () => {
    server.use(http.get('*/api/servers/:id/files', () => HttpResponse.json({ error: 'not_found', message: 'not found' }, { status: 404 })))
    const { region } = await openFiles()
    expect(await within(region).findByText('Der Server wurde noch nie gestartet – es gibt noch keine Daten.')).toBeInTheDocument()
  })

  it('sorts by size with folders first', async () => {
    const { user, region } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    const names = () =>
      within(region)
        .getAllByRole('row')
        .slice(1)
        .map((row) => within(row).getAllByRole('cell')[0].textContent ?? '')
    await user.selectOptions(screen.getByLabelText('Sortieren nach'), 'size')
    expect(names().slice(0, 3)).toEqual(['logs', 'plugins', 'world'])
    expect(names()[3]).toMatch(/^current/) // the link (size 0) is the smallest non-folder
    await user.click(screen.getByRole('button', { name: 'Aufsteigend sortiert – umkehren' }))
    expect(names().slice(0, 4)).toEqual(['world', 'plugins', 'logs', 'world-backup.zip'])
    expect(screen.getByRole('button', { name: 'Absteigend sortiert – umkehren' })).toBeInTheDocument()
  })

  it('uploads files into the open folder', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'plugins' }))
    await within(region).findByRole('button', { name: 'Essentials' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File(['jar'], 'WorldEdit.jar'))
    const uploads = await screen.findByRole('region', { name: 'Uploads' })
    expect(await within(uploads).findByText('Hochgeladen')).toBeInTheDocument()
    expect(await within(region).findByText('WorldEdit.jar')).toBeInTheDocument()
  })

  it('asks before overwriting an existing file and can skip it', async () => {
    const { user, region } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File(['x'], 'server.properties'))
    const dialog = await screen.findByRole('dialog', { name: '„server.properties“ existiert bereits' })
    await user.click(within(dialog).getByRole('button', { name: 'Überspringen' }))
    const uploads = screen.getByRole('region', { name: 'Uploads' })
    expect(await within(uploads).findByText('Übersprungen')).toBeInTheDocument()
  })

  it('rejects reserved names without uploading', async () => {
    const { user, region } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File(['x'], '.rcon-cli.env'))
    const uploads = await screen.findByRole('region', { name: 'Uploads' })
    expect(within(uploads).getByText('Dieser Name ist für das panel reserviert.')).toBeInTheDocument()
  })
})
