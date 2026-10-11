import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { OnlinePlayersCard } from './online-players-card'

describe('OnlinePlayersCard', () => {
  it('shows how many players are online and who', async () => {
    await renderWithProviders(<OnlinePlayersCard serverId={1} />, { me: loginAs('admin') })
    const card = screen.getByRole('region', { name: 'Online-Spieler' })
    expect(await within(card).findByText('2 / 20')).toBeInTheDocument()
    expect(within(card).getByText('Alex')).toBeInTheDocument()
    expect(within(card).getByText('Steve')).toBeInTheDocument()
  })

  it('works with the view permission alone', async () => {
    await renderWithProviders(<OnlinePlayersCard serverId={1} />, { me: loginAs('alice') })
    expect(await screen.findByText('2 / 20')).toBeInTheDocument()
  })

  it('says when nobody is online', async () => {
    server.use(http.get('*/api/servers/:id/players', () => HttpResponse.json({ available: true, online: [], max: 20 })))
    await renderWithProviders(<OnlinePlayersCard serverId={1} />, { me: loginAs('admin') })
    expect(await screen.findByText('0 / 20')).toBeInTheDocument()
    expect(screen.getByText('Gerade ist niemand online.')).toBeInTheDocument()
  })

  it('says when the list is not available', async () => {
    await renderWithProviders(<OnlinePlayersCard serverId={2} />, { me: loginAs('admin') })
    expect(await screen.findByText('Nicht verfügbar – der Server läuft nicht oder startet noch.')).toBeInTheDocument()
  })
})
