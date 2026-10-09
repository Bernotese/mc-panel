// web/src/routes/_app/users/$id.permissions.tsx
import { createFileRoute, notFound } from '@tanstack/react-router'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { UserPermissionsPage } from '@/features/users/permissions-page'

export const Route = createFileRoute('/_app/users/$id/permissions')({
  beforeLoad: async (opts) => {
    await guardBeforeLoad(adminGuard)(opts)
    // `/users/abc/permissions` or `/users/0/permissions` are not user ids – render the 404 page instead of firing requests.
    if (!/^[1-9]\d*$/.test(opts.params.id)) throw notFound()
  },
  component: PermissionsRoute,
})

function PermissionsRoute() {
  const { id } = Route.useParams()
  return <UserPermissionsPage userId={Number(id)} />
}
