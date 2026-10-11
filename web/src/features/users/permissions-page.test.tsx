import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { UserPermissionsPage } from './permissions-page'

describe('UserPermissionsPage', () => {
  it('shows the matrix and saves changed grants', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UserPermissionsPage userId={2} />, { me: loginAs('admin', { sudo: true }) })
    expect(await screen.findByRole('heading', { name: 'Rechte von alice' })).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'survival: Starten/Stoppen' })).toBeChecked()
    const lobbyPower = screen.getByRole('checkbox', { name: 'lobby: Starten/Stoppen' })
    expect(lobbyPower).not.toBeChecked()
    const save = screen.getByRole('button', { name: 'Speichern' })
    expect(save).toBeDisabled()

    await user.click(lobbyPower)
    expect(screen.getByRole('checkbox', { name: 'lobby: Ansehen' })).toBeChecked()
    expect(screen.getByText('Ungespeicherte Änderungen')).toBeInTheDocument()
    await user.click(save)

    expect(await screen.findByText('Rechte gespeichert.')).toBeInTheDocument()
    expect(db.grants.get(2)).toContainEqual({ server_id: 3, permissions: ['server.view', 'server.power'] })
    expect(screen.getByRole('button', { name: 'Speichern' })).toBeDisabled()
  })

  it('disables the matrix for administrators', async () => {
    await renderWithProviders(<UserPermissionsPage userId={3} />, { me: loginAs('admin') })
    expect(await screen.findByText('Administratoren haben automatisch alle Rechte auf allen Servern.')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'survival: Ansehen' })).toBeDisabled()
  })

  it('warns before granting file access', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<UserPermissionsPage userId={2} />, { me: loginAs('admin', { sudo: true }) })
    await screen.findByRole('heading', { name: 'Rechte von alice' })
    expect(screen.queryByText(/erlaubt Code-Ausführung/)).not.toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', { name: 'survival: Dateien' }))
    expect(screen.getByText(/„Dateien“ erlaubt Code-Ausführung im Minecraft-Server/)).toBeInTheDocument()
  })
})
