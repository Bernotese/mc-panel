export const TREE_STORAGE_KEY = 'mcpanel.tree'

/** Whether the server tree is expanded; default open, and open when storage is unavailable. */
export function readTreeOpen(): boolean {
  try {
    return localStorage.getItem(TREE_STORAGE_KEY) !== 'closed'
  } catch {
    return true
  }
}

export function writeTreeOpen(open: boolean): void {
  try {
    localStorage.setItem(TREE_STORAGE_KEY, open ? 'open' : 'closed')
  } catch {
    // storage unavailable (private mode) – the choice only lasts for this page view
  }
}
