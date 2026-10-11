import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { playerListsKey } from '@/features/players/api'
import { ServerDetailPage } from '@/features/servers/server-detail-page'
import { loginAs } from '@/mocks/db'
import { server } from '@/mocks/node'
import { renderWithProviders } from '@/test/render'
import { FakeEventSource } from './testing/fake-event-source'
import { stubLogViewport } from './testing/layout'

let eventSeq = 1_000

async function openConsole(serverId = 1) {
  const user = userEvent.setup()
  await renderWithProviders(<ServerDetailPage serverId={serverId} />, { me: loginAs('admin') })
  await user.click(await screen.findByRole('tab', { name: 'Konsole' }))
  await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
  const es = FakeEventSource.latest()
  act(() => es.open())
  return { user, es, log: screen.getByRole('log', { name: 'Server-Log' }) }
}

function emitLine(es: FakeEventSource, text: string, stream: 'out' | 'err' = 'out') {
  eventSeq += 1
  act(() => es.emit('line', { ts: Date.now(), stream, line: text }, String(eventSeq)))
}

beforeEach(() => {
  FakeEventSource.reset()
  vi.stubGlobal('EventSource', FakeEventSource)
  stubLogViewport()
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('ConsoleTab', () => {
  it('shows log lines, marks warnings and hides RCON noise until asked', async () => {
    const { user, es, log } = await openConsole()
    expect(screen.getByText('Verbunden')).toBeInTheDocument()
    emitLine(es, '[10:00:00 INFO]: Done (3.2s)! For help, type "help"')
    emitLine(es, '[10:00:01 INFO]: Thread RCON Client /127.0.0.1 started')
    emitLine(es, "\u001b[33m[10:00:02 WARN]: Can't keep up!\u001b[0m")
    expect(await within(log).findByText(/Done \(3\.2s\)/)).toBeInTheDocument()
    expect(within(log).getByText("[10:00:02 WARN]: Can't keep up!").closest('[data-level]')).toHaveAttribute('data-level', 'warn')
    expect(within(log).queryByText(/Thread RCON Client/)).not.toBeInTheDocument()

    await user.click(screen.getByRole('switch', { name: 'RCON-Meldungen ausblenden' }))
    expect(await within(log).findByText(/Thread RCON Client/)).toBeInTheDocument()
  })

  it('renders markup in log lines as text', async () => {
    const { es, log } = await openConsole()
    emitLine(es, '<img src=x onerror=alert(1)>')
    expect(await within(log).findByText('<img src=x onerror=alert(1)>')).toBeInTheDocument()
    expect(log.querySelector('img')).toBeNull()
  })

  it('filters the log', async () => {
    const { user, es, log } = await openConsole()
    emitLine(es, 'Done (3.2s)!')
    emitLine(es, "Can't keep up!")
    await within(log).findByText("Can't keep up!")
    await user.type(screen.getByRole('searchbox', { name: 'Log filtern' }), 'keep')
    await waitFor(() => expect(within(log).queryByText('Done (3.2s)!')).not.toBeInTheDocument())
    expect(within(log).getByText("Can't keep up!")).toBeInTheDocument()
  })

  it('sends a command and shows the answer as a block', async () => {
    const { user, log } = await openConsole()
    const input = screen.getByRole('textbox', { name: 'Befehl' })
    await user.type(input, 'list{Enter}')
    expect(await within(log).findByText('› list')).toBeInTheDocument()
    expect(await within(log).findByText(/of a max of 20 players online/)).toBeInTheDocument()
    expect(input).toHaveValue('')
  })

  it('shows why a command failed', async () => {
    server.use(http.post('*/api/servers/:id/console', () => HttpResponse.json({ error: 'rcon_unavailable', message: 'not ready' }, { status: 503 })))
    const { user, log } = await openConsole()
    await user.type(screen.getByRole('textbox', { name: 'Befehl' }), 'list{Enter}')
    expect(await within(log).findByText('Die Server-Konsole antwortet gerade nicht – startet der Server noch?')).toBeInTheDocument()
  })

  it('checks a command before sending it', async () => {
    const { user } = await openConsole()
    await user.type(screen.getByRole('textbox', { name: 'Befehl' }), '/{Enter}')
    expect(await screen.findByRole('alert')).toHaveTextContent('Gib einen Befehl ein.')
  })

  it('recalls earlier commands with the arrow keys', async () => {
    const { user, log } = await openConsole()
    const input = screen.getByRole('textbox', { name: 'Befehl' })
    await user.type(input, 'list{Enter}')
    await user.type(input, 'say hi{Enter}')
    expect(await within(log).findByText('Keine Ausgabe.')).toBeInTheDocument()
    await user.type(input, '{ArrowUp}')
    expect(input).toHaveValue('say hi')
    await user.type(input, '{ArrowUp}')
    expect(input).toHaveValue('list')
    await user.type(input, '{ArrowDown}{ArrowDown}')
    expect(input).toHaveValue('')
  })

  it('locks the command line while the server is stopped', async () => {
    const { es } = await openConsole(2)
    expect(screen.getByRole('textbox', { name: 'Befehl' })).toBeDisabled()
    expect(screen.getByText('Der Server läuft nicht. Befehle sind erst nach dem Start möglich.')).toBeInTheDocument()
    act(() => es.emit('state', { running: false }))
    expect(screen.getByText('Server gestoppt')).toBeInTheDocument()
  })

  it('offers a jump to new lines after scrolling up', async () => {
    const { user, es, log } = await openConsole()
    emitLine(es, 'line one')
    await within(log).findByText('line one')
    Object.defineProperty(log, 'scrollHeight', { configurable: true, value: 2000 })
    Object.defineProperty(log, 'clientHeight', { configurable: true, value: 600 })
    log.scrollTop = 0
    fireEvent.scroll(log)
    expect(screen.queryByRole('button', { name: 'Neue Zeilen' })).not.toBeInTheDocument()
    emitLine(es, 'line two')
    await user.click(await screen.findByRole('button', { name: 'Neue Zeilen' }))
    expect(screen.queryByRole('button', { name: 'Neue Zeilen' })).not.toBeInTheDocument()
  })

  it('copies the visible lines', async () => {
    const { user, es, log } = await openConsole()
    emitLine(es, 'alpha')
    emitLine(es, 'Thread RCON Client /127.0.0.1 started')
    emitLine(es, 'beta')
    await within(log).findByText('beta')
    await user.click(screen.getByRole('button', { name: 'Kopieren' }))
    expect(await screen.findByText('Sichtbare Zeilen kopiert.')).toBeInTheDocument()
    expect(await navigator.clipboard.readText()).toBe('alpha\nbeta')
  })

  it('announces a lost permission and locks the command line', async () => {
    server.use(http.get('*/api/servers/:id/console/stream', () => HttpResponse.json({ error: 'forbidden', message: 'no' }, { status: 403 })))
    const { es } = await openConsole()
    expect(screen.getByRole('textbox', { name: 'Befehl' })).toBeEnabled()
    act(() => es.fail(true))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Keine Berechtigung'))
    expect(screen.getByRole('textbox', { name: 'Befehl' })).toBeDisabled()
  })

  it('locks the command line for a server that is gone', async () => {
    server.use(http.get('*/api/servers/:id/console/stream', () => HttpResponse.json({ error: 'not_found', message: 'gone' }, { status: 404 })))
    const { es } = await openConsole()
    act(() => es.fail(true))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Server nicht gefunden'))
    expect(screen.getByRole('textbox', { name: 'Befehl' })).toBeDisabled()
  })

  it('names the limit of open consoles', async () => {
    server.use(http.get('*/api/servers/:id/console/stream', () => HttpResponse.json({ error: 'rate_limited', message: 'too many' }, { status: 429 })))
    const { es } = await openConsole()
    act(() => es.fail(true))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Zu viele offene Konsolen – schließe eine andere.'))
  })

  it('refreshes the player data after a command', async () => {
    const user = userEvent.setup()
    const { queryClient } = await renderWithProviders(<ServerDetailPage serverId={1} />, { me: loginAs('admin') })
    queryClient.setQueryData(playerListsKey(1), { ops: [], whitelist: [], bans: [], errors: [] })
    await user.click(await screen.findByRole('tab', { name: 'Konsole' }))
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
    act(() => FakeEventSource.latest().open())
    expect(queryClient.getQueryState(playerListsKey(1))?.isInvalidated).toBe(false)
    await user.type(screen.getByRole('textbox', { name: 'Befehl' }), 'op Steve{Enter}')
    await waitFor(() => expect(queryClient.getQueryState(playerListsKey(1))?.isInvalidated).toBe(true))
  })
})
