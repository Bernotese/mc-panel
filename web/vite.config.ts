/// <reference types="vitest/config" />
import fs from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { defineConfig, type Plugin } from 'vite'

/** The MSW service worker lives in public/ for `npm run dev:mock`, but must never be embedded into the Go binary. */
function stripMswWorker(): Plugin {
  let outDir = ''
  return {
    name: 'mcpanel-strip-msw-worker',
    apply: 'build',
    configResolved(config) {
      outDir = path.resolve(config.root, config.build.outDir)
    },
    closeBundle() {
      fs.rmSync(path.join(outDir, 'mockServiceWorker.js'), { force: true })
    },
  }
}

/** emptyOutDir wipes internal/webui/dist, including the tracked .gitkeep that keeps the go:embed directory in git; recreate it. */
function keepGitkeep(): Plugin {
  let outDir = ''
  return {
    name: 'mcpanel-keep-gitkeep',
    apply: 'build',
    configResolved(config) {
      outDir = path.resolve(config.root, config.build.outDir)
    },
    closeBundle() {
      fs.writeFileSync(path.join(outDir, '.gitkeep'), '')
    },
  }
}

/** A production bundle with the mock layer enabled would silently serve fake data; refuse to build it. */
function forbidMswInBuild(): Plugin {
  return {
    name: 'mcpanel-forbid-msw-build',
    apply: 'build',
    configResolved(config) {
      if (config.mode === 'mock' || config.env.VITE_MSW === '1' || process.env.VITE_MSW === '1') {
        throw new Error('Refusing to build: VITE_MSW / mode "mock" would bundle the MSW mock layer into the production UI.')
      }
    },
  }
}

export default defineConfig({
  plugins: [tanstackRouter({ target: 'react', autoCodeSplitting: true, routeFileIgnorePattern: '\\.test\\.' }), react(), tailwindcss(), forbidMswInBuild(), stripMswWorker(), keepGitkeep()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false } },
  },
  build: { outDir: '../internal/webui/dist', emptyOutDir: true, sourcemap: false },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    exclude: ['e2e/**', 'node_modules/**'],
    // The tests run on a shared VM (CI runners and make test-vm); under load the default 5 s
    // made slow render tests flaky.
    testTimeout: 15000,
    hookTimeout: 15000,
  },
})
