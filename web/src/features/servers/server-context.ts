import { createContext, useContext } from 'react'
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

/** Provided by ServerLayout for the header and every section route. */
export const ServerContext = createContext<ServerContextValue | null>(null)

export function useServerContext(): ServerContextValue {
  const value = useContext(ServerContext)
  if (!value) throw new Error('useServerContext outside of ServerLayout')
  return value
}
