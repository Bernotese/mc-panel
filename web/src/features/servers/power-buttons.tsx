import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { POWER_ACTIONS, powerAllowed } from './status'
import { usePower } from './use-power'

/** Start, stop (after confirmation) and restart; actions that make no sense in the current state are disabled. */
export function PowerButtons({ server, extracting = false }: { server: Pick<Server, 'id' | 'name' | 'status'>; extracting?: boolean }) {
  const { t } = useTranslation()
  const [confirmStop, setConfirmStop] = useState(false)
  const power = usePower(server)

  return (
    <>
      <div role="group" aria-label={t('servers.power_group', { name: server.name })} className="flex flex-wrap gap-2">
        {POWER_ACTIONS.map((action) => (
          <Button
            key={action}
            type="button"
            size="sm"
            variant={action === 'stop' ? 'outline' : 'secondary'}
            disabled={power.isPending || !powerAllowed(server.status.state, action) || (extracting && action !== 'stop')}
            onClick={() => (action === 'stop' ? setConfirmStop(true) : power.mutate(action))}
          >
            {t(`servers.power.${action}`)}
          </Button>
        ))}
      </div>
      {extracting ? <p className="text-muted-foreground text-sm">{t('servers.extracting_lock')}</p> : null}
      <ConfirmDialog
        open={confirmStop}
        onOpenChange={setConfirmStop}
        title={t('servers.stop_title', { name: server.name })}
        description={t('servers.stop_description')}
        confirmLabel={t('servers.power.stop')}
        destructive
        pending={power.isPending}
        onConfirm={() => power.mutate('stop', { onSettled: () => setConfirmStop(false) })}
      />
    </>
  )
}
