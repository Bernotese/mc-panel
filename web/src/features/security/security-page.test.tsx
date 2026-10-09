import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { SecurityPage } from './security-page'

describe('SecurityPage', () => {
  it('shows all six tabs and switches between them', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<SecurityPage />, { me: loginAs('admin') })
    expect(screen.getByRole('heading', { name: 'Security Center' })).toBeInTheDocument()
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual([
      'Übersicht',
      'Login-Versuche',
      'Jails',
      'IP-Regeln',
      'Einstellungen',
      'Benachrichtigungen',
    ])
    expect(await screen.findByRole('img', { name: /Login-Versuche/ })).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'Jails' }))
    expect(await screen.findByRole('table', { name: 'Jails' })).toBeInTheDocument()
  })
})
