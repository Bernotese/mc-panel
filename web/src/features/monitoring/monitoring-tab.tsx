import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import type { MetricsRange, Server } from '@/api/types'
import { Section } from '@/components/section'
import { StaleNotice } from '@/components/stale-notice'
import { useMe } from '@/features/auth/me'
import { freshnessOf } from '@/lib/freshness'
import { serverQueryOptions } from '@/features/servers/api'
import { liveMetricsQueryOptions, monitoringOverviewQueryOptions, serverMetricsQueryOptions } from './api'
import { DownAlertsSwitch } from './down-alerts-switch'
import { LiveHeader } from './live-header'
import { MetricCharts } from './metric-charts'
import { RangePicker } from './range-picker'

export function MonitoringTab({ server }: { server: Server }) {
  const me = useMe()
  const [range, setRange] = useState<MetricsRange>('1h')
  const running = server.status.state === 'running'
  // Shares the live query of the header (same key, no own polling): only for the TPS chart's hint.
  const live = useQuery({ ...liveMetricsQueryOptions(server.id), select: (m) => m.tps_source })
  const tpsSource = live.data
  // Same keys as the header and the charts (no own polling): one notice for the tab when either keeps stale data.
  const charts = useQuery(serverMetricsQueryOptions(server.id, range))
  // The server header above already shows a notice while its queries are stale; a second one would only repeat it.
  // Disabled observers only read the cache state, they fetch nothing.
  const detail = useQuery({ ...serverQueryOptions(server.id), enabled: false })
  const overview = useQuery({ ...monitoringOverviewQueryOptions, enabled: false })
  const headerStale = freshnessOf(detail, server.instance !== null && overview).stale
  return (
    <div className="space-y-6">
      {headerStale ? null : <StaleNotice freshness={freshnessOf(live, charts)} />}
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
