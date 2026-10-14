import { useRouter, type ErrorComponentProps } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { PixelIcon } from '@/components/pixel-icon'
import { Button } from '@/components/ui/button'
import { errorMessage } from '@/lib/error-message'

/** Replaces TanStack's untranslated default error screen (guards can fail with jailed/ip_blocked/5xx/network). */
export function RouteError({ error, reset }: ErrorComponentProps) {
  const { t } = useTranslation()
  const router = useRouter()
  return (
    <main className="mx-auto flex min-h-svh max-w-md flex-col items-center justify-center gap-4 p-6 text-center">
      <PixelIcon kind="grass" size={48} />
      <h1 className="pixel-heading text-xl">{t('errors.title')}</h1>
      <p role="alert" className="text-muted-foreground text-sm">
        {errorMessage(error, t)}
      </p>
      <Button
        type="button"
        onClick={() => {
          reset()
          void router.invalidate()
        }}
      >
        {t('common.retry')}
      </Button>
    </main>
  )
}
