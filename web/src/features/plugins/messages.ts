import type { TFunction } from 'i18next'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError, isSudoCancelled } from '@/api/errors'
import { errorMessage } from '@/lib/error-message'

/** Plugin-specific texts for the 409 answers (spec §6.6/§6.7); everything else as everywhere else. */
export function pluginErrorMessage(err: unknown, t: TFunction): string {
  if (isApiError(err, 'conflict')) return t('plugins.error.job_running')
  if (isApiError(err, 'plugin_incompatible')) return t('plugins.error.incompatible')
  if (isApiError(err, 'plugin_not_downloadable')) return t('plugins.error.not_downloadable')
  return errorMessage(err, t)
}

/** Starting a job: 429 means the panel's four job slots are taken (spec §6.4), not the per-account limit. */
export function jobStartErrorMessage(err: unknown, t: TFunction): string {
  if (isApiError(err, 'rate_limited')) return t('plugins.error.too_many_jobs')
  return pluginErrorMessage(err, t)
}

/** Starting an install: 409 conflict is a running job or a project installed meanwhile (plan 03, deviation 5). */
export function installErrorMessage(err: unknown, t: TFunction): string {
  if (isApiError(err, 'conflict')) return t('plugins.error.install_conflict')
  return jobStartErrorMessage(err, t)
}

export function usePluginErrorToast(): (err: unknown) => void {
  const { t } = useTranslation()
  return useCallback(
    (err: unknown) => {
      if (isSudoCancelled(err)) return
      toast.error(pluginErrorMessage(err, t))
    },
    [t],
  )
}
