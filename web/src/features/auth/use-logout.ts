// web/src/features/auth/use-logout.ts
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { api } from '@/api/client'
import { unwrapVoid } from '@/api/errors'
import { clearSession } from './me'

export function useLogout() {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  return useMutation({
    mutationFn: () => unwrapVoid(api.POST('/auth/logout')),
    onSettled: async () => {
      clearSession(queryClient)
      await navigate({ to: '/login' })
    },
  })
}
