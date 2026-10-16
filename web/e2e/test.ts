import { test as base, expect } from '@playwright/test'

/** Reports that only the mock backend causes: MSW registers its service worker with a plain string URL. */
const MOCK_ONLY = new Set(['report require-trusted-types-for: ServiceWorkerContainer register|/mockServiceWorker.js'])

/**
 * SEC-31/SEC-43: every e2e test fails on a CSP violation, enforced or report-only (Trusted Types). The dev server
 * and the panel send the same policy (vite.config.ts, internal/httpx). Specs import `test` from here, never from
 * @playwright/test directly (eslint).
 */
export const test = base.extend<{ cspGuard: void }>({
  cspGuard: [
    async ({ page }, use) => {
      const violations: string[] = []
      await page.exposeFunction('__mcpanelCspViolation', (key: string, where: string) => {
        if (!MOCK_ONLY.has(key)) violations.push(`${key} (${where})`)
      })
      await page.addInitScript(() => {
        document.addEventListener('securitypolicyviolation', (e) => {
          const report = (window as unknown as { __mcpanelCspViolation(key: string, where: string): Promise<void> }).__mcpanelCspViolation
          void report(`${e.disposition} ${e.effectiveDirective}: ${e.sample || e.blockedURI}`, `${e.blockedURI} at ${e.sourceFile}:${e.lineNumber}`)
        })
      })
      await use()
      expect(violations, 'CSP violations').toEqual([])
    },
    { auto: true },
  ],
})

export { expect }
export type { Page } from '@playwright/test'
