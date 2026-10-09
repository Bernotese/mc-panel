import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/errors'
import i18n from '@/i18n'
import { errorMessage, isWebAuthnCancelled } from './error-message'

const t = i18n.t.bind(i18n)

describe('errorMessage', () => {
  it('translates known codes', () => {
    expect(errorMessage(new ApiError({ status: 401, code: 'invalid_code', message: 'invalid code' }), t)).toBe('Der Code ist ungültig.')
  })

  it('appends the server detail for validation and conflict errors', () => {
    const err = new ApiError({ status: 409, code: 'conflict', message: 'username taken' })
    expect(errorMessage(err, t)).toBe('Konflikt mit dem aktuellen Zustand (username taken)')
  })

  it('uses Retry-After for rate limits', () => {
    const err = new ApiError({ status: 429, code: 'rate_limited', message: 'slow', retryAfterSeconds: 30 })
    expect(errorMessage(err, t)).toBe('Zu viele Versuche. Bitte in 30 s erneut versuchen.')
  })

  it('falls back to a network message for non-API errors', () => {
    expect(errorMessage(new TypeError('Failed to fetch'), t)).toBe('Server nicht erreichbar. Bitte Verbindung prüfen.')
  })

  it('recognises cancelled WebAuthn ceremonies', () => {
    const cancelled = Object.assign(new Error('The operation either timed out or was not allowed.'), { name: 'NotAllowedError' })
    expect(isWebAuthnCancelled(cancelled)).toBe(true)
    expect(errorMessage(cancelled, t)).toBe('Vorgang abgebrochen oder Zeit abgelaufen.')
  })
})
