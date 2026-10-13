import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { GIB } from '@/features/monitoring/model'
import { mockMonitoringSettings, monitoringSettings } from '@/features/monitoring/test-utils'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { SettingsPage } from './settings-page'

async function openCard() {
  const user = userEvent.setup()
  await renderWithProviders(<SettingsPage />, { me: loginAs('admin', { sudo: true }) })
  const card = await screen.findByRole('region', { name: 'Monitoring & Alerts' })
  return { user, card }
}

describe('MonitoringCard', () => {
  it('shows the thresholds and the firing alerts', async () => {
    mockMonitoringSettings(
      monitoringSettings({ alerts: [{ kind: 'server.down', server_id: 4, server_name: 'modded', since: Date.now() - 5 * 60_000, detail: 'Container läuft nicht' }] }),
    )
    const { card } = await openCard()
    expect(await within(card).findByLabelText('Server down nach (Minuten)')).toHaveValue('3')
    expect(within(card).getByLabelText('oder unter (GiB)')).toHaveValue('10')
    const firing = within(card).getByRole('list', { name: 'Aktive Alerts' })
    expect(firing).toHaveTextContent('Server down – modded')
    expect(firing).toHaveTextContent('Container läuft nicht')
  })

  it('saves changed thresholds in bytes', async () => {
    mockMonitoringSettings(monitoringSettings())
    let body: unknown = null
    server.use(
      http.put('*/api/monitoring/settings', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(monitoringSettings({ down_after_min: 5, disk_min_bytes: 20 * GIB }))
      }),
    )
    const { user, card } = await openCard()
    const save = await within(card).findByRole('button', { name: 'Einstellungen speichern' })
    expect(save).toBeDisabled()
    await user.clear(within(card).getByLabelText('Server down nach (Minuten)'))
    await user.type(within(card).getByLabelText('Server down nach (Minuten)'), '5')
    await user.clear(within(card).getByLabelText('oder unter (GiB)'))
    await user.type(within(card).getByLabelText('oder unter (GiB)'), '20')
    await user.click(save)
    expect(await screen.findByText('Monitoring-Einstellungen gespeichert.')).toBeInTheDocument()
    expect(body).toEqual({ disk_min_percent: 10, disk_min_bytes: 20 * GIB, down_after_min: 5, crash_restarts: 3, crash_window_min: 10 })
  })

  it('refuses values outside the limits without a request', async () => {
    mockMonitoringSettings(monitoringSettings())
    let calls = 0
    server.use(
      http.put('*/api/monitoring/settings', () => {
        calls += 1
        return HttpResponse.json(monitoringSettings())
      }),
    )
    const { user, card } = await openCard()
    const field = await within(card).findByLabelText('Crash-Loop ab Neustarts')
    await user.clear(field)
    await user.type(field, '1')
    expect(within(card).getByText('Erlaubt sind 2 bis 20.')).toBeInTheDocument()
    expect(field).toHaveAttribute('aria-invalid', 'true')
    expect(within(card).getByRole('button', { name: 'Einstellungen speichern' })).toBeDisabled()
    expect(calls).toBe(0)
  })

  it('warns when no notification channel is active', async () => {
    mockMonitoringSettings(monitoringSettings())
    server.use(http.get('*/api/security/notifiers', () => HttpResponse.json([])))
    const { card } = await openCard()
    expect(await within(card).findByText('Es ist keine aktive Benachrichtigung eingerichtet – Alerts werden erkannt, aber nicht verschickt.')).toBeInTheDocument()
    expect(within(card).getByRole('link', { name: 'Benachrichtigungen öffnen' })).toHaveAttribute('href', '/security')
  })

  it('does not count an odd stored GiB value as a change', async () => {
    const stored = Math.round(1.333 * GIB)
    mockMonitoringSettings(monitoringSettings({ disk_min_bytes: stored }))
    let body: unknown = null
    server.use(
      http.put('*/api/monitoring/settings', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json(monitoringSettings({ disk_min_bytes: stored, down_after_min: 5 }))
      }),
    )
    const { user, card } = await openCard()
    expect(await within(card).findByLabelText('oder unter (GiB)')).toHaveValue('1,33')
    const save = within(card).getByRole('button', { name: 'Einstellungen speichern' })
    expect(save).toBeDisabled()
    await user.clear(within(card).getByLabelText('Server down nach (Minuten)'))
    await user.type(within(card).getByLabelText('Server down nach (Minuten)'), '5')
    await user.click(save)
    // The toast of the previous test may still be on screen: wait for the request instead.
    await waitFor(() => expect(body).toMatchObject({ disk_min_bytes: stored, down_after_min: 5 }))
  })

  it('writes the limits in the error in the locale notation', async () => {
    mockMonitoringSettings(monitoringSettings())
    const { user, card } = await openCard()
    const field = await within(card).findByLabelText('oder unter (GiB)')
    await user.clear(field)
    await user.type(field, '20000')
    expect(within(card).getByText('Erlaubt sind 1 bis 10.240.')).toBeInTheDocument()
  })
})
