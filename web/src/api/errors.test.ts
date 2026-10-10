import { describe, expect, it } from 'vitest'
import { ApiError, isApiError, toApiError, unwrap, unwrapVoid } from './errors'

describe('toApiError', () => {
  it('parses the JSON error format', () => {
    const err = toApiError(new Response(null, { status: 409 }), { error: 'conflict', message: 'username taken', request_id: 'r-1' })
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 409, code: 'conflict', message: 'username taken', requestId: 'r-1' })
  })

  it('falls back to unknown for non-JSON bodies', () => {
    const err = toApiError(new Response(null, { status: 502 }), 'Bad Gateway')
    expect(err).toMatchObject({ status: 502, code: 'unknown', message: 'Bad Gateway' })
  })

  it('reads Retry-After seconds', () => {
    const res = new Response(null, { status: 429, headers: { 'Retry-After': '30' } })
    expect(toApiError(res, { error: 'rate_limited', message: 'slow down' }).retryAfterSeconds).toBe(30)
  })

  it('narrows by code', () => {
    const err = toApiError(new Response(null, { status: 403 }), { error: 'sudo_required', message: 'x' })
    expect(isApiError(err, 'sudo_required')).toBe(true)
    expect(isApiError(err, 'forbidden')).toBe(false)
    expect(isApiError(new Error('x'))).toBe(false)
  })
})

describe('unwrap', () => {
  it('returns data on success and throws ApiError otherwise', async () => {
    await expect(unwrap(Promise.resolve({ data: 1, response: new Response(null, { status: 200 }) }))).resolves.toBe(1)
    await expect(
      unwrap(Promise.resolve({ error: { error: 'not_found', message: 'nope' }, response: new Response(null, { status: 404 }) })),
    ).rejects.toMatchObject({ code: 'not_found' })
    await expect(unwrapVoid(Promise.resolve({ error: undefined, response: new Response(null, { status: 500 }) }))).rejects.toMatchObject({
      status: 500,
      code: 'unknown',
    })
  })
})
