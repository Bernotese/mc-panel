import type { Page } from '@playwright/test'
import { test } from '../test'
import { shoot, signInMock } from './shoot'

/** Works before and after T2's routes: old tabs or new section links. */
async function openSection(page: Page, name: string) {
  await page.getByRole('tab', { name, exact: true }).or(page.getByRole('link', { name, exact: true })).first().click()
}

test('live: dashboard, console, players, monitoring', async ({ page }) => {
  await signInMock(page)
  await shoot(page, 'live-dashboard')
  await page.getByRole('link', { name: 'survival', exact: true }).first().click()
  await openSection(page, 'Konsole')
  await page.getByRole('log', { name: 'Server-Log' }).waitFor()
  await shoot(page, 'live-console')
  await openSection(page, 'Spieler')
  await page.getByRole('region', { name: 'Online' }).waitFor()
  await shoot(page, 'live-players')
  await openSection(page, 'Monitoring')
  await page.getByRole('region', { name: 'Jetzt' }).waitFor()
  await shoot(page, 'live-monitoring')
})
