import { useInfiniteQuery } from '@tanstack/react-query'
import { ExternalLink } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PluginSearchHit, PluginSource } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useFormat } from '@/lib/format'
import { MAX_QUERY_LENGTH, SEARCH_DEBOUNCE_MS, searchQueryOptions } from './api'
import { pluginErrorMessage } from './messages'
import { externalHref, uniqueHits, type InstallTarget } from './model'
import { PluginIcon } from './plugin-icon'
import { useDebouncedValue } from './use-debounced-value'

export interface DiscoverProps {
  serverId: number
  /** From the inventory: Modrinth and Hangar for plugins, only Modrinth for mods (spec §3.1). */
  sources: readonly PluginSource[]
  /** A job runs: "Installieren" is locked (spec §7.1). */
  busy: boolean
  onInstall(target: InstallTarget): void
}

export function Discover({ serverId, sources, busy, onInstall }: DiscoverProps) {
  const { t } = useTranslation()
  const inputId = useId()
  const [chosen, setChosen] = useState<PluginSource>(sources[0] ?? 'modrinth')
  const source = sources.includes(chosen) ? chosen : (sources[0] ?? 'modrinth')
  const [text, setText] = useState('')
  const q = useDebouncedValue(text.trim(), SEARCH_DEBOUNCE_MS)
  const search = useInfiniteQuery(searchQueryOptions(serverId, source, q))
  const hits = uniqueHits(search.data?.pages.flatMap((page) => page.hits) ?? [])
  const sourceName = t(`plugins.source.${source}`)

  function results() {
    if (search.isPending) return <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
    if (search.isError && hits.length === 0) {
      return (
        <div role="alert" className="border-destructive/60 space-y-2 border p-3 text-sm">
          <p className="font-medium">{t('plugins.discover.failed')}</p>
          <p className="text-muted-foreground">{pluginErrorMessage(search.error, t)}</p>
          <Button type="button" variant="outline" size="sm" disabled={search.isFetching} onClick={() => void search.refetch()}>
            {t('common.retry')}
          </Button>
        </div>
      )
    }
    if (hits.length === 0) return <p className="text-muted-foreground text-sm">{t('plugins.discover.empty')}</p>
    return (
      <div className="space-y-3">
        <ul data-layout="grid" className="grid gap-3 sm:grid-cols-2 2xl:grid-cols-3">
          {hits.map((hit) => (
            <HitCard key={`${hit.source}:${hit.project_id}`} hit={hit} busy={busy} onInstall={onInstall} />
          ))}
        </ul>
        {search.isRefetchError ? (
          // Spec §7.1: refreshing failed (e.g. after an install) – the hits below may be stale, so say so.
          <div role="alert" className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-destructive">{t('plugins.discover.failed')}</span>
            <span className="text-muted-foreground">{pluginErrorMessage(search.error, t)}</span>
            <Button type="button" variant="outline" size="sm" disabled={search.isFetching} onClick={() => void search.refetch()}>
              {t('common.retry')}
            </Button>
          </div>
        ) : null}
        {search.isFetchNextPageError ? (
          <div role="alert" className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-destructive">{pluginErrorMessage(search.error, t)}</span>
            <Button type="button" variant="outline" size="sm" onClick={() => void search.fetchNextPage()}>
              {t('common.retry')}
            </Button>
          </div>
        ) : null}
        {search.hasNextPage && !search.isFetchNextPageError ? (
          <Button type="button" variant="outline" disabled={search.isFetchingNextPage} onClick={() => void search.fetchNextPage()}>
            {t('common.load_more')}
          </Button>
        ) : null}
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
        <div className="min-w-0 flex-1 space-y-2">
          <Label htmlFor={inputId}>{t('plugins.discover.search')}</Label>
          <Input
            id={inputId}
            type="search"
            value={text}
            maxLength={MAX_QUERY_LENGTH}
            placeholder={t('plugins.discover.search_placeholder')}
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setText(e.target.value)}
          />
        </div>
        {sources.length > 1 ? (
          <div role="group" aria-label={t('plugins.discover.source')} className="flex gap-1">
            {sources.map((s) => (
              <Button key={s} type="button" size="sm" variant={s === source ? 'secondary' : 'outline'} aria-pressed={s === source} onClick={() => setChosen(s)}>
                {t(`plugins.source.${s}`)}
              </Button>
            ))}
          </div>
        ) : null}
      </div>
      <h3 className="pixel-heading text-xs uppercase">
        {q === '' ? t('plugins.discover.popular', { source: sourceName }) : t('plugins.discover.results', { source: sourceName })}
      </h3>
      {results()}
    </div>
  )
}

function HitCard({ hit, busy, onInstall }: { hit: PluginSearchHit; busy: boolean; onInstall(target: InstallTarget): void }) {
  const { t } = useTranslation()
  const fmt = useFormat()
  const page = externalHref(hit.page_url)
  return (
    <li className="bg-surface-2 flex min-w-0 flex-col gap-3 border p-3">
      <div className="flex min-w-0 items-start gap-3">
        <PluginIcon url={hit.icon_url} />
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <h4 className="font-medium break-words">{hit.title}</h4>
            {hit.installed ? <Badge variant="secondary">{t('plugins.discover.installed')}</Badge> : null}
          </div>
          <p className="text-muted-foreground text-xs">{t('plugins.discover.meta', { author: hit.author, downloads: fmt.number(hit.downloads) })}</p>
        </div>
      </div>
      {/* Spec §7.1: only the short description, as plain text – never Markdown or HTML from the upstream. */}
      <p className="line-clamp-3 text-sm break-words">{hit.description}</p>
      <div className="mt-auto flex flex-wrap items-center gap-3">
        {!hit.installed ? (
          <Button
            type="button"
            size="sm"
            disabled={busy}
            aria-label={t('plugins.discover.install_label', { title: hit.title })}
            onClick={() => onInstall({ source: hit.source, project: hit.project_id, title: hit.title })}
          >
            {t('plugins.discover.install')}
          </Button>
        ) : null}
        {page ? (
          <a
            href={page}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={t('plugins.discover.open_page_label', { title: hit.title })}
            className="text-primary inline-flex items-center gap-1 text-sm underline-offset-4 hover:underline"
          >
            {t('plugins.discover.open_page')}
            <ExternalLink aria-hidden="true" className="size-3.5" />
          </a>
        ) : null}
      </div>
    </li>
  )
}
