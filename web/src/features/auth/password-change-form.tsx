import { useMutation } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { isApiError, unwrapVoid } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { cn } from '@/lib/utils'

/**
 * Current, new and repeated password for POST /me/password; used by the profile card and the forced change after an
 * admin set the password (SEC-45). The fields are cleared before onChanged runs.
 */
export function PasswordChangeForm({ submitLabel, onChanged, className }: { submitLabel: string; onChanged(): void | Promise<void>; className?: string }) {
  const { t } = useTranslation()
  const id = useId()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const tooShort = next !== '' && (next.length < 12 || next.length > 256)
  const same = next !== '' && next === current
  const mismatch = confirm !== '' && confirm !== next
  const complete = current !== '' && next !== '' && confirm !== '' && !tooShort && !same && !mismatch

  const change = useMutation({
    mutationFn: () => unwrapVoid(api.POST('/me/password', { body: { current_password: current, new_password: next } })),
    onSuccess: async () => {
      setCurrent('')
      setNext('')
      setConfirm('')
      await onChanged()
    },
  })
  const error = change.error
    ? isApiError(change.error, 'invalid_credentials')
      ? t('profile.password.wrong_current')
      : errorMessage(change.error, t)
    : null
  const newHint = tooShort ? t('profile.password.too_short') : same ? t('profile.password.same') : null

  return (
    <form
      className={cn('space-y-4', className)}
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (complete) change.mutate()
      }}
    >
      <div className="space-y-2">
        <Label htmlFor={`${id}-current`}>{t('profile.password.current')}</Label>
        <Input id={`${id}-current`} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
      </div>
      <div className="space-y-2">
        <Label htmlFor={`${id}-new`}>{t('profile.password.new')}</Label>
        <Input
          id={`${id}-new`}
          type="password"
          autoComplete="new-password"
          value={next}
          maxLength={256}
          aria-invalid={newHint !== null || undefined}
          onChange={(e) => setNext(e.target.value)}
        />
        {newHint ? <p className="text-destructive text-sm">{newHint}</p> : null}
      </div>
      <div className="space-y-2">
        <Label htmlFor={`${id}-confirm`}>{t('profile.password.confirm')}</Label>
        <Input
          id={`${id}-confirm`}
          type="password"
          autoComplete="new-password"
          value={confirm}
          aria-invalid={mismatch || undefined}
          onChange={(e) => setConfirm(e.target.value)}
        />
        {mismatch ? <p className="text-destructive text-sm">{t('profile.password.mismatch')}</p> : null}
      </div>
      {error ? (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      ) : null}
      <Button type="submit" disabled={change.isPending || !complete}>
        {submitLabel}
      </Button>
    </form>
  )
}
