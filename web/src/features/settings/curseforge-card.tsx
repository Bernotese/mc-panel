import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { ServerSettings } from '@/api/types'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { serverSettingsKey, updateServerSettings } from '@/features/servers/api'
import { FieldRow } from '@/features/servers/field-row'
import { validateCurseForgeKey } from '@/features/servers/validation'
import { useErrorToast } from '@/lib/error-message'
import { SettingsCard } from './settings-card'

const CONSOLE_URL = 'https://console.curseforge.com/'

/** Write-only: the panel only reports whether a key is stored, never the key itself. */
export function CurseForgeCard({ settings }: { settings: ServerSettings }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const [key, setKey] = useState('')
  const [confirmRemove, setConfirmRemove] = useState(false)
  const error = key === '' ? null : validateCurseForgeKey(key)
  const stored = settings.curseforge_api_key_set

  const save = useMutation({
    mutationFn: (value: string | null) => updateServerSettings({ curseforge_api_key: value }),
    onSuccess: (result, value) => {
      queryClient.setQueryData(serverSettingsKey, result)
      setKey('')
      setConfirmRemove(false)
      toast.success(value === null ? t('settings.curseforge.removed') : t('settings.curseforge.saved'))
    },
    onError: notifyError,
  })

  return (
    <SettingsCard
      title={t('settings.curseforge.title')}
      description={t('settings.curseforge.description')}
      actions={<Badge variant={stored ? 'secondary' : 'outline'}>{stored ? t('settings.curseforge.set') : t('settings.curseforge.not_set')}</Badge>}
    >
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (key !== '' && error === null) save.mutate(key)
        }}
      >
        <FieldRow
          id={`${id}-key`}
          label={stored ? t('settings.curseforge.label_replace') : t('settings.curseforge.label')}
          hint={t('settings.curseforge.hint')}
          error={error ?? undefined}
        >
          {(c) => <Input {...c} type="password" value={key} autoComplete="off" spellCheck={false} onChange={(e) => setKey(e.target.value)} />}
        </FieldRow>
        <a href={CONSOLE_URL} target="_blank" rel="noreferrer" className="text-primary inline-block underline underline-offset-4">
          {t('settings.curseforge.console_link')}
        </a>
        <div className="flex flex-wrap gap-2">
          <Button type="submit" disabled={key === '' || error !== null || save.isPending}>
            {t('settings.curseforge.save')}
          </Button>
          {stored ? (
            <Button type="button" variant="outline" disabled={save.isPending} onClick={() => setConfirmRemove(true)}>
              {t('settings.curseforge.remove')}
            </Button>
          ) : null}
        </div>
      </form>
      <ConfirmDialog
        open={confirmRemove}
        onOpenChange={setConfirmRemove}
        title={t('settings.curseforge.remove_title')}
        description={t('settings.curseforge.remove_description')}
        confirmLabel={t('settings.curseforge.remove')}
        destructive
        pending={save.isPending}
        onConfirm={() => save.mutate(null)}
      />
    </SettingsCard>
  )
}
