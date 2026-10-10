import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { ProfilePage } from './profile-page'

describe('ProfilePage', () => {
  it('shows the admin-only session overview only to admins', async () => {
    await renderWithProviders(<ProfilePage />, { me: loginAs('alice') })
    expect(screen.getByRole('heading', { name: 'Profil' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Aktive Sitzungen' })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Alle Sitzungen (Admin)' })).not.toBeInTheDocument()
  })

  it('includes all sessions for admins', async () => {
    await renderWithProviders(<ProfilePage />, { me: loginAs('admin') })
    expect(screen.getByRole('region', { name: 'Alle Sitzungen (Admin)' })).toBeInTheDocument()
  })
})
