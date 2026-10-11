import { http, HttpResponse } from 'msw'
import type {
  ConsoleCommandRequest,
  ConsoleCommandResponse,
  ConsoleLineEvent,
  ConsoleStateEvent,
  OnlinePlayers,
  Permission,
  PlayerAction,
  PlayerActionRequest,
  PlayerLists,
} from '@/api/types'
import { db, recordAudit, type MockPlayers, type MockServer } from '../db'
import { API, apiError, guard, idParam, op, readJson, serverPermissions, type Authed, type ErrorResponse, type MockOperation } from './util'

const PLAYER_RE = /^[A-Za-z0-9_]{3,16}$/
const ACTIONS: readonly PlayerAction[] = ['kick', 'ban', 'pardon', 'op', 'deop', 'whitelist_add', 'whitelist_remove']
const MAX_COMMAND_BYTES = 1000
const MAX_REASON_LENGTH = 200
const MAX_PLAYERS = 20
const TICK_MS = 2000
// Like agentapi.ValidateCommand: no control character (incl. newline and tab) and no DEL.
// eslint-disable-next-line no-control-regex -- matches control characters on purpose (agentapi.ValidateCommand)
const CONTROL_RE = /[\u0000-\u001f\u007f]/
// Like the panel's reason check: printable only (Unicode category C = control/format/unassigned).
const NON_PRINTABLE_RE = /\p{C}/u

type Params = Parameters<typeof idParam>[0]

/** Session, permission and server lookup shared by every console operation. */
function access(request: Request, params: Params, perm: Permission): { g: Authed; server: MockServer } | ErrorResponse {
  const g = guard(request)
  if (g instanceof Response) return g
  const id = idParam(params)
  if (!serverPermissions(g.user, id).includes(perm)) return apiError(403, 'forbidden', `missing permission ${perm}`)
  const server = db.servers.find((s) => s.id === id)
  if (!server?.instance) return apiError(404, 'not_found', 'server not found')
  return { g, server }
}

function isRunning(s: MockServer): boolean {
  return s.status.state === 'running'
}

function hhmmss(): string {
  return new Date().toTimeString().slice(0, 8)
}

/** Minecraft's ban date format: "2026-10-11 09:32:51 +0000". */
function mcDate(d: Date): string {
  return `${d.toISOString().slice(0, 19).replace('T', ' ')} +0000`
}

/** Stable fake UUID per name (offline-mode servers derive them from the name, too). */
function mockUuid(name: string): string {
  let h = 0
  for (const c of name.toLowerCase()) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return `00000000-0000-4000-8000-${h.toString(16).padStart(12, '0')}`
}

/** Same rules as agentapi.ValidateCommand after stripping one leading "/"; null if invalid. */
function normalizeCommand(raw: unknown): string | null {
  if (typeof raw !== 'string') return null
  const cmd = raw.startsWith('/') ? raw.slice(1) : raw
  const bytes = new TextEncoder().encode(cmd).length
  if (bytes < 1 || bytes > MAX_COMMAND_BYTES || CONTROL_RE.test(cmd) || cmd.trim() === '') return null
  return cmd
}

function listOutput(p: MockPlayers, uuids: boolean): string {
  const names = p.online.map((o) => (uuids && o.uuid ? `${o.name} (${o.uuid})` : o.name)).join(', ')
  return `There are ${p.online.length} of a max of ${MAX_PLAYERS} players online: ${names}`
}

function commandOutput(server: MockServer, cmd: string): string {
  const [verb, ...rest] = cmd.trim().split(/\s+/)
  if (verb === 'list') return listOutput(server.players, rest[0] === 'uuids')
  if (verb === 'say') return ''
  return 'Unknown or incomplete command, see below for error'
}

