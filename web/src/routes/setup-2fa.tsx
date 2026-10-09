// web/src/routes/setup-2fa.tsx
import { createFileRoute } from '@tanstack/react-router'
import { setupGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { Setup2faPage } from '@/features/setup/setup-2fa-page'

export const Route = createFileRoute('/setup-2fa')({
  beforeLoad: guardBeforeLoad(setupGuard),
  component: Setup2faPage,
})
