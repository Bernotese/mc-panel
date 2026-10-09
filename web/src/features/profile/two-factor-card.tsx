import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrapVoid } from '@/api/errors'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { refreshMe, useMe } from '@/features/auth/me'
import { RecoveryCodesPanel } from '@/features/setup/recovery-codes-panel'
import { TotpSetupCard } from '@/features/setup/totp-setup-card'
import { useErrorToast } from '@/lib/error-message'
import { SectionCard } from './section-card'

export function TwoFactorCard() {
  const { t } = useTranslation()
  const me = useMe()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [setupOpen, setSetupOpen] = useState(false)
  const [codes, setCodes] = useState<string[] | null>(null)
  const [confirmDisable, setConfirmDisable] = useState(false)
  const enabled = me?.totp_enabled === true

  const disable = useMutation({
    mutationFn: () => unwrapVoid(api.DELETE('/me/totp')),
    onSuccess: async () => {
      setConfirmDisable(false)
      toast.success(t('profile.totp.disabled_done'))
      await refreshMe(queryClient)
    },
    onError: (err) => {
      setConfirmDisable(false)
      notifyError(err)
    },
  })

  const closeSetup = () => {
    setSetupOpen(false)
    setCodes(null)
  }

  return (
    <SectionCard
      title={t('profile.totp.title')}
      actions={<Badge variant={enabled ? 'secondary' : 'outline'}>{enabled ? t('profile.totp.enabled') : t('profile.totp.disabled')}</Badge>}
    >
      {enabled ? (
        <Button type="button" variant="outline" onClick={() => setConfirmDisable(true)}>
          {t('profile.totp.disable')}
        </Button>
      ) : (
        <Button type="button" onClick={() => setSetupOpen(true)}>
          {t('profile.totp.setup')}
        </Button>
      )}
      <Dialog open={setupOpen} onOpenChange={(open) => (open ? setSetupOpen(true) : closeSetup())}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('profile.totp.setup_title')}</DialogTitle>
            <DialogDescription>{t('profile.totp.setup_description')}</DialogDescription>
          </DialogHeader>
          {codes ? (
            <RecoveryCodesPanel codes={codes} doneLabel={t('profile.recovery.done')} onDone={closeSetup} />
          ) : (
            <TotpSetupCard
              onEnabled={(newCodes) => {
                toast.success(t('profile.totp.enabled_done'))
                void refreshMe(queryClient)
                if (newCodes.length > 0) setCodes(newCodes)
                else closeSetup()
              }}
            />
          )}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={confirmDisable}
        onOpenChange={setConfirmDisable}
        title={t('profile.totp.disable_title')}
        description={t('profile.totp.disable_description')}
        confirmLabel={t('profile.totp.disable')}
        destructive
        pending={disable.isPending}
        onConfirm={() => disable.mutate()}
      />
    </SectionCard>
  )
}
