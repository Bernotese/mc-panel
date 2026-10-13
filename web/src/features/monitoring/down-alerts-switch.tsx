import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { Server } from '@/api/types'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { serverKey } from '@/features/servers/api'
import { useErrorToast } from '@/lib/error-message'
import { updateServerAlerts } from './api'

/** Admin switch (spec §7.2); a missing value means on (V2: no row = true). */
export function DownAlertsSwitch({ server }: { server: Server }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const checked = server.down_alerts ?? true
  const save = useMutation({
    mutationFn: (value: boolean) => updateServerAlerts(server.id, value),
    onSuccess: (_data, value) => {
      queryClient.setQueryData<Server>(serverKey(server.id), (prev) => (prev ? { ...prev, down_alerts: value } : prev))
      toast.success(t(value ? 'monitoring.down_alerts.enabled' : 'monitoring.down_alerts.disabled'))
    },
    onError: notifyError,
  })
  return (
    <div className="flex items-start gap-3 rounded-md border p-4">
      <Switch id={id} checked={checked} disabled={save.isPending} aria-describedby={`${id}-hint`} onCheckedChange={(value) => save.mutate(value)} />
      <div className="space-y-1">
        <Label htmlFor={id}>{t('monitoring.down_alerts.label')}</Label>
        <p id={`${id}-hint`} className="text-muted-foreground text-sm">
          {t('monitoring.down_alerts.hint')}
        </p>
      </div>
    </div>
  )
}
