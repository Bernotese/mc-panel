import { defineConfig, devices } from '@playwright/test'

const MOCK_PORT = 5174
const realBaseURL = process.env.E2E_BASE_URL

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: { locale: 'de-DE', trace: 'retain-on-failure' },
  projects: [
    {
      name: 'mock',
      testMatch: /(?<![\w-])(totp|smoke|servers|plugins|backups|monitoring|web-port)\.spec\.ts$/,
      use: { ...devices['Desktop Chrome'], baseURL: `http://localhost:${MOCK_PORT}`, locale: 'de-DE' },
    },
    {
      // Visual review only (make test-vm SUITE=shots); never part of CI or `npm run e2e`.
      name: 'screenshots',
      testMatch: /\.shots\.ts$/,
      use: { ...devices['Desktop Chrome'], baseURL: `http://localhost:${MOCK_PORT}`, locale: 'de-DE' },
    },
    ...(realBaseURL
      ? [
          {
            name: 'real',
            testMatch: /login-totp\.spec\.ts$/,
            // The spec needs a fresh admin without 2FA, so a retry could only fail on a different screen and hide the cause.
            retries: 0,
            use: { ...devices['Desktop Chrome'], baseURL: realBaseURL, locale: 'de-DE' },
          },
        ]
      : []),
  ],
  webServer: {
    command: `npx vite --mode mock --port ${MOCK_PORT} --strictPort`,
    url: `http://localhost:${MOCK_PORT}`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
})
