// web/src/features/auth/use-logout.ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { isApiError, unwrapVoid } from '@/api/errors'
import { clearSession, refreshMe } from './me'

const FAILED_TOAST = 'logout-failed'

/**
 * Signs out on the server. The sign-in page only appears once the server confirmed that the session ended: the
 * session cookie is HttpOnly, so the browser keeps sending it until the server revokes the session. A logout that
 * fails (network, 429, 5xx) leaves the user signed in and offers a retry instead of pretending otherwise.
 */
export function useLogout() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const { t } = useTranslation()

  const signedOut = async () => {
    toast.dismiss(FAILED_TOAST)
    clearSession(queryClient)
    await navigate({ to: '/login' })
  }

  const mutation = useMutation({
    mutationFn: () => unwrapVoid(api.POST('/auth/logout')),
    onSuccess: signedOut,
    onError: async (err) => {
      // 401: the session had already ended (expired or revoked), which is a confirmed sign-out as well.
      if (isApiError(err) && err.status === 401) return signedOut()
      // Ask the server whether the session still exists; this also refreshes a stale CSRF token for the retry.
      const me = await refreshMe(queryClient).catch(() => undefined)
      if (me === null) return signedOut()
      toast.error(t('auth.logout_failed'), {
        id: FAILED_TOAST,
        duration: Infinity,
        action: { label: t('auth.logout_retry'), onClick: () => mutation.mutate() },
      })
    },
  })
  return mutation
}
