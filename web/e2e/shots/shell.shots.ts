import { test } from '@playwright/test'
import { openServer, serverSectionLink } from '../helpers'
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
