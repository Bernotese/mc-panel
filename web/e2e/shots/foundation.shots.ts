import { expect, test } from '../test'
import { shoot, signInMock } from './shoot'

test('foundation: login and dashboard', async ({ page }) => {
  await page.goto('/')
  await shoot(page, 'foundation-login')
  await signInMock(page)
  await shoot(page, 'foundation-dashboard')
})

test('foundation: forced password change', async ({ page }) => {
  await signInMock(page)
  await page.getByRole('link', { name: 'Benutzer', exact: true }).click()
  await page.getByRole('button', { name: 'Benutzer anlegen' }).click()
  const dialog = page.getByRole('dialog', { name: 'Benutzer anlegen' })
  await dialog.getByLabel('Benutzername').fill('carol')
  await dialog.getByLabel('Initiales Passwort').fill('long-enough-password')
  await shoot(page, 'foundation-create-user-hint')
  await dialog.getByRole('button', { name: 'Anlegen' }).click()
  const sudo = page.getByRole('dialog', { name: 'Aktion bestätigen' })
  await sudo.getByLabel('Code aus der Authenticator-App').fill('123456')
  await sudo.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(page.getByRole('row', { name: /carol/ })).toBeVisible()
  await shoot(page, 'foundation-users-must-change')
  await page.getByRole('button', { name: 'Abmelden', exact: true }).click()
  await page.getByLabel('Benutzername').fill('carol')
  await page.getByLabel('Passwort').fill('long-enough-password')
  await page.getByRole('button', { name: 'Anmelden' }).click()
  await expect(page.getByRole('heading', { name: 'Neues Passwort wählen' })).toBeVisible()
  await page.getByLabel('Aktuelles Passwort').fill('long-enough-password')
  await page.getByLabel('Neues Passwort', { exact: true }).fill('long-enough-password')
  await shoot(page, 'foundation-change-password')
})
