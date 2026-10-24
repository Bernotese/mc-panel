import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { applyMe, meKey } from '@/features/auth/me'
import { db, loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { createAppRouter } from '@/router'
import { createTestQueryClient } from '@/test/render'
import { routerNavigator, wireApi } from './wire-api'

async function mountApp(path: string, queryClient: QueryClient = createTestQueryClient()) {
  const router = createAppRouter(queryClient, createMemoryHistory({ initialEntries: [path] }))
  wireApi(queryClient, routerNavigator(router))
  await router.load()
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router, queryClient }
}

describe('app router with the real guards', () => {
  it('moves a session that the backend downgraded to setup onto /setup-2fa without looping', async () => {
    // Cached Me says "full"; the backend knows better (a new admin without any factor is downgraded to `setup`).
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('newadmin', { stage: 'full' }))
    const { router } = await mountApp('/', queryClient)

    expect(await screen.findByRole('heading', { name: 'Zwei-Faktor-Anmeldung einrichten' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/setup-2fa')
    expect(queryClient.getQueryData<{ stage: string }>(meKey)?.stage).toBe('setup')
    // stays there (no redirect ping-pong back to the dashboard)
    await new Promise((resolve) => setTimeout(resolve, 150))
    expect(router.state.location.pathname).toBe('/setup-2fa')
  })

  it('also downgrades plain users when 2FA became mandatory for everyone', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('alice', { stage: 'full' }))
    db.settings.enforce_2fa_for_users = true
    const { router } = await mountApp('/', queryClient)
    await waitFor(() => expect(router.state.location.pathname).toBe('/setup-2fa'))
  })

  it('moves a session that must change its password onto /change-password without looping (SEC-45)', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('alice')) // cached as full; the backend derives `password` from the flag
    db.users.find((u) => u.username === 'alice')!.mustChangePassword = true
    const { router } = await mountApp('/', queryClient)

    expect(await screen.findByRole('heading', { name: 'Neues Passwort wählen' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/change-password')
    await new Promise((resolve) => setTimeout(resolve, 150))
    expect(router.state.location.pathname).toBe('/change-password')
  })

  it('does not trust a stale cached full Me when opening /setup-2fa directly', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('newadmin', { stage: 'setup' }))
    const me = queryClient.getQueryData<ReturnType<typeof loginAs>>(meKey)!
    queryClient.setQueryData(meKey, { ...me, stage: 'full' })
    const { router } = await mountApp('/setup-2fa', queryClient)
    expect(await screen.findByRole('heading', { name: 'Zwei-Faktor-Anmeldung einrichten' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/setup-2fa')
  })

  it('renders the 404 page for a malformed user id in the permissions route', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('admin'))
    await mountApp('/users/abc/permissions', queryClient)
    expect(await screen.findByText('Seite nicht gefunden')).toBeInTheDocument()
  })

  it('shows a translated error screen with retry when a guard request fails', async () => {
    server.use(http.get('*/api/auth/me', () => HttpResponse.json({ error: 'jailed', message: 'ip jailed' }, { status: 403 })))
    await mountApp('/')
    expect(await screen.findByText('Deine IP-Adresse ist vorübergehend gesperrt.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Erneut versuchen' })).toBeInTheDocument()
  })

  it('keeps regular users out of the create server page', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('alice'))
    const { router } = await mountApp('/servers/new', queryClient)
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })

  it('renders the 404 page for a malformed server id', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('admin'))
    await mountApp('/servers/abc', queryClient)
    expect(await screen.findByText('Seite nicht gefunden')).toBeInTheDocument()
  })

  it('opens the detail page of a server', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('alice'))
    await mountApp('/servers/1', queryClient)
    expect(await screen.findByRole('heading', { name: 'survival', level: 1 })).toBeInTheDocument()
  })

  it('keeps regular users out of the settings page', async () => {
    const queryClient = createTestQueryClient()
    applyMe(queryClient, loginAs('alice'))
    const { router } = await mountApp('/settings', queryClient)
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
  })
})
