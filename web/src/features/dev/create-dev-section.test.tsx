import { screen, waitFor, within } from '@testing-library/react'
import { http } from 'msw'
import { describe, expect, it } from 'vitest'
import { ServerOverview } from '@/features/servers/server-overview'
import { server as mockApi } from '@/mocks/node'
import { renderServerTab } from '@/test/render-section'

function overview(serverId: number, username = 'admin') {
  return renderServerTab((s) => (s.instance ? <ServerOverview server={s} instance={s.instance} /> : null), { serverId, username, sudo: true })
}

describe('CreateDevSection', () => {
  it('links a prod to its dev', async () => {
    await overview(1, 'alice')
    const section = await screen.findByRole('region', { name: 'Dev-Instanz' })
    expect(within(section).getByRole('link', { name: 'dev-survival' })).toHaveAttribute('href', '/servers/5')
  })

  it('creates dev-<name> with the suggested memory and opens it', async () => {
    const bodies: unknown[] = []
    mockApi.use(
      http.post('*/api/servers/4/dev', async ({ request }) => {
        bodies.push(await request.clone().json())
      }),
    )
    const { user, router } = await overview(4)
    const section = await screen.findByRole('region', { name: 'Dev-Instanz' })
    expect(within(section).getByText(/belegt etwa so viel Speicherplatz wie modded/)).toBeInTheDocument()
    expect(
      within(section).getByText(
        'Plugins und Mods mit externen Diensten (z. B. eine gemeinsame MySQL-Datenbank oder ein Discord-Bot) laufen auf der Dev-Instanz mit denselben Zugangsdaten und können dort Daten von modded ändern.',
      ),
    ).toBeInTheDocument()
    await waitFor(() => expect(within(section).getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(3))
    await user.click(within(section).getByRole('button', { name: 'dev-modded anlegen' }))
    expect(await screen.findByText('dev-modded angelegt. Der erste Sync läuft.')).toBeInTheDocument()
    expect(bodies).toEqual([{ memory_mib: 3072 }])
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/servers\/\d+$/))
    expect(router.state.location.pathname).not.toBe('/servers/4')
  })

  it('is not offered to users', async () => {
    await overview(2, 'alice')
    await screen.findByRole('region', { name: 'Zustand' })
    expect(screen.queryByRole('region', { name: 'Dev-Instanz' })).not.toBeInTheDocument()
  })

  it('is not offered on a dev', async () => {
    await overview(5)
    await screen.findByRole('region', { name: 'Zustand' })
    expect(screen.queryByRole('region', { name: 'Dev-Instanz' })).not.toBeInTheDocument()
  })
})
