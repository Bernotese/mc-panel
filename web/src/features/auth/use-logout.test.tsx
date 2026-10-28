import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { LogoutButton } from './logout-button'
import { meKey } from './me'

/** Status of an authenticated request sent after the logout: 401 once the server session ended. */
async function meStatus(): Promise<number> {
  return (await fetch(`${window.location.origin}/api/auth/me`)).status
}

const unavailable = () => HttpResponse.json({ error: 'internal', message: 'unavailable' }, { status: 503 })

describe('useLogout', () => {
  it('shows the sign-in page once the server ended the session', async () => {
    const { router } = await renderWithProviders(<LogoutButton />, { me: loginAs('alice') })
    await userEvent.setup().click(screen.getByRole('button', { name: 'Abmelden' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(await meStatus()).toBe(401)
  })

  it('stays signed in and offers a retry when the server did not confirm the logout', async () => {
    server.use(http.post('*/api/auth/logout', unavailable, { once: true }))
    const { router, queryClient } = await renderWithProviders(<LogoutButton />, { me: loginAs('alice') })
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Abmelden' }))

    expect(await screen.findByText('Abmelden fehlgeschlagen: Die Sitzung ist noch aktiv.')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/')
    expect(queryClient.getQueryData(meKey)).toMatchObject({ username: 'alice' })
    expect(await meStatus()).toBe(200) // the server session survived, and the UI says so

    // fireEvent: the toast's swipe handling calls setPointerCapture on pointerdown, which jsdom lacks.
    fireEvent.click(screen.getByRole('button', { name: 'Erneut versuchen' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(await meStatus()).toBe(401)
    await waitFor(() => expect(screen.queryByText('Abmelden fehlgeschlagen: Die Sitzung ist noch aktiv.')).not.toBeInTheDocument())
  })

  it('treats an already ended session as signed out', async () => {
    server.use(http.post('*/api/auth/logout', () => HttpResponse.json({ error: 'unauthorized', message: 'authentication required' }, { status: 401 })))
    const { router } = await renderWithProviders(<LogoutButton />, { me: loginAs('alice') })
    await userEvent.setup().click(screen.getByRole('button', { name: 'Abmelden' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(screen.queryByText('Abmelden fehlgeschlagen: Die Sitzung ist noch aktiv.')).not.toBeInTheDocument()
  })

  it('signs out when the logout failed but the session is gone anyway', async () => {
    server.use(
      http.post('*/api/auth/logout', unavailable),
      http.get('*/api/auth/me', () => HttpResponse.json({ error: 'unauthorized', message: 'authentication required' }, { status: 401 })),
    )
    const { router } = await renderWithProviders(<LogoutButton />, { me: loginAs('alice') })
    await userEvent.setup().click(screen.getByRole('button', { name: 'Abmelden' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
  })
})
