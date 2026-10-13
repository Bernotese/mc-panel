import { Link } from '@tanstack/react-router'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { OverviewServer, Server } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { AddressLine } from '@/features/servers/address-line'
import { describeInstance, formatGiB } from '@/features/servers/format'
import { PowerButtons } from '@/features/servers/power-buttons'
import { StatusBadge } from '@/features/servers/status-badge'
import { ServerMetrics } from '@/features/monitoring/server-metrics'
import { useFormat } from '@/lib/format'

export function ServerCard({ server, metrics }: { server: Server; metrics?: OverviewServer }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const headingId = useId()
  const { instance, status } = server
  const canPower = server.permissions.includes('server.power')
  const showMemory = status.state === 'running' && status.memory_limit_bytes > 0

  return (
    <article aria-labelledby={headingId} className="h-full">
      <Card className="h-full">
        <CardHeader>
          <div className="flex items-start justify-between gap-2">
            <h2 id={headingId} className="min-w-0 text-lg font-semibold break-words">
              <Link
                to="/servers/$id"
                params={{ id: String(server.id) }}
                className="focus-visible:ring-ring rounded-sm outline-none hover:underline focus-visible:ring-2"
              >
                {server.name}
              </Link>
            </h2>
            <StatusBadge state={status.state} />
          </div>
          <CardDescription>{instance ? describeInstance(instance, t) : t('servers.not_configured_hint')}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {instance ? <AddressLine address={server.address} /> : null}
          {instance ? <ServerMetrics server={server} metrics={metrics} /> : null}
          {showMemory ? (
            <p className="text-muted-foreground text-sm tabular-nums">
              {t('servers.memory_usage', { used: formatGiB(status.memory_bytes, fmt.locale), limit: formatGiB(status.memory_limit_bytes, fmt.locale) })}
            </p>
          ) : null}
          {status.restart_required ? <Badge variant="outline">{t('servers.restart_required')}</Badge> : null}
          {instance && canPower ? <PowerButtons server={server} /> : null}
          {instance && !canPower ? <p className="text-muted-foreground text-sm">{t('servers.no_power')}</p> : null}
        </CardContent>
      </Card>
    </article>
  )
}
