import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isSudoCancelled } from '@/api/errors'
import type { DevInstance, Server } from '@/api/types'
import { Section } from '@/components/section'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { memoryLimitsKey, memoryLimitsQueryOptions, serverKey, serverSettingsQueryOptions, serversKey, updateServer } from '@/features/servers/api'
import { FieldRow } from '@/features/servers/field-row'
import { memoryLimitsFrom } from '@/features/servers/form-model'
import { memoryInputValue } from '@/features/servers/format'
import { MemoryField } from '@/features/servers/server-form'
import { memoryMiBFromGiB, validateMemoryMiB } from '@/features/servers/validation'
import { errorMessage } from '@/lib/error-message'
import { devKey, devQueryOptions, updateDev } from './api'
import { devSettingsPatch, DISTANCE, draftFromDev, IDLE_MINUTES, validateDevSettings, type DevSettingsDraft } from './model'

/** Settings area of a dev (spec §8): sync time, idle time, the two overrides and the memory; everything else follows prod. */
export function DevSettings({ server }: { server: Server }) {
  const { t } = useTranslation()
  const dev = useQuery(devQueryOptions(server.id))
  if (dev.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
  if (dev.isError)
    return (
      <p role="alert" className="text-destructive text-sm">
        {errorMessage(dev.error, t)}
      </p>
    )
  return (
    <div className="grid gap-6 xl:grid-cols-2">
      {/* Re-initialised when the stored settings change (after saving or elsewhere). */}
      <DevSettingsSection key={JSON.stringify(draftFromDev(dev.data))} server={server} dev={dev.data} />
      <div className="flex flex-col gap-6">
        {server.instance ? <DevMemorySection key={server.instance.memory_mib} server={server} memoryMiB={server.instance.memory_mib} /> : null}
        <p className="bevel-inset px-4 py-3 text-sm">
          {t('dev.settings.managed')}{' '}
          <Link to="/servers/$id/settings" params={{ id: String(dev.data.source_id) }} className="text-primary underline underline-offset-4">
            {t('dev.settings.managed_link', { source: dev.data.source_name })}
          </Link>
        </p>
      </div>
    </div>
  )
}

export function DevSettingsSection({ server, dev }: { server: Server; dev: DevInstance }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const settings = useQuery(serverSettingsQueryOptions)
  const [draft, setDraft] = useState<DevSettingsDraft>(() => draftFromDev(dev))
  const [submitted, setSubmitted] = useState(false)
  const errors = validateDevSettings(draft)
  const valid = Object.keys(errors).length === 0
  const patch = valid ? devSettingsPatch(draft, dev) : {}
  const dirty = !valid || Object.keys(patch).length > 0
  const shown = (field: keyof DevSettingsDraft) => (submitted ? errors[field] : undefined)
  const set = (p: Partial<DevSettingsDraft>) => setDraft({ ...draft, ...p })

  const save = useMutation({
    mutationFn: () => updateDev(server.id, patch),
    onSuccess: (updated) => {
      queryClient.setQueryData(devKey(server.id), updated)
      // The overrides change the dev's spec (revision): the header may now ask for a restart.
      void queryClient.invalidateQueries({ queryKey: serversKey })
      toast.success(t('dev.settings.saved'))
    },
  })

  return (
    <Section title={t('dev.settings.title')} description={t('dev.settings.description', { source: dev.source_name })}>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          setSubmitted(true)
          if (valid && Object.keys(patch).length > 0) save.mutate()
        }}
      >
        <FieldRow
          id={`${id}-time`}
          label={t('dev.settings.sync_time')}
          hint={settings.data ? t('dev.settings.sync_time_hint', { timezone: settings.data.backup_timezone }) : undefined}
          error={shown('time')}
        >
          {(c) => <Input {...c} type="time" className="w-32" value={draft.time} onChange={(e) => set({ time: e.target.value })} />}
        </FieldRow>
        <FieldRow id={`${id}-idle`} label={t('dev.settings.idle_minutes')} hint={t('dev.settings.idle_minutes_hint', IDLE_MINUTES)} error={shown('idle')}>
          {(c) => (
            <Input
              {...c}
              type="number"
              inputMode="numeric"
              min={IDLE_MINUTES.min}
              max={IDLE_MINUTES.max}
              className="w-24"
              value={draft.idle}
              onChange={(e) => set({ idle: e.target.value })}
            />
          )}
        </FieldRow>
        <div className="grid gap-4 sm:grid-cols-2">
          <FieldRow id={`${id}-view`} label={t('servers.props.view_distance')} error={shown('view')}>
            {(c) => (
              <Input {...c} type="number" inputMode="numeric" min={DISTANCE.min} max={DISTANCE.max} className="w-24" value={draft.view} onChange={(e) => set({ view: e.target.value })} />
            )}
          </FieldRow>
          <FieldRow id={`${id}-simulation`} label={t('servers.props.simulation_distance')} error={shown('simulation')}>
            {(c) => (
              <Input
                {...c}
                type="number"
                inputMode="numeric"
                min={DISTANCE.min}
                max={DISTANCE.max}
                className="w-24"
                value={draft.simulation}
                onChange={(e) => set({ simulation: e.target.value })}
              />
            )}
          </FieldRow>
        </div>
        <p className="text-muted-foreground text-sm">{t('dev.settings.overrides_hint', { source: dev.source_name })}</p>
        {save.error && !isSudoCancelled(save.error) ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(save.error, t)}
          </p>
        ) : null}
        <div>
          <Button type="submit" disabled={save.isPending || !dirty}>
            {t('dev.settings.save')}
          </Button>
        </div>
      </form>
    </Section>
  )
}

/** Memory is the one field of PATCH /servers/{id} a dev accepts (admin, sudo); a sync never overwrites it. */
function DevMemorySection({ server, memoryMiB }: { server: Server; memoryMiB: number }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const limits = useQuery(memoryLimitsQueryOptions)
  const [text, setText] = useState(() => memoryInputValue(memoryMiB))
  const [touched, setTouched] = useState(false)
  const mib = memoryMiBFromGiB(text)
  const error = validateMemoryMiB(mib, memoryLimitsFrom(limits.data)) ?? undefined
  const save = useMutation({
    mutationFn: (value: number) => updateServer(server.id, { memory_mib: value }),
    onSuccess: (updated) => {
      queryClient.setQueryData(serverKey(server.id), updated)
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
      void queryClient.invalidateQueries({ queryKey: memoryLimitsKey })
      toast.success(updated.status.restart_required ? t('servers.settings.saved_restart') : t('servers.settings.saved'))
    },
  })
  return (
    <Section title={t('servers.form.section.resources')} description={t('dev.settings.memory_hint')}>
      <form
        className="flex flex-col gap-4"
        noValidate
        onSubmit={(e) => {
          e.preventDefault()
          setTouched(true)
          if (!error && mib !== null) save.mutate(mib)
        }}
      >
        <MemoryField
          id={`${id}-memory`}
          text={text}
          error={touched ? error : undefined}
          limits={limits.data}
          ownMiB={memoryMiB}
          recommendedMiB={undefined}
          onChange={(value) => {
            setText(value)
            setTouched(true)
          }}
        />
        {save.error && !isSudoCancelled(save.error) ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(save.error, t)}
          </p>
        ) : null}
        <div>
          <Button type="submit" disabled={save.isPending || mib === memoryMiB}>
            {t('dev.settings.save_memory')}
          </Button>
        </div>
      </form>
    </Section>
  )
}
