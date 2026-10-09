// web/src/routes/login/index.tsx
import { createFileRoute } from '@tanstack/react-router'
import { loginGuard } from '@/features/auth/guards'
import { LoginPage } from '@/features/auth/login-page'
import { guardBeforeLoad } from '@/features/auth/route-guards'

export const Route = createFileRoute('/login/')({
  beforeLoad: guardBeforeLoad(loginGuard),
  component: LoginPage,
})
