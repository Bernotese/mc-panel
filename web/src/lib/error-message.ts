import type { TFunction } from 'i18next'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError } from '@/api/errors'

const DETAIL_CODES: ReadonlySet<string> = new Set(['bad_request', 'validation_failed', 'conflict'])

/** NotAllowedError/AbortError: user cancelled the browser dialog or it timed out. */
export function isWebAuthnCancelled(err: unknown): boolean {
  if (typeof err !== 'object' || err === null) return false
  const { name, code } = err as { name?: unknown; code?: unknown }
  return name === 'NotAllowedError' || name === 'AbortError' || code === 'ERROR_CEREMONY_ABORTED'
}

export function errorMessage(err: unknown, t: TFunction): string {
  if (isWebAuthnCancelled(err)) return t('webauthn.cancelled')
  if (isApiError(err)) {
    if (err.code === 'unknown') return t('errors.unknown', { status: err.status })
    if (err.code === 'rate_limited' && err.retryAfterSeconds) return t('errors.rate_limited_retry', { seconds: err.retryAfterSeconds })
    const base = t(`errors.${err.code}`)
    return DETAIL_CODES.has(err.code) && err.message && err.message !== err.code ? `${base} (${err.message})` : base
  }
  return t('errors.network')
}

export function useErrorToast(): (err: unknown) => void {
  const { t } = useTranslation()
  return useCallback((err: unknown) => {
    toast.error(errorMessage(err, t))
  }, [t])
}
