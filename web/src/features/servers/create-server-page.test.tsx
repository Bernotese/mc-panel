import { screen, waitFor, within } from '@testing-library/react'
import userEvent, { type UserEvent } from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { db, loginAs, MOCK_TOTP_CODE } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { CreateServerPage } from './create-server-page'

function createdInstance(name: string) {
  return db.servers.find((s) => s.name === name)?.instance
}

async function fillRequired(user: UserEvent, name: string): Promise<void> {
  await user.type(await screen.findByLabelText('Name'), name)
  await user.click(screen.getByRole('checkbox', { name: 'Ich akzeptiere die Minecraft-EULA.' }))
}

describe('CreateServerPage', () => {
  it('creates a paper server with the newest version after sudo', async () => {
    const user = userEvent.setup()
    const onCreated = vi.fn()
    await renderWithProviders(<CreateServerPage onCreated={onCreated} />, { me: loginAs('admin') })
    await fillRequired(user, 'skyblock')
    expect(screen.getByText('Adresse: skyblock.mc.example.com')).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'Paper' })).toBeChecked()
    await screen.findByRole('option', { name: '26.2' })
    expect(screen.getByRole('combobox', { name: 'Minecraft-Version' })).toHaveValue('26.2')
    expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(4)
    expect(screen.getByRole('link', { name: 'Minecraft-EULA lesen (öffnet einen neuen Tab)' })).toHaveAttribute(
      'href',
      'https://aka.ms/MinecraftEULA',
    )

    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    const sudo = await screen.findByRole('dialog', { name: 'Aktion bestätigen' })
    await user.type(within(sudo).getByLabelText('Code aus der Authenticator-App'), MOCK_TOTP_CODE)
    await user.click(within(sudo).getByRole('button', { name: 'Bestätigen' }))

    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(expect.objectContaining({ name: 'skyblock' })))
    expect(await screen.findByText('Server „skyblock“ angelegt.')).toBeInTheDocument()
    expect(createdInstance('skyblock')).toMatchObject({
      type: 'paper',
      version: '26.2',
      loader_version: null,
      modpack: null,
      java: 'java25',
      memory_mib: 4096,
    })
  })

  it('shows inline errors and sends nothing while the form is invalid', async () => {
    const user = userEvent.setup()
    const onCreated = vi.fn()
    await renderWithProviders(<CreateServerPage onCreated={onCreated} />, { me: loginAs('admin', { sudo: true }) })
    const name = await screen.findByLabelText('Name')
    await user.type(name, 'Bad Name')
    expect(screen.getByText('Nur Kleinbuchstaben, Ziffern und Bindestrich, 2–32 Zeichen, am Anfang und Ende kein Bindestrich.')).toBeInTheDocument()
    expect(name).toHaveAttribute('aria-invalid', 'true')
    const memory = screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })
    await user.clear(memory)
    await user.type(memory, '40')
    expect(screen.getByText('Gib 1 bis 20 GiB in Schritten von 0,25 GiB an.')).toBeInTheDocument()
    expect(screen.queryByText('Ohne Zustimmung zur EULA kann kein Server angelegt werden.')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    expect(screen.getByText('Bitte korrigiere die markierten Felder.')).toBeInTheDocument()
    expect(screen.getByText('Ohne Zustimmung zur EULA kann kein Server angelegt werden.')).toBeInTheDocument()
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(db.servers).toHaveLength(5)
    expect(onCreated).not.toHaveBeenCalled()
  })

  it('suggests memory per type within the host budget and warns about overcommitting', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin') })
    // mock host: 26 GiB RAM, 25 GiB container budget, limit 20 GiB; the seeded servers have 12 GiB of heap = 15 GiB of
    // container limits, so half of the free 10 GiB is 5 GiB of container = 4 GiB of heap
    expect(await screen.findByText(/Der Host hat 26 GiB RAM: 1 GiB bleibt frei, und der Container darf 25 % mehr nutzen/)).toBeInTheDocument()
    expect(screen.getByText('Empfohlen: 4 GiB')).toBeInTheDocument()
    const memory = screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })
    expect(memory).toHaveAttribute('max', '20')
    // 6 GiB for mod loaders, capped by the free budget
    await user.click(screen.getByRole('radio', { name: 'Fabric' }))
    expect(screen.getByText('Empfohlen: 4 GiB')).toBeInTheDocument()
    expect(memory).toHaveValue(4)
    expect(screen.queryByRole('status')).not.toBeInTheDocument()

    // 8 GiB of heap = 10 GiB of container: exactly the free budget, no warning yet
    await user.clear(memory)
    await user.type(memory, '8')
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    await user.clear(memory)
    await user.type(memory, '20')
    expect(screen.getByRole('status')).toHaveTextContent(
      'Liefen alle Server gleichzeitig, könnten ihre Container bis zu 40 GiB nutzen, verfügbar sind aber nur 25 GiB (26 GiB RAM minus 1 GiB für das System).',
    )
    // a changed value stays when the type changes
    await user.click(screen.getByRole('radio', { name: 'Paper' }))
    expect(memory).toHaveValue(20)
  })

  it('says so when no safe size is left instead of recommending the minimum', async () => {
    // 20 + 2 + 4 + 2 GiB of heap = 35 GiB of container limits on a 25 GiB budget
    const first = db.servers.find((s) => s.instance?.memory_mib === 4096)
    if (!first?.instance) throw new Error('no seeded 4 GiB server')
    first.instance.memory_mib = 20480
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin') })
    expect(await screen.findByText(/Keine sichere Größe mehr frei: Schon das Minimum von 1 GiB/)).toBeInTheDocument()
    expect(screen.queryByText(/Empfohlen/)).not.toBeInTheDocument()
    expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(1)
    expect(screen.getByRole('status')).toHaveTextContent('bis zu 36,25 GiB')
  })

  it('explains a missing base domain and shows the 409 from the panel', async () => {
    db.serverSettings.base_domain = null
    const user = userEvent.setup()
    const onCreated = vi.fn()
    await renderWithProviders(<CreateServerPage onCreated={onCreated} />, { me: loginAs('admin', { sudo: true }) })
    expect(await screen.findByText('Keine Basisdomain festgelegt')).toBeInTheDocument()
    expect(screen.getAllByRole('link', { name: 'Zu den Einstellungen' })[0]).toHaveAttribute('href', '/settings')
    await fillRequired(user, 'skyblock')
    expect(screen.getByText('Adresse: noch keine – erst wenn eine Basisdomain festgelegt ist.')).toBeInTheDocument()
    await screen.findByRole('option', { name: '26.2' })
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    expect(await screen.findByText('Es ist noch keine Basisdomain festgelegt. Trag sie unter Einstellungen ein.')).toBeInTheDocument()
    expect(onCreated).not.toHaveBeenCalled()
    expect(createdInstance('skyblock')).toBeUndefined()
  })

  it('falls back to free text when the version list is unavailable', async () => {
    delete db.catalogs.vanilla
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin', { sudo: true }) })
    await fillRequired(user, 'legacy')
    await user.click(screen.getByRole('radio', { name: 'Vanilla' }))
    const version = await screen.findByRole('textbox', { name: 'Minecraft-Version' })
    expect(
      screen.getByText('Die Versionsliste ist gerade nicht abrufbar. Trag die Version von Hand ein, z. B. 1.21.11 oder LATEST.'),
    ).toBeInTheDocument()
    await user.type(version, '1.20.4')
    await user.click(screen.getByRole('button', { name: 'Erweitert' }))
    expect(screen.getByRole('combobox', { name: 'Java-Version' })).toHaveDisplayValue('Automatisch (Java 17)')
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    await waitFor(() => expect(createdInstance('legacy')).toMatchObject({ type: 'vanilla', version: '1.20.4', java: 'java17' }))
  })

  it('offers CurseForge modpacks only with a stored API key', async () => {
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin') })
    expect(await screen.findByRole('radio', { name: 'CurseForge-Modpack' })).toBeDisabled()
    expect(screen.getByText(/^Für CurseForge-Modpacks braucht mc-panel einen API-Key\./)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Zu den Einstellungen' })).toHaveAttribute('href', '/settings')
  })

  it('asks for the CurseForge modpack once a key is stored', async () => {
    db.serverSettings.curseforge_api_key_set = true
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin') })
    await user.click(await screen.findByRole('radio', { name: 'CurseForge-Modpack' }))
    expect(screen.getByRole('textbox', { name: 'CurseForge-Modpack' })).toBeInTheDocument()
    expect(screen.queryByRole('combobox', { name: 'Minecraft-Version' })).not.toBeInTheDocument()
    expect(screen.queryByText(/^Für CurseForge-Modpacks braucht mc-panel einen API-Key\./)).not.toBeInTheDocument()
  })

  it('offers the fabric loader versions from the catalog', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin', { sudo: true }) })
    await fillRequired(user, 'fabric-test')
    await user.click(screen.getByRole('radio', { name: 'Fabric' }))
    await screen.findByRole('option', { name: '0.19.5' })
    const loader = screen.getByRole('combobox', { name: 'Fabric-Loader' })
    expect(loader).toHaveDisplayValue('Neueste (empfohlen)')
    await user.selectOptions(loader, '0.19.4')
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    await waitFor(() =>
      expect(createdInstance('fabric-test')).toMatchObject({ type: 'fabric', version: '26.3', loader_version: '0.19.4', java: 'java25' }),
    )
  })

  it('asks for a Modrinth project instead of a version', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin', { sudo: true }) })
    await fillRequired(user, 'cobble')
    await user.click(screen.getByRole('radio', { name: 'Modrinth-Modpack' }))
    expect(screen.queryByRole('combobox', { name: 'Minecraft-Version' })).not.toBeInTheDocument()
    const pack = screen.getByRole('textbox', { name: 'Modrinth-Projekt' })
    await user.type(pack, 'Bad Slug')
    expect(screen.getByText('Nur Kleinbuchstaben, Ziffern und Bindestrich, wie in der Modrinth-Adresse.')).toBeInTheDocument()
    await user.clear(pack)
    await user.type(pack, 'cobblemon-fabric')
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    await waitFor(() =>
      expect(createdInstance('cobble')).toMatchObject({ type: 'modrinth', version: '', modpack: 'cobblemon-fabric', java: 'java21' }),
    )
  })

  it('sends edited properties and warns when online mode is off', async () => {
    const user = userEvent.setup()
    await renderWithProviders(<CreateServerPage onCreated={vi.fn()} />, { me: loginAs('admin', { sudo: true }) })
    await fillRequired(user, 'props')
    await screen.findByRole('option', { name: '26.2' })
    await user.click(screen.getByRole('button', { name: 'Eigenschaften' }))
    const motd = screen.getByLabelText('Nachricht im Serverbrowser (MOTD)')
    expect(motd).toHaveAttribute('placeholder', 'props')
    await user.type(motd, 'Hallo')
    await user.click(screen.getByRole('switch', { name: 'PvP' }))
    await user.click(screen.getByRole('switch', { name: 'Online-Modus (Konten bei Mojang prüfen)' }))
    expect(
      screen.getByText('Ohne Online-Modus kann sich jeder mit beliebigem Namen anmelden – auch mit dem Namen eines Operators.'),
    ).toBeInTheDocument()
    expect(screen.getByText('Whitelist, Operatoren und Bans verwaltest du im Tab „Spieler“ der Serverseite; Änderungen dort wirken sofort.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Whitelist')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Server anlegen' }))
    await waitFor(() => expect(createdInstance('props')?.properties).toMatchObject({ motd: 'Hallo', pvp: false, online_mode: false }))
  })
})
