import { createContext, useContext, type ReactNode } from 'react'
import type { Server } from '@/api/types'
import type { ServerSection } from './sections'

export interface ServerContextValue {
  server: Server
  isAdmin: boolean
  /** A zip extraction runs: start/restart are locked (only known to users with server.files). */
  extracting: boolean
  /** Allowed sections from serverSections(): tree, drawer and route guards agree. */
  sections: ServerSection[]
}

const Ctx = createContext<ServerContextValue | null>(null)

export function ServerContextProvider({ value, children }: { value: ServerContextValue; children: ReactNode }) {
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useServerContext(): ServerContextValue {
  const value = useContext(Ctx)
  if (!value) throw new Error('useServerContext outside of ServerLayout')
  return value
}
