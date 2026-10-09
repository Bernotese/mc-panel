// web/src/routes/_app/users/$id.permissions.tsx
import { createFileRoute } from '@tanstack/react-router'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { UserPermissionsPage } from '@/features/users/permissions-page'

export const Route = createFileRoute('/_app/users/$id/permissions')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: PermissionsRoute,
})

function PermissionsRoute() {
  const { id } = Route.useParams()
  return <UserPermissionsPage userId={Number(id)} />
}
