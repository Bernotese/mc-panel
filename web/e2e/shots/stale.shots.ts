import { expect } from '@playwright/test'
import { test } from '../test'
import { shoot, signInMock } from './shoot'

// Offline, the next poll pauses (TanStack's onlineManager) and the surfaces mark their cached data stale (issue #155).
test('stale: dashboard, server header and monitoring while offline', async ({ page, context }) => {
  test.setTimeout(120_000)
  await signInMock(page)
  await page.getByRole('article', { name: 'survival' }).waitFor()
  await context.setOffline(true)
  await expect(page.getByTestId('stale-notice')).toBeVisible({ timeout: 40_000 })
  await shoot(page, 'stale-dashboard')

  await context.setOffline(false)
  await expect(page.getByTestId('stale-notice')).toHaveCount(0, { timeout: 40_000 })
  await page.getByRole('link', { name: 'survival', exact: true }).first().click()
  await page.getByRole('link', { name: 'Monitoring', exact: true }).first().click()
  await page.getByRole('region', { name: 'Jetzt' }).waitFor()
  await context.setOffline(true)
  await expect(page.getByTestId('stale-notice').first()).toBeVisible({ timeout: 40_000 })
  // The header notice follows with the next detail poll (≤ 15 s).
  await expect(page.getByTestId('stale-notice')).toHaveCount(2, { timeout: 40_000 })
  await shoot(page, 'stale-monitoring')
  await context.setOffline(false)
})
