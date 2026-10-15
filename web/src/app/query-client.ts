// web/src/app/query-client.ts
import { QueryClient, timeoutManager } from '@tanstack/react-query'
import { backgroundTimeoutProvider } from '@/api/background'
import { ApiError } from '@/api/errors'

export function createQueryClient(): QueryClient {
  // Polls send X-MCPanel-Background (SEC-19); set before any query schedules a timer.
  timeoutManager.setTimeoutProvider(backgroundTimeoutProvider)
  return new QueryClient({
    defaultOptions: {
      queries: {
        // 4xx never get better by retrying; 5xx/network get two more attempts.
        retry: (failureCount, error) => !(error instanceof ApiError && error.status < 500) && failureCount < 2,
        // Stays off: a focus refetch would extend the session without the background header (api/background.ts).
        refetchOnWindowFocus: false,
        staleTime: 15_000,
      },
      mutations: { retry: false },
    },
  })
}
