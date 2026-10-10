import { describe, expect, it } from 'vitest'
import { formatCountries, parseCountries } from './countries'

describe('parseCountries', () => {
  it('normalises, dedupes and separates invalid entries', () => {
    expect(parseCountries(' de, AT;ch  de\nGermany')).toEqual({ codes: ['DE', 'AT', 'CH'], invalid: ['GERMANY'] })
  })

  it('treats an empty input as "all countries"', () => {
    expect(parseCountries('   ')).toEqual({ codes: [], invalid: [] })
  })

  it('formats codes for the input field', () => {
    expect(formatCountries(['DE', 'AT'])).toBe('DE, AT')
  })
})
