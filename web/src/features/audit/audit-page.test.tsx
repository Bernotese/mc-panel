import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { AuditPage } from './audit-page'

describe('AuditPage', () => {
  it('shows a valid chain status', async () => {
    await renderWithProviders(<AuditPage />, { me: loginAs('admin') })
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Kette gültig (60 Einträge geprüft)'))
  })

  it('shows the first broken entry of a tampered chain', async () => {
    server.use(http.get('*/api/audit/verify', () => HttpResponse.json({ ok: false, checked: 17, first_bad_id: 17 })))
    await renderWithProviders(<AuditPage />, { me: loginAs('admin') })
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Kette ungültig ab Eintrag #17'))
  })

  it('pages with "load more"', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AuditPage />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Audit-Einträge' })
    expect(within(table).getAllByRole('row')).toHaveLength(51)
    await user.click(screen.getByRole('button', { name: 'Mehr laden' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Audit-Einträge' })).getAllByRole('row')).toHaveLength(61))
    expect(screen.queryByRole('button', { name: 'Mehr laden' })).not.toBeInTheDocument()
  })

  it('puts all filters into one toolbar row', async () => {
    await renderWithProviders(<AuditPage />, { me: loginAs('admin') })
    const toolbar = await screen.findByRole('search', { name: 'Filter' })
    for (const label of ['Akteur', 'Aktion', 'Ergebnis', 'Von', 'Bis']) {
      expect(within(toolbar).getByLabelText(label)).toBeInTheDocument()
    }
    expect(toolbar).toHaveClass('flex-wrap')
  })

  it('shows the empty text when filters match nothing', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AuditPage />, { me: loginAs('admin') })
    await screen.findByRole('table', { name: 'Audit-Einträge' })
    await user.type(screen.getByLabelText('Aktion'), 'does.not.exist')
    await user.click(screen.getByRole('button', { name: 'Filtern' }))
    expect(await screen.findByText('Keine Einträge gefunden.')).toBeInTheDocument()
  })

  it('filters by result', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<AuditPage />, { me: loginAs('admin') })
    await screen.findByRole('table', { name: 'Audit-Einträge' })
    await user.selectOptions(screen.getByLabelText('Ergebnis'), 'denied')
    await user.click(screen.getByRole('button', { name: 'Filtern' }))
    await waitFor(() => expect(within(screen.getByRole('table', { name: 'Audit-Einträge' })).getAllByRole('row')).toHaveLength(9))
    for (const row of within(screen.getByRole('table', { name: 'Audit-Einträge' })).getAllByRole('row').slice(1)) {
      expect(within(row).getByText('Abgelehnt')).toBeInTheDocument()
    }
  })
})
