import { describe, expect, it } from 'vitest'
import { formatDate, formatDateTime, formatNumber, formatRelative } from './format'

// Noon UTC stays on 8 Oct 2026 in every timezone from UTC-11 to UTC+11.
const NOON = Date.UTC(2026, 9, 8, 12, 0, 0)

describe('format', () => {
  it('formats dates per locale', () => {
    expect(formatDate(NOON, 'de')).toBe('08.10.2026')
    expect(formatDate(NOON, 'en')).toBe('Oct 8, 2026')
    expect(formatDateTime(NOON, 'de')).toContain('08.10.2026')
  })

  it('formats relative times', () => {
    expect(formatRelative(NOON - 5 * 60_000, 'de', NOON)).toBe('vor 5 Minuten')
    expect(formatRelative(NOON - 5 * 60_000, 'en', NOON)).toBe('5 minutes ago')
    expect(formatRelative(NOON + 2 * 3_600_000, 'de', NOON)).toBe('in 2 Stunden')
    expect(formatRelative(NOON - 3 * 86_400_000, 'en', NOON)).toBe('3 days ago')
  })

  it('formats numbers per locale', () => {
    expect(formatNumber(12345, 'de')).toBe('12.345')
    expect(formatNumber(12345, 'en')).toBe('12,345')
  })
})
