import { expect, test } from '@playwright/test'
import { openServer, openServerSection, serverSectionLink, signIn } from './helpers'

test('admin installs ViaBackwards, disables a jar, installs an update and removes a jar', async ({ page }) => {
  await signIn(page, 'admin', true)
  await openServer(page, 'survival')
  await expect(page).toHaveURL(/\/servers\/1$/)
  await openServerSection(page, 'survival', 'Plugins')
  const installed = page.getByRole('region', { name: 'Installierte Plugins' })
  await expect(installed.getByText('LuckPerms', { exact: true })).toBeVisible()

  // Discover on Hangar and install with the dependency preview.
  await page.getByRole('tab', { name: 'Entdecken' }).click()
  const discover = page.getByRole('region', { name: 'Entdecken' })
  await discover.getByRole('tablist', { name: 'Quelle' }).getByRole('tab', { name: 'Hangar' }).click()
  await expect(discover.getByRole('link', { name: 'Hangar öffnen (neues Fenster)' })).toBeVisible()
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

test('installing ChunkyBorder brings its required dependency Chunky along', async ({ page }) => {
  await signIn(page, 'admin', true)
  await openServer(page, 'survival')
  await expect(page).toHaveURL(/\/servers\/1$/)
  await openServerSection(page, 'survival', 'Plugins')
  const installed = page.getByRole('region', { name: 'Installierte Plugins' })
  await expect(installed.getByText('LuckPerms', { exact: true })).toBeVisible()
  await expect(installed.getByText('Chunky', { exact: true })).toHaveCount(0)

  await page.getByRole('tab', { name: 'Entdecken' }).click()
  const discover = page.getByRole('region', { name: 'Entdecken' })
  await discover.getByLabel('Suchen').fill('ChunkyBorder')
  await discover.getByRole('button', { name: 'ChunkyBorder installieren', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'ChunkyBorder installieren' })
  const dependency = dialog.getByRole('region', { name: 'Wird mitinstalliert', exact: true })
  await expect(dependency.getByText('Chunky', { exact: true })).toBeVisible()
  await expect(dependency.getByText('benötigt von ChunkyBorder')).toBeVisible()
  await expect(dialog.getByRole('region', { name: 'Wird installiert', exact: true }).getByText('ChunkyBorder', { exact: true })).toBeVisible()
  await dialog.getByRole('button', { name: 'Installieren', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText('Installation abgeschlossen')).toBeVisible({ timeout: 15_000 })

  await page.getByRole('tab', { name: 'Installiert' }).click()
  await expect(installed.getByText('ChunkyBorder', { exact: true })).toBeVisible()
  await expect(installed.getByText('Chunky', { exact: true })).toBeVisible()
})

test('the mods tab of a fabric server offers only Modrinth', async ({ page }) => {
  await signIn(page, 'admin', true)
  await openServer(page, 'modded')
  await expect(page).toHaveURL(/\/servers\/4$/)
  await expect(serverSectionLink(page, 'modded', 'Plugins')).toHaveCount(0)
  await openServerSection(page, 'modded', 'Mods')
  await expect(page.getByRole('region', { name: 'Installierte Mods' }).getByText('fabric-api-0.110.0.jar')).toBeVisible()
  await page.getByRole('tab', { name: 'Entdecken' }).click()
  const discover = page.getByRole('region', { name: 'Entdecken' })
  await expect(discover.getByRole('heading', { name: 'Beliebt auf Modrinth' })).toBeVisible()
  await expect(discover.getByRole('tablist', { name: 'Quelle' })).toHaveCount(0)
  await expect(discover.getByRole('tab', { name: 'Hangar' })).toHaveCount(0)
})

test('vanilla servers have neither a plugins nor a mods tab', async ({ page }) => {
  await signIn(page, 'admin', true)
  await openServer(page, 'creative')
  await expect(page).toHaveURL(/\/servers\/2$/)
  await expect(serverSectionLink(page, 'creative', 'Übersicht')).toBeVisible()
  await expect(serverSectionLink(page, 'creative', 'Plugins')).toHaveCount(0)
  await expect(serverSectionLink(page, 'creative', 'Mods')).toHaveCount(0)
})

test('a user without the plugins permission sees no plugins tab', async ({ page }) => {
  await signIn(page, 'alice', false)
  await openServer(page, 'survival')
  await expect(page).toHaveURL(/\/servers\/1$/)
  await expect(serverSectionLink(page, 'survival', 'Übersicht')).toBeVisible()
  await expect(serverSectionLink(page, 'survival', 'Plugins')).toHaveCount(0)
})
