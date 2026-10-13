import { useState } from 'react'
import type { MetricsRange, Server } from '@/api/types'
import { useMe } from '@/features/auth/me'
import { DownAlertsSwitch } from './down-alerts-switch'
import { LiveHeader } from './live-header'
import { MetricCharts } from './metric-charts'
import { RangePicker } from './range-picker'

export function MonitoringTab({ server }: { server: Server }) {
  const me = useMe()
  const [range, setRange] = useState<MetricsRange>('1h')
  return (
    <div className="space-y-6">
      <LiveHeader serverId={server.id} />
      <RangePicker value={range} onChange={setRange} />
      <MetricCharts serverId={server.id} range={range} />
      {me?.role === 'admin' ? <DownAlertsSwitch server={server} /> : null}
    </div>
  )
}
