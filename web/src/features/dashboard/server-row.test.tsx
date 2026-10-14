import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { mockOverview, overviewServer } from '@/features/monitoring/test-utils'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { DashboardPage } from './dashboard-page'

describe('ServerRow', () => {
  it('shows dashes, not zeros, when a running server has no values yet', async () => {
    mockOverview({ servers: [overviewServer(1, { players: null, tps: null, cpu_percent: null })], host: null })
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const row = await screen.findByRole('article', { name: 'survival' })
    expect(await within(row).findAllByText('–')).toHaveLength(3)
    expect(within(row).queryByText('0')).not.toBeInTheDocument()
    expect(within(row).queryByText(/NaN/)).not.toBeInTheDocument()
  })

  it('lists the servers in a labelled list with icon power buttons that keep their names', async () => {
    await renderWithProviders(<DashboardPage />, { me: loginAs('admin') })
    const list = await screen.findByRole('list', { name: 'Server-Liste' })
    const row = within(list).getByRole('article', { name: 'survival' })
    const group = within(row).getByRole('group', { name: 'Steuerung für survival' })
    for (const name of ['Starten', 'Stoppen', 'Neu starten']) {
      const button = within(group).getByRole('button', { name })
      expect(button).toHaveAttribute('title', name)
    }
  })

  it('shows reachability as ping bars with a label', async () => {
    mockOverview({ servers: [overviewServer(1, { up: true })], host: null })
    await renderWithProviders(<DashboardPage />, { me: loginAs('alice') })
    const row = await screen.findByRole('article', { name: 'survival' })
    expect(await within(row).findByRole('img', { name: 'Erreichbar' })).toBeInTheDocument()
  })
})
