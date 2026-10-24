import { useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Section } from '@/components/section'
import { PasswordChangeForm } from '@/features/auth/password-change-form'

export function PasswordCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  return (
    <Section title={t('profile.password.title')} description={t('profile.password.hint')}>
      <PasswordChangeForm
        submitLabel={t('profile.password.submit')}
        onChanged={() => {
          toast.success(t('profile.password.changed'))
          void queryClient.invalidateQueries({ queryKey: ['sessions'] })
        }}
      />
    </Section>
  )
}
