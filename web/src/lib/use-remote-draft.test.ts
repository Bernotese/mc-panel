import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { mergeChangedKeys, useRemoteDraft } from './use-remote-draft'

interface Remote {
  revision: number
  a: string
  b: string
}
type Draft = { a: string; b: string }

const toDraft = (r: Remote): Draft => ({ a: r.a, b: r.b })

function setup(initial: Remote) {
  return renderHook(({ remote }) => useRemoteDraft(remote, { toDraft, merge: mergeChangedKeys }), { initialProps: { remote: initial } })
}

describe('useRemoteDraft', () => {
  it('follows the remote value while the draft is clean', () => {
    const { result, rerender } = setup({ revision: 1, a: 'x', b: 'y' })
    expect(result.current.dirty).toBe(false)
    rerender({ remote: { revision: 2, a: 'x2', b: 'y' } })
    expect(result.current.base.revision).toBe(2)
    expect(result.current.draft).toEqual({ a: 'x2', b: 'y' })
    expect(result.current.start).toEqual({ a: 'x2', b: 'y' })
    expect(result.current.conflict).toBe(false)
  })

  it('takes a bare revision bump over without remounting a clean form', () => {
    const { result, rerender } = setup({ revision: 1, a: 'x', b: 'y' })
    const generation = result.current.generation
    rerender({ remote: { revision: 2, a: 'x', b: 'y' } })
    expect(result.current.base.revision).toBe(2)
    expect(result.current.generation).toBe(generation)
  })

  it('keeps a dirty draft when the remote value changes and reports the conflict', () => {
    const { result, rerender } = setup({ revision: 1, a: 'x', b: 'y' })
    act(() => result.current.setDraft({ a: 'mine', b: 'y' }))
    expect(result.current.dirty).toBe(true)
    rerender({ remote: { revision: 2, a: 'x', b: 'theirs' } })
    expect(result.current.draft).toEqual({ a: 'mine', b: 'y' })
    expect(result.current.base.revision).toBe(1)
    expect(result.current.conflict).toBe(true)
    expect(result.current.remoteDraft).toEqual({ a: 'x', b: 'theirs' })
  })

  it('keeps the base revision of a dirty draft on a bare revision bump, without a conflict', () => {
    const { result, rerender } = setup({ revision: 1, a: 'x', b: 'y' })
    act(() => result.current.setDraft({ a: 'mine', b: 'y' }))
    rerender({ remote: { revision: 2, a: 'x', b: 'y' } })
    expect(result.current.base.revision).toBe(1)
    expect(result.current.conflict).toBe(false)
  })

  it('reload drops the draft and follows the remote value', () => {
    const { result, rerender } = setup({ revision: 1, a: 'x', b: 'y' })
    act(() => result.current.setDraft({ a: 'mine', b: 'y' }))
    rerender({ remote: { revision: 2, a: 'x', b: 'theirs' } })
    const generation = result.current.generation
    act(() => result.current.reload())
    expect(result.current.draft).toEqual({ a: 'x', b: 'theirs' })
    expect(result.current.base.revision).toBe(2)
    expect(result.current.dirty).toBe(false)
    expect(result.current.conflict).toBe(false)
    expect(result.current.generation).toBe(generation + 1)
  })

  it('keepMine puts the own changes onto the remote value', () => {
    const { result, rerender } = setup({ revision: 1, a: 'x', b: 'y' })
    act(() => result.current.setDraft({ a: 'mine', b: 'y' }))
    rerender({ remote: { revision: 2, a: 'x', b: 'theirs' } })
    act(() => result.current.keepMine())
    expect(result.current.draft).toEqual({ a: 'mine', b: 'theirs' })
    expect(result.current.start).toEqual({ a: 'mine', b: 'theirs' })
    expect(result.current.base.revision).toBe(2)
    expect(result.current.dirty).toBe(true)
    expect(result.current.conflict).toBe(false)
  })

  it('saved makes the stored value the new clean baseline', () => {
    const { result, rerender } = setup({ revision: 1, a: 'x', b: 'y' })
    act(() => result.current.setDraft({ a: 'mine', b: 'y' }))
    const saved = { revision: 2, a: 'mine', b: 'y' }
    act(() => result.current.saved(saved))
    rerender({ remote: saved })
    expect(result.current.dirty).toBe(false)
    expect(result.current.base.revision).toBe(2)
    expect(result.current.conflict).toBe(false)
  })
})

describe('useRemoteDraft with a custom comparison', () => {
  it('counts only changes that would save differently', () => {
    const same = (a: Draft, b: Draft) => Number(a.a) === Number(b.a) && a.b === b.b
    const { result } = renderHook(() => useRemoteDraft({ revision: 1, a: '10', b: 'y' }, { toDraft, merge: mergeChangedKeys, same }))
    act(() => result.current.setDraft({ a: '010', b: 'y' }))
    expect(result.current.dirty).toBe(false)
    act(() => result.current.setDraft({ a: '11', b: 'y' }))
    expect(result.current.dirty).toBe(true)
  })
})

describe('mergeChangedKeys', () => {
  it('takes the remote value with the keys the draft changed against the base', () => {
    expect(mergeChangedKeys({ a: 1, b: 1, c: 1 }, { a: 2, b: 1, c: 1 }, { a: 1, b: 3, c: 3 })).toEqual({ a: 2, b: 3, c: 3 })
  })
})
