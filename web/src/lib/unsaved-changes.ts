import { createContext, useContext, useEffect, useId } from 'react'

/** Which forms of an area hold unsaved changes; read when a navigation or unload starts, so no re-render is needed. */
export interface UnsavedRegistry {
  set(id: string, dirty: boolean): void
  delete(id: string): void
  any(): boolean
}

export function createUnsavedRegistry(): UnsavedRegistry {
  const dirty = new Map<string, boolean>()
  return {
    set: (id, value) => void dirty.set(id, value),
    delete: (id) => void dirty.delete(id),
    any: () => [...dirty.values()].some(Boolean),
  }
}

export const UnsavedContext = createContext<UnsavedRegistry | null>(null)

/** Issue #154: a form reports its unsaved changes to the guard of its area (if there is one). */
export function useReportUnsaved(dirty: boolean): void {
  const registry = useContext(UnsavedContext)
  const id = useId()
  useEffect(() => {
    registry?.set(id, dirty)
  }, [registry, id, dirty])
  useEffect(() => () => registry?.delete(id), [registry, id])
}
