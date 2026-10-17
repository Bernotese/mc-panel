import { expect, type Page } from '@playwright/test'

const VIEWPORTS = { desktop: { width: 1440, height: 900 }, mobile: { width: 390, height: 844 } } as const

/**
 * Switches the theme the way a user with preference "system" sees it: the stored preference stays "system" and the
 * emulated colour scheme flips, so the app (watchSystemTheme) and the toaster (theme="system") follow together.
 */
async function setTheme(page: Page, theme: 'dark' | 'light') {
  await page.evaluate(() => localStorage.setItem('mcpanel.theme', 'system'))
  await page.emulateMedia({ colorScheme: theme })
}

// Without transitions a shot never catches a colour change halfway (washed-out badges in light shots).
const NO_TRANSITIONS = '*, *::before, *::after { transition: none !important; animation-duration: 0s !important; }'

/** Screenshots of the current page in both themes and both viewports (web/screenshots/, not committed). */
export async function shoot(page: Page, name: string) {
  // With the page's style nonce (SEC-31): addStyleTag would be blocked by the CSP and trip the violation guard.
  await page.evaluate((css) => {
    const style = document.createElement('style')
    style.nonce = document.querySelector<HTMLMetaElement>('meta[property="csp-nonce"]')?.nonce ?? ''
    style.textContent = css
    document.head.append(style)
  }, NO_TRANSITIONS)
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
