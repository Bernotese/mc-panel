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
})
