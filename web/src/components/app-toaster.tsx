import { Toaster } from 'sonner'
import { useThemePreference } from '@/lib/theme'

export function AppToaster() {
  const pref = useThemePreference()
  return <Toaster theme={pref} richColors closeButton position="top-right" />
}
