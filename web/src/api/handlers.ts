// web/src/api/handlers.ts
import type { Me } from './types'

export interface ApiHandlers {
  onUnauthorized(): void
  /** May be async: the middleware waits for it before handing the 403 back to the caller. */
  onSetupRequired(): void | Promise<void>
  requestSudo(): Promise<boolean>
  onMe(me: Me): void
}

const defaults: ApiHandlers = {
  onUnauthorized: () => undefined,
  onSetupRequired: () => undefined,
  requestSudo: () => Promise.resolve(false),
  onMe: () => undefined,
}

let current: ApiHandlers = defaults

export function configureApiHandlers(overrides: Partial<ApiHandlers>): void {
  current = { ...current, ...overrides }
}

export function resetApiHandlers(): void {
  current = defaults
}

export function apiHandlers(): ApiHandlers {
  return current
}
