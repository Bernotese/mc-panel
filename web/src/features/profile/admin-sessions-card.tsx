// web/src/features/profile/admin-sessions-card.tsx
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { Session } from '@/api/types'
import { DataList } from '@/components/data-list'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { SectionCard } from './section-card'
import { buildSessionColumns, useDeviceLabel } from './session-columns'
import { allSessionsKey } from './session-keys'

export function AdminSessionsCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const deviceLabel = useDeviceLabel()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const sessions = useQuery({ queryKey: allSessionsKey, queryFn: () => unwrap(api.GET('/admin/sessions')) })
  const revoke = useMutation({
    mutationFn: (session: Session) => unwrapVoid(api.DELETE('/admin/sessions/{id}', { params: { path: { id: session.id } } })),
    onSuccess: () => {
      toast.success(t('profile.sessions.revoked'))
      void queryClient.invalidateQueries({ queryKey: ['sessions'] })
    },
    onError: notifyError,
  })

  return (
    <SectionCard title={t('profile.sessions.all_title')} className="md:col-span-2">
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
          columns={buildSessionColumns({ t, fmt, deviceLabel, withUser: true, pending: revoke.isPending, onRevoke: (s) => revoke.mutate(s) })}
          rowKey={(s) => s.id}
          caption={t('profile.sessions.all_caption')}
          empty={t('profile.sessions.empty')}
        />
      )}
    </SectionCard>
  )
}
