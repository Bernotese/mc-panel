import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { PlayerActionRequest } from '@/api/types'
import { plainText } from '@/features/console/ansi'
import { useErrorToast } from '@/lib/error-message'
import { playerAction, playersKey } from './api'

/** Runs a player action, shows the server's answer as a toast and reloads online players and lists. */
export function usePlayerAction(serverId: number) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  return useMutation({
    mutationFn: (body: PlayerActionRequest) => playerAction(serverId, body),
    onSuccess: (response) => {
      toast.success(plainText(response.output).trim() || t('players.done'))
      void queryClient.invalidateQueries({ queryKey: playersKey(serverId) })
    },
    onError: notifyError,
  })
}
