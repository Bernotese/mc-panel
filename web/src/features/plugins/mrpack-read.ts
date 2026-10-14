import { Inflate } from 'fflate'

/** Spec §5: the index of a pack is usually < 500 KiB; anything above this is refused before and while inflating. */
export const MAX_INDEX_BYTES = 5 * 1024 * 1024
/** The central directory is read in one piece; 32 MiB covers several hundred thousand entries. */
const MAX_DIRECTORY_BYTES = 32 * 1024 * 1024
const INDEX_NAME = 'modrinth.index.json'

const SIG_EOCD = 0x06054b50
const SIG_CENTRAL = 0x02014b50
const SIG_LOCAL = 0x04034b50
/** End of central directory record without comment, plus the longest comment. */
const EOCD_MIN = 22
const EOCD_SEARCH = EOCD_MIN + 0xffff

export type MrpackReadErrorCode = 'not_zip' | 'unsupported' | 'no_index' | 'too_large' | 'invalid_json'

export class MrpackReadError extends Error {
  readonly code: MrpackReadErrorCode

  constructor(code: MrpackReadErrorCode, message: string) {
    super(message)
    this.name = 'MrpackReadError'
    this.code = code
  }
}

/**
 * Reads only `modrinth.index.json` from a .mrpack (spec §2, §5): the central directory at the end of the file names
 * the entry, and only its bytes are read and inflated – a pack with hundreds of MB of overrides is never loaded whole,
 * and no other entry is ever decompressed. Returns the parsed JSON unchecked; the panel validates it (spec §4.1).
 */
export async function readMrpackIndex(file: Blob): Promise<unknown> {
  const entry = await findIndexEntry(file)
  if (entry.size > MAX_INDEX_BYTES) throw new MrpackReadError('too_large', `${INDEX_NAME} is larger than 5 MiB`)
  const local = new DataView(await readSlice(file, entry.offset, entry.offset + 30))
  if (local.byteLength < 30 || local.getUint32(0, true) !== SIG_LOCAL) throw new MrpackReadError('not_zip', 'broken local header')
  const start = entry.offset + 30 + local.getUint16(26, true) + local.getUint16(28, true)
  const raw = new Uint8Array(await readSlice(file, start, start + entry.compressedSize))
  if (raw.byteLength !== entry.compressedSize) throw new MrpackReadError('not_zip', 'truncated entry')
  const bytes = entry.method === 0 ? raw : inflateLimited(raw)
  if (bytes.byteLength > MAX_INDEX_BYTES) throw new MrpackReadError('too_large', `${INDEX_NAME} is larger than 5 MiB`)
  let text: string
  try {
    text = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    throw new MrpackReadError('invalid_json', `${INDEX_NAME} is not UTF-8`)
  }
  let value: unknown
  try {
    value = JSON.parse(text.replace(/^﻿/, ''))
  } catch {
    throw new MrpackReadError('invalid_json', `${INDEX_NAME} is not valid JSON`)
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new MrpackReadError('invalid_json', `${INDEX_NAME} is not a JSON object`)
  }
  return value
}

interface IndexEntry {
  method: number
  compressedSize: number
  size: number
  offset: number
}

async function findIndexEntry(file: Blob): Promise<IndexEntry> {
  if (file.size < EOCD_MIN) throw new MrpackReadError('not_zip', 'too small for a ZIP')
  const tailStart = Math.max(0, file.size - EOCD_SEARCH)
  const tail = new DataView(await readSlice(file, tailStart, file.size))
  let eocd = -1
  for (let i = tail.byteLength - EOCD_MIN; i >= 0; i--) {
    if (tail.getUint32(i, true) === SIG_EOCD) {
      eocd = i
      break
    }
  }
  if (eocd < 0) throw new MrpackReadError('not_zip', 'no end of central directory')
  const count = tail.getUint16(eocd + 10, true)
  const dirSize = tail.getUint32(eocd + 12, true)
  const dirOffset = tail.getUint32(eocd + 16, true)
  if (count === 0xffff || dirSize === 0xffffffff || dirOffset === 0xffffffff) {
    throw new MrpackReadError('unsupported', 'ZIP64 archives are not supported')
  }
  if (dirSize > MAX_DIRECTORY_BYTES || dirOffset + dirSize > tailStart + eocd) {
    throw new MrpackReadError('not_zip', 'broken central directory')
  }
  const dir = new DataView(await readSlice(file, dirOffset, dirOffset + dirSize))
  const decoder = new TextDecoder()
  for (let p = 0, n = 0; n < count; n++) {
    if (p + 46 > dir.byteLength || dir.getUint32(p, true) !== SIG_CENTRAL) throw new MrpackReadError('not_zip', 'broken central directory')
    const flags = dir.getUint16(p + 8, true)
    const method = dir.getUint16(p + 10, true)
    const compressedSize = dir.getUint32(p + 20, true)
    const size = dir.getUint32(p + 24, true)
    const nameLen = dir.getUint16(p + 28, true)
    const extraLen = dir.getUint16(p + 30, true)
    const commentLen = dir.getUint16(p + 32, true)
    const offset = dir.getUint32(p + 42, true)
    if (p + 46 + nameLen > dir.byteLength) throw new MrpackReadError('not_zip', 'broken central directory')
    const name = decoder.decode(new Uint8Array(dir.buffer, dir.byteOffset + p + 46, nameLen))
    if (name === INDEX_NAME) {
      if (flags & 1) throw new MrpackReadError('unsupported', 'encrypted entries are not supported')
      if (method !== 0 && method !== 8) throw new MrpackReadError('unsupported', `compression method ${method} is not supported`)
      if (compressedSize === 0xffffffff || size === 0xffffffff || offset === 0xffffffff) {
        throw new MrpackReadError('unsupported', 'ZIP64 entries are not supported')
      }
      return { method, compressedSize, size, offset }
    }
    p += 46 + nameLen + extraLen + commentLen
  }
  throw new MrpackReadError('no_index', `${INDEX_NAME} is missing`)
}

/** Inflates with a hard output limit: a lying size in the directory cannot make the browser inflate a bomb. */
function inflateLimited(raw: Uint8Array): Uint8Array {
  const chunks: Uint8Array[] = []
  let total = 0
  const inflate = new Inflate((chunk) => {
    total += chunk.byteLength
    if (total > MAX_INDEX_BYTES) throw new MrpackReadError('too_large', `${INDEX_NAME} is larger than 5 MiB`)
    chunks.push(chunk)
  })
  try {
    inflate.push(raw, true)
  } catch (err) {
    if (err instanceof MrpackReadError) throw err
    throw new MrpackReadError('not_zip', `${INDEX_NAME} cannot be decompressed`)
  }
  const out = new Uint8Array(total)
  let at = 0
  for (const c of chunks) {
    out.set(c, at)
    at += c.byteLength
  }
  return out
}

function readSlice(file: Blob, start: number, end: number): Promise<ArrayBuffer> {
  const part = file.slice(start, end)
  if (typeof part.arrayBuffer === 'function') return part.arrayBuffer()
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(reader.result as ArrayBuffer)
    reader.onerror = () => reject(reader.error ?? new Error('read failed'))
    reader.readAsArrayBuffer(part)
  })
}
