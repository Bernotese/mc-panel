import { CircleCheck, CircleX, LoaderCircle } from 'lucide-react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { BackupJob } from '@/api/types'
import { Button } from '@/components/ui/button'
import { formatBytes } from '@/features/files/format'
import { useFormat } from '@/lib/format'
import { jobErrorKey, jobTitleKey, stepKey } from './model'

export function JobStatus({ job, onDismiss }: { job: BackupJob; onDismiss(): void }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const id = useId()
  const isActive = job.state === 'queued' || job.state === 'running'
  const Icon = job.state === 'done' ? CircleCheck : job.state === 'failed' ? CircleX : LoaderCircle
  return (
    <section aria-labelledby={id} className="flex flex-col gap-2 rounded-md border p-4 sm:flex-row sm:items-start sm:justify-between">
      <div className="flex gap-3">
        <Icon aria-hidden="true" className={isActive ? 'mt-0.5 size-5 animate-spin' : 'mt-0.5 size-5'} />
        <div className="space-y-1 text-sm">
          <h2 id={id} className="font-medium">
            {t(jobTitleKey(job))}
          </h2>
          {job.trigger === 'scheduled' ? <p className="text-muted-foreground">{t('backups.job.scheduled')}</p> : null}
          {job.state !== 'failed' ? <p>{t(stepKey(job.step))}</p> : null}
          {job.state === 'queued' && job.queue_position > 0 ? (
            <p className="text-muted-foreground">{t('backups.job.queue_position', { position: job.queue_position })}</p>
          ) : null}
          {isActive && job.bytes > 0 ? (
            <p className="text-muted-foreground">{t('backups.job.bytes', { bytes: formatBytes(job.bytes, fmt.locale) })}</p>
          ) : null}
          {job.state === 'failed' ? (
            <div role="alert" className="text-destructive space-y-1">
              <p>{t(jobErrorKey(job.error_code ?? 'internal'))}</p>
              {job.error !== '' ? <p className="text-muted-foreground break-words">{job.error}</p> : null}
            </div>
          ) : null}
        </div>
      </div>
      {isActive ? null : (
        <Button type="button" variant="outline" size="sm" onClick={onDismiss}>
          {t('backups.job.dismiss')}
        </Button>
      )}
    </section>
  )
}
