import { expect, test } from './test'
import { confirmSudo, openServer, openServerSection, serverSectionLink, serverTree, signIn } from './helpers'

test('admin creates a dev instance, pauses its sync and adds a dev op', async ({ page }) => {
  await signIn(page, 'admin', true)
  await openServer(page, 'creative')
  await page.getByRole('button', { name: 'dev-creative anlegen' }).click()
  await confirmSudo(page)
  await expect(page.getByRole('heading', { name: 'dev-creative', level: 1 })).toBeVisible()
  await expect(page.getByText('dev-creative angelegt. Der erste Sync läuft.')).toBeVisible()

  // The dev hangs under its prod in the tree and says what it copies.
  const nested = serverTree(page).getByRole('list', { name: 'Dev-Instanz von creative' })
  await expect(nested.getByRole('link', { name: 'dev-creative', exact: true })).toBeVisible()
  const banner = page.getByRole('region', { name: 'Dev-Instanz' })
  await expect(banner.getByRole('link', { name: 'creative', exact: true })).toBeVisible()
  // The first sync ends within a few seconds in the mock.
  await expect(banner.getByText(/Änderungen werden beim nächsten Sync überschrieben/)).toBeVisible({ timeout: 15_000 })
  await expect(banner.getByText(/^Letzter Sync: /)).toBeVisible()
  await expect(serverSectionLink(page, 'dev-creative', 'Backups')).toHaveCount(0)

  await banner.getByRole('switch', { name: 'Sync pausieren' }).click()
  await expect(page.getByText('Sync pausiert.')).toBeVisible()
  await expect(banner.getByText(/Sync pausiert seit/)).toBeVisible()

  await openServerSection(page, 'dev-creative', 'Spieler')
  const ops = page.getByRole('region', { name: 'Dev-Ops' })
  await ops.getByLabel('Dev-Op hinzufügen').fill('Alex')
  await ops.getByRole('button', { name: 'Hinzufügen' }).click()
  await expect(ops.getByText('Alex', { exact: true })).toBeVisible()

  // The prod cannot be deleted while it has a dev.
  await openServer(page, 'creative')
  await openServerSection(page, 'creative', 'Einstellungen')
  const danger = page.getByRole('region', { name: 'Server löschen' })
  await expect(danger.getByText('Dieser Server hat eine Dev-Instanz. Lösche zuerst sie.')).toBeVisible()
  await expect(danger.getByRole('button', { name: 'Server löschen' })).toBeDisabled()
})
