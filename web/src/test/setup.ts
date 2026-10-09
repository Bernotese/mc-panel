import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll } from 'vitest'
import { setCsrfToken } from '@/api/csrf'
import { resetApiHandlers } from '@/api/handlers'
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

installMatchMedia()
await i18nReady
await i18n.changeLanguage('de')

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(async () => {
  cleanup()
  server.resetHandlers()
  resetDb()
  resetApiHandlers()
  setCsrfToken('')
  setViewport('desktop')
  setPrefersDark(false)
  localStorage.clear()
  await i18n.changeLanguage('de')
})
afterAll(() => server.close())
