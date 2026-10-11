import { expect, test, type Page } from '@playwright/test'

const PASSWORD = 'correct-horse-battery'
const TOTP_CODE = '123456'

/** Signs in via the UI; the mock state lives in the page, so the test must not reload afterwards. */
async function signIn(page: Page, username: string, withTotp: boolean) {
  await page.goto('/')
  await expect(page).toHaveURL(/\/login$/)
  await page.getByLabel('Benutzername').fill(username)
  await page.getByLabel('Passwort').fill(PASSWORD)
  await page.getByRole('button', { name: 'Anmelden' }).click()
  if (withTotp) {
    await expect(page).toHaveURL(/\/login\/2fa$/)
    await page.getByLabel('Code aus der Authenticator-App').fill(TOTP_CODE)
    await page.getByRole('button', { name: 'Bestätigen' }).click()
  }
  await expect(page.getByRole('heading', { name: 'Server', level: 1 })).toBeVisible()
}

async function confirmSudo(page: Page) {
  const sudo = page.getByRole('dialog', { name: 'Aktion bestätigen' })
  await sudo.getByLabel('Code aus der Authenticator-App').fill(TOTP_CODE)
  await sudo.getByRole('button', { name: 'Bestätigen' }).click()
  await expect(sudo).toBeHidden()
}

test('admin creates a paper server, changes the MOTD and restarts it', async ({ page }) => {
  await signIn(page, 'admin', true)

  await page.getByRole('link', { name: 'Server anlegen' }).click()
  await expect(page).toHaveURL(/\/servers\/new$/)
  await page.getByLabel('Name', { exact: true }).fill('skyblock')
  await expect(page.getByText('Adresse: skyblock.mc.example.com')).toBeVisible()
  await expect(page.getByRole('radio', { name: 'Paper' })).toBeChecked()
  await expect(page.getByRole('combobox', { name: 'Minecraft-Version' })).toHaveValue('26.2')
  await page.getByRole('checkbox', { name: 'Ich akzeptiere die Minecraft-EULA.' }).check()
  await page.getByRole('button', { name: 'Server anlegen' }).click()
  await confirmSudo(page)

  await expect(page).toHaveURL(/\/servers\/\d+$/)
  await expect(page.getByRole('heading', { name: 'skyblock', level: 1 })).toBeVisible()
  const state = page.getByRole('region', { name: 'Zustand' })
  await expect(state.getByText('Läuft')).toBeVisible()
  await expect(state.getByText('skyblock.mc.example.com')).toBeVisible()

  await page.getByRole('tab', { name: 'Einstellungen' }).click()
  await expect(page.getByText('Der Server läuft. Änderungen werden erst nach einem Neustart aktiv.')).toBeVisible()
  await page.getByRole('button', { name: 'Eigenschaften' }).click()
  await page.getByLabel('Nachricht im Serverbrowser (MOTD)').fill('Willkommen auf Skyblock')
  // Sudo is still valid (10 min), so saving asks for no second confirmation.
  await page.getByRole('button', { name: 'Änderungen speichern' }).click()
  await expect(page.getByText('Änderungen gespeichert. Sie werden beim nächsten Neustart aktiv.')).toBeVisible()

  await page.getByRole('tab', { name: 'Übersicht' }).click()
  await expect(page.getByText('Neustart nötig')).toBeVisible()
  await page.getByRole('button', { name: 'Jetzt neu starten' }).click()
  await expect(page.getByText('Neustart für „skyblock“ angefordert.')).toBeVisible()
  await expect(page.getByText('Neustart nötig')).toBeHidden()
  await expect(state.getByText('Läuft')).toBeVisible()
})

test('a restricted user sees only permitted servers and no admin areas', async ({ page }) => {
  await signIn(page, 'alice', false)

  await expect(page.getByRole('article', { name: 'survival' })).toBeVisible()
  await expect(page.getByRole('article', { name: 'creative' })).toBeVisible()
  await expect(page.getByRole('article', { name: 'lobby' })).toHaveCount(0)
  await expect(page.getByRole('link', { name: 'Server anlegen' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Server anlegen' })).toHaveCount(0)
  const nav = page.getByRole('navigation', { name: 'Hauptnavigation' })
  await expect(nav.getByRole('link', { name: 'Einstellungen' })).toHaveCount(0)

  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await expect(page.getByRole('heading', { name: 'survival', level: 1 })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Übersicht' })).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Einstellungen' })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: 'Löschen' })).toHaveCount(0)
  await expect(page.getByRole('group', { name: 'Steuerung für survival' })).toBeVisible()
})

test('admin follows the console, sends a command and kicks a player', async ({ page }) => {
  await signIn(page, 'admin', true)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)

  await page.getByRole('tab', { name: 'Konsole' }).click()
  const log = page.getByRole('log', { name: 'Server-Log' })
  await expect(page.getByText('Verbunden')).toBeVisible()
  await expect(log).toContainText('INFO')
  await expect(log).not.toContainText('Thread RCON Client')

  const command = page.getByRole('textbox', { name: 'Befehl' })
  await command.fill('list')
  await command.press('Enter')
  await expect(log).toContainText('› list')
  await expect(log).toContainText('of a max of 20 players online')
  await expect(command).toHaveValue('')

  await page.getByRole('tab', { name: 'Spieler' }).click()
  const online = page.getByRole('region', { name: 'Online' })
  await expect(online.getByText('Steve', { exact: true })).toBeVisible()
  await online.getByRole('button', { name: 'Steve kicken' }).click()
  await expect(page.locator('[data-sonner-toast]').first()).toBeVisible()
  await expect(online.getByText('Steve', { exact: true })).toHaveCount(0)
})

test('a user without console permission sees online players but no console', async ({ page }) => {
  await signIn(page, 'alice', false)
  await page.getByRole('link', { name: 'survival', exact: true }).click()
  await expect(page).toHaveURL(/\/servers\/1$/)
  await expect(page.getByRole('region', { name: 'Online-Spieler' }).getByText('Alex')).toBeVisible()
  await expect(page.getByRole('tab', { name: 'Konsole' })).toHaveCount(0)
  await expect(page.getByRole('tab', { name: 'Spieler' })).toHaveCount(0)
})
