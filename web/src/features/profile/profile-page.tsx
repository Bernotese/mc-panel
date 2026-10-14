// web/src/features/profile/profile-page.tsx
import { useTranslation } from 'react-i18next'
import { PageHeader } from '@/components/page-header'
import { useMe } from '@/features/auth/me'
import { AdminSessionsCard } from './admin-sessions-card'
import { LanguageCard } from './language-card'
import { PasskeysCard } from './passkeys-card'
import { PasswordCard } from './password-card'
import { RecoveryCard } from './recovery-card'
import { SessionsCard } from './sessions-card'
import { TwoFactorCard } from './two-factor-card'

export function ProfilePage() {
  const { t } = useTranslation()
  const me = useMe()
  return (
    <>
      <PageHeader title={t('profile.title')} description={me ? t('nav.signed_in_as', { username: me.username }) : t('profile.description')} />
      {/* Account settings in two columns on wide content areas; the session tables below use the full width. */}
      <div className="@container/profile flex flex-col gap-6">
        <div className="grid gap-6 @4xl/profile:grid-cols-2 @4xl/profile:items-start">
          <div className="flex min-w-0 flex-col gap-6">
            <LanguageCard />
            <PasswordCard />
          </div>
          <div className="flex min-w-0 flex-col gap-6">
            <TwoFactorCard />
            <RecoveryCard />
            <PasskeysCard />
          </div>
        </div>
        <SessionsCard />
        {me?.role === 'admin' ? <AdminSessionsCard /> : null}
      </div>
    </>
  )
}
