import { expect, test } from './test'
import { confirmSudo, openServer, openServerSection, signIn } from './helpers'

test('admin publishes a web port, the header links the web map, unpublishing removes it', async ({ page }) => {
  await signIn(page, 'admin', true)
  await openServer(page, 'creative')
  const header = page.getByRole('region', { name: 'creative', exact: true })
  const mapLink = header.getByRole('link', { name: 'Webkarte öffnen' })
  await expect(header.getByText('creative.mc.example.com')).toBeVisible()
  await expect(mapLink).toHaveCount(0)

  await openServerSection(page, 'creative', 'Einstellungen')
  const section = page.getByRole('region', { name: 'Weboberfläche' })
  await expect(section.getByLabel('Port im Container')).toHaveValue('8100')
  await section.getByRole('switch', { name: 'Web-Port veröffentlichen' }).click()
  await section.getByRole('button', { name: 'Weboberfläche speichern' }).click()
  await confirmSudo(page)
  await expect(page.getByText('Web-Port veröffentlicht. Die Adresse ist in wenigen Sekunden erreichbar.')).toBeVisible()
  // pending until the next poll, then active
  await expect(section.getByText('Aktiv')).toBeVisible()
  await expect(section.getByRole('link', { name: 'https://creative.mc.example.com' })).toBeVisible()
  await expect(mapLink).toHaveAttribute('href', 'https://creative.mc.example.com')
  await expect(mapLink).toHaveAttribute('rel', 'noopener noreferrer')
  await expect(mapLink).toHaveAttribute('target', '_blank')

  await section.getByRole('switch', { name: 'Web-Port veröffentlichen' }).click()
  await section.getByRole('button', { name: 'Weboberfläche speichern' }).click()
  await expect(page.getByText('Web-Port wird nicht mehr veröffentlicht.')).toBeVisible()
  await expect(section.getByText('Aus')).toBeVisible()
  await expect(mapLink).toHaveCount(0)
})
