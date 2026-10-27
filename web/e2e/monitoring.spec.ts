import { expect, test } from './test'
import { confirmSudo, openServerSection, signIn } from './helpers'

test('admin sees host and server values on the dashboard and explores the monitoring tab', async ({ page }) => {
  await signIn(page, 'admin', true)
  const host = page.getByRole('region', { name: 'Host' })
  await expect(host.getByText(/GiB frei von/)).toBeVisible()
  await expect(page.getByRole('article', { name: 'modded' }).getByText('Server down')).toBeVisible()

  const survival = page.getByRole('article', { name: 'survival', exact: true })
  await expect(survival.getByTestId('server-metrics')).toBeVisible()
  await survival.getByRole('link', { name: 'survival', exact: true }).click()
  await openServerSection(page, 'survival', 'Monitoring')

  const now = page.getByRole('region', { name: 'Jetzt' })
  await expect(now.getByRole('list', { name: 'Online-Spieler' })).toBeVisible()
  await expect(page.getByRole('img', { name: 'Spieler', exact: true })).toBeVisible()
  await expect(page.getByRole('img', { name: 'TPS und MSPT' })).toBeVisible()

  await page.getByRole('button', { name: '7 Tage' }).click()
  await expect(page.getByRole('button', { name: '7 Tage' })).toHaveAttribute('aria-pressed', 'true')
  await page.getByRole('button', { name: 'Als Tabelle anzeigen' }).first().click()
  await expect(page.getByRole('table', { name: 'Spieler', exact: true })).toBeVisible()

  // Keyboard readout on the players chart.
  await page.getByRole('img', { name: 'Spieler', exact: true }).focus()
  await page.keyboard.press('End')
  await expect(page.getByRole('status').filter({ hasText: 'Spieler:' }).first()).toBeVisible()

  await expect(page.getByRole('switch', { name: 'Down-Alerts für diesen Server' })).toBeChecked()
})

test('admin changes an alert threshold with sudo', async ({ page }) => {
  await signIn(page, 'admin', true)
  // exact: the host card links to "Monitoring-Einstellungen" too.
  await page.getByRole('navigation', { name: 'Hauptnavigation' }).getByRole('link', { name: 'Einstellungen', exact: true }).click()
  const card = page.getByRole('region', { name: 'Monitoring & Alerts' })
  const field = card.getByLabel('Server down nach (Minuten)')
  await field.fill('5')
  await card.getByRole('button', { name: 'Einstellungen speichern' }).click()
  await confirmSudo(page)
  await expect(page.getByText('Monitoring-Einstellungen gespeichert.')).toBeVisible()
  await expect(field).toHaveValue('5')
})

test('a user without the admin role sees server values but no host card', async ({ page }) => {
  await signIn(page, 'alice', false)
  await expect(page.getByRole('article', { name: 'survival', exact: true }).getByTestId('server-metrics')).toBeVisible()
  await expect(page.getByRole('region', { name: 'Host' })).toHaveCount(0)
})
