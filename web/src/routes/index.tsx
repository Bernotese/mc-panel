import { createFileRoute } from '@tanstack/react-router'

export const Route = createFileRoute('/')({ component: Home })

export function Home() {
  return <main className="p-6 text-2xl font-semibold">mc-panel</main>
}
