import { browserSupportsWebAuthn, startAuthentication, startRegistration } from '@simplewebauthn/browser'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { setCsrfToken } from '@/api/csrf'
import { loginAs, WEBAUTHN_CHALLENGE } from '@/mocks/db'
import { extractPublicKey, isWebAuthnSupported, loginWithPasskey, registerPasskey, sudoWithPasskey } from './webauthn'

vi.mock('@simplewebauthn/browser', () => ({
  browserSupportsWebAuthn: vi.fn(() => true),
  startAuthentication: vi.fn(),
  startRegistration: vi.fn(),
}))

const assertion = { id: 'cred-1', rawId: 'cred-1', type: 'public-key', response: {}, clientExtensionResults: {} }

beforeEach(() => {
  vi.mocked(browserSupportsWebAuthn).mockReturnValue(true)
  vi.mocked(startAuthentication).mockResolvedValue(assertion as unknown as Awaited<ReturnType<typeof startAuthentication>>)
  vi.mocked(startRegistration).mockResolvedValue(assertion as unknown as Awaited<ReturnType<typeof startRegistration>>)
})

describe('webauthn helpers', () => {
  it('feature-detects support and never throws', () => {
    expect(isWebAuthnSupported()).toBe(true)
    vi.mocked(browserSupportsWebAuthn).mockImplementation(() => {
      throw new Error('no window.PublicKeyCredential')
    })
    expect(isWebAuthnSupported()).toBe(false)
  })

  it('unwraps the go-webauthn {publicKey} envelope', () => {
    expect(extractPublicKey<{ challenge: string }>({ publicKey: { challenge: 'abc' } })).toEqual({ challenge: 'abc' })
    expect(() => extractPublicKey({ foo: 1 })).toThrow('publicKey')
  })

  it('logs in with a passkey and passes optionsJSON = publicKey', async () => {
    setCsrfToken(loginAs('admin', { stage: 'preauth' }).csrf_token)
    const me = await loginWithPasskey()
    expect(me.stage).toBe('full')
    expect(vi.mocked(startAuthentication).mock.calls[0][0].optionsJSON).toMatchObject({ challenge: WEBAUTHN_CHALLENGE })
  })

  it('confirms sudo with a passkey', async () => {
    setCsrfToken(loginAs('admin').csrf_token)
    expect(await sudoWithPasskey()).toBeGreaterThan(Date.now())
  })

  it('registers a named passkey and returns first-time recovery codes', async () => {
    setCsrfToken(loginAs('newadmin', { stage: 'setup' }).csrf_token)
    const created = await registerPasskey('YubiKey 5C')
    expect(created.passkey.name).toBe('YubiKey 5C')
    expect(created.recovery_codes).toHaveLength(10)
    expect(vi.mocked(startRegistration).mock.calls[0][0].optionsJSON).toMatchObject({ rp: { id: 'localhost' } })
  })
})
