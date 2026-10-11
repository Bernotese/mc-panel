export const MAX_HISTORY = 100

/** index = position while browsing with ↑/↓ (null = editing the draft). */
export interface HistoryState {
  readonly items: readonly string[]
  readonly index: number | null
  readonly draft: string
}

export const emptyHistory: HistoryState = { items: [], index: null, draft: '' }

export function pushHistory(h: HistoryState, command: string): HistoryState {
  if (!command) return { ...h, index: null, draft: '' }
  const items = h.items.at(-1) === command ? h.items : [...h.items, command].slice(-MAX_HISTORY)
  return { items, index: null, draft: '' }
}

export function historyUp(h: HistoryState, current: string): [HistoryState, string] {
  if (h.items.length === 0) return [h, current]
  if (h.index === null) {
    const index = h.items.length - 1
    return [{ ...h, index, draft: current }, h.items[index]]
  }
  const index = Math.max(0, h.index - 1)
  return [{ ...h, index }, h.items[index]]
}

export function historyDown(h: HistoryState, current: string): [HistoryState, string] {
  if (h.index === null) return [h, current]
  if (h.index >= h.items.length - 1) return [{ ...h, index: null, draft: '' }, h.draft]
  const index = h.index + 1
  return [{ ...h, index }, h.items[index]]
}
