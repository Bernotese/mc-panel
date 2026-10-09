// web/src/routes/_app.tsx
import { createFileRoute } from '@tanstack/react-router'
import { appGuard } from '@/features/auth/guards'
import { guardBeforeLoad } from '@/features/auth/route-guards'
import { AppLayout } from '@/features/layout/app-layout'

export const Route = createFileRoute('/_app')({
  beforeLoad: guardBeforeLoad(appGuard),
  component: AppLayout,
})
