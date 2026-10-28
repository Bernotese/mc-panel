import { useState } from 'react'

function sameJSON(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}

/** The remote value with every top-level key the draft changed against the base (the own change wins). */
export function mergeChangedKeys<D extends object>(base: D, draft: D, remote: D): D {
  const out = { ...remote }
  for (const key of Object.keys(draft) as (keyof D)[]) if (!sameJSON(draft[key], base[key])) out[key] = draft[key]
  return out
}

export interface RemoteDraftOptions<R, D> {
  /** The form value of a remote value. */
  toDraft(remote: R): D
  /** keepMine: the own changes (draft against base) on top of the remote value. */
  merge(base: D, draft: D, remote: D): D
  /** Whether two form values would save the same (default: equal JSON); decides dirty and conflict. */
  same?(a: D, b: D): boolean
}

export interface RemoteDraft<R, D> {
  /** The remote value the draft is based on (its revision is the one to save onto). */
  base: R
  /** The form value of the base. */
  baseDraft: D
  draft: D
  /** The draft this generation of the form started with: uncontrolled forms take it as their initial value. */
  start: D
  /** Changes whenever the draft is replaced from outside: key uncontrolled forms with it. */
  generation: number
  dirty: boolean
  /** The draft is dirty and the remote value now differs from the base in what the form shows. */
  conflict: boolean
  remoteDraft: D
  setDraft(draft: D | ((draft: D) => D)): void
  /** Drop the draft and follow the remote value. */
  reload(): void
  /** Rebase the draft onto the remote value: the own changes stay, everything else follows the remote. */
  keepMine(): void
  /** After a successful save: the stored value is the new clean baseline. */
  saved(stored: R): void
}

interface State<R, D> {
  base: R
  draft: D
  start: D
  generation: number
}

/**
 * Issue #154: a form draft against a polled remote value. A clean draft follows every remote change (a bare
 * revision bump without remounting the form); a dirty one keeps its base, so the save goes onto the revision it was
 * edited against, and reports a conflict as soon as the remote value differs in what the form shows.
 */
export function useRemoteDraft<R, D>(remote: R, { toDraft, merge, same = sameJSON }: RemoteDraftOptions<R, D>): RemoteDraft<R, D> {
  const [state, setState] = useState<State<R, D>>(() => {
    const draft = toDraft(remote)
    return { base: remote, draft, start: draft, generation: 0 }
  })
  const baseDraft = toDraft(state.base)
  const dirty = !same(state.draft, baseDraft)
  const remoteDraft = toDraft(remote)

  // Following the remote value while clean: state derived from props, adjusted during render (no effect, no flash).
  if (!dirty && !sameJSON(remote, state.base)) {
    const remount = !sameJSON(remoteDraft, state.draft)
    setState({
      base: remote,
      draft: remoteDraft,
      start: remount ? remoteDraft : state.start,
      generation: remount ? state.generation + 1 : state.generation,
    })
  }

  const replace = (base: R, draft: D) => setState((s) => ({ base, draft, start: draft, generation: s.generation + 1 }))
  return {
    base: state.base,
    baseDraft,
    draft: state.draft,
    start: state.start,
    generation: state.generation,
    dirty,
    conflict: dirty && !same(remoteDraft, baseDraft),
    remoteDraft,
    setDraft: (draft) => setState((s) => ({ ...s, draft: typeof draft === 'function' ? (draft as (d: D) => D)(s.draft) : draft })),
    reload: () => replace(remote, remoteDraft),
    keepMine: () => replace(remote, merge(baseDraft, state.draft, remoteDraft)),
    saved: (stored) => replace(stored, toDraft(stored)),
  }
}
