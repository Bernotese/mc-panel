import { deflateSync, strToU8, zipSync, type Zippable } from 'fflate'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { MAX_INDEX_BYTES, MrpackReadError, readMrpackIndex } from './mrpack-read'

/** Every chunk the reader's Inflate emitted: its size bounds what one push allocates. */
const emitted: number[] = []

vi.mock('fflate', async (importOriginal) => {
  const orig = await importOriginal<typeof import('fflate')>()
  class Inflate extends orig.Inflate {
    constructor(cb: (chunk: Uint8Array, final: boolean) => void) {
      super((chunk, final) => {
        emitted.push(chunk.byteLength)
        cb(chunk, final)
      })
    }
  }
  return { ...orig, Inflate }
})

beforeEach(() => {
  emitted.length = 0
})

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

async function failure(file: Blob, signal?: AbortSignal): Promise<MrpackReadError> {
  try {
    await readMrpackIndex(file, signal)
  } catch (err) {
    if (err instanceof MrpackReadError) return err
    throw err
  }
  throw new Error('expected a MrpackReadError')
}

const SIG_LOCAL = 0x04034b50
const SIG_CENTRAL = 0x02014b50

/** Changes header fields of every local header (offset relative to the local signature) and central entry (relative to the central signature). */
function patch(bytes: Uint8Array, local: Record<number, number>, central: Record<number, number>): Uint8Array {
  const out = bytes.slice()
  const view = new DataView(out.buffer)
  for (let i = 0; i + 4 <= out.length; i++) {
    const sig = view.getUint32(i, true)
    const fields = sig === SIG_LOCAL ? local : sig === SIG_CENTRAL ? central : undefined
    if (!fields) continue
    for (const [at, value] of Object.entries(fields)) view.setUint32(i + Number(at), value, true)
  }
  return out
}

/** Sets the uncompressed size in every local header and central directory entry (a lying archive). */
function patchSize(bytes: Uint8Array, size: number): Uint8Array {
  return patch(bytes, { 22: size }, { 24: size })
}

function blob(bytes: Uint8Array): Blob {
  return new Blob([bytes as Uint8Array<ArrayBuffer>])
}

/** A Blob that remembers the size of every slice the reader takes (and so reads). */
class SpyBlob extends Blob {
  readonly slices: number[] = []

  slice(start?: number, end?: number, contentType?: string): Blob {
    const part = super.slice(start, end, contentType)
    this.slices.push(part.size)
    return part
  }
}

/** A deflate stream of n zero bytes: tiny on disk, n bytes when inflated (a moderate bomb, built at test time). */
function zeros(n: number): Uint8Array {
  return deflateSync(new Uint8Array(n), { level: 9 })
}

