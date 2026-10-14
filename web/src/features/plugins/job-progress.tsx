import { CircleCheck, CircleMinus, CircleX, Clock, LoaderCircle } from 'lucide-react'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import type { TFunction } from 'i18next'
import type { PluginJob, PluginJobStep } from '@/api/types'
import { Button } from '@/components/ui/button'
import { jobProgress } from './model'

function jobTitle(job: PluginJob, t: TFunction): string {
  if (job.kind === 'mrpack') {
    if (job.state === 'running') return t('plugins.mrpack.job.running')
    return job.state === 'done' ? t('plugins.mrpack.job.done') : t('plugins.mrpack.job.failed')
  }
  if (job.kind === 'install') {
    if (job.state === 'running') return t('plugins.job.install_running')
    return job.state === 'done' ? t('plugins.job.install_done') : t('plugins.job.install_failed')
  }
  if (job.state === 'running') return t('plugins.job.update_running')
  return job.state === 'done' ? t('plugins.job.update_done') : t('plugins.job.update_failed')
}

/** Spec §7.1: failed steps name the reason from error_code (translated); the panel's text is only a fallback. */
function stepText(step: PluginJobStep, t: TFunction): string {
  if (step.state !== 'failed') return t(`plugins.step.state.${step.state}`)
  if (step.error_code) {
    // A code this UI does not know yet (newer panel) falls back to the panel's text instead of the raw i18n key.
    const reason = t(`plugins.step.error.${step.error_code}`, { defaultValue: step.error || t('plugins.step.state.failed') })
    return t('plugins.step.failed_reason', { reason })
  }
  return step.error ? t('plugins.step.failed_reason', { reason: step.error }) : t('plugins.step.state.failed')
}

function StepIcon({ state }: { state: PluginJobStep['state'] }) {
  const cls = 'mt-0.5 size-4 shrink-0'
  if (state === 'downloading') return <LoaderCircle aria-hidden="true" className={`${cls} animate-spin`} />
  if (state === 'done') return <CircleCheck aria-hidden="true" className={cls} />
  if (state === 'failed') return <CircleX aria-hidden="true" className={`${cls} text-destructive`} />
  if (state === 'skipped') return <CircleMinus aria-hidden="true" className={cls} />
  return <Clock aria-hidden="true" className={`${cls} text-muted-foreground`} />
}

export function JobProgress({ job, onDismiss }: { job: PluginJob; onDismiss(): void }) {
  const { t } = useTranslation()
  const id = useId()
  const { finished, total } = jobProgress(job)
  return (
    <section aria-labelledby={id} className="space-y-3 rounded-md border p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        {/* Only the title is announced; the per-step lines change on every poll (as in 3b, review F9). */}
        <p id={id} role="status" className="font-medium">
          {jobTitle(job, t)}
        </p>
        {job.state !== 'running' ? (
          <Button type="button" variant="ghost" size="sm" onClick={onDismiss}>
            {t('plugins.job.dismiss')}
          </Button>
        ) : null}
      </div>
      <progress className="h-1.5 w-full" value={finished} max={total || 1} aria-label={t('plugins.job.progress')} />
      <p className="text-muted-foreground text-sm tabular-nums">{t('plugins.job.counts', { finished, total })}</p>
      <ul className="space-y-2 text-sm">
        {job.steps.map((step, i) => (
          <li key={`${i}:${step.file}`} className="flex items-start gap-2">
            <StepIcon state={step.state} />
            <div className="min-w-0 flex-1">
              <p className="break-words">
                <span className="font-medium">{step.title}</span>{' '}
                <span className="text-muted-foreground font-mono text-xs break-all">{step.file}</span>
              </p>
              <p className={step.state === 'failed' ? 'text-destructive' : 'text-muted-foreground'}>{stepText(step, t)}</p>
            </div>
          </li>
        ))}
      </ul>
    </section>
  )
}
