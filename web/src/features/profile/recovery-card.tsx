import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { refreshMe, useMe } from '@/features/auth/me'
import { RecoveryCodesPanel } from '@/features/setup/recovery-codes-panel'
import { useErrorToast } from '@/lib/error-message'
import { SectionCard } from './section-card'

export function RecoveryCard() {
  const { t } = useTranslation()
  const me = useMe()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [confirm, setConfirm] = useState(false)
  const [codes, setCodes] = useState<string[] | null>(null)
  const regenerate = useMutation({
    mutationFn: () => unwrap(api.POST('/me/recovery-codes')),
    onSuccess: (result) => {
      setConfirm(false)
      setCodes(result.codes)
      void refreshMe(queryClient)
    },
    onError: (err) => {
      setConfirm(false)
      notifyError(err)
    },
  })

  if (!me || (!me.totp_enabled && me.passkey_count === 0)) return null

  return (
    <SectionCard title={t('profile.recovery.title')}>
      <div className="space-y-3">
        <p className="text-sm">{t('profile.recovery.remaining', { remaining: me.recovery_codes_remaining })}</p>
        <Button type="button" variant="outline" onClick={() => setConfirm(true)}>
          {t('profile.recovery.regenerate')}
        </Button>
      </div>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={t('profile.recovery.regenerate_title')}
        description={t('profile.recovery.regenerate_description')}
        confirmLabel={t('profile.recovery.regenerate')}
        pending={regenerate.isPending}
        onConfirm={() => regenerate.mutate()}
      />
      <Dialog open={codes !== null} onOpenChange={(open) => !open && setCodes(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('profile.recovery.new_codes_title')}</DialogTitle>
            <DialogDescription>{t('recovery.description')}</DialogDescription>
          </DialogHeader>
          {codes ? <RecoveryCodesPanel codes={codes} doneLabel={t('profile.recovery.done')} onDone={() => setCodes(null)} /> : null}
        </DialogContent>
      </Dialog>
    </SectionCard>
  )
}
