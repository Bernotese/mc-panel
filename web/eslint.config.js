import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  globalIgnores(['dist', 'public/mockServiceWorker.js', 'src/routeTree.gen.ts', 'src/api/schema.d.ts']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      globals: globals.browser,
    },
  },
  {
    // shadcn components and TanStack route files legitimately co-export non-components
    files: ['src/components/ui/**', 'src/routes/**'],
    rules: { 'react-refresh/only-export-components': 'off' },
  },
  {
    // e2e/test.ts adds the CSP violation guard (SEC-31); a spec importing Playwright's own `test` would skip it.
    files: ['e2e/**/*.spec.ts', 'e2e/**/*.shots.ts'],
    rules: {
      'no-restricted-imports': [
        'error',
        { paths: [{ name: '@playwright/test', importNames: ['test'], message: "Import test from e2e/test.ts (CSP guard)." }] },
      ],
    },
  },
])
