import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { PowerAction, Server } from '@/api/types'
import { isApiError } from '@/api/errors'
import { useErrorToast } from '@/lib/error-message'
import { powerServer, serversKey } from './api'

/** Power action with toast and refresh of list and detail (both share the serversKey prefix). */
export function usePower(server: Pick<Server, 'id' | 'name'>) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  return useMutation({
    mutationFn: (action: PowerAction) => powerServer(server.id, action),
    onSuccess: (_data, action) => {
      toast.success(t('servers.power_requested', { action: t(`servers.power_noun.${action}`), name: server.name }))
      void queryClient.invalidateQueries({ queryKey: serversKey })
    },
    onError: (err, action) => {
      // 409 on start/restart: the panel refuses while an archive is being extracted.
      if (isApiError(err, 'conflict') && action !== 'stop') toast.error(t('servers.extracting_lock'))
      else notifyError(err)
    },
  })
}
