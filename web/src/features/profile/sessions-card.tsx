// web/src/features/profile/sessions-card.tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Session } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { DataList } from '@/components/data-list'
import { Section } from '@/components/section'
import { Button } from '@/components/ui/button'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { buildSessionColumns, useDeviceLabel } from './session-columns'
import { mySessionsKey } from './query-keys'

export function SessionsCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const deviceLabel = useDeviceLabel()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [confirmOthers, setConfirmOthers] = useState(false)
  const sessions = useQuery({ queryKey: mySessionsKey, queryFn: () => unwrap(api.GET('/me/sessions')) })
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['sessions'] })

  const revoke = useMutation({
    mutationFn: (session: Session) => unwrapVoid(api.DELETE('/me/sessions/{id}', { params: { path: { id: session.id } } })),
    onSuccess: () => {
      toast.success(t('profile.sessions.revoked'))
      void invalidate()
    },
    onError: notifyError,
  })
  const revokeOthers = useMutation({
    mutationFn: () => unwrapVoid(api.POST('/me/sessions/revoke-others')),
    onSuccess: () => {
      setConfirmOthers(false)
      toast.success(t('profile.sessions.revoked_others'))
      void invalidate()
    },
    onError: notifyError,
  })
  const hasOthers = (sessions.data ?? []).some((s) => !s.current)

  return (
    <Section
      title={t('profile.sessions.title')}
     
      actions={
        <Button type="button" variant="outline" size="sm" disabled={!hasOthers} onClick={() => setConfirmOthers(true)}>
          {t('profile.sessions.revoke_others')}
        </Button>
      }
    >
      {sessions.isError ? (
        <p role="alert" className="text-destructive">
          {errorMessage(sessions.error, t)}
        </p>
      ) : null}
      {sessions.isPending ? (
        <p className="text-muted-foreground">{t('common.loading')}</p>
      ) : (
        <DataList
          items={sessions.data ?? []}
          columns={buildSessionColumns({ t, fmt, deviceLabel, withUser: false, pending: revoke.isPending, onRevoke: (s) => revoke.mutate(s) })}
          rowKey={(s) => s.id}
          caption={t('profile.sessions.caption')}
          empty={t('profile.sessions.empty')}
        />
      )}
      <ConfirmDialog
        open={confirmOthers}
        onOpenChange={setConfirmOthers}
        title={t('profile.sessions.revoke_others_title')}
        description={t('profile.sessions.revoke_others_description')}
        confirmLabel={t('profile.sessions.revoke_others')}
        destructive
        pending={revokeOthers.isPending}
        onConfirm={() => revokeOthers.mutate()}
      />
    </Section>
  )
}
