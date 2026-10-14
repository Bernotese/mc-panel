import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { FilesTab } from '@/features/files/files-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/files')({ component: FilesRoute })

function FilesRoute() {
  const { server, isAdmin } = useServerContext()
  const navigate = useNavigate()
  // Sections are routes now: FilesTab's own useBlocker asks before leaving with unsaved edits or running uploads.
  // Transitional (until T4 links to the settings itself): remove with FilesTab's onOpenSettings prop.
  const openSettings = isAdmin ? () => void navigate({ to: '/servers/$id/settings', params: { id: String(server.id) } }) : undefined
  return (
    <SectionGuard section="files">
      <FilesTab server={server} onOpenSettings={openSettings} />
    </SectionGuard>
  )
}
