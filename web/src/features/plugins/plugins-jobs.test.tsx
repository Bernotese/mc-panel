import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { PluginJob, PluginJobStep } from '@/api/types'
import { server } from '@/mocks/node'
import { JOB_POLL_MS, startJob } from './api'
import { hit, inventory, job, mockInventory, modrinthJar, openDiscover, openPlugins, step, update } from './test-utils'

const LP = modrinthJar('LuckPerms-Bukkit-5.5.71.jar', 'LuckPerms', '5.5.71')
const NEW_FILE = 'LuckPerms-Bukkit-5.5.72.jar'
const steps = (state: PluginJobStep['state']) => [step('LuckPerms', NEW_FILE, state)]

/** GET …/jobs answers null until `started`, then the sequence (the last entry repeats). */
function jobSequence(sequence: PluginJob[]) {
  const state = { started: false, polls: 0 }
  server.use(
    http.get('*/api/servers/:id/plugins/jobs', () => {
      if (!state.started) return HttpResponse.json({ job: null })
      const next = sequence[Math.min(state.polls, sequence.length - 1)]
      state.polls += 1
      return HttpResponse.json({ job: next })
    }),
  )
  return state
}

// Real time keeps flowing (MSW, userEvent); advanceTimersByTimeAsync jumps over the 2-s poll interval.
beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => {
  vi.useRealTimers()
})

