import { useTranslation } from 'react-i18next'
import type { MetricsRange } from '@/api/types'
import { Button } from '@/components/ui/button'
import { RANGES } from './model'

export function RangePicker({ value, onChange }: { value: MetricsRange; onChange(range: MetricsRange): void }) {
  const { t } = useTranslation()
  return (
    <div role="group" aria-label={t('monitoring.range.label')} className="flex flex-wrap gap-2">
      {RANGES.map((r) => (
        <Button key={r} type="button" size="sm" variant={r === value ? 'default' : 'outline'} aria-pressed={r === value} onClick={() => onChange(r)}>
          {t(`monitoring.range.${r}`)}
        </Button>
      ))}
    </div>
  )
}
