import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { db } from '@/mocks/db'
import { renderServerTab } from '@/test/render-section'
import { memoryLimitsKey, serverKey } from './api'
import { ServerSettingsTab } from './server-settings-tab'

describe('ServerSettingsTab', () => {
  it('shows the form and the danger zone at the end', async () => {
    await renderServerTab((s) => <ServerSettingsTab server={s} instance={s.instance} />, { path: '/servers/1/settings' })
    expect(await screen.findByRole('button', { name: 'Änderungen speichern' })).toBeInTheDocument()
    const danger = screen.getByRole('region', { name: 'Server löschen' })
    expect(danger).toHaveAttribute('data-tone', 'danger')
    expect(within(danger).getByRole('button', { name: /löschen/i })).toBeDisabled()
    // the danger zone is the last section of the area, after the form (the toaster's region lives outside the area)
    const area = danger.parentElement as HTMLElement
    expect(area.lastElementChild).toBe(danger)
    expect(area).toContainElement(screen.getByRole('button', { name: 'Änderungen speichern' }))
  })

  it('shows only the hint and the danger zone for a server without instance', async () => {
    await renderServerTab((s) => <ServerSettingsTab server={s} instance={null} />, { path: '/servers/1/settings' })
    expect(await screen.findByRole('region', { name: 'Server löschen' })).toBeInTheDocument()
    expect(screen.getByText(/hat noch keine Minecraft-Instanz/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Änderungen speichern' })).not.toBeInTheDocument()
  })

  it('saves only onto the revision it was loaded with and offers a reload on a conflict (#150)', async () => {
    const { user, queryClient } = await renderServerTab((s) => <ServerSettingsTab server={s} instance={s.instance} />, {
      path: '/servers/1/settings',
      sudo: true,
    })
    const memory = await screen.findByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })
    await user.clear(memory)
    await user.type(memory, '5')
    // The revision moves on meanwhile (e.g. the base domain changed); a bump alone keeps the unsaved edit in the form.
    const stored = db.servers.find((s) => s.id === 1)
    if (!stored?.instance) throw new Error('no seeded server 1')
    stored.revision += 1
    await queryClient.invalidateQueries({ queryKey: serverKey(1) })
    await waitFor(() => expect(queryClient.getQueryData<{ instance: { revision: number } }>(serverKey(1))?.instance.revision).toBe(stored.revision))
    expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(5)

    await user.click(screen.getByRole('button', { name: 'Änderungen speichern' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Der Server wurde inzwischen geändert – bitte neu laden.')
    expect(stored.instance.memory_mib).toBe(4096)

    await user.click(within(alert).getByRole('button', { name: 'Neu laden' }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    const fresh = screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })
    expect(fresh).toHaveValue(4)
    await user.clear(fresh)
    await user.type(fresh, '6')
    // A save refreshes the memory budget too (allocated sizes may have changed).
    const budgetUpdates = queryClient.getQueryState(memoryLimitsKey)?.dataUpdateCount ?? 0
    await user.click(screen.getByRole('button', { name: 'Änderungen speichern' }))
    await waitFor(() => expect(stored.instance?.memory_mib).toBe(6144))
    await waitFor(() => expect(queryClient.getQueryState(memoryLimitsKey)?.dataUpdateCount).toBeGreaterThan(budgetUpdates))
  })
})
