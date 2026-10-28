import { expect } from '@playwright/test'
import { test } from '../test'
import { shoot, signInMock } from './shoot'

// Offline, the next poll pauses (TanStack's onlineManager) and the surfaces mark their cached data stale (issue #155).
test('stale: dashboard, server header and monitoring while offline', async ({ page, context }) => {
  test.setTimeout(120_000)
  await signInMock(page)
  await page.getByRole('article', { name: 'survival', exact: true }).waitFor()
  await context.setOffline(true)
  await expect(page.getByTestId('stale-notice')).toBeVisible({ timeout: 40_000 })
  await shoot(page, 'stale-dashboard')

  await context.setOffline(false)
  await expect(page.getByTestId('stale-notice')).toHaveCount(0, { timeout: 40_000 })
  await page.getByRole('link', { name: 'survival', exact: true }).first().click()
  await page.getByRole('link', { name: 'Monitoring', exact: true }).first().click()
  await page.getByRole('region', { name: 'Jetzt' }).waitFor()
  await context.setOffline(true)
  // The header notice follows with the next detail poll (≤ 15 s) and replaces the tab's own: one notice per page.
  const header = page.getByRole('region', { name: 'survival', exact: true })
  await expect(header.getByTestId('stale-notice')).toBeVisible({ timeout: 40_000 })
  await expect(page.getByRole('region', { name: 'Jetzt' }).locator('[data-stale]').first()).toBeVisible({ timeout: 40_000 })
  await expect(page.getByTestId('stale-notice')).toHaveCount(1)
  await shoot(page, 'stale-monitoring')
  await context.setOffline(false)
})
