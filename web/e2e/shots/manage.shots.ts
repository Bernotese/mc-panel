import { test, type Page } from '../test'
import { shoot, signInMock } from './shoot'

/** Before T2: tabs on the detail page; after T2: links in the server tree. Both are tried. */
async function openArea(page: Page, name: string) {
  const tab = page.getByRole('tab', { name, exact: true })
  const link = page.getByRole('link', { name, exact: true }).first()
  await tab.or(link).first().waitFor()
  if (await tab.count()) await tab.click()
  else await link.click()
}

test('manage: files, plugins, backups, settings, create', async ({ page }) => {
  await signInMock(page)
  await page.getByRole('link', { name: 'survival', exact: true }).first().click()

  await openArea(page, 'Dateien')
  await page.getByRole('region', { name: 'Dateien' }).getByRole('button', { name: 'server.properties', exact: true }).click()
  await page.getByRole('textbox', { name: 'Inhalt von server.properties' }).waitFor()
  await shoot(page, 'manage-files-editor')

  await openArea(page, 'Plugins')
  await shoot(page, 'manage-plugins-installed')
  await page.getByRole('tab', { name: 'Entdecken' }).click()
  await page.getByRole('region', { name: 'Entdecken' }).getByRole('listitem').first().waitFor()
  await shoot(page, 'manage-plugins-discover')

  await openArea(page, 'Backups')
  await shoot(page, 'manage-backups')

  await openArea(page, 'Einstellungen')
  await shoot(page, 'manage-settings')

  // The create link lives on the start page (before T2) or in the shell (after T2).
  const create = page.getByRole('link', { name: 'Server anlegen' }).first()
  if (!(await create.count())) await page.locator('a[href="/"]').first().click()
  await create.click()
  await page.getByRole('heading', { level: 1 }).waitFor()
  await shoot(page, 'manage-create')
})
