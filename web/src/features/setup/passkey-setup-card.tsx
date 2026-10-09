import { useMutation } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PasskeyCreated } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { isWebAuthnSupported, registerPasskey } from '@/features/webauthn/webauthn'
import { errorMessage } from '@/lib/error-message'

export function PasskeySetupCard({ onRegistered }: { onRegistered(created: PasskeyCreated): void }) {
  const { t } = useTranslation()
  const id = useId()
  const [name, setName] = useState(() => t('passkey.default_name'))
  const register = useMutation({
    mutationFn: (passkeyName: string) => registerPasskey(passkeyName),
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
        if (trimmed) register.mutate(trimmed)
      }}
    >
      <p className="text-muted-foreground text-sm">{t('passkey.intro')}</p>
      <div className="space-y-2">
        <Label htmlFor={id}>{t('passkey.name_label')}</Label>
        <Input id={id} value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
      </div>
      {register.error ? (
        <p role="alert" className="text-destructive text-sm">
          {errorMessage(register.error, t)}
        </p>
      ) : null}
      <Button type="submit" className="w-full" disabled={register.isPending || !trimmed}>
        {t('passkey.register')}
      </Button>
    </form>
  )
}
