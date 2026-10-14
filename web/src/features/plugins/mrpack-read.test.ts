import { strToU8, zipSync, type Zippable } from 'fflate'
import { describe, expect, it } from 'vitest'
import { MAX_INDEX_BYTES, MrpackReadError, readMrpackIndex } from './mrpack-read'

const INDEX = {
  formatVersion: 1,
  game: 'minecraft',
  versionId: '1.0.0',
  name: 'Test Pack',
  files: [{ path: 'mods/lithium.jar', hashes: { sha512: 'a'.repeat(128) }, downloads: [], fileSize: 1 }],
  dependencies: { minecraft: '1.21.1', 'fabric-loader': '0.16.9' },
}

function pack(files: Zippable, level: 0 | 6 = 6): Blob {
  const bytes = zipSync(files, { level })
  return new Blob([bytes as Uint8Array<ArrayBuffer>])
}

async function failure(file: Blob): Promise<MrpackReadError> {
  try {
    await readMrpackIndex(file)
  } catch (err) {
    if (err instanceof MrpackReadError) return err
    throw err
  }
  throw new Error('expected a MrpackReadError')
}

/** Sets the uncompressed size of every central directory entry (a lying archive). */
function patchDirectorySize(bytes: Uint8Array, size: number): Uint8Array {
  const out = bytes.slice()
  const view = new DataView(out.buffer)
  for (let i = 0; i + 46 <= out.length; i++) {
    if (view.getUint32(i, true) === 0x02014b50) view.setUint32(i + 24, size, true)
  }
  return out
}

describe('readMrpackIndex', () => {
  it('reads the deflated index next to other entries', async () => {
    const file = pack({
      'modrinth.index.json': strToU8(JSON.stringify(INDEX)),
      'overrides/config/sodium.json': strToU8('{}'),
      'overrides/mods/extra.jar': new Uint8Array(4096),
    })
    expect(await readMrpackIndex(file)).toEqual(INDEX)
  })

  it('reads a stored index and ignores a byte order mark', async () => {
    const file = pack({ 'modrinth.index.json': strToU8(`﻿${JSON.stringify(INDEX)}`) }, 0)
    expect(await readMrpackIndex(file)).toEqual(INDEX)
  })

  it('refuses files that are no ZIP', async () => {
    expect((await failure(new Blob(['not a zip at all, just some text that is long enough']))).code).toBe('not_zip')
    expect((await failure(new Blob(['PK']))).code).toBe('not_zip')
  })

  it('refuses a ZIP without the index at the top level', async () => {
    const file = pack({ 'pack/modrinth.index.json': strToU8(JSON.stringify(INDEX)), 'readme.txt': strToU8('hi') })
    expect((await failure(file)).code).toBe('no_index')
  })

  it('refuses an index that is not a JSON object', async () => {
    expect((await failure(pack({ 'modrinth.index.json': strToU8('{"name": ') }))).code).toBe('invalid_json')
    expect((await failure(pack({ 'modrinth.index.json': strToU8('[1, 2]') }))).code).toBe('invalid_json')
    expect((await failure(pack({ 'modrinth.index.json': new Uint8Array([0x7b, 0xff, 0x7d]) }))).code).toBe('invalid_json')
  })

  it('refuses an index above 5 MiB before inflating it', async () => {
    const big = strToU8(`{"pad":"${' '.repeat(MAX_INDEX_BYTES)}"}`)
    expect((await failure(pack({ 'modrinth.index.json': big }))).code).toBe('too_large')
  })

  it('stops inflating when the directory lies about the size', async () => {
    const big = strToU8(`{"pad":"${' '.repeat(MAX_INDEX_BYTES)}"}`)
    const lying = patchDirectorySize(zipSync({ 'modrinth.index.json': big }), 100)
    expect((await failure(new Blob([lying as Uint8Array<ArrayBuffer>]))).code).toBe('too_large')
  })
})
