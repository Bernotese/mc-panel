import { queryOptions, useQuery, type QueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { setCsrfToken } from '@/api/csrf'
import { isApiError, unwrap } from '@/api/errors'
import type { Me } from '@/api/types'
import { setLocale } from '@/i18n'

export const meKey = ['me'] as const

export async function fetchMe(): Promise<Me | null> {
  try {
    return await unwrap(api.GET('/auth/me'))
  } catch (err) {
    if (isApiError(err) && err.status === 401) return null
    throw err
  }
}

export const meQueryOptions = queryOptions({ queryKey: meKey, queryFn: fetchMe, staleTime: 60_000 })

/** Reads the cached Me only. Route guards load it; the API middleware keeps it fresh after every stage change. */
export function useMe(): Me | null {
  return useQuery({ ...meQueryOptions, enabled: false }).data ?? null
}

export function applyMe(queryClient: QueryClient, me: Me): void {
  setCsrfToken(me.csrf_token)
  queryClient.setQueryData(meKey, me)
  setLocale(me.locale)
}

export function loadMe(queryClient: QueryClient): Promise<Me | null> {
  return queryClient.ensureQueryData(meQueryOptions)
}

export function refreshMe(queryClient: QueryClient): Promise<Me | null> {
  return queryClient.fetchQuery({ ...meQueryOptions, staleTime: 0 })
}

export function clearSession(queryClient: QueryClient): void {
  setCsrfToken('')
  queryClient.setQueryData(meKey, null)
  queryClient.removeQueries({ predicate: (q) => q.queryKey[0] !== meKey[0] })
}
