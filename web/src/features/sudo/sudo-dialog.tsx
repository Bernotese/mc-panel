import { ShieldCheck } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Me } from '@/api/types'
import { CodeForm } from '@/components/code-form'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useMe } from '@/features/auth/me'
import { isWebAuthnSupported, sudoWithPasskey } from '@/features/webauthn/webauthn'
import { errorMessage } from '@/lib/error-message'
import { GENERIC_SUDO_ACTION, sudoActionKey } from './sudo-actions'
import { settleSudo, useSudoActions, useSudoOpen } from './sudo-store'

/** Mounted fresh for every sudo request, so no state survives between requests. */
function SudoContent({ me }: { me: Me }) {
  const { t } = useTranslation()
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const totp = me.totp_enabled
  const passkey = me.passkey_count > 0 && isWebAuthnSupported()

  async function run(action: () => Promise<unknown>) {
    setPending(true)
    setError(null)
    try {
      await action()
      settleSudo(true)
    } catch (err) {
      setError(errorMessage(err, t))
      setPending(false)
    }
  }

  const totpForm = (
    <CodeForm
      label={t('sudo.code_label')}
      submitLabel={t('sudo.confirm')}
      numeric
      pending={pending}
      error={error}
      onSubmit={(code) => void run(() => unwrap(api.POST('/auth/sudo/totp', { body: { code } })))}
    />
  )
  const passkeyPanel = (
    <div className="space-y-3">
      <p className="text-muted-foreground text-sm">{t('sudo.passkey_hint')}</p>
      {error ? (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      ) : null}
      <Button type="button" className="w-full" disabled={pending} onClick={() => void run(sudoWithPasskey)}>
        {t('sudo.passkey_button')}
      </Button>
    </div>
  )

  let body = (
    <p role="alert" className="text-sm">
      {t('sudo.no_method')}
    </p>
  )
  if (totp && passkey) {
    body = (
      <Tabs defaultValue="totp">
        <TabsList className="grid w-full grid-cols-2">
          <TabsTrigger value="totp">{t('sudo.tab_totp')}</TabsTrigger>
          <TabsTrigger value="passkey">{t('sudo.tab_passkey')}</TabsTrigger>
        </TabsList>
        <TabsContent value="totp" className="pt-3">
          {totpForm}
        </TabsContent>
        <TabsContent value="passkey" className="pt-3">
          {passkeyPanel}
        </TabsContent>
      </Tabs>
    )
  } else if (totp) {
    body = totpForm
  } else if (passkey) {
    body = passkeyPanel
  }

  return (
    <div className="space-y-4">
      {body}
      <Button type="button" variant="outline" className="w-full" onClick={() => settleSudo(false)}>
        {t('common.cancel')}
      </Button>
    </div>
  )
}

/** Names what the confirmation is for (SEC-36): one action, or the count and list of concurrent ones. */
function SudoActions() {
  const { t } = useTranslation()
  const keys = useSudoActions().map(sudoActionKey)
  // An unknown single action adds nothing to the title ("Aktion bestätigen").
  if (keys.length === 0 || (keys.length === 1 && keys[0] === GENERIC_SUDO_ACTION)) return null
  const labels = keys.map((k) => t(k))
  return (
    <p className="text-foreground text-sm font-medium">
      {labels.length === 1 ? t('sudo.subject_single', { action: labels[0] }) : t('sudo.subject_multiple', { n: labels.length, actions: labels.join(', ') })}
    </p>
  )
}

/** Global confirmation dialog; opened by the API middleware on 403 sudo_required. */
export function SudoDialog() {
  const { t } = useTranslation()
  const open = useSudoOpen()
  const me = useMe()
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) settleSudo(false)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ShieldCheck className="text-primary size-5 shrink-0" aria-hidden="true" />
            {t('sudo.title')}
          </DialogTitle>
          <DialogDescription>{t('sudo.description')}</DialogDescription>
          <SudoActions />
        </DialogHeader>
        {open && me ? (
          <div className="pt-2">
            <SudoContent me={me} />
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  )
}
