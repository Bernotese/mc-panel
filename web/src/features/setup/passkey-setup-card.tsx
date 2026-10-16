import { useMutation } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PasskeyCreated } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useMe } from '@/features/auth/me'
import { isWebAuthnSupported, registerPasskey } from '@/features/webauthn/webauthn'
import { CurrentPasswordField } from './current-password-field'
import { enrolErrorMessage, isFirstFactor } from './first-factor'

export function PasskeySetupCard({ onRegistered }: { onRegistered(created: PasskeyCreated): void }) {
  const { t } = useTranslation()
  const id = useId()
  const [name, setName] = useState(() => t('passkey.default_name'))
  const needsPassword = isFirstFactor(useMe())
  const [password, setPassword] = useState('')
  const [passwordMissing, setPasswordMissing] = useState(false)
  const register = useMutation({
    mutationFn: (passkeyName: string) => registerPasskey(passkeyName, needsPassword ? password : undefined),
    onSuccess: (created) => onRegistered(created),
  })

  if (!isWebAuthnSupported()) {
    return <p className="text-muted-foreground text-sm">{t('passkey.unsupported')}</p>
  }

  const trimmed = name.trim()
  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault()
        setPasswordMissing(needsPassword && password === '')
        if (trimmed && (!needsPassword || password !== '')) register.mutate(trimmed)
      }}
    >
      <p className="text-muted-foreground text-sm">{t('passkey.intro')}</p>
      <div className="space-y-2">
        <Label htmlFor={id}>{t('passkey.name_label')}</Label>
        <Input id={id} value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
      </div>
      {needsPassword ? <CurrentPasswordField value={password} onChange={setPassword} /> : null}
      {passwordMissing || register.error ? (
        <p role="alert" className="text-destructive text-sm">
          {passwordMissing ? t('first_factor.password_required') : enrolErrorMessage(register.error, t)}
        </p>
      ) : null}
      <Button type="submit" className="w-full" disabled={register.isPending || !trimmed}>
        {t('passkey.register')}
      </Button>
    </form>
  )
}
