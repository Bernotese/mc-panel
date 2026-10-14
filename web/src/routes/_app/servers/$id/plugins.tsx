import { createFileRoute } from '@tanstack/react-router'
import { pluginKindOf } from '@/features/plugins/model'
import { PluginsTab } from '@/features/plugins/plugins-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/plugins')({ component: PluginsRoute })

function PluginsRoute() {
  const { server } = useServerContext()
  const kind = server.instance ? pluginKindOf(server.instance.type) : null
  return <SectionGuard section="plugins">{kind ? <PluginsTab server={server} kind={kind} /> : null}</SectionGuard>
}
