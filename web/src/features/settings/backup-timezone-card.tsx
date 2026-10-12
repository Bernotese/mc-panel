import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { ServerSettings } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Button } from '@/components/ui/button'
import { serverSettingsKey, serversKey, updateServerSettings } from '@/features/servers/api'
import { FieldRow } from '@/features/servers/field-row'
import { useErrorToast } from '@/lib/error-message'
import { SettingsCard } from './settings-card'
import { timeZoneOptions } from './time-zones'

export function BackupTimezoneCard({ settings }: { settings: ServerSettings }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [draft, setDraft] = useState<string | null>(null)
  const value = draft ?? settings.backup_timezone
  const options = useMemo(() => timeZoneOptions(settings.backup_timezone), [settings.backup_timezone])

  const save = useMutation({
    mutationFn: (zone: string) => updateServerSettings({ backup_timezone: zone }),
    onSuccess: (result) => {
      queryClient.setQueryData(serverSettingsKey, result)
      // next_run_at and the hint of every schedule change with the zone; the backup queries sit below serversKey.
      void queryClient.invalidateQueries({ queryKey: serversKey })
      setDraft(null)
      toast.success(t('settings.backup_timezone.saved'))
    },
    onError: notifyError,
  })

  return (
    <SettingsCard title={t('settings.backup_timezone.title')} description={t('settings.backup_timezone.description')}>
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (value !== settings.backup_timezone) save.mutate(value)
        }}
      >
        <FieldRow id={`${id}-zone`} label={t('settings.backup_timezone.label')}>
          {(c) => (
            <NativeSelect {...c} className="w-full sm:w-80" value={value} onChange={(e) => setDraft(e.target.value)}>
              {options.map((z) => (
                <option key={z} value={z}>
                  {z}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
        <Button type="submit" disabled={value === settings.backup_timezone || save.isPending}>
          {t('settings.backup_timezone.save')}
        </Button>
      </form>
    </SettingsCard>
  )
}
