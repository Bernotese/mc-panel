// web/src/api/csrf.ts
/** Synchronizer token of the current session. Updated from every Me response; never persisted. */
let csrfToken = ''

export function getCsrfToken(): string {
  return csrfToken
}

export function setCsrfToken(token: string): void {
  csrfToken = token
}
