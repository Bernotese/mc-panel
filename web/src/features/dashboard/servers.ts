import { queryOptions } from '@tanstack/react-query'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'

export const serversKey = ['servers'] as const

export const serversQueryOptions = queryOptions({
  queryKey: serversKey,
  queryFn: () => unwrap(api.GET('/servers')),
})
