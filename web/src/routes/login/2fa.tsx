// web/src/routes/login/2fa.tsx
import { createFileRoute } from '@tanstack/react-router'
import { secondFactorGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { SecondFactorPage } from '@/features/auth/second-factor-page'

export const Route = createFileRoute('/login/2fa')({
  beforeLoad: guardBeforeLoad(secondFactorGuard),
  component: SecondFactorPage,
})
