import { describe, expect, it } from 'vitest'
import { dateInputToMs, EMPTY_FILTERS, toAuditQuery } from './audit-filters'

describe('audit filters', () => {
  it('converts date inputs to local start and end of day', () => {
    expect(dateInputToMs('2026-10-08', false)).toBe(new Date(2026, 9, 8, 0, 0, 0, 0).getTime())
    expect(dateInputToMs('2026-10-08', true)).toBe(new Date(2026, 9, 8, 23, 59, 59, 999).getTime())
    expect(dateInputToMs('', false)).toBeUndefined()
    expect(dateInputToMs('08.10.2026', false)).toBeUndefined()
  })

  it('omits empty filters from the query', () => {
    expect(toAuditQuery(EMPTY_FILTERS)).toEqual({ actor: undefined, action: undefined, result: undefined, from: undefined, to: undefined })
    expect(toAuditQuery({ ...EMPTY_FILTERS, actor: ' admin ', result: 'denied' })).toMatchObject({ actor: 'admin', result: 'denied' })
  })
})
