import { describe, expect, it } from 'vitest'
import {
  detectLevel,
  entryText,
  eventId,
  initialLogState,
  isRconNoise,
  logReducer,
  MAX_ENTRIES,
  parseLineEvent,
  parseStateEvent,
  visibleEntries,
  type IncomingLine,
  type LogEntry,
  type LogState,
} from './log-model'

function line(id: string, text: string, stream: 'out' | 'err' = 'out'): IncomingLine {
  return { id, ts: 1_760_000_000_000, stream, line: text }
}

function texts(state: LogState): string[] {
  return state.entries.map((e) => (e.kind === 'line' ? e.text : `> ${e.command}`))
}

describe('logReducer', () => {
  it('appends lines with stable keys, plain text, level and RCON flag', () => {
    const s = logReducer(initialLogState(), {
      type: 'lines',
      lines: [line('1000', '§a[10:00:00 INFO]: Done'), line('1001', '[10:00:01 WARN]: Can\'t keep up!'), line('1002', 'Thread RCON Client /127.0.0.1 started')],
    })
    expect(s.entries.map((e) => e.key)).toEqual(['l0', 'l1', 'l2'])
    const [done, warn, rcon] = s.entries as Extract<LogEntry, { kind: 'line' }>[]
    expect(done.text).toBe('[10:00:00 INFO]: Done')
    expect(done.segments).toEqual([{ text: '[10:00:00 INFO]: Done', color: 'bright-green' }])
    expect(done.level).toBe('info')
    expect(warn.level).toBe('warn')
    expect(rcon.rcon).toBe(true)
    expect(s.lastId).toBe(1002n)
  })

  it('drops lines it has already seen', () => {
    let s = logReducer(initialLogState(), { type: 'lines', lines: [line('1000', 'a'), line('1001', 'b')] })
    s = logReducer(s, { type: 'connected' })
    s = logReducer(s, { type: 'lines', lines: [line('999', 'old'), line('1000', 'a'), line('1001', 'b'), line('1002', 'c')] })
    expect(texts(s)).toEqual(['a', 'b', 'c'])
  })

  it('keeps lines that share an event id within one connection', () => {
    let s = logReducer(initialLogState(), { type: 'connected' })
    s = logReducer(s, { type: 'lines', lines: [line('1000', 'a'), line('1000', 'b')] })
    s = logReducer(s, { type: 'lines', lines: [line('1000', 'c'), line('1001', 'd')] })
    expect(texts(s)).toEqual(['a', 'b', 'c', 'd'])
    expect(s.lastId).toBe(1001n)
  })

  it('accepts lines without a usable event id', () => {
    const s = logReducer(initialLogState(), { type: 'lines', lines: [line('', 'a'), line('x', 'b')] })
    expect(texts(s)).toEqual(['a', 'b'])
    expect(s.lastId).toBeNull()
  })

  it('returns the same state when nothing new arrived', () => {
    let s = logReducer(initialLogState(), { type: 'lines', lines: [line('5', 'a')] })
    s = logReducer(s, { type: 'connected' })
    expect(logReducer(s, { type: 'lines', lines: [line('5', 'a')] })).toBe(s)
  })

  it('keeps at most MAX_ENTRIES entries', () => {
    const lines = Array.from({ length: MAX_ENTRIES + 3 }, (_, i) => line(String(i + 1), `line ${i + 1}`))
    const s = logReducer(initialLogState(), { type: 'lines', lines })
    expect(s.entries).toHaveLength(MAX_ENTRIES)
    expect(texts(s)[0]).toBe('line 4')
    expect(texts(s).at(-1)).toBe(`line ${MAX_ENTRIES + 3}`)
  })

  it('tracks a command from pending to its answer', () => {
    let s = logReducer(initialLogState(), { type: 'command', id: 1, command: 'list' })
    expect(s.entries).toEqual([{ kind: 'command', key: 'c1', id: 1, command: 'list', status: 'pending', output: '', segments: [] }])
    s = logReducer(s, { type: 'command_done', id: 1, ok: true, output: '§6There are 0 of a max of 20 players online: ' })
    expect(s.entries[0]).toMatchObject({ status: 'ok', segments: [{ text: 'There are 0 of a max of 20 players online: ', color: 'yellow' }] })
  })

  it('stores a failed command as plain error text', () => {
    let s = logReducer(initialLogState(), { type: 'command', id: 7, command: 'list' })
    s = logReducer(s, { type: 'command_done', id: 7, ok: false, output: '§cnot formatted' })
    expect(s.entries[0]).toMatchObject({ status: 'error', output: '§cnot formatted', segments: [{ text: '§cnot formatted' }] })
  })

  it('ignores the answer of a command that left the buffer', () => {
    const s = logReducer(initialLogState(), { type: 'lines', lines: [line('1', 'a')] })
    expect(logReducer(s, { type: 'command_done', id: 99, ok: true, output: 'x' }).entries).toEqual(s.entries)
  })
})

