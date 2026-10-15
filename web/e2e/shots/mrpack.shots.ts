import { fileURLToPath } from 'node:url'
import { test } from '@playwright/test'
import { shoot, signInMock } from './shoot'

test('mods: modpack import dialog', async ({ page }) => {
  await signInMock(page)
  await page.getByRole('link', { name: 'modded', exact: true }).first().click()
  await page.getByRole('link', { name: 'Mods', exact: true }).first().click()
  const chooser = page.waitForEvent('filechooser')
  await page.getByRole('button', { name: 'Modpack importieren (.mrpack)' }).click()
  await (await chooser).setFiles(fileURLToPath(new URL('../fixtures/test.mrpack', import.meta.url)))
  await page.getByRole('dialog', { name: 'Modpack importieren' }).getByText('Lithium', { exact: true }).waitFor()
  await shoot(page, 'mods-mrpack-dialog')
})
