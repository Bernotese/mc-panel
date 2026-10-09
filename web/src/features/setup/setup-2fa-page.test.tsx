import { browserSupportsWebAuthn, startRegistration } from '@simplewebauthn/browser'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Me } from '@/api/types'
import { meKey } from '@/features/auth/me'
import { loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { Setup2faPage } from './setup-2fa-page'

vi.mock('qrcode', () => ({ default: { toDataURL: vi.fn(async () => 'data:image/png;base64,QR') } }))
vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
})

function renderPage() {
  return renderWithProviders(<Setup2faPage />, { path: '/setup-2fa', me: loginAs('newadmin', { stage: 'setup' }) })
}

describe('Setup2faPage', () => {
  it('keeps the recovery codes on screen after Me turned full, then continues to /', async () => {
    const user = userEvent.setup()
    const { router, queryClient } = await renderPage()
    await user.click(screen.getByRole('button', { name: 'Einrichtung starten' }))
    await user.type(await screen.findByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(screen.getByRole('button', { name: 'Aktivieren' }))

    const list = await screen.findByRole('list', { name: 'Wiederherstellungscodes' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(10)
    await waitFor(() => expect(queryClient.getQueryData<Me>(meKey)?.stage).toBe('full'))
    expect(screen.getByRole('list', { name: 'Wiederherstellungscodes' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/setup-2fa')

    await user.click(screen.getByRole('checkbox', { name: 'Ich habe die Codes sicher gespeichert.' }))
    await user.click(screen.getByRole('button', { name: 'Weiter zum Panel' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })

  it('registers a passkey as first factor and shows the codes', async () => {
    vi.mocked(startRegistration).mockResolvedValue({
      id: 'c',
      rawId: 'c',
      type: 'public-key',
      response: {},
      clientExtensionResults: {},
    } as unknown as Awaited<ReturnType<typeof startRegistration>>)
    const user = userEvent.setup()
    await renderPage()
    await user.click(screen.getByRole('tab', { name: 'Passkey' }))
    await user.click(screen.getByRole('button', { name: 'Passkey registrieren' }))
    expect(await screen.findByRole('list', { name: 'Wiederherstellungscodes' })).toBeInTheDocument()
  })

  it('offers only TOTP when WebAuthn is unsupported', async () => {
    vi.mocked(browserSupportsWebAuthn).mockReturnValue(false)
    await renderPage()
    expect(screen.queryByRole('tab', { name: 'Passkey' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Einrichtung starten' })).toBeInTheDocument()
  })
})
