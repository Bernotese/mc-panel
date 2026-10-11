import { useCallback, useEffect, useReducer, useRef, useState } from 'react'
import { isApiError } from '@/api/errors'
import { consoleStreamUrl, probeConsoleStream } from './api'
import { initialLogState, logReducer, parseLineEvent, parseStateEvent, type IncomingLine, type LogEntry } from './log-model'

export type ConnectionState = 'connecting' | 'live' | 'stopped' | 'reconnecting' | 'unavailable' | 'forbidden' | 'not_found' | 'unauthorized'

export interface StreamTiming {
  /** Lines arriving within this window are rendered together. */
  flushMs: number
  /** Delay before a new EventSource after the stream answered 200 again. */
  reconnectMs: number
  /** Delay after 5xx, network errors or a 429 without Retry-After. */
  retryMs: number
}

export const DEFAULT_TIMING: StreamTiming = { flushMs: 50, reconnectMs: 3_000, retryMs: 10_000 }

/** EventSource.CLOSED; a constant so that tests can replace the global EventSource. */
const EVENT_SOURCE_CLOSED = 2

export function classifyProbe(err: unknown, timing: StreamTiming): { state: ConnectionState; retryMs: number | null } {
  if (err === null) return { state: 'reconnecting', retryMs: timing.reconnectMs }
  if (isApiError(err)) {
    if (err.status === 401) return { state: 'unauthorized', retryMs: null }
    if (err.status === 403) return { state: 'forbidden', retryMs: null }
    if (err.status === 404) return { state: 'not_found', retryMs: null }
    if (err.status === 429) {
      const retryMs = err.retryAfterSeconds !== undefined ? Math.max(err.retryAfterSeconds * 1000, timing.reconnectMs) : timing.retryMs
      return { state: 'reconnecting', retryMs }
    }
  }
  return { state: 'unavailable', retryMs: timing.retryMs }
}

export interface ConsoleStream {
  entries: readonly LogEntry[]
  connection: ConnectionState
  /** Adds a pending command block and returns its id for finishCommand. */
  addCommand(command: string): number
  finishCommand(id: number, ok: boolean, output: string): void
}

export function useConsoleStream(serverId: number, timing: StreamTiming = DEFAULT_TIMING): ConsoleStream {
  const [log, dispatch] = useReducer(logReducer, undefined, initialLogState)
  const [connection, setConnection] = useState<ConnectionState>('connecting')
  const commandSeq = useRef(0)
  const { flushMs, reconnectMs, retryMs } = timing

  useEffect(() => {
    const current: StreamTiming = { flushMs, reconnectMs, retryMs }
    let disposed = false
    let source: EventSource | null = null
    let retryTimer: ReturnType<typeof setTimeout> | undefined
    let flushTimer: ReturnType<typeof setTimeout> | undefined
    let pending: IncomingLine[] = []

    function flush() {
      flushTimer = undefined
      if (pending.length === 0) return
      const lines = pending
      pending = []
      dispatch({ type: 'lines', lines })
    }

    function connect() {
      if (disposed) return
      const es = new EventSource(consoleStreamUrl(serverId))
      source = es
      es.onopen = () => setConnection((prev) => (prev === 'stopped' ? prev : 'live'))
      es.addEventListener('line', (event: Event) => {
        const message = event as MessageEvent<string>
        const line = parseLineEvent(message.data, message.lastEventId)
        if (!line) return
        pending.push(line)
        flushTimer ??= setTimeout(flush, current.flushMs)
      })
      es.addEventListener('state', (event: Event) => {
        const state = parseStateEvent((event as MessageEvent<string>).data)
        if (state) setConnection(state.running ? 'live' : 'stopped')
      })
      es.onerror = () => {
        if (es.readyState !== EVENT_SOURCE_CLOSED) {
          setConnection('reconnecting') // the browser retries by itself and sends Last-Event-ID
          return
        }
        es.close()
        source = null
        void probeConsoleStream(serverId).then((err) => {
          if (disposed) return
          const next = classifyProbe(err, current)
          setConnection(next.state)
          if (next.retryMs !== null) retryTimer = setTimeout(connect, next.retryMs)
        })
      }
    }

    connect()
    return () => {
      disposed = true
      source?.close()
      clearTimeout(retryTimer)
      clearTimeout(flushTimer)
    }
  }, [serverId, flushMs, reconnectMs, retryMs])

  const addCommand = useCallback((command: string) => {
    commandSeq.current += 1
    const id = commandSeq.current
    dispatch({ type: 'command', id, command })
    return id
  }, [])

  const finishCommand = useCallback((id: number, ok: boolean, output: string) => {
    dispatch({ type: 'command_done', id, ok, output })
  }, [])

  return { entries: log.entries, connection, addCommand, finishCommand }
}
