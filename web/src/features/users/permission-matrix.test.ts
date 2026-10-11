import { describe, expect, it } from 'vitest'
import { fromMatrix, newlyGranted, sameMatrix, toggle, toMatrix } from './permission-matrix'

describe('permission matrix', () => {
  it('round-trips grants and drops empty servers', () => {
    const grants = [
      { server_id: 2, permissions: ['server.view' as const] },
      { server_id: 1, permissions: ['server.power' as const, 'server.view' as const] },
      { server_id: 3, permissions: [] },
    ]
    expect(fromMatrix(toMatrix(grants))).toEqual([
      { server_id: 1, permissions: ['server.view', 'server.power'] },
      { server_id: 2, permissions: ['server.view'] },
    ])
  })

  it('adds server.view whenever another permission is granted', () => {
    expect(toggle({}, 1, 'server.power', true)).toEqual({ 1: ['server.view', 'server.power'] })
  })

  it('removes every permission of a server when server.view is revoked', () => {
    expect(toggle({ 1: ['server.view', 'server.power'] }, 1, 'server.view', false)).toEqual({ 1: [] })
  })

  it('removes a single non-view permission', () => {
    expect(toggle({ 1: ['server.view', 'server.power'] }, 1, 'server.power', false)).toEqual({ 1: ['server.view'] })
  })

  it('compares matrices independent of order and empty entries', () => {
    expect(sameMatrix({ 1: ['server.power', 'server.view'], 2: [] }, { 1: ['server.view', 'server.power'] })).toBe(true)
    expect(sameMatrix({ 1: ['server.view'] }, {})).toBe(false)
  })
})

describe('newlyGranted', () => {
  it('lists servers where a permission was added', () => {
    const saved = { 1: ['server.view' as const], 2: ['server.view' as const, 'server.files' as const] }
    const draft = { 1: ['server.view' as const, 'server.files' as const], 2: ['server.view' as const, 'server.files' as const], 3: ['server.view' as const] }
    expect(newlyGranted(draft, saved, 'server.files')).toEqual([1])
    expect(newlyGranted(saved, saved, 'server.files')).toEqual([])
  })
})
