import { useMutation } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { Locale } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Section } from '@/components/section'
import { Label } from '@/components/ui/label'
import { useMe } from '@/features/auth/me'
import { SUPPORTED_LOCALES } from '@/i18n'
import { useErrorToast } from '@/lib/error-message'

export function LanguageCard() {
  const { t } = useTranslation()
  const id = useId()
  const me = useMe()
  const notifyError = useErrorToast()
  // PATCH /me returns Me; the API middleware applies it (cache + i18n language + <html lang>).
  const update = useMutation({
    mutationFn: (locale: Locale) => unwrap(api.PATCH('/me', { body: { locale } })),
    onSuccess: () => toast.success(t('profile.language.saved')),
    onError: notifyError,
  })
  return (
    <Section title={t('profile.language.title')}>
      <div className="space-y-2">
        <Label htmlFor={id}>{t('profile.language.label')}</Label>
        <NativeSelect
          id={id}
          className="w-full"
          value={me?.locale ?? 'de'}
          disabled={update.isPending}
          onChange={(e) => update.mutate(e.target.value as Locale)}
        >
          {SUPPORTED_LOCALES.map((locale) => (
            <option key={locale} value={locale}>
              {t(`profile.language.${locale}`)}
            </option>
          ))}
        </NativeSelect>
      </div>
    </Section>
  )
}
