import { useTranslation } from 'react-i18next'
import { useFormat } from '@/lib/format'
import { formatBytes } from './format'
import { baseName } from './path'
import type { FileJob } from '@/api/types'

export function JobStatus({ job }: { job: FileJob }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  return (
    <div className="space-y-2 rounded-md border p-3 text-sm">
      {/* Only the title is a live region: the counts change with every poll and would be read out every 2 s. */}
      <p role="status" className="font-medium">
        {t('files.extract.running', { name: baseName(job.path) })}
      </p>
      <progress
        className="h-1.5 w-full"
        value={job.bytes}
        max={job.total_bytes || 1}
        aria-label={t('files.extract.progress')}
      />
      <p className="text-muted-foreground tabular-nums">
        {t('files.extract.counts', {
          entries: fmt.number(job.entries),
          total_entries: fmt.number(job.total_entries),
          bytes: formatBytes(job.bytes, fmt.locale),
          total_bytes: formatBytes(job.total_bytes, fmt.locale),
        })}
      </p>
    </div>
  )
}
