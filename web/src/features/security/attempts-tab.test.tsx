import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { setViewport } from '@/test/media'
import { renderWithProviders } from '@/test/render'
import { AttemptsTab } from './attempts-tab'

describe('AttemptsTab', () => {
  it('pages through attempts with "load more"', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AttemptsTab />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Login-Versuche' })
    expect(within(table).getAllByRole('row')).toHaveLength(51)
    await user.click(screen.getByRole('button', { name: 'Mehr laden' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Login-Versuche' })).getAllByRole('row')).toHaveLength(101))
  })

  it('filters failures of one IP', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AttemptsTab />, { me: loginAs('admin') })
    await screen.findByRole('table', { name: 'Login-Versuche' })
    await user.selectOptions(screen.getByLabelText('Ergebnis'), 'failure')
    await user.type(screen.getByLabelText('IP-Adresse'), '203.0.113.9')
    await user.click(screen.getByRole('button', { name: 'Filtern' }))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Mehr laden' })).not.toBeInTheDocument())
    const rows = within(screen.getByRole('table', { name: 'Login-Versuche' })).getAllByRole('row').slice(1)
    expect(rows.length).toBeGreaterThan(0)
    for (const row of rows) {
      expect(within(row).getByText('Fehlgeschlagen')).toBeInTheDocument()
      expect(within(row).getByText(/203\.0\.113\.9/)).toBeInTheDocument()
    }
  })

  it('renders cards instead of a table on a phone', async () => {
    setViewport('mobile')
    await renderWithProviders(<AttemptsTab />, { me: loginAs('admin') })
    const list = await screen.findByRole('list', { name: 'Login-Versuche' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(50)
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })
})
