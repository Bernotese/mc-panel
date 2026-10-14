import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import type { BackupSchedule, BackupScheduleMode } from '@/api/types'
import { NativeSelect } from '@/components/native-select'
import { Section } from '@/components/section'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FieldRow } from '@/features/servers/field-row'
import { useFormat } from '@/lib/format'
import { setSchedule, updateBackupSchedule } from './api'
import { useBackupErrorToast } from './messages'
import {
  browserTimeZone,
  draftFromSchedule,
  INTERVAL_MAX_HOURS,
  INTERVAL_MIN_HOURS,
  KEEP_FIELDS,
  KEEP_MAX,
  sameSchedule,
  SCHEDULE_MODES,
  scheduleRequest,
  validateSchedule,
  WEEKDAYS,
  type ScheduleDraft,
} from './model'

/**
 * The form is keyed by the stored schedule: after a save (or a refetch with other values) it starts from the new state.
 * The card around it stays, so the region keeps its place in the page.
 */
export function ScheduleCard({ serverId, schedule }: { serverId: number; schedule: BackupSchedule }) {
  const { t } = useTranslation()
  const key = JSON.stringify([schedule.mode, schedule.interval_hours, schedule.time_of_day, schedule.weekday, ...KEEP_FIELDS.map((f) => schedule[f])])
  return (
    <Section title={t('backups.schedule.title')} description={t('backups.schedule.description')}>
      <ScheduleForm key={key} serverId={serverId} schedule={schedule} />
    </Section>
  )
}

function ScheduleForm({ serverId, schedule }: { serverId: number; schedule: BackupSchedule }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const queryClient = useQueryClient()
  const notifyError = useBackupErrorToast()
  const [draft, setDraft] = useState<ScheduleDraft>(() => draftFromSchedule(schedule))
  const errors = validateSchedule(draft)
  const request = scheduleRequest(draft, schedule)
  const unchanged = request !== null && sameSchedule(request, schedule)
  const set = (patch: Partial<ScheduleDraft>) => setDraft((d) => ({ ...d, ...patch }))
  const keepErrorId = `${id}-keep-message`
  // Times are formatted in the browser's zone; say so when the schedule runs in another one.
  const localZone = browserTimeZone()

  const save = useMutation({
    mutationFn: () => {
      if (request === null) throw new Error('backups: invalid schedule')
      return updateBackupSchedule(serverId, request)
    },
    onSuccess: (next) => {
      setSchedule(queryClient, serverId, next)
      toast.success(t('backups.schedule.saved'))
    },
    onError: notifyError,
  })

  return (
    <form
      className="space-y-4 text-sm"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (request !== null && !unchanged) save.mutate()
      }}
    >
      <div className="grid gap-4 sm:grid-cols-3">
        <FieldRow id={`${id}-mode`} label={t('backups.schedule.mode_label')}>
          {(c) => (
            <NativeSelect {...c} value={draft.mode} onChange={(e) => set({ mode: e.target.value as BackupScheduleMode })}>
              {SCHEDULE_MODES.map((m) => (
                <option key={m} value={m}>
                  {t(`backups.schedule.mode.${m}`)}
                </option>
              ))}
            </NativeSelect>
          )}
        </FieldRow>
        {draft.mode === 'interval' ? (
          <FieldRow id={`${id}-interval`} label={t('backups.schedule.interval_label')} error={errors.interval_hours}>
            {(c) => (
              <Input
                {...c}
                type="number"
                inputMode="numeric"
                min={INTERVAL_MIN_HOURS}
                max={INTERVAL_MAX_HOURS}
                value={draft.interval_hours}
                onChange={(e) => set({ interval_hours: e.target.value })}
              />
            )}
          </FieldRow>
        ) : null}
        {draft.mode === 'weekly' ? (
          <FieldRow id={`${id}-weekday`} label={t('backups.schedule.weekday_label')}>
            {(c) => (
              <NativeSelect {...c} value={String(draft.weekday)} onChange={(e) => set({ weekday: Number(e.target.value) })}>
                {WEEKDAYS.map((d) => (
                  <option key={d} value={String(d)}>
                    {t(`backups.schedule.weekday.${d}`)}
                  </option>
                ))}
              </NativeSelect>
            )}
          </FieldRow>
        ) : null}
        {draft.mode === 'daily' || draft.mode === 'weekly' ? (
          <FieldRow id={`${id}-time`} label={t('backups.schedule.time_label')} error={errors.time}>
            {(c) => <Input {...c} type="time" value={draft.time} onChange={(e) => set({ time: e.target.value })} />}
          </FieldRow>
        ) : null}
      </div>

      <fieldset className="space-y-2">
        <legend className="font-medium">{t('backups.schedule.retention_title')}</legend>
        <p className="text-muted-foreground">{t('backups.schedule.retention_hint')}</p>
        <div className="grid gap-4 sm:grid-cols-4">
          {KEEP_FIELDS.map((f) => (
            <FieldRow key={f} id={`${id}-${f}`} label={t(`backups.schedule.${f}`)} error={errors[f]}>
              {(c) => (
                <Input
                  {...c}
                  // The shared "at least one value" error belongs to all four fields.
                  aria-invalid={c['aria-invalid'] ?? (errors.keep ? true : undefined)}
                  aria-describedby={[c['aria-describedby'], errors.keep ? keepErrorId : undefined].filter(Boolean).join(' ') || undefined}
                  type="number"
                  inputMode="numeric"
                  min={0}
                  max={KEEP_MAX}
                  value={draft[f]}
                  onChange={(e) => set({ [f]: e.target.value })}
                />
              )}
            </FieldRow>
          ))}
        </div>
        {errors.keep ? (
          <p id={keepErrorId} role="alert" className="text-destructive">
            {t(errors.keep.key)}
          </p>
        ) : null}
      </fieldset>

      <div className="text-muted-foreground space-y-1">
        <p>{t('backups.schedule.timezone_hint', { timezone: schedule.timezone })}</p>
        {schedule.mode !== 'off' ? (
          <p>
            {schedule.next_run_at === null
              ? t('backups.schedule.not_scheduled')
              : localZone !== null && localZone !== schedule.timezone
                ? t('backups.schedule.next_run_local', {
                    when: fmt.dateTime(schedule.next_run_at),
                    zone: localZone,
                    relative: fmt.relative(schedule.next_run_at),
                  })
                : t('backups.schedule.next_run', { when: fmt.dateTime(schedule.next_run_at), relative: fmt.relative(schedule.next_run_at) })}
          </p>
        ) : null}
        {schedule.last_status !== null ? (
          <p>
            {schedule.last_run_at !== null
              ? t('backups.schedule.last_run', {
                  when: fmt.dateTime(schedule.last_run_at),
                  status: t(`backups.schedule.last_status.${schedule.last_status}`),
                })
              : t('backups.schedule.last_status_only', { status: t(`backups.schedule.last_status.${schedule.last_status}`) })}
          </p>
        ) : null}
        {schedule.last_status === 'failed' && schedule.last_error ? <p className="text-destructive break-words">{schedule.last_error}</p> : null}
      </div>

      <Button type="submit" disabled={request === null || unchanged || save.isPending}>
        {t('backups.schedule.save')}
      </Button>
    </form>
  )
}
