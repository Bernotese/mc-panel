import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { JailsTab } from './jails-tab'

describe('JailsTab', () => {
  it('lists active jails and can include lifted ones', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<JailsTab />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Jails' })
    expect(within(table).getAllByRole('row')).toHaveLength(3)
    await user.click(screen.getByRole('switch', { name: 'Nur aktive' }))
    expect(await screen.findByText('Aufgehoben von admin')).toBeInTheDocument()
  })

  it('lifts a jail after sudo confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<JailsTab />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Sperre für 203.0.113.5/32 aufheben' }))
    const confirm = screen.getByRole('dialog', { name: 'Sperre für 203.0.113.5/32 aufheben?' })
    await user.click(within(confirm).getByRole('button', { name: 'Aufheben' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))
    expect(await screen.findByText('Sperre für 203.0.113.5/32 aufgehoben.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Sperre für 203.0.113.5/32 aufheben' })).not.toBeInTheDocument())
  })

  it('jails an IP manually and refuses the own IP', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<JailsTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'IP sperren' }))
    const dialog = screen.getByRole('dialog', { name: 'IP sperren' })
    await user.type(within(dialog).getByLabelText('IP-Adresse'), '198.51.100.7')
    await user.type(within(dialog).getByLabelText('Grund'), 'test')
    await user.click(within(dialog).getByRole('button', { name: 'IP sperren' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('refusing to jail your own IP')

    const ip = within(dialog).getByLabelText('IP-Adresse')
    await user.clear(ip)
    await user.type(ip, '203.0.113.77')
    await user.click(within(dialog).getByRole('button', { name: 'IP sperren' }))
    expect(await screen.findByText('203.0.113.77/32 gesperrt.')).toBeInTheDocument()
    expect(await screen.findByRole('row', { name: /203\.0\.113\.77\/32/ })).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: 'IP sperren' })).not.toBeInTheDocument()
  })

  it('validates the duration', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<JailsTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'IP sperren' }))
    const dialog = screen.getByRole('dialog', { name: 'IP sperren' })
    const duration = within(dialog).getByLabelText('Dauer (Minuten)')
    await user.clear(duration)
    await user.type(duration, '20000')
    expect(within(dialog).getByText('Die Dauer muss zwischen 1 und 10080 Minuten liegen.')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'IP sperren' })).toBeDisabled()
  })
})