function applyAction(p: MockPlayers, action: PlayerAction, name: string, reason: string): string {
  const lower = name.toLowerCase()
  const is = (n: { name: string }) => n.name.toLowerCase() === lower
  const uuid = p.online.find(is)?.uuid ?? mockUuid(name)
  switch (action) {
    case 'kick': {
      const player = p.online.find(is)
      if (!player) return 'No player was found'
      p.online = p.online.filter((o) => o !== player)
      return `Kicked ${player.name}: ${reason || 'Kicked by an operator'}`
    }
    case 'ban':
      if (p.bans.some(is)) return 'Nothing changed. The player is already banned'
      p.bans.push({ uuid, name, reason: reason || 'Banned by an operator.', source: 'Rcon', created: mcDate(new Date()), expires: 'forever' })
      p.online = p.online.filter((o) => !is(o))
      return `Banned ${name}: ${reason || 'Banned by an operator.'}`
    case 'pardon':
      if (!p.bans.some(is)) return "Nothing changed. The player isn't banned"
      p.bans = p.bans.filter((b) => !is(b))
      return `Unbanned ${name}`
    case 'op':
      if (p.ops.some(is)) return 'Nothing changed. The player already is an operator'
      p.ops.push({ uuid, name, level: 4 })
      return `Made ${name} a server operator`
    case 'deop':
      if (!p.ops.some(is)) return 'Nothing changed. The player is not an operator'
      p.ops = p.ops.filter((o) => !is(o))
      return `Made ${name} no longer a server operator`
    case 'whitelist_add':
      if (p.whitelist.some(is)) return 'Player is already whitelisted'
      p.whitelist.push({ uuid, name })
      return `Added ${name} to the whitelist`
    case 'whitelist_remove':
      if (!p.whitelist.some(is)) return 'Player is not whitelisted'
      p.whitelist = p.whitelist.filter((w) => !is(w))
      return `Removed ${name} from the whitelist`
  }
}

function startLines(server: MockServer): Array<[string, ConsoleLineEvent['stream']]> {
  const t = hhmmss()
  return [
    [`[${t} INFO]: Starting minecraft server version ${server.instance?.version ?? ''}`, 'out'],
    [`[${t} INFO]: Loading properties`, 'out'],
    [`[${t} INFO]: Default game type: SURVIVAL`, 'out'],
    [`[${t} INFO]: Preparing level "world"`, 'out'],
    [`[${t} INFO]: \u001b[33mPreparing spawn area: 100%\u001b[0m`, 'out'],
    [`[${t} INFO]: §aWelcome to ${server.name}§r`, 'out'],
    [`[${t} WARN]: Can't keep up! Is the server overloaded? Running 2043ms or 40 ticks behind`, 'out'],
    [`[${t} ERROR]: Could not pass event PlayerJoinEvent to ExamplePlugin v1.0`, 'err'],
    [`[${t} INFO]: Done (8.976s)! For help, type "help"`, 'out'],
    [`[${t} INFO]: Thread RCON Client /127.0.0.1 started`, 'out'],
    [`[${t} INFO]: Thread RCON Client /127.0.0.1 shutting down`, 'out'],
    [`[${t} INFO]: Alex joined the game`, 'out'],
  ]
}

function sseState(running: boolean): string {
  const data: ConsoleStateEvent = { running }
  return `event: state\ndata: ${JSON.stringify(data)}\n\n`
}

function sseLine(id: bigint, line: string, stream: ConsoleLineEvent['stream']): string {
  const data: ConsoleLineEvent = { ts: Date.now(), stream, line }
  return `id: ${id.toString()}\nevent: line\ndata: ${JSON.stringify(data)}\n\n`
}

