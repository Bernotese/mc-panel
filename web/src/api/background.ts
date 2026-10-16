import type { TimeoutProvider } from '@tanstack/react-query'

/** Header of polling requests: the panel serves them but does not extend the session's idle time (SEC-19). */
export const BACKGROUND_HEADER = 'X-MCPanel-Background'

let depth = 0

/** True while a background callback runs; the API middleware reads it synchronously in onRequest. */
export function isBackground(): boolean {
  return depth > 0
}

/** Runs fn and marks every API request it starts synchronously as background. */
export function runInBackground<T>(fn: () => T): T {
  depth += 1
  try {
    return fn()
  } finally {
    depth -= 1
  }
}

/**
 * TanStack Query runs `refetchInterval` through timeoutManager.setInterval and starts the queryFn synchronously in
 * that callback, so wrapping the callback marks exactly the polls (background.test.ts guards this on upgrades).
 * Window-focus refetches are off app-wide (query-client.ts); initial loads and refetches after a user action never
 * go through setInterval.
 */
export const backgroundTimeoutProvider: TimeoutProvider<number> = {
  setTimeout: (callback, delay) => window.setTimeout(callback, delay),
  clearTimeout: (id) => window.clearTimeout(id),
  setInterval: (callback, delay) => window.setInterval(() => runInBackground(callback), delay),
  clearInterval: (id) => window.clearInterval(id),
}
