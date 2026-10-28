import { expect, test, type Page } from './test'
import { nextTotp, totp, totpStep } from './totp'

const username = process.env.E2E_ADMIN_USER ?? 'e2e-admin'
const password = process.env.E2E_ADMIN_PASSWORD ?? ''

test.skip(!process.env.E2E_BASE_URL || !password, 'requires E2E_BASE_URL and E2E_ADMIN_PASSWORD of a fresh admin without 2FA')

async function submitPassword(page: Page) {
  await page.goto('/login')
  await page.getByLabel('Benutzername').fill(username)
  await page.getByLabel('Passwort').fill(password)
  await page.getByRole('button', { name: 'Anmelden' }).click()
}

test('fresh admin: forced TOTP setup, recovery codes, TOTP sign-in, passkey registration and passkey sign-in', async ({ page, browserName }) => {
  test.skip(browserName !== 'chromium', 'the virtual authenticator needs the Chrome DevTools Protocol')
  // Waiting for the next TOTP step (sudo) can take up to 30 s on top of the flow itself.
  test.setTimeout(120_000)

  // A CTAP2 platform authenticator with user verification, driven through CDP: the browser's
  // real WebAuthn stack talks to the real backend, only the authenticator is virtual.
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('WebAuthn.enable')
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true },
  })

  await submitPassword(page)
  await expect(page).toHaveURL(/\/setup-2fa$/)

  await page.getByRole('button', { name: 'Einrichtung starten' }).click()
  await expect(page.getByRole('img', { name: 'QR-Code für die Authenticator-App' })).toBeVisible()
  const secret = ((await page.getByTestId('totp-secret').textContent()) ?? '').trim()
  expect(secret).toMatch(/^[A-Z2-7]+=*$/)
  let lastStep = totpStep()
  await page.getByLabel('Code aus der Authenticator-App').fill(totp(secret))
  await page.getByLabel('Aktuelles Passwort').fill(password) // the first second factor needs it (SEC-15)
  await page.getByRole('button', { name: 'Aktivieren' }).click()

  await expect(page.getByRole('list', { name: 'Wiederherstellungscodes' }).getByRole('listitem')).toHaveCount(10)
  await page.getByRole('checkbox', { name: 'Ich habe die Codes sicher gespeichert.' }).check()
  await page.getByRole('button', { name: 'Weiter zum Panel' }).click()
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()

  const sid = (await page.context().cookies()).find((c) => c.name === '__Host-sid')
  expect(sid).toMatchObject({ httpOnly: true, secure: true, sameSite: 'Strict', path: '/' })

  // A logout the server never confirmed must not look like one: the HttpOnly cookie still works.
  await page.route('**/api/auth/logout', (route) => route.abort('failed'))
  await page.getByRole('button', { name: 'Abmelden', exact: true }).click()
  await expect(page.getByText('Abmelden fehlgeschlagen: Die Sitzung ist noch aktiv.')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()
  expect((await page.request.get('/api/auth/me')).status()).toBe(200)

  // The retry revokes the session on the server; later authenticated requests fail.
  await page.unroute('**/api/auth/logout')
  await page.getByRole('button', { name: 'Erneut versuchen' }).click()
  await expect(page).toHaveURL(/\/login$/)
  expect((await page.request.get('/api/auth/me')).status()).toBe(401)

  // Sign in again with TOTP. The setup code consumed its step (replay protection), so the next step is used.
  await submitPassword(page)
  await expect(page).toHaveURL(/\/login\/2fa$/)
  let next = await nextTotp(secret, lastStep)
  lastStep = next.step
  await page.getByLabel('Code aus der Authenticator-App').fill(next.code)
  await page.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()

  // Register a passkey in the profile. Adding a second factor needs sudo, confirmed with TOTP.
  await page.goto('/profile')
  await page.getByRole('button', { name: 'Passkey hinzufügen' }).click()
  await page.getByRole('button', { name: 'Passkey registrieren' }).click()
  const sudo = page.getByRole('dialog', { name: 'Aktion bestätigen' })
  await expect(sudo).toBeVisible()
  next = await nextTotp(secret, lastStep)
  await sudo.getByLabel('Code aus der Authenticator-App').fill(next.code)
  await sudo.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(page.getByRole('table', { name: 'Registrierte Passkeys' }).getByText('Mein Passkey')).toBeVisible()
  const { credentials } = await cdp.send('WebAuthn.getCredentials', { authenticatorId })
  expect(credentials).toHaveLength(1)
  expect(credentials[0].isResidentCredential).toBe(true)

  await page.getByRole('button', { name: 'Abmelden', exact: true }).click()
  await expect(page).toHaveURL(/\/login$/)
  expect((await page.request.get('/api/auth/me')).status()).toBe(401)

  // Sign in with the passkey as second factor.
  await submitPassword(page)
  await expect(page).toHaveURL(/\/login\/2fa$/)
  await page.getByRole('tab', { name: 'Passkey' }).click()
  await page.getByRole('button', { name: 'Mit Passkey anmelden' }).click()
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()
  const after = await cdp.send('WebAuthn.getCredentials', { authenticatorId })
  expect(after.credentials[0].signCount).toBeGreaterThan(0)
})
