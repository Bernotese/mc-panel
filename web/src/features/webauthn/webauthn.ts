import { browserSupportsWebAuthn, startAuthentication, startRegistration } from '@simplewebauthn/browser'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Me, PasskeyCreated, WebAuthnOptions } from '@/api/types'

type RegistrationOptionsJSON = Parameters<typeof startRegistration>[0]['optionsJSON']
type AuthenticationOptionsJSON = Parameters<typeof startAuthentication>[0]['optionsJSON']
type CredentialJSON = Record<string, unknown>

export function isWebAuthnSupported(): boolean {
  try {
    return browserSupportsWebAuthn()
  } catch {
    return false
  }
}

/** go-webauthn answers with CredentialCreationOptions/CredentialRequestOptions: { publicKey: {...} }. */
export function extractPublicKey<T>(options: WebAuthnOptions): T {
  const publicKey = (options as { publicKey?: unknown }).publicKey
  if (typeof publicKey !== 'object' || publicKey === null) throw new Error('WebAuthn options without publicKey')
  return publicKey as T
}

function asCredential(value: object): CredentialJSON {
  return value as unknown as CredentialJSON
}

export async function loginWithPasskey(): Promise<Me> {
  const options = await unwrap(api.POST('/auth/2fa/webauthn/begin'))
  const credential = await startAuthentication({ optionsJSON: extractPublicKey<AuthenticationOptionsJSON>(options) })
  return unwrap(api.POST('/auth/2fa/webauthn/finish', { body: { credential: asCredential(credential) } }))
}

export async function sudoWithPasskey(): Promise<number> {
  const options = await unwrap(api.POST('/auth/sudo/webauthn/begin'))
  const credential = await startAuthentication({ optionsJSON: extractPublicKey<AuthenticationOptionsJSON>(options) })
  const result = await unwrap(api.POST('/auth/sudo/webauthn/finish', { body: { credential: asCredential(credential) } }))
  return result.sudo_until
}

/** currentPassword is required for the first second factor (SEC-15) and ignored otherwise. */
export async function registerPasskey(name: string, currentPassword?: string): Promise<PasskeyCreated> {
  const options = await unwrap(api.POST('/me/passkeys/begin'))
  const credential = await startRegistration({ optionsJSON: extractPublicKey<RegistrationOptionsJSON>(options) })
  const body = { name, credential: asCredential(credential), ...(currentPassword ? { current_password: currentPassword } : {}) }
  return unwrap(api.POST('/me/passkeys/finish', { body }))
}
