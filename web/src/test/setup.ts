import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'
import i18n, { i18nReady } from '@/i18n'
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

afterEach(async () => {
  cleanup()
  setViewport('desktop')
  setPrefersDark(false)
  localStorage.clear()
  await i18n.changeLanguage('de')
})
