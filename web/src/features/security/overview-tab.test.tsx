import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { OverviewTab } from './overview-tab'

describe('OverviewTab', () => {
  it('shows KPIs, the 24h chart and top IPs', async () => {
    await renderWithProviders(<OverviewTab />, { me: loginAs('admin') })
    expect(await screen.findByRole('img', { name: /Login-Versuche \(24 Stunden\)/ })).toBeInTheDocument()
    expect(screen.getAllByTestId('chart-bar')).toHaveLength(24)
    expect(within(screen.getByRole('region', { name: 'Fehlversuche' })).getByText('4')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Aktive Jails' })).getByText('2')).toBeInTheDocument()
    expect(within(screen.getByRole('list', { name: 'Top-IPs (Fehlversuche)' })).getAllByRole('listitem')).toHaveLength(4)
    // Stat slots keep their region names and are sunken inventory slots.
    const failures = screen.getByRole('region', { name: 'Fehlversuche' })
    expect(failures.querySelector('.bevel-inset')).not.toBeNull()
    expect(screen.getByRole('heading', { level: 3, name: 'Agent' })).toBeInTheDocument()
  })

  it('switches the range', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<OverviewTab />, { me: loginAs('admin') })
    await screen.findByRole('img', { name: /24 Stunden/ })
    const button = screen.getByRole('button', { name: '7 Tage' })
    await user.click(button)
    expect(button).toHaveAttribute('aria-pressed', 'true')
    await waitFor(() => expect(screen.getAllByTestId('chart-bar')).toHaveLength(7))
  })
})
