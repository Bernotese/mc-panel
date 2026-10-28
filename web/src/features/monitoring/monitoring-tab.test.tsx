import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MetricsRange } from '@/api/types'
import { renderSection } from '@/test/render-section'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { LIVE_POLL_MS, metricsPollMs } from './api'
import { MonitoringTab } from './monitoring-tab'
import { liveMetrics, metricsSeries, mockLive, mockMetrics } from './test-utils'

async function openMonitoring(serverId = 1, username = 'admin') {
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  const rendered = await renderSection(serverId, (s) => <MonitoringTab server={s} />, { me: loginAs(username, { sudo: true }) })
  return { user, ...rendered }
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => {
  vi.useRealTimers()
})

describe('MonitoringTab', () => {
  it('shows the live values with player names and the TPS source', async () => {
    mockLive(liveMetrics())
    mockMetrics()
    await openMonitoring()
    const now = await screen.findByRole('region', { name: 'Jetzt' })
    expect(await within(now).findByText('2/20')).toBeInTheDocument()
    expect(within(now).getByText('19,9')).toBeInTheDocument()
    expect(within(now).getByText('12,3 ms')).toBeInTheDocument()
    expect(within(now).getByText('31,5 %')).toBeInTheDocument()
    expect(within(now).getByText('3 / 5 GiB')).toBeInTheDocument()
    expect(within(now).getByRole('list', { name: 'Online-Spieler' })).toHaveTextContent('AlexSteve')
    expect(within(now).getByText('Quelle: tick query')).toBeInTheDocument()
  })

  it('says when TPS is not available and when nobody is online', async () => {
    mockLive(liveMetrics({ tps: null, mspt: null, tps_source: 'none', players: 0, player_names: [] }))
    mockMetrics()
    await openMonitoring()
    const now = await screen.findByRole('region', { name: 'Jetzt' })
    expect(await within(now).findByText('TPS für diese Version nicht verfügbar')).toBeInTheDocument()
    expect(within(now).getByText('Niemand online.')).toBeInTheDocument()
  })

  it('reports an unreachable server and a missing agent', async () => {
    mockLive(liveMetrics({ up: false, players: null, tps: null }))
    mockMetrics()
    await openMonitoring()
    expect(await screen.findByText('Der Server antwortet nicht auf Anfragen (startet er noch?).')).toBeInTheDocument()

    // A failed refresh keeps the last values but no longer claims a current verdict (issue #155).
    server.use(http.get('*/api/servers/:id/metrics/live', () => HttpResponse.json({ error: 'agent_unavailable', message: 'no agent' }, { status: 503 })))
    await act(() => vi.advanceTimersByTimeAsync(LIVE_POLL_MS + 100))
    expect(await screen.findByTestId('stale-notice')).toHaveTextContent('Der Agent ist nicht erreichbar.')
    expect(screen.queryByText('Der Server antwortet nicht auf Anfragen (startet er noch?).')).not.toBeInTheDocument()
  })

  it('reports a missing agent when the live values never loaded', async () => {
    server.use(http.get('*/api/servers/:id/metrics/live', () => HttpResponse.json({ error: 'agent_unavailable', message: 'no agent' }, { status: 503 })))
    mockMetrics()
    await openMonitoring()
    expect(await screen.findByText('Der Agent ist gerade nicht erreichbar; es gibt keine aktuellen Werte.')).toBeInTheDocument()
  })

  it('keeps live values and charts when their refresh fails, marks them stale once and recovers', async () => {
    mockLive(liveMetrics())
    mockMetrics()
    const outage = { broken: false }
    server.use(
      http.get('*/api/servers/:id/metrics/live', () => (outage.broken ? HttpResponse.error() : undefined)),
      http.get('*/api/servers/:id/metrics', () => (outage.broken ? HttpResponse.error() : undefined)),
    )
    const { user } = await openMonitoring()
    const now = await screen.findByRole('region', { name: 'Jetzt' })
    expect(await within(now).findByText('19,9')).toBeInTheDocument()
    expect(await screen.findByRole('img', { name: 'Spieler' })).toBeInTheDocument()

    outage.broken = true
    await act(() => vi.advanceTimersByTimeAsync(metricsPollMs('1h') + 100))
    const notice = await screen.findByTestId('stale-notice')
    expect(notice).toHaveTextContent('Keine Verbindung zum Panel.')
    expect(within(now).getByText('19,9').closest('[data-stale]')).not.toBeNull()
    expect(within(now).getByText('3 / 5 GiB').closest('[data-stale]')).not.toBeNull()
    const charts = screen.getByRole('img', { name: 'Spieler' }).closest('[data-stale]')
    expect(charts).not.toBeNull()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()

    outage.broken = false
    await user.click(within(notice).getByRole('button', { name: 'Erneut versuchen' }))
    await waitFor(() => expect(screen.queryByTestId('stale-notice')).not.toBeInTheDocument())
    expect(within(now).getByText('19,9').closest('[data-stale]')).toBeNull()
    expect(screen.getByRole('img', { name: 'Spieler' }).closest('[data-stale]')).toBeNull()
  })

  it('shows a dash for missing live values', async () => {
    mockLive(liveMetrics({ tps: null, mspt: null }))
    mockMetrics()
    await openMonitoring()
    const now = await screen.findByRole('region', { name: 'Jetzt' })
    expect(await within(now).findAllByText('–')).toHaveLength(2)
  })

  it('polls the live values every 5 s only while the tab is open', async () => {
    const live = mockLive((n) => liveMetrics({ players: n }))
    mockMetrics()
    const { unmount } = await openMonitoring()
    await screen.findByText('1/20')
    await act(() => vi.advanceTimersByTimeAsync(LIVE_POLL_MS + 100))
    expect(await screen.findByText('2/20')).toBeInTheDocument()
    unmount()
    const calls = live.calls
    await act(() => vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 3))
    expect(live.calls).toBe(calls)
  })

  it('draws four charts and switches the period', async () => {
    mockLive(liveMetrics())
    const ranges = mockMetrics()
    const { user } = await openMonitoring()
    for (const name of ['Spieler', 'TPS und MSPT', 'CPU-Auslastung', 'Arbeitsspeicher']) {
      expect(await screen.findByRole('img', { name })).toBeInTheDocument()
    }
    expect(screen.getByRole('button', { name: '1 Stunde' })).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByRole('button', { name: '7 Tage' }))
    expect(screen.getByRole('button', { name: '7 Tage' })).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(ranges).toContain('7d'))
    expect(ranges.at(-1)).toBe('7d')
  })

  it('lets admins switch the down alerts of the server', async () => {
    mockLive(liveMetrics())
    mockMetrics()
    let body: unknown = null
    server.use(
      http.put('*/api/servers/:id/alerts', async ({ request }) => {
        body = await request.json()
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = await openMonitoring()
    const toggle = await screen.findByRole('switch', { name: 'Down-Alerts für diesen Server' })
    expect(toggle).toBeChecked()
    await user.click(toggle)
    expect(await screen.findByText('Down-Alerts ausgeschaltet.')).toBeInTheDocument()
    expect(body).toEqual({ down_alerts: false })
  })

  it('shows other users the tab without the alert switch', async () => {
    mockLive(liveMetrics())
    mockMetrics()
    await openMonitoring(1, 'alice')
    await screen.findByRole('region', { name: 'Jetzt' })
    expect(screen.queryByRole('switch', { name: 'Down-Alerts für diesen Server' })).not.toBeInTheDocument()
  })

  it('says "stopped" instead of a TPS source for a stopped server', async () => {
    mockLive(liveMetrics({ up: null, players: null, player_names: [], tps: null, mspt: null, cpu_percent: null, memory_bytes: null, memory_limit_bytes: null, tps_source: 'none' }))
    mockMetrics()
    await openMonitoring(2)
    const now = await screen.findByRole('region', { name: 'Jetzt' })
    expect(await within(now).findByText('Server gestoppt')).toBeInTheDocument()
    expect(within(now).queryByText(/Quelle|nicht verfügbar|ermittelt/)).not.toBeInTheDocument()
  })

  it('tells in the TPS chart that this version has no TPS', async () => {
    mockLive(liveMetrics({ tps: null, mspt: null, tps_source: 'none' }))
    server.use(
      http.get('*/api/servers/:id/metrics', ({ request }) => {
        const range = (new URL(request.url).searchParams.get('range') ?? '1h') as MetricsRange
        return HttpResponse.json(metricsSeries(range, ['up', 'players', 'players_max', 'cpu_percent', 'memory_bytes', 'memory_limit_bytes']))
      }),
    )
    await openMonitoring()
    await screen.findByRole('img', { name: 'Spieler' })
    const figure = screen.getByText('TPS und MSPT').closest('figure')
    expect(figure).not.toBeNull()
    expect(await within(figure as HTMLElement).findByText('TPS für diese Version nicht verfügbar')).toBeInTheDocument()
  })
})
