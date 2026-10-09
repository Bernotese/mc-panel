import { useSyncExternalStore } from 'react'

export type ThemePreference = 'light' | 'dark' | 'system'
export const THEME_STORAGE_KEY = 'mcpanel.theme'
const DARK_QUERY = '(prefers-color-scheme: dark)'
const listeners = new Set<() => void>()

export function getStoredTheme(): ThemePreference {
  try {
    const value = localStorage.getItem(THEME_STORAGE_KEY)
    return value === 'light' || value === 'dark' || value === 'system' ? value : 'system'
  } catch {
    return 'system'
  }
}

export function resolveTheme(pref: ThemePreference): 'light' | 'dark' {
  if (pref !== 'system') return pref
  return window.matchMedia(DARK_QUERY).matches ? 'dark' : 'light'
}

/** Applies the class strategy on <html>. Called in main.tsx before the first render (no inline script, CSP). */
export function applyTheme(pref: ThemePreference = getStoredTheme()): void {
  const resolved = resolveTheme(pref)
  const root = document.documentElement
  root.classList.toggle('dark', resolved === 'dark')
  root.style.colorScheme = resolved
}

export function setTheme(pref: ThemePreference): void {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, pref)
  } catch {
    // storage unavailable (private mode) – the choice only lasts for this page view
  }
  applyTheme(pref)
  listeners.forEach((l) => l())
}

export function nextTheme(pref: ThemePreference): ThemePreference {
  if (pref === 'light') return 'dark'
  if (pref === 'dark') return 'system'
  return 'light'
}

export function watchSystemTheme(): () => void {
  const mql = window.matchMedia(DARK_QUERY)
  const onChange = () => {
    if (getStoredTheme() === 'system') applyTheme('system')
  }
  mql.addEventListener('change', onChange)
  return () => mql.removeEventListener('change', onChange)
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function useThemePreference(): ThemePreference {
  return useSyncExternalStore(subscribe, getStoredTheme, () => 'system')
}
