import { isApiError } from '@/api/errors'
import type { PowerAction, Server, ServerState, ServerStatus } from '@/api/types'

export const FAST_POLL_MS = 3_000
export const SLOW_POLL_MS = 15_000

export const SERVER_STATES: readonly ServerState[] = [
  'not_configured',
  'unknown',
  'creating',
  'starting',
  'running',
  'stopping',
  'stopped',
  'restarting',
  'error',
]

export const POWER_ACTIONS: readonly PowerAction[] = ['start', 'stop', 'restart']

export type BadgeVariant = 'default' | 'secondary' | 'destructive' | 'outline'

const BADGE_VARIANTS: Record<ServerState, BadgeVariant> = {
  not_configured: 'outline',
  unknown: 'outline',
  creating: 'outline',
  starting: 'outline',
  running: 'default',
  stopping: 'outline',
  stopped: 'secondary',
  restarting: 'outline',
  error: 'destructive',
}

const TRANSITIONAL: ReadonlySet<ServerState> = new Set<ServerState>(['creating', 'starting', 'stopping', 'restarting'])

const POWER_STATES: Record<PowerAction, ReadonlySet<ServerState>> = {
  // unknown: the agent is away – the panel stores a start/stop wish and applies it later (spec §3.1). A restart needs
  // the agent right away (503 without it), so it is not offered then.
  start: new Set<ServerState>(['stopped', 'error', 'unknown']),
  stop: new Set<ServerState>(['running', 'starting', 'restarting', 'error', 'unknown']),
  restart: new Set<ServerState>(['running', 'error']),
}

export function stateBadgeVariant(state: ServerState): BadgeVariant {
  return BADGE_VARIANTS[state]
}

export function stateLabelKey(state: ServerState): string {
  return `servers.state.${state}`
}

/**
 * True while the state is expected to change soon (spec §9: poll every 3 s while an operation runs or the state
 * differs from desired_state). error, unknown and not_configured count as settled: polling faster cannot change them.
 */
export function isBusy(status: ServerStatus): boolean {
  if (status.operation !== '' || TRANSITIONAL.has(status.state)) return true
  return (status.desired_state === 'running' && status.state === 'stopped') || (status.desired_state === 'stopped' && status.state === 'running')
}

export function pollInterval(servers: readonly Server[] | undefined): number {
  return servers?.some((s) => isBusy(s.status)) ? FAST_POLL_MS : SLOW_POLL_MS
}

/**
 * Detail page: like pollInterval, but no polling without data or once the server is gone or hidden (403/404) –
 * a deleted server or a revoked permission must not be polled forever. Transient errors keep the last data polling.
 */
export function detailPollInterval(server: Server | undefined, error: unknown): number | false {
  if (!server || isApiError(error, 'not_found') || isApiError(error, 'forbidden')) return false
  return pollInterval([server])
}

export function powerAllowed(state: ServerState, action: PowerAction): boolean {
  return POWER_STATES[action].has(state)
}
