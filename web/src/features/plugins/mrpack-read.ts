import { Inflate } from 'fflate'

/** Spec §5: the index of a pack is usually < 500 KiB; anything above this is refused before and while inflating. */
export const MAX_INDEX_BYTES = 5 * 1024 * 1024
/**
 * Deflate never needs much more than the stored size (5 bytes per 64 KiB block); a larger compressed entry cannot hold a
 * valid index and is refused before a byte of it is read.
 */
const MAX_COMPRESSED_BYTES = MAX_INDEX_BYTES + 64 * 1024
/**
 * Compressed input per Inflate push. fflate inflates a whole push before it hands out any output, and deflate expands
 * at most ~1032:1, so one push allocates at most ~4 MiB: the output limit is checked after every push, never after a
 * bomb was inflated whole.
 */
const INFLATE_CHUNK = 4 * 1024
/** Pushes between two yields to the event loop (checking the signal), so the tab stays responsive and can cancel. */
const PUSHES_PER_YIELD = 32
/** The central directory is read in one piece; 32 MiB covers several hundred thousand entries. */
const MAX_DIRECTORY_BYTES = 32 * 1024 * 1024
const INDEX_NAME = 'modrinth.index.json'

const SIG_EOCD = 0x06054b50
const SIG_CENTRAL = 0x02014b50
const SIG_LOCAL = 0x04034b50
const LOCAL_HEADER = 30
const CENTRAL_HEADER = 46
/** General purpose flags: bit 0 = encrypted, bit 3 = sizes and CRC follow the data (local header fields are 0). */
const FLAG_ENCRYPTED = 1
const FLAG_DESCRIPTOR = 8
/** End of central directory record without comment, plus the longest comment. */
const EOCD_MIN = 22
const EOCD_SEARCH = EOCD_MIN + 0xffff

/** too_large_to_send: the index without downloads/fileSize still exceeds the panel's 1 MiB body limit. */
export type MrpackReadErrorCode = 'not_zip' | 'unsupported' | 'no_index' | 'too_large' | 'invalid_json' | 'too_large_to_send'

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
 * and no other entry is ever decompressed. Every read and allocation is bounded before it happens: the compressed
 * entry by MAX_COMPRESSED_BYTES and the archive's own layout, the output by MAX_INDEX_BYTES per bounded push. The local
 * header must agree with the central directory and the output with the declared size. An aborted signal stops the
 * read between steps. Returns the parsed JSON unchecked; the panel validates it (spec §4.1).
 */
export async function readMrpackIndex(file: Blob, signal?: AbortSignal): Promise<unknown> {
  signal?.throwIfAborted()
  const entry = await findIndexEntry(file, signal)
  if (entry.size > MAX_INDEX_BYTES || entry.compressedSize > MAX_COMPRESSED_BYTES) {
    throw new MrpackReadError('too_large', `${INDEX_NAME} is larger than 5 MiB`)
  }
  if (entry.method === 0 && entry.compressedSize !== entry.size) throw new MrpackReadError('not_zip', 'broken entry sizes')
  if (entry.offset + LOCAL_HEADER > entry.dataEnd) throw new MrpackReadError('not_zip', 'broken local header')
  const local = new DataView(await readSlice(file, entry.offset, entry.offset + LOCAL_HEADER + INDEX_NAME_BYTES.length))
  signal?.throwIfAborted()
  checkLocalHeader(local, entry)
  const start = entry.offset + LOCAL_HEADER + local.getUint16(26, true) + local.getUint16(28, true)
  if (start + entry.compressedSize > entry.dataEnd) throw new MrpackReadError('not_zip', 'entry outside the archive data')
  const raw = new Uint8Array(await readSlice(file, start, start + entry.compressedSize))
  signal?.throwIfAborted()
  if (raw.byteLength !== entry.compressedSize) throw new MrpackReadError('not_zip', 'truncated entry')
  const bytes = entry.method === 0 ? raw : await inflateLimited(raw, signal)
  if (bytes.byteLength !== entry.size) throw new MrpackReadError('not_zip', `${INDEX_NAME} does not match its declared size`)
  let text: string
  try {
    text = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    throw new MrpackReadError('invalid_json', `${INDEX_NAME} is not UTF-8`)
  }
  let value: unknown
  try {
    value = JSON.parse(text.replace(/^\uFEFF/, ''))
  } catch {
    throw new MrpackReadError('invalid_json', `${INDEX_NAME} is not valid JSON`)
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    throw new MrpackReadError('invalid_json', `${INDEX_NAME} is not a JSON object`)
  }
  return value
}

const INDEX_NAME_BYTES = new TextEncoder().encode(INDEX_NAME)

interface IndexEntry {
  flags: number
  method: number
  crc: number
  compressedSize: number
  size: number
  offset: number
  /** Where the entries end and the central directory begins: no entry may reach past it. */
  dataEnd: number
}

