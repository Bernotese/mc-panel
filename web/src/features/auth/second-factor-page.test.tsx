import { browserSupportsWebAuthn, startAuthentication } from '@simplewebauthn/browser'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getCsrfToken } from '@/api/csrf'
import type { Me } from '@/api/types'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { meKey } from './me'
import { SecondFactorPage } from './second-factor-page'

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
})

function renderPage() {
  return renderWithProviders(<SecondFactorPage />, { path: '/login/2fa', me: loginAs('admin', { stage: 'preauth' }) })
}

describe('SecondFactorPage', () => {
  it('offers exactly the factors listed in Me.second_factors', async () => {
    await renderPage()
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Authenticator-App', 'Passkey', 'Wiederherstellungscode'])
  })

  it('lands on / with a fresh full-stage Me after a valid TOTP code', async () => {
    const user = userEvent.setup()
    const { router, queryClient } = await renderPage()
    await user.type(screen.getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(screen.getByRole('button', { name: 'Bestätigen' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    const me = queryClient.getQueryData<Me>(meKey)
    expect(me?.stage).toBe('full')
    expect(getCsrfToken()).toBe(me?.csrf_token)
  })

  it('shows an error for a wrong code without logging the user out', async () => {
    const user = userEvent.setup()
    const { router } = await renderPage()
    await user.type(screen.getByLabelText('Code aus der Authenticator-App'), '000000')
    await user.click(screen.getByRole('button', { name: 'Bestätigen' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Der Code ist ungültig.')
    expect(router.state.location.pathname).toBe('/login/2fa')
  })

  it('accepts a recovery code typed with dashes and capitals', async () => {
    const user = userEvent.setup()
    const { router } = await renderPage()
    await user.click(screen.getByRole('tab', { name: 'Wiederherstellungscode' }))
    await user.type(screen.getByLabelText('Einer deiner Wiederherstellungscodes'), 'ABCDE-FGHJK')
    await user.click(screen.getByRole('button', { name: 'Bestätigen' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })

  it('signs in with a passkey', async () => {
    vi.mocked(startAuthentication).mockResolvedValue({
      id: 'c',
      rawId: 'c',
      type: 'public-key',
      response: {},
      clientExtensionResults: {},
    } as unknown as Awaited<ReturnType<typeof startAuthentication>>)
    const user = userEvent.setup()
    const { router } = await renderPage()
    await user.click(screen.getByRole('tab', { name: 'Passkey' }))
    await user.click(screen.getByRole('button', { name: 'Mit Passkey anmelden' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })

  it('hides the passkey tab when WebAuthn is unsupported', async () => {
    vi.mocked(browserSupportsWebAuthn).mockReturnValue(false)
    await renderPage()
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['Authenticator-App', 'Wiederherstellungscode'])
  })
})
