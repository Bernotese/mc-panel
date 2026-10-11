import { act, screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { db } from '@/mocks/db'
import { server } from '@/mocks/node'
import { JOB_POLL_MS } from './api'
import { openFiles } from './test-utils'

function job(state: 'running' | 'done' | 'failed', percent: number, error = '') {
  return {
    id: 'j1',
    state,
    path: 'neue-welt.zip',
    dest: 'neue-welt',
    bytes: percent * 10,
    entries: percent,
    total_bytes: 1000,
    total_entries: 100,
    error,
    started_at: Date.now() - 1000,
    finished_at: state === 'running' ? 0 : Date.now(),
  }
}

const listWithZip = (name = 'neue-welt.zip') =>
  http.get('*/api/servers/:id/files', () =>
    HttpResponse.json({ path: '', truncated: false, entries: [{ name, type: 'file', size: 1024, mtime: Date.now() }] }),
  )

async function openExtractDialog(serverId = 2, name = 'neue-welt.zip') {
  const { user, region } = await openFiles(serverId)
  await user.click(await within(region).findByRole('button', { name: `Aktionen für ${name}` }))
  await user.click(screen.getByRole('menuitem', { name: 'Entpacken …' }))
  const dialog = await screen.findByRole('dialog', { name: `„${name}“ entpacken` })
  return { user, region, dialog }
}

// Real time keeps flowing (so MSW and userEvent work); advanceTimersByTimeAsync jumps over the 2-s poll interval.
beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => {
  vi.useRealTimers()
})

describe('extracting', () => {
  it('shows progress, reports the result and stops polling afterwards', async () => {
    let polls = 0
    server.use(
      listWithZip(),
      http.post('*/api/servers/:id/files/extract', async ({ request }) => {
        expect(await request.json()).toEqual({ path: 'neue-welt.zip', dest: 'neue-welt' })
        return HttpResponse.json(job('running', 10), { status: 202 })
      }),
      http.get('*/api/servers/:id/files/jobs', () => {
        polls += 1
        return HttpResponse.json({ job: polls < 2 ? job('running', 50) : job('done', 100) })
      }),
    )
    const { user, dialog } = await openExtractDialog()
    expect(within(dialog).getByLabelText('Zielordner')).toHaveValue('neue-welt')
    await user.click(within(dialog).getByRole('button', { name: 'Entpacken' }))
    expect(await screen.findByRole('progressbar', { name: 'Fortschritt beim Entpacken' })).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await screen.findByText('„neue-welt.zip“ wurde nach neue-welt entpackt.')).toBeInTheDocument()
    expect(screen.queryByRole('progressbar', { name: 'Fortschritt beim Entpacken' })).not.toBeInTheDocument()
    const seen = polls
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS * 4))
    expect(polls).toBe(seen)
  })

  it('reports a failed job', async () => {
    let polls = 0
    server.use(
      listWithZip(),
      http.post('*/api/servers/:id/files/extract', () => HttpResponse.json(job('running', 10), { status: 202 })),
      http.get('*/api/servers/:id/files/jobs', () => {
        polls += 1
        return HttpResponse.json({ job: job('failed', 20, 'Archiv beschädigt') })
      }),
    )
    const { user, dialog } = await openExtractDialog()
    await user.click(within(dialog).getByRole('button', { name: 'Entpacken' }))
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await screen.findByText('Entpacken von „neue-welt.zip“ fehlgeschlagen: Archiv beschädigt')).toBeInTheDocument()
    expect(polls).toBeGreaterThan(0)
  })

  it('does not offer extracting while the server runs', async () => {
    server.use(listWithZip('w.zip'))
    const { dialog } = await openExtractDialog(1, 'w.zip')
    expect(within(dialog).getByText('Entpacken geht nur bei gestopptem Server.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Entpacken' })).toBeDisabled()
  })

  it('says the agent is unreachable when the state is unknown', async () => {
    const creative = db.servers.find((s) => s.id === 2)
    if (!creative) throw new Error('mock server 2 missing')
    creative.status = { ...creative.status, state: 'unknown' }
    server.use(listWithZip())
    const { dialog } = await openExtractDialog()
    expect(within(dialog).getByText('Der Agent ist nicht erreichbar – Entpacken ist gerade nicht möglich.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Entpacken' })).toBeDisabled()
  })

  it('disables the dialog while another job runs', async () => {
    server.use(
      listWithZip(),
      http.get('*/api/servers/:id/files/jobs', () => HttpResponse.json({ job: job('running', 30) })),
    )
    const { dialog } = await openExtractDialog()
    expect(await within(dialog).findByText('Es läuft bereits ein Entpacken.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Entpacken' })).toBeDisabled()
  })

  it.each([
    [409, 'instance_running', 'Der Server muss dafür gestoppt sein.'],
    [503, 'agent_unavailable', 'Der Agent ist nicht erreichbar.'],
  ])('shows %i %s inside the dialog', async (status, code, message) => {
    server.use(
      listWithZip(),
      http.post('*/api/servers/:id/files/extract', () => HttpResponse.json({ error: code, message: 'x' }, { status })),
    )
    const { user, dialog } = await openExtractDialog()
    await user.click(within(dialog).getByRole('button', { name: 'Entpacken' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent(message)
  })
})
