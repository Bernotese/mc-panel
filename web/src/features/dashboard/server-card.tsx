import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap, unwrapVoid } from '@/api/errors'
import type { PowerAction, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { useErrorToast } from '@/lib/error-message'
import { serversKey } from './servers'

const POWER_ACTIONS: PowerAction[] = ['start', 'stop', 'restart']

export function ServerCard({ server, canDelete }: { server: Server; canDelete: boolean }) {
  const { t } = useTranslation()
  const headingId = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const canPower = server.permissions.includes('server.power')

  const power = useMutation({
    mutationFn: (action: PowerAction) =>
      unwrap(api.POST('/servers/{id}/power', { params: { path: { id: server.id } }, body: { action } })),
    onSuccess: (_data, action) =>
      toast.success(t('dashboard.power_requested', { action: t(`dashboard.power_noun.${action}`), name: server.name })),
    onError: notifyError,
  })
  const remove = useMutation({
    mutationFn: () => unwrapVoid(api.DELETE('/servers/{id}', { params: { path: { id: server.id } } })),
    onSuccess: () => {
      setConfirmDelete(false)
      toast.success(t('dashboard.deleted', { name: server.name }))
      void queryClient.invalidateQueries({ queryKey: serversKey })
    },
    onError: notifyError,
  })

  return (
    <article aria-labelledby={headingId} className="h-full">
      <Card className="h-full">
        <CardHeader>
          <h2 id={headingId} className="text-lg font-semibold">
            {server.name}
          </h2>
          <CardDescription>{t('dashboard.stub_hint')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <ul aria-label={t('dashboard.permissions_label')} className="flex flex-wrap gap-1.5">
            {server.permissions.map((p) => (
              <li key={p}>
                <Badge variant="secondary">{t(`permission.${p}`)}</Badge>
              </li>
            ))}
          </ul>
          {canPower ? (
            <div role="group" aria-label={t('dashboard.power_group', { name: server.name })} className="flex flex-wrap gap-2">
              {POWER_ACTIONS.map((action) => (
                <Button
                  key={action}
                  type="button"
                  size="sm"
                  variant={action === 'stop' ? 'outline' : 'secondary'}
                  disabled={power.isPending}
                  onClick={() => power.mutate(action)}
                >
                  {t(`dashboard.power.${action}`)}
                </Button>
              ))}
            </div>
          ) : (
            <p className="text-muted-foreground text-sm">{t('dashboard.no_power')}</p>
          )}
          {canDelete ? (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              className="text-destructive"
              aria-label={t('dashboard.delete_for', { name: server.name })}
              onClick={() => setConfirmDelete(true)}
            >
              {t('common.delete')}
            </Button>
          ) : null}
        </CardContent>
      </Card>
      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={t('dashboard.delete_title', { name: server.name })}
        description={t('dashboard.delete_description')}
        confirmLabel={t('common.delete')}
        destructive
        pending={remove.isPending}
        onConfirm={() => remove.mutate()}
      />
    </article>
  )
}
