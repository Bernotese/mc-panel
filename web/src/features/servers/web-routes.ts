/** Web port paths spec §2: at most 8 routes per server (agentapi.MaxWebTargets). */
export const MAX_WEB_ROUTES = 8

/** BlueMap's default web server port (web ports spec §1.1); prefilled for the root. */
export const DEFAULT_ROOT_PORT = 8100

/** agentapi.ValidWebPath without the root "": one segment, 1–32 of [a-z0-9-], first and last a letter or digit. */
const WEB_PATH = /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/

export type WebRouteInput = { path: string; port: number }

/** The port rule of agentapi.ValidWebPort; null = valid. */
export function webPortError(raw: string): 'invalid' | 'reserved' | null {
  if (!/^\d{1,5}$/.test(raw)) return 'invalid'
  const port = Number(raw)
  if (port === 25565 || port === 25575) return 'reserved'
  return port >= 1024 && port <= 65535 ? null : 'invalid'
}

/** The path rule of a path row (the root has its own slot, so "" is invalid here); null = valid. */
export function webPathError(raw: string, taken: string[]): 'invalid' | 'duplicate' | null {
  if (!WEB_PATH.test(raw)) return 'invalid'
  return taken.includes(raw) ? 'duplicate' : null
}

/** The request body's list in the panel's order: the root (if switched on) first, then the path rows by path. */
export function toWebRoutes(rootOn: boolean, rootPort: string, rows: { path: string; port: string }[]): WebRouteInput[] {
  const paths = rows
    .map((r) => ({ path: r.path.trim(), port: Number(r.port.trim()) }))
    .sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))
  return rootOn ? [{ path: '', port: Number(rootPort.trim()) }, ...paths] : paths
}

/** The public URL of a route, as the panel builds it: https://<host>/ or https://<host>/<path>/. */
export function webRouteUrl(host: string, path: string): string {
  return path ? `https://${host}/${path}/` : `https://${host}/`
}
