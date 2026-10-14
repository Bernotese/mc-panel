import { Play, RotateCw, Square, type LucideIcon } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PowerAction, Server } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { POWER_ACTIONS, powerAllowed } from './status'
import { usePower } from './use-power'

const ICONS: Record<PowerAction, LucideIcon> = { start: Play, stop: Square, restart: RotateCw }

/**
 * Start, stop (after confirmation) and restart; actions that make no sense in the current state are disabled.
 * `compact` renders icon buttons (the label stays the accessible name and tooltip).
 */
export function PowerButtons({
  server,
  extracting = false,
  compact = false,
}: {
  server: Pick<Server, 'id' | 'name' | 'status'>
  extracting?: boolean
  compact?: boolean
}) {
  const { t } = useTranslation()
  const [confirmStop, setConfirmStop] = useState(false)
  const power = usePower(server)

  return (
    <>
      <div role="group" aria-label={t('servers.power_group', { name: server.name })} className={compact ? 'flex gap-1.5' : 'flex flex-wrap gap-2'}>
        {POWER_ACTIONS.map((action) => {
          const Icon = ICONS[action]
          const label = t(`servers.power.${action}`)
          return (
            <Button
              key={action}
              type="button"
              size={compact ? 'icon-sm' : 'sm'}
              // In lists only "start" is the call to action; in the header stop is the odd one out.
              variant={compact ? (action === 'start' ? 'default' : 'secondary') : action === 'stop' ? 'outline' : 'default'}
              aria-label={compact ? label : undefined}
              title={compact ? label : undefined}
              disabled={power.isPending || !powerAllowed(server.status.state, action) || (extracting && action !== 'stop')}
              onClick={() => (action === 'stop' ? setConfirmStop(true) : power.mutate(action))}
            >
              <Icon aria-hidden="true" />
              {compact ? null : label}
            </Button>
          )
        })}
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
