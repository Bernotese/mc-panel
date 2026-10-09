import { LayoutDashboard, ScrollText, ShieldCheck, UserRound, Users, type LucideIcon } from 'lucide-react'
import type { Role } from '@/api/types'

export type NavPath = '/' | '/users' | '/security' | '/audit' | '/profile'

export interface NavItem {
  to: NavPath
  labelKey: string
  icon: LucideIcon
  adminOnly: boolean
}

export const NAV_ITEMS: readonly NavItem[] = [
  { to: '/', labelKey: 'nav.dashboard', icon: LayoutDashboard, adminOnly: false },
  { to: '/users', labelKey: 'nav.users', icon: Users, adminOnly: true },
  { to: '/security', labelKey: 'nav.security', icon: ShieldCheck, adminOnly: true },
  { to: '/audit', labelKey: 'nav.audit', icon: ScrollText, adminOnly: true },
  { to: '/profile', labelKey: 'nav.profile', icon: UserRound, adminOnly: false },
]

export function navItemsFor(role: Role): NavItem[] {
  return NAV_ITEMS.filter((item) => !item.adminOnly || role === 'admin')
}
