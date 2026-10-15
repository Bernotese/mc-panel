import { expect, test, type Page } from './test'

async function signInAsAdmin(page: Page) {
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
  await page.getByLabel('Benutzername').fill('admin')
  await page.getByLabel('Passwort').fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Anmelden' }).click()
  await expect(page).toHaveURL(/\/login\/2fa$/)
  await page.getByLabel('Code aus der Authenticator-App').fill('123456')
  await page.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()
}

test('admin signs in with password and TOTP and opens the security center', async ({ page }) => {
  await signInAsAdmin(page)
  await page.getByRole('link', { name: 'Security Center' }).click()
  await expect(page.getByRole('img', { name: /Login-Versuche \(24 Stunden\)/ })).toBeVisible()
  await page.getByRole('link', { name: 'Audit-Log' }).click()
  await expect(page.getByRole('status')).toContainText('Kette gültig')
})

test('sensitive actions ask for sudo once and then succeed', async ({ page }) => {
  await signInAsAdmin(page)
  await page.getByRole('link', { name: 'Benutzer', exact: true }).click()
  await page.getByRole('button', { name: 'Benutzer anlegen' }).click()
  const dialog = page.getByRole('dialog', { name: 'Benutzer anlegen' })
  await dialog.getByLabel('Benutzername').fill('carol')
  await dialog.getByLabel('Initiales Passwort').fill('long-enough-password')
  await dialog.getByRole('button', { name: 'Anlegen' }).click()
  const sudo = page.getByRole('dialog', { name: 'Aktion bestätigen' })
  await sudo.getByLabel('Code aus der Authenticator-App').fill('123456')
  await sudo.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(page.getByRole('row', { name: /carol/ })).toBeVisible()
  await expect(sudo).toBeHidden()
})

test.describe('phone (360 px)', () => {
  test.use({ viewport: { width: 360, height: 740 } })

  test('navigates via the menu sheet and shows cards instead of tables', async ({ page }) => {
    await signInAsAdmin(page)
    await page.getByRole('button', { name: 'Menü öffnen' }).click()
    await page.getByRole('dialog').getByRole('link', { name: 'Security Center' }).click()
    await page.getByRole('tab', { name: 'Login-Versuche' }).click()
    await expect(page.getByRole('list', { name: 'Login-Versuche' })).toBeVisible()
    await expect(page.locator('table:visible')).toHaveCount(0)
    const scrollWidth = await page.evaluate(() => document.documentElement.scrollWidth)
    expect(scrollWidth).toBeLessThanOrEqual(360)
  })
})

test('theme preference survives a reload', async ({ page }) => {
  await page.goto('/login')
  await page.getByRole('button', { name: /Farbschema wechseln/ }).click()
  const theme = await page.evaluate(() => localStorage.getItem('mcpanel.theme'))
  await page.reload()
  const hasDark = await page.evaluate(() => document.documentElement.classList.contains('dark'))
  expect(hasDark).toBe(theme === 'dark')
})
