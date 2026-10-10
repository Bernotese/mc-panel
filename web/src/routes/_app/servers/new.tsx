import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { adminGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { CreateServerPage } from '@/features/servers/create-server-page'

export const Route = createFileRoute('/_app/servers/new')({
  beforeLoad: guardBeforeLoad(adminGuard),
  component: NewServerRoute,
})

function NewServerRoute() {
  const navigate = useNavigate()
  return <CreateServerPage onCreated={(server) => void navigate({ to: '/servers/$id', params: { id: String(server.id) } })} />
}
