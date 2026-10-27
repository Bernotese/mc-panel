import { test } from '../test'
import { shoot, signInMock } from './shoot'

test('dev instances: dashboard, banner, settings, dev ops, create', async ({ page }) => {
  await signInMock(page)
  await page.getByRole('article', { name: 'dev-survival', exact: true }).waitFor()
  await shoot(page, 'dev-dashboard')

  const tree = page.getByRole('navigation', { name: 'Server', exact: true })
  await tree.getByRole('link', { name: 'dev-survival', exact: true }).click()
  await page.getByRole('region', { name: 'Dev-Instanz' }).waitFor()
  await shoot(page, 'dev-overview')

  await tree.getByRole('link', { name: 'Spieler', exact: true }).click()
  await page.getByRole('region', { name: 'Dev-Ops' }).getByText('Steve').waitFor()
  await shoot(page, 'dev-players')

  await tree.getByRole('link', { name: 'Einstellungen', exact: true }).click()
  await page.getByRole('region', { name: 'Sync und Ruhezustand' }).waitFor()
  await shoot(page, 'dev-settings')

  // Paused: the banner says since when and that dev keeps its changes.
  await page.getByRole('switch', { name: 'Sync pausieren' }).click()
  await page.getByText(/Sync pausiert seit/).waitFor()
  await shoot(page, 'dev-paused')

  await tree.getByRole('link', { name: 'survival', exact: true }).click()
  await page.getByRole('link', { name: 'dev-survival', exact: true }).last().waitFor()
  await shoot(page, 'dev-prod-overview')

  await tree.getByRole('link', { name: 'modded', exact: true }).click()
  await page.getByRole('button', { name: 'dev-modded anlegen' }).waitFor()
  await shoot(page, 'dev-create')
})
