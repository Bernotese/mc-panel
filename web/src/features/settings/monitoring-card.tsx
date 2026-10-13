import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/api/client'
import { unwrap } from '@/api/errors'
import type { MonitoringSettings } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { monitoringSettingsKey, monitoringSettingsQueryOptions, updateMonitoringSettings } from '@/features/monitoring/api'
import { alertKey, draftFromSettings, sameSettings, SETTINGS_FIELDS, validateSettings, type SettingsDraft, type SettingsField } from '@/features/monitoring/model'
import { notifiersKey } from '@/features/security/security-key'
import { FieldRow } from '@/features/servers/field-row'
import { errorMessage, useErrorToast } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { SettingsCard } from './settings-card'

const HINTS: Partial<Record<SettingsField, string>> = { disk_min_gib: 'monitoring.settings.disk_hint', down_after_min: 'monitoring.settings.down_hint' }

export function MonitoringCard() {
  const { t } = useTranslation()
  const fmt = useFormat()
  const firingId = useId()
  const settings = useQuery(monitoringSettingsQueryOptions)
  const notifiers = useQuery({ queryKey: notifiersKey, queryFn: () => unwrap(api.GET('/security/notifiers')) })
  const noChannel = notifiers.data !== undefined && !notifiers.data.some((n) => n.enabled)

  let body
  if (settings.isPending) body = <p className="text-muted-foreground">{t('common.loading')}</p>
  else if (settings.isError)
    body = (
      <p role="alert" className="text-destructive">
        {errorMessage(settings.error, t)}
      </p>
    )
  else
    body = (
      <>
        {/* Keyed by the stored values: after a save the form starts from the new state. */}
        <MonitoringForm key={JSON.stringify(draftFromSettings(settings.data))} settings={settings.data} />
        <div className="space-y-2">
          <h4 id={firingId} className="font-medium">
            {t('monitoring.settings.firing')}
          </h4>
          {settings.data.alerts.length === 0 ? (
            <p className="text-muted-foreground">{t('monitoring.settings.no_alerts')}</p>
          ) : (
            <ul aria-labelledby={firingId} className="space-y-1">
              {settings.data.alerts.map((a) => (
                <li key={`${a.kind}:${a.server_id ?? 'host'}`} className="text-destructive">
                  {t(alertKey(a.kind))}
                  {a.server_name ? ` – ${a.server_name}` : ''}, {t('monitoring.settings.since', { time: fmt.relative(a.since) })}
                  {a.detail ? <span className="text-muted-foreground block">{a.detail}</span> : null}
                </li>
              ))}
            </ul>
          )}
        </div>
      </>
    )

  return (
    <SettingsCard title={t('monitoring.settings.title')} description={t('monitoring.settings.description')}>
      {body}
      <p className="text-muted-foreground">{t('monitoring.settings.channels_hint')}</p>
      {noChannel ? (
        <p role="status" className="text-destructive">
          {t('monitoring.settings.no_notifiers')}
        </p>
      ) : null}
      <Link to="/security" className="text-primary underline underline-offset-4">
        {t('monitoring.settings.notifiers_link')}
      </Link>
    </SettingsCard>
  )
}

function MonitoringForm({ settings }: { settings: MonitoringSettings }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useErrorToast()
  const initial = draftFromSettings(settings)
  const [draft, setDraft] = useState<SettingsDraft>(initial)
  const v = validateSettings(draft)
  const changed = v.request !== null && !sameSettings(v.request, settings)
  const save = useMutation({
    mutationFn: updateMonitoringSettings,
    onSuccess: (result) => {
      queryClient.setQueryData(monitoringSettingsKey, result)
      toast.success(t('monitoring.settings.saved'))
    },
    onError: notifyError,
  })

  return (
    <form
      className="grid gap-4 sm:grid-cols-2"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (v.request && changed && !save.isPending) save.mutate(v.request)
      }}
    >
      {SETTINGS_FIELDS.map((field) => {
        const hint = HINTS[field]
        // Errors show for edited fields only; untouched fields hold the stored, valid value.
        const error = draft[field] !== initial[field] ? v.errors[field] : undefined
        return (
          <FieldRow key={field} id={`${id}-${field}`} label={t(`monitoring.settings.${field}`)} hint={hint ? t(hint) : undefined} error={error}>
            {(c) => <Input {...c} inputMode={field === 'disk_min_gib' ? 'decimal' : 'numeric'} value={draft[field]} onChange={(e) => setDraft({ ...draft, [field]: e.target.value })} />}
          </FieldRow>
        )
      })}
      <div className="sm:col-span-2">
        <Button type="submit" disabled={!changed || save.isPending}>
          {t('monitoring.settings.save')}
        </Button>
      </div>
    </form>
  )
}
