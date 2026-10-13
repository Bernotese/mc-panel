import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/errors'
import type { Server, ServerStatus } from '@/api/types'
import i18n from '@/i18n'
import { detailPollInterval, FAST_POLL_MS, isBusy, pollInterval, powerAllowed, SERVER_STATES, SLOW_POLL_MS, stateBadgeVariant, stateLabelKey } from './status'

const settled: ServerStatus = {
  state: 'running',
  desired_state: 'running',
  restart_required: false,
  operation: '',
  last_error: '',
  started_at: 1,
  memory_bytes: 0,
  memory_limit_bytes: 0,
  cpu_percent: 0,
}

function status(patch: Partial<ServerStatus>): ServerStatus {
  return { ...settled, ...patch }
}

function server(patch: Partial<ServerStatus>): Server {
  return { id: 1, name: 'survival', permissions: [], address: null, instance: null, status: status(patch), down_alerts: null }
}

describe('server status', () => {
  it('is busy while the agent works or the state is transitional', () => {
    expect(isBusy(status({}))).toBe(false)
    expect(isBusy(status({ operation: 'pulling' }))).toBe(true)
    for (const state of ['creating', 'starting', 'stopping', 'restarting'] as const) expect(isBusy(status({ state })), state).toBe(true)
  })

  it('is busy while the state lags behind the desired state', () => {
    expect(isBusy(status({ state: 'stopped', desired_state: 'running' }))).toBe(true)
    expect(isBusy(status({ state: 'running', desired_state: 'stopped' }))).toBe(true)
    expect(isBusy(status({ state: 'stopped', desired_state: 'stopped' }))).toBe(false)
  })

  it('treats error, unknown and not configured as settled', () => {
    expect(isBusy(status({ state: 'error', desired_state: 'running', last_error: 'exit 1' }))).toBe(false)
    expect(isBusy(status({ state: 'error', desired_state: 'stopped', last_error: 'pull failed' }))).toBe(false)
    expect(isBusy(status({ state: 'error', desired_state: 'running', last_error: '' }))).toBe(false)
    expect(isBusy(status({ state: 'unknown', desired_state: 'running' }))).toBe(false)
    expect(isBusy(status({ state: 'not_configured', desired_state: null }))).toBe(false)
  })

  it('polls fast only while any server is busy', () => {
    expect(FAST_POLL_MS).toBe(3_000)
    expect(SLOW_POLL_MS).toBe(15_000)
    expect(pollInterval(undefined)).toBe(SLOW_POLL_MS)
    expect(pollInterval([server({}), server({ state: 'stopped', desired_state: 'stopped' })])).toBe(SLOW_POLL_MS)
    expect(pollInterval([server({}), server({ operation: 'creating', state: 'creating' })])).toBe(FAST_POLL_MS)
  })

  it('allows only power actions that make sense in the current state', () => {
    expect(powerAllowed('running', 'start')).toBe(false)
    expect(powerAllowed('running', 'stop')).toBe(true)
    expect(powerAllowed('running', 'restart')).toBe(true)
    expect(powerAllowed('stopped', 'start')).toBe(true)
    expect(powerAllowed('stopped', 'stop')).toBe(false)
    expect(powerAllowed('stopped', 'restart')).toBe(false)
    expect(powerAllowed('creating', 'start')).toBe(false)
    expect(powerAllowed('unknown', 'start')).toBe(true)
    expect(powerAllowed('unknown', 'stop')).toBe(true)
    // restart needs the agent right away (503 without it); start/stop are stored and applied later
    expect(powerAllowed('unknown', 'restart')).toBe(false)
    expect(powerAllowed('error', 'restart')).toBe(true)
    expect(powerAllowed('not_configured', 'start')).toBe(false)
  })

  it('maps every state to a translated label and a badge variant', () => {
    for (const state of SERVER_STATES) expect(i18n.exists(stateLabelKey(state)), state).toBe(true)
    expect(stateBadgeVariant('running')).toBe('default')
    expect(stateBadgeVariant('stopped')).toBe('secondary')
    expect(stateBadgeVariant('error')).toBe('destructive')
    expect(stateBadgeVariant('creating')).toBe('outline')
  })

  it('stops polling a detail page without data or after 403/404', () => {
    expect(detailPollInterval(undefined, null)).toBe(false)
    expect(detailPollInterval(server({}), null)).toBe(SLOW_POLL_MS)
    expect(detailPollInterval(server({ state: 'starting' }), null)).toBe(FAST_POLL_MS)
    const gone = new ApiError({ status: 404, code: 'not_found', message: 'server not found' })
    const hidden = new ApiError({ status: 403, code: 'forbidden', message: 'missing permission server.view' })
    expect(detailPollInterval(server({}), gone)).toBe(false)
    expect(detailPollInterval(server({}), hidden)).toBe(false)
    // A transient failure (agent, network) keeps polling with the last known data.
    expect(detailPollInterval(server({}), new ApiError({ status: 503, code: 'agent_unavailable', message: 'x' }))).toBe(SLOW_POLL_MS)
  })
})
