import { useInfiniteQuery } from '@tanstack/react-query'
import { ExternalLink } from 'lucide-react'
import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { PluginKind, PluginSearchHit, PluginSource } from '@/api/types'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useFormat } from '@/lib/format'
import { MAX_QUERY_LENGTH, SEARCH_DEBOUNCE_MS, searchQueryOptions } from './api'
import { pluginErrorMessage } from './messages'
import { readHideInstalled, writeHideInstalled } from './hide-installed'
import { externalHref, sourceHomeUrl, uniqueHits, type InstallTarget } from './model'
import { PluginIcon } from './plugin-icon'
import { useDebouncedValue } from './use-debounced-value'

/** Hits per page of GET …/plugins/search (V3). */
const PAGE_SIZE = 20
/**
 * With "Installierte ausblenden" a page can lose most of its hits: up to this many pages are loaded on their own per step
 * (first view, "Mehr laden", scrolling to the end) until a page's worth is visible – bounded for the 60/min limit (spec §6.6).
 */
const MAX_FILL_PAGES = 4

interface Step {
  key: string
  /** Visible hits this step aims for. */
  wanted: number
  /** Page count at which this step stops loading on its own. */
  maxPages: number
}

export interface DiscoverProps {
  serverId: number
  kind: PluginKind
  /** From the inventory: Modrinth and Hangar for plugins, only Modrinth for mods (spec §3.1). */
  sources: readonly PluginSource[]
  /** A job runs: "Installieren" is locked (spec §7.1). */
  busy: boolean
  onInstall(target: InstallTarget): void
}

export function Discover({ serverId, kind, sources, busy, onInstall }: DiscoverProps) {
  const { t } = useTranslation()
  const inputId = useId()
  const hideId = useId()
  const [chosen, setChosen] = useState<PluginSource>(sources[0] ?? 'modrinth')
  const source = sources.includes(chosen) ? chosen : (sources[0] ?? 'modrinth')
  const [text, setText] = useState('')
  const [hideInstalled, setHideInstalled] = useState(readHideInstalled)
  const q = useDebouncedValue(text.trim(), SEARCH_DEBOUNCE_MS)
  const search = useInfiniteQuery(searchQueryOptions(serverId, source, q))
  const hits = uniqueHits(search.data?.pages.flatMap((page) => page.hits) ?? [])
  const visible = hideInstalled ? hits.filter((h) => !h.installed) : hits
  const hiddenCount = hits.length - visible.length
  const pages = search.data?.pages.length ?? 0
  const sourceName = t(`plugins.source.${source}`)

  // A new search or source starts a new first step (state reset during render instead of an effect).
  const key = `${source}\u0000${q}`
  const [step, setStep] = useState<Step>({ key, wanted: PAGE_SIZE, maxPages: MAX_FILL_PAGES })
  if (step.key !== key) setStep({ key, wanted: PAGE_SIZE, maxPages: MAX_FILL_PAGES })

  const { fetchNextPage } = search
  const canLoad = search.isSuccess && search.hasNextPage && !search.isFetching && !search.isError
  const fill = canLoad && step.key === key && visible.length < step.wanted && pages < step.maxPages
  // `pages` re-runs the effect when a page arrives without `fill` ever turning false in between (batched updates).
  useEffect(() => {
    if (fill) void fetchNextPage()
  }, [fill, pages, fetchNextPage])

  // "Mehr laden" (button or scrolling to the end): another page's worth of visible hits.
  const more = useCallback(() => {
    if (!canLoad) return
    setStep({ key, wanted: visible.length + PAGE_SIZE, maxPages: pages + MAX_FILL_PAGES })
  }, [canLoad, key, visible.length, pages])
  const sentinel = useOnVisible(more, canLoad)

  function toggleHide(on: boolean) {
    setHideInstalled(on)
    writeHideInstalled(on)
  }

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
        {visible.length > 0 ? (
          <ul data-layout="grid" className="grid gap-3 sm:grid-cols-2 2xl:grid-cols-3">
            {visible.map((hit) => (
              <HitCard key={`${hit.source}:${hit.project_id}`} hit={hit} busy={busy} onInstall={onInstall} />
            ))}
          </ul>
        ) : search.isFetchingNextPage ? (
          <p className="text-muted-foreground text-sm">{t('common.loading')}</p>
        ) : (
          <p className="text-muted-foreground text-sm">{t('plugins.discover.all_installed')}</p>
        )}
        {hiddenCount > 0 ? (
          <p className="text-muted-foreground text-xs" aria-live="polite">
            {t('plugins.discover.hidden_count', { count: hiddenCount })}
          </p>
        ) : null}
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
          <div ref={sentinel}>
            <Button type="button" variant="outline" disabled={search.isFetching} onClick={more}>
              {search.isFetchingNextPage ? t('common.loading') : t('common.load_more')}
            </Button>
          </div>
        ) : null}
      </div>
    )
  }

  const home = sourceHomeUrl(source, kind)
  const body = (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <h3 className="pixel-heading text-xs uppercase">
          {q === '' ? t('plugins.discover.popular', { source: sourceName }) : t('plugins.discover.results', { source: sourceName })}
        </h3>
        {/* Issue #85: say clearly where the hits come from. */}
        <p className="bevel-inset bg-slot px-2 py-1 text-xs">
          {t('plugins.discover.results_from')}{' '}
          <a
            href={home}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={t('plugins.discover.source_page_label', { source: sourceName })}
            className="text-primary inline-flex items-center gap-1 font-medium underline-offset-4 hover:underline"
          >
            {sourceName}
            <ExternalLink aria-hidden="true" className="size-3" />
          </a>
        </p>
      </div>
      {results()}
    </div>
  )

  return (
    <div className="flex flex-col gap-4">
      {sources.length > 1 ? (
        <Tabs value={source} onValueChange={(value) => setChosen(value as PluginSource)}>
          <TabsList aria-label={t('plugins.discover.source')}>
            {sources.map((s) => (
              <TabsTrigger key={s} value={s}>
                {t(`plugins.source.${s}`)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      ) : null}
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
        <div className="flex h-9 items-center gap-2">
          <Switch id={hideId} checked={hideInstalled} onCheckedChange={toggleHide} />
          <Label htmlFor={hideId}>{t('plugins.discover.hide_installed')}</Label>
        </div>
      </div>
      {body}
    </div>
  )
}

/**
 * Calls onVisible when the returned element scrolls into view (infinite scroll); the button inside stays the fallback
 * for keyboards, tests (jsdom has no IntersectionObserver) and a list that is too short to scroll.
 */
function useOnVisible(onVisible: () => void, enabled: boolean) {
  const callback = useRef(onVisible)
  useEffect(() => {
    callback.current = onVisible
  })
  const [node, setNode] = useState<HTMLElement | null>(null)
  useEffect(() => {
    if (!node || !enabled || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) callback.current()
    }, { rootMargin: '200px' })
    observer.observe(node)
    return () => observer.disconnect()
  }, [node, enabled])
  return setNode
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
            <Badge variant="outline">{t(`plugins.source.${hit.source}`)}</Badge>
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
