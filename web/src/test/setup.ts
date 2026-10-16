import '@testing-library/jest-dom/vitest'
import { cleanup, configure } from '@testing-library/react'
import { afterAll, afterEach, beforeAll, vi } from 'vitest'
import { setCsrfToken } from '@/api/csrf'
import { resetApiHandlers } from '@/api/handlers'
import { resetSudoStore } from '@/features/sudo/sudo-store'
import i18n, { i18nReady } from '@/i18n'
import { resetDb } from '@/mocks/db'
import { server } from '@/mocks/node'
import { installMatchMedia, setPrefersDark, setViewport } from './media'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
globalThis.ResizeObserver ??= ResizeObserverStub as unknown as typeof ResizeObserver
Element.prototype.scrollIntoView ??= function scrollIntoView() {}
Element.prototype.hasPointerCapture ??= () => false
Element.prototype.releasePointerCapture ??= () => {}
// jsdom only reports "Not implemented" for scrollTo; the router calls it on navigation.
window.scrollTo = vi.fn() as unknown as typeof window.scrollTo

// Like testTimeout in vite.config.ts: on the shared VM a render under load can miss the default
// 1 s of findBy/waitFor. Passing tests do not wait longer; only failures report later.
configure({ asyncUtilTimeout: 5000 })

installMatchMedia()
await i18nReady
await i18n.changeLanguage('de')

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(async () => {
  cleanup()
  server.resetHandlers()
  resetDb()
  resetApiHandlers()
  resetSudoStore()
  setCsrfToken('')
  setViewport('desktop')
  setPrefersDark(false)
  localStorage.clear()
  await i18n.changeLanguage('de')
})
afterAll(() => server.close())
