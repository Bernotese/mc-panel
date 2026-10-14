import { createFileRoute } from '@tanstack/react-router'
import { PlayersTab } from '@/features/players/players-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/players')({ component: PlayersRoute })

function PlayersRoute() {
  const { server } = useServerContext()
  return (
    <SectionGuard section="players">
      <PlayersTab server={server} />
    </SectionGuard>
  )
}
