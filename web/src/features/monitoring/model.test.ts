import { describe, expect, it } from 'vitest'
import type { MonitoringSettings, OverviewServer, Server } from '@/api/types'
import {
  alertKey,
  draftChanged,
  draftFromSettings,
  GIB,
  lastValue,
  metricPoints,
  percent,
  reachability,
  sameSettings,
  settingsRequest,
  toChartPoints,
  tpsSourceKey,
  validateSettings,
} from './model'

const settings: MonitoringSettings = {
  disk_min_percent: 10,
  disk_min_bytes: 10 * GIB,
  down_after_min: 3,
  crash_restarts: 3,
  crash_window_min: 10,
  alerts: [],
}

function srv(state: Server['status']['state']): Server {
  return { status: { state } } as Server
}

describe('monitoring model', () => {
  it('turns API points into chart points', () => {
    expect(toChartPoints([[1, 2, 1.5, 3]])).toEqual([{ t: 1, v: 2, min: 1.5, max: 3 }])
    expect(toChartPoints(undefined)).toEqual([])
    expect(metricPoints({ range: '1h', step_ms: 30_000, series: { tps: [[0, 20, 20, 20]] } }, 'tps')).toHaveLength(1)
    expect(metricPoints({ range: '1h', step_ms: 30_000, series: {} }, 'tps')).toEqual([])
    expect(lastValue([])).toBeNull()
    expect(lastValue(toChartPoints([[0, 1, 1, 1], [1, 4, 4, 4]]))).toBe(4)
  })

  it('derives reachability from the state and the ping', () => {
    const up = { up: true } as OverviewServer
    const down = { up: false } as OverviewServer
    expect(reachability(srv('running'), up)).toBe('reachable')
    expect(reachability(srv('running'), down)).toBe('unreachable')
    expect(reachability(srv('running'), { up: null } as OverviewServer)).toBeNull()
    expect(reachability(srv('running'), undefined)).toBeNull()
    expect(reachability(srv('stopped'), down)).toBeNull()
  })

  it('maps alert kinds and TPS sources to translation keys', () => {
    expect(alertKey('server.crash_loop')).toBe('monitoring.alert.server_crash_loop')
    expect(alertKey('host.disk_low')).toBe('monitoring.alert.host_disk_low')
    expect(tpsSourceKey('tick_query')).toBe('monitoring.tps_source.tick_query')
  })

  it('clamps percentages and refuses missing totals', () => {
    expect(percent(5, 10)).toBe(50)
    expect(percent(15, 10)).toBe(100)
    expect(percent(null, 10)).toBeNull()
    expect(percent(5, 0)).toBeNull()
  })

  it('edits the disk threshold in GiB and validates every field', () => {
    const draft = draftFromSettings(settings)
    expect(draft).toEqual({ disk_min_percent: '10', disk_min_gib: '10', down_after_min: '3', crash_restarts: '3', crash_window_min: '10' })
    expect(validateSettings(draft).request).toEqual({ disk_min_percent: 10, disk_min_bytes: 10 * GIB, down_after_min: 3, crash_restarts: 3, crash_window_min: 10 })
    expect(sameSettings(validateSettings(draft).request!, settings)).toBe(true)

    const bad = validateSettings({ ...draft, down_after_min: '0', crash_restarts: '2.5', disk_min_gib: '1,234' })
    expect(bad.request).toBeNull()
    expect(bad.errors.down_after_min).toEqual({ key: 'monitoring.settings.validation.range', params: { min: 1, max: 60 } })
    expect(bad.errors.crash_restarts).toEqual({ key: 'monitoring.settings.validation.integer' })
    expect(bad.errors.disk_min_gib).toEqual({ key: 'monitoring.settings.validation.decimal' })

    const half = validateSettings({ ...draft, disk_min_gib: '1,5' })
    expect(half.request?.disk_min_bytes).toBe(1.5 * GIB)
    expect(validateSettings({ ...draft, disk_min_gib: '10241' }).errors.disk_min_gib?.key).toBe('monitoring.settings.validation.range')
  })

  it('shows odd byte values with two decimals', () => {
    expect(draftFromSettings({ ...settings, disk_min_bytes: 1.25 * GIB }).disk_min_gib).toBe('1.25')
  })

  it('writes GiB with a decimal comma in German', () => {
    expect(draftFromSettings({ ...settings, disk_min_bytes: 1.25 * GIB }, 'de').disk_min_gib).toBe('1,25')
    expect(draftFromSettings({ ...settings, disk_min_bytes: 10_240 * GIB }, 'de').disk_min_gib).toBe('10240')
  })

  it('compares the form as entered, not via recomputed bytes', () => {
    const odd = { ...settings, disk_min_bytes: Math.round(1.333 * GIB) }
    const initial = draftFromSettings(odd, 'de')
    expect(initial.disk_min_gib).toBe('1,33')
    expect(draftChanged(initial, initial)).toBe(false)
    expect(draftChanged({ ...initial, disk_min_gib: '1.330' }, initial)).toBe(false)
    expect(settingsRequest(initial, initial, odd)).toBeNull()
    // Another field changes: the untouched GiB threshold keeps its stored bytes.
    expect(settingsRequest({ ...initial, down_after_min: '5' }, initial, odd)).toEqual({ ...validateSettings(initial).request, down_after_min: 5, disk_min_bytes: odd.disk_min_bytes })
    expect(settingsRequest({ ...initial, disk_min_gib: '2' }, initial, odd)?.disk_min_bytes).toBe(2 * GIB)
  })
})
