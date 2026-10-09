import { expect, test, type Page } from '@playwright/test'
import { totp } from './totp'

const username = process.env.E2E_ADMIN_USER ?? 'e2e-admin'
const password = process.env.E2E_ADMIN_PASSWORD ?? ''

test.skip(!process.env.E2E_BASE_URL || !password, 'requires E2E_BASE_URL and E2E_ADMIN_PASSWORD of a fresh admin without 2FA')

async function submitPassword(page: Page) {
  await page.goto('/login')
  await page.getByLabel('Benutzername').fill(username)
  await page.getByLabel('Passwort').fill(password)
  await page.getByRole('button', { name: 'Anmelden' }).click()
}

test('fresh admin: forced TOTP setup, recovery codes, sign-in again with TOTP', async ({ page }) => {
  await submitPassword(page)
  await expect(page).toHaveURL(/\/setup-2fa$/)

  await page.getByRole('button', { name: 'Einrichtung starten' }).click()
  await expect(page.getByRole('img', { name: 'QR-Code für die Authenticator-App' })).toBeVisible()
  const secret = ((await page.getByTestId('totp-secret').textContent()) ?? '').trim()
  expect(secret).toMatch(/^[A-Z2-7]+=*$/)
  await page.getByLabel('Code aus der Authenticator-App').fill(totp(secret))
  await page.getByRole('button', { name: 'Aktivieren' }).click()

  await expect(page.getByRole('list', { name: 'Wiederherstellungscodes' }).getByRole('listitem')).toHaveCount(10)
  await page.getByRole('checkbox', { name: 'Ich habe die Codes sicher gespeichert.' }).check()
  await page.getByRole('button', { name: 'Weiter zum Panel' }).click()
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()

  const sid = (await page.context().cookies()).find((c) => c.name === '__Host-sid')
  expect(sid).toMatchObject({ httpOnly: true, secure: true, sameSite: 'Strict', path: '/' })

  await page.getByRole('button', { name: 'Abmelden' }).click()
  await expect(page).toHaveURL(/\/login$/)

  await submitPassword(page)
  await expect(page).toHaveURL(/\/login\/2fa$/)
  // The setup code consumed the current 30 s step (replay protection); the next step is within the ±1 tolerance.
  await page.getByLabel('Code aus der Authenticator-App').fill(totp(secret, Date.now() + 30_000))
  await page.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()
})
