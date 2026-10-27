import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import type { MetricsRange, Server } from '@/api/types'
import { Section } from '@/components/section'
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
      <Section>
        <div className="space-y-4">
          <RangePicker value={range} onChange={setRange} />
          <MetricCharts serverId={server.id} range={range} tpsUnavailable={running && tpsSource === 'none'} />
        </div>
      </Section>
      {/* Dev instances raise no down or crash alerts (dev instances spec §2): they sleep on purpose. */}
      {me?.role === 'admin' && !server.dev ? <DownAlertsSwitch server={server} /> : null}
    </div>
  )
}
