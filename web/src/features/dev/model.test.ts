import { describe, expect, it } from 'vitest'
import type { DevInstance, DevSummary, InstanceConfig } from '@/api/types'
import i18n from '@/i18n'
import {
  canHaveDev,
  defaultDevMemoryMiB,
  devSettingsPatch,
  draftFromDev,
  formatSyncWhen,
  serverTreeEntries,
  validateDevSettings,
  withDevsAfterSources,
} from './model'

function summary(sourceId: number): DevSummary {
  return { source_id: sourceId, source_name: `s${sourceId}`, sync_paused: false, next_sync_at: null, last_sync_at: null, last_sync_status: null, sync_pending: false, sleeping: true }
}

const DEV: DevInstance = {
  ...summary(1),
  server_id: 5,
  paused_at: null,
  sync_time: 300,
  idle_minutes: 15,
  view_distance: 4,
  simulation_distance: 3,
  last_sync_error: null,
}

describe('serverTreeEntries', () => {
  it('nests a dev under its prod and keeps the order otherwise', () => {
    const servers = [{ id: 1 }, { id: 2 }, { id: 5, dev: summary(1) }, { id: 6, dev: summary(9) }]
    expect(serverTreeEntries(servers).map((e) => [e.server.id, e.dev?.id ?? null])).toEqual([
      [1, 5],
      [2, null],
      [6, null], // its prod is not visible: stays at the top level
    ])
    expect(withDevsAfterSources(servers).map((s) => s.id)).toEqual([1, 5, 2, 6])
  })
})

describe('dev rules', () => {
  it('limits dev names to 32 characters', () => {
    expect(canHaveDev('a'.repeat(28))).toBe(true)
    expect(canHaveDev('a'.repeat(29))).toBe(false)
  })
  it('suggests 2 GiB, 3 GiB with a loader or modpack, at most the host limit', () => {
    const inst = (type: InstanceConfig['type']) => ({ instance: { type } as InstanceConfig })
    expect(defaultDevMemoryMiB(inst('paper'), 20480)).toBe(2048)
    expect(defaultDevMemoryMiB(inst('fabric'), 20480)).toBe(3072)
    expect(defaultDevMemoryMiB(inst('modrinth'), 20480)).toBe(3072)
    expect(defaultDevMemoryMiB(inst('fabric'), 1536)).toBe(1536)
  })
})

describe('formatSyncWhen', () => {
  it('names today and tomorrow, else the date', () => {
    const now = new Date(2026, 9, 27, 14, 0).getTime()
    expect(formatSyncWhen(new Date(2026, 9, 27, 18, 30).getTime(), 'de', now)).toBe('heute 18:30')
    expect(formatSyncWhen(new Date(2026, 9, 28, 5, 0).getTime(), 'de', now)).toBe('morgen 05:00')
    expect(formatSyncWhen(new Date(2026, 9, 26, 5, 3).getTime(), 'de', now)).toBe('gestern 05:03')
    expect(formatSyncWhen(new Date(2026, 9, 28, 5, 0).getTime(), 'en', now)).toMatch(/^tomorrow /)
    expect(formatSyncWhen(new Date(2026, 9, 30, 5, 0).getTime(), 'de', now)).not.toMatch(/morgen/)
  })
})

describe('dev settings form', () => {
  it('sends only changed fields', () => {
    const draft = draftFromDev(DEV)
    expect(draft).toEqual({ time: '05:00', idle: '15', view: '4', simulation: '3' })
    expect(devSettingsPatch(draft, DEV)).toEqual({})
    expect(devSettingsPatch({ ...draft, time: '04:30', view: '8' }, DEV)).toEqual({ sync_time: 270, view_distance: 8 })
  })
  it('validates ranges and the time', () => {
    const errors = validateDevSettings({ time: '25:00', idle: '4', view: '33', simulation: '3' })
    expect(Object.keys(errors).sort()).toEqual(['idle', 'time', 'view'])
    for (const e of Object.values(errors)) expect(i18n.exists(e.key), e.key).toBe(true)
  })
})
