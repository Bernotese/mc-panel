import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { PlayersTab } from '@/features/players/players-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/players')({ component: PlayersRoute })

function PlayersRoute() {
  const { server, isAdmin } = useServerContext()
  const navigate = useNavigate()
  // Transitional (until T3 links to the settings itself): remove with PlayersTab's onOpenSettings prop.
  const openSettings = isAdmin ? () => void navigate({ to: '/servers/$id/settings', params: { id: String(server.id) } }) : undefined
  return (
    <SectionGuard section="players">
      <PlayersTab server={server} onOpenSettings={openSettings} />
    </SectionGuard>
  )
}
