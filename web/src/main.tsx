// web/src/main.tsx
import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createQueryClient } from './app/query-client'
import { routerNavigator, wireApi } from './app/wire-api'
import { i18nReady } from './i18n'
import { applyTheme, watchSystemTheme } from './lib/theme'
import { createAppRouter } from './router'
import './index.css'

// Theme before the first paint – no inline script in index.html (CSP script-src 'self').
applyTheme()
watchSystemTheme()

async function enableMocking(): Promise<void> {
  if (import.meta.env.VITE_MSW !== '1') return
  const { worker } = await import('./mocks/browser')
  await worker.start({ onUnhandledRequest: 'bypass' })
}

const queryClient = createQueryClient()
const router = createAppRouter(queryClient)
wireApi(queryClient, routerNavigator(router))

void Promise.all([enableMocking(), i18nReady]).then(() => {
  const root = document.getElementById('root')
  if (!root) throw new Error('#root missing in index.html')
  createRoot(root).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </StrictMode>,
  )
})
