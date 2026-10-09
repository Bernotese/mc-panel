/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [
    tanstackRouter({ target: 'react', autoCodeSplitting: true, routeFileIgnorePattern: '\\.test\\.' }),
    react(),
    tailwindcss(),
  ],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { '/api': { target: 'http://127.0.0.1:8080', changeOrigin: false } },
  },
  build: { outDir: '../internal/webui/dist', emptyOutDir: true, sourcemap: false },
  test: { environment: 'jsdom', globals: true, setupFiles: ['./src/test/setup.ts'], exclude: ['e2e/**', 'node_modules/**'] },
})
