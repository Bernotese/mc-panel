import { useSyncExternalStore } from 'react'

let pending: Promise<boolean> | null = null
let resolvePending: ((confirmed: boolean) => void) | null = null
const listeners = new Set<() => void>()

function emit(): void {
  listeners.forEach((l) => l())
}

/** Opens the global sudo dialog. Concurrent callers share one dialog and one answer. */
export function requestSudo(): Promise<boolean> {
  if (pending) return pending
  pending = new Promise<boolean>((resolve) => {
    resolvePending = resolve
  })
  emit()
  return pending
}

export function settleSudo(confirmed: boolean): void {
  const resolve = resolvePending
  if (!resolve) return
  pending = null
  resolvePending = null
  emit()
  resolve(confirmed)
}

export function isSudoOpen(): boolean {
  return pending !== null
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function useSudoOpen(): boolean {
  return useSyncExternalStore(subscribe, isSudoOpen, () => false)
}

export function resetSudoStore(): void {
  settleSudo(false)
}