describe('plugin jobs and updates', () => {
  it('follows an update job, locks the controls and reloads the inventory afterwards', async () => {
    let inventoryCalls = 0
    let updated = false
    server.use(
      http.get('*/api/servers/:id/plugins', () => {
        inventoryCalls += 1
        return HttpResponse.json(inventory({ jars: [updated ? modrinthJar(NEW_FILE, 'LuckPerms', '5.5.72') : LP] }))
      }),
      http.get('*/api/servers/:id/plugins/updates', () =>
        HttpResponse.json({ updates: updated ? [] : [update(LP.file, '5.5.71', '5.5.72')], checked_at: Date.now() }),
      ),
    )
    const jobs = jobSequence([job('update', 'running', steps('downloading')), job('update', 'done', steps('done'))])
    server.use(
      http.post('*/api/servers/:id/plugins/update', async ({ request }) => {
        expect(await request.json()).toEqual({ files: [] })
        jobs.started = true
        return HttpResponse.json(job('update', 'running', steps('pending')), { status: 202 })
      }),
    )
    const { user, region } = await openPlugins()
    expect(await within(region).findByText('Update: 5.5.72')).toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Alle aktualisieren (1)' }))

    const progress = await screen.findByRole('region', { name: 'Aktualisierung läuft …' })
    expect(within(progress).getByText('Wartet')).toBeInTheDocument()
    expect(within(progress).getByRole('progressbar', { name: 'Fortschritt des Auftrags' })).toBeInTheDocument()
    expect(within(region).getByRole('switch', { name: 'LuckPerms aktiv' })).toBeDisabled()
    expect(within(region).getByRole('button', { name: 'Alle aktualisieren (1)' })).toBeDisabled()

    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await within(progress).findByText('Lädt …')).toBeInTheDocument()
    const before = inventoryCalls
    updated = true
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    expect(await screen.findByText('Fertig.')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Aktualisierung abgeschlossen' })).toBeInTheDocument()
    await waitFor(() => expect(inventoryCalls).toBeGreaterThan(before))
    expect(await within(region).findByText('5.5.72')).toBeInTheDocument()
    expect(within(region).getByRole('switch', { name: 'LuckPerms aktiv' })).toBeEnabled()
    expect(within(region).queryByRole('button', { name: /Alle aktualisieren/ })).not.toBeInTheDocument()
    const seen = jobs.polls
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS * 4))
    expect(jobs.polls).toBe(seen)
  })

  it('updates a single jar, translates step errors and hides a finished job on request', async () => {
    mockInventory(inventory({ jars: [LP] }))
    server.use(
      http.get('*/api/servers/:id/plugins/updates', () => HttpResponse.json({ updates: [update(LP.file, '5.5.71', '5.5.72')], checked_at: Date.now() })),
    )
    const jobs = jobSequence([job('update', 'failed', [step('LuckPerms', NEW_FILE, 'failed', 'hash_mismatch')])])
    server.use(
      http.post('*/api/servers/:id/plugins/update', async ({ request }) => {
        expect(await request.json()).toEqual({ files: [LP.file] })
        jobs.started = true
        return HttpResponse.json(job('update', 'running', steps('downloading')), { status: 202 })
      }),
    )
    const { user, region } = await openPlugins()
    await within(region).findByText('Update: 5.5.72')
    await user.click(within(region).getByRole('button', { name: 'Aktionen für LuckPerms' }))
    await user.click(screen.getByRole('menuitem', { name: 'Auf 5.5.72 aktualisieren' }))
    expect(await screen.findByRole('region', { name: 'Aktualisierung läuft …' })).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    const progress = await screen.findByRole('region', { name: 'Aktualisierung fehlgeschlagen' })
    expect(within(progress).getByText('Fehler: Prüfsumme stimmt nicht – die Datei wurde verworfen.')).toBeInTheDocument()
    expect(await screen.findByText('Nicht alles hat geklappt – Details stehen beim Auftrag.')).toBeInTheDocument()
    await user.click(within(progress).getByRole('button', { name: 'Ausblenden' }))
    expect(screen.queryByRole('region', { name: 'Aktualisierung fehlgeschlagen' })).not.toBeInTheDocument()
  })

  it('checks for updates on request', async () => {
    mockInventory(inventory({ jars: [LP] }))
    const refreshes: (string | null)[] = []
    server.use(
      http.get('*/api/servers/:id/plugins/updates', ({ request }) => {
        const refresh = new URL(request.url).searchParams.get('refresh')
        refreshes.push(refresh)
        return HttpResponse.json({ updates: refresh === 'true' ? [update(LP.file, '5.5.71', '5.5.72')] : [], checked_at: Date.now() })
      }),
    )
    const { user, region } = await openPlugins()
    await within(region).findByRole('switch', { name: 'LuckPerms aktiv' })
    expect(within(region).queryByRole('button', { name: /Alle aktualisieren/ })).not.toBeInTheDocument()
    await user.click(within(region).getByRole('button', { name: 'Nach Updates suchen' }))
    expect(await within(region).findByText('Update: 5.5.72')).toBeInTheDocument()
    expect(within(region).getByRole('button', { name: 'Alle aktualisieren (1)' })).toBeEnabled()
    expect(await screen.findByText('Updates gefunden: 1')).toBeInTheDocument()
    expect(refreshes).toEqual([null, 'true'])
  })

  it('reports the rate limit of the update check', async () => {
    mockInventory(inventory({ jars: [LP] }))
    server.use(
      http.get('*/api/servers/:id/plugins/updates', ({ request }) =>
        new URL(request.url).searchParams.get('refresh')
          ? HttpResponse.json({ error: 'rate_limited', message: 'slow down' }, { status: 429, headers: { 'Retry-After': '30' } })
          : HttpResponse.json({ updates: [], checked_at: Date.now() }),
      ),
    )
    const { user, region } = await openPlugins()
    await within(region).findByRole('switch', { name: 'LuckPerms aktiv' })
    await user.click(within(region).getByRole('button', { name: 'Nach Updates suchen' }))
    expect(await screen.findByText('Zu viele Versuche. Bitte in 30 s erneut versuchen.')).toBeInTheDocument()
  })

  it('locks installing from the search while a job runs', async () => {
    const jobs = jobSequence([job('install', 'running', [step('Chunky', 'Chunky-1.4.28.jar', 'downloading')])])
    jobs.started = true
    server.use(http.get('*/api/servers/:id/plugins/search', () => HttpResponse.json({ hits: [hit('Chunky')], total: 1, offset: 0 })))
    const { region } = await openDiscover()
    expect(await within(region).findByRole('button', { name: 'Chunky installieren' })).toBeDisabled()
    expect(screen.getByRole('region', { name: 'Installation läuft …' })).toBeInTheDocument()
  })

  it('picks up a job started elsewhere when the panel answers 409 conflict', async () => {
    mockInventory(inventory({ jars: [LP] }))
    const jobs = jobSequence([job('install', 'running', [step('Chunky', 'Chunky-1.4.28.jar', 'downloading')])])
    server.use(
      http.post('*/api/servers/:id/plugins/toggle', () => {
        jobs.started = true // another browser tab started a job meanwhile
        return HttpResponse.json({ error: 'conflict', message: 'a plugin job of this server is running' }, { status: 409 })
      }),
    )
    const { user, region } = await openPlugins()
    await user.click(await within(region).findByRole('switch', { name: 'LuckPerms aktiv' }))
    expect(await screen.findByText('Für diesen Server läuft gerade schon ein Auftrag. Warte, bis er fertig ist.')).toBeInTheDocument()
    expect(await screen.findByRole('region', { name: 'Installation läuft …' })).toBeInTheDocument()
    expect(within(region).getByRole('switch', { name: 'LuckPerms aktiv' })).toBeDisabled()
  })

  it('does not show a job that ended before the tab was opened', async () => {
    const old: PluginJob = { ...job('install', 'done', [step('Chunky', 'Chunky-1.4.28.jar', 'done')]), finished_at: Date.now() - 60_000 }
    const jobs = jobSequence([old])
    jobs.started = true
    const { region } = await openPlugins()
    await within(region).findByRole('switch', { name: 'OldTool aktiv' })
    expect(screen.queryByRole('region', { name: 'Installation abgeschlossen' })).not.toBeInTheDocument()
    expect(screen.queryByText('Fertig.')).not.toBeInTheDocument()
  })
})

