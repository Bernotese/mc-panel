import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { server } from '@/mocks/node'
import { JOB_POLL_MS } from './api'
import { backupJob, jobSequence, mockOverview, openBackups, overview, snapshot } from './test-utils'

// Real time keeps flowing (MSW, userEvent); advanceTimersByTimeAsync jumps over the 2-s poll interval.
beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => {
  vi.useRealTimers()
})

describe('backups tab', () => {
  it('lists the snapshots with kind, note, size and pin, newest first', async () => {
    mockOverview(
      overview({
        snapshots: [
          snapshot(1, { kind: 'manual', note: '<b>vor Update</b>' }),
          snapshot(2, { pinned: true }),
          snapshot(3, { kind: 'pre-restore', bytes: 1024 }),
        ],
      }),
    )
    await openBackups()
    const list = await screen.findByRole('region', { name: 'Snapshots' })
    expect(within(list).getByText('Manuell')).toBeInTheDocument()
    // Notes are text, never markup.
    expect(within(list).getByText('<b>vor Update</b>')).toBeInTheDocument()
    expect(within(list).getAllByText('512 MiB')).toHaveLength(2)
    expect(within(list).getByText('1 KiB')).toBeInTheDocument()
    expect(within(list).getByText('Vor Restore')).toBeInTheDocument()
    expect(within(list).getByText('Angepinnt')).toBeInTheDocument()
    expect(within(list).getAllByText('Geplant')).toHaveLength(1)
  })

  it('shows an empty list with a hint', async () => {
    mockOverview(overview())
    await openBackups()
    expect(await screen.findByText('Noch keine Backups. Erstelle das erste mit „Backup erstellen“.')).toBeInTheDocument()
  })

  it('shows only a hint when backups are unavailable on the host', async () => {
    mockOverview(overview({ available: false }))
    await openBackups()
    expect(await screen.findByText(/Backups sind auf diesem Host nicht verfügbar/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Backup erstellen' })).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Zeitplan' })).not.toBeInTheDocument()
  })

  it('validates the note and starts a backup with the trimmed note', async () => {
    mockOverview(overview())
    let body: unknown = null
    server.use(
      http.post('*/api/servers/:id/backups', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ job: backupJob('backup', 'queued', { queue_position: 1 }) }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Backup erstellen' })
    const note = within(dialog).getByLabelText('Notiz (optional)')
    await user.type(note, 'x'.repeat(65))
    expect(within(dialog).getByText('Die Notiz darf höchstens 64 Zeichen lang sein.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Backup starten' })).toBeDisabled()
    await user.clear(note)
    await user.type(note, '  vor Update  ')
    await user.click(within(dialog).getByRole('button', { name: 'Backup starten' }))
    await waitFor(() => expect(body).toEqual({ note: 'vor Update' }))
    expect(await screen.findByText('Backup gestartet.')).toBeInTheDocument()
    const job = await screen.findByRole('region', { name: 'Backup läuft …' })
    expect(within(job).getByText('Wartet auf einen freien Platz (Position 1).')).toBeInTheDocument()
  })

  it('sends no note when the field is empty', async () => {
    mockOverview(overview())
    let body: unknown = null
    server.use(
      http.post('*/api/servers/:id/backups', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ job: backupJob('backup', 'queued') }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Backup erstellen' })).getByRole('button', { name: 'Backup starten' }))
    await waitFor(() => expect(body).toEqual({}))
  })

  it('follows the job, locks the actions and reloads the snapshots when it is done', async () => {
    let listed = [snapshot(2)]
    let overviewCalls = 0
    server.use(
      http.get('*/api/servers/:id/backups', () => {
        overviewCalls += 1
        return HttpResponse.json(overview({ snapshots: listed }))
      }),
    )
    const jobs = jobSequence([
      backupJob('backup', 'running', { step: 'saving' }),
      backupJob('backup', 'running', { step: 'backing_up', bytes: 3 * 1024 * 1024 }),
      backupJob('backup', 'done', { snapshot: snapshot(1).id }),
    ])
    server.use(
      http.post('*/api/servers/:id/backups', () => {
        jobs.started = true
        return HttpResponse.json({ job: backupJob('backup', 'queued') }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Backup erstellen' })).getByRole('button', { name: 'Backup starten' }))

    const progress = await screen.findByRole('region', { name: 'Backup läuft …' })
    expect(screen.getByRole('button', { name: 'Backup erstellen' })).toBeDisabled()
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await within(progress).findByText('Speichern im Spiel wird angehalten …')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await within(progress).findByText('3 MiB verarbeitet')).toBeInTheDocument()
    const before = overviewCalls
    listed = [snapshot(1, { kind: 'manual' }), snapshot(2)]
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await screen.findByRole('region', { name: 'Backup abgeschlossen' })).toBeInTheDocument()
    expect(screen.getAllByText('Backup abgeschlossen').length).toBeGreaterThan(0) // region title + toast
    await waitFor(() => expect(overviewCalls).toBeGreaterThan(before))
    expect(await screen.findByText('Manuell')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Backup erstellen' })).toBeEnabled()
    const seen = jobs.polls
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS * 4))
    expect(jobs.polls).toBe(seen)
  })

  it('shows a failed job with its translated reason and the detail text', async () => {
    mockOverview(overview())
    const jobs = jobSequence([
      backupJob('backup', 'failed', { error_code: 'insufficient_storage', error: 'only 1 GiB free' }),
    ])
    server.use(
      http.post('*/api/servers/:id/backups', () => {
        jobs.started = true
        return HttpResponse.json({ job: backupJob('backup', 'running') }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Backup erstellen' })).getByRole('button', { name: 'Backup starten' }))
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    const failed = await screen.findByRole('region', { name: 'Backup fehlgeschlagen' })
    expect(within(failed).getByText('Auf dem Host ist nicht genug Platz frei.')).toBeInTheDocument()
    expect(within(failed).getByText('only 1 GiB free')).toBeInTheDocument()
    await act(async () => {
      within(failed).getByRole('button', { name: 'Ausblenden' }).click()
    })
    expect(screen.queryByRole('region', { name: 'Backup fehlgeschlagen' })).not.toBeInTheDocument()
  })

  it('explains backup_running and picks up the job another tab started', async () => {
    mockOverview(overview())
    const jobs = jobSequence([backupJob('backup', 'running', { trigger: 'scheduled' })])
    server.use(
      http.post('*/api/servers/:id/backups', () => {
        jobs.started = true
        return HttpResponse.json({ error: 'backup_running', message: 'a job runs' }, { status: 409 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Backup erstellen' })).getByRole('button', { name: 'Backup starten' }))
    expect(await screen.findByText('Für diesen Server läuft oder wartet bereits ein Backup-Auftrag.')).toBeInTheDocument()
    const progress = await screen.findByRole('region', { name: 'Backup läuft …' })
    expect(within(progress).getByText('Geplanter Lauf')).toBeInTheDocument()
  })

  it('reloads the snapshots without a toast when a running job vanishes (panel restart)', async () => {
    let overviewCalls = 0
    server.use(
      http.get('*/api/servers/:id/backups', () => {
        overviewCalls += 1
        return HttpResponse.json(overview())
      }),
    )
    const jobs = jobSequence([backupJob('backup', 'running')])
    jobs.started = true
    await openBackups()
    await screen.findByRole('region', { name: 'Backup läuft …' })
    const before = overviewCalls
    server.use(http.get('*/api/servers/:id/backups/job', () => HttpResponse.json({ job: null })))
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    await waitFor(() => expect(overviewCalls).toBeGreaterThan(before))
    expect(screen.queryByText('Backup abgeschlossen')).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Backup läuft …' })).not.toBeInTheDocument()
  })

  it('marks the server, its files, plugins, players and open editors stale after a restore', async () => {
    mockOverview(overview({ snapshots: [snapshot(3)] }))
    const jobs = jobSequence([backupJob('restore', 'running', { step: 'restoring' }), backupJob('restore', 'done', { snapshot: snapshot(3).id })])
    jobs.started = true
    const { queryClient } = await openBackups()
    const spy = vi.spyOn(queryClient, 'invalidateQueries')
    await screen.findByRole('region', { name: 'Wiederherstellung läuft …' })
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await screen.findByRole('region', { name: 'Wiederherstellung abgeschlossen' })).toBeInTheDocument()
    await waitFor(() => {
      const keys = spy.mock.calls.map(([filters]) => filters)
      // A prefix, not exact: the files, plugins and players queries sit below ['servers', id].
      expect(keys).toContainEqual({ queryKey: ['servers', 1] })
      expect(keys).toContainEqual({ queryKey: ['file-content', 1] })
    })
  })

  it('keeps the dialog and the note when the backup cannot start, and closes it on backup_running', async () => {
    mockOverview(overview())
    let status = 503
    server.use(
      http.post('*/api/servers/:id/backups', () =>
        status === 503
          ? HttpResponse.json({ error: 'backups_unavailable', message: 'restic missing' }, { status: 503 })
          : HttpResponse.json({ error: 'backup_running', message: 'a job runs' }, { status: 409 }),
      ),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Backup erstellen' })
    await user.type(within(dialog).getByLabelText('Notiz (optional)'), 'vor Update')
    await user.click(within(dialog).getByRole('button', { name: 'Backup starten' }))
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Backup starten' })).toBeEnabled())
    expect(screen.getByRole('dialog', { name: 'Backup erstellen' })).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Notiz (optional)')).toHaveValue('vor Update')
    status = 409
    await user.click(within(dialog).getByRole('button', { name: 'Backup starten' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Backup erstellen' })).not.toBeInTheDocument())
  })

  it('sends one create request for a double click', async () => {
    mockOverview(overview())
    let posts = 0
    server.use(
      http.post('*/api/servers/:id/backups', () => {
        posts += 1
        return HttpResponse.json({ job: backupJob('backup', 'queued') }, { status: 202 })
      }),
    )
    const { user } = await openBackups()
    await user.click(await screen.findByRole('button', { name: 'Backup erstellen' }))
    const submit = within(await screen.findByRole('dialog', { name: 'Backup erstellen' })).getByRole('button', { name: 'Backup starten' })
    act(() => {
      fireEvent.click(submit)
      fireEvent.click(submit)
    })
    expect(await screen.findByText('Backup gestartet.')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(200))
    expect(posts).toBe(1)
  })
})
