import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import type { MetricsRange, Server } from '@/api/types'
import { useMe } from '@/features/auth/me'
import { liveMetricsQueryOptions } from './api'
import { DownAlertsSwitch } from './down-alerts-switch'
import { LiveHeader } from './live-header'
import { MetricCharts } from './metric-charts'
import { RangePicker } from './range-picker'

export function MonitoringTab({ server }: { server: Server }) {
  const me = useMe()
  const [range, setRange] = useState<MetricsRange>('1h')
  const running = server.status.state === 'running'
  // Shares the live query of the header (same key, no own polling): only for the TPS chart's hint.
  const tpsSource = useQuery({ ...liveMetricsQueryOptions(server.id), select: (m) => m.tps_source }).data
  return (
    <div className="space-y-6">
      <LiveHeader serverId={server.id} running={running} />
      <RangePicker value={range} onChange={setRange} />
      <MetricCharts serverId={server.id} range={range} tpsUnavailable={running && tpsSource === 'none'} />
      {me?.role === 'admin' ? <DownAlertsSwitch server={server} /> : null}
    </div>
  )
}
