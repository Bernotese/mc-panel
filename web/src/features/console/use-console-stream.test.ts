import { act, renderHook, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import type { LogEntry } from './log-model'
import { FakeEventSource } from './testing/fake-event-source'
import { classifyProbe, DEFAULT_TIMING, useConsoleStream, type StreamTiming } from './use-console-stream'
import { ApiError } from '@/api/errors'

const FAST: StreamTiming = { flushMs: 0, reconnectMs: 0, retryMs: 0 }
const STREAM = '*/api/servers/:id/console/stream'

function line(text: string) {
  return { ts: 1_760_000_000_000, stream: 'out', line: text }
}

function texts(entries: readonly LogEntry[]): string[] {
  return entries.map((e) => (e.kind === 'line' ? e.text : `> ${e.command}`))
}

beforeEach(() => {
  FakeEventSource.reset()
  vi.stubGlobal('EventSource', FakeEventSource)
  loginAs('admin')
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('useConsoleStream', () => {
  it('opens the stream of the server and collects lines in order', async () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    const es = FakeEventSource.latest()
    expect(es.url).toBe('/api/servers/1/console/stream')
    expect(result.current.connection).toBe('connecting')
    act(() => {
      es.open()
      es.emit('state', { running: true })
      es.emit('line', line('first'), '1000')
      es.emit('line', line('second'), '1001')
    })
    await waitFor(() => expect(texts(result.current.entries)).toEqual(['first', 'second']))
    expect(result.current.connection).toBe('live')
  })

  it('ignores malformed events', async () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    const es = FakeEventSource.latest()
    act(() => {
      es.open()
      es.emit('line', { ts: 'x', stream: 'out', line: 'bad' }, '1')
      es.emit('state', { running: 'maybe' })
      es.emit('line', line('good'), '2')
    })
    await waitFor(() => expect(texts(result.current.entries)).toEqual(['good']))
    expect(result.current.connection).toBe('live')
  })

  it('reports a stopped server', async () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    const es = FakeEventSource.latest()
    act(() => {
      es.open()
      es.emit('state', { running: false })
    })
    expect(result.current.connection).toBe('stopped')
  })

  it('shows a transient error as reconnecting and leaves the retry to the browser', () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    act(() => FakeEventSource.latest().fail(false))
    expect(result.current.connection).toBe('reconnecting')
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('reconnects after the stream was closed and skips lines it already has', async () => {
    server.use(http.get(STREAM, () => new HttpResponse(null, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })))
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    const first = FakeEventSource.latest()
    act(() => {
      first.open()
      first.emit('line', line('first'), '1000')
      first.emit('line', line('second'), '1001')
    })
    await waitFor(() => expect(result.current.entries).toHaveLength(2))
    act(() => first.fail(true))
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2))
    const second = FakeEventSource.latest()
    act(() => {
      second.open()
      second.emit('line', line('first'), '1000')
      second.emit('line', line('second'), '1001')
      second.emit('line', line('third'), '1002')
    })
    await waitFor(() => expect(texts(result.current.entries)).toEqual(['first', 'second', 'third']))
    expect(result.current.connection).toBe('live')
  })

  it('gives up after a 403', async () => {
    server.use(http.get(STREAM, () => HttpResponse.json({ error: 'forbidden', message: 'no' }, { status: 403 })))
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    act(() => FakeEventSource.latest().fail(true))
    await waitFor(() => expect(result.current.connection).toBe('forbidden'))
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('retries later while the agent is unavailable', async () => {
    server.use(http.get(STREAM, () => HttpResponse.json({ error: 'agent_unavailable', message: 'down' }, { status: 503 })))
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    act(() => FakeEventSource.latest().fail(true))
    await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThanOrEqual(2))
    expect(result.current.connection).toBe('unavailable')
  })

  it('closes the stream when the console is left', () => {
    const { unmount } = renderHook(() => useConsoleStream(1, FAST))
    const es = FakeEventSource.latest()
    unmount()
    expect(es.readyState).toBe(FakeEventSource.CLOSED)
  })

  it('starts over with an empty log when the server changes', async () => {
    const { result, rerender } = renderHook(({ id }) => useConsoleStream(id, FAST), { initialProps: { id: 1 } })
    const first = FakeEventSource.latest()
    act(() => {
      first.open()
      first.emit('line', line('from server 1'), '1000')
    })
    await waitFor(() => expect(result.current.entries).toHaveLength(1))
    rerender({ id: 2 })
    expect(first.readyState).toBe(FakeEventSource.CLOSED)
    expect(result.current.entries).toEqual([])
    const second = FakeEventSource.latest()
    expect(second.url).toBe('/api/servers/2/console/stream')
    act(() => {
      second.open()
      second.emit('line', line('from server 2'), '5')
    })
    await waitFor(() => expect(texts(result.current.entries)).toEqual(['from server 2']))
  })

  it('keeps lines that share an event id', async () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    const es = FakeEventSource.latest()
    act(() => {
      es.open()
      es.emit('line', line('a'), '1000')
      es.emit('line', line('b'), '1000')
    })
    await waitFor(() => expect(texts(result.current.entries)).toEqual(['a', 'b']))
  })

  it('stops after a 401 of the probe without opening another stream', async () => {
    server.use(http.get(STREAM, () => HttpResponse.json({ error: 'unauthorized', message: 'no' }, { status: 401 })))
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    act(() => FakeEventSource.latest().fail(true))
    await waitFor(() => expect(result.current.connection).toBe('unauthorized'))
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('probes without replaying the log', async () => {
    let query = ''
    server.use(
      http.get(STREAM, ({ request }) => {
        query = new URL(request.url).search
        return new HttpResponse(null, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
      }),
    )
    renderHook(() => useConsoleStream(1, FAST))
    act(() => FakeEventSource.latest().fail(true))
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2))
    expect(query).toBe('?tail=0')
  })

  it('tracks commands next to the log lines', () => {
    const { result } = renderHook(() => useConsoleStream(1, FAST))
    let id = 0
    act(() => {
      id = result.current.addCommand('list')
    })
    expect(result.current.entries).toMatchObject([{ kind: 'command', command: 'list', status: 'pending' }])
    act(() => result.current.finishCommand(id, true, 'There are 0 players'))
    expect(result.current.entries).toMatchObject([{ kind: 'command', status: 'ok', output: 'There are 0 players' }])
  })
})

