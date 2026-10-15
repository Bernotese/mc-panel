/// <reference types="vitest/config" />
import { randomBytes } from 'node:crypto'
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

/** The panel's CSP (CSP in internal/httpx/middleware.go; a Go test in internal/webui keeps both equal). */
const cspPolicy = (nonce: string) =>
  `default-src 'self'; script-src 'self'; style-src 'self' 'nonce-${nonce}'; img-src 'self'; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`
const CSP_REPORT_ONLY = "require-trusted-types-for 'script'; trusted-types 'none'"

/** The panel replaces this in index.html with a fresh nonce per response (internal/webui). */
const NONCE_PLACEHOLDER = 'MCPANEL_CSP_NONCE'

/**
 * The dev server (make dev, the e2e mock) sends the panel's CSP, so a violation shows up before a release.
 * Only the react-refresh preamble, an inline script that exists only in dev, needs the nonce in script-src.
 */
function devCsp(nonce: string): Plugin {
  const policy = cspPolicy(nonce).replace("script-src 'self'", `script-src 'self' 'nonce-${nonce}'`)
  return {
    name: 'mcpanel-dev-csp',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use((_req, res, next) => {
        res.setHeader('Content-Security-Policy', policy)
        res.setHeader('Content-Security-Policy-Report-Only', CSP_REPORT_ONLY)
        next()
      })
    },
  }
}

/**
 * sonner 2 inserts its stylesheet as an inline <style> without a nonce when the module loads. main.tsx imports
 * the same stylesheet from sonner/dist/styles.css instead, so this turns the insertion into a no-op. Fails the
 * build if sonner changes the function, rather than shipping a CSP violation.
 */
function sonnerStaticStyles(): Plugin {
  const fn = 'function __insertCSS(code) {'
  return {
    name: 'mcpanel-sonner-static-styles',
    enforce: 'pre',
    transform(code, id) {
      // The dev server imports it with a ?v=<hash> query.
      if (!/[\\/]node_modules[\\/]sonner[\\/]dist[\\/]index\.m?js(\?|$)/.test(id)) return null
      if (!code.includes(fn)) this.error(`sonner no longer defines "${fn}"; check how it injects its styles (CSP style-src)`)
      return { code: code.replace(fn, `${fn} return;`), map: null }
    },
  }
}

export default defineConfig(({ command }) => {
  const nonce = command === 'build' ? NONCE_PLACEHOLDER : randomBytes(16).toString('base64')
  return {
    plugins: [tanstackRouter({ target: 'react', autoCodeSplitting: true, routeFileIgnorePattern: '\\.test\\.' }), react(), tailwindcss(), forbidMswInBuild(), stripMswWorker(), keepGitkeep(), devCsp(nonce), sonnerStaticStyles()],
    html: { cspNonce: nonce },
    // Served through the transform pipeline (not pre-bundled), so sonnerStaticStyles applies in dev too.
    optimizeDeps: { exclude: ['sonner'] },
    resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
    server: {
      port: 5173,
      strictPort: true,
      proxy: { '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false } },
    },
    // No data: URIs (CSP font-src/img-src 'self'): small font subsets would otherwise be inlined into the CSS.
    build: { outDir: '../internal/webui/dist', emptyOutDir: true, sourcemap: false, assetsInlineLimit: 0 },
    test: {
      environment: 'jsdom',
      globals: true,
      setupFiles: ['./src/test/setup.ts'],
      exclude: ['e2e/**', 'node_modules/**'],
    },
  }
})