/** The local header must describe the same entry as the central directory (a reader trusting either one alone can be fooled). */
function checkLocalHeader(local: DataView, entry: IndexEntry) {
  if (local.byteLength < LOCAL_HEADER || local.getUint32(0, true) !== SIG_LOCAL) throw new MrpackReadError('not_zip', 'broken local header')
  const flags = local.getUint16(6, true)
  const nameLen = local.getUint16(26, true)
  const sameName =
    nameLen === INDEX_NAME_BYTES.length &&
    local.byteLength >= LOCAL_HEADER + nameLen &&
    INDEX_NAME_BYTES.every((b, i) => local.getUint8(LOCAL_HEADER + i) === b)
  const sameSizes =
    flags & FLAG_DESCRIPTOR
      ? true // the sizes follow the data; the central directory's are checked against the output
      : local.getUint32(14, true) === entry.crc && local.getUint32(18, true) === entry.compressedSize && local.getUint32(22, true) === entry.size
  if (!sameName || !sameSizes || (flags & FLAG_ENCRYPTED) !== (entry.flags & FLAG_ENCRYPTED) || local.getUint16(8, true) !== entry.method) {
    throw new MrpackReadError('not_zip', 'local header does not match the central directory')
  }
}

async function findIndexEntry(file: Blob, signal?: AbortSignal): Promise<IndexEntry> {
  if (file.size < EOCD_MIN) throw new MrpackReadError('not_zip', 'too small for a ZIP')
  const tailStart = Math.max(0, file.size - EOCD_SEARCH)
  const tail = new DataView(await readSlice(file, tailStart, file.size))
  signal?.throwIfAborted()
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
  signal?.throwIfAborted()
  const decoder = new TextDecoder()
  for (let p = 0, n = 0; n < count; n++) {
    if (p + CENTRAL_HEADER > dir.byteLength || dir.getUint32(p, true) !== SIG_CENTRAL) throw new MrpackReadError('not_zip', 'broken central directory')
    const flags = dir.getUint16(p + 8, true)
    const method = dir.getUint16(p + 10, true)
    const crc = dir.getUint32(p + 16, true)
    const compressedSize = dir.getUint32(p + 20, true)
    const size = dir.getUint32(p + 24, true)
    const nameLen = dir.getUint16(p + 28, true)
    const extraLen = dir.getUint16(p + 30, true)
    const commentLen = dir.getUint16(p + 32, true)
    const offset = dir.getUint32(p + 42, true)
    if (p + CENTRAL_HEADER + nameLen > dir.byteLength) throw new MrpackReadError('not_zip', 'broken central directory')
    const name = decoder.decode(new Uint8Array(dir.buffer, dir.byteOffset + p + CENTRAL_HEADER, nameLen))
    if (name === INDEX_NAME) {
      if (flags & FLAG_ENCRYPTED) throw new MrpackReadError('unsupported', 'encrypted entries are not supported')
      if (method !== 0 && method !== 8) throw new MrpackReadError('unsupported', `compression method ${method} is not supported`)
      if (compressedSize === 0xffffffff || size === 0xffffffff || offset === 0xffffffff) {
        throw new MrpackReadError('unsupported', 'ZIP64 entries are not supported')
      }
      return { flags, method, crc, compressedSize, size, offset, dataEnd: dirOffset }
    }
    p += CENTRAL_HEADER + nameLen + extraLen + commentLen
  }
  throw new MrpackReadError('no_index', `${INDEX_NAME} is missing`)
}

/**
 * Inflates with a hard output limit: a lying size in the directory cannot make the browser inflate a bomb. The input
 * goes in INFLATE_CHUNK pieces, so no push can allocate more than ~4 MiB before the limit is checked again.
 */
async function inflateLimited(raw: Uint8Array, signal?: AbortSignal): Promise<Uint8Array> {
  const chunks: Uint8Array[] = []
  let total = 0
  const inflate = new Inflate((chunk) => {
    total += chunk.byteLength
    if (total > MAX_INDEX_BYTES) throw new MrpackReadError('too_large', `${INDEX_NAME} is larger than 5 MiB`)
    chunks.push(chunk)
  })
  for (let at = 0, pushes = 0; at < raw.byteLength || pushes === 0; at += INFLATE_CHUNK, pushes++) {
    if (pushes > 0 && pushes % PUSHES_PER_YIELD === 0) {
      await yieldToEventLoop()
      signal?.throwIfAborted()
    }
    const end = Math.min(at + INFLATE_CHUNK, raw.byteLength)
    try {
      inflate.push(raw.subarray(at, end), end === raw.byteLength)
    } catch (err) {
      if (err instanceof MrpackReadError) throw err
      throw new MrpackReadError('not_zip', `${INDEX_NAME} cannot be decompressed`)
    }
  }
  const out = new Uint8Array(total)
  let at = 0
  for (const c of chunks) {
    out.set(c, at)
    at += c.byteLength
  }
  return out
}

function yieldToEventLoop(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0))
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
