import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'

/** One setting that changed on the server since the draft was loaded; `mine` is set when the draft changed it too. */
export interface DraftChange {
  key: string
  label: string
  theirs: string
  mine: string | null
}

/**
 * Issue #154: a draft that met a newer stored value – noticed while polling ("remote") or refused by the panel with
 * 409 server_modified ("rejected"). Lists what changed and lets the user load the stored state or keep the own
 * changes on top of it. `waiting`: the newer stored value is still loading.
 */
export function DraftConflict({
  reason,
  changes,
  waiting = false,
  onReload,
  onKeepMine,
}: {
  reason: 'remote' | 'rejected'
  changes: DraftChange[]
  waiting?: boolean
  onReload(): void
  onKeepMine(): void
}) {
  const { t } = useTranslation()
  return (
    <div role="alert" className="border-status-starting/60 bg-card space-y-3 border-2 p-4 text-sm">
      <div className="space-y-1">
        <p className="text-status-starting font-medium">{t('servers.draft.title')}</p>
        <p>{t(`servers.draft.${reason}`)}</p>
      </div>
      {waiting ? (
        <p className="text-muted-foreground">{t('servers.draft.waiting')}</p>
      ) : changes.length > 0 ? (
        <ul aria-label={t('servers.draft.changes')} className="space-y-1">
          {changes.map((c) => (
            <li key={c.key} className="break-words">
              <span className="font-medium">{c.label}</span> – {t('servers.draft.theirs', { value: c.theirs })}
              {c.mine !== null ? <span className="text-status-starting"> · {t('servers.draft.mine', { value: c.mine })}</span> : null}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-muted-foreground">{t('servers.draft.outside')}</p>
      )}
      <p className="text-muted-foreground">{t('servers.draft.keep_hint')}</p>
      <div className="flex flex-wrap gap-3">
        <Button type="button" variant="outline" disabled={waiting} onClick={onReload}>
          {t('servers.draft.reload')}
        </Button>
        <Button type="button" disabled={waiting} onClick={onKeepMine}>
          {t('servers.draft.keep')}
        </Button>
      </div>
    </div>
  )
}
