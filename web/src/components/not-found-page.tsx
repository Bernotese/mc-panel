import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

export function NotFoundPage() {
  const { t } = useTranslation()
  return (
    <main className="flex min-h-[50svh] flex-col items-center justify-center gap-3 p-6 text-center">
      <h1 className="text-2xl font-semibold">{t('not_found.title')}</h1>
      <p className="text-muted-foreground">{t('not_found.description')}</p>
      <Link to="/" className="text-primary underline underline-offset-4">
        {t('not_found.home')}
      </Link>
    </main>
  )
}
