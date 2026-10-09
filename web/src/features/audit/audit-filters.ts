import type { AuditResult } from '@/api/types'

export interface AuditFilters {
  actor: string
  action: string
  result: AuditResult | ''
  from: string
  to: string
}

export const EMPTY_FILTERS: AuditFilters = { actor: '', action: '', result: '', from: '', to: '' }

/** <input type="date"> yields YYYY-MM-DD; interpret it in the browser's local time zone. */
export function dateInputToMs(value: string, endOfDay: boolean): number | undefined {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return undefined
  const [y, m, d] = value.split('-').map(Number)
  return endOfDay ? new Date(y, m - 1, d, 23, 59, 59, 999).getTime() : new Date(y, m - 1, d, 0, 0, 0, 0).getTime()
}

export function toAuditQuery(filters: AuditFilters) {
  return {
    actor: filters.actor.trim() || undefined,
    action: filters.action.trim() || undefined,
    result: filters.result || undefined,
    from: dateInputToMs(filters.from, false),
    to: dateInputToMs(filters.to, true),
  }
}
