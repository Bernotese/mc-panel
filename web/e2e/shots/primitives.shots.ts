import { expect, test } from '../test'
import { shoot, signInMock } from './shoot'

// Navigation only by clicking: the MSW state lives in the page (no page.goto after sign-in).
test('primitives: buttons, badges, dialogs, tables, tabs, toasts', async ({ page }) => {
  await signInMock(page)

  // Server list: buttons, badges; stop confirmation = destructive button in a dialog.
  await shoot(page, 'primitives-dashboard')
  const survival = page.getByRole('article', { name: 'survival' })
  await survival.getByRole('button', { name: 'Stoppen' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await shoot(page, 'primitives-confirm-dialog')
  await page.getByRole('dialog').getByRole('button', { name: 'Stoppen' }).click()
  await expect(page.getByText('Stopp für „survival“ angefordert.')).toBeVisible()
  await shoot(page, 'primitives-toast')

  // Users: dense table and a form dialog with inputs.
  await page.getByRole('link', { name: 'Benutzer', exact: true }).click()
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
  await shoot(page, 'primitives-table')
  await page.getByRole('button', { name: 'Benutzer anlegen' }).click()
  await expect(page.getByRole('dialog', { name: 'Benutzer anlegen' })).toBeVisible()
  await shoot(page, 'primitives-form-dialog')
  await page.keyboard.press('Escape')

  // Security center: underline tabs, switches, selects.
  await page.getByRole('link', { name: 'Security Center' }).click()
  await shoot(page, 'primitives-tabs')
  await page.getByRole('tab', { name: 'Einstellungen' }).click()
  await shoot(page, 'primitives-switches')
})