describe('classifyProbe', () => {
  const err = (status: number, code: 'unauthorized' | 'forbidden' | 'not_found' | 'rate_limited' | 'agent_unavailable', retryAfterSeconds?: number) =>
    new ApiError({ status, code, message: code, retryAfterSeconds })

  it('maps the status of the probe to the next step', () => {
    expect(classifyProbe(null, DEFAULT_TIMING)).toEqual({ state: 'reconnecting', retryMs: 3_000 })
    expect(classifyProbe(err(401, 'unauthorized'), DEFAULT_TIMING)).toEqual({ state: 'unauthorized', retryMs: null })
    expect(classifyProbe(err(403, 'forbidden'), DEFAULT_TIMING)).toEqual({ state: 'forbidden', retryMs: null })
    expect(classifyProbe(err(404, 'not_found'), DEFAULT_TIMING)).toEqual({ state: 'not_found', retryMs: null })
    expect(classifyProbe(err(429, 'rate_limited', 30), DEFAULT_TIMING)).toEqual({ state: 'reconnecting', retryMs: 30_000 })
    expect(classifyProbe(err(429, 'rate_limited'), DEFAULT_TIMING)).toEqual({ state: 'reconnecting', retryMs: 10_000 })
    expect(classifyProbe(err(503, 'agent_unavailable'), DEFAULT_TIMING)).toEqual({ state: 'unavailable', retryMs: 10_000 })
    expect(classifyProbe(new TypeError('Failed to fetch'), DEFAULT_TIMING)).toEqual({ state: 'unavailable', retryMs: 10_000 })
  })
})
