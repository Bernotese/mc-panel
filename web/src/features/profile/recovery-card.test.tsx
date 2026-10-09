import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { RecoveryCard } from './recovery-card'

describe('RecoveryCard', () => {
  it('shows the remaining codes and regenerates them', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<RecoveryCard />, { me: loginAs('admin', { sudo: true }) })
    expect(screen.getByText('Verbleibende Codes: 10')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Neue Codes erzeugen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Neue Wiederherstellungscodes erzeugen?' })).getByRole('button', { name: 'Neue Codes erzeugen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Neue Wiederherstellungscodes' })
    expect(within(within(dialog).getByRole('list', { name: 'Wiederherstellungscodes' })).getAllByRole('listitem')).toHaveLength(10)
  })

  it('cannot be dismissed before the codes were confirmed', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<RecoveryCard />, { me: loginAs('admin', { sudo: true }) })
    await user.click(screen.getByRole('button', { name: 'Neue Codes erzeugen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'Neue Wiederherstellungscodes erzeugen?' })).getByRole('button', { name: 'Neue Codes erzeugen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Neue Wiederherstellungscodes' })
    expect(within(dialog).queryByRole('button', { name: /Schließen|Close/ })).not.toBeInTheDocument()
    await user.keyboard('{Escape}')
    await user.click(document.querySelector('[data-slot="dialog-overlay"]')!)
    expect(screen.getByRole('dialog', { name: 'Neue Wiederherstellungscodes' })).toBeInTheDocument()
    await user.click(within(dialog).getByRole('checkbox', { name: 'Ich habe die Codes sicher gespeichert.' }))
    await user.click(within(dialog).getByRole('button', { name: 'Fertig' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Neue Wiederherstellungscodes' })).not.toBeInTheDocument())
  })

  it('is hidden for accounts without a second factor', async () => {
    await renderWithProviders(<RecoveryCard />, { me: loginAs('alice') })
    expect(screen.queryByRole('region', { name: 'Wiederherstellungscodes' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Neue Codes erzeugen' })).not.toBeInTheDocument()
  })
})
