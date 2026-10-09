// web/src/features/auth/route-guards.ts
import { redirect } from '@tanstack/react-router'
import type { Me } from '@/api/types'
import type { RouterContext } from '@/router'
import type { AuthPath } from './guards'
import { loadMe } from './me'

/** Turns a pure guard into a TanStack Router beforeLoad hook. */
export function guardBeforeLoad(guard: (me: Me | null) => AuthPath | null) {
  return async ({ context }: { context: RouterContext }): Promise<void> => {
    const target = guard(await loadMe(context.queryClient))
    if (target) throw redirect({ to: target })
  }
}
