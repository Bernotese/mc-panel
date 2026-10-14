import { createFileRoute } from '@tanstack/react-router'
import { MonitoringTab } from '@/features/monitoring/monitoring-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/monitoring')({ component: MonitoringRoute })

function MonitoringRoute() {
  const { server } = useServerContext()
  return (
    <SectionGuard section="monitoring">
      <MonitoringTab server={server} />
    </SectionGuard>
  )
}