describe('classification', () => {
  it('detects RCON connection noise', () => {
    expect(isRconNoise('[09:32:51 INFO]: Thread RCON Client /0:0:0:0:0:0:0:1 started')).toBe(true)
    expect(isRconNoise('[09:32:51 INFO]: Thread RCON Client /127.0.0.1 shutting down')).toBe(true)
    expect(isRconNoise('[09:32:51 INFO]: RCON running on 0.0.0.0:25575')).toBe(false)
  })

  it('detects warning and error levels of Paper and Vanilla log formats', () => {
    expect(detectLevel('[10:00:00 WARN]: x', 'out')).toBe('warn')
    expect(detectLevel('[10:00:00] [Server thread/WARN]: x', 'out')).toBe('warn')
    expect(detectLevel('[10:00:00 ERROR]: x', 'out')).toBe('error')
    expect(detectLevel('[10:00:00] [Server thread/FATAL]: x', 'out')).toBe('error')
    expect(detectLevel('[10:00:00 INFO]: WARN in text is fine', 'out')).toBe('info')
    expect(detectLevel('java.lang.Exception', 'err')).toBe('warn')
    expect(detectLevel('[10:00:00 ERROR]: x', 'err')).toBe('error')
  })
})

describe('events', () => {
  it('parses event ids as nanosecond numbers', () => {
    expect(eventId('1760000000123456789')).toBe(1760000000123456789n)
    expect(eventId('')).toBeNull()
    expect(eventId('12a')).toBeNull()
    expect(eventId('123456789012345678901')).toBeNull()
  })

  it('parses line events and rejects malformed data', () => {
    expect(parseLineEvent('{"ts":1,"stream":"err","line":"x"}', '42')).toEqual({ id: '42', ts: 1, stream: 'err', line: 'x' })
    expect(parseLineEvent('not json', '1')).toBeNull()
    expect(parseLineEvent('null', '1')).toBeNull()
    expect(parseLineEvent('{"ts":"1","stream":"out","line":"x"}', '1')).toBeNull()
    expect(parseLineEvent('{"ts":1,"stream":"stdout","line":"x"}', '1')).toBeNull()
    expect(parseLineEvent('{"ts":1,"stream":"out","line":5}', '1')).toBeNull()
  })

  it('parses state events', () => {
    expect(parseStateEvent('{"running":false}')).toEqual({ running: false })
    expect(parseStateEvent('{"running":"yes"}')).toBeNull()
    expect(parseStateEvent('[')).toBeNull()
  })
})

describe('visibleEntries', () => {
  const base = logReducer(
    logReducer(initialLogState(), {
      type: 'lines',
      lines: [line('1', 'Done (3.2s)!'), line('2', 'Thread RCON Client /127.0.0.1 started'), line('3', "Can't keep up!")],
    }),
    { type: 'command', id: 1, command: 'list' },
  )
  const state = logReducer(base, { type: 'command_done', id: 1, ok: true, output: 'There are 2 players' })

  it('hides RCON noise on request', () => {
    expect(visibleEntries(state.entries, { query: '', hideRcon: true })).toHaveLength(3)
    expect(visibleEntries(state.entries, { query: '', hideRcon: false })).toHaveLength(4)
  })

  it('filters lines and command blocks case-insensitively', () => {
    expect(visibleEntries(state.entries, { query: 'KEEP', hideRcon: true }).map(entryText)).toEqual(["Can't keep up!"])
    expect(visibleEntries(state.entries, { query: '2 players', hideRcon: true }).map((e) => e.key)).toEqual(['c1'])
    expect(visibleEntries(state.entries, { query: '  ', hideRcon: false })).toHaveLength(4)
  })

  it('renders entries as copyable text', () => {
    expect(state.entries.map(entryText)).toEqual(['Done (3.2s)!', 'Thread RCON Client /127.0.0.1 started', "Can't keep up!", '› list\nThere are 2 players'])
  })
})
