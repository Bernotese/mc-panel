// web/src/mocks/browser.ts
import { setupWorker } from 'msw/browser'
import { seedRecoveryScenario } from './db'
import { handlers } from './handlers'

// The browser mock shows a server with a stuck restore too (data safety spec §2.3); unit tests seed it where needed.
seedRecoveryScenario()

export const worker = setupWorker(...handlers)
