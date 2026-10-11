import type { ConsoleLineEvent, ConsoleStateEvent } from '@/api/types'
import { parseFormatted, plainText, type Segment } from './ansi'

/** Ring buffer size of the console (spec §7.2). */
export const MAX_ENTRIES = 5000

export type LogLevel = 'info' | 'warn' | 'error'
export type LogStreamName = ConsoleLineEvent['stream']

/** A `line` event of the console stream plus its SSE id (Docker timestamp in ns, as text). */
export interface IncomingLine extends ConsoleLineEvent {
  id: string
}

export interface LineEntry {
  kind: 'line'
  key: string
  ts: number
  stream: LogStreamName
  text: string
  segments: Segment[]
  level: LogLevel
  rcon: boolean
}

export type CommandStatus = 'pending' | 'ok' | 'error'

export interface CommandEntry {
  kind: 'command'
  key: string
  id: number
  command: string
  status: CommandStatus
  output: string
  segments: Segment[]
}

export type LogEntry = LineEntry | CommandEntry

export interface LogState {
  entries: LogEntry[]
  nextKey: number
  /** Highest event id seen so far; lines with a lower or equal id are duplicates of a reconnect. */
  lastId: bigint | null
}

export type LogAction =
  | { type: 'lines'; lines: readonly IncomingLine[] }
  | { type: 'command'; id: number; command: string }
  | { type: 'command_done'; id: number; ok: boolean; output: string }

const RCON_NOISE = /\bThread RCON Client \S+ (?:started|shutting down)\b/
const ERROR_MARK = /[\s/[](?:ERROR|FATAL|SEVERE)\]/
const WARN_MARK = /[\s/[](?:WARN|WARNING)\]/
const EVENT_ID = /^\d{1,20}$/

export function initialLogState(): LogState {
  return { entries: [], nextKey: 0, lastId: null }
}

/** The two lines every RCON call leaves in the server log (spec §3.3). */
export function isRconNoise(text: string): boolean {
  return RCON_NOISE.test(text)
}

export function detectLevel(text: string, stream: LogStreamName): LogLevel {
  if (ERROR_MARK.test(text)) return 'error'
  if (WARN_MARK.test(text) || stream === 'err') return 'warn'
  return 'info'
}

export function eventId(id: string): bigint | null {
  return EVENT_ID.test(id) ? BigInt(id) : null
}

function parseJson(data: string): Record<string, unknown> | null {
  try {
    const value: unknown = JSON.parse(data)
    return typeof value === 'object' && value !== null ? (value as Record<string, unknown>) : null
  } catch {
    return null
  }
}

export function parseLineEvent(data: string, lastEventId: string): IncomingLine | null {
  const value = parseJson(data)
  if (!value) return null
  const { ts, stream, line } = value
  if (typeof ts !== 'number' || typeof line !== 'string' || (stream !== 'out' && stream !== 'err')) return null
  return { id: lastEventId, ts, stream, line }
}

export function parseStateEvent(data: string): ConsoleStateEvent | null {
  const value = parseJson(data)
  return value && typeof value.running === 'boolean' ? { running: value.running } : null
}

function makeLine(incoming: IncomingLine, key: string): LineEntry {
  const segments = parseFormatted(incoming.line)
  const text = segments.map((s) => s.text).join('')
  return {
    kind: 'line',
    key,
    ts: incoming.ts,
    stream: incoming.stream,
    text,
    segments,
    level: detectLevel(text, incoming.stream),
    rcon: isRconNoise(text),
  }
}

function trim(entries: LogEntry[]): LogEntry[] {
  return entries.length > MAX_ENTRIES ? entries.slice(entries.length - MAX_ENTRIES) : entries
}

export function logReducer(state: LogState, action: LogAction): LogState {
  switch (action.type) {
    case 'lines': {
      let { nextKey, lastId } = state
      const added: LogEntry[] = []
      for (const incoming of action.lines) {
        const id = eventId(incoming.id)
        if (id !== null && lastId !== null && id <= lastId) continue
        if (id !== null) lastId = id
        added.push(makeLine(incoming, `l${nextKey}`))
        nextKey += 1
      }
      if (added.length === 0) return state
      return { entries: trim([...state.entries, ...added]), nextKey, lastId }
    }
    case 'command': {
      const entry: CommandEntry = {
        kind: 'command',
        key: `c${action.id}`,
        id: action.id,
        command: action.command,
        status: 'pending',
        output: '',
        segments: [],
      }
      return { ...state, entries: trim([...state.entries, entry]) }
    }
    case 'command_done':
      return {
        ...state,
        entries: state.entries.map((e) =>
          e.kind === 'command' && e.id === action.id
            ? {
                ...e,
                status: action.ok ? 'ok' : 'error',
                output: action.output,
                // Error texts come from the UI (translated), never from the game: no formatting codes to apply.
                segments: action.ok ? parseFormatted(action.output) : [{ text: action.output }],
              }
            : e,
        ),
      }
  }
}

export interface LogFilter {
  query: string
  hideRcon: boolean
}

export function visibleEntries(entries: readonly LogEntry[], filter: LogFilter): readonly LogEntry[] {
  const query = filter.query.trim().toLowerCase()
  if (!query && !filter.hideRcon) return entries
  return entries.filter((e) => {
    if (e.kind === 'line') {
      if (filter.hideRcon && e.rcon) return false
      return !query || e.text.toLowerCase().includes(query)
    }
    return !query || e.command.toLowerCase().includes(query) || plainText(e.output).toLowerCase().includes(query)
  })
}

export function entryText(entry: LogEntry): string {
  if (entry.kind === 'line') return entry.text
  const output = entry.status === 'error' ? entry.output : plainText(entry.output)
  return output ? `› ${entry.command}\n${output}` : `› ${entry.command}`
}
