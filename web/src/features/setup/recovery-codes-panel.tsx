import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { downloadText } from '@/lib/download'

export interface RecoveryCodesPanelProps {
  codes: string[]
  doneLabel: string
  onDone(): void
}

export function RecoveryCodesPanel({ codes, doneLabel, onDone }: RecoveryCodesPanelProps) {
  const { t } = useTranslation()
  const titleId = useId()
  const checkboxId = useId()
  const [confirmed, setConfirmed] = useState(false)
  const text = codes.join('\n')

  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      toast.success(t('recovery.copied'))
    } catch {
      toast.error(t('recovery.copy_failed'))
    }
  }

  return (
    <section aria-labelledby={titleId} className="space-y-4">
      <h2 id={titleId} className="text-lg font-semibold">
        {t('recovery.title')}
      </h2>
      <p className="text-muted-foreground text-sm">{t('recovery.description')}</p>
      <ol aria-label={t('recovery.list_label')} className="bg-muted grid grid-cols-1 gap-2 rounded-md p-4 font-mono text-sm sm:grid-cols-2">
        {codes.map((code) => (
          <li key={code} className="select-all">
            {code}
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" onClick={() => void copy()}>
          {t('recovery.copy')}
        </Button>
        <Button
          type="button"
          variant="outline"
          onClick={() => downloadText('mc-panel-recovery-codes.txt', `${t('recovery.file_header')}\n\n${text}\n`)}
        >
          {t('recovery.download')}
        </Button>
      </div>
      <div className="flex items-start gap-2">
        <input
          id={checkboxId}
          type="checkbox"
          className="accent-primary mt-0.5 size-4"
          checked={confirmed}
          onChange={(e) => setConfirmed(e.target.checked)}
        />
        <label htmlFor={checkboxId} className="text-sm">
          {t('recovery.confirm')}
        </label>
      </div>
      <Button type="button" className="w-full" disabled={!confirmed} onClick={onDone}>
        {doneLabel}
      </Button>
    </section>
  )
}
