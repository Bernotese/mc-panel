import { describe, expect, it } from 'vitest'
import { setPrefersDark } from '@/test/media'
import { applyTheme, getStoredTheme, nextTheme, resolveTheme, setTheme, THEME_STORAGE_KEY, watchSystemTheme } from './theme'

describe('theme', () => {
  it('defaults to system and ignores garbage in localStorage', () => {
    expect(getStoredTheme()).toBe('system')
    localStorage.setItem(THEME_STORAGE_KEY, 'neon')
    expect(getStoredTheme()).toBe('system')
  })

  it('resolves system preference via prefers-color-scheme', () => {
    setPrefersDark(true)
    expect(resolveTheme('system')).toBe('dark')
    setPrefersDark(false)
    expect(resolveTheme('system')).toBe('light')
    expect(resolveTheme('dark')).toBe('dark')
  })

  it('persists the choice under mcpanel.theme and toggles the dark class', () => {
    setTheme('dark')
    expect(localStorage.getItem('mcpanel.theme')).toBe('dark')
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    setTheme('light')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })

  it('follows the OS while set to system', () => {
    setTheme('system')
    const stop = watchSystemTheme()
    setPrefersDark(true)
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    setPrefersDark(false)
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    stop()
    applyTheme('light')
  })

  it('cycles light → dark → system → light', () => {
    expect(nextTheme('light')).toBe('dark')
    expect(nextTheme('dark')).toBe('system')
    expect(nextTheme('system')).toBe('light')
  })
})
