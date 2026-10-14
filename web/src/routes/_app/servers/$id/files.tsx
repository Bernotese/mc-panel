import { createFileRoute } from '@tanstack/react-router'
import { FilesTab } from '@/features/files/files-tab'
import { SectionGuard } from '@/features/servers/section-guard'
import { useServerContext } from '@/features/servers/server-context'

export const Route = createFileRoute('/_app/servers/$id/files')({ component: FilesRoute })

function FilesRoute() {
  const { server } = useServerContext()
  // FilesTab's own useBlocker asks before leaving with unsaved edits or running uploads.
  return (
    <SectionGuard section="files">
      <FilesTab server={server} />
    </SectionGuard>
  )
}
