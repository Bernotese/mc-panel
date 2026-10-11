import { X } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { SectionCard } from '@/features/profile/section-card'
import { errorMessage } from '@/lib/error-message'
import { useFormat } from '@/lib/format'
import { formatBytes } from './format'
import type { UploadItem } from './upload-queue'

export function UploadList({ items, onCancel, onClear }: { items: UploadItem[]; onCancel(id: number): void; onClear(): void }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  if (items.length === 0) return null
  const anyFinished = items.some((i) => ['done', 'failed', 'cancelled', 'skipped'].includes(i.status))

  /** Every byte is sent; the panel and the service still store the file (a large file takes a moment). */
  function processing(item: UploadItem): boolean {
    return item.status === 'uploading' && item.loaded >= item.size
  }

  function errorText(item: UploadItem): string | null {
    if (item.status !== 'failed') return null
    return typeof item.error === 'string' && item.error.startsWith('files.') ? t(item.error) : errorMessage(item.error, t)
  }

  return (
    <SectionCard
      title={t('files.uploads.title')}
      actions={
        anyFinished ? (
          <Button type="button" variant="outline" size="sm" onClick={onClear}>
            {t('files.uploads.clear')}
          </Button>
        ) : null
      }
    >
      <ul className="divide-y">
        {items.map((item) => {
          const error = errorText(item)
          const active = item.status === 'queued' || item.status === 'uploading'
          return (
            <li key={item.id} className="space-y-1 py-2">
              <div className="flex items-center justify-between gap-2">
                <span className="min-w-0 truncate font-medium" title={item.path}>
                  {item.name}
                </span>
                <span className="flex shrink-0 items-center gap-2 text-sm">
                  <span className="text-muted-foreground tabular-nums">{formatBytes(item.size, fmt.locale)}</span>
                  <span>{processing(item) ? t('files.uploads.processing') : t(`files.uploads.status.${item.status}`)}</span>
                  {active ? (
                    <Button type="button" variant="ghost" size="icon-xs" aria-label={t('files.uploads.cancel', { name: item.name })} onClick={() => onCancel(item.id)}>
                      <X aria-hidden="true" />
                    </Button>
                  ) : null}
                </span>
              </div>
              {item.status === 'uploading' ? (
                <progress className="h-1.5 w-full" value={item.loaded} max={item.size || 1} aria-label={t('files.uploads.progress', { name: item.name })} />
              ) : null}
              {error ? (
                <p role="alert" className="text-destructive text-sm">
                  {error}
                </p>
              ) : null}
            </li>
          )
        })}
      </ul>
    </SectionCard>
  )
}
