// web/src/routes/change-password.tsx
import { createFileRoute } from '@tanstack/react-router'
import { ChangePasswordPage } from '@/features/auth/change-password-page'
import { changePasswordGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'

export const Route = createFileRoute('/change-password')({
  beforeLoad: guardBeforeLoad(changePasswordGuard),
  component: ChangePasswordPage,
})
