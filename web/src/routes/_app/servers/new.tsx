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
  // The dashboard shows the new server until the detail page exists (task 20 switches to /servers/$id).
  return <CreateServerPage onCreated={() => void navigate({ to: '/' })} />
}
