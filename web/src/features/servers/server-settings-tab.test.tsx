import type { QueryClient } from '@tanstack/react-query'
import { act, screen, waitFor, within } from '@testing-library/react'
import type userEvent from '@testing-library/user-event'
import { http } from 'msw'
import { describe, expect, it } from 'vitest'
import { db } from '@/mocks/db'
import { server as mockApi } from '@/mocks/node'
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

  describe('drafts (issue #154)', () => {
    function stored() {
      const server = db.servers.find((s) => s.id === 1)
      if (!server?.instance) throw new Error('no seeded server 1')
      return server as typeof server & { instance: NonNullable<typeof server.instance> }
    }
    function settings() {
      return renderServerTab((s) => <ServerSettingsTab server={s} instance={s.instance} />, { path: '/servers/1/settings', sudo: true })
    }
    async function setMemory(user: ReturnType<typeof userEvent.setup>, gib: string) {
      const memory = await screen.findByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })
      await user.clear(memory)
      await user.type(memory, gib)
    }
    /** Waits until the polled server carries the mock's current revision. */
    async function poll(queryClient: QueryClient) {
      await queryClient.invalidateQueries({ queryKey: serverKey(1) })
      await waitFor(() => expect(queryClient.getQueryData<{ instance: { revision: number } }>(serverKey(1))?.instance.revision).toBe(stored().revision))
    }

    it('lets a clean form follow changes made elsewhere', async () => {
      const { queryClient } = await settings()
      expect(await screen.findByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(4)
      stored().instance.memory_mib = 6144
      stored().revision += 1
      await poll(queryClient)
      await waitFor(() => expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(6))
      expect(screen.queryByRole('region', { name: 'Inzwischen geändert' })).not.toBeInTheDocument()
    })

    it('keeps a dirty draft when the server changes elsewhere, compares, and keeps the own changes on top', async () => {
      const { user, queryClient } = await settings()
      await setMemory(user, '5')
      stored().instance.properties = { ...stored().instance.properties, max_players: 50 }
      stored().revision += 1
      await poll(queryClient)

      const box = await screen.findByRole('region', { name: 'Inzwischen geändert' })
      expect(box).toHaveTextContent('Diese Einstellungen wurden woanders geändert')
      expect(within(box).getByRole('list', { name: 'Geänderte Einstellungen' })).toHaveTextContent('Maximale Spielerzahl – gespeichert: 50')
      expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(5)

      await user.click(within(box).getByRole('button', { name: 'Meine Änderungen behalten' }))
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Inzwischen geändert' })).not.toBeInTheDocument())
      expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(5)
      await user.click(screen.getByRole('button', { name: 'Änderungen speichern' }))
      // Saved onto the new revision: the own memory and the other admin's player limit.
      await waitFor(() => expect(stored().instance.memory_mib).toBe(5120))
      expect(stored().instance.properties.max_players).toBe(50)
      expect(screen.getByRole('button', { name: 'Änderungen speichern' })).toBeDisabled()
    })

    it('marks a field changed on both sides and loads the stored state on request', async () => {
      const { user, queryClient } = await settings()
      await setMemory(user, '5')
      stored().instance.memory_mib = 6144
      stored().revision += 1
      await poll(queryClient)
      const box = await screen.findByRole('region', { name: 'Inzwischen geändert' })
      expect(box).toHaveTextContent('Arbeitsspeicher (GiB) – gespeichert: 6 GiB · deins: 5 GiB')
      await user.click(within(box).getByRole('button', { name: 'Gespeicherten Stand laden' }))
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Inzwischen geändert' })).not.toBeInTheDocument())
      expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(6)
      expect(screen.getByRole('button', { name: 'Änderungen speichern' })).toBeDisabled()
    })

    it('saves only onto the revision it was loaded with and resolves the refusal (#150)', async () => {
      const { user, queryClient } = await settings()
      await setMemory(user, '5')
      // A bare revision bump (e.g. the base domain changed) keeps the edit without a notice …
      stored().revision += 1
      await poll(queryClient)
      expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(5)
      expect(screen.queryByRole('region', { name: 'Inzwischen geändert' })).not.toBeInTheDocument()

      // … but the save still goes onto the old revision and is refused.
      await user.click(screen.getByRole('button', { name: 'Änderungen speichern' }))
      const box = await screen.findByRole('region', { name: 'Inzwischen geändert' })
      expect(box).toHaveTextContent('Der Server wurde inzwischen geändert, deshalb wurde nichts gespeichert.')
      expect(await within(box).findByText('Geändert wurden Einstellungen außerhalb dieses Formulars.')).toBeInTheDocument()
      expect(stored().instance.memory_mib).toBe(4096)

      await user.click(within(box).getByRole('button', { name: 'Meine Änderungen behalten' }))
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Inzwischen geändert' })).not.toBeInTheDocument())
      // A save refreshes the memory budget too (allocated sizes may have changed).
      const budgetUpdates = queryClient.getQueryState(memoryLimitsKey)?.dataUpdateCount ?? 0
      await user.click(screen.getByRole('button', { name: 'Änderungen speichern' }))
      await waitFor(() => expect(stored().instance.memory_mib).toBe(5120))
      await waitFor(() => expect(queryClient.getQueryState(memoryLimitsKey)?.dataUpdateCount).toBeGreaterThan(budgetUpdates))
    })

    it('clears a refusal when the draft goes back to the stored values', async () => {
      const { user, queryClient } = await settings()
      await setMemory(user, '5')
      stored().revision += 1
      await poll(queryClient)
      await user.click(screen.getByRole('button', { name: 'Änderungen speichern' }))
      const box = await screen.findByRole('region', { name: 'Inzwischen geändert' })
      await within(box).findByText('Geändert wurden Einstellungen außerhalb dieses Formulars.')
      await setMemory(user, '4')
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Inzwischen geändert' })).not.toBeInTheDocument())
      expect(screen.queryByText('Der Server wurde inzwischen geändert. Lade ihn neu und übernimm deine Änderungen noch einmal.')).not.toBeInTheDocument()
      // Clean again, the form follows the server: a new edit saves onto the current revision.
      await setMemory(user, '6')
      await user.click(screen.getByRole('button', { name: 'Änderungen speichern' }))
      await waitFor(() => expect(stored().instance.memory_mib).toBe(6144))
    })

    it('locks the fields while saving', async () => {
      let release: () => void = () => undefined
      mockApi.use(
        http.patch('*/api/servers/:id', async () => {
          await new Promise<void>((resolve) => {
            release = resolve
          })
          return undefined
        }),
      )
      const { user } = await settings()
      await setMemory(user, '5')
      await user.click(screen.getByRole('button', { name: 'Änderungen speichern' }))
      await waitFor(() => expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toBeDisabled())
      release()
      await waitFor(() => expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toBeEnabled())
      expect(stored().instance.memory_mib).toBe(5120)
    })

    it('does not count a value that saves the same as a change', async () => {
      const { user, router } = await settings()
      await setMemory(user, '4.0')
      expect(screen.getByRole('button', { name: 'Änderungen speichern' })).toBeDisabled()
      act(() => void router.navigate({ to: '/' }))
      await waitFor(() => expect(router.state.location.pathname).toBe('/'))
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })

    it('asks before leaving with unsaved changes and not after saving', async () => {
      const { user, router } = await settings()
      await setMemory(user, '5')
      act(() => void router.navigate({ to: '/' }))
      const dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
      await user.click(within(dialog).getByRole('button', { name: 'Abbrechen' }))
      expect(router.state.location.pathname).toBe('/servers/1/settings')
      expect(screen.getByRole('spinbutton', { name: 'Arbeitsspeicher (GiB)' })).toHaveValue(5)

      await user.click(screen.getByRole('button', { name: 'Änderungen speichern' }))
      await waitFor(() => expect(stored().instance.memory_mib).toBe(5120))
      act(() => void router.navigate({ to: '/' }))
      await waitFor(() => expect(router.state.location.pathname).toBe('/'))
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })

    it('leaves after confirming the dialog', async () => {
      const { user, router } = await settings()
      await setMemory(user, '5')
      act(() => void router.navigate({ to: '/servers/$id', params: { id: '1' } }))
      const dialog = await screen.findByRole('dialog', { name: 'Ungespeicherte Änderungen verwerfen?' })
      await user.click(within(dialog).getByRole('button', { name: 'Verwerfen und verlassen' }))
      await waitFor(() => expect(router.state.location.pathname).toBe('/servers/1'))
    })
  })
})
