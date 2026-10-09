// web/src/features/auth/logout-button.tsx
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { useLogout } from './use-logout'

export function LogoutButton({ label }: { label?: string }) {
  const { t } = useTranslation()
  const logout = useLogout()
  return (
    <Button type="button" variant="link" disabled={logout.isPending} onClick={() => logout.mutate()}>
      {label ?? t('auth.logout')}
    </Button>
  )
}
