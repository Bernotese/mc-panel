import { createFileRoute } from '@tanstack/react-router'
import { AuditPage } from '@/features/audit/audit-page'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'

export const Route = createFileRoute('/_app/audit')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: AuditPage,
})
