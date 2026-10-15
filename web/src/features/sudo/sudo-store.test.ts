import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { isSudoOpen, requestSudo, settleSudo, useSudoActions } from './sudo-store'

describe('sudo store', () => {
  it('opens once and shares the pending promise between concurrent requests', async () => {
    const a = requestSudo()
    const b = requestSudo()
    expect(a).toBe(b)
    expect(isSudoOpen()).toBe(true)
    settleSudo(true)
    await expect(a).resolves.toBe(true)
    expect(isSudoOpen()).toBe(false)
  })

  it('resolves false on cancel and allows a new request afterwards', async () => {
    const first = requestSudo()
    settleSudo(false)
    await expect(first).resolves.toBe(false)
    const second = requestSudo()
    expect(second).not.toBe(first)
    settleSudo(true)
    await expect(second).resolves.toBe(true)
  })

  it('ignores settle calls without a pending request', () => {
    expect(() => settleSudo(true)).not.toThrow()
    expect(isSudoOpen()).toBe(false)
  })

  it('collects the actions of concurrent requests until the dialog is settled', () => {
    const { result } = renderHook(() => useSudoActions())
    expect(result.current).toEqual([])
    act(() => {
      void requestSudo({ method: 'DELETE', path: '/servers/{id}' })
      void requestSudo({ method: 'POST', path: '/servers/{id}/backups/{snapshot}/restore' })
    })
    expect(result.current).toEqual([
      { method: 'DELETE', path: '/servers/{id}' },
      { method: 'POST', path: '/servers/{id}/backups/{snapshot}/restore' },
    ])
    act(() => settleSudo(false))
    expect(result.current).toEqual([])
  })
})
