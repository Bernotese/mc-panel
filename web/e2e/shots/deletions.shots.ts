import { test } from '../test'
import { shoot, signInMock } from './shoot'

// #148: the browser mock seeds a failed, a partial and a completed deletion.
test('deletions: dashboard and settings', async ({ page }) => {
  await signInMock(page)
  await page.getByRole('region', { name: 'Löschungen in Arbeit' }).waitFor()
  await shoot(page, 'deletions-dashboard')
  await page.getByRole('link', { name: 'Alle Löschungen' }).click()
  await page.getByRole('region', { name: 'Gelöschte Server' }).getByRole('list', { name: 'Löschungen' }).waitFor()
  await shoot(page, 'deletions-settings')
})
