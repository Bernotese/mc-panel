import { createFileRoute } from '@tanstack/react-router'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'
import { ServerSettingsTab } from '@/features/servers/server-settings-tab'

export const Route = createFileRoute('/_app/servers/$id/settings')({ component: SettingsRoute })

function SettingsRoute() {
  const { server } = useServerContext()
  // ServerSettingsTab ends with the danger zone (delete server).
  return (
    <SectionGuard section="settings">
      <ServerSettingsTab server={server} instance={server.instance} />
    </SectionGuard>
  )
}
