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
      <div className="grid gap-4 md:grid-cols-2">
        <LanguageCard />
        <PasswordCard />
        <TwoFactorCard />
        <RecoveryCard />
        <PasskeysCard />
        <SessionsCard />
        {me?.role === 'admin' ? <AdminSessionsCard /> : null}
      </div>
    </>
  )
}