/** Builds a ZIP whose only entry is modrinth.index.json with the given raw deflate data and declared sizes. */
function rawZip(data: Uint8Array, size: number, crc = 0): Uint8Array {
  const name = strToU8('modrinth.index.json')
  const local = new Uint8Array(30 + name.length)
  const lv = new DataView(local.buffer)
  lv.setUint32(0, SIG_LOCAL, true)
  lv.setUint16(4, 20, true)
  lv.setUint16(8, 8, true)
  lv.setUint32(14, crc, true)
  lv.setUint32(18, data.length, true)
  lv.setUint32(22, size, true)
  lv.setUint16(26, name.length, true)
  local.set(name, 30)
  const central = new Uint8Array(46 + name.length)
  const cv = new DataView(central.buffer)
  cv.setUint32(0, SIG_CENTRAL, true)
  cv.setUint16(4, 20, true)
  cv.setUint16(6, 20, true)
  cv.setUint16(10, 8, true)
  cv.setUint32(16, crc, true)
  cv.setUint32(20, data.length, true)
  cv.setUint32(24, size, true)
  cv.setUint16(28, name.length, true)
  central.set(name, 46)
  const eocd = new Uint8Array(22)
  const ev = new DataView(eocd.buffer)
  ev.setUint32(0, 0x06054b50, true)
  ev.setUint16(8, 1, true)
  ev.setUint16(10, 1, true)
  ev.setUint32(12, central.length, true)
  ev.setUint32(16, local.length + data.length, true)
  const out = new Uint8Array(local.length + data.length + central.length + eocd.length)
  out.set(local, 0)
  out.set(data, local.length)
  out.set(central, local.length + data.length)
  out.set(eocd, local.length + data.length + central.length)
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
    const file = pack({ 'modrinth.index.json': strToU8(`\uFEFF${JSON.stringify(INDEX)}`) }, 0)
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

  it('stops inflating when the headers lie about the size', async () => {
    const big = strToU8(`{"pad":"${' '.repeat(MAX_INDEX_BYTES)}"}`)
    const lying = patchSize(zipSync({ 'modrinth.index.json': big }), 100)
    expect((await failure(blob(lying))).code).toBe('too_large')
  })

  it('inflates a bomb in bounded steps and stops right after the limit', async () => {
    const bomb = rawZip(zeros(4 * MAX_INDEX_BYTES), 100)
    expect((await failure(blob(bomb))).code).toBe('too_large')
    // one push of the whole entry would emit all 20 MiB at once; bounded pushes stay far below the limit each
    expect(Math.max(...emitted)).toBeLessThanOrEqual(MAX_INDEX_BYTES)
    expect(emitted.reduce((a, b) => a + b, 0)).toBeLessThan(2 * MAX_INDEX_BYTES)
  })

  it('refuses a compressed entry above the budget before reading it', async () => {
    const ok = zipSync({ 'modrinth.index.json': strToU8(JSON.stringify(INDEX)) })
    // the central directory claims a 1 GiB compressed entry; the file is far smaller
    const file = new SpyBlob([patch(ok, { 18: 1 << 30 }, { 20: 1 << 30 }) as Uint8Array<ArrayBuffer>])
    expect((await failure(file)).code).toBe('too_large')
    expect(Math.max(...file.slices)).toBeLessThan(128 * 1024)
  })

  it('refuses an entry that reaches into the central directory', async () => {
    const ok = zipSync({ 'modrinth.index.json': strToU8(JSON.stringify(INDEX)) })
    const file = new SpyBlob([patch(ok, { 18: 1 << 20 }, { 20: 1 << 20 }) as Uint8Array<ArrayBuffer>])
    expect((await failure(file)).code).toBe('not_zip')
    expect(Math.max(...file.slices)).toBeLessThan(128 * 1024)
  })

  it('refuses local headers that disagree with the central directory', async () => {
    const ok = zipSync({ 'modrinth.index.json': strToU8(JSON.stringify(INDEX)) })
    expect(await failure(blob(patch(ok, { 22: 7 }, {})))).toMatchObject({ code: 'not_zip' })
    expect(await failure(blob(patch(ok, { 18: 7 }, {})))).toMatchObject({ code: 'not_zip' })
    const renamed = ok.slice()
    renamed.set(strToU8('modrinth.index.jsoo'), 30) // the local name of the first (only) entry
    expect(await failure(blob(renamed))).toMatchObject({ code: 'not_zip' })
  })

  it('refuses an index shorter than its declared size', async () => {
    const ok = zipSync({ 'modrinth.index.json': strToU8(JSON.stringify(INDEX)) })
    const size = JSON.stringify(INDEX).length + 10
    expect(await failure(blob(patchSize(ok, size)))).toMatchObject({ code: 'not_zip' })
  })

  it('refuses an index whose CRC-32 does not match', async () => {
    for (const level of [0, 6] as const) {
      const ok = zipSync({ 'modrinth.index.json': strToU8(JSON.stringify(INDEX)) }, { level })
      const view = new DataView(ok.buffer)
      const crc = view.getUint32(14, true) ^ 1 // the local header of the first (only) entry
      expect(await failure(blob(patch(ok, { 14: crc }, { 16: crc })))).toMatchObject({ code: 'not_zip' })
    }
  })

  it('refuses a stored entry whose sizes differ', async () => {
    const ok = zipSync({ 'modrinth.index.json': strToU8(JSON.stringify(INDEX)) }, { level: 0 })
    expect(await failure(blob(patch(ok, { 22: 9 }, { 24: 9 })))).toMatchObject({ code: 'not_zip' })
  })

  it('stops when the signal aborts', async () => {
    const file = pack({ 'modrinth.index.json': strToU8(JSON.stringify(INDEX)) })
    const before = new AbortController()
    before.abort()
    await expect(readMrpackIndex(file, before.signal)).rejects.toMatchObject({ name: 'AbortError' })
    const during = new AbortController()
    const read = readMrpackIndex(blob(rawZip(zeros(4 * MAX_INDEX_BYTES), 100)), during.signal)
    during.abort()
    await expect(read).rejects.toMatchObject({ name: 'AbortError' })
  })
})
