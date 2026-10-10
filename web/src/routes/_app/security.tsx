// web/src/routes/_app/security.tsx
import { createFileRoute } from '@tanstack/react-router'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { SecurityPage } from '@/features/security/security-page'

export const Route = createFileRoute('/_app/security')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: SecurityPage,
})
