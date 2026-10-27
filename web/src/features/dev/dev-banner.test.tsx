import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { db } from '@/mocks/db'
import { renderServerTab } from '@/test/render-section'
import { DevBanner } from './dev-banner'

function devRow() {
  const dev = db.servers.find((s) => s.id === 5)?.dev
  if (!dev) throw new Error('dev-survival missing')
  return dev
}

async function banner(username = 'admin') {
  await renderServerTab((s) => (s.dev ? <DevBanner server={s} dev={s.dev} /> : null), { serverId: 5, username })
  return screen.findByRole('region', { name: 'Dev-Instanz' })
}

describe('DevBanner', () => {
  it('names the source, the next overwrite, the last sync and that the dev sleeps', async () => {
    const b = await banner()
    expect(within(b).getByRole('link', { name: 'survival' })).toHaveAttribute('href', '/servers/1')
    expect(b).toHaveTextContent(/Kopie von survival – Änderungen werden beim nächsten Sync überschrieben \((heute|morgen) \d\d:\d\d\)\./)
    expect(b).toHaveTextContent(/Letzter Sync: (heute|gestern) \d\d:\d\d/)
    expect(within(b).getByText('Schläft – auf dev-survival.mc.example.com verbinden zum Aufwecken.')).toBeInTheDocument()
    expect(within(b).getByRole('switch', { name: 'Sync pausieren' })).not.toBeChecked()
    expect(within(b).getByRole('button', { name: 'Jetzt syncen' })).toBeEnabled()
  })

  it('says since when the sync is paused', async () => {
    Object.assign(devRow(), { syncPaused: true, pausedAt: new Date(2026, 9, 12, 9, 0).getTime(), nextSyncAt: null })
    const b = await banner()
    expect(await within(b).findByText('– Sync pausiert seit 12.10.2026, Dev behält seine Änderungen.')).toBeInTheDocument()
    expect(within(b).getByRole('switch', { name: 'Sync pausieren' })).toBeChecked()
  })

  it('shows a failed sync with its error', async () => {
    Object.assign(devRow(), { lastSyncStatus: 'failed', lastSyncError: 'restic: repository is locked' })
    const b = await banner()
    expect(await within(b).findByRole('alert')).toHaveTextContent(/^Letzter Sync fehlgeschlagen \(.+\): restic: repository is locked$/)
  })

  it('drops the sleeping hint once the dev runs and offers no actions without server.files', async () => {
    Object.assign(devRow(), { sleeping: false })
    const b = await banner('alice')
    expect(within(b).queryByText(/Schläft/)).not.toBeInTheDocument()
    expect(within(b).queryByRole('button', { name: 'Jetzt syncen' })).not.toBeInTheDocument()
    expect(within(b).queryByRole('switch')).not.toBeInTheDocument()
  })

  it('requests a sync now and pauses the sync', async () => {
    const { user } = await renderServerTab((s) => (s.dev ? <DevBanner server={s} dev={s.dev} /> : null), { serverId: 5 })
    const b = await screen.findByRole('region', { name: 'Dev-Instanz' })
    await user.click(within(b).getByRole('button', { name: 'Jetzt syncen' }))
    expect(await screen.findByText('Sync angefordert.')).toBeInTheDocument()
    expect(devRow().syncSince).not.toBeNull()
    expect(await within(b).findByText('– Sync läuft, Dev wird gerade überschrieben.')).toBeInTheDocument()
    await user.click(within(b).getByRole('switch', { name: 'Sync pausieren' }))
    expect(await screen.findByText('Sync pausiert.')).toBeInTheDocument()
    expect(devRow().syncPaused).toBe(true)
  })
})
