import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { server } from '@/mocks/node'
import { openFiles } from './test-utils'

// jsdom cannot lay out CodeMirror; the page logic is tested against a textarea with the same contract.
vi.mock('./code-editor', () => ({
  default: ({
    value,
    label,
    onChange,
    onSave,
  }: {
    value: string
    label: string
    onChange(v: string): void
    onSave(): void
  }) => (
    <textarea
      aria-label={label}
      defaultValue={value}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        if ((e.ctrlKey || e.metaKey) && e.key === 's') {
          e.preventDefault()
          onSave()
        }
      }}
    />
  ),
}))

async function openConfig() {
  const { user, region } = await openFiles()
  await user.click(await within(region).findByRole('button', { name: 'plugins' }))
  await user.click(await within(region).findByRole('button', { name: 'Essentials' }))
  await user.click(await within(region).findByRole('button', { name: 'config.yml' }))
  const editor = await screen.findByRole('textbox', { name: 'Inhalt von config.yml' })
  return { user, region, editor }
}

describe('FileEditor', () => {
  it('saves with Ctrl+S and clears the unsaved marker', async () => {
    const { user, editor } = await openConfig()
    await user.type(editor, '\n# geändert')
    expect(screen.getByText('Ungespeicherte Änderungen')).toBeInTheDocument()
    await user.keyboard('{Control>}s{/Control}')
    expect(await screen.findByText('Gespeichert.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('Ungespeicherte Änderungen')).not.toBeInTheDocument())
  })

  // Review F8: Ctrl+S also saves while the focus is on a button of the editor.
  it('saves with Ctrl+S while the save button has the focus', async () => {
    const { user, editor } = await openConfig()
    await user.type(editor, 'x')
    expect(screen.getByText('Ungespeicherte Änderungen')).toBeInTheDocument()
    screen.getByRole('button', { name: 'Speichern' }).focus()
    await user.keyboard('{Control>}s{/Control}')
    // The toast store is global (an earlier test's "Gespeichert." may still show): the marker proves the save.
    await waitFor(() => expect(screen.queryByText('Ungespeicherte Änderungen')).not.toBeInTheDocument())
  })

  // Review round 2: the panel's editor limits get a file-specific text instead of "too many attempts".
  it('explains a busy editor when opening and saving', async () => {
    const busy = () => HttpResponse.json({ error: 'rate_limited', message: 'busy' }, { status: 429 })
    server.use(http.put('*/api/servers/:id/files/content', busy))
    const { user, editor } = await openConfig()
    await user.type(editor, 'x')
    await user.click(screen.getByRole('button', { name: 'Speichern' }))
    expect(await screen.findByText('Gerade werden zu viele Dateien gleichzeitig bearbeitet – bitte gleich noch einmal versuchen.')).toBeInTheDocument()
  })

  it('explains a busy editor when the file cannot be opened', async () => {
    server.use(http.get('*/api/servers/:id/files/content', () => HttpResponse.json({ error: 'rate_limited', message: 'busy' }, { status: 429 })))
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'server.properties' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Gerade werden zu viele Dateien gleichzeitig bearbeitet – bitte gleich noch einmal versuchen.')
  })

  it('offers reload or overwrite after a conflicting save', async () => {
    server.use(
      http.put('*/api/servers/:id/files/content', () => HttpResponse.json({ error: 'file_modified', message: 'modified' }, { status: 409 })),
    )
    const { user, editor } = await openConfig()
    await user.type(editor, 'x')
    await user.click(screen.getByRole('button', { name: 'Speichern' }))
    const dialog = await screen.findByRole('dialog', { name: 'Datei wurde geändert' })
    await user.click(within(dialog).getByRole('button', { name: 'Neu laden' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.queryByText('Ungespeicherte Änderungen')).not.toBeInTheDocument()
  })

  it('asks before leaving with unsaved changes', async () => {
    const { user, region, editor } = await openConfig()
    await user.type(editor, 'x')
    await user.click(screen.getByRole('button', { name: 'Zurück zur Liste' }))
    const dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
    await user.click(within(dialog).getByRole('button', { name: 'Verwerfen' }))
    expect(await within(region).findByRole('button', { name: 'config.yml' })).toBeInTheDocument()
  })

  it('asks before switching tabs with unsaved changes', async () => {
    const { user, editor } = await openConfig()
    await user.type(editor, 'x')
    await user.click(screen.getByRole('tab', { name: 'Übersicht' }))
    const dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
    await user.click(within(dialog).getByRole('button', { name: 'Verwerfen' }))
    expect(await screen.findByRole('region', { name: 'Zustand' })).toBeInTheDocument()
  })

  it('offers a download for files the editor cannot open', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'plugins' }))
    await user.click(await within(region).findByRole('button', { name: 'EssentialsX.jar' }))
    expect(await screen.findByText('Diese Datei ist zu groß für den Editor oder keine Textdatei.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Stattdessen herunterladen' })).toHaveAttribute(
      'href',
      '/api/servers/1/files/download?path=plugins%2FEssentialsX.jar',
    )
  })

  it('explains server.properties and shows the redacted password', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'server.properties' }))
    const editor = await screen.findByRole('textbox', { name: 'Inhalt von server.properties' })
    expect((editor as HTMLTextAreaElement).value).toContain('rcon.password=********')
    expect(screen.getByText(/wird beim Start aus den panel-Einstellungen überschrieben/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Zu den Einstellungen' })).toBeInTheDocument()
  })

  it('asks before "Zu den Einstellungen" drops unsaved changes', async () => {
    const { user, region } = await openFiles()
    await user.click(await within(region).findByRole('button', { name: 'server.properties' }))
    const editor = await screen.findByRole('textbox', { name: 'Inhalt von server.properties' })
    await user.type(editor, 'x')
    await user.click(screen.getByRole('button', { name: 'Zu den Einstellungen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
    expect(screen.getByRole('tab', { name: 'Dateien', hidden: true })).toHaveAttribute('aria-selected', 'true')
    await user.click(within(dialog).getByRole('button', { name: 'Verwerfen' }))
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Einstellungen' })).toHaveAttribute('aria-selected', 'true'))
  })

  describe('after a conflicting save', () => {
    function mockConflict(secondRead: 'ok' | 'fail') {
      let reads = 0
      const writes: { expected_mtime: number | null; content: string }[] = []
      server.use(
        http.get('*/api/servers/:id/files/content', ({ request }) => {
          reads += 1
          if (reads > 1 && secondRead === 'fail') return HttpResponse.json({ error: 'agent_unavailable', message: 'down' }, { status: 503 })
          return HttpResponse.json({ path: new URL(request.url).searchParams.get('path'), content: 'a: 1\n', mtime: reads === 1 ? 100 : 200, size: 5 })
        }),
        http.put('*/api/servers/:id/files/content', async ({ request }) => {
          writes.push((await request.json()) as { expected_mtime: number | null; content: string })
          return writes.length === 1
            ? HttpResponse.json({ error: 'file_modified', message: 'modified' }, { status: 409 })
            : HttpResponse.json({ mtime: 300, size: 6 })
        }),
      )
      return writes
    }

    it('overwrites with the fresh mtime and clears the unsaved marker', async () => {
      const writes = mockConflict('ok')
      const { user, editor } = await openConfig()
      await user.type(editor, 'x')
      await user.click(screen.getByRole('button', { name: 'Speichern' }))
      const dialog = await screen.findByRole('dialog', { name: 'Datei wurde geändert' })
      await user.click(within(dialog).getByRole('button', { name: 'Überschreiben' }))
      await waitFor(() => expect(writes).toHaveLength(2))
      expect(writes[0].expected_mtime).toBe(100)
      expect(writes[1].expected_mtime).toBe(200)
      await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
      await waitFor(() => expect(screen.queryByText('Ungespeicherte Änderungen')).not.toBeInTheDocument())
    })

    it('keeps the draft when reloading fails', async () => {
      mockConflict('fail')
      const { user, editor } = await openConfig()
      await user.type(editor, 'x')
      await user.click(screen.getByRole('button', { name: 'Speichern' }))
      const dialog = await screen.findByRole('dialog', { name: 'Datei wurde geändert' })
      await user.click(within(dialog).getByRole('button', { name: 'Neu laden' }))
      expect((await screen.findAllByText('Der Agent ist nicht erreichbar.')).length).toBeGreaterThan(0)
      expect(screen.getByText('Ungespeicherte Änderungen')).toBeInTheDocument()
    })
  })
})
