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

/** One path row of the form; `key` only keeps React rows apart, `touched` only decides when errors show. */
export type WebRouteRow = { key: number; path: string; port: string; touched: boolean }
/** The form's draft: the root slot and the path rows. */
export type WebRoutesDraft = { rootOn: boolean; rootPort: string; rows: WebRouteRow[] }

export function webDraftFromRoutes(stored: readonly WebRouteInput[]): WebRoutesDraft {
  const root = stored.find((r) => r.path === '')
  return {
    rootOn: root !== undefined,
    rootPort: String(root?.port ?? DEFAULT_ROOT_PORT),
    rows: stored.filter((r) => r.path !== '').map((r, i) => ({ key: i, path: r.path, port: String(r.port), touched: true })),
  }
}

export const webDraftRoutes = (d: WebRoutesDraft): WebRouteInput[] => toWebRoutes(d.rootOn, d.rootPort, d.rows)

/** Both drafts publish the same routes (ports as numbers: "08100" is 8100). */
export function sameWebDraft(a: WebRoutesDraft, b: WebRoutesDraft): boolean {
  return JSON.stringify(webDraftRoutes(a)) === JSON.stringify(webDraftRoutes(b))
}

/**
 * Issue #154, "keep my changes" for the routes (they carry no revision): the stored list with only the draft's own
 * changes against its base – root switched or re-ported, paths added, re-ported or removed (a renamed path is one
 * removed and one added). Routes someone else added or changed stay unless the draft changed the same path.
 */
export function rebaseWebDraft(base: WebRoutesDraft, draft: WebRoutesDraft, remote: WebRoutesDraft): WebRoutesDraft {
  const baseRoot = webDraftRoutes({ ...base, rows: [] })
  const draftRoot = webDraftRoutes({ ...draft, rows: [] })
  const root = JSON.stringify(baseRoot) === JSON.stringify(draftRoot) ? remote : draft
  const path = (r: WebRouteRow) => r.path.trim()
  const port = (r: WebRouteRow) => Number(r.port.trim())
  const baseRows = new Map(base.rows.map((r) => [path(r), r]))
  const draftPaths = new Set(draft.rows.map(path))
  const own = draft.rows.filter((r) => {
    const b = baseRows.get(path(r))
    return b === undefined || port(b) !== port(r)
  })
  const replaced = new Set([...[...baseRows.keys()].filter((p) => !draftPaths.has(p)), ...own.map(path)])
  const rows = [...remote.rows.filter((r) => !replaced.has(path(r))), ...own]
  return { rootOn: root.rootOn, rootPort: root.rootPort, rows: rows.map((r, i) => ({ ...r, key: i })) }
}
