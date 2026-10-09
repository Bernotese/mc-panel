import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { NotifiersTab } from './notifiers-tab'

describe('NotifiersTab', () => {
  it('lists notifiers with masked targets', async () => {
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin') })
    const table = await screen.findByRole('table', { name: 'Benachrichtigungen' })
    const discord = within(table).getByRole('row', { name: /Discord #alerts/ })
    expect(within(discord).getByText('https://discord.com/api/webhooks/…/****')).toBeInTheDocument()
    expect(within(discord).getByRole('switch', { name: 'Discord #alerts aktiv' })).toHaveAttribute('aria-checked', 'true')
  })

  it('creates an ntfy notifier', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Benachrichtigung hinzufügen' }))
    const dialog = screen.getByRole('dialog', { name: 'Benachrichtigung hinzufügen' })
    await user.selectOptions(within(dialog).getByLabelText('Dienst'), 'ntfy')
    await user.type(within(dialog).getByLabelText('Name'), 'ntfy Server')
    await user.type(within(dialog).getByLabelText('Server-URL'), 'https://ntfy.example.org')
    expect(within(dialog).getByRole('button', { name: 'Anlegen' })).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Topic'), 'mc-alerts')
    await user.click(within(dialog).getByRole('button', { name: 'Anlegen' }))
    expect(await screen.findByText('Benachrichtigung ntfy Server angelegt.')).toBeInTheDocument()
    expect(await screen.findByRole('row', { name: /ntfy Server/ })).toBeInTheDocument()
  })

  it('rejects non-http URLs', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Benachrichtigung hinzufügen' }))
    const dialog = screen.getByRole('dialog', { name: 'Benachrichtigung hinzufügen' })
    await user.type(within(dialog).getByLabelText('Webhook-URL'), 'javascript:alert(1)')
    expect(within(dialog).getByText('Bitte eine http(s)-URL angeben.')).toBeInTheDocument()
  })

  it('sends a test message', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Testnachricht an Discord #alerts senden' }))
    expect(await screen.findByText('Testnachricht an Discord #alerts gesendet.')).toBeInTheDocument()
  })

  it('shows the delivery error of a failed test message', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('button', { name: 'Testnachricht an ntfy Handy senden' }))
    expect(await screen.findByText('Eingaben ungültig (delivery failed: HTTP 404 from target)')).toBeInTheDocument()
  })

  it('reports a duplicate name on rename as validation error', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'Discord #alerts bearbeiten' }))
    const dialog = screen.getByRole('dialog', { name: 'Benachrichtigung bearbeiten' })
    const name = within(dialog).getByLabelText('Name')
    await user.clear(name)
    await user.type(name, 'ntfy Handy')
    await user.click(within(dialog).getByRole('button', { name: 'Speichern' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Eingaben ungültig (name already in use)')
  })

  it('toggles a notifier and renames it without resending the secret URL', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('switch', { name: 'ntfy Handy aktiv' }))
    await screen.findByText('Benachrichtigung ntfy Handy gespeichert.')
    expect(db.notifiers.find((n) => n.id === 2)?.enabled).toBe(true)

    await user.click(screen.getByRole('button', { name: 'Discord #alerts bearbeiten' }))
    const dialog = screen.getByRole('dialog', { name: 'Benachrichtigung bearbeiten' })
    expect(within(dialog).getByText('Leer lassen, um die gespeicherte URL zu behalten.')).toBeInTheDocument()
    const name = within(dialog).getByLabelText('Name')
    await user.clear(name)
    await user.type(name, 'Discord #ops')
    await user.click(within(dialog).getByRole('button', { name: 'Speichern' }))
    expect(await screen.findByRole('row', { name: /Discord #ops/ })).toBeInTheDocument()
    expect(db.notifiers.find((n) => n.id === 1)?.target_masked).toBe('https://discord.com/api/webhooks/…/****')
  })

  it('deletes a notifier after confirmation', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<NotifiersTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('button', { name: 'ntfy Handy löschen' }))
    await user.click(within(screen.getByRole('dialog', { name: 'ntfy Handy löschen?' })).getByRole('button', { name: 'Löschen' }))
    expect(await screen.findByText('Benachrichtigung ntfy Handy gelöscht.')).toBeInTheDocument()
  })
})
