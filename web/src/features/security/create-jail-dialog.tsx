import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { errorMessage } from '@/lib/error-message'

export function CreateJailDialog({ open, onOpenChange }: { open: boolean; onOpenChange(open: boolean): void }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const [ip, setIp] = useState('')
  const [duration, setDuration] = useState('60')
  const [reason, setReason] = useState('')
  const minutes = Number(duration)
  const durationInvalid = !Number.isInteger(minutes) || minutes < 1 || minutes > 10080
  const complete = ip.trim() !== '' && !durationInvalid && reason.length <= 200

  const create = useMutation({
    mutationFn: () => unwrap(api.POST('/security/jails', { body: { ip: ip.trim(), duration_minutes: minutes, reason: reason.trim() } })),
    onSuccess: (jail) => {
      toast.success(t('security.jails.created', { ip: jail.ip_key }))
      setIp('')
      setReason('')
      setDuration('60')
      onOpenChange(false)
      void queryClient.invalidateQueries({ queryKey: ['security'] })
    },
  })

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) create.reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t('security.jails.create')}</DialogTitle>
          <DialogDescription>{t('security.jails.create_description')}</DialogDescription>
        </DialogHeader>
        <form
          className="space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault()
            if (complete) create.mutate()
          }}
        >
          <div className="space-y-2">
            <Label htmlFor={`${id}-ip`}>{t('security.jails.ip')}</Label>
            <Input id={`${id}-ip`} value={ip} onChange={(e) => setIp(e.target.value)} autoComplete="off" spellCheck={false} />
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-duration`}>{t('security.jails.duration')}</Label>
            <Input
              id={`${id}-duration`}
              type="number"
              inputMode="numeric"
              min={1}
              max={10080}
              value={duration}
              onChange={(e) => setDuration(e.target.value)}
              aria-invalid={durationInvalid || undefined}
            />
            {durationInvalid ? <p className="text-destructive text-sm">{t('security.jails.duration_invalid')}</p> : null}
          </div>
          <div className="space-y-2">
            <Label htmlFor={`${id}-reason`}>{t('security.jails.reason')}</Label>
            <Input id={`${id}-reason`} value={reason} maxLength={200} onChange={(e) => setReason(e.target.value)} />
          </div>
          {create.error ? (
            <p role="alert" className="text-destructive text-sm">
              {errorMessage(create.error, t)}
            </p>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t('common.cancel')}
            </Button>
            <Button type="submit" variant="destructive" disabled={create.isPending || !complete}>
              {t('security.jails.create')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
