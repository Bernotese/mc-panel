import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ServerSettingsTab } from './server-settings-tab'
import { renderServerTab } from '@/test/render-section'

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
})
