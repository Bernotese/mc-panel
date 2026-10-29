// web/src/mocks/browser.ts
import { setupWorker } from 'msw/browser'
import { seedDeletionScenario, seedRecoveryScenario } from './db'
import { handlers } from './handlers'

// The browser mock shows a server with a stuck restore too (data safety spec §2.3); unit tests seed it where needed.
seedRecoveryScenario()
// Deletions that failed or are partial (#148), for the admin's deletions views.
seedDeletionScenario()

export const worker = setupWorker(...handlers)
