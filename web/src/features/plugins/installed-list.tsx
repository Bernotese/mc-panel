import { MoreHorizontal } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import type { InstalledJar, PluginInventory, PluginUpdate } from '@/api/types'
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
import { Switch } from '@/components/ui/switch'
import { externalHref, jarName, jarVersion } from './model'
import { PluginIcon } from './plugin-icon'

export interface InstalledListProps {
  inventory: PluginInventory
  /** Task 15: available updates by file name (badge and "Auf … aktualisieren"). */
  updates?: ReadonlyMap<string, PluginUpdate>
  /** Task 15: a job runs – switches and file-changing menu actions are locked (spec §7.1). */
  busy?: boolean
  /** The file whose toggle or removal is in flight. */
  pendingFile?: string | null
  onToggle(jar: InstalledJar, enabled: boolean): void
  onRemove(jar: InstalledJar): void
  /** Task 14: "Version wechseln …" opens the install dialog with the installed version preselected. */
  onChangeVersion?(jar: InstalledJar): void
  /** Task 15: single update. */
  onUpdate?(jar: InstalledJar, update: PluginUpdate): void
}

/** Table on desktop, cards on a phone (DataList), spec §7.1. */
export function InstalledList({ inventory, updates, busy = false, pendingFile = null, onToggle, onRemove, onChangeVersion, onUpdate }: InstalledListProps) {
  const { t } = useTranslation()

  function nameCell(jar: InstalledJar) {
    const name = jarName(jar)
    return (
      <div className="flex min-w-0 items-start gap-2">
        <PluginIcon url={jar.project?.icon_url} />
        <div className="min-w-0">
          <p className="font-medium break-words">{name}</p>
          {name !== jar.file ? <p className="text-muted-foreground font-mono text-xs break-all">{jar.file}</p> : null}
          {jar.source === 'manual' ? <p className="text-muted-foreground text-xs">{t('plugins.installed.unmatched')}</p> : null}
        </div>
      </div>
    )
  }

  function versionCell(jar: InstalledJar) {
    const available = updates?.get(jar.file)
    const channel = jar.version?.channel
    return (
      <div className="flex flex-wrap items-center gap-1">
        <span className="break-all">{jarVersion(jar) ?? '—'}</span>
        {channel && channel !== 'release' ? <Badge variant="outline">{t(`plugins.channel.${channel}`)}</Badge> : null}
        {available ? <Badge>{t('plugins.installed.update_badge', { version: available.to.name })}</Badge> : null}
      </div>
    )
  }

  function enabledCell(jar: InstalledJar) {
    return (
      <Switch
        checked={jar.enabled}
        disabled={busy || pendingFile === jar.file}
        aria-label={t('plugins.installed.enabled_label', { name: jarName(jar) })}
        onCheckedChange={(on) => onToggle(jar, on)}
      />
    )
  }

  function menu(jar: InstalledJar) {
    const name = jarName(jar)
    const page = externalHref(jar.project?.page_url)
    const available = updates?.get(jar.file)
    return (
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button type="button" variant="ghost" size="icon-sm" aria-label={t('plugins.installed.actions_label', { name })}>
            <MoreHorizontal aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {page ? (
            <DropdownMenuItem asChild>
              <a href={page} target="_blank" rel="noopener noreferrer">
                {t('plugins.installed.open_page')}
              </a>
            </DropdownMenuItem>
          ) : null}
          {onUpdate && available ? (
            <DropdownMenuItem disabled={busy} onSelect={() => onUpdate(jar, available)}>
              {t('plugins.installed.update_to', { version: available.to.name })}
            </DropdownMenuItem>
          ) : null}
          {onChangeVersion && jar.source !== 'manual' && jar.project ? (
            <DropdownMenuItem disabled={busy} onSelect={() => onChangeVersion(jar)}>
              {t('plugins.installed.change_version')}
            </DropdownMenuItem>
          ) : null}
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" disabled={busy || pendingFile === jar.file} onSelect={() => onRemove(jar)}>
            {t('plugins.installed.remove')}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    )
  }

  const columns: Column<InstalledJar>[] = [
    { id: 'name', header: t('plugins.installed.col.name'), cell: nameCell, className: 'max-w-0 w-full' },
    { id: 'version', header: t('plugins.installed.col.version'), cell: versionCell, className: 'whitespace-nowrap' },
    {
      id: 'source',
      header: t('plugins.installed.col.source'),
      cell: (jar) => <Badge variant="outline">{t(`plugins.source.${jar.source}`)}</Badge>,
    },
    { id: 'enabled', header: t('plugins.installed.col.enabled'), cell: enabledCell, className: 'w-0' },
    { id: 'actions', header: t('plugins.installed.col.actions'), cell: menu, className: 'w-0 text-right' },
  ]

  return (
    <DataList
      items={inventory.jars}
      columns={columns}
      rowKey={(jar) => jar.file}
      caption={t('plugins.installed.caption', { dir: inventory.dir })}
      empty={t('plugins.installed.empty', { dir: inventory.dir })}
    />
  )
}
