import { Monitor, Moon, Sun } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { nextTheme, setTheme, useThemePreference } from '@/lib/theme'

export function ThemeToggle() {
  const { t } = useTranslation()
  const pref = useThemePreference()
  const Icon = pref === 'dark' ? Moon : pref === 'light' ? Sun : Monitor
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      aria-label={t('theme.toggle', { current: t(`theme.${pref}`) })}
      title={t('theme.toggle', { current: t(`theme.${pref}`) })}
      onClick={() => setTheme(nextTheme(pref))}
    >
      <Icon className="size-4" aria-hidden="true" />
    </Button>
  )
}
