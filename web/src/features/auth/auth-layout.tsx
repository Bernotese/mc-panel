// web/src/features/auth/auth-layout.tsx
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ThemeToggle } from '@/components/theme-toggle'
import { Card, CardContent, CardDescription, CardHeader } from '@/components/ui/card'
import { cn } from '@/lib/utils'

export function AuthLayout({ title, description, wide = false, children }: { title: string; description?: string; wide?: boolean; children: ReactNode }) {
  const { t } = useTranslation()
  return (
    <div className="bg-muted/40 relative flex min-h-svh flex-col items-center justify-center px-4 py-10">
      <div className="absolute top-3 right-3">
        <ThemeToggle />
      </div>
      <main id="main" className={cn('w-full', wide ? 'max-w-lg' : 'max-w-sm')}>
        <p className="mb-4 text-center text-lg font-semibold tracking-tight">{t('auth.brand')}</p>
        <Card>
          <CardHeader>
            <h1 className="text-xl font-semibold">{title}</h1>
            {description ? <CardDescription>{description}</CardDescription> : null}
          </CardHeader>
          <CardContent>{children}</CardContent>
        </Card>
      </main>
    </div>
  )
}
