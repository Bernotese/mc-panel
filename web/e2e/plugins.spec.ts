import { expect, test } from '@playwright/test'
import { signIn } from './helpers'

test('admin installs ViaBackwards, disables a jar, installs an update and removes a jar', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await page.getByRole('tab', { name: 'Plugins', exact: true }).click()
  const installed = page.getByRole('region', { name: 'Installierte Plugins' })
  await expect(installed.getByText('LuckPerms', { exact: true })).toBeVisible()

  // Discover on Hangar and install with the dependency preview.
  await page.getByRole('tab', { name: 'Entdecken' }).click()
  const discover = page.getByRole('region', { name: 'Entdecken' })
  await discover.getByRole('group', { name: 'Quelle' }).getByRole('button', { name: 'Hangar' }).click()
  await discover.getByLabel('Suchen').fill('ViaBack')
  await discover.getByRole('button', { name: 'ViaBackwards installieren' }).click()
  const dialog = page.getByRole('dialog', { name: 'ViaBackwards installieren' })
  await expect(dialog.getByRole('region', { name: 'Bereits vorhanden' }).getByText('ViaVersion', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('region', { name: 'Wird installiert', exact: true }).getByText('ViaBackwards', { exact: true })).toBeVisible()
  await dialog.getByRole('button', { name: 'Installieren', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText('Installation läuft …')).toBeVisible()
  await expect(page.getByText('Installation abgeschlossen')).toBeVisible({ timeout: 15_000 })
  await page.getByRole('tab', { name: 'Installiert' }).click()
  await expect(installed.getByText('ViaBackwards', { exact: true })).toBeVisible()

  // Disable a jar.
  const oldTool = installed.getByRole('switch', { name: 'OldTool aktiv' })
  await expect(oldTool).toBeChecked()
  await oldTool.click()
  await expect(page.getByText('„OldTool“ deaktiviert.')).toBeVisible()
  await expect(installed.getByRole('switch', { name: 'OldTool aktiv' })).not.toBeChecked()

  // Install the available update of LuckPerms.
  await expect(installed.getByText('Update: 5.5.72')).toBeVisible()
  await installed.getByRole('button', { name: 'Aktionen für LuckPerms' }).click()
  await page.getByRole('menuitem', { name: 'Auf 5.5.72 aktualisieren' }).click()
  await expect(page.getByText('Aktualisierung abgeschlossen')).toBeVisible({ timeout: 15_000 })
  await expect(installed.getByText('Update: 5.5.72')).toHaveCount(0)
  await expect(installed.getByText('5.5.72', { exact: true })).toBeVisible()

  // Remove the jar installed above.
  await installed.getByRole('button', { name: 'Aktionen für ViaBackwards' }).click()
  await page.getByRole('menuitem', { name: 'Entfernen' }).click()
  const confirm = page.getByRole('dialog', { name: '„ViaBackwards“ entfernen?' })
  await confirm.getByRole('button', { name: 'Entfernen' }).click()
  await expect(page.getByText('„ViaBackwards“ entfernt.')).toBeVisible()
  await expect(installed.getByText('ViaBackwards', { exact: true })).toHaveCount(0)
})

test('the mods tab of a fabric server offers only Modrinth', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'modded', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/4$/)
  await expect(page.getByRole('tab', { name: 'Plugins', exact: true })).toHaveCount(0)
  await page.getByRole('tab', { name: 'Mods', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Installierte Mods' }).getByText('fabric-api-0.110.0.jar')).toBeVisible()
  await page.getByRole('tab', { name: 'Entdecken' }).click()
  const discover = page.getByRole('region', { name: 'Entdecken' })
  await expect(discover.getByRole('heading', { name: 'Beliebt auf Modrinth' })).toBeVisible()
  await expect(discover.getByRole('group', { name: 'Quelle' })).toHaveCount(0)
  await expect(discover.getByRole('button', { name: 'Hangar' })).toHaveCount(0)
})

test('vanilla servers and users without the permission see no plugins tab', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'creative', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/2$/)
  await expect(page.getByRole('tab', { name: 'Übersicht' })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Plugins', exact: true })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: 'Mods', exact: true })).toHaveCount(0)
})

test('a user without the plugins permission sees no plugins tab', async ({ page }) => {
  await signIn(page, 'alice', false)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await expect(page.getByRole('tab', { name: 'Übersicht' })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Plugins', exact: true })).toHaveCount(0)
})
