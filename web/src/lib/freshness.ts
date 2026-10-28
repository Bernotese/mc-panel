import { ApiError } from '@/api/errors'
import { useFormat } from './format'

/** The parts of a query result that freshness needs; every UseQueryResult fits. */
export interface FreshnessSource {
  data: unknown
  isError: boolean
  isPaused: boolean
  error: unknown
  dataUpdatedAt: number
  errorUpdatedAt: number
  isFetching: boolean
  refetch: () => unknown
}

export interface Freshness {
  /** Some source shows cached data whose refresh failed or waits for the network. */
  stale: boolean
  /** Last success of the oldest stale source (Unix ms); null while fresh. */
  since: number | null
  /** The panel is out of reach (browser offline, failed fetch, gateway error without a body). */
  offline: boolean
  /** The error of the oldest stale source; null while it only waits for the network. */
  error: unknown
  /** Latest failed attempt (Unix ms, 0 if none): the reference for "today" when formatting `since`. */
  checkedAt: number
  retrying: boolean
  retry: () => void
}

/** Cached data whose last refresh failed or is paused offline; without data there is nothing to mark. */
export function isStale(q: FreshnessSource): boolean {
  return q.data !== undefined && (q.isError || q.isPaused)
}

function isOffline(q: FreshnessSource): boolean {
  if (!q.isError) return q.isPaused
  // Caddy answers 502–504 without the panel's JSON body while the panel is down.
  if (q.error instanceof ApiError) return q.error.code === 'unknown' && q.error.status >= 502 && q.error.status <= 504
  return true
}

/**
 * Combines the queries behind one surface (issue #155): it is stale as soon as one of them is, since the oldest
 * success among the stale ones. Pass null/false for queries that do not apply.
 */
export function freshnessOf(...sources: (FreshnessSource | null | undefined | false)[]): Freshness {
  const stale = sources.filter((q): q is FreshnessSource => !!q && isStale(q))
  if (stale.length === 0) return { stale: false, since: null, offline: false, error: null, checkedAt: 0, retrying: false, retry: () => {} }
  const oldest = stale.reduce((a, b) => (b.dataUpdatedAt < a.dataUpdatedAt ? b : a))
  return {
    stale: true,
    since: oldest.dataUpdatedAt,
    offline: stale.some(isOffline),
    error: oldest.isError ? oldest.error : null,
    checkedAt: Math.max(...stale.map((q) => q.errorUpdatedAt)),
    retrying: stale.some((q) => q.isFetching),
    retry: () => {
      for (const q of stale) void q.refetch()
    },
  }
}

function sameDay(a: number, b: number): boolean {
  return new Date(a).toDateString() === new Date(b).toDateString()
}

/** "14:32", or with the date once the data is from an earlier day than the last failed attempt. */
export function useSinceText(): (f: Freshness) => string {
  const fmt = useFormat()
  return (f) => {
    if (f.since === null) return ''
    return f.checkedAt === 0 || sameDay(f.since, f.checkedAt) ? fmt.time(f.since) : fmt.dateTime(f.since)
  }
}
