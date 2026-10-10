import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { ServerSettings } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { serverSettingsKey, serversKey, updateServerSettings } from '@/features/servers/api'
import { FieldRow } from '@/features/servers/field-row'
import { normalizeBaseDomain, validateBaseDomain } from '@/features/servers/validation'
import { useErrorToast } from '@/lib/error-message'
import { SettingsCard } from './settings-card'

const EXAMPLE_DOMAIN = 'mc.example.com'

export function BaseDomainCard({ settings }: { settings: ServerSettings }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  // null = untouched; the input shows the stored value until the admin edits it.
  const [draft, setDraft] = useState<string | null>(null)
  const text = draft ?? settings.base_domain ?? ''
  const normalized = normalizeBaseDomain(text)
  const error = draft === null ? null : validateBaseDomain(normalized)
  const unchanged = normalized === (settings.base_domain ?? '')
  const example = error === null && normalized !== '' ? normalized : EXAMPLE_DOMAIN

  const save = useMutation({
    mutationFn: (baseDomain: string) => updateServerSettings({ base_domain: baseDomain }),
    onSuccess: (result) => {
      queryClient.setQueryData(serverSettingsKey, result)
      // Addresses and restart hints of all servers change with the base domain (spec §8).
      void queryClient.invalidateQueries({ queryKey: serversKey })
      setDraft(null)
      toast.success(t('settings.base_domain.saved'))
    },
    onError: notifyError,
  })

  return (
    <SettingsCard title={t('settings.base_domain.title')} description={t('settings.base_domain.description')}>
      <form
        className="space-y-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          if (error === null && !unchanged) save.mutate(normalized)
        }}
      >
        <FieldRow id={`${id}-domain`} label={t('settings.base_domain.label')} error={error ?? undefined}>
          {(c) => (
            <Input
              {...c}
              value={text}
              placeholder={EXAMPLE_DOMAIN}
              autoCapitalize="none"
              autoComplete="off"
              spellCheck={false}
              onChange={(e) => setDraft(e.target.value)}
            />
          )}
        </FieldRow>
        <div className="space-y-1">
          <p className="text-muted-foreground">{t('settings.base_domain.dns_hint')}</p>
          <p className="font-mono break-all">
            <code>{`*.${example}`}</code> → {t('settings.base_domain.dns_target')}
          </p>
        </div>
        {settings.base_domain !== null ? <p className="text-muted-foreground">{t('settings.base_domain.change_hint')}</p> : null}
        <Button type="submit" disabled={draft === null || error !== null || unchanged || save.isPending}>
          {t('settings.base_domain.save')}
        </Button>
      </form>
    </SettingsCard>
  )
}
