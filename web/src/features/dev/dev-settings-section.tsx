import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { isApiError, isSudoCancelled } from '@/api/errors'
import type { DevInstance, InstanceConfig, Server } from '@/api/types'
import { Section } from '@/components/section'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { memoryLimitsKey, memoryLimitsQueryOptions, serverKey, serverSettingsQueryOptions, serversKey, updateServer } from '@/features/servers/api'
import { DraftConflict } from '@/features/servers/draft-conflict'
import { FieldRow } from '@/features/servers/field-row'
import { memoryLimitsFrom } from '@/features/servers/form-model'
import { memoryInputValue } from '@/features/servers/format'
import { MemoryField } from '@/features/servers/server-form'
import { memoryMiBFromGiB, validateMemoryMiB } from '@/features/servers/validation'
import { errorMessage } from '@/lib/error-message'
import { useReportUnsaved } from '@/lib/unsaved-changes'
import { mergeChangedKeys, useRemoteDraft } from '@/lib/use-remote-draft'
import { devKey, devQueryOptions, updateDev } from './api'
import { devSettingsPatch, DISTANCE, draftFromDev, IDLE_MINUTES, sameDevDraft, sameDevField, validateDevSettings, type DevSettingsDraft } from './model'

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
      <DevSettingsSection server={server} dev={dev.data} />
      <div className="flex flex-col gap-6">
        {server.instance ? <DevMemorySection server={server} instance={server.instance} /> : null}
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
  // Issue #154: the draft survives the 15-s polls of the dev (DevBanner) and changes elsewhere; a clean form follows them.
  const d = useRemoteDraft(dev, { toDraft: draftFromDev, merge: mergeChangedKeys, same: sameDevDraft })
  useReportUnsaved(d.dirty)
  const { draft } = d
  const [submitted, setSubmitted] = useState(false)
  const errors = validateDevSettings(draft)
  const valid = Object.keys(errors).length === 0
  const patch = valid ? devSettingsPatch(draft, d.base) : {}
  const dirty = !valid || Object.keys(patch).length > 0
  const shown = (field: keyof DevSettingsDraft) => (submitted ? errors[field] : undefined)
  const set = (p: Partial<DevSettingsDraft>) => d.setDraft({ ...draft, ...p })
  const labels: Record<keyof DevSettingsDraft, string> = {
    time: t('dev.settings.sync_time'),
    idle: t('dev.settings.idle_minutes'),
    view: t('servers.props.view_distance'),
    simulation: t('servers.props.simulation_distance'),
  }
  const changes = (Object.keys(labels) as (keyof DevSettingsDraft)[])
    .filter((k) => !sameDevField(d.remoteDraft, d.baseDraft, k))
    .map((k) => ({
      key: k,
      label: labels[k],
      theirs: d.remoteDraft[k],
      mine: !sameDevField(draft, d.baseDraft, k) && !sameDevField(draft, d.remoteDraft, k) ? draft[k] : null,
    }))

  const save = useMutation({
    mutationFn: () => updateDev(server.id, patch),
    onSuccess: (updated) => {
      queryClient.setQueryData(devKey(server.id), updated)
      d.saved(updated)
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
        {d.conflict ? <DraftConflict reason="remote" changes={changes} onReload={d.reload} onKeepMine={d.keepMine} /> : null}
        {/* Locked while saving: an edit typed meanwhile would be replaced by the saved settings (issue #154). */}
        <fieldset disabled={save.isPending} className="contents">
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
        </fieldset>
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

/**
 * Memory is the one field of PATCH /servers/{id} a dev accepts (admin, sudo); a sync never overwrites it. Saved onto the
 * revision it was edited against, like the server form (issue #154).
 */
function DevMemorySection({ server, instance }: { server: Server; instance: InstanceConfig }) {
  const { t } = useTranslation()
  const id = useId()
  const queryClient = useQueryClient()
  const limits = useQuery(memoryLimitsQueryOptions)
  const sameMemory = (a: string, b: string) => (memoryMiBFromGiB(a) ?? a) === (memoryMiBFromGiB(b) ?? b)
  const d = useRemoteDraft(instance, { toDraft: (i) => memoryInputValue(i.memory_mib), merge: (_base, draft) => draft, same: sameMemory })
  useReportUnsaved(d.dirty)
  const text = d.draft
  const [touched, setTouched] = useState(false)
  const mib = memoryMiBFromGiB(text)
  const error = validateMemoryMiB(mib, memoryLimitsFrom(limits.data)) ?? undefined
  const save = useMutation({
    mutationFn: (value: number) => updateServer(server.id, { memory_mib: value, expected_revision: d.base.revision }),
    onSuccess: (updated) => {
      queryClient.setQueryData(serverKey(server.id), updated)
      if (updated.instance) d.saved(updated.instance)
      void queryClient.invalidateQueries({ queryKey: serversKey, exact: true })
      void queryClient.invalidateQueries({ queryKey: memoryLimitsKey })
      toast.success(updated.status.restart_required ? t('servers.settings.saved_restart') : t('servers.settings.saved'))
    },
    // The newer stored value is needed to compare with and to rebase onto.
    onError: (err) => {
      if (isApiError(err, 'server_modified')) void queryClient.invalidateQueries({ queryKey: serverKey(server.id) })
    },
  })
  const rejected = isApiError(save.error, 'server_modified')
  const resolve = (action: () => void) => {
    action()
    save.reset()
  }
  const memoryChanged = !sameMemory(d.remoteDraft, d.baseDraft)
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
        {d.conflict || rejected ? (
          <DraftConflict
            reason={rejected ? 'rejected' : 'remote'}
            changes={
              memoryChanged
                ? [{ key: 'memory', label: t('servers.form.memory'), theirs: t('servers.draft.gib', { value: d.remoteDraft }), mine: t('servers.draft.gib', { value: text }) }]
                : []
            }
            waiting={rejected && instance.revision === d.base.revision}
            onReload={() => resolve(d.reload)}
            onKeepMine={() => resolve(d.keepMine)}
          />
        ) : null}
        {/* Locked while saving: a value typed meanwhile would be replaced by the saved one. */}
        <fieldset disabled={save.isPending} className="contents">
          <MemoryField
            id={`${id}-memory`}
            text={text}
            error={touched ? error : undefined}
            limits={limits.data}
            ownMiB={memoryMiBFromGiB(d.baseDraft) ?? 0}
            recommendedMiB={undefined}
            onChange={(value) => {
              d.setDraft(value)
              setTouched(true)
              if (rejected && sameMemory(value, d.baseDraft)) save.reset()
            }}
          />
        </fieldset>
        {save.error && !rejected && !isSudoCancelled(save.error) ? (
          <p role="alert" className="text-destructive text-sm">
            {errorMessage(save.error, t)}
          </p>
        ) : null}
        <div>
          <Button type="submit" disabled={save.isPending || !d.dirty}>
            {t('dev.settings.save_memory')}
          </Button>
        </div>
      </form>
    </Section>
  )
}
