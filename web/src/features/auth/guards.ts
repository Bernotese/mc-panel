import type { Me, Stage } from '@/api/types'

export type AuthPath = '/' | '/login' | '/login/2fa' | '/setup-2fa'

export function pathForStage(stage: Stage): AuthPath {
  if (stage === 'preauth') return '/login/2fa'
  if (stage === 'setup') return '/setup-2fa'
  return '/'
}

export function appGuard(me: Me | null): AuthPath | null {
  if (!me) return '/login'
  return me.stage === 'full' ? null : pathForStage(me.stage)
}

export function adminGuard(me: Me | null): AuthPath | null {
  const base = appGuard(me)
  if (base || !me) return base ?? '/login'
  return me.role === 'admin' ? null : '/'
}

/** The login page stays reachable in preauth/setup so a user can switch accounts. */
export function loginGuard(me: Me | null): AuthPath | null {
  return me?.stage === 'full' ? '/' : null
}

export function secondFactorGuard(me: Me | null): AuthPath | null {
  if (!me) return '/login'
  return me.stage === 'preauth' ? null : pathForStage(me.stage)
}

export function setupGuard(me: Me | null): AuthPath | null {
  if (!me) return '/login'
  return me.stage === 'setup' ? null : pathForStage(me.stage)
}
