import { expect, type Page } from '@playwright/test'

const VIEWPORTS = { desktop: { width: 1440, height: 900 }, mobile: { width: 390, height: 844 } } as const

async function setTheme(page: Page, theme: 'dark' | 'light') {
  await page.evaluate((t) => {
    localStorage.setItem('mcpanel.theme', t)
    document.documentElement.classList.toggle('dark', t === 'dark')
    document.documentElement.style.colorScheme = t
  }, theme)
}

/** Screenshots of the current page in both themes and both viewports (web/screenshots/, not committed). */
export async function shoot(page: Page, name: string) {
  for (const [vp, size] of Object.entries(VIEWPORTS)) {
    await page.setViewportSize(size)
    for (const theme of ['dark', 'light'] as const) {
      await setTheme(page, theme)
      await page.waitForTimeout(150)
      await page.screenshot({ path: `screenshots/${name}-${theme}-${vp}.png`, fullPage: true })
    }
  }
  await page.setViewportSize(VIEWPORTS.desktop)
}

/** Signs in against the MSW mocks; the mock state lives in the page, so callers navigate by clicking, not page.goto. */
export async function signInMock(page: Page, user: 'admin' | 'alice' = 'admin') {
  await page.goto('/')
  await page.getByLabel('Benutzername').fill(user)
  await page.getByLabel('Passwort').fill('correct-horse-battery')
  await page.getByRole('button', { name: 'Anmelden' }).click()
  if (user === 'admin') {
    await page.getByLabel('Code aus der Authenticator-App').fill('123456')
    await page.getByRole('button', { name: 'Bestätigen' }).click()
  }
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
}
