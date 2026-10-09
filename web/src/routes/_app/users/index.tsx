// web/src/routes/_app/users/index.tsx
import { createFileRoute } from '@tanstack/react-router'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { UsersPage } from '@/features/users/users-page'

export const Route = createFileRoute('/_app/users/')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: UsersPage,
})
