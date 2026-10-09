/// <reference types="vitest/config" />
import fs from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

const outDir = path.resolve(import.meta.dirname, '../internal/webui/dist')

/** The MSW service worker lives in public/ for `npm run dev:mock`, but must never be embedded into the Go binary. */
function stripMswWorker(): Plugin {
  return {
    name: 'mcpanel-strip-msw-worker',
    apply: 'build',
    closeBundle() {
      fs.rmSync(path.join(outDir, 'mockServiceWorker.js'), { force: true })
    },
  }
}

export default defineConfig({
  plugins: [tanstackRouter({ target: 'react', autoCodeSplitting: true, routeFileIgnorePattern: '\\.test\\.' }), react(), tailwindcss(), stripMswWorker()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false } },
    // tests import ../api/openapi.yaml?raw (operationId coverage)
    fs: { allow: ['..'] },
  },
  build: { outDir, emptyOutDir: true, sourcemap: false },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    exclude: ['e2e/**', 'node_modules/**'],
  },
})
