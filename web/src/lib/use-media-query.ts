// web/src/lib/use-media-query.ts
import { useCallback, useSyncExternalStore } from 'react'

/** md breakpoint of Tailwind; below it tables collapse into cards and the sidebar becomes a sheet. */
export const DESKTOP_QUERY = '(min-width: 768px)'

export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      const mql = window.matchMedia(query)
      mql.addEventListener('change', onChange)
      return () => mql.removeEventListener('change', onChange)
    },
    [query],
  )
  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches, () => false)
}
