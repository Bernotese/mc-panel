import { MoreHorizontal, Pin } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { BackupSnapshot } from '@/api/types'
import { DataList, type Column } from '@/components/data-list'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { formatBytes } from '@/features/files/format'
import { useFormat } from '@/lib/format'
import { backupDownloadUrl } from './api'
import { isSnapshotId, kindKey } from './model'

export interface SnapshotListProps {
  serverId: number
  snapshots: BackupSnapshot[]
  /** A job is queued or running: everything except the download is locked (the panel answers 409 anyway). */
  busy: boolean
  /** The snapshot whose pin request is in flight. */
  pendingId: string | null
  onPin?(s: BackupSnapshot, pinned: boolean): void
  onRestore?(s: BackupSnapshot): void
  onDelete?(s: BackupSnapshot): void
}

export function SnapshotList({ serverId, snapshots, busy, pendingId, onPin, onRestore, onDelete }: SnapshotListProps) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const columns: Column<BackupSnapshot>[] = [
    {
      id: 'time',
      header: t('backups.snapshots.col_time'),
      cell: (s) => (
        <span title={fmt.dateTime(s.time)}>
          <span className="block">{fmt.dateTime(s.time)}</span>
          <span className="text-muted-foreground block text-xs">{fmt.relative(s.time)}</span>
        </span>
      ),
    },
    {
      id: 'kind',
      header: t('backups.snapshots.col_kind'),
      cell: (s) => (
        <span className="flex flex-wrap items-center gap-1">
          <Badge variant={s.kind === 'scheduled' ? 'secondary' : 'outline'}>{t(kindKey(s.kind))}</Badge>
          {s.pinned ? (
            <Badge variant="default">
              <Pin aria-hidden="true" />
              {t('backups.snapshots.pinned')}
            </Badge>
          ) : null}
        </span>
      ),
    },
    { id: 'note', header: t('backups.snapshots.col_note'), cell: (s) => <span className="break-words">{s.note ?? ''}</span> },
    { id: 'size', header: t('backups.snapshots.col_size'), cell: (s) => formatBytes(s.bytes, fmt.locale), className: 'whitespace-nowrap' },
    {
      id: 'actions',
      header: t('backups.snapshots.col_actions'),
      className: 'text-right',
      cell: (s) => (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button type="button" variant="ghost" size="icon-sm" aria-label={t('backups.snapshots.actions_label', { when: fmt.dateTime(s.time) })}>
              <MoreHorizontal aria-hidden="true" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {onPin && s.kind === 'scheduled' ? (
              <DropdownMenuItem disabled={busy || pendingId === s.id} onSelect={() => onPin(s, !s.pinned)}>
                {s.pinned ? t('backups.snapshots.unpin') : t('backups.snapshots.pin')}
              </DropdownMenuItem>
            ) : null}
            {isSnapshotId(s.id) ? (
              <DropdownMenuItem asChild>
                <a href={backupDownloadUrl(serverId, s.id)} download>
                  {t('backups.snapshots.download')}
                </a>
              </DropdownMenuItem>
            ) : null}
            {onRestore ? (
              <DropdownMenuItem disabled={busy} onSelect={() => onRestore(s)}>
                {t('backups.snapshots.restore')}
              </DropdownMenuItem>
            ) : null}
            {onDelete ? (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" disabled={busy} onSelect={() => onDelete(s)}>
                  {t('backups.snapshots.delete')}
                </DropdownMenuItem>
              </>
            ) : null}
          </DropdownMenuContent>
        </DropdownMenu>
      ),
    },
  ]
  return (
    <DataList items={snapshots} columns={columns} rowKey={(s) => s.id} caption={t('backups.snapshots.caption')} empty={t('backups.snapshots.empty')} />
  )
}
