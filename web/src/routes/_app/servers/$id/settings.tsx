import { createFileRoute } from '@tanstack/react-router'
import { DeleteServerSection } from '@/features/servers/delete-server-section'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'
import { ServerSettingsTab } from '@/features/servers/server-settings-tab'

export const Route = createFileRoute('/_app/servers/$id/settings')({ component: SettingsRoute })

function SettingsRoute() {
  const { server } = useServerContext()
  return (
    <SectionGuard section="settings">
      {server.instance ? (
        <div className="flex flex-col gap-6">
          <ServerSettingsTab server={server} instance={server.instance} />
          {/* Transitional: T4 moves the danger zone into ServerSettingsTab – then remove this line. */}
          <DeleteServerSection server={server} />
        </div>
      ) : null}
    </SectionGuard>
  )
}
