import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { LIVE_POLL_MS } from './api'
import { liveMetrics, mockLive, mockMetrics } from './test-utils'

async function openMonitoring(serverId = 1, username = 'admin') {
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  const rendered = await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs(username, { sudo: true }) })
  await user.click(await screen.findByRole('tab', { name: 'Monitoring' }))
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

    server.use(http.get('*/api/servers/:id/metrics/live', () => HttpResponse.json({ error: 'agent_unavailable', message: 'no agent' }, { status: 503 })))
    await act(() => vi.advanceTimersByTimeAsync(LIVE_POLL_MS + 100))
    expect(await screen.findByText('Der Agent ist gerade nicht erreichbar; es gibt keine aktuellen Werte.')).toBeInTheDocument()
  })

  it('polls the live values every 5 s only while the tab is open', async () => {
    const live = mockLive((n) => liveMetrics({ players: n }))
    mockMetrics()
    const { user } = await openMonitoring()
    await screen.findByText('1/20')
    await act(() => vi.advanceTimersByTimeAsync(LIVE_POLL_MS + 100))
    expect(await screen.findByText('2/20')).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Übersicht' }))
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
})
