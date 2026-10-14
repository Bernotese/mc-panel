import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Me } from '@/api/types'
import { CodeForm } from '@/components/code-form'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { isWebAuthnSupported, loginWithPasskey } from '@/features/webauthn/webauthn'
import { errorMessage } from '@/lib/error-message'
import { AuthLayout } from './auth-layout'
import { pathForStage } from './guards'
import { LogoutButton } from './logout-button'
import { applyMe, useMe } from './me'

export type SecondFactorMethod = 'totp' | 'webauthn' | 'recovery'

// eslint-disable-next-line react-refresh/only-export-components -- pure helper kept next to the page that uses it
export function availableMethods(me: Me, webauthnSupported: boolean): SecondFactorMethod[] {
  return me.second_factors.filter((f) => f !== 'webauthn' || webauthnSupported)
}

export function SecondFactorPage() {
  const { t } = useTranslation()
  const me = useMe()
  const navigate = useNavigate()
  const queryClient = useQueryClient()

  const finish = async (next: Me) => {
    applyMe(queryClient, next)
    await navigate({ to: pathForStage(next.stage) })
  }
  const totp = useMutation({ mutationFn: (code: string) => unwrap(api.POST('/auth/2fa/totp', { body: { code } })), onSuccess: finish })
  const recovery = useMutation({ mutationFn: (code: string) => unwrap(api.POST('/auth/2fa/recovery', { body: { code } })), onSuccess: finish })
  const passkey = useMutation({ mutationFn: loginWithPasskey, onSuccess: finish })

  const methods = me ? availableMethods(me, isWebAuthnSupported()) : []

  return (
    <AuthLayout title={t('second_factor.title')} description={me ? t('second_factor.description', { username: me.username }) : undefined}>
      {methods.length === 0 ? (
        <p role="alert" className="text-sm">
          {t('second_factor.no_method')}
        </p>
      ) : (
        <Tabs defaultValue={methods[0]}>
          {methods.length > 1 ? (
            <TabsList className="flex w-full flex-wrap group-data-horizontal/tabs:h-auto">
              {methods.map((m) => (
                <TabsTrigger key={m} value={m} className="flex-1">
                  {t(`second_factor.tab_${m}`)}
                </TabsTrigger>
              ))}
            </TabsList>
          ) : null}
          {methods.includes('totp') ? (
            <TabsContent value="totp" className="pt-4">
              <CodeForm
                label={t('second_factor.totp_label')}
                submitLabel={t('second_factor.verify')}
                numeric
                pending={totp.isPending}
                error={totp.error ? errorMessage(totp.error, t) : null}
                onSubmit={(code) => totp.mutate(code)}
              />
            </TabsContent>
          ) : null}
          {methods.includes('webauthn') ? (
            <TabsContent value="webauthn" className="space-y-3 pt-4">
              <p className="text-muted-foreground text-sm">{t('second_factor.passkey_hint')}</p>
              {passkey.error ? (
                <p role="alert" className="text-destructive text-sm">
                  {errorMessage(passkey.error, t)}
                </p>
              ) : null}
              <Button type="button" className="w-full" disabled={passkey.isPending} onClick={() => passkey.mutate()}>
                {t('second_factor.passkey_button')}
              </Button>
            </TabsContent>
          ) : null}
          {methods.includes('recovery') ? (
            <TabsContent value="recovery" className="pt-4">
              <CodeForm
                label={t('second_factor.recovery_label')}
                submitLabel={t('second_factor.verify')}
                pending={recovery.isPending}
                error={recovery.error ? errorMessage(recovery.error, t) : null}
                onSubmit={(code) => recovery.mutate(code)}
              />
            </TabsContent>
          ) : null}
        </Tabs>
      )}
      <div className="mt-6 text-center">
        <LogoutButton label={t('auth.other_account')} />
      </div>
    </AuthLayout>
  )
}
