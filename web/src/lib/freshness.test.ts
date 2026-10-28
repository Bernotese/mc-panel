import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/errors'
import { freshnessOf, type FreshnessSource } from './freshness'

function source(over: Partial<FreshnessSource> = {}): FreshnessSource {
  return { data: { ok: true }, isError: false, isPaused: false, error: null, dataUpdatedAt: 1_000, errorUpdatedAt: 0, isFetching: false, refetch: vi.fn(), ...over }
}

describe('freshnessOf', () => {
  it('is fresh while every source loaded or is still loading', () => {
    const f = freshnessOf(source(), source({ data: undefined, isError: true, error: new Error('first load') }), null, undefined, false)
    expect(f.stale).toBe(false)
    expect(f.since).toBeNull()
  })

  it('is stale when a refetch failed while data is kept, with the oldest success time', () => {
    const err = new ApiError({ status: 503, code: 'agent_unavailable', message: 'no agent' })
    const f = freshnessOf(source({ dataUpdatedAt: 5_000 }), source({ isError: true, error: err, dataUpdatedAt: 2_000, errorUpdatedAt: 9_000 }))
    expect(f.stale).toBe(true)
    expect(f.offline).toBe(false)
    expect(f.since).toBe(2_000)
    expect(f.error).toBe(err)
    expect(f.checkedAt).toBe(9_000)
  })

  it('is stale and offline while a refetch waits for the network', () => {
    const f = freshnessOf(source({ isPaused: true, dataUpdatedAt: 3_000 }))
    expect(f.stale).toBe(true)
    expect(f.offline).toBe(true)
    expect(f.since).toBe(3_000)
  })

  it('counts a failed fetch without an API error as offline', () => {
    expect(freshnessOf(source({ isError: true, error: new TypeError('Failed to fetch') })).offline).toBe(true)
  })

  it('counts a gateway error without an error body as offline (the panel behind Caddy is down)', () => {
    const gateway = new ApiError({ status: 502, code: 'unknown', message: 'HTTP 502' })
    expect(freshnessOf(source({ isError: true, error: gateway })).offline).toBe(true)
    const internal = new ApiError({ status: 500, code: 'internal', message: 'boom' })
    expect(freshnessOf(source({ isError: true, error: internal })).offline).toBe(false)
  })

  it('retries only the stale sources and reports a running retry', () => {
    const fresh = source()
    const failed = source({ isError: true, error: new TypeError('x'), isFetching: true })
    const f = freshnessOf(fresh, failed)
    expect(f.retrying).toBe(true)
    f.retry()
    expect(failed.refetch).toHaveBeenCalledTimes(1)
    expect(fresh.refetch).not.toHaveBeenCalled()
  })
})
