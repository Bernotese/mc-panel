export const RAIL_STORAGE_KEY = 'mcpanel.rail'

/** Whether the icon rail shows its labels; default collapsed (icons only), and collapsed when storage is unavailable. */
export function readRailExpanded(): boolean {
  try {
    return localStorage.getItem(RAIL_STORAGE_KEY) === 'expanded'
  } catch {
    return false
  }
}

export function writeRailExpanded(expanded: boolean): void {
  try {
    localStorage.setItem(RAIL_STORAGE_KEY, expanded ? 'expanded' : 'collapsed')
  } catch {
    // storage unavailable (private mode) – the choice only lasts for this page view
  }
}
