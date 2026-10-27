import { screen, within } from '@testing-library/react'
import { http } from 'msw'
import { describe, expect, it } from 'vitest'
import { PlayersTab } from '@/features/players/players-tab'
import { server as mockApi } from '@/mocks/node'
import { renderServerTab } from '@/test/render-section'

describe('DevOpsSection', () => {
  it('is part of the players area of a dev only', async () => {
    await renderServerTab((s) => <PlayersTab server={s} />, { serverId: 1 })
    await screen.findByRole('region', { name: 'Online' })
    expect(screen.queryByRole('region', { name: 'Dev-Ops' })).not.toBeInTheDocument()
  })

  it('adds and removes dev ops while the dev sleeps and blocks invalid names', async () => {
    let posts = 0
    mockApi.use(
      http.post('*/api/servers/5/dev/ops', () => {
        posts += 1
      }),
    )
    const { user } = await renderServerTab((s) => <PlayersTab server={s} />, { serverId: 5 })
    const section = await screen.findByRole('region', { name: 'Dev-Ops' })
    expect(await within(section).findByText('Steve')).toBeInTheDocument()
    const input = within(section).getByLabelText('Dev-Op hinzufügen')
    expect(input).toBeEnabled()
    await user.type(input, 'no spaces')
    await user.click(within(section).getByRole('button', { name: 'Hinzufügen' }))
    expect(within(section).getByText('3–16 Buchstaben, Ziffern oder Unterstriche.')).toBeInTheDocument()
    expect(posts).toBe(0)
    await user.clear(input)
    await user.type(input, 'Alex')
    await user.click(within(section).getByRole('button', { name: 'Hinzufügen' }))
    expect(await within(section).findByText('Alex')).toBeInTheDocument()
    expect(posts).toBe(1)
    await user.click(within(section).getByRole('button', { name: 'Steve aus den Dev-Ops entfernen' }))
    expect(await screen.findByText('Steve ist kein Dev-Op mehr.')).toBeInTheDocument()
    expect(within(section).queryByText('Steve')).not.toBeInTheDocument()
  })
})
