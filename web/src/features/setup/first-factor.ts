import type { TFunction } from 'i18next'
import type { Me } from '@/api/types'
import { isApiError } from '@/api/errors'
import { errorMessage } from '@/lib/error-message'

/** SEC-15: enrolling the first second factor needs the current password; later ones need sudo. */
export function isFirstFactor(me: Me | null): boolean {
  return me !== null && !me.totp_enabled && me.passkey_count === 0
}

/** Like errorMessage, but a wrong current password is not reported as a failed login. */
export function enrolErrorMessage(err: unknown, t: TFunction): string {
  if (isApiError(err, 'invalid_credentials')) return t('first_factor.password_wrong')
  return errorMessage(err, t)
}
