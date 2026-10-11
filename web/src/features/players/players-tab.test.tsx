import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { db, loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'

async function openPlayers(serverId = 1, username = 'admin') {
  const user = userEvent.setup()
  await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs(username) })
  await user.click(await screen.findByRole('tab', { name: 'Spieler' }))
  return user
}

describe('PlayersTab', () => {
  it('lists online players, whitelist, operators and bans', async () => {
    await openPlayers()
    const online = await screen.findByRole('region', { name: 'Online' })
    expect(await within(online).findByText('Alex')).toBeInTheDocument()
    expect(within(online).getByText('Steve')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Whitelist' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Operatoren' })).toBeInTheDocument()
    const bans = screen.getByRole('region', { name: 'Gebannte Spieler' })
    expect(await within(bans).findAllByRole('button', { name: / entbannen$/ })).toHaveLength(1)
  })

  it('kicks a player and refreshes the online list', async () => {
    const user = await openPlayers()
    const online = await screen.findByRole('region', { name: 'Online' })
    await user.click(await within(online).findByRole('button', { name: 'Steve kicken' }))
    await waitFor(() => expect(within(online).queryByText('Steve')).not.toBeInTheDocument())
    await waitFor(() => expect(document.querySelector('[data-sonner-toast]')).not.toBeNull())
  })

  it('bans a player with a reason', async () => {
    const user = await openPlayers()
    const online = await screen.findByRole('region', { name: 'Online' })
    await user.click(await within(online).findByRole('button', { name: 'Steve bannen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Steve bannen' })
    await user.type(within(dialog).getByLabelText('Grund (optional)'), 'griefing')
    await user.click(within(dialog).getByRole('button', { name: 'Bannen' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    const bans = screen.getByRole('region', { name: 'Gebannte Spieler' })
    expect(await within(bans).findByText('griefing')).toBeInTheDocument()
    expect(within(bans).getByRole('button', { name: 'Steve entbannen' })).toBeInTheDocument()
  })

  it('rejects a reason that is too long before sending', async () => {
    const user = await openPlayers()
    const online = await screen.findByRole('region', { name: 'Online' })
    await user.click(await within(online).findByRole('button', { name: 'Steve bannen' }))
    const dialog = await screen.findByRole('dialog', { name: 'Steve bannen' })
    await user.click(within(dialog).getByLabelText('Grund (optional)'))
    await user.paste('x'.repeat(201))
    await user.click(within(dialog).getByRole('button', { name: 'Bannen' }))
    expect(within(dialog).getByText('Höchstens 200 Zeichen.')).toBeInTheDocument()
    expect(within(online).getByText('Steve')).toBeInTheDocument()
  })

  it('adds a name to the whitelist after checking it', async () => {
    const user = await openPlayers()
    const whitelist = await screen.findByRole('region', { name: 'Whitelist' })
    const input = within(whitelist).getByLabelText('Spieler zur Whitelist hinzufügen')
    await user.type(input, 'a b')
    await user.click(within(whitelist).getByRole('button', { name: 'Hinzufügen' }))
    expect(within(whitelist).getByText('3–16 Buchstaben, Ziffern oder Unterstriche.')).toBeInTheDocument()
    await user.clear(input)
    await user.type(input, 'Notch')
    await user.click(within(whitelist).getByRole('button', { name: 'Hinzufügen' }))
    expect(await within(whitelist).findByText('Notch')).toBeInTheDocument()
    expect(input).toHaveValue('')
  })

  it('reports a list file the server could not read', async () => {
    server.use(http.get('*/api/servers/:id/players/lists', () => HttpResponse.json({ ops: [], whitelist: [], bans: [], errors: ['ops.json'] })))
    await openPlayers()
    const ops = await screen.findByRole('region', { name: 'Operatoren' })
    expect(await within(ops).findByRole('alert')).toHaveTextContent('Die Datei ops.json ist auf dem Server nicht lesbar.')
  })

  it('locks every change while the server is stopped', async () => {
    await openPlayers(2)
    expect(await screen.findByText('Der Server läuft nicht. Die Listen sind lesbar, ändern kannst du sie erst nach dem Start.')).toBeInTheDocument()
    const add = screen.getAllByRole('button', { name: 'Hinzufügen' })
    expect(add).toHaveLength(2)
    for (const button of add) expect(button).toBeDisabled()
  })

  it('leads admins to the whitelist setting', async () => {
    const user = await openPlayers()
    await user.click(await screen.findByRole('button', { name: 'Zu den Einstellungen' }))
    expect(screen.getByRole('tab', { name: 'Einstellungen' })).toHaveAttribute('aria-selected', 'true')
  })

  it('tells other users that an admin switches the whitelist on', async () => {
    db.grants.set(2, [{ server_id: 1, permissions: ['server.view', 'server.console'] }])
    await openPlayers(1, 'alice')
    expect(await screen.findByText('Die Whitelist ist aus: Einträge wirken erst, wenn ein Admin sie einschaltet.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Zu den Einstellungen' })).not.toBeInTheDocument()
  })
})
