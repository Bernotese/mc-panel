import { createFileRoute } from '@tanstack/react-router'

// Temporary landing page; replaced by the dashboard in Task 11.
export const Route = createFileRoute('/_app/')({
  component: () => <p className="text-2xl font-semibold">mc-panel</p>,
})
