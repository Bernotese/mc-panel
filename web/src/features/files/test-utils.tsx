import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'

export async function openFiles(serverId = 1, username = 'admin') {
  const user = userEvent.setup()
  await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs(username) })
  await user.click(await screen.findByRole('tab', { name: 'Dateien' }))
  const region = await screen.findByRole('region', { name: 'Dateien' })
  return { user, region }
}
