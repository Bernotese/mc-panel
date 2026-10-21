import { describe, expect, it } from 'vitest'
import { toWebRoutes, webPathError, webPortError, webRouteUrl } from './web-routes'

describe('webPathError', () => {
  it('accepts one lower-case segment of up to 32 characters', () => {
    for (const p of ['afk-smoke', 'a', '0', 'map2', 'a'.repeat(32)]) expect(webPathError(p, []), p).toBeNull()
  })

  it('rejects everything else, including the root', () => {
    for (const p of ['Afk', '', '-a', 'a-', 'a'.repeat(33), 'a/b', 'a.b', 'a_b', ' a']) expect(webPathError(p, []), JSON.stringify(p)).toBe('invalid')
  })

  it('reports a path another row already uses', () => {
    expect(webPathError('map', ['map'])).toBe('duplicate')
    expect(webPathError('map', ['maps'])).toBeNull()
  })
})

describe('webPortError', () => {
  it('follows agentapi.ValidWebPort', () => {
    expect(webPortError('8100')).toBeNull()
    expect(webPortError('1024')).toBeNull()
    expect(webPortError('65535')).toBeNull()
    expect(webPortError('25565')).toBe('reserved')
    expect(webPortError('25575')).toBe('reserved')
    for (const p of ['', '80', '1023', '65536', '8100.5', '-8100', 'abc']) expect(webPortError(p), p).toBe('invalid')
  })
})

describe('toWebRoutes', () => {
  it('puts the root first and sorts the path rows', () => {
    expect(
      toWebRoutes(true, '8100', [
        { path: 'b', port: '1' },
        { path: 'a', port: '2' },
      ]),
    ).toEqual([
      { path: '', port: 8100 },
      { path: 'a', port: 2 },
      { path: 'b', port: 1 },
    ])
  })

  it('leaves the root out while it is switched off', () => {
    expect(toWebRoutes(false, '', [])).toEqual([])
    expect(toWebRoutes(false, '8100', [{ path: ' afk-smoke ', port: ' 8200 ' }])).toEqual([{ path: 'afk-smoke', port: 8200 }])
  })
})

describe('webRouteUrl', () => {
  it('ends every URL in a slash', () => {
    expect(webRouteUrl('survival.mc.example.com', '')).toBe('https://survival.mc.example.com/')
    expect(webRouteUrl('survival.mc.example.com', 'afk-smoke')).toBe('https://survival.mc.example.com/afk-smoke/')
  })
})
