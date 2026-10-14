import { createFileRoute, notFound } from '@tanstack/react-router'
import { ServerLayout } from '@/features/servers/server-layout'

export const Route = createFileRoute('/_app/servers/$id')({
  beforeLoad: ({ params }) => {
    // `/servers/abc` or `/servers/0` are no server ids – render the 404 page instead of firing requests.
    if (!/^[1-9]\d*$/.test(params.id)) throw notFound()
  },
  component: ServerRoute,
})

function ServerRoute() {
  const { id } = Route.useParams()
  return <ServerLayout key={id} serverId={Number(id)} />
}
