import { test } from '../test'
import { shoot, signInMock } from './shoot'

test('foundation: login and dashboard', async ({ page }) => {
  await page.goto('/')
  await shoot(page, 'foundation-login')
  await signInMock(page)
  await shoot(page, 'foundation-dashboard')
})
