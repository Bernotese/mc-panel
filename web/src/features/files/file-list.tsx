import { File, FileText, Folder, Link2, MoreHorizontal } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { DataList, type Column } from '@/components/data-list'
import { FileName } from '@/components/file-name'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { visibleName } from '@/lib/file-name'
import { useFormat } from '@/lib/format'
import { downloadUrl } from './api'
import { EDITOR_MAX_BYTES, formatBytes, isZip } from './format'
import { joinPath } from './path'
import type { FileEntry } from '@/api/types'

export interface FileListProps {
  serverId: number
  dir: string
  entries: FileEntry[]
  onOpenDir(name: string): void
  onRename(entry: FileEntry): void
  onDelete(entry: FileEntry): void
  /** Task 15: opens text files in the editor. Without it, files are not clickable. */
  onOpenFile?(entry: FileEntry): void
  /** Task 16: offers "Extract …" for zip files. */
  onExtract?(entry: FileEntry): void
}

function editable(entry: FileEntry): boolean {
  return entry.type === 'file' && entry.size <= EDITOR_MAX_BYTES
}

export function FileList({ serverId, dir, entries, onOpenDir, onRename, onDelete, onOpenFile, onExtract }: FileListProps) {
  const { t } = useTranslation()
  const fmt = useFormat()

  function nameCell(entry: FileEntry) {
    if (entry.type === 'dir') {
      return (
        <Button type="button" variant="link" className="h-auto max-w-full justify-start p-0" onClick={() => onOpenDir(entry.name)}>
          <Folder aria-hidden="true" />
          <FileName name={entry.name} className="truncate" />
        </Button>
      )
    }
    if (onOpenFile && editable(entry)) {
      return (
        <Button type="button" variant="link" className="h-auto max-w-full justify-start p-0" onClick={() => onOpenFile(entry)}>
          <FileText aria-hidden="true" />
          <FileName name={entry.name} className="truncate" />
        </Button>
      )
    }
    const Icon = entry.type === 'symlink' ? Link2 : File
    return (
      <span className="flex min-w-0 items-center gap-1.5">
        <Icon aria-hidden="true" className="size-4 shrink-0" />
        <FileName name={entry.name} className="truncate" />
        {entry.type === 'symlink' || entry.type === 'other' ? <Badge variant="outline">{t(`files.type.${entry.type}`)}</Badge> : null}
      </span>
    )
  }

  function menu(entry: FileEntry) {
    const path = joinPath(dir, entry.name)
    const downloadable = entry.type === 'file' || entry.type === 'dir'
    return (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button type="button" variant="ghost" size="icon-sm" aria-label={t('files.actions_label', { name: visibleName(entry.name) })}>
            <MoreHorizontal aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {entry.type === 'dir' ? <DropdownMenuItem onSelect={() => onOpenDir(entry.name)}>{t('files.open')}</DropdownMenuItem> : null}
          {onOpenFile && editable(entry) ? <DropdownMenuItem onSelect={() => onOpenFile(entry)}>{t('files.edit')}</DropdownMenuItem> : null}
          {downloadable ? (
            <DropdownMenuItem asChild>
              <a href={downloadUrl(serverId, path)} download>
                {entry.type === 'dir' ? t('files.download_zip') : t('files.download')}
              </a>
            </DropdownMenuItem>
          ) : null}
          {onExtract && entry.type === 'file' && isZip(entry.name) ? (
            <DropdownMenuItem onSelect={() => onExtract(entry)}>{t('files.extract.action')}</DropdownMenuItem>
          ) : null}
          {entry.type !== 'other' ? <DropdownMenuItem onSelect={() => onRename(entry)}>{t('files.rename')}</DropdownMenuItem> : null}
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" onSelect={() => onDelete(entry)}>
            {t('files.delete')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    )
  }

  const columns: Column<FileEntry>[] = [
    { id: 'name', header: t('files.col.name'), cell: nameCell, className: 'max-w-0 w-full' },
    {
      id: 'size',
      header: t('files.col.size'),
      className: 'whitespace-nowrap text-right tabular-nums',
      cell: (e) => (e.type === 'file' ? formatBytes(e.size, fmt.locale) : '—'),
    },
    { id: 'mtime', header: t('files.col.modified'), className: 'whitespace-nowrap', cell: (e) => fmt.dateTime(e.mtime) },
    { id: 'actions', header: t('files.col.actions'), className: 'w-0 text-right', cell: menu },
  ]

  return (
    <DataList
      items={entries}
      columns={columns}
      rowKey={(e) => e.name}
      caption={t('files.caption', { dir: dir ? visibleName(dir) : t('files.root') })}
      empty={t('files.empty')}
    />
  )
}
