import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { Me } from '@/api/types'
import { MOCK_PASSWORD } from '@/mocks/db'
import { renderWithProviders } from '@/test/render'
import { LoginPage } from './login-page'
import { meKey } from './me'

async function fillAndSubmit(username: string, password: string) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText('Benutzername'), username)
  await user.type(screen.getByLabelText('Passwort'), password)
  await user.click(screen.getByRole('button', { name: 'Anmelden' }))
}

describe('LoginPage', () => {
  it('keeps submit disabled until both fields are filled', async () => {
    await renderWithProviders(<LoginPage />, { path: '/login' })
    expect(screen.getByRole('button', { name: 'Anmelden' })).toBeDisabled()
  })

  it('shows a generic error for wrong credentials and stays on /login', async () => {
    const { router } = await renderWithProviders(<LoginPage />, { path: '/login' })
    await fillAndSubmit('admin', 'definitely-wrong')
    expect(await screen.findByRole('alert')).toHaveTextContent('Benutzername oder Passwort falsch.')
    expect(router.state.location.pathname).toBe('/login')
  })

  it.each([
    ['admin', '/login/2fa', 'preauth'],
    ['newadmin', '/setup-2fa', 'setup'],
    ['alice', '/', 'full'],
  ])('sends %s to %s', async (username, target, stage) => {
    const { router, queryClient } = await renderWithProviders(<LoginPage />, { path: '/login' })
    await fillAndSubmit(username, MOCK_PASSWORD)
    await waitFor(() => expect(router.state.location.pathname).toBe(target))
    expect(queryClient.getQueryData<Me>(meKey)?.stage).toBe(stage)
  })
})
