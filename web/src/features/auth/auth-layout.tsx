// web/src/features/auth/auth-layout.tsx
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { PixelIcon } from '@/components/pixel-icon'
import { ThemeToggle } from '@/components/theme-toggle'
import { cn } from '@/lib/utils'

export function AuthLayout({ title, description, wide = false, children }: { title: string; description?: string; wide?: boolean; children: ReactNode }) {
  const { t } = useTranslation()
  return (
    <div className="pixel-pattern relative flex min-h-svh flex-col items-center justify-center px-4 py-12">
      <div className="absolute top-3 right-3">
        <ThemeToggle />
      </div>
      <main id="main" className={cn('w-full', wide ? 'max-w-lg' : 'max-w-md')}>
        <p className="mb-6 flex items-center justify-center gap-3">
          <PixelIcon kind="grass" size={32} />
          <span className="pixel-heading text-foreground text-lg">{t('auth.brand')}</span>
        </p>
        <div className="bg-card text-card-foreground bevel space-y-6 p-6 sm:p-8">
          <div className="space-y-2">
            <h1 className="pixel-heading text-xl">{title}</h1>
            {description ? <p className="text-muted-foreground text-sm">{description}</p> : null}
          </div>
          {children}
        </div>
      </main>
    </div>
  )
}
