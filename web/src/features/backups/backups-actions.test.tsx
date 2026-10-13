import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { server } from '@/mocks/node'
import { backupJob, mockOverview, openBackups, overview, snapshot, snapshotId } from './test-utils'

async function openMenu(user: Awaited<ReturnType<typeof openBackups>>['user'], index = 0) {
  const triggers = await screen.findAllByRole('button', { name: /^Aktionen für den Snapshot vom / })
  await user.click(triggers[index])
}

describe('snapshot actions', () => {
  it('offers pinning only for scheduled snapshots and replaces the id with the answer', async () => {
    mockOverview(overview({ snapshots: [snapshot(1), snapshot(2, { kind: 'manual' })] }))
    const pinned: string[] = []
    server.use(
      http.post('*/api/servers/:id/backups/:snapshot/pin', async ({ params, request }) => {
        pinned.push(String(params.snapshot))
        const body = (await request.json()) as { pinned: boolean }
        return HttpResponse.json({ snapshot: snapshot(1, { id: snapshotId(99), pinned: body.pinned }) })
      }),
    )
    const { user } = await openBackups()
    await openMenu(user, 1)
    expect(screen.queryByRole('menuitem', { name: 'Anpinnen' })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')

    await openMenu(user, 0)
    await user.click(await screen.findByRole('menuitem', { name: 'Anpinnen' }))
    expect(await screen.findByText('Snapshot angepinnt.')).toBeInTheDocument()
    expect(await screen.findByText('Angepinnt')).toBeInTheDocument()
    await openMenu(user, 0)
    await user.click(await screen.findByRole('menuitem', { name: 'Lösen' }))
    await waitFor(() => expect(pinned).toEqual([snapshotId(1), snapshotId(99)]))
  })

  it('links the download to the API', async () => {
    mockOverview(overview({ snapshots: [snapshot(7)] }))
    const { user } = await openBackups()
    await openMenu(user)
    const link = await screen.findByRole('menuitem', { name: 'Als Zip herunterladen' })
    expect(link).toHaveAttribute('href', `/api/servers/1/backups/${snapshotId(7)}/download`)
    expect(link).toHaveAttribute('download')
  })

  it('restores only after the server name was typed and starts the job', async () => {
    mockOverview(overview({ snapshots: [snapshot(3)] }))
    let restored: string | null = null
    server.use(
      http.post('*/api/servers/:id/backups/:snapshot/restore', ({ params }) => {
        restored = String(params.snapshot)
        return HttpResponse.json({ job: backupJob('restore', 'running', { step: 'stopping', snapshot: String(params.snapshot) }) }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Wiederherstellen' }))
    const dialog = await screen.findByRole('dialog', { name: /wiederherstellen\?$/ })
    expect(within(dialog).getByText(/sichert den aktuellen Stand als Snapshot „Vor Restore“/)).toBeInTheDocument()
    const submit = within(dialog).getByRole('button', { name: 'Wiederherstellen' })
    expect(submit).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'surviva')
    expect(submit).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'l')
    await user.click(submit)
    await waitFor(() => expect(restored).toBe(snapshotId(3)))
    expect(await screen.findByText('Wiederherstellung gestartet.')).toBeInTheDocument()
    const progress = await screen.findByRole('region', { name: 'Wiederherstellung läuft …' })
    expect(within(progress).getByText('Server wird gestoppt …')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Backup erstellen' })).toBeDisabled()
  })

  it('asks for sudo before a restore and shows nothing when it is cancelled', async () => {
    mockOverview(overview({ snapshots: [snapshot(3)] }))
    server.use(
      http.post('*/api/servers/:id/backups/:snapshot/restore', () =>
        HttpResponse.json({ error: 'sudo_required', message: 're-authentication required' }, { status: 403 }),
      ),
    )
    const { user } = await openBackups(1, 'admin', { sudo: false })
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Wiederherstellen' }))
    const dialog = await screen.findByRole('dialog', { name: /wiederherstellen\?$/ })
    await user.type(within(dialog).getByLabelText('Zum Bestätigen den Namen „survival“ eintippen'), 'survival')
    await user.click(within(dialog).getByRole('button', { name: 'Wiederherstellen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.click(within(sudo).getByRole('button', { name: 'Abbrechen' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Aktion bestätigen' })).not.toBeInTheDocument())
    await new Promise((resolve) => setTimeout(resolve, 100))
    expect(screen.queryByText('Diese Aktion muss bestätigt werden.')).not.toBeInTheDocument()
  })

  it('deletes a snapshot after confirmation', async () => {
    mockOverview(overview({ snapshots: [snapshot(4, { kind: 'pre-restore' })] }))
    let deleted: string | null = null
    server.use(
      http.delete('*/api/servers/:id/backups/:snapshot', ({ params }) => {
        deleted = String(params.snapshot)
        return HttpResponse.json({ job: backupJob('delete', 'running', { step: 'deleting' }) }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Löschen' }))
    const dialog = await screen.findByRole('dialog', { name: /löschen\?$/ })
    await user.click(within(dialog).getByRole('button', { name: 'Snapshot löschen' }))
    await waitFor(() => expect(deleted).toBe(snapshotId(4)))
    expect(await screen.findByRole('region', { name: 'Snapshot wird gelöscht …' })).toBeInTheDocument()
  })

  it('locks restore, delete and pin while a job runs, but not the download', async () => {
    mockOverview(overview({ snapshots: [snapshot(1)] }))
    server.use(http.get('*/api/servers/:id/backups/job', () => HttpResponse.json({ job: backupJob('backup', 'running') })))
    const { user } = await openBackups()
    await screen.findByRole('region', { name: 'Backup läuft …' })
    await openMenu(user)
    expect(await screen.findByRole('menuitem', { name: 'Wiederherstellen' })).toHaveAttribute('data-disabled')
    expect(screen.getByRole('menuitem', { name: 'Löschen' })).toHaveAttribute('data-disabled')
    expect(screen.getByRole('menuitem', { name: 'Anpinnen' })).toHaveAttribute('data-disabled')
    expect(screen.getByRole('menuitem', { name: 'Als Zip herunterladen' })).not.toHaveAttribute('data-disabled')
  })

  it('reloads the list when a pin hits a snapshot that is gone', async () => {
    let calls = 0
    server.use(
      http.get('*/api/servers/:id/backups', () => {
        calls += 1
        return HttpResponse.json(overview({ snapshots: [snapshot(1)] }))
      }),
      http.post('*/api/servers/:id/backups/:snapshot/pin', () => HttpResponse.json({ error: 'not_found', message: 'gone' }, { status: 404 })),
    )
    const { user } = await openBackups()
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Anpinnen' }))
    await waitFor(() => expect(calls).toBeGreaterThan(1))
  })

  it('names the busy file service when a pin is refused without a running job', async () => {
    server.use(
      http.get('*/api/servers/:id/backups', () => HttpResponse.json(overview({ snapshots: [snapshot(1)] }))),
      http.post('*/api/servers/:id/backups/:snapshot/pin', () =>
        HttpResponse.json({ error: 'backup_running', message: 'the file service is busy with this server (download or extract); try again later' }, { status: 409 }),
      ),
    )
    const { user } = await openBackups()
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Anpinnen' }))
    expect(await screen.findByText(/Dateidienst ist für diesen Server gerade beschäftigt/)).toBeInTheDocument()
    expect(screen.queryByText(/läuft oder wartet bereits ein Backup-Auftrag/)).not.toBeInTheDocument()
  })

  it('keeps the restore dialog and the typed name when the panel refuses', async () => {
    mockOverview(overview({ snapshots: [snapshot(3)] }))
    server.use(
      http.post('*/api/servers/:id/backups/:snapshot/restore', () =>
        HttpResponse.json({ error: 'validation_failed', message: 'snapshot must be 64 lowercase hex characters' }, { status: 400 }),
      ),
    )
    const { user } = await openBackups()
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Wiederherstellen' }))
    const dialog = await screen.findByRole('dialog', { name: /wiederherstellen\?$/ })
    const name = within(dialog).getByLabelText('Zum Bestätigen den Namen „survival“ eintippen')
    await user.type(name, 'survival')
    await user.click(within(dialog).getByRole('button', { name: 'Wiederherstellen' }))
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Wiederherstellen' })).toBeEnabled())
    expect(screen.getByRole('dialog', { name: /wiederherstellen\?$/ })).toBeInTheDocument()
    expect(name).toHaveValue('survival')
  })

  it('locks restore and delete while a pin is in flight', async () => {
    mockOverview(overview({ snapshots: [snapshot(1)] }))
    let release: () => void = () => undefined
    server.use(
      http.post('*/api/servers/:id/backups/:snapshot/pin', async () => {
        await new Promise<void>((resolve) => {
          release = resolve
        })
        return HttpResponse.json({ snapshot: snapshot(1, { id: snapshotId(98), pinned: true }) })
      }),
    )
    const { user } = await openBackups()
    await openMenu(user)
    await user.click(await screen.findByRole('menuitem', { name: 'Anpinnen' }))
    await openMenu(user)
    expect(await screen.findByRole('menuitem', { name: 'Wiederherstellen' })).toHaveAttribute('data-disabled')
    expect(screen.getByRole('menuitem', { name: 'Löschen' })).toHaveAttribute('data-disabled')
    expect(screen.getByRole('menuitem', { name: 'Als Zip herunterladen' })).not.toHaveAttribute('data-disabled')
    release()
    expect(await screen.findByText('Snapshot angepinnt.')).toBeInTheDocument()
  })
})
