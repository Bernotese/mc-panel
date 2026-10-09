// web/src/routes/_app/profile.tsx
import { createFileRoute } from '@tanstack/react-router'
import { ProfilePage } from '@/features/profile/profile-page'

export const Route = createFileRoute('/_app/profile')({ component: ProfilePage })
