// web/src/api/handlers.ts
import type { Me } from './types'

/** The request that answered 403 sudo_required (method and OpenAPI path template); names the action in the dialog. */
export interface SudoRequest {
  method: string
  path: string
}

export interface ApiHandlers {
  onUnauthorized(): void
  /** May be async: the middleware waits for it before handing the 403 back to the caller. */
  onSetupRequired(): void | Promise<void>
  /** Like onSetupRequired, for 403 password_change_required (an admin set the password, SEC-45). */
  onPasswordChangeRequired(): void | Promise<void>
  requestSudo(request: SudoRequest): Promise<boolean>
  onMe(me: Me): void
}

const defaults: ApiHandlers = {
  onUnauthorized: () => undefined,
  onSetupRequired: () => undefined,
  onPasswordChangeRequired: () => undefined,
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