describe('plugin jobs seen from outside', () => {
  const CHUNKY = [step('Chunky', 'Chunky-1.4.28.jar', 'downloading')]

  /** Counts inventory reads; GET …/jobs answers the given sequence, one entry per call (the last repeats). */
  function watchInventory(jobs: Array<PluginJob | null>) {
    const counts = { inventory: 0, jobs: 0 }
    server.use(
      http.get('*/api/servers/:id/plugins', () => {
        counts.inventory += 1
        return HttpResponse.json(inventory({ jars: [LP] }))
      }),
      http.get('*/api/servers/:id/plugins/updates', () => HttpResponse.json({ updates: [], checked_at: Date.now() })),
      http.get('*/api/servers/:id/plugins/jobs', () => {
        const next = jobs[Math.min(counts.jobs, jobs.length - 1)]
        counts.jobs += 1
        return HttpResponse.json({ job: next })
      }),
    )
    return counts
  }

  it('reloads the inventory without a success toast when a running job vanishes', async () => {
    const counts = watchInventory([job('install', 'running', CHUNKY), null])
    await openPlugins()
    expect(await screen.findByRole('region', { name: 'Installation läuft …' })).toBeInTheDocument()
    const before = counts.inventory
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    await waitFor(() => expect(counts.inventory).toBeGreaterThan(before))
    expect(screen.queryByRole('region', { name: 'Installation läuft …' })).not.toBeInTheDocument()
    expect(screen.queryAllByText(/^Fertig\./)).toHaveLength(0)
  })

  it('reloads the inventory without a success toast when another job replaced the running one', async () => {
    const other: PluginJob = { ...job('update', 'done', [step('LuckPerms', NEW_FILE, 'done')]), id: 'job-2' }
    const counts = watchInventory([job('install', 'running', CHUNKY), other])
    await openPlugins()
    expect(await screen.findByRole('region', { name: 'Installation läuft …' })).toBeInTheDocument()
    const before = counts.inventory
    await act(() => vi.advanceTimersByTimeAsync(JOB_POLL_MS + 100))
    await waitFor(() => expect(counts.inventory).toBeGreaterThan(before))
    expect(screen.queryAllByText(/^Fertig\./)).toHaveLength(0)
  })

  it('locks the removal dialog when a job starts meanwhile', async () => {
    watchInventory([null, job('install', 'running', CHUNKY)])
    mockInventory(inventory({ jars: [LP] }))
    const { user, region, queryClient } = await openPlugins()
    await user.click(await within(region).findByRole('button', { name: 'Aktionen für LuckPerms' }))
    await user.click(screen.getByRole('menuitem', { name: 'Entfernen' }))
    const dialog = await screen.findByRole('dialog', { name: '„LuckPerms“ entfernen?' })
    expect(within(dialog).getByRole('button', { name: 'Entfernen' })).toBeEnabled()
    act(() => startJob(queryClient, 1, job('install', 'running', CHUNKY)))
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Entfernen' })).toBeDisabled())
  })

  it('shows the panel text for a step error code it does not know', async () => {
    watchInventory([null])
    const { queryClient } = await openPlugins()
    const failed: PluginJobStep = {
      ...step('LuckPerms', NEW_FILE, 'failed'),
      error_code: 'disk_on_fire' as PluginJobStep['error_code'],
      error: 'the disk is on fire',
    }
    act(() => startJob(queryClient, 1, job('update', 'failed', [failed])))
    expect(await screen.findByText('Fehler: the disk is on fire')).toBeInTheDocument()
    expect(screen.queryByText(/plugins\.step\.error/)).not.toBeInTheDocument()
  })
})
