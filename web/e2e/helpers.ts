import { expect, type Locator, type Page } from '@playwright/test'

export const PASSWORD = 'correct-horse-battery'
export const TOTP_CODE = '123456'

/** Signs in via the UI; the mock state lives in the page, so the test must not reload afterwards. */
export async function signIn(page: Page, username: string, withTotp: boolean) {
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
  await page.getByLabel('Benutzername').fill(username)
  await page.getByLabel('Passwort').fill(PASSWORD)
  await page.getByRole('button', { name: 'Anmelden' }).click()
  if (withTotp) {
    await expect(page).toHaveURL(/\/login\/2fa$/)
    await page.getByLabel('Code aus der Authenticator-App').fill(TOTP_CODE)
    await page.getByRole('button', { name: 'Bestätigen' }).click()
  }
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()
}

export async function confirmSudo(page: Page) {
  const sudo = page.getByRole('dialog', { name: 'Aktion bestätigen' })
  await sudo.getByLabel('Code aus der Authenticator-App').fill(TOTP_CODE)
  await sudo.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(sudo).toBeHidden()
}

/** Server tree (spec §4.1); e2e runs at 1280 px, so the tree is always visible. */
export function serverTree(page: Page): Locator {
  return page.getByRole('navigation', { name: 'Server', exact: true })
}

/** Opens a server from the tree; names are unique there (the dashboard has the same link names). */
export async function openServer(page: Page, name: string) {
  await serverTree(page).getByRole('link', { name, exact: true }).click()
  await expect(page.getByRole('heading', { name, level: 1 })).toBeVisible()
}

/** Link of a section of the expanded server, e.g. serverSectionLink(page, 'survival', 'Dateien'). */
export function serverSectionLink(page: Page, server: string, label: string): Locator {
  return serverTree(page).getByRole('list', { name: `Bereiche von ${server}` }).getByRole('link', { name: label, exact: true })
}

export async function openServerSection(page: Page, server: string, label: string) {
  await serverSectionLink(page, server, label).click()
}
