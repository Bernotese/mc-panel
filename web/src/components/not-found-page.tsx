import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { PixelIcon } from '@/components/pixel-icon'

export function NotFoundPage() {
  const { t } = useTranslation()
  return (
    <main className="flex min-h-[50svh] flex-col items-center justify-center gap-4 p-6 text-center">
      <PixelIcon kind="grass" size={48} />
      <h1 className="pixel-heading text-2xl">{t('not_found.title')}</h1>
      <p className="text-muted-foreground">{t('not_found.description')}</p>
      <Link to="/" className="text-primary underline underline-offset-4">
        {t('not_found.home')}
      </Link>
    </main>
  )
}
