export const HIDE_INSTALLED_STORAGE_KEY = 'mcpanel.plugins.hideInstalled'

/** "Installierte ausblenden" in Entdecken; default on (one usually looks for something new), and on without storage. */
export function readHideInstalled(): boolean {
  try {
    return localStorage.getItem(HIDE_INSTALLED_STORAGE_KEY) !== 'off'
  } catch {
    return true
  }
}

export function writeHideInstalled(hide: boolean): void {
  try {
    localStorage.setItem(HIDE_INSTALLED_STORAGE_KEY, hide ? 'on' : 'off')
  } catch {
    // storage unavailable (private mode) – the choice only lasts for this page view
  }
}
