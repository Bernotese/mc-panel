import { describe, expect, it } from 'vitest'
import type { Me } from '@/api/types'
import { adminGuard, appGuard, loginGuard, pathForStage, secondFactorGuard, setupGuard } from './guards'

function me(stage: Me['stage'], role: Me['role'] = 'admin'): Me {
  return {
    id: 1,
    username: 'x',
    role,
    stage,
    locale: 'de',
    totp_enabled: true,
    passkey_count: 0,
    recovery_codes_remaining: 10,
    second_factors: ['totp'],
    sudo_until: 0,
    csrf_token: 't',
  }
}

describe('guards', () => {
  it('maps stages to their page', () => {
    expect(pathForStage('preauth')).toBe('/login/2fa')
    expect(pathForStage('setup')).toBe('/setup-2fa')
    expect(pathForStage('full')).toBe('/')
  })

  it('protects the app area', () => {
    expect(appGuard(null)).toBe('/login')
    expect(appGuard(me('preauth'))).toBe('/login/2fa')
    expect(appGuard(me('setup'))).toBe('/setup-2fa')
    expect(appGuard(me('full'))).toBeNull()
  })

  it('keeps users out of admin pages', () => {
    expect(adminGuard(me('full', 'user'))).toBe('/')
    expect(adminGuard(me('full', 'admin'))).toBeNull()
    expect(adminGuard(me('setup', 'admin'))).toBe('/setup-2fa')
  })

  it('routes the auth pages by stage', () => {
    expect(loginGuard(null)).toBeNull()
    expect(loginGuard(me('preauth'))).toBeNull()
    expect(loginGuard(me('full'))).toBe('/')
    expect(secondFactorGuard(null)).toBe('/login')
    expect(secondFactorGuard(me('preauth'))).toBeNull()
    expect(secondFactorGuard(me('full'))).toBe('/')
    expect(setupGuard(me('setup'))).toBeNull()
    expect(setupGuard(me('preauth'))).toBe('/login/2fa')
    expect(setupGuard(me('full'))).toBe('/')
  })
})
