import { ALL_PERMISSIONS, type Permission, type PermissionGrant } from '@/api/types'

export type Matrix = Record<number, Permission[]>

function ordered(perms: Iterable<Permission>): Permission[] {
  const set = new Set(perms)
  return ALL_PERMISSIONS.filter((p) => set.has(p))
}

export function toMatrix(grants: PermissionGrant[]): Matrix {
  const matrix: Matrix = {}
  for (const grant of grants) matrix[grant.server_id] = ordered(grant.permissions)
  return matrix
}

export function fromMatrix(matrix: Matrix): PermissionGrant[] {
  return Object.entries(matrix)
    .map(([serverId, perms]) => ({ server_id: Number(serverId), permissions: ordered(perms) }))
    .filter((g) => g.permissions.length > 0)
    .sort((a, b) => a.server_id - b.server_id)
}

/** Stream-D semantics: every permission implies server.view; revoking view revokes everything on that server. */
export function toggle(matrix: Matrix, serverId: number, permission: Permission, on: boolean): Matrix {
  const current = new Set(matrix[serverId] ?? [])
  if (on) {
    current.add(permission)
    current.add('server.view')
  } else if (permission === 'server.view') {
    current.clear()
  } else {
    current.delete(permission)
  }
  return { ...matrix, [serverId]: ordered(current) }
}

export function sameMatrix(a: Matrix, b: Matrix): boolean {
  return JSON.stringify(fromMatrix(a)) === JSON.stringify(fromMatrix(b))
}

/** Server ids where permission is in draft but was not saved yet (for the files warning, spec §6.4). */
export function newlyGranted(draft: Matrix, saved: Matrix, permission: Permission): number[] {
  return Object.entries(draft)
    .filter(([id, perms]) => perms.includes(permission) && !(saved[Number(id)] ?? []).includes(permission))
    .map(([id]) => Number(id))
    .sort((a, b) => a - b)
}
