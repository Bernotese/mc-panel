import { ScrollText, Server, Settings, ShieldCheck, UserRound, Users, type LucideIcon } from 'lucide-react'
import type { Role } from '@/api/types'

export type NavPath = '/' | '/users' | '/security' | '/audit' | '/settings' | '/profile'

export interface NavItem {
  to: NavPath
  labelKey: string
  icon: LucideIcon
  adminOnly: boolean
  /** Spec §4.1: the profile sits at the bottom of the icon rail, next to theme and logout. */
  placement: 'top' | 'bottom'
}

export const NAV_ITEMS: readonly NavItem[] = [
  { to: '/', labelKey: 'nav.dashboard', icon: Server, adminOnly: false, placement: 'top' },
  { to: '/users', labelKey: 'nav.users', icon: Users, adminOnly: true, placement: 'top' },
  { to: '/security', labelKey: 'nav.security', icon: ShieldCheck, adminOnly: true, placement: 'top' },
  { to: '/audit', labelKey: 'nav.audit', icon: ScrollText, adminOnly: true, placement: 'top' },
  { to: '/settings', labelKey: 'nav.settings', icon: Settings, adminOnly: true, placement: 'top' },
  { to: '/profile', labelKey: 'nav.profile', icon: UserRound, adminOnly: false, placement: 'bottom' },
]

export function navItemsFor(role: Role, placement?: 'top' | 'bottom'): NavItem[] {
  return NAV_ITEMS.filter((item) => (!item.adminOnly || role === 'admin') && (!placement || item.placement === placement))
}
