import type { CSSProperties } from 'react'
import { Toaster } from 'sonner'
import { useThemePreference } from '@/lib/theme'

// Square toasts with the block bevel; richColors keeps success/error distinguishable.
const STYLE = { '--border-radius': 'var(--radius)' } as CSSProperties

export function AppToaster() {
  const pref = useThemePreference()
  return (
    <Toaster
      theme={pref}
      richColors
      closeButton
      position="top-right"
      style={STYLE}
      toastOptions={{ className: 'bevel! font-sans', classNames: { title: 'font-semibold', closeButton: 'rounded-none!' } }}
    />
  )
}
