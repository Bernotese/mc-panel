import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Passkey, PasskeyCreated } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataList, type Column } from '@/components/data-list'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { refreshMe } from '@/features/auth/me'
import { PasskeySetupCard } from '@/features/setup/passkey-setup-card'
import { RecoveryCodesPanel } from '@/features/setup/recovery-codes-panel'
import { isWebAuthnSupported } from '@/features/webauthn/webauthn'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { SectionCard } from './section-card'
import { passkeysKey } from './query-keys'

export function PasskeysCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const supported = isWebAuthnSupported()
  const [adding, setAdding] = useState(false)
  const [codes, setCodes] = useState<string[] | null>(null)
  const [deleting, setDeleting] = useState<Passkey | null>(null)
  const passkeys = useQuery({ queryKey: passkeysKey, queryFn: () => unwrap(api.GET('/me/passkeys')) })

  const remove = useMutation({
    mutationFn: (passkey: Passkey) => unwrapVoid(api.DELETE('/me/passkeys/{id}', { params: { path: { id: passkey.id } } })),
    onSuccess: async (_data, passkey) => {
      setDeleting(null)
      toast.success(t('profile.passkeys.deleted', { name: passkey.name }))
      await queryClient.invalidateQueries({ queryKey: passkeysKey })
      await refreshMe(queryClient)
    },
    onError: (err) => {
      setDeleting(null)
      notifyError(err)
    },
  })

  const onRegistered = (created: PasskeyCreated) => {
    toast.success(t('profile.passkeys.added', { name: created.passkey.name }))
    void queryClient.invalidateQueries({ queryKey: passkeysKey })
    void refreshMe(queryClient)
    if (created.recovery_codes.length > 0) setCodes(created.recovery_codes)
    else setAdding(false)
  }
  const closeAdd = () => {
    setAdding(false)
    setCodes(null)
  }

  const columns: Column<Passkey>[] = [
    { id: 'name', header: t('profile.passkeys.col.name'), cell: (p) => <span className="font-medium">{p.name}</span> },
    { id: 'created', header: t('profile.passkeys.col.created'), cell: (p) => fmt.dateTime(p.created_at) },
    {
      id: 'last_used',
      header: t('profile.passkeys.col.last_used'),
      cell: (p) => (p.last_used_at === null ? t('profile.passkeys.never') : fmt.relative(p.last_used_at)),
    },
    {
      id: 'actions',
      header: t('profile.passkeys.col.actions'),
      cell: (p) => (
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="text-destructive"
          aria-label={t('profile.passkeys.delete_for', { name: p.name })}
          onClick={() => setDeleting(p)}
        >
          {t('common.delete')}
        </Button>
      ),
    },
  ]

  return (
    <SectionCard
      title={t('profile.passkeys.title')}
      className="md:col-span-2"
      actions={
        supported ? (
          <Button type="button" size="sm" onClick={() => setAdding(true)}>
            {t('profile.passkeys.add')}
          </Button>
        ) : undefined
      }
    >
      {!supported ? <p className="text-muted-foreground mb-3 text-sm">{t('passkey.unsupported')}</p> : null}
      {passkeys.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(passkeys.error, t)}
        </p>
      ) : null}
      {passkeys.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList items={passkeys.data ?? []} columns={columns} rowKey={(p) => p.id} caption={t('profile.passkeys.caption')} empty={t('profile.passkeys.empty')} />
      )}
      <Dialog open={adding} onOpenChange={(open) => (open ? setAdding(true) : closeAdd())}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('profile.passkeys.add')}</DialogTitle>
            <DialogDescription>{t('profile.passkeys.add_description')}</DialogDescription>
          </DialogHeader>
          {codes ? (
            <RecoveryCodesPanel codes={codes} doneLabel={t('profile.recovery.done')} onDone={closeAdd} />
          ) : (
            <PasskeySetupCard onRegistered={onRegistered} />
          )}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(open) => !open && setDeleting(null)}
        title={t('profile.passkeys.delete_title', { name: deleting?.name ?? '' })}
        description={t('profile.passkeys.delete_description')}
        confirmLabel={t('common.delete')}
        destructive
        pending={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </SectionCard>
  )
}
