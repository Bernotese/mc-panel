import { screen } from '@testing-library/react'
import { renderServerTab } from '@/features/servers/tab-test-utils'
import { FilesTab } from './files-tab'

export async function openFiles(serverId = 1, username = 'admin') {
  const { user, router, queryClient } = await renderServerTab((server) => <FilesTab server={server} />, {
    serverId,
    username,
    path: `/servers/${serverId}/files`,
  })
  const region = await screen.findByRole('region', { name: 'Dateien' })
  return { user, region, router, queryClient }
}
