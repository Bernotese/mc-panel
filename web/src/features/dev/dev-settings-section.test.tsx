import { screen, waitFor, within } from '@testing-library/react'
import { http } from 'msw'
import { describe, expect, it } from 'vitest'
import { ServerSettingsTab } from '@/features/servers/server-settings-tab'
import { db } from '@/mocks/db'
import { server as mockApi } from '@/mocks/node'
import { renderServerTab } from '@/test/render-section'

function capture(method: 'patch', path: string): unknown[] {
  const bodies: unknown[] = []
  mockApi.use(
    http[method](`*/api${path}`, async ({ request }) => {
      bodies.push(await request.clone().json())
    }),
  )
  return bodies
}

function settingsTab() {
  return renderServerTab((s) => <ServerSettingsTab server={s} instance={s.instance} />, { serverId: 5, sudo: true, path: '/servers/5/settings' })
}

describe('dev settings', () => {
  it('replaces the server form with the dev settings, the memory and the dev danger zone', async () => {
    await settingsTab()
    const sync = await screen.findByRole('region', { name: 'Sync und Ruhezustand' })
    expect(within(sync).getByLabelText('Tägliche Sync-Zeit')).toHaveValue('05:00')
    expect(await within(sync).findByText('In der Zeitzone des Panels: Europe/Berlin.')).toBeInTheDocument()
    expect(within(sync).getByLabelText('Schlafen nach (Minuten ohne Spieler)')).toHaveValue(15)
    expect(screen.getByRole('region', { name: 'Ressourcen' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Änderungen speichern' })).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Weboberflächen' })).not.toBeInTheDocument()
    const danger = screen.getByRole('region', { name: 'Dev-Instanz löschen' })
    expect(danger).toHaveTextContent('Löscht dev-survival mit seinen Daten. survival bleibt unverändert.')
    expect(within(danger).queryByRole('checkbox')).not.toBeInTheDocument()
  })

  it('sends only the changed settings', async () => {
    const bodies = capture('patch', '/servers/5/dev')
    const { user } = await settingsTab()
    const sync = await screen.findByRole('region', { name: 'Sync und Ruhezustand' })
    const save = within(sync).getByRole('button', { name: 'Einstellungen speichern' })
    expect(save).toBeDisabled()
    const idle = within(sync).getByLabelText('Schlafen nach (Minuten ohne Spieler)')
    await user.clear(idle)
    await user.type(idle, '30')
    const view = within(sync).getByLabelText('Sichtweite (Chunks)')
    await user.clear(view)
    await user.type(view, '6')
    await user.click(save)
    expect(await screen.findByText('Dev-Einstellungen gespeichert.')).toBeInTheDocument()
    expect(bodies).toEqual([{ idle_minutes: 30, view_distance: 6 }])
    expect(db.servers.find((s) => s.id === 5)?.instance?.properties.view_distance).toBe(6)
  })

  it('blocks values out of range', async () => {
    const bodies = capture('patch', '/servers/5/dev')
    const { user } = await settingsTab()
    const sync = await screen.findByRole('region', { name: 'Sync und Ruhezustand' })
    const idle = within(sync).getByLabelText('Schlafen nach (Minuten ohne Spieler)')
    await user.clear(idle)
    await user.type(idle, '4')
    await user.click(within(sync).getByRole('button', { name: 'Einstellungen speichern' }))
    expect(await within(sync).findByText('Gib eine ganze Zahl von 5 bis 120 an.')).toBeInTheDocument()
    expect(bodies).toEqual([])
  })

  it('changes only the memory through PATCH /servers/{id}', async () => {
    const bodies = capture('patch', '/servers/5')
    const { user } = await settingsTab()
    const resources = await screen.findByRole('region', { name: 'Ressourcen' })
    const memory = within(resources).getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })
    await user.clear(memory)
    await user.type(memory, '3')
    await user.click(within(resources).getByRole('button', { name: 'Arbeitsspeicher speichern' }))
    await waitFor(() => expect(bodies).toEqual([{ memory_mib: 3072 }]))
  })
})
