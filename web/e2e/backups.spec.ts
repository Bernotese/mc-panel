import { expect, test } from '@playwright/test'
import { confirmSudo, openServer, openServerSection, signIn } from './helpers'

test('admin creates a backup with a note, pins a scheduled snapshot and restores one', async ({ page }) => {
  await signIn(page, 'admin', true)
  await openServer(page, 'survival')
  await expect(page).toHaveURL(/\/servers\/1$/)
  await openServerSection(page, 'survival', 'Backups')
  const list = page.getByRole('region', { name: 'Snapshots' })
  await expect(list.getByText('vor Update auf 1.21.11')).toBeVisible()
  await expect(list.getByText('Angepinnt')).toHaveCount(1)

  // Create a backup with a note and follow the job.
  await page.getByRole('button', { name: 'Backup erstellen' }).click()
  const create = page.getByRole('dialog', { name: 'Backup erstellen' })
  await create.getByLabel('Notiz (optional)').fill('vor dem Test')
  await create.getByRole('button', { name: 'Backup starten' }).click()
  await expect(create).toBeHidden()
  await expect(page.getByRole('region', { name: 'Backup läuft …' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Backup abgeschlossen' })).toBeVisible({ timeout: 15_000 })
  await expect(list.getByText('vor dem Test')).toBeVisible()

  // Pin the newest scheduled snapshot (its menu offers "Anpinnen"); pinning needs sudo.
  const menus = list.getByRole('button', { name: /^Aktionen für den Snapshot vom / })
  const count = await menus.count()
  for (let i = 0; i < count; i++) {
    await menus.nth(i).click()
    // isVisible does not wait: let the menu open before asking for its entry.
    await expect(page.getByRole('menu')).toBeVisible()
    const pin = page.getByRole('menuitem', { name: 'Anpinnen' })
    if (await pin.isVisible()) {
      await pin.click()
      break
    }
    await page.keyboard.press('Escape')
  }
  await confirmSudo(page)
  await expect(page.getByText('Snapshot angepinnt.')).toBeVisible()
  await expect(list.getByText('Angepinnt')).toHaveCount(2)

  // Restore the manual snapshot: name confirmation; the sudo window of the pin is still open.
  const before = await list.getByText('Vor Restore').count()
  await list.getByRole('button', { name: /^Aktionen für den Snapshot vom / }).first().click()
  await page.getByRole('menuitem', { name: 'Wiederherstellen' }).click()
  const restore = page.getByRole('dialog', { name: /wiederherstellen\?$/ })
  await expect(restore.getByRole('button', { name: 'Wiederherstellen' })).toBeDisabled()
  await restore.getByLabel('Zum Bestätigen den Namen „survival“ eintippen').fill('survival')
  await restore.getByRole('button', { name: 'Wiederherstellen' }).click()
  await expect(page.getByRole('dialog', { name: 'Aktion bestätigen' })).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'Wiederherstellung läuft …' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Wiederherstellung abgeschlossen' })).toBeVisible({ timeout: 15_000 })
  await expect(list.getByText('Vor Restore')).toHaveCount(before + 1)
})

test('admin changes the schedule of a server', async ({ page }) => {
  await signIn(page, 'admin', true)
  await openServer(page, 'survival')
  await openServerSection(page, 'survival', 'Backups')
  const card = page.getByRole('region', { name: 'Zeitplan' })
  await expect(card.getByText(/Zeitzone des Panels: Europe\/Berlin/)).toBeVisible()
  await card.getByRole('combobox', { name: 'Zeitplan' }).selectOption('interval')
  await card.getByLabel('Intervall in Stunden').fill('6')
  await card.getByRole('button', { name: 'Zeitplan speichern' }).click()
  await confirmSudo(page)
  await expect(page.getByText('Zeitplan gespeichert.')).toBeVisible()
  await expect(card.getByText(/^Nächster Lauf: /)).toBeVisible()
})

test('a host without restic shows only a hint', async ({ page }) => {
  await signIn(page, 'admin', true)
  await openServer(page, 'modded')
  await openServerSection(page, 'modded', 'Backups')
  await expect(page.getByText(/Backups sind auf diesem Host nicht verfügbar/)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Backup erstellen' })).toHaveCount(0)
})
