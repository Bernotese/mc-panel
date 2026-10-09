import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { db, loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { AgentStatusCard } from './agent-status-card'

describe('AgentStatusCard', () => {
  it('shows firewall backend, version and pending syncs', async () => {
    await renderWithProviders(<AgentStatusCard />, { me: loginAs('admin') })
    expect(await screen.findByText('Erreichbar')).toBeInTheDocument()
    expect(screen.getByText('noop')).toBeInTheDocument()
    expect(screen.getByText('0.1.0-dev')).toBeInTheDocument()
  })

  it('degrades gracefully when the agent is unreachable', async () => {
    db.agent = { reachable: false, firewall: '', version: '', pending_sync: 3 }
    await renderWithProviders(<AgentStatusCard />, { me: loginAs('admin') })
    expect(await screen.findByText('Nicht erreichbar')).toBeInTheDocument()
    expect(screen.getByText(/Sperren wirken weiterhin in der App/)).toBeInTheDocument()
  })

  it('treats 503 agent_unavailable like an unreachable agent', async () => {
    server.use(http.get('*/api/security/agent', () => HttpResponse.json({ error: 'agent_unavailable', message: 'down' }, { status: 503 })))
    await renderWithProviders(<AgentStatusCard />, { me: loginAs('admin') })
    expect(await screen.findByText('Nicht erreichbar')).toBeInTheDocument()
  })
})
