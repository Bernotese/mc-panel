import { describe, expect, it } from 'vitest'
import { emptyHistory, historyDown, historyUp, MAX_HISTORY, pushHistory } from './history'

describe('command history', () => {
  it('walks back and forth and restores the draft', () => {
    let h = pushHistory(pushHistory(emptyHistory, 'list'), 'say hi')
    let value: string
    ;[h, value] = historyUp(h, 'dra')
    expect(value).toBe('say hi')
    ;[h, value] = historyUp(h, value)
    expect(value).toBe('list')
    ;[h, value] = historyUp(h, value)
    expect(value).toBe('list')
    ;[h, value] = historyDown(h, value)
    expect(value).toBe('say hi')
    ;[h, value] = historyDown(h, value)
    expect(value).toBe('dra')
    expect(h.index).toBeNull()
    ;[h, value] = historyDown(h, 'dra')
    expect(value).toBe('dra')
  })

  it('does nothing without entries', () => {
    expect(historyUp(emptyHistory, 'x')).toEqual([emptyHistory, 'x'])
  })

  it('skips empty commands and direct repetitions', () => {
    const h = pushHistory(pushHistory(pushHistory(emptyHistory, 'list'), 'list'), '')
    expect(h.items).toEqual(['list'])
  })

  it('keeps the last MAX_HISTORY commands', () => {
    let h = emptyHistory
    for (let i = 1; i <= MAX_HISTORY + 5; i++) h = pushHistory(h, `cmd ${i}`)
    expect(h.items).toHaveLength(MAX_HISTORY)
    expect(h.items[0]).toBe('cmd 6')
  })

  it('resets the position after a new command', () => {
    let h = pushHistory(emptyHistory, 'a')
    ;[h] = historyUp(h, '')
    h = pushHistory(h, 'b')
    expect(h).toEqual({ items: ['a', 'b'], index: null, draft: '' })
  })
})
