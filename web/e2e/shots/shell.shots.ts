import { expect, test } from '@playwright/test'
import { openServer, serverSectionLink, serverTree } from '../helpers'
import { shoot, signInMock } from './shoot'

test('shell: dashboard, server sections, collapsed tree, expanded rail', async ({ page }) => {
  await signInMock(page)
  await shoot(page, 'shell-dashboard')
  await openServer(page, 'survival')
  await shoot(page, 'shell-server-overview')
  await serverSectionLink(page, 'survival', 'Konsole').click()
  await shoot(page, 'shell-server-console')
  await serverSectionLink(page, 'survival', 'Einstellungen').click()
  await shoot(page, 'shell-server-settings')
  await page.getByRole('button', { name: 'Serverliste einklappen' }).click()
  await shoot(page, 'shell-tree-collapsed')
  await page.getByRole('button', { name: 'Menü ausklappen' }).click()
  await shoot(page, 'shell-rail-expanded')
  await page.getByRole('button', { name: 'Serverliste ausklappen' }).click()
  await shoot(page, 'shell-rail-expanded-tree')
})

test('shell: phone drawer and tablet flyout', async ({ page }) => {
  await signInMock(page)
  await openServer(page, 'survival')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: 'Menü öffnen' }).click()
  await page.waitForTimeout(400)
  await page.screenshot({ path: 'screenshots/shell-drawer-mobile.png' })
  await page.keyboard.press('Escape')
  await page.setViewportSize({ width: 900, height: 800 })
  await page.getByRole('button', { name: 'Serverliste öffnen' }).click()
  await page.waitForTimeout(400)
  await page.screenshot({ path: 'screenshots/shell-flyout-tablet.png' })
})

test('shell: rail and tree stay in place on a long, scrolled page', async ({ page }) => {
  await signInMock(page)
  await serverTree(page).getByRole('link', { name: 'Neuer Server' }).click()
  await expect(page.getByRole('button', { name: 'Server anlegen' })).toBeVisible()
  await page.setViewportSize({ width: 1440, height: 700 })
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight))
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(300)
  // A viewport shot (not fullPage): this is what a browser shows after scrolling down.
  await page.screenshot({ path: 'screenshots/shell-scrolled-desktop.png' })
})
