import { test } from '../test'
import { shoot, signInMock } from './shoot'

// Issue #159: the lower-load preset in the server settings, with the tradeoff shown before applying.
test('distance preset: tradeoff before applying', async ({ page }) => {
  await signInMock(page)
  await page.getByRole('link', { name: 'survival', exact: true }).first().click()
  // The server's settings, not the panel settings in the rail.
  await page.getByRole('list', { name: 'Bereiche von survival' }).getByRole('link', { name: 'Einstellungen', exact: true }).click()
  await page.getByRole('button', { name: 'Eigenschaften' }).click()
  const preset = page.getByRole('group', { name: 'Weniger Last' })
  await preset.getByRole('button', { name: 'Weniger Last übernehmen …' }).click()
  await preset.getByRole('button', { name: 'Übernehmen' }).waitFor()
  await preset.scrollIntoViewIfNeeded()
  await shoot(page, 'distance-preset-confirm')
  await preset.getByRole('button', { name: 'Übernehmen' }).click()
  await preset.getByText('Aktiv: Sichtweite 8, Simulationsweite 6.').waitFor()
  await shoot(page, 'distance-preset-active')
})
