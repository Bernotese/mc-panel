import { createFileRoute } from '@tanstack/react-router'
import { BackupsTab } from '@/features/backups/backups-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/backups')({ component: BackupsRoute })

function BackupsRoute() {
  const { server } = useServerContext()
  return (
    <SectionGuard section="backups">
      <BackupsTab server={server} />
    </SectionGuard>
  )
}
