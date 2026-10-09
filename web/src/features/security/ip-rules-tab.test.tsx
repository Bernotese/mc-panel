import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { IpRulesTab } from './ip-rules-tab'

describe('IpRulesTab', () => {
  it('lists allow and deny rules', async () => {
    await renderWithProviders(<IpRulesTab />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'IP-Regeln' })
    const allow = within(table).getByRole('row', { name: /198\.51\.100\.0\/24/ })
    expect(within(allow).getByText('Erlauben')).toBeInTheDocument()
    expect(within(allow).getByText('nie')).toBeInTheDocument()
    expect(within(within(table).getByRole('row', { name: /192\.0\.2\.0\/24/ })).getByText('Sperren')).toBeInTheDocument()
  })

  it('validates CIDR notation and adds a rule', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<IpRulesTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Regel hinzufügen' }))
    const dialog = screen.getByRole('dialog', { name: 'Regel hinzufügen' })
    const cidr = within(dialog).getByLabelText('CIDR')
    await user.type(cidr, '203.0.113.0')
    expect(within(dialog).getByText('Ungültige CIDR-Notation.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Regel hinzufügen' })).toBeDisabled()
    await user.type(cidr, '/24')
    await user.selectOptions(within(dialog).getByLabelText('Art'), 'deny')
    await user.type(within(dialog).getByLabelText('Kommentar'), 'abuse')
    await user.click(within(dialog).getByRole('button', { name: 'Regel hinzufügen' }))
    expect(await screen.findByText('Regel 203.0.113.0/24 hinzugefügt.')).toBeInTheDocument()
    expect(await screen.findByRole('row', { name: /203\.0\.113\.0\/24/ })).toBeInTheDocument()
  })

  it('deletes a rule after confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<IpRulesTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Regel 192.0.2.0/24 löschen' }))
    const confirm = screen.getByRole('dialog', { name: 'Regel 192.0.2.0/24 löschen?' })
    await user.click(within(confirm).getByRole('button', { name: 'Löschen' }))
    await waitFor(() => expect(screen.queryByRole('row', { name: /192\.0\.2\.0\/24/ })).not.toBeInTheDocument())
  })
})
