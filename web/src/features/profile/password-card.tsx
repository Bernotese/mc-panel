import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { isApiError, unwrapVoid } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'
import { SectionCard } from './section-card'

export function PasswordCard() {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const tooShort = next !== '' && (next.length < 12 || next.length > 256)
  const mismatch = confirm !== '' && confirm !== next
  const complete = current !== '' && next !== '' && confirm !== '' && !tooShort && !mismatch

  const change = useMutation({
    mutationFn: () => unwrapVoid(api.POST('/me/password', { body: { current_password: current, new_password: next } })),
    onSuccess: () => {
      setCurrent('')
      setNext('')
      setConfirm('')
      toast.success(t('profile.password.changed'))
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
    },
  })
  const error = change.error
    ? isApiError(change.error, 'invalid_credentials')
      ? t('profile.password.wrong_current')
      : errorMessage(change.error, t)
    : null

  return (
    <SectionCard title={t('profile.password.title')} description={t('profile.password.hint')}>
      <form
        className="space-y-4"
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
            aria-invalid={tooShort || undefined}
            onChange={(e) => setNext(e.target.value)}
          />
          {tooShort ? <p className="text-destructive text-sm">{t('profile.password.too_short')}</p> : null}
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
          {t('profile.password.submit')}
        </Button>
      </form>
    </SectionCard>
  )
}
