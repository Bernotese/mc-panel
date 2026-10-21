import { expect, test } from './test'
import { confirmSudo, openServer, openServerSection, signIn } from './helpers'

test('admin publishes the root and a path, the header links the web map, removing both unpublishes', async ({ page }) => {
  await signIn(page, 'admin', true)
  await openServer(page, 'creative')
  const header = page.getByRole('region', { name: 'creative', exact: true })
  const mapLink = header.getByRole('link', { name: 'Webkarte öffnen' })
  await expect(header.getByText('creative.mc.example.com')).toBeVisible()
  await expect(mapLink).toHaveCount(0)

  await openServerSection(page, 'creative', 'Einstellungen')
  const section = page.getByRole('region', { name: 'Weboberflächen' })
  const rootSwitch = section.getByRole('switch', { name: 'Unter der Hauptadresse veröffentlichen' })
  const save = section.getByRole('button', { name: 'Weboberflächen speichern' })
  await expect(section.getByRole('spinbutton', { name: 'Port im Container für die Hauptadresse' })).toHaveValue('8100')
  await rootSwitch.click()
  await section.getByRole('button', { name: 'Port hinzufügen' }).click()
  await section.getByRole('textbox', { name: 'Pfad 1' }).fill('afk-smoke')
  await section.getByRole('spinbutton', { name: 'Port im Container 1' }).fill('8200')
  await save.click()
  await confirmSudo(page)
  await expect(page.getByText('Weboberflächen gespeichert. Die Adressen sind in wenigen Sekunden erreichbar.')).toBeVisible()
  // pending until the next poll, then active
  await expect(section.getByText('Aktiv')).toBeVisible()
  await expect(section.getByRole('link', { name: 'https://creative.mc.example.com/', exact: true })).toBeVisible()
  await expect(section.getByRole('link', { name: 'https://creative.mc.example.com/afk-smoke/' })).toBeVisible()
  await expect(mapLink).toHaveAttribute('href', 'https://creative.mc.example.com')
  await expect(mapLink).toHaveAttribute('rel', 'noopener noreferrer')
  await expect(mapLink).toHaveAttribute('target', '_blank')

  // the path row goes, the root stays
  await section.getByRole('button', { name: 'Pfad 1 entfernen' }).click()
  await save.click()
  await expect(section.getByRole('link', { name: 'https://creative.mc.example.com/afk-smoke/' })).toHaveCount(0)
  await expect(section.getByRole('link', { name: 'https://creative.mc.example.com/', exact: true })).toBeVisible()
  await expect(mapLink).toBeVisible()

  await rootSwitch.click()
  await save.click()
  await expect(page.getByText('Es werden keine Web-Ports mehr veröffentlicht.')).toBeVisible()
  await expect(section.getByText('Aus')).toBeVisible()
  await expect(mapLink).toHaveCount(0)
})
