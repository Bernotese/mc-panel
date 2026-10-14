import { Play, RotateCw, Square } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PowerAction, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { POWER_ACTIONS, powerAllowed } from '@/features/servers/status'
import { usePower } from '@/features/servers/use-power'

const ICON: Record<PowerAction, typeof Play> = { start: Play, stop: Square, restart: RotateCw }

/** Icon-only power buttons for the server list; same rules as PowerButtons (stop asks first). */
export function PowerIconButtons({ server }: { server: Pick<Server, 'id' | 'name' | 'status'> }) {
  const { t } = useTranslation()
  const [confirmStop, setConfirmStop] = useState(false)
  const power = usePower(server)
  return (
    <>
      <div role="group" aria-label={t('servers.power_group', { name: server.name })} className="flex gap-1.5">
        {POWER_ACTIONS.map((action) => {
          const Icon = ICON[action]
          const label = t(`servers.power.${action}`)
          return (
            <Button
              key={action}
              type="button"
              size="icon-sm"
              variant={action === 'start' ? 'default' : 'secondary'}
              aria-label={label}
              title={label}
              disabled={power.isPending || !powerAllowed(server.status.state, action)}
              onClick={() => (action === 'stop' ? setConfirmStop(true) : power.mutate(action))}
            >
              <Icon aria-hidden="true" />
            </Button>
          )
        })}
      </div>
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
