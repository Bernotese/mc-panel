import type { Permission, Server } from '@/api/types'
import { pluginKindOf } from '@/features/plugins/model'

export type ServerSection = 'overview' | 'monitoring' | 'console' | 'players' | 'files' | 'plugins' | 'backups' | 'settings'

export const SECTION_ORDER: readonly ServerSection[] = ['overview', 'monitoring', 'console', 'players', 'files', 'plugins', 'backups', 'settings']

type SectionServer = Pick<Server, 'permissions' | 'instance'>

/** Spec §4.3: the only place that decides which areas of a server a user sees (tree, drawer and route guards). */
export function serverSections(server: SectionServer, isAdmin: boolean): ServerSection[] {
  if (server.instance === null) return ['overview']
  const has = (p: Permission) => server.permissions.includes(p)
  const allowed = new Set<ServerSection>(['overview', 'monitoring'])
  if (has('server.console')) {
    allowed.add('console')
    allowed.add('players')
  }
  if (has('server.files')) allowed.add('files')
  // vanilla and modpacks get no plugins section, so the UI never meets 409 plugins_unsupported.
  if (has('server.plugins') && pluginKindOf(server.instance.type) !== null) allowed.add('plugins')
  if (has('server.backups')) allowed.add('backups')
  if (isAdmin) allowed.add('settings')
  return SECTION_ORDER.filter((s) => allowed.has(s))
}

/** i18n key of a section's label; plugins are called "Mods" on fabric/neoforge. */
export function sectionLabelKey(section: ServerSection, server: Pick<Server, 'instance'>): string {
  if (section === 'plugins' && server.instance && pluginKindOf(server.instance.type) === 'mod') return 'servers.detail.tabs.mods'
  return `servers.detail.tabs.${section}`
}

export type SectionPath = '/servers/$id' | `/servers/$id/${Exclude<ServerSection, 'overview'>}`

export function sectionHref(section: ServerSection): SectionPath {
  return section === 'overview' ? '/servers/$id' : `/servers/$id/${section}`
}

/** Section to open on `target` when switching from `current`: keeps it if allowed, else overview. */
export function sectionForSwitch(current: ServerSection, target: SectionServer, isAdmin: boolean): ServerSection {
  return serverSections(target, isAdmin).includes(current) ? current : 'overview'
}

/** Section of a pathname like `/servers/1/console`; everything else is the overview. */
export function sectionFromPath(pathname: string): ServerSection {
  const seg = pathname.split('/')[3]
  return SECTION_ORDER.find((s) => s === seg) ?? 'overview'
}

/** Id of the server whose page is open (`/servers/<id>[/<section>]`), else null. Path-based: other routes have an `$id` param too. */
export function serverIdFromPath(pathname: string): number | null {
  const match = /^\/servers\/([1-9]\d*)(?:\/|$)/.exec(pathname)
  return match ? Number(match[1]) : null
}
