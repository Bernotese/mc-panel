import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { SettingsTab } from './settings-tab'

describe('SettingsTab', () => {
  it('saves 2FA enforcement and allowed countries', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SettingsTab />, { me: loginAs('admin', { sudo: true }) })
    const enforce = await screen.findByRole('switch', { name: 'Zwei-Faktor-Anmeldung für alle Benutzer erzwingen' })
    expect(enforce).toHaveAttribute('aria-checked', 'false')
    const save = screen.getByRole('button', { name: 'Speichern' })
    expect(save).toBeDisabled()
    await user.click(enforce)
    await user.type(screen.getByLabelText('Erlaubte Länder'), 'de, at')
    await user.click(save)
    expect(await screen.findByText('Einstellungen gespeichert.')).toBeInTheDocument()
    expect(db.settings).toEqual({ enforce_2fa_for_users: true, admin_allowlist_only: false, allowed_countries: ['DE', 'AT'] })
  })

  it('blocks invalid country codes', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SettingsTab />, { me: loginAs('admin', { sudo: true }) })
    await user.type(await screen.findByLabelText('Erlaubte Länder'), 'Germany')
    expect(screen.getByText('Ungültige Ländercodes: GERMANY')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Speichern' })).toBeDisabled()
  })

  it('disables the country filter without GeoIP database', async () => {
    db.geoipAvailable = false
    await renderWithProviders(<SettingsTab />, { me: loginAs('admin') })
    expect(await screen.findByText('Keine GeoIP-Datenbank gefunden – der Länderfilter ist deaktiviert.')).toBeInTheDocument()
    expect(screen.getByLabelText('Erlaubte Länder')).toBeDisabled()
  })

  it('reports a lock-out conflict from the server', async () => {
    const user = userEvent.setup()
    db.ipRules = []
    await renderWithProviders(<SettingsTab />, { me: loginAs('admin', { sudo: true }) })
    await user.click(await screen.findByRole('switch', { name: 'Admin-Zugang nur aus der Allowlist' }))
    await user.click(screen.getByRole('button', { name: 'Speichern' }))
    expect(await screen.findByText(/would lock you out/)).toBeInTheDocument()
    expect(db.settings.admin_allowlist_only).toBe(false)
  })
})
