import { useSyncExternalStore } from 'react'
import type { SudoAction } from './sudo-actions'

const NO_ACTIONS: readonly SudoAction[] = []
let pending: Promise<boolean> | null = null
let actions: readonly SudoAction[] = NO_ACTIONS
let resolvePending: ((confirmed: boolean) => void) | null = null
const listeners = new Set<() => void>()

function emit(): void {
  listeners.forEach((l) => l())
}

/**
 * Opens the global sudo dialog for the action that needs it. Concurrent callers share one dialog and one answer;
 * the dialog lists all of their actions.
 */
export function requestSudo(action?: SudoAction): Promise<boolean> {
  if (action) actions = [...actions, action]
  pending ??= new Promise<boolean>((resolve) => {
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
  actions = NO_ACTIONS
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

/** The actions waiting for the open dialog, in request order. */
export function useSudoActions(): readonly SudoAction[] {
  return useSyncExternalStore(subscribe, () => actions, () => NO_ACTIONS)
}

export function resetSudoStore(): void {
  settleSudo(false)
  actions = NO_ACTIONS
}
