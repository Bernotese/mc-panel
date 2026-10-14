import { useServerContext } from './server-context'

export function ServerHeader() {
  const { server } = useServerContext()
  return <h1 className="pixel-heading text-xl">{server.name}</h1>
}
