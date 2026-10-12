import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { UpdateBackupScheduleRequest } from '@/api/types'
import { server } from '@/mocks/node'
import { mockOverview, openBackups, overview, schedule } from './test-utils'

async function openSchedule(o = overview()) {
  mockOverview(o)
  const opened = await openBackups()
  const card = await screen.findByRole('region', { name: 'Zeitplan' })
  return { ...opened, card }
}

describe('schedule card', () => {
  it('shows the fields of the chosen mode, the time zone and the next run', async () => {
    const { user, card } = await openSchedule()
    const mode = within(card).getByRole('combobox', { name: 'Zeitplan' })
    expect(mode).toHaveValue('daily')
    expect(within(card).getByLabelText('Uhrzeit')).toHaveValue('04:00')
    expect(within(card).queryByLabelText('Wochentag')).not.toBeInTheDocument()
    expect(within(card).queryByLabelText('Intervall in Stunden')).not.toBeInTheDocument()
    expect(within(card).getByText(/Zeitzone des Panels: Europe\/Berlin/)).toBeInTheDocument()
    expect(within(card).getByText(/^Nächster Lauf: /)).toBeInTheDocument()
    expect(within(card).getByText(/^Letzter geplanter Lauf: .* – erfolgreich$/)).toBeInTheDocument()

    await user.selectOptions(mode, 'weekly')
    const weekday = within(card).getByRole('combobox', { name: 'Wochentag' })
    expect(within(weekday).getAllByRole('option').map((o) => o.textContent)).toEqual([
      'Montag',
      'Dienstag',
      'Mittwoch',
      'Donnerstag',
      'Freitag',
      'Samstag',
      'Sonntag',
    ])
    await user.selectOptions(mode, 'interval')
    // type="number": jest-dom reports the value as a number.
    expect(within(card).getByLabelText('Intervall in Stunden')).toHaveValue(24)
    expect(within(card).queryByLabelText('Uhrzeit')).not.toBeInTheDocument()
  })

  it('saves a weekly schedule and keeps the hidden interval', async () => {
    let body: UpdateBackupScheduleRequest | null = null
    server.use(
      http.put('*/api/servers/:id/backups/schedule', async ({ request }) => {
        body = (await request.json()) as UpdateBackupScheduleRequest
        return HttpResponse.json({ schedule: schedule({ ...body, next_run_at: Date.now() + 3_600_000 }) })
      }),
    )
    const { user, card } = await openSchedule()
    const save = within(card).getByRole('button', { name: 'Zeitplan speichern' })
    expect(save).toBeDisabled()
    await user.selectOptions(within(card).getByRole('combobox', { name: 'Zeitplan' }), 'weekly')
    await user.selectOptions(within(card).getByRole('combobox', { name: 'Wochentag' }), 'Samstag')
    // jsdom sanitizes partial values of <input type="time">; set the complete value at once.
    fireEvent.change(within(card).getByLabelText('Uhrzeit'), { target: { value: '03:15' } })
    await user.clear(within(card).getByLabelText('Monate behalten'))
    await user.type(within(card).getByLabelText('Monate behalten'), '12')
    await user.click(save)
    await waitFor(() =>
      expect(body).toEqual({
        mode: 'weekly',
        interval_hours: 24,
        time_of_day: 195,
        weekday: 6,
        keep_last: 24,
        keep_daily: 7,
        keep_weekly: 4,
        keep_monthly: 12,
      }),
    )
    expect(await screen.findByText('Zeitplan gespeichert.')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Zeitplan speichern' })).toBeDisabled()
  })

  it('blocks saving invalid values and explains why', async () => {
    const { user, card } = await openSchedule()
    await user.selectOptions(within(card).getByRole('combobox', { name: 'Zeitplan' }), 'interval')
    const interval = within(card).getByLabelText('Intervall in Stunden')
    await user.clear(interval)
    await user.type(interval, '200')
    expect(within(card).getByText('Erlaubt sind ganze Zahlen von 1 bis 168.')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Zeitplan speichern' })).toBeDisabled()
    await user.clear(interval)
    await user.type(interval, '6')
    for (const label of ['Letzte behalten', 'Tage behalten', 'Wochen behalten', 'Monate behalten']) {
      await user.clear(within(card).getByLabelText(label))
      await user.type(within(card).getByLabelText(label), '0')
    }
    expect(within(card).getByText('Mindestens ein Aufbewahrungswert muss größer als 0 sein.')).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Zeitplan speichern' })).toBeDisabled()
    await user.selectOptions(within(card).getByRole('combobox', { name: 'Zeitplan' }), 'off')
    expect(within(card).getByRole('button', { name: 'Zeitplan speichern' })).toBeEnabled()
  })

  it('shows the error of the last failed run and no next run while off', async () => {
    const { card } = await openSchedule(
      overview({ schedule: schedule({ mode: 'off', next_run_at: null, last_status: 'failed', last_error: 'RCON nicht erreichbar' }) }),
    )
    expect(within(card).getByText(/– fehlgeschlagen$/)).toBeInTheDocument()
    expect(within(card).getByText('RCON nicht erreichbar')).toBeInTheDocument()
    expect(within(card).queryByText(/^Nächster Lauf/)).not.toBeInTheDocument()
  })
})
