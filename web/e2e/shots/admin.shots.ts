import { expect, test, type Page } from '../test'
import { shoot, signInMock } from './shoot'

async function openNav(page: Page, name: string) {
  await page.getByRole('navigation', { name: 'Hauptnavigation' }).getByRole('link', { name, exact: true }).click()
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
}

test('admin: auth screens', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('button', { name: 'Anmelden' })).toBeVisible()
  await shoot(page, 'admin-login')
  await page.getByLabel('Benutzername').fill('admin')
  await page.getByLabel('Passwort').fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Anmelden' }).click()
  await expect(page).toHaveURL(/\/login\/2fa$/)
  await shoot(page, 'admin-2fa')
})

test('admin: setup-2fa for a user without second factor', async ({ page }) => {
  await signInMock(page, 'alice')
  test.skip(!page.url().endsWith('/setup-2fa'), 'alice already has a second factor in the mock')
  await shoot(page, 'admin-setup-2fa')
})

test('admin: pages', async ({ page }) => {
  await signInMock(page)
  await openNav(page, 'Benutzer')
  await shoot(page, 'admin-users')
  await page.getByRole('link', { name: 'Rechte von alice' }).click()
  await expect(page.getByRole('heading', { name: 'Rechte von alice' })).toBeVisible()
  await page.getByRole('checkbox', { name: 'survival: Dateien' }).click()
  await shoot(page, 'admin-permissions')
  await openNav(page, 'Security Center')
  await expect(page.getByRole('img', { name: /Login-Versuche/ })).toBeVisible()
  await shoot(page, 'admin-security')
  await openNav(page, 'Audit-Log')
  await expect(page.getByRole('status')).toContainText('Kette gültig')
  await shoot(page, 'admin-audit')
  await openNav(page, 'Einstellungen')
  await expect(page.getByLabel('Basisdomain')).toBeVisible()
  await shoot(page, 'admin-settings')
  await openNav(page, 'Profil')
  await shoot(page, 'admin-profile')
  await openNav(page, 'Benutzer')
  await page.getByRole('button', { name: 'Benutzer anlegen' }).click()
  await expect(page.getByRole('dialog', { name: 'Benutzer anlegen' })).toBeVisible()
  await shoot(page, 'admin-create-user-dialog')
})
