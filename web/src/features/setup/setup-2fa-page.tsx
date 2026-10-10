import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { AuthLayout } from '@/features/auth/auth-layout'
import { LogoutButton } from '@/features/auth/logout-button'
import { useMe } from '@/features/auth/me'
import { isWebAuthnSupported } from '@/features/webauthn/webauthn'
import { PasskeySetupCard } from './passkey-setup-card'
import { RecoveryCodesPanel } from './recovery-codes-panel'
import { TotpSetupCard } from './totp-setup-card'

/**
 * Forced setup. The recovery codes live in local state, so they stay visible even though the API
 * middleware has already refreshed Me to stage "full" – the route guard only runs on navigation.
 */
export function Setup2faPage() {
  const { t } = useTranslation()
  const me = useMe()
  const navigate = useNavigate()
  const [codes, setCodes] = useState<string[] | null>(null)
  const passkeySupported = isWebAuthnSupported()

  const finish = () => void navigate({ to: '/' })
  const onFactorAdded = (newCodes: string[]) => {
    if (newCodes.length > 0) setCodes(newCodes)
    else finish()
  }

  if (codes) {
    return (
      <AuthLayout wide title={t('setup.codes_title')}>
        <RecoveryCodesPanel codes={codes} doneLabel={t('setup.finish')} onDone={finish} />
      </AuthLayout>
    )
  }

  return (
    <AuthLayout wide title={t('setup.title')} description={me?.role === 'admin' ? t('setup.description_admin') : t('setup.description_user')}>
      <Tabs defaultValue="totp">
        {passkeySupported ? (
          <TabsList className="grid w-full grid-cols-2">
            <TabsTrigger value="totp">{t('setup.tab_totp')}</TabsTrigger>
            <TabsTrigger value="passkey">{t('setup.tab_passkey')}</TabsTrigger>
          </TabsList>
        ) : null}
        <TabsContent value="totp" className="pt-4">
          <TotpSetupCard onEnabled={onFactorAdded} />
        </TabsContent>
        {passkeySupported ? (
          <TabsContent value="passkey" className="pt-4">
            <PasskeySetupCard onRegistered={(created) => onFactorAdded(created.recovery_codes)} />
          </TabsContent>
        ) : null}
      </Tabs>
      <div className="mt-6 text-center">
        <LogoutButton />
      </div>
    </AuthLayout>
  )
}
