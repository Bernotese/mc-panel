// web/src/app/query-client.ts
import { QueryClient } from '@tanstack/react-query'
import { ApiError } from '@/api/errors'

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // 4xx never get better by retrying; 5xx/network get two more attempts.
        retry: (failureCount, error) => !(error instanceof ApiError && error.status < 500) && failureCount < 2,
        refetchOnWindowFocus: false,
        staleTime: 15_000,
      },
      mutations: { retry: false },
    },
  })
}
