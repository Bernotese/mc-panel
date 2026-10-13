import type { TFunction } from 'i18next'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError, isSudoCancelled } from '@/api/errors'
import { errorMessage } from '@/lib/error-message'

/** Backup-specific texts for the 409 answers (spec §5.8); everything else as everywhere else. */
export function backupErrorMessage(err: unknown, t: TFunction): string {
  if (isApiError(err, 'backup_running')) return t('backups.error.job_running')
  if (isApiError(err, 'conflict')) return t('backups.error.conflict')
  return errorMessage(err, t)
}

export function useBackupErrorToast(): (err: unknown) => void {
  const { t } = useTranslation()
  return useCallback(
    (err: unknown) => {
      if (isSudoCancelled(err)) return // the user dismissed the sudo dialog on purpose
      toast.error(backupErrorMessage(err, t))
    },
    [t],
  )
}
