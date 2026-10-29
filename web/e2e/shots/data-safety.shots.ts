import { test } from '../test'
import { shoot, signInMock } from './shoot'

// Data safety spec §2.3: the seeded archive server has a stuck restore (recovery_required).
test('data safety: recovery banner, resolve, dashboard', async ({ page }) => {
  await signInMock(page)
  await page.getByRole('article', { name: 'archive', exact: true }).waitFor()
  await shoot(page, 'data-safety-dashboard')

  await page.getByRole('link', { name: 'archive', exact: true }).first().click()
  const recovery = page.getByRole('region', { name: 'Wiederherstellung nötig' })
  await recovery.waitFor()
  await shoot(page, 'data-safety-recovery')

  await recovery.getByRole('checkbox', { name: 'Ich habe das Datenverzeichnis geprüft' }).check()
  await recovery.getByRole('button', { name: 'Wiederherstellung auflösen' }).click()
  // Resolving needs sudo: the global dialog asks for the second factor and retries.
  const done = page.getByRole('status', { name: 'Wiederherstellung aufgelöst' })
  const sudo = page.getByRole('dialog', { name: 'Aktion bestätigen' })
  await done.or(sudo).first().waitFor()
  if (await sudo.isVisible()) {
    await sudo.getByLabel('Code aus der Authenticator-App').fill('123456')
    await sudo.getByRole('button', { name: 'Bestätigen' }).click()
  }
  await done.waitFor()
  await shoot(page, 'data-safety-resolved')
})
