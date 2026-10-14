import { act, createEvent, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { delay, http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server } from '@/mocks/node'
import { openFiles } from './test-utils'

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
    // Review F10: there is no folder to create files in yet.
    for (const name of ['Neuer Ordner', 'Neue Datei', 'Hochladen']) {
      expect(within(region).queryByRole('button', { name })).not.toBeInTheDocument()
    }
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

  // Review F2: an early 409 can arrive as a reset connection; a name the listing shows is asked about up front.
  it('asks about an existing file before sending it', async () => {
    const seen: string[] = []
    server.use(
      http.post('*/api/servers/:id/files/upload', ({ request }) => {
        seen.push(new URL(request.url).searchParams.get('overwrite') ?? '')
        return HttpResponse.json({ size: 1 }, { status: 201 })
      }),
    )
    const { user, region } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File(['x'], 'server.properties'))
    const dialog = await screen.findByRole('dialog', { name: '„server.properties“ existiert bereits' })
    expect(seen).toEqual([])
    await user.click(within(dialog).getByRole('button', { name: 'Überschreiben' }))
    const uploads = screen.getByRole('region', { name: 'Uploads' })
    expect(await within(uploads).findByText('Hochgeladen')).toBeInTheDocument()
    expect(seen).toEqual(['true'])
  })

  // Review F6: after the last byte the panel and the service still store the file.
  it('says an upload is being processed once every byte is sent', async () => {
    server.use(
      http.post('*/api/servers/:id/files/upload', async () => {
        await delay('infinite')
        return HttpResponse.json({ size: 1 }, { status: 201 })
      }),
    )
    const { user, region } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File([], 'empty.txt'))
    const uploads = await screen.findByRole('region', { name: 'Uploads' })
    expect(await within(uploads).findByText('Wird verarbeitet …')).toBeInTheDocument()
  })

  // Review F1: leaving the files area cancels running uploads, so any navigation asks first (useBlocker).
  it('asks before navigating away while uploads run', async () => {
    server.use(
      http.post('*/api/servers/:id/files/upload', async () => {
        await delay('infinite')
        return HttpResponse.json({ size: 1 }, { status: 201 })
      }),
    )
    const { user, region, router } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File(['x'], 'big.jar'))
    await screen.findByRole('region', { name: 'Uploads' })

    act(() => void router.navigate({ to: '/' }))
    const dialog = await screen.findByRole('dialog', { name: 'Laufende Uploads abbrechen?' })
    expect(within(dialog).getByText('Laufende Uploads werden abgebrochen.')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Abbrechen' }))
    expect(router.state.location.pathname).toBe('/servers/1/files')

    act(() => void router.navigate({ to: '/' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Laufende Uploads abbrechen?' })).getByRole('button', { name: 'Verlassen' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })

  it('rejects reserved names without uploading', async () => {
    const { user, region } = await openFiles()
    await within(region).findByRole('button', { name: 'plugins' })
    await user.upload(screen.getByLabelText('Dateien hochladen'), new File(['x'], '.rcon-cli.env'))
    const uploads = await screen.findByRole('region', { name: 'Uploads' })
    expect(within(uploads).getByText('Dieser Name ist für das panel reserviert.')).toBeInTheDocument()
  })

  it('sends the normalised path when renaming and does nothing for an unchanged path', async () => {
    let body: unknown = null
    server.use(
      http.post('*/api/servers/:id/files/rename', async ({ request }) => {
        body = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für bukkit.yml' }))
    await user.click(screen.getByRole('menuitem', { name: 'Umbenennen oder verschieben' }))
    let dialog = await screen.findByRole('dialog', { name: '„bukkit.yml“ umbenennen oder verschieben' })
    const input = within(dialog).getByLabelText('Neuer Pfad')
    await user.clear(input)
    await user.type(input, 'plugins/bukkit-neu.yml/')
    await user.click(within(dialog).getByRole('button', { name: 'Umbenennen' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(body).toEqual({ from: 'bukkit.yml', to: 'plugins/bukkit-neu.yml' })

    body = null
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für ops.json' }))
    await user.click(screen.getByRole('menuitem', { name: 'Umbenennen oder verschieben' }))
    dialog = await screen.findByRole('dialog', { name: '„ops.json“ umbenennen oder verschieben' })
    await user.click(within(dialog).getByRole('button', { name: 'Umbenennen' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(body).toBeNull()
  })

  it('refuses reserved names in any letter case', async () => {
    const { user } = await openFiles()
    await user.click(await screen.findByRole('button', { name: 'Neue Datei' }))
    const dialog = await screen.findByRole('dialog', { name: 'Neue Datei in Server-Daten' })
    await user.type(within(dialog).getByLabelText('Name'), '.RCON-CLI.ENV')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    expect(within(dialog).getByText('Dieser Name ist für das panel reserviert.')).toBeInTheDocument()
  })

  describe('drag and drop', () => {
    const dragData = (extra: object = {}) => ({ dataTransfer: { types: ['Files'], files: [], items: [], ...extra } })
    async function zone() {
      const { region } = await openFiles()
      await within(region).findByRole('button', { name: 'plugins' })
      return { region, zone: within(region).getByRole('table').closest('div.relative.space-y-3') as HTMLElement }
    }

    it('keeps the highlight while moving over children and clears it on a real exit', async () => {
      const { region, zone: dropZone } = await zone()
      fireEvent.dragOver(dropZone, dragData())
      const overlay = await within(region).findByText('Dateien hier ablegen, um sie nach Server-Daten hochzuladen')
      expect(overlay).toHaveClass('pointer-events-none')
      const child = within(dropZone).getByRole('table')
      // jsdom has no DragEvent, so relatedTarget has to be set by hand.
      const leave = (relatedTarget: Element) => {
        const event = createEvent.dragLeave(dropZone)
        Object.defineProperty(event, 'relatedTarget', { value: relatedTarget })
        fireEvent(dropZone, event)
      }
      leave(child)
      expect(within(region).getByText('Dateien hier ablegen, um sie nach Server-Daten hochzuladen')).toBeInTheDocument()
      leave(document.body)
      expect(within(region).queryByText('Dateien hier ablegen, um sie nach Server-Daten hochzuladen')).not.toBeInTheDocument()
    })

    it('names dropped folders instead of silently ignoring them', async () => {
      const { region, zone: dropZone } = await zone()
      const folder = { kind: 'file', webkitGetAsEntry: () => ({ isDirectory: true }), getAsFile: () => null }
      fireEvent.drop(dropZone, dragData({ items: [folder] }))
      expect(await within(region).findByText('Ordner werden nicht unterstützt – bitte als Zip hochladen.')).toBeInTheDocument()
      expect(screen.queryByRole('region', { name: 'Uploads' })).not.toBeInTheDocument()
    })

    it('uploads dropped files and skips only the folders', async () => {
      const { zone: dropZone } = await zone()
      const file = new File(['x'], 'neu.txt')
      const items = [
        { kind: 'file', webkitGetAsEntry: () => ({ isDirectory: true }), getAsFile: () => null },
        { kind: 'file', webkitGetAsEntry: () => ({ isDirectory: false }), getAsFile: () => file },
      ]
      fireEvent.drop(dropZone, dragData({ items }))
      const uploads = await screen.findByRole('region', { name: 'Uploads' })
      expect(await within(uploads).findByText('neu.txt')).toBeInTheDocument()
    })

    it('stops the browser from opening a file dropped beside the zone', async () => {
      await zone()
      expect(fireEvent.dragOver(document.body, dragData())).toBe(false) // false = preventDefault was called
      expect(fireEvent.drop(document.body, dragData())).toBe(false)
    })
  })
})