export const consoleOperations: MockOperation[] = [
  op(
    'streamConsole',
    // Plain http.get with a ReadableStream instead of msw's sse(): sse() needs a global
    // EventSource (missing in jsdom) and cannot answer 401/403/404.
    http.get(`${API}/servers/:id/console/stream`, ({ request, params }) => {
      const a = access(request, params, 'server.console')
      if (a instanceof Response) return a
      const encoder = new TextEncoder()
      let timer: ReturnType<typeof setInterval> | undefined
      const stop = () => {
        if (timer !== undefined) clearInterval(timer)
        timer = undefined
      }
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          const send = (text: string) => {
            try {
              controller.enqueue(encoder.encode(text))
            } catch {
              stop() // the client went away
            }
          }
          if (!isRunning(a.server)) {
            send(sseState(false))
            return
          }
          // state first, before any line: the console shows "connected" at once (V3).
          send(sseState(true))
          // Event ids are Docker timestamps in ns: strictly increasing per line.
          let id = BigInt(Date.now()) * 1_000_000n
          const line = (text: string, s: ConsoleLineEvent['stream'] = 'out') => {
            id += 1n
            send(sseLine(id, text, s))
          }
          for (const [text, s] of startLines(a.server)) line(text, s)
          let tick = 0
          timer = setInterval(() => {
            tick += 1
            line(`[${hhmmss()} INFO]: Tick ${tick}`)
          }, TICK_MS)
        },
        cancel() {
          stop()
        },
      })
      request.signal.addEventListener('abort', stop)
      return new HttpResponse(stream, { headers: { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-store' } })
    }),
  ),

  op(
    'sendConsoleCommand',
    http.post(`${API}/servers/:id/console`, async ({ request, params }) => {
      const a = access(request, params, 'server.console')
      if (a instanceof Response) return a
      const body = await readJson<Partial<ConsoleCommandRequest>>(request)
      const cmd = normalizeCommand(body?.command)
      if (cmd === null) return apiError(400, 'validation_failed', 'command: 1..1000 bytes without control characters')
      const target = `server:${a.server.id}`
      if (!isRunning(a.server)) {
        recordAudit(a.g.user, 'server.console_command', target, 'failure', { command: cmd })
        return apiError(409, 'instance_not_running', 'server is not running')
      }
      recordAudit(a.g.user, 'server.console_command', target, 'success', { command: cmd })
      return HttpResponse.json<ConsoleCommandResponse>({ output: commandOutput(a.server, cmd) })
    }),
  ),

  op(
    'getOnlinePlayers',
    http.get(`${API}/servers/:id/players`, ({ request, params }) => {
      const a = access(request, params, 'server.view')
      if (a instanceof Response) return a
      if (!isRunning(a.server)) return HttpResponse.json<OnlinePlayers>({ available: false, online: [], max: null })
      return HttpResponse.json<OnlinePlayers>({ available: true, online: a.server.players.online, max: MAX_PLAYERS })
    }),
  ),

  op(
    'getPlayerLists',
    http.get(`${API}/servers/:id/players/lists`, ({ request, params }) => {
      const a = access(request, params, 'server.console')
      if (a instanceof Response) return a
      const p = a.server.players
      return HttpResponse.json<PlayerLists>({ ops: p.ops, whitelist: p.whitelist, bans: p.bans, errors: [] })
    }),
  ),

  op(
    'playerAction',
    http.post(`${API}/servers/:id/players/actions`, async ({ request, params }) => {
      const a = access(request, params, 'server.console')
      if (a instanceof Response) return a
      const body = await readJson<Partial<PlayerActionRequest>>(request)
      if (!body?.action || !ACTIONS.includes(body.action)) return apiError(400, 'validation_failed', 'action: unknown action')
      if (typeof body.name !== 'string' || !PLAYER_RE.test(body.name)) return apiError(400, 'validation_failed', 'name must match ^[A-Za-z0-9_]{3,16}$')
      const reason = body.reason ?? ''
      if (reason !== '' && body.action !== 'kick' && body.action !== 'ban') return apiError(400, 'validation_failed', 'reason: only for kick and ban')
      if ([...reason].length > MAX_REASON_LENGTH || NON_PRINTABLE_RE.test(reason)) return apiError(400, 'validation_failed', 'reason: at most 200 printable characters')
      const target = `server:${a.server.id}`
      const details = { action: body.action, name: body.name, ...(reason ? { reason } : {}) }
      if (!isRunning(a.server)) {
        recordAudit(a.g.user, 'server.player_action', target, 'failure', details)
        return apiError(409, 'instance_not_running', 'server is not running')
      }
      const output = applyAction(a.server.players, body.action, body.name, reason)
      recordAudit(a.g.user, 'server.player_action', target, 'success', details)
      return HttpResponse.json<ConsoleCommandResponse>({ output })
    }),
  ),
]
