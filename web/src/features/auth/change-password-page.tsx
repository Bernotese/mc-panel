import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { AuthLayout } from './auth-layout'
import { pathForStage } from './guards'
import { LogoutButton } from './logout-button'
import { refreshMe, useMe } from './me'
import { PasswordChangeForm } from './password-change-form'

/**
 * Forced password change (stage `password`, SEC-45): an admin set this password, so the session can do nothing else
 * until the user chose their own. Afterwards the backend continues as `full`, or `setup` when 2FA is still missing.
 */
export function ChangePasswordPage() {
  const { t } = useTranslation()
  const me = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const finish = async () => {
    const next = await refreshMe(queryClient)
    await navigate({ to: next ? pathForStage(next.stage) : '/login' })
  }

  return (
    <AuthLayout title={t('change_password.title')} description={me ? t('change_password.description', { username: me.username }) : undefined}>
      <PasswordChangeForm submitLabel={t('change_password.submit')} onChanged={finish} />
      <div className="text-center">
        <LogoutButton />
      </div>
    </AuthLayout>
  )
}
