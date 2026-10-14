import { describe, expect, it } from 'vitest'
import type { MrpackPlan, MrpackPlanEntry } from '@/api/types'
import { excludeList, importCount, MAX_BODY_BYTES, toRequestIndex, versionSwitch } from './mrpack-model'
import { MrpackReadError } from './mrpack-read'

function entry(sha: string, action: MrpackPlanEntry['action'], optional = false): MrpackPlanEntry {
  return { file: `${sha}.jar`, sha512: sha, action, reason: action === 'skip' ? 'client_only' : null, optional, project: null, version: null, replaces: null }
}

function plan(over: Partial<MrpackPlan> = {}): MrpackPlan {
  return {
    pack: { name: 'P', version_id: '1' },
    game_version: { pack: '1.21.11', server: '1.21.11' },
    loader: { name: 'fabric', pack: '0.19.5', server: null },
    version_mismatch: false,
    entries: [entry('a', 'install'), entry('b', 'install', true), entry('c', 'update', true), entry('d', 'unchanged', true), entry('e', 'skip', true)],
    kept: [],
    ignored_files: 0,
    ...over,
  }
}

describe('toRequestIndex', () => {
  it('drops downloads and fileSize and keeps everything else', () => {
    const raw = {
      formatVersion: 1,
      extra: { kept: true },
      files: [{ path: 'mods/a.jar', hashes: { sha512: 'x' }, env: { server: 'optional' }, downloads: ['u'], fileSize: 3 }, 'odd'],
    }
    expect(toRequestIndex(raw)).toEqual({
      formatVersion: 1,
      extra: { kept: true },
      files: [{ path: 'mods/a.jar', hashes: { sha512: 'x' }, env: { server: 'optional' } }, 'odd'],
    })
  })

  it('refuses an index that is still above the body limit', () => {
    const files = Array.from({ length: 2000 }, (_, i) => ({ path: `mods/${i}.jar`, hashes: { sha512: 'f'.repeat(128) }, note: 'x'.repeat(MAX_BODY_BYTES / 2000) }))
    expect(() => toRequestIndex({ files })).toThrow(MrpackReadError)
  })
})

describe('selection', () => {
  it('excludes only deselected optional install and update entries', () => {
    const p = plan()
    const deselected = new Set(['a', 'b', 'c', 'd', 'e'])
    expect(excludeList(p, deselected)).toEqual(['b', 'c'])
    expect(importCount(p, deselected)).toBe(1)
    expect(importCount(p, new Set())).toBe(3)
  })
})

describe('versionSwitch', () => {
  it('sets the pack version with its Java and pins the loader only where the server pins another one', () => {
    expect(versionSwitch(plan({ game_version: { pack: '26.3', server: '1.21.11' } }), 'java25')).toEqual({ version: '26.3', java: 'java25' })
    expect(versionSwitch(plan({ loader: { name: 'fabric', pack: '0.19.5', server: '0.19.4' } }), 'java21')).toEqual({ loader_version: '0.19.5' })
    expect(
      versionSwitch(plan({ game_version: { pack: '26.3', server: '1.21.11' }, loader: { name: 'fabric', pack: '0.19.5', server: '0.19.4' } }), 'java25'),
    ).toEqual({ version: '26.3', java: 'java25', loader_version: '0.19.5' })
  })
})
