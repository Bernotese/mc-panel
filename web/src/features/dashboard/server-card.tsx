import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { AddressLine } from '@/features/servers/address-line'
import { deleteServer, serversKey } from '@/features/servers/api'
import { describeInstance, formatGiB } from '@/features/servers/format'
import { PowerButtons } from '@/features/servers/power-buttons'
import { StatusBadge } from '@/features/servers/status-badge'
import { useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'

export function ServerCard({ server, canDelete }: { server: Server; canDelete: boolean }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const headingId = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [confirmDelete, setConfirmDelete] = useState(false)
  const { instance, status } = server
  const canPower = server.permissions.includes('server.power')
  const showMemory = status.state === 'running' && status.memory_limit_bytes > 0

  const remove = useMutation({
    mutationFn: () => deleteServer(server.id),
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
          <div className="flex items-start justify-between gap-2">
            <h2 id={headingId} className="min-w-0 text-lg font-semibold break-words">
              {server.name}
            </h2>
            <StatusBadge state={status.state} />
          </div>
          <CardDescription>{instance ? describeInstance(instance, t) : t('servers.not_configured_hint')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {instance ? <AddressLine address={server.address} /> : null}
          {showMemory ? (
            <p className="text-muted-foreground text-sm tabular-nums">
              {t('servers.memory_usage', { used: formatGiB(status.memory_bytes, fmt.locale), limit: formatGiB(status.memory_limit_bytes, fmt.locale) })}
            </p>
          ) : null}
          {status.restart_required ? <Badge variant="outline">{t('servers.restart_required')}</Badge> : null}
          {instance && canPower ? <PowerButtons server={server} /> : null}
          {instance && !canPower ? <p className="text-muted-foreground text-sm">{t('servers.no_power')}</p> : null}
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
