import { createFileRoute } from '@tanstack/react-router'
import { ConsoleTab } from '@/features/console/console-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/console')({ component: ConsoleRoute })

function ConsoleRoute() {
  const { server } = useServerContext()
  return (
    <SectionGuard section="console">
      <ConsoleTab server={server} />
    </SectionGuard>
  )
}
